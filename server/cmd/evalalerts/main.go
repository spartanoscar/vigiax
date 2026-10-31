// evalalerts runs a real camera's frames through Vigiax's alert rules, the
// same code the live station uses, and scores what they raise against the
// official gage of the same site: when each alert went out during a flood
// compared with when the river crossed that line, and how many alerts went out
// on calm days. With -agent, the agent wakes on each rule event as it does in
// the service: Muse Spark reads the frame (and compares it with a normal-day
// frame), and Muse Spark Open decides whether to go higher than the rules.
//
// Readings (SAM 3.1) and agent runs are cached under -cache, so a rerun costs
// nothing. Imagery and gage data: U.S. Geological Survey, public domain.
//
//	MODEL_API_KEY=... BASETEN_API_KEY=... go run ./cmd/evalalerts -station el-reno \
//	    -from 2026-10-04T00:00:00-05:00 -to 2026-10-31T00:00:00-05:00 \
//	    -agent -ref OK_North_Canadian_River_near_El_Reno___2026-10-04T17-00-23Z.jpg \
//	    -out ../eval/results/alerts-el-reno-2026-10
package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/spartanoscar/vigiax/server/internal/agent"
	"github.com/spartanoscar/vigiax/server/internal/alert"
	"github.com/spartanoscar/vigiax/server/internal/meta"
	"github.com/spartanoscar/vigiax/server/internal/scene"
	"github.com/spartanoscar/vigiax/server/internal/station"
	"github.com/spartanoscar/vigiax/server/internal/usgs"
)

const ft2cm = 30.48

// A calm day: the gage moved less than this in the whole day and stayed under
// the attention line. Any alert raised on such a day is a false alarm.
const calmRangeCm = 15.0

type frame struct {
	File    string
	Reading station.Reading
	GageCm  *float64 // nearest gage reading within 30 min
}

// row is one alert event in the results.
type row struct {
	Mode   string // rules or agent
	Event  alert.Event
	File   string
	GageCm *float64
}

// agentRun is what the agent did on one rule event, cached by event.
type agentRun struct {
	Scene     *scene.Reading `json:"scene,omitempty"`
	Change    *scene.Change  `json:"change,omitempty"`
	SceneUSD  float64        `json:"scene_usd"`
	SceneRule string         `json:"scene_rule,omitempty"`
	Result    agent.Result   `json:"result"`
}

func main() {
	id := flag.String("station", "el-reno", "live station (internal/station/live.go)")
	from := flag.String("from", "", "start, RFC 3339")
	to := flag.String("to", "", "end, RFC 3339")
	cache := flag.String("cache", "../data/raw/usgs", "frame, reading and agent-run cache")
	out := flag.String("out", "", "results directory")
	withAgent := flag.Bool("agent", false, "also run the agent on each rule event (needs BASETEN_API_KEY)")
	ref := flag.String("ref", "", "normal-day frame (a file name of the camera) the scene is compared with")
	flag.Parse()
	key := os.Getenv("MODEL_API_KEY")
	if key == "" || *from == "" || *to == "" || *out == "" || (*withAgent && os.Getenv("BASETEN_API_KEY") == "") {
		flag.Usage()
		os.Exit(2)
	}
	var cfg *station.Config
	for i := range station.Live {
		if station.Live[i].ID == *id {
			cfg = &station.Live[i]
		}
	}
	if cfg == nil || cfg.Source == nil || cfg.Alerts == nil {
		log.Fatalf("no live station %q with a camera and alert settings", *id)
	}
	c := *cfg.Alerts
	t0, err := time.Parse(time.RFC3339, *from)
	check(err)
	t1, err := time.Parse(time.RFC3339, *to)
	check(err)
	camDir := filepath.Join(*cache, cfg.Source.Camera)
	check(os.MkdirAll(filepath.Join(camDir, "agent"), 0o755))
	check(os.MkdirAll(*out, 0o755))

	ctx := context.Background()
	gage, err := usgs.GageHeights(ctx, cfg.Source.Site, t0, t1)
	check(err)
	files, err := usgs.Files(ctx, cfg.Source.Camera, t0, t1)
	check(err)
	log.Printf("%d gage readings, %d frames", len(gage), len(files))

	tmp, err := os.MkdirTemp("", "evalalerts")
	check(err)
	defer os.RemoveAll(tmp)
	st, err := station.Open(tmp, cfg.ID)
	check(err)
	check(st.Update(func(x *station.Config) { *x = *cfg }))

	mc := meta.NewClient(key)
	var frames []frame
	for _, f := range files {
		t, err := usgs.FrameTime(f)
		check(err)
		fr := frame{File: f}
		if g, ok := usgs.Nearest(gage, t, 30*time.Minute); ok {
			cm := g * ft2cm
			fr.GageCm = &cm
		}
		path := filepath.Join(camDir, strings.TrimSuffix(f, ".jpg")+".reading.json")
		if b, err := os.ReadFile(path); err == nil {
			check(json.Unmarshal(b, &fr.Reading))
			check(st.Add(fr.Reading))
		} else {
			// The live station's own path: SAM 3.1 on the frame, the mud prompts
			// when it finds no water, the rating from water area to level.
			img := frameJPEG(ctx, camDir, cfg.Source.Camera, f)
			fr.Reading, err = st.MeasureFrame(ctx, mc, img, station.Reading{T: t, FrameURL: usgs.FrameURL(cfg.Source.Camera, f)})
			check(err)
			b, _ := json.Marshal(fr.Reading)
			check(os.WriteFile(path, b, 0o644))
		}
		frames = append(frames, fr)
		lv := "blind"
		if fr.Reading.LevelCm != nil {
			lv = fmt.Sprintf("%5.0f cm", *fr.Reading.LevelCm)
		}
		log.Printf("%s  %s  gage %s", t.Format(time.RFC3339), lv, cmText(fr.GageCm))
	}
	sort.Slice(frames, func(i, j int) bool { return frames[i].Reading.T.Before(frames[j].Reading.T) })

	rows := replay(st, &c, frames, "rules", nil)
	var runs []agentRun
	if *withAgent {
		b := agent.NewBaseten(os.Getenv("BASETEN_API_KEY"))
		var refJPEG []byte
		if *ref != "" {
			refJPEG = frameJPEG(ctx, camDir, cfg.Source.Camera, *ref)
		}
		wake := func(state *alert.State, events []alert.Event, e alert.Event, fr frame) []alert.Event {
			r, raised := runAgent(ctx, mc, b, st, &c, state, events, e, fr, camDir, cfg.Source.Camera, refJPEG)
			runs = append(runs, r)
			return raised
		}
		rows = append(rows, replay(st, &c, frames, "agent", wake)...)
	}
	report(*out, cfg, &c, frames, gage, rows, runs, *withAgent, *ref)
}

// replay steps the frames through the rules in time order, as the service does
// with each new reading. wake, when set, runs the agent on the events that wake
// it in the service: a raise, a jump or a blind camera, but not its own raises.
func replay(st *station.Station, c *alert.Config, frames []frame, mode string,
	wake func(*alert.State, []alert.Event, alert.Event, frame) []alert.Event) []row {
	var state alert.State
	var events []alert.Event
	var rows []row
	for _, fr := range frames {
		o := st.Observe(c, fr.Reading, time.Time{})
		e := state.Step(c, o)
		if e == nil {
			continue
		}
		events = append(events, *e)
		rows = append(rows, row{Mode: mode, Event: *e, File: fr.File, GageCm: fr.GageCm})
		if wake == nil || (e.Kind != "raise" && e.Kind != "jump" && e.Kind != "blind") {
			continue
		}
		for _, r := range wake(&state, events, *e, fr) {
			events = append(events, r)
			rows = append(rows, row{Mode: mode, Event: r, File: fr.File, GageCm: fr.GageCm})
		}
	}
	return rows
}

// runAgent does what the service does when an event wakes the agent, with the
// event's time as now: Muse Spark reads the frame and compares it with the
// normal-day frame, the scene rules may raise the level, and Muse Spark Open
// decides whether to go higher. It returns the raises the scene rules and the
// agent made.
func runAgent(ctx context.Context, mc *meta.Client, b *agent.Baseten, st *station.Station, c *alert.Config,
	state *alert.State, events []alert.Event, woke alert.Event, fr frame, camDir, cam string, ref []byte) (agentRun, []alert.Event) {
	path := filepath.Join(camDir, "agent", woke.T.UTC().Format("2006-01-02T15-04-05Z")+"-"+woke.Kind+".json")
	var r agentRun
	cached := false
	if bs, err := os.ReadFile(path); err == nil && json.Unmarshal(bs, &r) == nil {
		cached = true
	}
	f := agent.Gather(st, *c, state.Level, events, woke, woke.T)
	if !cached {
		jpeg := frameJPEG(ctx, camDir, cam, fr.File)
		var srErr, chErr error
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.Scene, srErr = scene.Read(ctx, mc, scene.DefaultModel, scene.DefaultEffort, jpeg)
		}()
		if ref != nil {
			wg.Add(1)
			go func() {
				defer wg.Done()
				r.Change, chErr = scene.Compare(ctx, mc, scene.DefaultModel, scene.DefaultEffort, ref, jpeg)
			}()
		}
		wg.Wait()
		check(srErr)
		check(chErr)
	}
	var raised []alert.Event
	floor, why := 0, ""
	if r.Scene != nil {
		f.Scene = r.Scene
		r.SceneUSD = r.Scene.CostUSD
		floor, why = r.Scene.Floor()
	}
	if r.Change != nil {
		f.Change = r.Change
		r.SceneUSD += r.Change.CostUSD
		if lv, w := r.Change.Floor(); lv > floor {
			floor, why = lv, w
		}
	}
	if floor > 0 {
		r.SceneRule = fmt.Sprintf("%s: %s", alert.Level(floor), why)
		if e := state.Raise(woke.T, alert.Level(floor), "scene: "+why, "scene", f.LevelCm, f.Rise5); e != nil {
			f.RuleLevel = state.Level
			raised = append(raised, *e)
		}
	}
	if !cached {
		cctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
		r.Result = agent.Decide(cctx, b, f, nil)
		cancel()
		r.Result.CostUSD += r.SceneUSD
		bs, _ := json.MarshalIndent(r, "", "  ")
		check(os.WriteFile(path, bs, 0o644))
	}
	d := r.Result.Decision
	verdict := "no verified answer: the rule level stands"
	if r.Result.Verified && d != nil {
		verdict = d.Action + " → " + d.Level
		var to alert.Level
		if d.Action == "raise" && to.UnmarshalJSON([]byte(`"`+d.Level+`"`)) == nil {
			if e := state.Raise(woke.T, to, "agent: "+d.Reason, "agent", f.LevelCm, f.Rise5); e != nil {
				raised = append(raised, *e)
			}
		}
	}
	log.Printf("agent on %s %s (%s → %s): %s ($%.4f)", woke.T.Format(time.RFC3339), woke.Kind, woke.From, woke.To, verdict, r.Result.CostUSD)
	return r, raised
}

// frameJPEG returns a camera frame, downloaded once into the cache.
func frameJPEG(ctx context.Context, camDir, cam, file string) []byte {
	path := filepath.Join(camDir, file)
	if b, err := os.ReadFile(path); err == nil {
		return b
	}
	b, err := usgs.Get(ctx, usgs.FrameURL(cam, file))
	check(err)
	check(os.WriteFile(path, b, 0o644))
	return b
}

// crossing is the first gage reading at or above a line, at or after from.
func crossing(gage map[time.Time]float64, lineCm float64, from time.Time) (time.Time, bool) {
	var ts []time.Time
	for t := range gage {
		ts = append(ts, t)
	}
	sort.Slice(ts, func(i, j int) bool { return ts[i].Before(ts[j]) })
	for _, t := range ts {
		if !t.Before(from) && gage[t]*ft2cm >= lineCm {
			return t, true
		}
	}
	return time.Time{}, false
}

func report(out string, cfg *station.Config, c *alert.Config, frames []frame, gage map[time.Time]float64,
	rows []row, runs []agentRun, withAgent bool, ref string) {
	loc, err := time.LoadLocation("America/Chicago") // the camera's local day
	check(err)
	day := func(t time.Time) string { return t.In(loc).Format("2006-01-02") }

	// Days from the gage: calm ones are where false alarms are counted. Only
	// days the camera took frames on count.
	type span struct{ lo, hi float64 }
	days := map[string]*span{}
	seen := map[string]bool{}
	for _, f := range frames {
		seen[day(f.Reading.T)] = true
	}
	for t, ft := range gage {
		if !seen[day(t)] {
			continue
		}
		cm := ft * ft2cm
		d := days[day(t)]
		if d == nil {
			d = &span{cm, cm}
			days[day(t)] = d
		}
		d.lo, d.hi = math.Min(d.lo, cm), math.Max(d.hi, cm)
	}
	var calm []string
	for d, s := range days {
		if s.hi-s.lo < calmRangeCm && (c.Rules.Attention.LevelCm == nil || s.hi < *c.Rules.Attention.LevelCm) {
			calm = append(calm, d)
		}
	}
	sort.Strings(calm)
	isCalm := map[string]bool{}
	for _, d := range calm {
		isCalm[d] = true
	}
	framesOn := func(days map[string]bool) (n int) {
		for _, f := range frames {
			if days[day(f.Reading.T)] {
				n++
			}
		}
		return n
	}
	raisesOn := func(mode string, days map[string]bool) (n int, list []string) {
		for _, r := range rows {
			if r.Mode == mode && r.Event.Kind == "raise" && days[day(r.Event.T)] {
				n++
				list = append(list, fmt.Sprintf("%s %s → %s", r.Event.T.In(loc).Format("Jan 2 15:04"), r.Event.From, r.Event.To))
			}
		}
		return n, list
	}

	// The flood: when the gage crossed each line, and when each mode raised to it.
	var peakT time.Time
	peak := math.Inf(-1)
	for t, ft := range gage {
		if ft*ft2cm > peak || (ft*ft2cm == peak && t.Before(peakT)) {
			peak, peakT = ft*ft2cm, t
		}
	}
	firstRaise := func(mode string, l alert.Level) (time.Time, string, bool) {
		for _, r := range rows {
			if r.Mode == mode && r.Event.Kind == "raise" && r.Event.To >= l {
				return r.Event.T, r.Event.Reason, true
			}
		}
		return time.Time{}, "", false
	}
	modes := []string{"rules"}
	if withAgent {
		modes = append(modes, "agent")
	}
	var flood strings.Builder
	fmt.Fprintf(&flood, "| Level | Line | Gage crossed the line | %s |\n|---|---|---|%s\n",
		strings.Join(modeHeads(modes), " | "), strings.Repeat("---|", len(modes)))
	for l := alert.Attention; l <= alert.Evacuate; l++ {
		r := c.Rule(l)
		if !r.Enabled || r.LevelCm == nil {
			continue
		}
		gt, gok := crossing(gage, *r.LevelCm, time.Time{})
		gcell := "never"
		if gok {
			gcell = gt.In(loc).Format("Jan 2 15:04")
		}
		var cells []string
		for _, m := range modes {
			vt, why, ok := firstRaise(m, l)
			switch {
			case !ok:
				cells = append(cells, "not raised")
			case gok:
				cells = append(cells, fmt.Sprintf("%s (%s) — %s", vt.In(loc).Format("Jan 2 15:04"), lead(gt.Sub(vt)), shortWhy(why)))
			default:
				cells = append(cells, fmt.Sprintf("%s — %s", vt.In(loc).Format("Jan 2 15:04"), shortWhy(why)))
			}
		}
		fmt.Fprintf(&flood, "| %s | %.0f cm | %s | %s |\n", l, *r.LevelCm, gcell, strings.Join(cells, " | "))
	}

	// Cost and frames.
	var samUSD float64
	blind := 0
	for _, f := range frames {
		samUSD += f.Reading.CostUSD
		if f.Reading.Blind {
			blind++
		}
	}
	var agentUSD, agentSec float64
	agentRaises, verified := 0, 0
	for _, r := range runs {
		agentUSD += r.Result.CostUSD
		agentSec += r.Result.Seconds
		if r.Result.Verified {
			verified++
		}
		if r.Result.Verified && r.Result.Decision != nil && r.Result.Decision.Action == "raise" {
			agentRaises++
		}
	}
	nDays := len(days)

	// Files: every event, and the readings with the gage beside them.
	w, err := os.Create(filepath.Join(out, "events.csv"))
	check(err)
	cw := csv.NewWriter(w)
	cw.Write([]string{"mode", "time_utc", "kind", "from", "to", "by", "level_cm", "gage_cm", "frame", "reason"})
	for _, r := range rows {
		cw.Write([]string{r.Mode, r.Event.T.UTC().Format(time.RFC3339), r.Event.Kind, r.Event.From.String(), r.Event.To.String(),
			r.Event.By, cmText(r.Event.LevelCm), cmText(r.GageCm), r.File, r.Event.Reason})
	}
	cw.Flush()
	check(w.Close())
	w, err = os.Create(filepath.Join(out, "readings.csv"))
	check(err)
	cw = csv.NewWriter(w)
	cw.Write([]string{"time_utc", "file", "level_cm", "gage_cm", "sam_confidence", "blind", "mud"})
	for _, f := range frames {
		cw.Write([]string{f.Reading.T.UTC().Format(time.RFC3339), f.File, cmText(f.Reading.LevelCm), cmText(f.GageCm),
			fmt.Sprintf("%.3f", f.Reading.Confidence), fmt.Sprint(f.Reading.Blind), fmt.Sprint(f.Reading.Mud)})
	}
	cw.Flush()
	check(w.Close())
	if withAgent {
		b, _ := json.MarshalIndent(runs, "", "  ")
		check(os.WriteFile(filepath.Join(out, "agent-runs.json"), b, 0o644))
	}

	var md strings.Builder
	fmt.Fprintf(&md, "# %s — alerts on real frames against the official gage\n\n", cfg.Name)
	fmt.Fprintf(&md, "Generated by `go run ./cmd/evalalerts` (server/) on %s. Imagery and gage data: U.S. Geological Survey (public domain).\n\n",
		time.Now().Format("2006-01-02"))
	fmt.Fprintf(&md, "Every frame of the camera went through the live station's own code: SAM 3.1 on the frame, the rating from water area to level (%s), and the alert rules with the station's settings (`internal/station/live.go`). Lines: attention %s, alert %s, evacuate %s; %s\n\n",
		cfg.Rating.Fit, ruleText(c.Rules.Attention), ruleText(c.Rules.Alert), ruleText(c.Rules.Evacuate), c.Note)
	fmt.Fprintf(&md, "| | |\n|---|---|\n")
	fmt.Fprintf(&md, "| Period | %s → %s (%d days, camera's local time) |\n", day(frames[0].Reading.T), day(frames[len(frames)-1].Reading.T), nDays)
	fmt.Fprintf(&md, "| Frames | %d (%d blind: no water found with confidence) |\n", len(frames), blind)
	fmt.Fprintf(&md, "| Peak of the gage | %.0f cm on %s |\n", peak, peakT.In(loc).Format("Jan 2 15:04"))
	n, list := raisesOn("rules", isCalm)
	fmt.Fprintf(&md, "| **False alarms on calm days, rules** | **%d in %d calm days** (%d frames)%s |\n", n, len(calm), framesOn(isCalm), listText(list))
	if withAgent {
		n, list := raisesOn("agent", isCalm)
		fmt.Fprintf(&md, "| **False alarms on calm days, rules + agent** | **%d in %d calm days**%s |\n", n, len(calm), listText(list))
		fmt.Fprintf(&md, "| Agent runs | %d (woken by the rules' events); %d verified, %d raised above the rules; mean %.1f s |\n",
			len(runs), verified, agentRaises, agentSec/math.Max(1, float64(len(runs))))
	}
	fmt.Fprintf(&md, "| Model cost | SAM 3.1 $%.4f (%.4f per day)", samUSD, samUSD/float64(nDays))
	if withAgent {
		fmt.Fprintf(&md, " · agent with Muse Spark scene reading $%.4f", agentUSD)
	}
	fmt.Fprintf(&md, "; a rerun from the cache costs nothing |\n\n")
	fmt.Fprintf(&md, "Calm days: the gage moved less than %.0f cm in the day and stayed under the attention line: %s.\n\n", calmRangeCm, strings.Join(calm, ", "))
	fmt.Fprintf(&md, "## The flood: when each alert went out\n\nTimes are the camera's local time. In brackets, how long before (+) or after (−) the official gage crossed the same line.\n\n%s\n", flood.String())
	if withAgent && ref != "" {
		fmt.Fprintf(&md, "Normal-day frame the scene was compared with: `%s`.\n\n", ref)
	}
	fmt.Fprintf(&md, "Caveats: the camera publishes one photo an hour by day, so an alert can come up to an hour after the water does what raises it, and nothing is measured at night. The rating was fitted on the flood's even frames (eval/results/el-reno-2026-10); the calm days were never used to fit it.\n\n")
	fmt.Fprintf(&md, "Per-event and per-frame values: events.csv, readings.csv%s.\n", map[bool]string{true: ", agent-runs.json", false: ""}[withAgent])
	check(os.WriteFile(filepath.Join(out, "README.md"), []byte(md.String()), 0o644))
	fmt.Print(md.String())
}

func modeHeads(modes []string) []string {
	var h []string
	for _, m := range modes {
		h = append(h, map[string]string{"rules": "Rules raised", "agent": "Rules + agent raised"}[m])
	}
	return h
}

func lead(d time.Duration) string {
	m := math.Round(d.Minutes())
	if math.Abs(m) >= 90 {
		return fmt.Sprintf("%+.1f h", m/60)
	}
	return fmt.Sprintf("%+.0f min", m)
}

func shortWhy(why string) string {
	if i := strings.Index(why, ";"); i > 0 {
		why = why[:i]
	}
	return why
}

func ruleText(r alert.Rule) string {
	var p []string
	if r.LevelCm != nil {
		p = append(p, fmt.Sprintf("%.0f cm", *r.LevelCm))
	}
	if r.RiseCmPerMin != nil {
		p = append(p, fmt.Sprintf("%.1f cm/min", *r.RiseCmPerMin))
	}
	return strings.Join(p, " or ")
}

func listText(list []string) string {
	if len(list) == 0 {
		return ""
	}
	return ": " + strings.Join(list, "; ")
}

func cmText(v *float64) string {
	if v == nil {
		return ""
	}
	return fmt.Sprintf("%.0f", *v)
}

func check(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
