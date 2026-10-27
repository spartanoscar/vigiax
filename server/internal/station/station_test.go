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
