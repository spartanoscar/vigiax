package usgs

import (
	"testing"
	"time"
)

func TestFrameTime(t *testing.T) {
	got, err := FrameTime("OK_North_Canadian_River_near_El_Reno___2026-10-27T23-00-05Z.jpg")
	if want := time.Date(2026, 10, 27, 23, 0, 5, 0, time.UTC); err != nil || !got.Equal(want) {
		t.Fatalf("got %v, %v; want %v", got, err, want)
	}
	if _, err := FrameTime("no-time.jpg"); err == nil {
		t.Fatal("accepted a name without time")
	}
}

func TestNearest(t *testing.T) {
	t0 := time.Date(2026, 10, 27, 23, 0, 0, 0, time.UTC)
	g := map[time.Time]float64{t0: 3.9, t0.Add(15 * time.Minute): 4.1}
	if v, ok := Nearest(g, t0.Add(10*time.Minute), 30*time.Minute); !ok || v != 4.1 {
		t.Fatalf("got %v %v", v, ok)
	}
	if _, ok := Nearest(g, t0.Add(2*time.Hour), 30*time.Minute); ok {
		t.Fatal("matched a reading 2 h away")
	}
}
