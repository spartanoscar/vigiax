package main

import (
	"context"
	"log"
	"time"

	"github.com/spartanoscar/vigiax/server/internal/station"
	"github.com/spartanoscar/vigiax/server/internal/usgs"
)

const (
	usgsPoll     = 10 * time.Minute // the cameras publish about once an hour
	usgsBackfill = 12 * time.Hour   // frames measured on a fresh start
	ft2cm        = 30.48
)

func (s *server) startLiveStations() error {
	for _, cfg := range station.Live {
		st, err := s.station(cfg.ID)
		if err != nil {
			return err
		}
		if err := st.Update(func(c *station.Config) {
			c.Name, c.Prompt, c.Calib, c.Rating, c.Source = cfg.Name, cfg.Prompt, nil, cfg.Rating, cfg.Source
		}); err != nil {
			return err
		}
		go s.followUSGS(st)
	}
	return nil
}

func (s *server) followUSGS(st *station.Station) {
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		if err := s.pollUSGS(ctx, st); err != nil {
			log.Printf("%s: %v", st.Config().ID, err)
		}
		cancel()
		time.Sleep(usgsPoll)
	}
}

// pollUSGS measures the frames published since the last reading, oldest first.
func (s *server) pollUSGS(ctx context.Context, st *station.Station) error {
	cfg := st.Config()
	now := time.Now()
	since := now.Add(-usgsBackfill)
	if r, ok := st.Last(); ok && r.T.After(since) {
		since = r.T
	}
	files, err := usgs.Files(ctx, cfg.Source.Camera, since, now)
	if err != nil {
		return err
	}
	// The gage reference is a bonus: the frames are measured without it.
	gage, err := usgs.GageHeights(ctx, cfg.Source.Site, now.Add(-usgsBackfill-time.Hour), now)
	if err != nil {
		log.Printf("%s: gage: %v", cfg.ID, err)
	}
	// Gages publish up to an hour after the camera: complete earlier readings.
	for _, r := range st.Readings(now.Add(-usgsBackfill)) {
		if r.RefCm != nil {
			continue
		}
		if g, ok := usgs.Nearest(gage, r.T, 30*time.Minute); ok {
			if _, err := st.SetRef(r.T, g*ft2cm); err != nil {
				return err
			}
		}
	}
	for _, f := range files {
		t, err := usgs.FrameTime(f)
		if err != nil || !t.After(since) {
			continue
		}
		if err := s.live.allow(now, cfg.ID, s.live.perIP); err != nil {
			return err
		}
		u := usgs.FrameURL(cfg.Source.Camera, f)
		img, err := usgs.Get(ctx, u)
		if err != nil {
			return err
		}
		rd := station.Reading{T: t, FrameURL: u}
		if g, ok := usgs.Nearest(gage, t, 30*time.Minute); ok {
			cm := g * ft2cm
			rd.RefCm = &cm
		}
		rd, err = st.MeasureFrame(ctx, s.meta, img, rd)
		s.live.spend(now, cfg.ID, rd.CostUSD)
		if err != nil {
			return err
		}
		s.hub.publish("reading", summarize(st, time.Now()))
		s.checkAlerts(st, rd)
		s.maybeReference(st, rd, img)
		log.Printf("%s: %s water %.1f %% conf %.2f blind %v", cfg.ID, t.Format(time.RFC3339), rd.Coverage*100, rd.Confidence, rd.Blind)
	}
	return nil
}
