// Package station keeps one camera's configuration, calibration and level
// series. Each frame becomes one Reading from SAM 3.1; no language model runs
// on this path.
package station

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"sync"
	"time"

	"github.com/spartanoscar/vigiax/server/internal/gauge"
	"github.com/spartanoscar/vigiax/server/internal/meta"
	"github.com/spartanoscar/vigiax/server/internal/sam"
)

// DefaultPrompt finds clear and muddy water alike ("water" misses muddy floods).
const DefaultPrompt = "flood water"

// keep is how many readings stay in memory: 4 hours at one frame every 5 s.
const keep = 2880

type Reading struct {
	T          time.Time `json:"t"`
	LevelCm    *float64  `json:"level_cm,omitempty"`    // nil without calibration or water at the gauge
	WaterlineY *int      `json:"waterline_y,omitempty"` // frame row of the water surface at the gauge
	Coverage   float64   `json:"coverage"`              // water area / frame area
	Confidence float64   `json:"confidence"`
	SamMs      int64     `json:"sam_ms"`
	CostUSD    float64   `json:"cost_usd"`
}

type Config struct {
	ID     string             `json:"id"`
	Name   string             `json:"name"`
	Prompt string             `json:"prompt"`
	Calib  *gauge.Calibration `json:"calibration,omitempty"`
}

type Station struct {
	mu       sync.Mutex
	cfg      Config
	readings []Reading
	dir      string
}

var validID = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)

// Open loads a station from dir (config and readings), or starts a new one.
func Open(dir, id string) (*Station, error) {
	if !validID.MatchString(id) {
		return nil, fmt.Errorf("station id %q: use 1-40 lowercase letters, digits or dashes", id)
	}
	s := &Station{dir: dir, cfg: Config{ID: id, Name: id, Prompt: DefaultPrompt}}
	if b, err := os.ReadFile(s.path(".json")); err == nil {
		if err := json.Unmarshal(b, &s.cfg); err != nil {
			return nil, fmt.Errorf("station %s: %w", id, err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	f, err := os.Open(s.path(".jsonl"))
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	} else if err != nil {
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var r Reading
		if json.Unmarshal(sc.Bytes(), &r) == nil {
			s.readings = append(s.readings, r)
		}
	}
	if len(s.readings) > keep {
		s.readings = s.readings[len(s.readings)-keep:]
	}
	return s, sc.Err()
}

func (s *Station) path(ext string) string { return filepath.Join(s.dir, s.cfg.ID+ext) }

func (s *Station) Config() Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg
}

// SetCalibration stores a new calibration. Readings already taken keep their level.
func (s *Station) SetCalibration(c *gauge.Calibration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cfg.Calib = c
	b, _ := json.MarshalIndent(s.cfg, "", "  ")
	return os.WriteFile(s.path(".json"), b, 0o644)
}

// Measure segments one JPEG frame and appends the reading.
func (s *Station) Measure(ctx context.Context, c *meta.Client, jpeg []byte, now time.Time) (Reading, error) {
	cfg := s.Config()
	r, err := sam.Segment(ctx, c, jpeg, cfg.Prompt)
	if err != nil {
		return Reading{}, err
	}
	rd := Reading{T: now.UTC(), SamMs: r.Latency.Milliseconds(), CostUSD: r.CostUSD}
	if r.FrameW > 0 {
		area := 0.0
		for i := range r.Objects {
			area += r.Objects[i].Area()
			rd.Confidence = max(rd.Confidence, r.Objects[i].Confidence)
		}
		rd.Coverage = area / float64(r.FrameW*r.FrameH)
		x0, x1 := 0, r.FrameW-1
		if cfg.Calib != nil {
			x0, x1 = cfg.Calib.ColX1, cfg.Calib.ColX2
		}
		if y, ok := r.Waterline(x0, x1); ok {
			rd.WaterlineY = &y
			if cfg.Calib != nil {
				lv := cfg.Calib.LevelCm(float64(y))
				rd.LevelCm = &lv
			}
		}
	}
	return rd, s.append(rd)
}

func (s *Station) append(r Reading) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.readings = append(s.readings, r)
	if len(s.readings) > keep {
		s.readings = s.readings[len(s.readings)-keep:]
	}
	f, err := os.OpenFile(s.path(".jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	b, _ := json.Marshal(r)
	_, err = f.Write(append(b, '\n'))
	return err
}

// Readings returns the readings taken after since, oldest first.
func (s *Station) Readings(since time.Time) []Reading {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := sort.Search(len(s.readings), func(i int) bool { return s.readings[i].T.After(since) })
	return append([]Reading(nil), s.readings[i:]...)
}

// Last returns the latest reading, if any.
func (s *Station) Last() (Reading, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.readings) == 0 {
		return Reading{}, false
	}
	return s.readings[len(s.readings)-1], true
}

// Rise is the least-squares slope of the level over the readings in
// (now-window, now], in cm per minute. ok is false with fewer than three
// calibrated readings or less than a third of the window covered.
func (s *Station) Rise(now time.Time, window time.Duration) (cmPerMin float64, n int, ok bool) {
	rs := s.Readings(now.Add(-window))
	var t0 time.Time
	var span, st, sl, stt, stl float64
	for _, r := range rs {
		if r.LevelCm == nil || r.T.After(now) {
			continue
		}
		if n == 0 {
			t0 = r.T
		}
		t := r.T.Sub(t0).Minutes()
		span = t
		st += t
		sl += *r.LevelCm
		stt += t * t
		stl += t * *r.LevelCm
		n++
	}
	fn := float64(n)
	den := fn*stt - st*st
	if n < 3 || den == 0 || span < window.Minutes()/3 {
		return 0, n, false
	}
	return (fn*stl - st*sl) / den, n, true
}
