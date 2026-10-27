// probe runs the measurement pipeline on still images and prints what each
// Meta model returned, with latency and cost.
//
//	MODEL_API_KEY=... go run ./cmd/probe [-prompt "flood water"] [-scene] [-cols 600:680] img.jpg...
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/spartanoscar/vigiax/server/internal/meta"
	"github.com/spartanoscar/vigiax/server/internal/sam"
	"github.com/spartanoscar/vigiax/server/internal/scene"
)

type objectOut struct {
	ID         int     `json:"id"`
	Confidence float64 `json:"confidence"`
	Box        sam.Box `json:"box"`
	Mask       string  `json:"mask"`
	MaskOn     int     `json:"mask_on"`
	AreaPx     int     `json:"area_px"`
}

type imageOut struct {
	Image      string         `json:"image"`
	Prompt     string         `json:"prompt"`
	Frame      string         `json:"frame,omitempty"`
	Objects    []objectOut    `json:"objects"`
	Coverage   float64        `json:"water_coverage"`
	WaterlineY *int           `json:"waterline_y,omitempty"`
	SamMs      int64          `json:"sam_ms"`
	SamUSD     float64        `json:"sam_usd"`
	Scene      *scene.Reading `json:"scene,omitempty"`
	SceneMs    int64          `json:"scene_ms,omitempty"`
	SceneUSD   float64        `json:"scene_usd,omitempty"`
	SceneTok   *meta.Usage    `json:"scene_tokens,omitempty"`
}

func main() {
	prompt := flag.String("prompt", "flood water", "SAM 3.1 concept prompt")
	withScene := flag.Bool("scene", false, "also read the scene with Muse Spark")
	sceneModel := flag.String("scene-model", scene.DefaultModel, "Muse Spark model id")
	effort := flag.String("effort", scene.DefaultEffort, "Muse Spark reasoning effort (minimal ... max)")
	cols := flag.String("cols", "", "x0:x1 frame columns where the waterline is read (default: whole width)")
	flag.Parse()
	key := os.Getenv("MODEL_API_KEY")
	if key == "" || flag.NArg() == 0 {
		log.Fatal("usage: MODEL_API_KEY=... probe [flags] image.jpg...")
	}
	c := meta.NewClient(key)
	ctx := context.Background()
	total := 0.0

	for _, path := range flag.Args() {
		img, err := os.ReadFile(path)
		if err != nil {
			log.Fatal(err)
		}
		r, err := sam.Segment(ctx, c, img, *prompt)
		if err != nil {
			log.Fatalf("%s: %v", path, err)
		}
		out := imageOut{Image: path, Prompt: *prompt, SamMs: r.Latency.Milliseconds(), SamUSD: r.CostUSD, Objects: []objectOut{}}
		total += r.CostUSD
		if r.FrameW > 0 {
			out.Frame = fmt.Sprintf("%dx%d", r.FrameW, r.FrameH)
			area := 0.0
			for _, o := range r.Objects {
				on := 0
				for _, v := range o.Mask {
					on += int(v)
				}
				out.Objects = append(out.Objects, objectOut{o.ID, o.Confidence, o.Box,
					fmt.Sprintf("%dx%d", o.MaskW, o.MaskH), on, int(o.Area())})
				area += o.Area()
			}
			out.Coverage = area / float64(r.FrameW*r.FrameH)
			x0, x1 := 0, r.FrameW-1
			if *cols != "" {
				fmt.Sscanf(*cols, "%d:%d", &x0, &x1)
			}
			if y, ok := r.Waterline(x0, x1); ok {
				out.WaterlineY = &y
			}
		}
		if *withScene {
			s, err := scene.Read(ctx, c, *sceneModel, *effort, img)
			if err != nil {
				log.Fatalf("%s: %v", path, err)
			}
			out.Scene, out.SceneMs, out.SceneUSD, out.SceneTok = s, s.Latency.Milliseconds(), s.CostUSD, &s.Usage
			total += s.CostUSD
		}
		b, _ := json.MarshalIndent(out, "", "  ")
		fmt.Println(string(b))
	}
	fmt.Fprintf(os.Stderr, "total %.4f USD, %s\n", total, time.Now().Format(time.RFC3339))
}
