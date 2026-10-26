package sam

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"
)

// testdata/ellipse.json: a synthetic 51×37 mask encoded by Meta's reference
// encoder (meta-sam-parser 0.1.0), with the raster it must decode to.
func loadEllipse(t *testing.T) (w, h int, payload, raster string) {
	t.Helper()
	b, err := os.ReadFile("testdata/ellipse.json")
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		W, H            int
		Payload, Raster string
	}
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	return f.W, f.H, f.Payload, f.Raster
}

func TestDecodeOneBit(t *testing.T) {
	w, h, payload, want := loadEllipse(t)
	got, err := DecodeOneBit(payload, w, h)
	if err != nil {
		t.Fatal(err)
	}
	for i := range got {
		if got[i] != want[i]-'0' {
			t.Fatalf("pixel %d (x=%d y=%d): got %d want %c", i, i%w, i/w, got[i], want[i])
		}
	}
}

func TestDecodeOneBitRejectsCorruption(t *testing.T) {
	w, h, payload, _ := loadEllipse(t)
	if _, err := DecodeOneBit(payload[:len(payload)-1], w, h); err == nil {
		t.Fatal("truncated payload decoded without error")
	}
	if _, err := DecodeOneBit("~"+payload[1:], w, h); err == nil {
		t.Fatal("lossless marker accepted as one_bit")
	}
}

func TestParse(t *testing.T) {
	w, h, payload, _ := loadEllipse(t)
	// Box 102×74 px holding the 51×37 raster: SAM downsampled it by 2.
	line := fmt.Sprintf("<0f>3<|box;x1=100;y1=200;x2=201;y2=273;w=640;h=480;c=0.875|>"+
		"<|mask;x=0;y=0;c=0.875;data=%d,%d,%s|>", h, w, payload)
	r, err := Parse(line)
	if err != nil {
		t.Fatal(err)
	}
	if r.FrameW != 640 || r.FrameH != 480 || len(r.Objects) != 1 {
		t.Fatalf("got frame %dx%d, %d objects", r.FrameW, r.FrameH, len(r.Objects))
	}
	o := r.Objects[0]
	if o.ID != 3 || o.Confidence != 0.875 || o.Box != (Box{100, 200, 201, 273}) {
		t.Fatalf("got %+v", o)
	}
	// Ellipse centre (20, 18) in the raster is (140, 236) in the frame.
	if !o.At(140, 236) || o.At(100, 200) || o.At(99, 236) {
		t.Fatal("At maps the raster to the wrong frame pixels")
	}
	if got := o.Area(); got != 809*4 {
		t.Fatalf("area %v, want %v", got, 809*4)
	}
	if r, _ := Parse(""); len(r.Objects) != 0 {
		t.Fatal("empty output must parse to no objects")
	}
}
