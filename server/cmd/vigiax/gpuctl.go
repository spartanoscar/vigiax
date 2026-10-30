package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Turning the live view's GPU on and off. The GPU is rented by the minute, so
// only a judge or the admin can turn it on, and it turns itself off when nobody
// watches. The renting is done by an outside command (GPU_CTL) shared with
// other services, which keeps the account's limits (one machine at a time,
// minutes per session, a daily budget) and the one GPU they all use:
//
//	up --no-wait  rent it (or join the one running); its last line is the json below
//	json          {"pod", "stage", "minutes_left", "url", ...}, stage "off" without one
//	token         the API token of the running one
//	touch NAME    NAME used it just now
//	reap SECONDS  turn it off when no service used it for that long
//	down          turn it off
//
// Our routes:
//
//	GET  /api/gpu   {stage, minutes_left, can_control, starting_s, note}; ?beat=1 also says "someone is watching"
//	POST /api/gpu   {"on": true|false}, judge or admin only

const (
	gpuIdleSeconds = "180"             // nobody watching for this long, from any service: off
	gpuCmdWait     = 150 * time.Second // RunPod answers in seconds; the command may wait 120 s for its lock
)

type gpuControl struct {
	path string // the command; empty = the service cannot turn a GPU on

	mu      sync.Mutex
	asked   time.Time // when "on" was asked, until the GPU is ready
	note    string    // why the last "on" failed, in words for the page
	touched time.Time // last time we told the command someone watches
	synced  time.Time // last time we asked the command which GPU is running
}

type gpuState struct {
	Pod         *string `json:"pod"`
	Stage       string  `json:"stage"`
	MinutesLeft float64 `json:"minutes_left"`
	URL         string  `json:"url"`
}

func newGPUControl(path string) *gpuControl { return &gpuControl{path: path} }

func (g *gpuControl) run(ctx context.Context, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, gpuCmdWait)
	defer cancel()
	cmd := exec.CommandContext(ctx, g.path, args...)
	cmd.Env = os.Environ()
	// The command's own names for its limits, from ours.
	for _, kv := range [][2]string{{"GPU_MAX_MINUTES", "MAX_MINUTES"}, {"GPU_DAILY_BUDGET_USD", "DAILY_BUDGET_USD"}, {"GPU_VOLUME", "NETWORK_VOLUME_ID"}} {
		if v := os.Getenv(kv[0]); v != "" {
			cmd.Env = append(cmd.Env, kv[1]+"="+v)
		}
	}
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// gpuSync links the client to the GPU the command has running, if any (turned
// on here, by another service sharing it, or before a restart), or unlinks it.
func (s *server) gpuSync(ctx context.Context) {
	g := s.gpuCtl
	g.mu.Lock()
	g.synced = time.Now()
	g.mu.Unlock()
	out, err := g.run(ctx, "json")
	var st gpuState
	if err != nil || json.Unmarshal([]byte(lastLine(out)), &st) != nil {
		log.Printf("gpu: json: %v %.200s", err, out)
		return
	}
	if st.Pod == nil || st.URL == "" {
		s.gpu.Link("", "")
		return
	}
	tok, err := g.run(ctx, "token")
	if err != nil {
		return
	}
	s.gpu.Link(st.URL, tok)
}

func lastLine(s string) string {
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		return s[i+1:]
	}
	return s
}

// touch tells the command a page here uses the live view, at most every 10 s.
func (g *gpuControl) touch(now time.Time) {
	if g.path == "" {
		return
	}
	g.mu.Lock()
	due := now.Sub(g.touched) > 10*time.Second
	if due {
		g.touched = now
	}
	g.mu.Unlock()
	if due {
		go g.run(context.Background(), "touch", "vigiax")
	}
}

// gpuWatch asks the command to turn the GPU off when no service used it for a
// while, and keeps the link in step with a GPU turned on or off elsewhere.
func (s *server) gpuWatch() {
	g := s.gpuCtl
	if g.path == "" {
		return
	}
	s.gpuSync(context.Background())
	for range time.Tick(30 * time.Second) {
		if !s.gpu.Linked() {
			continue
		}
		if out, err := g.run(context.Background(), "reap", gpuIdleSeconds); err != nil {
			log.Printf("gpu: reap: %v %.200s", err, out)
		}
		s.gpuSync(context.Background())
	}
}

func (s *server) handleGPU(w http.ResponseWriter, r *http.Request) {
	g := s.gpuCtl
	now := time.Now()
	role := s.access.role(r)
	canControl := g.path != "" && (role == roleJudge || role == roleAdmin)
	if r.Method == http.MethodPost {
		if !canControl {
			httpError(w, http.StatusForbidden, errors.New("only a judge's code can turn the GPU on"))
			return
		}
		var req struct {
			On bool `json:"on"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&req); err != nil {
			httpError(w, http.StatusBadRequest, err)
			return
		}
		if req.On {
			s.gpuOn(r.Context(), now)
		} else {
			if out, err := g.run(r.Context(), "down"); err != nil {
				log.Printf("gpu: down: %v %.200s", err, out)
			}
			s.gpu.Link("", "")
			g.mu.Lock()
			g.asked, g.note = time.Time{}, ""
			g.mu.Unlock()
		}
	} else if r.URL.Query().Get("beat") == "1" {
		g.touch(now)
	}
	// A GPU turned on or off elsewhere (another service, a restart): follow it.
	g.mu.Lock()
	sync := g.path != "" && now.Sub(g.synced) > 15*time.Second
	g.mu.Unlock()
	if sync {
		s.gpuSync(r.Context())
	}

	h := s.gpu.Health(r.Context())
	g.mu.Lock()
	defer g.mu.Unlock()
	out := map[string]any{"stage": h.Stage, "minutes_left": h.MinutesLeft, "can_control": canControl, "note": g.note}
	if h.Stage == "ready" || h.Stage == "off" && !s.gpu.Linked() && now.Sub(g.asked) > time.Minute {
		g.asked = time.Time{}
	}
	if !g.asked.IsZero() {
		out["starting_s"] = int(now.Sub(g.asked).Seconds())
		if h.Stage == "off" {
			out["stage"] = "starting"
		}
	}
	writeJSON(w, out)
}

// gpuOn rents the GPU, or joins the one already running.
func (s *server) gpuOn(ctx context.Context, now time.Time) {
	g := s.gpuCtl
	g.touch(now)
	out, err := g.run(ctx, "up", "--no-wait")
	g.mu.Lock()
	g.note = ""
	if err != nil {
		g.note = gpuWhy(out)
		g.mu.Unlock()
		log.Printf("gpu: up: %v %.300s", err, out)
		return
	}
	g.asked = now
	g.mu.Unlock()
	s.gpuSync(ctx)
}

// gpuWhy turns the command's refusal into words for the page.
func gpuWhy(out string) string {
	switch {
	case strings.Contains(out, "tope del día"):
		return "Today's GPU time is used up. The view keeps working with the Meta Model API, every 5 s."
	case strings.Contains(out, "presupuesto del mes"):
		return "This month's GPU budget is used up. The view keeps working with the Meta Model API, every 5 s."
	case strings.Contains(out, "otra máquina"):
		return "The GPU is busy with another project right now. Try again in a few minutes."
	default:
		return "No GPU could be rented right now. Try again in a minute; the view keeps working with the Meta Model API."
	}
}
