package main

import (
	"errors"
	"log"
	"math"
	"net/http"
	"time"

	"github.com/spartanoscar/vigiax/server/internal/alert"
	"github.com/spartanoscar/vigiax/server/internal/station"
)

// A normal-day frame per station, for Muse Spark to compare events against.
// It is taken by itself from a quiet frame (normal level, clear view, steady
// water) and renewed weekly, as vegetation and the channel change with the
// seasons; it can also be set by hand from the dashboard.

const referenceMaxAge = 7 * 24 * time.Hour

// maybeReference stores jpeg as the reference when the station has none (or an
// old one) and this reading looks like a normal day.
func (s *server) maybeReference(st *station.Station, rd station.Reading, jpeg []byte) {
	if jpeg == nil || rd.Blind || rd.LevelCm == nil || rd.Confidence < 0.8 || rd.AtTop {
		return
	}
	cfg := st.Config()
	if _, ref, ok := st.GetReference(); ok && rd.T.Sub(ref.T) < referenceMaxAge {
		return
	}
	a := s.alerts(cfg.ID)
	a.mu.Lock()
	quiet := a.state.Level == alert.Normal && !a.state.Moved && !a.state.Blind
	a.mu.Unlock()
	if v, _, ok := st.Rise(rd.T, 5*time.Minute); ok && math.Abs(v) > 0.5 {
		quiet = false
	}
	if !quiet {
		return
	}
	if err := st.SetReference(jpeg, station.Reference{T: rd.T, LevelCm: rd.LevelCm, By: "auto"}); err != nil {
		log.Printf("%s: reference: %v", cfg.ID, err)
		return
	}
	log.Printf("%s: normal-day reference taken (%.0f cm)", cfg.ID, *rd.LevelCm)
}

// GET /api/stations/{id}/reference.jpg: the normal-day frame.
// POST /api/stations/{id}/reference: use the latest frame as the normal-day frame.
func (s *server) handleReference(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	st, ok := s.stations[r.PathValue("id")]
	s.mu.Unlock()
	if !ok {
		httpError(w, http.StatusNotFound, errors.New("no such station"))
		return
	}
	if r.Method == http.MethodGet {
		jpeg, _, ok := st.GetReference()
		if !ok {
			httpError(w, http.StatusNotFound, errors.New("no normal-day frame yet"))
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		w.Header().Set("Cache-Control", "no-cache")
		w.Write(jpeg)
		return
	}
	if !s.editable(r, st.Config()) {
		httpError(w, http.StatusForbidden, errors.New("the real stations are read-only; enter a judge's code"))
		return
	}
	s.mu.Lock()
	f, ok := s.frames[st.Config().ID]
	s.mu.Unlock()
	if !ok || time.Since(f.at) > frameFresh {
		httpError(w, http.StatusConflict, errors.New("no recent frame: start the camera node first"))
		return
	}
	last, _ := st.Last()
	if err := st.SetReference(f.jpeg, station.Reference{T: f.at, LevelCm: last.LevelCm, By: "manual"}); err != nil {
		httpError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, map[string]any{"station": st.Config().ID, "t": f.at})
}
