package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spartanoscar/vigiax/server/internal/alert"
	"github.com/spartanoscar/vigiax/server/internal/station"
)

// Low connectivity. A camera node on a weak link sends less, and with no link
// at all it keeps measuring by itself:
//
//   - 2G: only the gauge and what is around it, a few kB instead of the whole
//     picture (POST .../frames?crop=x,y,w,h); SAM 3.1 still measures it.
//   - No signal: the node reads the gauge from its own pixels, sounds its local
//     siren, and keeps each reading as a 10-byte packet until it has a link.
//   - LoRa: the same 10-byte packets, one at a time, over the radio.
//
//	POST /api/stations/{id}/node   body: one or more 10-byte packets
//
// A packet, big-endian: unix seconds (4) · level in mm (2, 0xFFFF = could not
// see) · confidence × 255 (1) · flags (1, bit 0: local siren on) · sequence (2).

const (
	packetSize   = 10
	maxPackets   = 1440 // two hours of readings every 5 s
	packetBlind  = 0xFFFF
	nodeMaxAge   = 24 * time.Hour  // older packets are not news
	nodeMaxAhead = 2 * time.Minute // a node clock running ahead
)

type packet struct {
	T          time.Time
	LevelCm    *float64
	Confidence float64
	Siren      bool
}

func decodePackets(b []byte, now time.Time) ([]packet, error) {
	if len(b) == 0 || len(b)%packetSize != 0 {
		return nil, fmt.Errorf("send %d-byte packets", packetSize)
	}
	if len(b)/packetSize > maxPackets {
		return nil, fmt.Errorf("at most %d packets at once", maxPackets)
	}
	var ps []packet
	for i := 0; i < len(b); i += packetSize {
		p := b[i : i+packetSize]
		t := time.Unix(int64(binary.BigEndian.Uint32(p[0:4])), 0).UTC()
		if t.Before(now.Add(-nodeMaxAge)) || t.After(now.Add(nodeMaxAhead)) {
			continue
		}
		k := packet{T: t, Confidence: float64(p[6]) / 255, Siren: p[7]&1 != 0}
		if mm := binary.BigEndian.Uint16(p[4:6]); mm != packetBlind {
			cm := float64(mm) / 10
			k.LevelCm = &cm
		}
		ps = append(ps, k)
	}
	sort.Slice(ps, func(i, j int) bool { return ps[i].T.Before(ps[j].T) })
	return ps, nil
}

func (s *server) postNode(w http.ResponseWriter, r *http.Request) {
	st, err := s.station(r.PathValue("id"))
	if err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	if st.Config().Source != nil {
		httpError(w, http.StatusForbidden, errors.New("this is a live station: the service measures its own camera"))
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxPackets*packetSize))
	if err != nil {
		httpError(w, http.StatusBadRequest, fmt.Errorf("at most %d packets at once", maxPackets))
		return
	}
	now := time.Now()
	ps, err := decodePackets(body, now)
	if err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	// Readings kept while offline all arrive at once: each one steps the alert
	// state (the history keeps when each level was reached), but people are
	// told once, the last change: three messages in a row would bury the one
	// that matters now.
	added := 0
	var last *alert.Event
	var lastCfg *alert.Config
	for _, p := range ps {
		rd := station.Reading{T: p.T, LevelCm: p.LevelCm, Confidence: p.Confidence, Blind: p.LevelCm == nil}
		ok, err := st.AddNode(rd)
		if err != nil {
			httpError(w, http.StatusInternalServerError, err)
			return
		}
		if ok {
			added++
			if e, c := s.stepAlerts(st, rd); e != nil {
				last, lastCfg = e, c
			}
		}
	}
	if last != nil {
		s.announce(st.Config().ID, lastCfg, *last)
	}
	sum := summarize(st, now)
	if added > 0 {
		s.hub.publish("reading", sum)
	}
	writeJSON(w, map[string]any{"added": added, "station": sum})
}

// postRestart begins a new run of a twin station: the page reloaded and its
// water is back at the start. The alert level is normal again without telling
// anyone, and the readings of the last run no longer count for jumps or rises.
//
//	POST /api/stations/{id}/restart
func (s *server) postRestart(w http.ResponseWriter, r *http.Request) {
	st, err := s.station(r.PathValue("id"))
	if err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	cfg := st.Config()
	if cfg.Source != nil {
		httpError(w, http.StatusForbidden, errors.New("only a twin can start over"))
		return
	}
	now := time.Now().UTC()
	a := s.alerts(cfg.ID)
	a.mu.Lock()
	e := a.state.Restart(now)
	a.record(e)
	a.restart = now
	a.mu.Unlock()
	s.mu.Lock()
	delete(s.moved, cfg.ID)
	s.mu.Unlock()
	c := alertConfig(cfg)
	s.hub.publish("alert", alertEvent{Station: cfg.ID, Event: e, Rule: c.Rule(e.To), Message: messages(&c, e)})
	writeJSON(w, map[string]string{"level": e.To.String()})
}

// parseCrop reads ?crop=x,y,w,h: where a frame sits in the node's whole picture.
func parseCrop(q string) (*station.Crop, error) {
	if q == "" {
		return nil, nil
	}
	f := strings.Split(q, ",")
	if len(f) != 4 {
		return nil, errors.New("crop is x,y,w,h")
	}
	var v [4]int
	for i, s := range f {
		n, err := strconv.Atoi(strings.TrimSpace(s))
		if err != nil || n < 0 || n > 10000 {
			return nil, errors.New("crop is x,y,w,h in pixels")
		}
		v[i] = n
	}
	return &station.Crop{X: v[0], Y: v[1], W: v[2], H: v[3]}, nil
}
