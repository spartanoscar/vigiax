package alert

import (
	"fmt"
	"strings"
	"time"
)

// The messages are fixed templates filled with measured values: no model writes
// the text people receive.

var (
	titleEs = map[Level]string{Normal: "NIVEL NORMAL", Attention: "ATENCIÓN", Alert: "ALERTA DE CRECIENTE", Evacuate: "EVACUAR AHORA"}
	titleEn = map[Level]string{Normal: "BACK TO NORMAL", Attention: "ATTENTION", Alert: "FLASH FLOOD ALERT", Evacuate: "EVACUATE NOW"}
	bogota  = time.FixedZone("COT", -5*3600)
)

// Fields are the values of the WhatsApp template, in order:
// title, place, what is happening, what to do, time.
type Fields [5]string

// Fill builds the message fields for an event, in "es" or "en".
func Fill(c *Config, e Event, lang string) Fields {
	en := lang == "en"
	title := titleEs[e.To]
	if en {
		title = titleEn[e.To]
	}
	fact := facts(c, e, en)
	action := "Sigue atento a nuevos avisos."
	if en {
		action = "Stay tuned for new notices."
	}
	if r := c.rule(e.To); r != nil {
		if en && r.ActionEn != "" {
			action = r.ActionEn
		} else if !en && r.Action != "" {
			action = r.Action
		}
	}
	switch e.Kind {
	case "false_alarm":
		fact = "Una persona en el lugar marcó la alerta como falsa alarma."
		if en {
			fact = "A person on site marked the alert as a false alarm."
		}
	case "jump":
		title = "SALTO EN LA MEDICIÓN"
		fact = "El nivel cambió de golpe entre dos fotos: puede ser la cámara movida o el frente de una creciente."
		action = "Si puedes, revisa la cámara y la quebrada desde un lugar seguro."
		if en {
			title = "MEASUREMENT JUMP"
			fact = "The level changed at once between two frames: the camera may have moved, or a flood front arrived."
			action = "If you can, check the camera and the stream from a safe place."
		}
	case "blind":
		title = "CÁMARA SIN VISIÓN"
		fact = "La cámara no ve el agua (noche, niebla o lente tapado). Se mantiene el último nivel de alerta."
		action = "Si puedes, mira la quebrada desde un lugar seguro."
		if en {
			title = "CAMERA CANNOT SEE"
			fact = "The camera cannot see the water (night, fog or a covered lens). The last alert level is kept."
			action = "If you can, look at the stream from a safe place."
		}
	}
	at := e.T.In(bogota).Format("15:04") // 24 h: "p. m." would end the template sentence twice
	place := c.Place
	if en && c.PlaceEn != "" {
		place = c.PlaceEn
	}
	return Fields{title, place, fact, action, at}
}

func facts(c *Config, e Event, en bool) string {
	var parts []string
	if e.RiseCmPerMin != nil && *e.RiseCmPerMin > 0.05 {
		cm, min := *e.RiseCmPerMin*c.RiseWindowMin, c.RiseWindowMin
		switch {
		case min > 60 && en: // hourly cameras: a rate reads better than a long window
			parts = append(parts, fmt.Sprintf("The water is rising %.0f cm an hour", *e.RiseCmPerMin*60))
		case min > 60:
			parts = append(parts, fmt.Sprintf("El agua sube %.0f cm por hora", *e.RiseCmPerMin*60))
		case en:
			parts = append(parts, fmt.Sprintf("The water rose %.0f cm in %.0f minutes", cm, min))
		default:
			parts = append(parts, fmt.Sprintf("El agua subió %.0f cm en %.0f minutos", cm, min))
		}
	}
	if e.LevelCm != nil {
		m := *e.LevelCm / 100
		switch {
		case en && len(parts) > 0:
			parts = append(parts, fmt.Sprintf("and is at %.1f m", m))
		case en:
			parts = append(parts, fmt.Sprintf("The water is at %.1f m", m))
		case len(parts) > 0:
			parts = append(parts, strings.Replace(fmt.Sprintf("y está a %.1f m", m), ".", ",", 1))
		default:
			parts = append(parts, strings.Replace(fmt.Sprintf("El agua está a %.1f m", m), ".", ",", 1))
		}
	}
	if len(parts) == 0 {
		if en {
			return "The level changed."
		}
		return "El nivel cambió."
	}
	return strings.Join(parts, " ") + "."
}

// Text is the whole message as WhatsApp shows it with the approved template.
func Text(f Fields, lang string) string {
	if lang == "en" {
		return fmt.Sprintf("⚠️ %s at %s.\nWhat is happening: %s\nWhat to do: %s\nAutomatic notice from Vigiax at %s (Colombia time). In an emergency call your local emergency number.", f[0], f[1], f[2], f[3], f[4])
	}
	return fmt.Sprintf("⚠️ %s en %s.\nQué pasa: %s\nQué hacer: %s\nAviso automático de Vigiax a las %s. Si estás en emergencia llama al 123.", f[0], f[1], f[2], f[3], f[4])
}
