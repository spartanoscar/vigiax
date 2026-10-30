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
	case "moved":
		title = "CÁMARA MOVIDA"
		fact = "La regla ya no está donde se calibró: la cámara se movió. Las lecturas quedan en pausa y se mantiene el nivel de alerta."
		action = "Revisa el soporte de la cámara y vuelve a calibrar."
		if en {
			title = "CAMERA MOVED"
			fact = "The gauge is no longer where it was calibrated: the camera moved. Readings are paused and the alert level is kept."
			action = "Check the camera mount and recalibrate."
		}
	case "restart":
		title = "NUEVA SIMULACIÓN"
		fact = "El gemelo empezó una simulación nueva: el nivel de alerta vuelve a normal."
		if en {
			title = "NEW SIMULATION"
			fact = "The twin started a new simulation: the alert level is back to normal."
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
	// The rate as measured, not stretched over the window: a fall of 50 cm in
	// one minute must not read as "250 cm in 5 minutes".
	rate := func(v float64, unitEn, unitEs string) (string, string) {
		f := "%.0f"
		if v < 10 {
			f = "%.1f"
		}
		n := fmt.Sprintf(f, v)
		return n + " cm " + unitEn, strings.Replace(n, ".", ",", 1) + " cm " + unitEs
	}
	switch {
	case e.RiseCmPerMin != nil && *e.RiseCmPerMin < -0.05 && strings.Contains(e.Reason, "damming"):
		r, rs := rate(-*e.RiseCmPerMin, "a minute", "por minuto")
		if c.RiseWindowMin > 60 {
			r, rs = rate(-*e.RiseCmPerMin*60, "an hour", "por hora")
		}
		if en {
			parts = append(parts, "The water is falling "+r+": possible damming upstream")
		} else {
			parts = append(parts, "El agua baja "+rs+": posible represamiento aguas arriba")
		}
	case e.RiseCmPerMin != nil && *e.RiseCmPerMin > 0.05:
		r, rs := rate(*e.RiseCmPerMin, "a minute", "por minuto")
		if c.RiseWindowMin > 60 { // hourly cameras
			r, rs = rate(*e.RiseCmPerMin*60, "an hour", "por hora")
		}
		if en {
			parts = append(parts, "The water is rising "+r)
		} else {
			parts = append(parts, "El agua sube "+rs)
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
