package station

import "github.com/spartanoscar/vigiax/server/internal/alert"

// Live are the real cameras the service follows on its own. Their rating
// comes from eval/results/el-reno-2026-10: a 3.66 m flood in October 2026,
// test MAE 6.9 cm against the USGS gage.
var Live = []Config{{
	ID:     "el-reno",
	Name:   "North Canadian River near El Reno, OK (USGS)",
	Prompt: DefaultPrompt,
	Rating: &Rating{Intercept: -5.15, Slope: 41.07, Unit: "FT", Fit: "eval/results/el-reno-2026-10"},
	Source: &Source{Kind: "usgs", Camera: "OK_North_Canadian_River_near_El_Reno", Site: "07239500"},
	Alerts: &alert.Config{
		Place: "el río North Canadian en El Reno, Oklahoma", PlaceEn: "the North Canadian River at El Reno, Oklahoma",
		Confirm: 1, RiseWindowMin: 180, LowerAfterMin: 120, // one photo an hour
		DropCmPerMin: ptr(0.5), // 90 cm in 3 hours
		Note:         "Demo lines between today's level (about 1.2 m) and the October 2026 peak (4.6 m); not official flood stages.",
		Rules: alert.Rules{
			Attention: alert.Rule{Enabled: true, LevelCm: ptr(250), RiseCmPerMin: ptr(0.5), Audience: "leader", Channels: []string{"push"},
				Why:    "The river rises 30 cm an hour or passes 2.5 m: someone should watch it.",
				Action: "Vigila el río y prepárate por si sigue subiendo.", ActionEn: "Watch the river and get ready in case it keeps rising."},
			Alert: alert.Rule{Enabled: true, LevelCm: ptr(350), RiseCmPerMin: ptr(1), Audience: "group", Channels: []string{"push", "whatsapp"},
				Why:    "The river rises 60 cm an hour or passes 3.5 m: keep away from the banks.",
				Action: "Aléjate de la orilla y de los cruces bajos.", ActionEn: "Keep away from the banks and low crossings."},
			Evacuate: alert.Rule{Enabled: true, LevelCm: ptr(450), RiseCmPerMin: ptr(2), Audience: "everyone", Channels: []string{"push", "whatsapp", "siren"},
				Why:    "The river is near the October 2026 peak: leave the low areas.",
				Action: "Sal de las zonas bajas ahora.", ActionEn: "Leave the low areas now."},
		},
	},
}}

func ptr(v float64) *float64 { return &v }
