package main

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/spartanoscar/vigiax/server/internal/gpu"
)

// The live SAM view: the dashboard sends the camera view a few times a second
// and gets back what SAM 3.1 finds on it (water, people, logs), drawn over the
// picture as it moves. It runs on a rented GPU when one is linked, never on the
// Meta API budget, and it measures nothing: the level still comes from the
// frames a station measures.
//
//	GET  /api/gpu                          is the GPU there: off, starting, installing, loading, ready
//	POST /api/live/segment?prompts=a,b     JPEG body → objects with boxes and masks

const (
	maxLiveFrame   = 400 << 10 // a 640×360 JPEG is about 40 KB
	maxLivePrompts = 6
	maxLiveBusy    = 3 // frames at the GPU at once, from everyone
)

var livePrompt = regexp.MustCompile(`^[a-z][a-z ]{1,39}$`)

type liveGate struct {
	mu   sync.Mutex
	busy map[string]bool // one frame at a time per visitor
	n    int
}

// enter lets a frame through when the visitor has none at the GPU and the GPU
// is not full; a frame turned away is simply skipped by the dashboard.
func (g *liveGate) enter(ip string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.busy[ip] || g.n >= maxLiveBusy {
		return false
	}
	g.busy[ip] = true
	g.n++
	return true
}

func (g *liveGate) leave(ip string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.busy, ip)
	g.n--
}

func (s *server) getGPU(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.gpu.Health(r.Context()))
}

func (s *server) postLiveSegment(w http.ResponseWriter, r *http.Request) {
	var prompts []string
	for _, p := range strings.Split(r.URL.Query().Get("prompts"), ",") {
		p = strings.ToLower(strings.TrimSpace(p))
		if !livePrompt.MatchString(p) {
			httpError(w, http.StatusBadRequest, fmt.Errorf("prompts are short English phrases: %q", p))
			return
		}
		prompts = append(prompts, p)
	}
	if len(prompts) > maxLivePrompts {
		httpError(w, http.StatusBadRequest, fmt.Errorf("at most %d prompts", maxLivePrompts))
		return
	}
	img, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxLiveFrame))
	if err != nil || len(img) == 0 {
		httpError(w, http.StatusBadRequest, fmt.Errorf("send one JPEG frame of at most %d KB", maxLiveFrame>>10))
		return
	}
	ip := clientIP(r)
	if !s.liveGate.enter(ip) {
		httpError(w, http.StatusTooManyRequests, errors.New("the GPU is busy with your previous frame"))
		return
	}
	defer s.liveGate.leave(ip)
	t0 := time.Now()
	res, err := s.gpu.Segment(r.Context(), img, prompts, 0.4)
	if errors.Is(err, gpu.ErrOff) {
		httpError(w, http.StatusServiceUnavailable, errors.New("no GPU right now: the view shows the last measured frame"))
		return
	}
	if err != nil {
		httpError(w, http.StatusBadGateway, err)
		return
	}
	for i := range res.Objects {
		if m := res.Objects[i].Mask; m != "" {
			res.Objects[i].Mask = "data:image/png;base64," + m
		}
	}
	writeJSON(w, map[string]any{"result": res, "round_trip_ms": time.Since(t0).Milliseconds()})
}
