package agent

import (
	"strings"
	"testing"

	"github.com/spartanoscar/vigiax/server/internal/alert"
)

func f64(v float64) *float64 { return &v }

func facts() Facts {
	return Facts{RuleLevel: alert.Alert, LevelCm: f64(192), Rise5: f64(3.6), Rise30: f64(1.2),
		Lines: map[string]any{"rules": map[string]any{"alert": map[string]any{"level_cm": 180.0}, "evacuate": map[string]any{"level_cm": 260.0}}}}
}

func TestCheckAcceptsHonestAnswers(t *testing.T) {
	for _, d := range []Decision{
		{Level: "alert", Action: "hold", Reason: "Water at 192 cm is above the 180 cm line and rising 18 cm in 5 minutes.", CitedLevelCm: f64(192), CitedRise: f64(3.6)},
		{Level: "evacuate", Action: "raise", Reason: "Two people stand on the bank while the water rises fast.", CitedRise: f64(3.5)},
	} {
		if p := Check(d, facts()); len(p) > 0 {
			t.Errorf("%+v rejected: %v", d, p)
		}
	}
}

func TestCheckRejects(t *testing.T) {
	cases := map[string]Decision{
		"below":        {Level: "attention", Action: "hold", Reason: "ok"},
		"hold-moves":   {Level: "evacuate", Action: "hold", Reason: "ok"},
		"raise-same":   {Level: "alert", Action: "raise", Reason: "ok"},
		"wrong level":  {Level: "alert", Action: "hold", Reason: "ok", CitedLevelCm: f64(250)},
		"wrong rise":   {Level: "alert", Action: "hold", Reason: "ok", CitedRise: f64(17)},
		"invented cm":  {Level: "alert", Action: "hold", Reason: "The water rose 75 cm in ten minutes."},
		"bad level":    {Level: "panic", Action: "hold", Reason: "ok"},
		"empty reason": {Level: "alert", Action: "hold", Reason: " "},
	}
	for name, d := range cases {
		if p := Check(d, facts()); len(p) == 0 {
			t.Errorf("%s: accepted %+v", name, d)
		} else if name == "invented cm" && !strings.Contains(p[0], "75") {
			t.Errorf("%s: %v", name, p)
		}
	}
}
