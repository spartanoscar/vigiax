package station

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	_ "image/jpeg" // frame size when SAM found nothing
	"image/png"
	"os"
	"time"

	"github.com/spartanoscar/vigiax/server/internal/sam"
)

// View is what SAM saw on the station's last measured frame: the frame itself
// and a mask of everything it matched, so a person can check the segmentation
// against the picture (and recalibrate when they disagree).
type View struct {
	T       time.Time `json:"t"`
	Prompt  string    `json:"prompt"`  // the phrase that found the water ("flood water", "mud", ...)
	Objects int       `json:"objects"` // matches SAM returned
	Reading Reading   `json:"reading"` // what was measured on this frame (not the station's latest)
	Frame   []byte    `json:"-"`       // the JPEG that was measured
	Mask    []byte    `json:"-"`       // PNG, frame-sized: matched pixels opaque, the rest transparent
}

var maskColor = color.NRGBA{0, 190, 255, 255}

// maskPNG draws every object's mask on one w×h transparent image.
func maskPNG(r *sam.Result, w, h int) ([]byte, error) {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for i := range r.Objects {
		o := &r.Objects[i]
		for y := max(o.Box.Y1, 0); y <= min(o.Box.Y2, h-1); y++ {
			for x := max(o.Box.X1, 0); x <= min(o.Box.X2, w-1); x++ {
				if o.At(x, y) {
					img.SetNRGBA(x, y, maskColor)
				}
			}
		}
	}
	var b bytes.Buffer
	if err := (&png.Encoder{CompressionLevel: png.BestSpeed}).Encode(&b, img); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// keepView stores the frame, mask and reading of a measurement, in memory and
// on disk (so the view survives a restart of the service). A frame where SAM
// found nothing (a blind camera at night) is kept too, with an empty mask: the
// view is always the last frame, matching the reading shown with it. Failing
// to draw or save it never fails the measurement.
func (s *Station) keepView(prompt string, jpeg []byte, r *sam.Result, rd Reading) {
	w, h := r.FrameW, r.FrameH
	if w == 0 {
		c, _, err := image.DecodeConfig(bytes.NewReader(jpeg))
		if err != nil {
			return
		}
		w, h = c.Width, c.Height
	}
	mask, err := maskPNG(r, w, h)
	if err != nil {
		return
	}
	v := &View{T: rd.T, Prompt: prompt, Objects: len(r.Objects), Reading: rd, Frame: jpeg, Mask: mask}
	s.mu.Lock()
	s.view = v
	s.mu.Unlock()
	if s.dir == "" {
		return
	}
	meta, _ := json.Marshal(v)
	_ = os.WriteFile(s.path(".sam.jpg"), jpeg, 0o644)
	_ = os.WriteFile(s.path(".sam.png"), mask, 0o644)
	_ = os.WriteFile(s.path(".sam.json"), meta, 0o644)
}

// LastView is what SAM saw on the last measured frame, or nil before the first.
func (s *Station) LastView() *View {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.view == nil && s.dir != "" {
		var v View
		b, err := os.ReadFile(s.path(".sam.json"))
		if err != nil || json.Unmarshal(b, &v) != nil {
			return nil
		}
		if v.Frame, err = os.ReadFile(s.path(".sam.jpg")); err != nil {
			return nil
		}
		if v.Mask, err = os.ReadFile(s.path(".sam.png")); err != nil {
			return nil
		}
		s.view = &v
	}
	return s.view
}
