package scene

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/spartanoscar/vigiax/server/internal/meta"
)

// Change is what Muse Spark sees as different between the station's reference
// frame (the stream on a normal day) and the frame now. A frame on its own
// shows mud and rocks; against the reference it shows what is new: a channel
// dammed by logs, a sandbar gone under water, a bridge that is no longer there.
type Change struct {
	WaterChange       string   `json:"water_change"`
	ChannelBlocked    string   `json:"channel_blocked"`
	NewMaterial       []string `json:"new_material"`
	StructuresChanged string   `json:"structures_changed"`
	Notable           string   `json:"notable_changes"`
	Danger            string   `json:"danger_change"`

	Model   string        `json:"model"`
	Latency time.Duration `json:"-"`
	CostUSD float64       `json:"-"`
}

const compareInstructions = `Two frames from the same fixed camera over a mountain stream, for a flash-flood early-warning system.
The FIRST is the stream on a normal day (reference). The SECOND is now. Report only what changed from the first to
the second. Ignore changes of light, weather and small camera noise.
channel_blocked: logs, rocks or mud piled across the channel, damming it.
new_material: things in the channel or in the flow now that were not there before.
structures_changed: houses, walls, bridges or roads that look damaged or are no longer there.
notable_changes: one plain sentence. danger_change: how much more dangerous the stream looks now.`

var compareSchema = json.RawMessage(`{
  "type": "object",
  "additionalProperties": false,
  "required": ["water_change", "channel_blocked", "new_material", "structures_changed", "notable_changes", "danger_change"],
  "properties": {
    "water_change":       {"type": "string", "enum": ["none", "higher", "lower", "muddier", "clearer", "not_visible"]},
    "channel_blocked":    {"type": "string", "enum": ["no", "partly", "yes", "not_visible"]},
    "new_material":       {"type": "array", "items": {"type": "string", "enum": ["branches", "logs_or_whole_trees",
                           "rocks_or_boulders", "mud_deposits", "building_materials", "vehicles", "other"]}},
    "structures_changed": {"type": "string", "enum": ["none_visible", "unchanged", "damaged", "missing"]},
    "notable_changes":    {"type": "string"},
    "danger_change":      {"type": "string", "enum": ["none", "minor", "significant"]}
  }
}`)

// Compare asks Muse Spark what changed from the reference frame to now.
func Compare(ctx context.Context, c *meta.Client, model, effort string, reference, now []byte) (*Change, error) {
	req := &meta.Request{Model: model, Instructions: compareInstructions,
		Input: []meta.Message{meta.User(meta.Text("Reference (normal day):"), meta.JPEG(reference), meta.Text("Now:"), meta.JPEG(now))}}
	req.WithSchema("change", compareSchema)
	if effort != "" {
		req.Reasoning = &struct {
			Effort string `json:"effort"`
		}{effort}
	}
	res, err := c.Create(ctx, req)
	if err != nil {
		return nil, err
	}
	var ch Change
	if err := json.Unmarshal([]byte(res.OutputText()), &ch); err != nil {
		return nil, fmt.Errorf("scene: compare answer: %w", err)
	}
	ch.Model, ch.Latency, ch.CostUSD = model, res.Latency, res.CostUSD()
	return &ch, nil
}

// Floor is the alert level the change calls for by itself (0-3, as in Reading.Floor).
func (ch *Change) Floor() (level int, why string) {
	has := func(k string) bool {
		for _, m := range ch.NewMaterial {
			if m == k {
				return true
			}
		}
		return false
	}
	switch {
	case ch.StructuresChanged == "damaged" || ch.StructuresChanged == "missing":
		return 3, "against the normal-day frame, a house, bridge or road is " + ch.StructuresChanged
	case ch.ChannelBlocked == "yes" || ch.ChannelBlocked == "partly":
		return 2, "against the normal-day frame, the channel is " + map[string]string{"yes": "blocked", "partly": "partly blocked"}[ch.ChannelBlocked] + ": possible damming"
	case ch.Danger == "significant" && (has("logs_or_whole_trees") || has("rocks_or_boulders") || has("mud_deposits") || has("building_materials")):
		return 2, "against the normal-day frame, the flow carries new logs, rocks or mud"
	}
	return 0, ""
}
