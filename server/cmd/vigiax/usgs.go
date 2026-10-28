package main

import (
	"context"
	"log"
	"time"

	"github.com/spartanoscar/vigiax/server/internal/alert"
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
	Alerts: &alert.Config{
		Place: "el río North Canadian en El Reno, Oklahoma", PlaceEn: "the North Canadian River at El Reno, Oklahoma",
		Confirm: 1, RiseWindowMin: 180, LowerAfterMin: 120, // one photo an hour
		Note: "Demo lines between today's level (about 1.2 m) and the October 2026 peak (4.6 m); not official flood stages.",
		Rules: alert.Rules{
			Attention: alert.Rule{Enabled: true, LevelCm: ptr(250), RiseCmPerMin: ptr(0.5), Audience: "leader", Channels: []string{"push"},
				Why:    "The river rises 30 cm an hour or passes 2.5 m: someone should watch it.",
				Action: "Vigila el río y prepárate por si sigue subiendo.", ActionEn: "Watch the river and get ready in case it keeps rising."},
			Alert: alert.Rule{Enabled: true, LevelCm: ptr(350), RiseCmPerMin: ptr(1), Audience: "group", Channels: []string{"push", "whatsapp"},
				Why:    "The river rises 60 cm an hour or passes 3.5 m: keep away from the banks.",
				Action: "Aléjate de la orilla y de los cruces bajos.", ActionEn: "Keep away from the banks and low crossings."},
			Evacuate: alert.Rule{Enabled: true, LevelCm: ptr(450), RiseCmPerMin: ptr(2), Audience: "everyone", Channels: []string{"push", "whatsapp", "siren"},
				Why:    "The river is near the October 2026 peak: leave the low areas.",
				Action: "Sal de las zonas bajas ahora.", ActionEn: "Leave the low areas now."},
		},
	},
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
		log.Printf("%s: %s water %.1f %% conf %.2f blind %v", cfg.ID, t.Format(time.RFC3339), rd.Coverage*100, rd.Confidence, rd.Blind)
	}
	return nil
}
