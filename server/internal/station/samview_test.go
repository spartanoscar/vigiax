package station

import (
	"bytes"
	"image"
	"image/jpeg"
	"image/png"
	"testing"
	"time"

	"github.com/spartanoscar/vigiax/server/internal/sam"
)

func TestMaskPNGMarksOnlyMatchedPixels(t *testing.T) {
	// A 10×6 frame with one object over x 2..5, y 3..4 whose mask covers its
	// left half only.
	r := &sam.Result{FrameW: 10, FrameH: 6, Objects: []sam.Object{{
		Box: sam.Box{X1: 2, Y1: 3, X2: 5, Y2: 4}, MaskW: 4, MaskH: 2,
		Mask: []byte{1, 1, 0, 0, 1, 1, 0, 0},
	}}}
	b, err := maskPNG(r, 10, 6)
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	if got := img.Bounds().Size(); got.X != 10 || got.Y != 6 {
		t.Fatalf("mask is %v, want the frame size 10×6", got)
	}
	alpha := func(x, y int) uint32 { _, _, _, a := img.At(x, y).RGBA(); return a }
	for _, p := range [][2]int{{2, 3}, {3, 4}} {
		if alpha(p[0], p[1]) == 0 {
			t.Errorf("pixel %v is water but transparent", p)
		}
	}
	for _, p := range [][2]int{{4, 3}, {0, 0}, {9, 5}} {
		if alpha(p[0], p[1]) != 0 {
			t.Errorf("pixel %v is not water but drawn", p)
		}
	}
}

func TestLastViewSurvivesARestart(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, "view-test")
	if err != nil {
		t.Fatal(err)
	}
	r := &sam.Result{FrameW: 2, FrameH: 2, Objects: []sam.Object{{Box: sam.Box{X2: 1, Y2: 1}, MaskW: 1, MaskH: 1, Mask: []byte{1}}}}
	at := time.Date(2026, 10, 29, 15, 0, 0, 0, time.UTC)
	s.keepView("mud", []byte("jpeg"), r, Reading{T: at, Confidence: 0.9})

	again, err := Open(dir, "view-test")
	if err != nil {
		t.Fatal(err)
	}
	v := again.LastView()
	if v == nil || !v.T.Equal(at) || v.Prompt != "mud" || v.Reading.Confidence != 0.9 || string(v.Frame) != "jpeg" || len(v.Mask) == 0 {
		t.Fatalf("view after reopening = %+v", v)
	}
}

// A frame where SAM found nothing (a camera blind at night) still replaces the
// view, with an empty mask, so the picture always matches the reading next to it.
func TestBlindFrameReplacesTheView(t *testing.T) {
	s, err := Open(t.TempDir(), "blind-test")
	if err != nil {
		t.Fatal(err)
	}
	day := &sam.Result{FrameW: 2, FrameH: 2, Objects: []sam.Object{{Box: sam.Box{X2: 1, Y2: 1}, MaskW: 1, MaskH: 1, Mask: []byte{1}}}}
	s.keepView("flood water", []byte("day"), day, Reading{T: time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC), Confidence: 0.94})

	var night bytes.Buffer
	if err := jpeg.Encode(&night, image.NewGray(image.Rect(0, 0, 8, 4)), nil); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 29, 1, 0, 0, 0, time.UTC)
	s.keepView("flood water", night.Bytes(), &sam.Result{}, Reading{T: at, Blind: true})

	v := s.LastView()
	if v == nil || !v.T.Equal(at) || !v.Reading.Blind || !bytes.Equal(v.Frame, night.Bytes()) {
		t.Fatalf("view after a blind frame = %+v", v)
	}
	m, err := png.Decode(bytes.NewReader(v.Mask))
	if err != nil {
		t.Fatal(err)
	}
	if got := m.Bounds().Size(); got.X != 8 || got.Y != 4 {
		t.Fatalf("mask is %v, want the frame size 8×4", got)
	}
	for y := 0; y < 4; y++ {
		for x := 0; x < 8; x++ {
			if _, _, _, a := m.At(x, y).RGBA(); a != 0 {
				t.Fatalf("pixel %d,%d drawn on a frame where SAM found nothing", x, y)
			}
		}
	}
}
