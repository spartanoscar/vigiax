package main

import (
	"testing"
	"time"

	"github.com/spartanoscar/vigiax/server/internal/alert"
)

// run feeds levels (one frame every 5 s) through the service's rules and
// returns the events by kind and level.
func run(t *testing.T, levels []float64) []alert.Event {
	t.Helper()
	s := testServer(t)
	st, err := s.station("twin-test01")
	if err != nil {
		t.Fatal(err)
	}
	t0 := time.Date(2026, 10, 28, 20, 0, 0, 0, time.UTC)
	for i, lv := range levels {
		lv := lv
		rd := stationReading(t0.Add(time.Duration(i)*5*time.Second), lv)
		if err := st.Add(rd); err != nil {
			t.Fatal(err)
		}
		s.checkAlerts(st, rd)
	}
	return s.alerts("twin-test01").events
}

func TestTwinFloodRaisesWithoutJumps(t *testing.T) {
	// The twin's 2-minute surge: 45 → 320 cm with a smoothstep, 24 frames.
	var lv []float64
	for i := range 40 {
		k := min(1, float64(i)/24)
		lv = append(lv, 45+275*k*k*(3-2*k))
	}
	evs := run(t, lv)
	var raised alert.Level
	for _, e := range evs {
		if e.Kind == "jump" {
			t.Fatalf("a real flood was flagged as a jump: %+v", e)
		}
		if e.Kind == "raise" {
			raised = e.To
		}
	}
	if raised != alert.Evacuate {
		t.Fatalf("the surge reached %v, want evacuate; events %+v", raised, evs)
	}
}

func TestCameraJumpIsFlaggedNotARise(t *testing.T) {
	// Steady at 63 cm, then the camera is knocked and reads 150 cm: below the
	// alert line (180), above attention (100). The rise rate must not fire
	// alert or evacuate; the attention line does after its confirmation.
	lv := []float64{63, 63, 63, 63, 63, 63, 63, 63, 150, 150, 150, 150, 150, 150}
	evs := run(t, lv)
	if len(evs) == 0 || evs[0].Kind != "jump" {
		t.Fatalf("no jump flagged: %+v", evs)
	}
	for _, e := range evs {
		if e.Kind == "raise" && e.To > alert.Attention {
			t.Fatalf("the jump raised %v through the rise rate: %+v", e.To, e)
		}
	}
	if evs[len(evs)-1].To != alert.Attention {
		t.Fatalf("the level line did not apply after the jump: %+v", evs)
	}
}
