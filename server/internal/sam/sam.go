// Package sam segments images with SAM 3.1 on Meta Model API and reads its
// special-token output (https://dev.meta.ai/docs/sam/reading-segmentation).
package sam

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"time"

	"github.com/spartanoscar/vigiax/server/internal/meta"
)

const Model = "sam-3.1"

// Box is in source pixels, inclusive corners, as SAM reports it.
type Box struct{ X1, Y1, X2, Y2 int }

// Object is one match: a box and a mask raster that covers that box. The raster
// may be smaller than the box (SAM downsamples large ones), so map through At.
type Object struct {
	ID         int
	Box        Box
	Confidence float64
	MaskW      int
	MaskH      int
	Mask       []byte // row-major 0/1, MaskH × MaskW
}

type Result struct {
	FrameW, FrameH int
	Objects        []Object
	Latency        time.Duration
	CostUSD        float64
}

// Segment finds every match of a short noun phrase in a JPEG image.
func Segment(ctx context.Context, c *meta.Client, jpeg []byte, prompt string) (*Result, error) {
	res, err := c.Create(ctx, &meta.Request{
		Model:    Model,
		Input:    []meta.Message{meta.User(meta.Text(prompt), meta.JPEG(jpeg))},
		Metadata: map[string]string{"mask_encoding": "one_bit", "include_confidence": "true"},
	})
	if err != nil {
		return nil, err
	}
	r, err := Parse(res.OutputText())
	if err != nil {
		return nil, err
	}
	r.Latency, r.CostUSD = res.Latency, res.CostUSD()
	return r, nil
}

var recordRe = regexp.MustCompile(
	`(\d+)<\|box;x1=(\d+);y1=(\d+);x2=(\d+);y2=(\d+);w=(\d+);h=(\d+)(?:;c=([0-9.]+))?\|>` +
		`<\|mask;x=0;y=0(?:;c=[0-9.]+)?;data=(\d+),(\d+),([^|]*)\|>`)

// Parse reads the output of a single-image request (frame <0f>). Empty text
// means no match, which is a valid result.
func Parse(text string) (*Result, error) {
	r := &Result{}
	for _, m := range recordRe.FindAllStringSubmatch(text, -1) {
		n := func(i int) int { v, _ := strconv.Atoi(m[i]); return v }
		o := Object{ID: n(1), Box: Box{n(2), n(3), n(4), n(5)}, MaskH: n(9), MaskW: n(10)}
		r.FrameW, r.FrameH = n(6), n(7)
		if m[8] != "" {
			o.Confidence, _ = strconv.ParseFloat(m[8], 64)
		}
		mask, err := DecodeOneBit(m[11], o.MaskW, o.MaskH)
		if err != nil {
			return nil, fmt.Errorf("object %d: %w", o.ID, err)
		}
		o.Mask = mask
		r.Objects = append(r.Objects, o)
	}
	return r, nil
}

// At reports whether frame pixel (x, y) is inside the object's mask.
func (o *Object) At(x, y int) bool {
	if x < o.Box.X1 || x > o.Box.X2 || y < o.Box.Y1 || y > o.Box.Y2 {
		return false
	}
	mx := (x - o.Box.X1) * o.MaskW / (o.Box.X2 - o.Box.X1 + 1)
	my := (y - o.Box.Y1) * o.MaskH / (o.Box.Y2 - o.Box.Y1 + 1)
	return o.Mask[my*o.MaskW+mx] == 1
}

// Waterline is the median topmost masked row over frame columns x0..x1, across
// all objects; ok is false when no column has a masked pixel.
func (r *Result) Waterline(x0, x1 int) (y int, ok bool) {
	var tops []int
	for x := max(x0, 0); x <= min(x1, r.FrameW-1); x++ {
		top := -1
		for i := range r.Objects {
			o := &r.Objects[i]
			if x < o.Box.X1 || x > o.Box.X2 {
				continue
			}
			for y := o.Box.Y1; y <= o.Box.Y2 && (top < 0 || y < top); y++ {
				if o.At(x, y) {
					top = y
					break
				}
			}
		}
		if top >= 0 {
			tops = append(tops, top)
		}
	}
	if len(tops) == 0 {
		return 0, false
	}
	slices.Sort(tops)
	return tops[len(tops)/2], true
}

// Area is the object's mask area in frame pixels.
func (o *Object) Area() float64 {
	on := 0
	for _, v := range o.Mask {
		on += int(v)
	}
	boxArea := float64((o.Box.X2 - o.Box.X1 + 1) * (o.Box.Y2 - o.Box.Y1 + 1))
	return float64(on) / float64(len(o.Mask)) * boxArea
}
