// Package gauge turns pixel rows into water levels. A calibration maps a frame
// row to a value on the staff gauge; it comes from Muse Spark pointing at the
// numbers printed on the gauge, checked against the gauge mask from SAM 3.1, or
// from two points entered by hand.
package gauge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/spartanoscar/vigiax/server/internal/meta"
	"github.com/spartanoscar/vigiax/server/internal/sam"
)

// Label is a number printed on the gauge and the frame row of its center.
type Label struct {
	Value float64 `json:"value"`
	Unit  string  `json:"unit"`
	X     float64 `json:"x"`
	Y     float64 `json:"y"`
}

// Calibration maps a frame row to a gauge value: value = (ZeroY - y) / PxPerUnit.
// Rows grow downward, so a higher water surface has a smaller y.
type Calibration struct {
	PxPerUnit float64 `json:"px_per_unit"`
	ZeroY     float64 `json:"zero_y"`
	Unit      string  `json:"unit"`   // FT, M or CM
	ColX1     int     `json:"col_x1"` // gauge columns, where the waterline is read
	ColX2     int     `json:"col_x2"`
	Source    string  `json:"source"` // "labels" or "manual"
	RMSE      float64 `json:"rmse"`   // fit residual, in gauge units
	Labels    []Label `json:"labels,omitempty"`
}

// Value is the gauge reading at frame row y.
func (c *Calibration) Value(y float64) float64 { return (c.ZeroY - y) / c.PxPerUnit }

// Cm converts a gauge value to centimetres.
func (c *Calibration) Cm(v float64) float64 { return ToCm(v, c.Unit) }

// ToCm converts a value in unit (FT, M or CM) to centimetres.
func ToCm(v float64, unit string) float64 {
	switch strings.ToUpper(unit) {
	case "FT":
		return v * 30.48
	case "M":
		return v * 100
	default:
		return v
	}
}

// LevelCm is the water level in centimetres for a waterline at frame row y.
func (c *Calibration) LevelCm(y float64) float64 { return c.Cm(c.Value(y)) }

// ErrRejected marks a calibration that failed its checks.
var ErrRejected = errors.New("gauge: calibration rejected")

// maxRMSE is the largest fit residual accepted, in gauge units (a tenth of the
// spacing between printed numbers when they are one unit apart).
const maxRMSE = 0.1

// Fit computes a calibration from labels, checked against the gauge box found by
// SAM: at least three labels, all inside the box rows, values strictly ordered by
// row, and evenly spaced (least-squares residual under maxRMSE).
func Fit(labels []Label, gaugeBox sam.Box) (*Calibration, error) {
	if len(labels) < 3 {
		return nil, fmt.Errorf("%w: %d labels, need at least 3", ErrRejected, len(labels))
	}
	ls := append([]Label(nil), labels...)
	sort.Slice(ls, func(i, j int) bool { return ls[i].Y < ls[j].Y })
	margin := 0.02 * float64(gaugeBox.Y2-gaugeBox.Y1)
	for i, l := range ls {
		if l.Y < float64(gaugeBox.Y1)-margin || l.Y > float64(gaugeBox.Y2)+margin {
			return nil, fmt.Errorf("%w: label %g at row %.0f is outside the gauge rows %d-%d",
				ErrRejected, l.Value, l.Y, gaugeBox.Y1, gaugeBox.Y2)
		}
		if i > 0 && l.Value >= ls[i-1].Value {
			return nil, fmt.Errorf("%w: label %g at row %.0f is not below %g", ErrRejected, l.Value, l.Y, ls[i-1].Value)
		}
	}
	// value = a + b*y; b = -1/PxPerUnit.
	n := float64(len(ls))
	var sy, sv, syy, syv float64
	for _, l := range ls {
		sy += l.Y
		sv += l.Value
		syy += l.Y * l.Y
		syv += l.Y * l.Value
	}
	b := (n*syv - sy*sv) / (n*syy - sy*sy)
	a := (sv - b*sy) / n
	var sse float64
	for _, l := range ls {
		d := l.Value - (a + b*l.Y)
		sse += d * d
	}
	c := &Calibration{
		PxPerUnit: -1 / b,
		ZeroY:     a * (-1 / b),
		Unit:      strings.ToUpper(ls[0].Unit),
		ColX1:     gaugeBox.X1,
		ColX2:     gaugeBox.X2,
		Source:    "labels",
		RMSE:      math.Sqrt(sse / n),
		Labels:    ls,
	}
	if c.RMSE > maxRMSE {
		return nil, fmt.Errorf("%w: labels are not evenly spaced (residual %.2f %s)", ErrRejected, c.RMSE, c.Unit)
	}
	return c, nil
}

// TwoPoint builds a calibration from two rows with known values, for gauges the
// model cannot read (night, small print) and for the digital twin.
func TwoPoint(y1, v1, y2, v2 float64, unit string, colX1, colX2 int) (*Calibration, error) {
	if y1 == y2 || v1 == v2 || (v1 > v2) != (y1 < y2) {
		return nil, fmt.Errorf("%w: points must differ and the higher value must be higher in the frame", ErrRejected)
	}
	px := (y2 - y1) / (v1 - v2)
	return &Calibration{PxPerUnit: px, ZeroY: y1 + v1*px, Unit: strings.ToUpper(unit),
		ColX1: colX1, ColX2: colX2, Source: "manual"}, nil
}

const labelPrompt = `Point to every number printed on the water-level staff gauge in this image. ` +
	`For each number, answer with its value, the unit printed next to it ("FT", "M", "CM", or "" if none), ` +
	`and the center of the printed number as x and y on a normalized 0-1000 grid with (0,0) at the top-left ` +
	`of the image. Do not miss any number.`

var labelSchema = json.RawMessage(`{
  "type": "object",
  "properties": {"labels": {"type": "array", "items": {"type": "object",
    "properties": {"value": {"type": "number"}, "unit": {"type": "string"},
                   "x": {"type": "integer"}, "y": {"type": "integer"}},
    "required": ["value", "unit", "x", "y"], "additionalProperties": false}}},
  "required": ["labels"], "additionalProperties": false
}`)

// ReadLabels asks Muse Spark for the gauge numbers and returns them in frame
// pixels. Rows proved accurate with "minimal" reasoning on the first test
// gauge; columns did not, so callers take the columns from SAM's gauge box.
func ReadLabels(ctx context.Context, c *meta.Client, model string, jpeg []byte, frameW, frameH int) ([]Label, *meta.Response, error) {
	req := &meta.Request{
		Model: model,
		Input: []meta.Message{meta.User(meta.JPEG(jpeg), meta.Text(labelPrompt))},
		Reasoning: &struct {
			Effort string `json:"effort"`
		}{"minimal"},
	}
	req.WithSchema("gauge_labels", labelSchema)
	res, err := c.Create(ctx, req)
	if err != nil {
		return nil, nil, err
	}
	var out struct{ Labels []Label }
	if err := json.Unmarshal([]byte(res.OutputText()), &out); err != nil {
		return nil, res, fmt.Errorf("gauge labels: %w", err)
	}
	for i := range out.Labels {
		out.Labels[i].X *= float64(frameW) / 1000
		out.Labels[i].Y *= float64(frameH) / 1000
	}
	return out.Labels, res, nil
}

// GaugePrompt is the SAM 3.1 concept that finds staff gauges.
const GaugePrompt = "measuring ruler"

// Calibrate finds the gauge with SAM, reads its numbers with Muse Spark and fits
// them. It returns the costs of both calls whether or not the fit passes.
func Calibrate(ctx context.Context, c *meta.Client, model string, jpeg []byte) (*Calibration, float64, error) {
	r, err := sam.Segment(ctx, c, jpeg, GaugePrompt)
	if err != nil {
		return nil, 0, err
	}
	cost := r.CostUSD
	if len(r.Objects) == 0 {
		return nil, cost, fmt.Errorf("%w: no gauge found in the frame", ErrRejected)
	}
	best := r.Objects[0]
	for _, o := range r.Objects[1:] {
		if o.Confidence > best.Confidence {
			best = o
		}
	}
	labels, res, err := ReadLabels(ctx, c, model, jpeg, r.FrameW, r.FrameH)
	if res != nil {
		cost += res.CostUSD()
	}
	if err != nil {
		return nil, cost, err
	}
	cal, err := Fit(labels, best.Box)
	return cal, cost, err
}
