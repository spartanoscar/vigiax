// overlay segments each image with SAM 3.1 for several concepts and writes the
// masks painted over the photo (one color family per concept, one shade per
// object), plus a JSON summary.
//
//	MODEL_API_KEY=... go run ./cmd/overlay -out dir [-prompts "flood water,person,rock"] img.jpg...
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	_ "image/png"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/spartanoscar/vigiax/server/internal/meta"
	"github.com/spartanoscar/vigiax/server/internal/sam"
)

// One color family per concept, in prompt order; shades separate objects.
var families = [][]color.RGBA{
	{{0, 190, 255, 255}, {0, 140, 230, 255}, {60, 220, 255, 255}},  // water: cyan/blue
	{{255, 70, 40, 255}, {255, 130, 0, 255}, {230, 30, 90, 255}},   // red/orange
	{{255, 215, 0, 255}, {170, 230, 40, 255}, {255, 170, 60, 255}}, // yellow/lime
	{{190, 90, 255, 255}, {255, 90, 220, 255}, {130, 110, 255, 255}},
}

type objOut struct {
	ID         int     `json:"id"`
	Confidence float64 `json:"confidence"`
	Box        sam.Box `json:"box"`
	AreaPct    float64 `json:"area_pct"`
}

type conceptOut struct {
	Prompt    string   `json:"prompt"`
	Color     string   `json:"color"`
	Objects   []objOut `json:"objects"`
	LatencyMs int64    `json:"latency_ms"`
	CostUSD   float64  `json:"cost_usd"`
}

type imageOut struct {
	Image    string       `json:"image"`
	Overlay  string       `json:"overlay"`
	W        int          `json:"w"`
	H        int          `json:"h"`
	Concepts []conceptOut `json:"concepts"`
}

func main() {
	out := flag.String("out", "overlays", "output directory")
	prompts := flag.String("prompts", "flood water,person,rock", "comma-separated SAM concepts")
	flag.Parse()
	key := os.Getenv("MODEL_API_KEY")
	if key == "" || flag.NArg() == 0 {
		log.Fatal("usage: MODEL_API_KEY=... overlay -out dir [-prompts a,b] image.jpg...")
	}
	if err := os.MkdirAll(*out, 0o755); err != nil {
		log.Fatal(err)
	}
	c := meta.NewClient(key)
	var all []imageOut
	total := 0.0

	for _, path := range flag.Args() {
		raw, err := os.ReadFile(path)
		if err != nil {
			log.Fatal(err)
		}
		src, _, err := image.Decode(strings.NewReader(string(raw)))
		if err != nil {
			log.Fatalf("%s: %v", path, err)
		}
		b := src.Bounds()
		canvas := image.NewRGBA(b)
		draw.Draw(canvas, b, src, b.Min, draw.Src)
		res := imageOut{Image: filepath.Base(path), W: b.Dx(), H: b.Dy()}

		for i, p := range strings.Split(*prompts, ",") {
			p = strings.TrimSpace(p)
			r, err := sam.Segment(context.Background(), c, raw, p)
			if err != nil {
				log.Fatalf("%s %q: %v", path, p, err)
			}
			total += r.CostUSD
			fam := families[i%len(families)]
			co := conceptOut{Prompt: p, Color: hex(fam[0]), LatencyMs: r.Latency.Milliseconds(), CostUSD: r.CostUSD, Objects: []objOut{}}
			for j := range r.Objects {
				o := &r.Objects[j]
				paint(canvas, o, fam[j%len(fam)])
				co.Objects = append(co.Objects, objOut{o.ID, o.Confidence, o.Box, 100 * o.Area() / float64(b.Dx()*b.Dy())})
			}
			res.Concepts = append(res.Concepts, co)
			log.Printf("%s %q: %d objects, %d ms", res.Image, p, len(r.Objects), co.LatencyMs)
		}

		res.Overlay = strings.TrimSuffix(res.Image, filepath.Ext(res.Image)) + "-sam.jpg"
		f, err := os.Create(filepath.Join(*out, res.Overlay))
		if err != nil {
			log.Fatal(err)
		}
		if err := jpeg.Encode(f, canvas, &jpeg.Options{Quality: 86}); err != nil {
			log.Fatal(err)
		}
		f.Close()
		all = append(all, res)
	}

	j, _ := json.MarshalIndent(all, "", "  ")
	if err := os.WriteFile(filepath.Join(*out, "overlays.json"), j, 0o644); err != nil {
		log.Fatal(err)
	}
	log.Printf("total %.4f USD", total)
}

// paint tints the mask at 50% and draws its outline and box in solid color.
func paint(img *image.RGBA, o *sam.Object, c color.RGBA) {
	for y := o.Box.Y1; y <= o.Box.Y2; y++ {
		for x := o.Box.X1; x <= o.Box.X2; x++ {
			if !o.At(x, y) {
				continue
			}
			edge := !o.At(x-1, y) || !o.At(x+1, y) || !o.At(x, y-1) || !o.At(x, y+1)
			if edge {
				img.SetRGBA(x, y, c)
				continue
			}
			p := img.RGBAAt(x, y)
			img.SetRGBA(x, y, color.RGBA{blend(p.R, c.R), blend(p.G, c.G), blend(p.B, c.B), 255})
		}
	}
	for x := o.Box.X1; x <= o.Box.X2; x += 2 { // dotted box
		img.SetRGBA(x, o.Box.Y1, c)
		img.SetRGBA(x, o.Box.Y2, c)
	}
	for y := o.Box.Y1; y <= o.Box.Y2; y += 2 {
		img.SetRGBA(o.Box.X1, y, c)
		img.SetRGBA(o.Box.X2, y, c)
	}
}

func blend(a, b uint8) uint8 { return uint8((uint16(a) + uint16(b)) / 2) }

func hex(c color.RGBA) string { return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B) }
