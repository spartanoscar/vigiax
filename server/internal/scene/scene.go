// Package scene asks Muse Spark what a frame shows that a water mask cannot:
// mud, debris, people and vehicles near the water. Called on events only.
package scene

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/spartanoscar/vigiax/server/internal/meta"
)

const (
	DefaultModel = "muse-spark-1.3"
	// "low" sees what the water carries and what it damaged better than
	// "minimal" (eval/results/scene-2026-10: 4 of 4 debris kinds and 4 of 5
	// damaged sites, against 3 and 3) for about $0.002 more per frame; the scene
	// is read on events only.
	DefaultEffort = "low"
)

type Reading struct {
	WaterVisible      bool     `json:"water_visible"`
	Flow              string   `json:"flow"` // clear_water, muddy_water, debris_flow or not_visible
	WaterColor        string   `json:"water_color"`
	Surface           string   `json:"surface"`
	Debris            string   `json:"debris"`
	DebrisKinds       []string `json:"debris_kinds"`
	Structures        string   `json:"structures"`
	PeopleNearWater   int      `json:"people_near_water"`
	VehiclesNearWater int      `json:"vehicles_near_water"`
	Lighting          string   `json:"lighting"`
	Visibility        string   `json:"visibility"`
	Summary           string   `json:"summary"`

	Model   string        `json:"model"`
	Latency time.Duration `json:"-"`
	Usage   meta.Usage    `json:"-"`
	CostUSD float64       `json:"-"`
}

const instructions = `You watch a fixed camera pointed at a mountain stream for a flash-flood early-warning system.
Report only what is visible in this frame; when something cannot be seen, say so with the "not_visible" value instead of guessing.

flow tells water from a debris flow, the deadliest case:
- clear_water / muddy_water: it still moves like water, even if brown, fast and carrying some branches or logs;
- debris_flow: a thick slurry of mud that moves like wet concrete and carries rocks or boulders, whole trees or
  building materials; also called mudflow, lahar or "avenida torrencial". Choose it only when the flow itself looks
  like that, not for debris left on the banks after the water went down.
debris_kinds: everything carried by the flow or piled in the channel that you can see.
structures: houses, walls, bridges or roads in view, and whether they look intact, threatened by the flow, or
  already damaged or destroyed.
Count people and vehicles that are in the water or within about 10 meters of its edge.
summary: one plain sentence a rescuer would find useful, no speculation about the future.`

var schema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "water_visible":       {"type": "boolean"},
    "flow":                {"type": "string", "enum": ["clear_water", "muddy_water", "debris_flow", "not_visible"]},
    "water_color":         {"type": "string", "enum": ["clear", "turbid", "muddy", "not_visible"]},
    "surface":             {"type": "string", "enum": ["calm", "rippled", "turbulent", "not_visible"]},
    "debris":              {"type": "string", "enum": ["none", "few", "many", "not_visible"]},
    "debris_kinds":        {"type": "array", "items": {"type": "string", "enum": ["branches", "logs_or_whole_trees",
                            "rocks_or_boulders", "building_materials", "vehicles", "animals", "other"]}},
    "structures":          {"type": "string", "enum": ["none_visible", "intact", "at_risk", "damaged_or_destroyed"]},
    "people_near_water":   {"type": "integer"},
    "vehicles_near_water": {"type": "integer"},
    "lighting":            {"type": "string", "enum": ["day", "dusk", "night"]},
    "visibility":          {"type": "string", "enum": ["good", "rain", "fog", "obstructed"]},
    "summary":             {"type": "string"}
  },
  "required": ["water_visible", "flow", "water_color", "surface", "debris", "debris_kinds", "structures", "people_near_water",
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

// Floor is the alert level a scene calls for by itself, with fixed rules, and
// why. It does not depend on the water level: a debris flow or a house in the
// flow's path is an emergency even when the gauge is buried or reads low.
// Levels: 0 normal, 1 attention, 2 alert, 3 evacuate (as in package alert).
//
// eval/results/scene-2026-10: the model rarely names a debris flow (1 of 3),
// but in those photos it saw the houses at risk or the rocks and trees carried,
// so the rules combine signals instead of trusting one label.
func (r *Reading) Floor() (level int, why string) {
	has := func(k string) bool {
		for _, d := range r.DebrisKinds {
			if d == k {
				return true
			}
		}
		return false
	}
	switch {
	case r.Flow == "debris_flow":
		return 3, "Muse Spark sees a debris flow (thick mud carrying rocks or trees)"
	case r.Structures == "at_risk" || r.Structures == "damaged_or_destroyed":
		return 3, "Muse Spark sees houses, bridges or roads " + strings.ReplaceAll(r.Structures, "_", " ")
	case has("building_materials") || has("vehicles") || has("animals"):
		return 2, "Muse Spark sees building materials, vehicles or animals carried by the water"
	case (has("logs_or_whole_trees") || has("rocks_or_boulders")) && r.Flow == "muddy_water" && r.Surface == "turbulent":
		return 2, "Muse Spark sees logs or rocks in turbulent muddy water"
	}
	return 0, ""
}
