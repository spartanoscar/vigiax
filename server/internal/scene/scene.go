// Package scene asks Muse Spark what a frame shows that a water mask cannot:
// mud, debris, people and vehicles near the water. Called on events only.
package scene

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/spartanoscar/vigiax/server/internal/meta"
)

const (
	DefaultModel = "muse-spark-1.3"
	// "minimal" read the same scenes as "low" in about a third of the time and
	// output tokens on the first test images (2026-10-26).
	DefaultEffort = "minimal"
)

type Reading struct {
	WaterVisible      bool   `json:"water_visible"`
	WaterColor        string `json:"water_color"`
	Surface           string `json:"surface"`
	Debris            string `json:"debris"`
	PeopleNearWater   int    `json:"people_near_water"`
	VehiclesNearWater int    `json:"vehicles_near_water"`
	Lighting          string `json:"lighting"`
	Visibility        string `json:"visibility"`
	Summary           string `json:"summary"`

	Model   string        `json:"model"`
	Latency time.Duration `json:"-"`
	Usage   meta.Usage    `json:"-"`
	CostUSD float64       `json:"-"`
}

const instructions = `You watch a fixed camera pointed at a mountain stream for a flash-flood early-warning system.
Report only what is visible in this frame; when something cannot be seen, say so with the "not_visible" value instead of guessing.
Count people and vehicles that are in the water or within about 10 meters of its edge.
summary: one plain sentence a rescuer would find useful, no speculation about the future.`

var schema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "water_visible":       {"type": "boolean"},
    "water_color":         {"type": "string", "enum": ["clear", "turbid", "muddy", "not_visible"]},
    "surface":             {"type": "string", "enum": ["calm", "rippled", "turbulent", "not_visible"]},
    "debris":              {"type": "string", "enum": ["none", "few", "many", "not_visible"]},
    "people_near_water":   {"type": "integer"},
    "vehicles_near_water": {"type": "integer"},
    "lighting":            {"type": "string", "enum": ["day", "dusk", "night"]},
    "visibility":          {"type": "string", "enum": ["good", "rain", "fog", "obstructed"]},
    "summary":             {"type": "string"}
  },
  "required": ["water_visible", "water_color", "surface", "debris", "people_near_water",
               "vehicles_near_water", "lighting", "visibility", "summary"],
  "additionalProperties": false
}`)

// Read describes one JPEG frame. effort is the reasoning effort ("" keeps the model default).
func Read(ctx context.Context, c *meta.Client, model, effort string, jpeg []byte) (*Reading, error) {
	req := &meta.Request{
		Model:        model,
		Instructions: instructions,
		Input:        []meta.Message{meta.User(meta.Text("Describe this frame."), meta.JPEG(jpeg))},
	}
	req.WithSchema("scene_reading", schema)
	if effort != "" {
		req.Reasoning = &struct {
			Effort string `json:"effort"`
		}{effort}
	}
	res, err := c.Create(ctx, req)
	if err != nil {
		return nil, err
	}
	var r Reading
	if err := json.Unmarshal([]byte(res.OutputText()), &r); err != nil {
		return nil, fmt.Errorf("scene: %w: %q", err, res.OutputText())
	}
	r.Model, r.Latency, r.Usage, r.CostUSD = res.Model, res.Latency, res.Usage, res.CostUSD()
	return &r, nil
}
