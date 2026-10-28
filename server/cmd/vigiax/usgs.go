package main

import (
	"context"
	"log"
	"time"

	"github.com/spartanoscar/vigiax/server/internal/station"
	"github.com/spartanoscar/vigiax/server/internal/usgs"
)

// liveStations are real cameras the service follows on its own. Their rating
// comes from eval/results/el-reno-2026-10: a 3.66 m flood in October 2026,
// test MAE 6.9 cm against the USGS gage.
var liveStations = []station.Config{{
	ID:     "el-reno",
	Name:   "North Canadian River near El Reno, OK (USGS)",
	Prompt: station.DefaultPrompt,
	Rating: &station.Rating{Intercept: -5.15, Slope: 41.07, Unit: "FT", Fit: "eval/results/el-reno-2026-10"},
	Source: &station.Source{Kind: "usgs", Camera: "OK_North_Canadian_River_near_El_Reno", Site: "07239500"},
}}

const (
	usgsPoll     = 10 * time.Minute // the cameras publish about once an hour
	usgsBackfill = 12 * time.Hour   // frames measured on a fresh start
	ft2cm        = 30.48
)

func (s *server) startLiveStations() error {
	for _, cfg := range liveStations {
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
	gage, err := usgs.GageHeights(ctx, cfg.Source.Site, since.Add(-time.Hour), now)
	if err != nil {
		log.Printf("%s: gage: %v", cfg.ID, err)
	}
	for _, f := range files {
		t, err := usgs.FrameTime(f)
		if err != nil || !t.After(since) {
			continue
		}
		if err := s.budget.allow(now, "live:"+cfg.ID); err != nil {
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
		s.budget.spend(now, "live:"+cfg.ID, rd.CostUSD)
		if err != nil {
			return err
		}
		s.hub.publish("reading", summarize(st, time.Now()))
		log.Printf("%s: %s water %.1f %% conf %.2f blind %v", cfg.ID, t.Format(time.RFC3339), rd.Coverage*100, rd.Confidence, rd.Blind)
	}
	return nil
}
