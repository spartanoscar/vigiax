package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/spartanoscar/vigiax/server/internal/agent"
	"github.com/spartanoscar/vigiax/server/internal/alert"
	"github.com/spartanoscar/vigiax/server/internal/scene"
	"github.com/spartanoscar/vigiax/server/internal/station"
	"github.com/spartanoscar/vigiax/server/internal/usgs"
)

// The agent runs off the hot path: the rules alert at once, and the agent
// wakes on their events, so its seconds never delay a warning.

const (
	frameFresh = 2 * time.Minute // an older frame is not "the scene now"
	keepRuns   = 20
)

type agentRuns struct {
	mu       sync.Mutex
	lastJump time.Time // jumps wake the agent at most once a minute
	busy     bool
	pending  *alert.Event
	runs     []agent.Result
	loaded   bool
}

type frame struct {
	jpeg []byte
	at   time.Time
}

func (s *server) agentFor(id string) *agentRuns {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.agentState[id]
	if !ok {
		a = &agentRuns{}
		s.agentState[id] = a
	}
	return a
}

func (s *server) keepFrame(id string, jpeg []byte, at time.Time) {
	s.mu.Lock()
	s.frames[id] = frame{jpeg: jpeg, at: at}
	for k, f := range s.frames { // drop the frames of stations that went quiet
		if at.Sub(f.at) > frameFresh {
			delete(s.frames, k)
		}
	}
	s.mu.Unlock()
}

// wakeAgent runs the agent for an event; one run per station at a time, and
// an event that arrives meanwhile is handled right after.
func (s *server) wakeAgent(id string, e alert.Event) {
	if s.agent == nil || e.By == "agent" || e.By == "scene" || (e.Kind != "raise" && e.Kind != "jump" && e.Kind != "blind") {
		return
	}
	a := s.agentFor(id)
	a.mu.Lock()
	if e.Kind == "jump" {
		if e.T.Sub(a.lastJump) < time.Minute {
			a.mu.Unlock()
			return
		}
		a.lastJump = e.T
	}
	if a.busy {
		a.pending = &e
		a.mu.Unlock()
		return
	}
	a.busy = true
	a.mu.Unlock()
	for {
		s.runAgent(id, e)
		a.mu.Lock()
		if a.pending == nil {
			a.busy = false
			a.mu.Unlock()
			return
		}
		e, a.pending = *a.pending, nil
		a.mu.Unlock()
	}
}

func (s *server) runAgent(id string, woke alert.Event) {
	s.mu.Lock()
	st := s.stations[id]
	s.mu.Unlock()
	if st == nil {
		return
	}
	var before []agent.Step // wake and scene steps, kept with the run
	say := func(step agent.Step) { s.hub.publish("agent", map[string]any{"station": id, "step": step}) }
	sayBefore := func(step agent.Step) {
		before = append(before, step)
		say(step)
	}
	now := time.Now()
	if err := s.agentBudget.allow(now, "agent", s.agentBudget.limit); err != nil {
		sayBefore(agent.Step{T: now, Kind: "error", Text: "agent budget for today is used up: the rule level stands"})
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	f := s.facts(st, woke, now)
	sayBefore(agent.Step{T: now, Kind: "wake", Text: fmt.Sprintf("woken by %s (%s → %s): %s", woke.Kind, woke.From, woke.To, woke.Reason)})

	var sceneUSD float64
	if jpeg := s.sceneFrame(ctx, st, now); jpeg != nil {
		// The frame on its own and against the normal-day reference, in parallel.
		var sr *scene.Reading
		var ch *scene.Change
		var srErr, chErr error
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			sr, srErr = scene.Read(ctx, s.meta, scene.DefaultModel, scene.DefaultEffort, jpeg)
		}()
		if ref, _, ok := st.GetReference(); ok {
			wg.Add(1)
			go func() {
				defer wg.Done()
				ch, chErr = scene.Compare(ctx, s.meta, scene.DefaultModel, scene.DefaultEffort, ref, jpeg)
			}()
		}
		wg.Wait()
		floor, why := 0, ""
		if srErr != nil {
			sayBefore(agent.Step{T: time.Now(), Kind: "error", Text: "scene: " + srErr.Error()})
		} else {
			f.Scene, sceneUSD = sr, sr.CostUSD
			sayBefore(agent.Step{T: time.Now(), Kind: "scene", Text: "Muse Spark: " + sr.Summary, USD: sr.CostUSD})
			floor, why = sr.Floor()
		}
		if chErr != nil {
			sayBefore(agent.Step{T: time.Now(), Kind: "error", Text: "compare: " + chErr.Error()})
		} else if ch != nil {
			f.Change, sceneUSD = ch, sceneUSD+ch.CostUSD
			sayBefore(agent.Step{T: time.Now(), Kind: "scene", Text: "Against the normal-day frame: " + ch.Notable, USD: ch.CostUSD})
			if lv, w := ch.Floor(); lv > floor {
				floor, why = lv, w
			}
		}
		// Fixed scene rules come before the agent's judgement: a debris flow, a
		// dammed channel or a house in the water is an emergency whatever the gauge says.
		if floor > 0 {
			if e := s.raiseTo(st, alert.Level(floor), "scene: "+why, "scene", f); e != nil {
				sayBefore(agent.Step{T: time.Now(), Kind: "scene", Text: fmt.Sprintf("scene rule → %s: %s", e.To, why)})
				f.RuleLevel = e.To
			}
		}
	}
	r := agent.Decide(ctx, s.agent, f, say)
	r.Steps = append(before, r.Steps...)
	r.CostUSD += sceneUSD
	s.agentBudget.spend(time.Now(), "agent", r.CostUSD)

	if r.Verified && r.Decision.Action == "raise" {
		var to alert.Level
		if to.UnmarshalJSON([]byte(`"`+r.Decision.Level+`"`)) == nil {
			s.raiseTo(st, to, "agent: "+r.Decision.Reason, "agent", f)
		}
	}
	verdict := "holds the rule level"
	if r.Decision != nil {
		verdict = r.Decision.Action + " → " + r.Decision.Level
	}
	decided := agent.Step{T: time.Now(), Kind: "decide", Text: fmt.Sprintf("%s · %d tokens · %.1f s", verdict,
		r.Tokens.PromptTokens+r.Tokens.CompletionTokens, r.Seconds), USD: r.CostUSD}
	r.Steps = append(r.Steps, decided)
	say(decided)
	s.saveRun(id, r)
	log.Printf("%s: agent %s (verified %v, %d attempt(s), $%.4f)", id, verdict, r.Verified, r.Attempts, r.CostUSD)
}

// facts gathers what the agent may know: measurements, the rules, the day.
func (s *server) facts(st *station.Station, woke alert.Event, now time.Time) agent.Facts {
	cfg := st.Config()
	c := alertConfig(cfg)
	al := s.alerts(cfg.ID)
	al.mu.Lock()
	state := al.state
	var today []alert.Event
	for _, e := range al.events {
		if now.Sub(e.T) < 24*time.Hour {
			today = append(today, e)
		}
	}
	al.mu.Unlock()
	if len(today) > 10 {
		today = today[len(today)-10:]
	}
	f := agent.Facts{Station: cfg.ID, Place: c.PlaceEn, Now: now, Woke: woke, RuleLevel: state.Level, History: today}
	if f.Place == "" {
		f.Place = c.Place
	}
	if r, ok := st.Last(); ok {
		f.LevelCm = r.LevelCm
	}
	// The same rates the rules use: a jump between two frames is left out.
	if last, ok := st.Last(); ok {
		f.Rise5 = observe(st, &alert.Config{RiseWindowMin: 5}, last, time.Time{}).RiseCmPerMin
		f.Rise30 = observe(st, &alert.Config{RiseWindowMin: 30}, last, time.Time{}).RiseCmPerMin
	}
	rs := st.Readings(now.Add(-time.Duration(max(c.RiseWindowMin, 5)) * time.Minute))
	step := max(1, len(rs)/8) // at most about eight points
	for i := len(rs) - 1; i >= 0; i -= step {
		if rs[i].LevelCm != nil {
			f.Recent = append([]agent.Point{{MinutesAgo: round1(now.Sub(rs[i].T).Minutes()), LevelCm: round1(*rs[i].LevelCm)}}, f.Recent...)
		}
	}
	var lines map[string]any
	b, _ := json.Marshal(map[string]any{"rules": c.Rules, "confirm_readings": c.Confirm, "rise_window_minutes": c.RiseWindowMin})
	json.Unmarshal(b, &lines)
	f.Lines = lines
	if s.subs != nil {
		f.Subscribers = s.subs.count(cfg.ID, now)
	}
	return f
}

func round1(v float64) float64 { return float64(int(v*10+0.5)) / 10 }

// sceneFrame is the frame Muse Spark reads: the last one posted, if recent,
// or the live camera's latest photo.
func (s *server) sceneFrame(ctx context.Context, st *station.Station, now time.Time) []byte {
	cfg := st.Config()
	s.mu.Lock()
	f, ok := s.frames[cfg.ID]
	s.mu.Unlock()
	if ok && now.Sub(f.at) <= frameFresh {
		return f.jpeg
	}
	if r, ok := st.Last(); ok && r.FrameURL != "" {
		if b, err := usgs.Get(ctx, r.FrameURL); err == nil {
			return b
		}
	}
	return nil
}

func (s *server) saveRun(id string, r agent.Result) {
	a := s.agentFor(id)
	a.mu.Lock()
	a.runs = append(a.runs, r)
	if len(a.runs) > keepRuns {
		a.runs = a.runs[len(a.runs)-keepRuns:]
	}
	a.mu.Unlock()
	f, err := os.OpenFile(filepath.Join(s.dir, id+".agent.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		log.Printf("agent: %v", err)
		return
	}
	defer f.Close()
	b, _ := json.Marshal(r)
	f.Write(append(b, '\n'))
}

// GET /api/stations/{id}/agent: the agent's recent runs, newest last.
func (s *server) getAgent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.mu.Lock()
	_, ok := s.stations[id]
	s.mu.Unlock()
	if !ok {
		httpError(w, http.StatusNotFound, errors.New("no such station"))
		return
	}
	a := s.agentFor(id)
	a.mu.Lock()
	if !a.loaded {
		a.loaded = true
		if f, err := os.Open(filepath.Join(s.dir, id+".agent.jsonl")); err == nil {
			sc := bufio.NewScanner(f)
			sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
			var all []agent.Result
			for sc.Scan() {
				var res agent.Result
				if json.Unmarshal(sc.Bytes(), &res) == nil {
					all = append(all, res)
				}
			}
			f.Close()
			if len(all) > keepRuns {
				all = all[len(all)-keepRuns:]
			}
			a.runs = all // the file holds every run, including those of this process
		}
	}
	runs := append([]agent.Result(nil), a.runs...)
	a.mu.Unlock()
	writeJSON(w, map[string]any{"station": id, "enabled": s.agent != nil, "model": agent.DefaultModel, "runs": runs})
}

// raiseTo lifts a station's alert above what the level rules set (never lower)
// and announces it; nil when the station was already there.
func (s *server) raiseTo(st *station.Station, to alert.Level, reason, by string, f agent.Facts) *alert.Event {
	id := st.Config().ID
	c := alertConfig(st.Config())
	al := s.alerts(id)
	al.mu.Lock()
	e := al.state.Raise(time.Now(), to, reason, by, f.LevelCm, f.Rise5)
	if e != nil {
		al.record(*e)
	}
	al.mu.Unlock()
	if e != nil {
		s.announce(id, &c, *e)
	}
	return e
}
