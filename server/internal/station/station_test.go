package station

import (
	"math"
	"testing"
	"time"

	"github.com/spartanoscar/vigiax/server/internal/gauge"
)

func level(cm float64) *float64 { return &cm }

func TestRiseAndReopen(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, "test-1")
	if err != nil {
		t.Fatal(err)
	}
	t0 := time.Date(2026, 10, 27, 15, 0, 0, 0, time.UTC)
	// 2 cm/min for 10 minutes, one reading per minute, plus one without level.
	for i := 0; i <= 10; i++ {
		if err := s.append(Reading{T: t0.Add(time.Duration(i) * time.Minute), LevelCm: level(50 + 2*float64(i))}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.append(Reading{T: t0.Add(10*time.Minute + 30*time.Second)}); err != nil {
		t.Fatal(err)
	}
	now := t0.Add(11 * time.Minute)

	rate, n, ok := s.Rise(now, 10*time.Minute)
	if !ok || n != 9 || math.Abs(rate-2) > 1e-9 { // 15:02..15:10; the window excludes its start
		t.Fatalf("10 min: got %.3f cm/min from %d readings, ok=%v", rate, n, ok)
	}
	if _, _, ok := s.Rise(now, 60*time.Minute); ok {
		t.Fatal("11 minutes of data accepted for a 60-minute window")
	}

	if err := s.SetCalibration(&gauge.Calibration{PxPerUnit: 10, Unit: "CM", Source: "manual"}); err != nil {
		t.Fatal(err)
	}
	re, err := Open(dir, "test-1")
	if err != nil {
		t.Fatal(err)
	}
	if got := len(re.Readings(time.Time{})); got != 12 {
		t.Fatalf("reopened with %d readings, want 12", got)
	}
	if c := re.Config().Calib; c == nil || c.PxPerUnit != 10 {
		t.Fatal("calibration not persisted")
	}
	if _, err := Open(dir, "../etc"); err == nil {
		t.Fatal("accepted an unsafe station id")
	}
}

func TestSetRefSurvivesReopen(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, "ref-1")
	if err != nil {
		t.Fatal(err)
	}
	t0 := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	for i := range 3 {
		if err := s.append(Reading{T: t0.Add(time.Duration(i) * time.Hour), LevelCm: level(130)}); err != nil {
			t.Fatal(err)
		}
	}
	if ok, err := s.SetRef(t0.Add(time.Hour), 119); !ok || err != nil {
		t.Fatalf("SetRef: %v %v", ok, err)
	}
	if ok, _ := s.SetRef(t0.Add(30*time.Minute), 119); ok {
		t.Fatal("SetRef matched a time without a reading")
	}
	s, err = Open(dir, "ref-1")
	if err != nil {
		t.Fatal(err)
	}
	rs := s.Readings(t0.Add(-time.Second))
	if len(rs) != 3 || rs[1].RefCm == nil || *rs[1].RefCm != 119 || rs[0].RefCm != nil || !rs[2].T.Equal(t0.Add(2*time.Hour)) {
		t.Fatalf("after reopen: %+v", rs)
	}
}
