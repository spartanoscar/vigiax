package alert

import (
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 28, 20, 0, 0, 0, time.UTC)

func obs(i int, level float64) Obs {
	return Obs{T: t0.Add(time.Duration(i) * 5 * time.Second), LevelCm: &level}
}

func TestOneOddFrameDoesNotFire(t *testing.T) {
	c := TwinDefaults()
	var s State
	for i, lv := range []float64{45, 46, 300, 45, 46, 45} { // a glitch at 3 m
		if e := s.Step(&c, obs(i, lv)); e != nil {
			t.Fatalf("reading %d raised %v", i, e)
		}
	}
}

func TestRaiseAfterConfirmAndJumpLevels(t *testing.T) {
	c := TwinDefaults()
	var s State
	var got []*Event
	for i, lv := range []float64{45, 120, 130, 140, 270, 280, 290} {
		if e := s.Step(&c, obs(i, lv)); e != nil {
			got = append(got, e)
		}
	}
	if len(got) != 2 || got[0].To != Attention || got[1].To != Evacuate || got[1].From != Attention {
		t.Fatalf("events: %+v", got)
	}
}

func TestRiseRateFiresBelowTheLines(t *testing.T) {
	c := TwinDefaults()
	var s State
	rise := 4.0
	var e *Event
	for i := range 3 {
		o := obs(i, 60)
		o.RiseCmPerMin = &rise
		e = s.Step(&c, o)
	}
	if e == nil || e.To != Alert || !strings.Contains(e.Reason, "rising") {
		t.Fatalf("got %+v", e)
	}
}

func TestBlindNeverLowersAndLowerWaits(t *testing.T) {
	c := TwinDefaults()
	s := State{Level: Alert}
	e := s.Step(&c, Obs{T: t0, Blind: true})
	if e == nil || e.Kind != "blind" || s.Level != Alert {
		t.Fatalf("blind: %+v, level %v", e, s.Level)
	}
	if e := s.Step(&c, Obs{T: t0.Add(time.Hour), Blind: true}); e != nil || s.Level != Alert {
		t.Fatal("a second blind reading changed something")
	}
	// Water back down: lowers only after LowerAfterMin (2 min) below the lines.
	if e := s.Step(&c, obs(0, 50)); e != nil {
		t.Fatalf("lowered at once: %+v", e)
	}
	if e := s.Step(&c, obs(12, 50)); e != nil { // 1 min
		t.Fatalf("lowered after 1 min: %+v", e)
	}
	if e := s.Step(&c, obs(24, 50)); e == nil || e.To != Normal || e.Kind != "lower" { // 2 min
		t.Fatalf("did not lower after 2 min: %+v", e)
	}
}

func TestFalseAlarmQuietsTheSameLevel(t *testing.T) {
	c := TwinDefaults()
	s := State{Level: Attention}
	if e := s.FalseAlarm(t0, "leader", 30*time.Minute); e == nil || s.Level != Normal {
		t.Fatal("false alarm did not lower")
	}
	for i := range 5 {
		if e := s.Step(&c, obs(i, 120)); e != nil {
			t.Fatalf("raised again to attention during the quiet time: %+v", e)
		}
	}
	var e *Event
	for i := 5; i < 8; i++ {
		e = s.Step(&c, obs(i, 200)) // alert is a different level: not quieted
	}
	if e == nil || e.To != Alert {
		t.Fatalf("alert was quieted too: %+v", e)
	}
}

func TestValidate(t *testing.T) {
	c := TwinDefaults()
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	bad := TwinDefaults()
	v := 50.0
	bad.Rules.Alert.LevelCm = &v // below attention's line
	if bad.Validate() == nil {
		t.Fatal("accepted lines out of order")
	}
	bad = TwinDefaults()
	bad.Rules.Evacuate.Channels = []string{"sms-to-anyone"}
	if bad.Validate() == nil {
		t.Fatal("accepted an unknown channel")
	}
}

func TestMessage(t *testing.T) {
	c := TwinDefaults()
	lv, rise := 192.0, 7.6
	e := Event{T: t0, Kind: "raise", From: Attention, To: Alert, LevelCm: &lv, RiseCmPerMin: &rise}
	es := Text(Fill(&c, e, "es"), "es")
	want := "⚠️ ALERTA DE CRECIENTE en la quebrada del gemelo digital.\nQué pasa: El agua sube 7,6 cm por minuto y está a 1,9 m.\nQué hacer: Aléjate de la orilla y no cruces el puente.\nAviso automático de Vigiax a las 15:00. Si estás en emergencia llama al 123."
	if es != want {
		t.Fatalf("es:\n%s\nwant:\n%s", es, want)
	}
	en := Text(Fill(&c, e, "en"), "en")
	if !strings.Contains(en, "FLASH FLOOD ALERT at the digital twin's stream.\nWhat is happening: The water is rising 7.6 cm a minute and is at 1.9 m.") {
		t.Fatalf("en:\n%s", en)
	}
}

func TestMovedCameraKeepsTheLevelUntilBack(t *testing.T) {
	c := TwinDefaults()
	s := State{Level: Attention}
	if e := s.Step(&c, Obs{T: t0, Moved: true, MovedWhy: "gauge 120 px sideways"}); e == nil || e.Kind != "moved" || s.Level != Attention {
		t.Fatalf("moved: %+v, level %v", e, s.Level)
	}
	for i := 1; i < 30; i++ { // a knocked camera reading the far bank must not raise anything
		if e := s.Step(&c, Obs{T: t0.Add(time.Duration(i) * 5 * time.Second), Moved: true}); e != nil {
			t.Fatalf("event while moved: %+v", e)
		}
	}
	s.Step(&c, obs(40, 120))
	if s.Moved {
		t.Fatal("still marked moved after a normal reading")
	}
}

func TestSuddenDropRaisesAlert(t *testing.T) {
	c := TwinDefaults()
	var s State
	var e *Event
	drop := -2.4 // cm/min, faster than the 1.5 default
	for i := range 3 {
		o := obs(i, 60)
		o.RiseCmPerMin = &drop
		e = s.Step(&c, o)
	}
	if e == nil || e.To != Alert || !strings.Contains(e.Reason, "damming") {
		t.Fatalf("sudden drop: %+v", e)
	}
	msg := Text(Fill(&c, *e, "en"), "en")
	if !strings.Contains(msg, "The water is falling 2.4 cm a minute: possible damming upstream") {
		t.Fatalf("message:\n%s", msg)
	}
}

// A fast rise is raised one confirmed level at a time; each raise says why
// for the level it raises to, not for a higher line the water already reached.
func TestRaiseReasonNamesTheLevelRaisedTo(t *testing.T) {
	c := TwinDefaults()
	var s State
	var got []*Event
	for i, lv := range []float64{45, 150, 190, 200, 290} {
		if e := s.Step(&c, obs(i, lv)); e != nil {
			got = append(got, e)
		}
	}
	if len(got) == 0 || got[0].To != Attention {
		t.Fatalf("events: %+v", got)
	}
	if r := got[0].Reason; !strings.Contains(r, "attention line") || !strings.Contains(r, "past the alert line too") {
		t.Fatalf("first raise to attention says %q", r)
	}
}
