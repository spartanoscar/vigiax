package gauge

import (
	"errors"
	"math"
	"testing"

	"github.com/spartanoscar/vigiax/server/internal/sam"
)

// Darby Creek gauge (1280×1675 frame): the gauge box SAM 3.1 returned, and the
// label rows Muse Spark returned on a 0-1000 grid with "minimal" and "low"
// reasoning (2026-10-27). The "low" rows start above the gauge.
var (
	darbyBox = sam.Box{X1: 636, Y1: 397, X2: 676, Y2: 1470}
	minimal  = []float64{270, 313, 356, 399, 442, 485, 528, 571, 614, 657, 700, 743, 786, 829}
	low      = []float64{205, 250, 295, 340, 385, 430, 475, 520, 565, 610, 655, 700, 745, 790}
)

func darbyLabels(rows []float64) []Label {
	var ls []Label
	for i, r := range rows {
		ls = append(ls, Label{Value: float64(17 - i), Unit: "FT", Y: r * 1675 / 1000})
	}
	return ls
}

func TestFitAcceptsConsistentLabels(t *testing.T) {
	c, err := Fit(darbyLabels(minimal), darbyBox)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(c.PxPerUnit-72.0) > 0.5 || c.Unit != "FT" || c.RMSE > 0.01 {
		t.Fatalf("got %+v", c)
	}
	if v := c.Value(1388); math.Abs(v-4) > 0.05 {
		t.Fatalf("label 4 row reads %.2f ft", v)
	}
	if cm := c.LevelCm(c.ZeroY - c.PxPerUnit); math.Abs(cm-30.48) > 1e-9 {
		t.Fatalf("1 ft = %.2f cm", cm)
	}
	if c.ColX1 != 636 || c.ColX2 != 676 {
		t.Fatal("waterline columns must come from the gauge box")
	}
}

func TestFitRejects(t *testing.T) {
	shuffled := darbyLabels(minimal)
	shuffled[3].Y, shuffled[4].Y = shuffled[4].Y, shuffled[3].Y
	uneven := darbyLabels(minimal)
	uneven[6].Y += 40
	for name, ls := range map[string][]Label{
		"labels above the gauge": darbyLabels(low),
		"too few labels":         darbyLabels(minimal)[:2],
		"values out of order":    shuffled,
		"uneven spacing":         uneven,
	} {
		if _, err := Fit(ls, darbyBox); !errors.Is(err, ErrRejected) {
			t.Errorf("%s: got %v, want rejection", name, err)
		}
	}
}

func TestTwoPoint(t *testing.T) {
	c, err := TwoPoint(100, 2, 300, 1, "m", 10, 20)
	if err != nil {
		t.Fatal(err)
	}
	if c.PxPerUnit != 200 || c.Value(200) != 1.5 || c.LevelCm(500) != 0 {
		t.Fatalf("got %+v", c)
	}
	if _, err := TwoPoint(100, 1, 300, 2, "m", 0, 0); err == nil {
		t.Fatal("accepted a higher value lower in the frame")
	}
}
