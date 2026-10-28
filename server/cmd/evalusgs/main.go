// evalusgs measures a real flood on a USGS webcam with SAM 3.1 and compares the
// result with the official gage height of the same station.
//
// USGS webcams (https://www.usgs.gov/tools/hydrologic-imagery-visualization-and-information-system-hivis)
// and gage data are public domain (credit: U.S. Geological Survey). Frames and
// SAM results are cached under -cache, so a rerun costs nothing.
//
// The camera has no staff gauge, so the level signal is the water area in the
// frame. Even-numbered frames (in time order) fit area → gage height; the odd
// frames are the test set the error is reported on.
//
//	MODEL_API_KEY=... go run ./cmd/evalusgs -cam OK_North_Canadian_River_near_El_Reno -site 07239500 \
//	    -from 2026-10-13T00:00:00-05:00 -to 2026-10-16T00:00:00-05:00 -out ../eval/results/el-reno-2026-10
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
	"time"

	"github.com/spartanoscar/vigiax/server/internal/meta"
	"github.com/spartanoscar/vigiax/server/internal/sam"
	"github.com/spartanoscar/vigiax/server/internal/station"
	"github.com/spartanoscar/vigiax/server/internal/usgs"
)

type frame struct {
	File       string    `json:"file"`
	T          time.Time `json:"t"`
	GageFt     float64   `json:"gage_ft"`
	Found      bool      `json:"found"`
	Confidence float64   `json:"confidence"`
	Coverage   float64   `json:"coverage"`
	SamMs      int64     `json:"sam_ms"`
	CostUSD    float64   `json:"cost_usd"`
	Test       bool      `json:"test"`
	PredFt     *float64  `json:"pred_ft,omitempty"`
}

func main() {
	cam := flag.String("cam", "", "NIMS camera id")
	site := flag.String("site", "", "USGS site number of the gage")
	from := flag.String("from", "", "start, RFC 3339")
	to := flag.String("to", "", "end, RFC 3339")
	prompt := flag.String("prompt", station.DefaultPrompt, "SAM 3.1 concept")
	cache := flag.String("cache", "../data/raw/usgs", "frame and SAM result cache")
	out := flag.String("out", "", "results directory")
	minConf := flag.Float64("min-confidence", 0.5, "readings below this SAM confidence count as degraded")
	flag.Parse()
	key := os.Getenv("MODEL_API_KEY")
	if key == "" || *cam == "" || *site == "" || *from == "" || *to == "" || *out == "" {
		flag.Usage()
		os.Exit(2)
	}
	t0, err := time.Parse(time.RFC3339, *from)
	check(err)
	t1, err := time.Parse(time.RFC3339, *to)
	check(err)
	camDir := filepath.Join(*cache, *cam)
	check(os.MkdirAll(camDir, 0o755))
	check(os.MkdirAll(*out, 0o755))

	ctx := context.Background()
	gage, err := usgs.GageHeights(ctx, *site, t0, t1)
	check(err)
	if len(gage) == 0 {
		log.Fatal("no gage height for this site and period")
	}
	files, err := usgs.Files(ctx, *cam, t0, t1)
	check(err)
	log.Printf("%d gage readings, %d frames", len(gage), len(files))

	c := meta.NewClient(key)
	var frames []frame
	for _, f := range files {
		t, err := usgs.FrameTime(f)
		check(err)
		g, ok := usgs.Nearest(gage, t, 30*time.Minute)
		if !ok {
			continue
		}
		fr := frame{File: f, T: t, GageFt: g}
		img := cached(filepath.Join(camDir, f), func() []byte {
			b, err := usgs.Get(ctx, usgs.FrameURL(*cam, f))
			check(err)
			return b
		})
		resPath := filepath.Join(camDir, strings.TrimSuffix(f, ".jpg")+"."+strings.ReplaceAll(*prompt, " ", "_")+".sam.json")
		var r *sam.Result
		if b, err := os.ReadFile(resPath); err == nil {
			check(json.Unmarshal(b, &r))
		} else {
			r, err = sam.Segment(ctx, c, img, *prompt)
			check(err)
			b, _ := json.Marshal(r)
			check(os.WriteFile(resPath, b, 0o644))
		}
		fr.SamMs, fr.CostUSD = r.Latency.Milliseconds(), r.CostUSD // also when read from the cache
		for i := range r.Objects {
			fr.Confidence = max(fr.Confidence, r.Objects[i].Confidence)
			if r.FrameW > 0 {
				fr.Coverage += r.Objects[i].Area() / float64(r.FrameW*r.FrameH)
			}
		}
		fr.Found = fr.Confidence >= *minConf
		frames = append(frames, fr)
		log.Printf("%s  gage %5.2f ft  water %4.1f %%  conf %.2f", t.Format(time.RFC3339), g, fr.Coverage*100, fr.Confidence)
	}
	report(frames, *out, *cam, *site, *prompt, *minConf)
}

// report fits gage height = a + b·coverage on the even frames and scores the odd ones.
func report(frames []frame, out, cam, site, prompt string, minConf float64) {
	sort.Slice(frames, func(i, j int) bool { return frames[i].T.Before(frames[j].T) })
	var train, test []*frame
	n := 0
	for i := range frames {
		if !frames[i].Found {
			continue
		}
		frames[i].Test = n%2 == 1
		if frames[i].Test {
			test = append(test, &frames[i])
		} else {
			train = append(train, &frames[i])
		}
		n++
	}
	if len(train) < 3 || len(test) < 3 {
		log.Fatalf("too few usable frames: %d train, %d test", len(train), len(test))
	}
	var sx, sy, sxx, sxy float64
	for _, f := range train {
		sx += f.Coverage
		sy += f.GageFt
		sxx += f.Coverage * f.Coverage
		sxy += f.Coverage * f.GageFt
	}
	k := float64(len(train))
	b := (k*sxy - sx*sy) / (k*sxx - sx*sx)
	a := (sy - b*sx) / k
	var abs, sq, cost float64
	var errs []float64
	for i := range frames {
		cost += frames[i].CostUSD
		if frames[i].Found {
			p := a + b*frames[i].Coverage
			frames[i].PredFt = &p
		}
	}
	for _, f := range test {
		e := *f.PredFt - f.GageFt
		abs += math.Abs(e)
		sq += e * e
		errs = append(errs, math.Abs(e))
	}
	sort.Float64s(errs)
	ft2cm := 30.48
	mae := abs / float64(len(test)) * ft2cm
	rmse := math.Sqrt(sq/float64(len(test))) * ft2cm
	p90 := errs[int(math.Ceil(0.9*float64(len(errs))))-1] * ft2cm
	lo, hi := math.Inf(1), math.Inf(-1)
	for _, f := range frames {
		lo, hi = math.Min(lo, f.GageFt), math.Max(hi, f.GageFt)
	}
	degraded := len(frames) - n

	w, err := os.Create(filepath.Join(out, "frames.csv"))
	check(err)
	cw := csv.NewWriter(w)
	cw.Write([]string{"time_utc", "file", "gage_ft", "sam_confidence", "water_coverage", "usable", "set", "predicted_ft"})
	for _, f := range frames {
		set, pred := "", ""
		if f.Found {
			set = map[bool]string{true: "test", false: "fit"}[f.Test]
			pred = fmt.Sprintf("%.2f", *f.PredFt)
		}
		cw.Write([]string{f.T.Format(time.RFC3339), f.File, fmt.Sprintf("%.2f", f.GageFt), fmt.Sprintf("%.3f", f.Confidence),
			fmt.Sprintf("%.4f", f.Coverage), fmt.Sprint(f.Found), set, pred})
	}
	cw.Flush()
	check(w.Close())

	md := fmt.Sprintf(`# %s — SAM 3.1 water level vs USGS gage %s

Generated by `+"`go run ./cmd/evalusgs`"+` (server/) on %s. Imagery and gage data: U.S. Geological Survey (public domain).

| | |
|---|---|
| Frames | %d (%s → %s, UTC) |
| Gage range | %.2f → %.2f ft (%.0f cm) |
| Usable frames (SAM confidence ≥ %.2f) | %d; degraded %d |
| SAM prompt | "%s" |
| Fit (even frames, n=%d) | gage_ft = %.2f + %.2f × water_coverage |
| **Test error (odd frames, n=%d)** | **MAE %.1f cm · RMSE %.1f cm · P90 %.1f cm** |
| Model cost of the measurements | $%.4f (a rerun from the cache costs nothing) |

Readings below the confidence threshold are frames where SAM found no water: at this camera, night
frames without enough light. Vigiax treats them as a blind camera, never as a lower level.

Caveat: fit and test frames come from the same event (alternate frames). Calibrating on one event and
testing on another is the stricter test.

Per-frame values: frames.csv.
`, cam, site, time.Now().UTC().Format(time.DateOnly), len(frames), frames[0].T.Format(time.RFC3339), frames[len(frames)-1].T.Format(time.RFC3339),
		lo, hi, (hi-lo)*ft2cm, minConf, n, degraded, prompt, len(train), a, b, len(test), mae, rmse, p90, cost)
	check(os.WriteFile(filepath.Join(out, "README.md"), []byte(md), 0o644))
	fmt.Print(md)
}

func cached(path string, fetch func() []byte) []byte {
	if b, err := os.ReadFile(path); err == nil {
		return b
	}
	b := fetch()
	check(os.WriteFile(path, b, 0o644))
	return b
}

func check(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
