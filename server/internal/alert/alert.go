// Package alert turns a station's readings into an alert level: normal,
// attention, alert or evacuate. Fixed rules set the level; no language model
// runs here. A rule fires on a level line or on a rise rate, only after it holds
// for several readings in a row, and the level comes down only after the water
// stays below the line for a while. A blind camera never lowers the level.
package alert

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

type Level int

const (
	Normal Level = iota
	Attention
	Alert
	Evacuate
)

var levelNames = [...]string{"normal", "attention", "alert", "evacuate"}

func (l Level) String() string {
	if l < Normal || l > Evacuate {
		return fmt.Sprintf("level(%d)", int(l))
	}
	return levelNames[l]
}

func (l Level) MarshalJSON() ([]byte, error) { return json.Marshal(l.String()) }

func (l *Level) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	for i, n := range levelNames {
		if n == s {
			*l = Level(i)
			return nil
		}
	}
	return fmt.Errorf("alert: unknown level %q", s)
}

// Rule is what raises the station to one level and who hears about it.
type Rule struct {
	Enabled      bool     `json:"enabled"`
	LevelCm      *float64 `json:"level_cm,omitempty"`        // fires at or above this level
	RiseCmPerMin *float64 `json:"rise_cm_per_min,omitempty"` // or at or above this rise rate
	Audience     string   `json:"audience"`                  // leader, group or everyone
	Channels     []string `json:"channels"`                  // whatsapp, push, siren
	Why          string   `json:"why"`                       // shown in the settings
	Action       string   `json:"action"`                    // what to do, sent in the message (Spanish)
	ActionEn     string   `json:"action_en"`                 // the same in English
}

type Rules struct {
	Attention Rule `json:"attention"`
	Alert     Rule `json:"alert"`
	Evacuate  Rule `json:"evacuate"`
}

// Config is a station's alert settings.
type Config struct {
	Place         string  `json:"place"`               // name used in the messages (Spanish)
	PlaceEn       string  `json:"place_en"`            // the same in English
	Confirm       int     `json:"confirm_readings"`    // readings in a row before raising
	RiseWindowMin float64 `json:"rise_window_minutes"` // window of the rise rate
	LowerAfterMin float64 `json:"lower_after_minutes"` // minutes below a line before lowering
	Rules         Rules   `json:"rules"`
	Note          string  `json:"note,omitempty"` // where the numbers come from
}

func (c *Config) rule(l Level) *Rule {
	switch l {
	case Attention:
		return &c.Rules.Attention
	case Alert:
		return &c.Rules.Alert
	case Evacuate:
		return &c.Rules.Evacuate
	}
	return nil
}

var (
	audiences = map[string]bool{"leader": true, "group": true, "everyone": true}
	channels  = map[string]bool{"whatsapp": true, "push": true, "siren": true}
)

// Validate checks a configuration sent by a visitor.
func (c *Config) Validate() error {
	if c.Confirm < 1 || c.Confirm > 20 {
		return errors.New("confirm_readings must be 1-20")
	}
	if c.RiseWindowMin < 1 || c.RiseWindowMin > 360 {
		return errors.New("rise_window_minutes must be 1-360")
	}
	if c.LowerAfterMin < 0 || c.LowerAfterMin > 720 {
		return errors.New("lower_after_minutes must be 0-720")
	}
	if len(c.Place) > 80 || strings.TrimSpace(c.Place) == "" || len(c.PlaceEn) > 80 {
		return errors.New("place must be 1-80 characters")
	}
	var lastLine float64
	for l := Attention; l <= Evacuate; l++ {
		r := c.rule(l)
		if len(r.Why) > 200 || len(r.Action) > 160 || len(r.ActionEn) > 160 {
			return fmt.Errorf("%s: text too long", l)
		}
		if !audiences[r.Audience] {
			return fmt.Errorf("%s: audience must be leader, group or everyone", l)
		}
		for _, ch := range r.Channels {
			if !channels[ch] {
				return fmt.Errorf("%s: unknown channel %q", l, ch)
			}
		}
		if r.LevelCm != nil && (*r.LevelCm <= 0 || *r.LevelCm > 5000) {
			return fmt.Errorf("%s: level_cm must be 0-5000", l)
		}
		if r.RiseCmPerMin != nil && (*r.RiseCmPerMin <= 0 || *r.RiseCmPerMin > 500) {
			return fmt.Errorf("%s: rise_cm_per_min must be 0-500", l)
		}
		if r.Enabled && r.LevelCm == nil && r.RiseCmPerMin == nil {
			return fmt.Errorf("%s: an enabled level needs a level line or a rise rate", l)
		}
		if r.Enabled && r.LevelCm != nil {
			if *r.LevelCm <= lastLine {
				return fmt.Errorf("%s: its level line must be above the lower levels' lines", l)
			}
			lastLine = *r.LevelCm
		}
	}
	return nil
}

// Obs is one reading as the rules see it.
type Obs struct {
	T            time.Time
	LevelCm      *float64 // smoothed level the rules use; nil when unknown
	LastCm       *float64 // latest measured level, the one messages cite
	RiseCmPerMin *float64 // over the rise window; nil when unknown
	Blind        bool     // the camera cannot see the water
	JumpCm       *float64 // set when the level jumped between two close frames
}

// Event is a change of level, or a blind camera while the level is raised.
type Event struct {
	T            time.Time `json:"t"`
	Kind         string    `json:"kind"` // raise, lower, false_alarm, blind, jump
	From         Level     `json:"from"`
	To           Level     `json:"to"`
	Reason       string    `json:"reason"`
	LevelCm      *float64  `json:"level_cm,omitempty"`
	RiseCmPerMin *float64  `json:"rise_cm_per_min,omitempty"`
	By           string    `json:"by,omitempty"` // who marked a false alarm
}

// State follows one station's level from reading to reading.
type State struct {
	Level Level     `json:"level"`
	Since time.Time `json:"since"`
	Blind bool      `json:"blind"`

	upN        int       // readings in a row above the current level
	upTo       Level     // lowest level seen in that run
	belowSince time.Time // first reading of the current run below the current level
	quietUntil time.Time // after a false alarm: no raise to the same level until then
	quietAt    Level
}

// target is the highest enabled level whose rule holds, and why.
func target(c *Config, o Obs) (Level, string) {
	for l := Evacuate; l >= Attention; l-- {
		r := c.rule(l)
		if !r.Enabled {
			continue
		}
		if r.LevelCm != nil && o.LevelCm != nil && *o.LevelCm >= *r.LevelCm {
			return l, fmt.Sprintf("level %.0f cm at or above the %s line (%.0f cm)", *o.LevelCm, l, *r.LevelCm)
		}
		if r.RiseCmPerMin != nil && o.RiseCmPerMin != nil && *o.RiseCmPerMin >= *r.RiseCmPerMin {
			return l, fmt.Sprintf("rising %.1f cm/min, at or above the %s rate (%.1f cm/min)", *o.RiseCmPerMin, l, *r.RiseCmPerMin)
		}
	}
	return Normal, "below every enabled line and rate"
}

// Step applies one reading and returns the event it causes, if any.
func (s *State) Step(c *Config, o Obs) *Event {
	ev := func(kind string, to Level, why string) *Event {
		lv := o.LastCm
		if lv == nil {
			lv = o.LevelCm
		}
		e := &Event{T: o.T, Kind: kind, From: s.Level, To: to, Reason: why, LevelCm: lv, RiseCmPerMin: o.RiseCmPerMin}
		if kind == "raise" || kind == "lower" {
			s.Level, s.Since = to, o.T
		}
		return e
	}
	if o.JumpCm != nil {
		// A camera that moved and the front of a flood look alike from one frame
		// to the next. This frame only flags it; from the next one the level
		// lines apply with the usual confirmation, and the rise rate leaves the
		// jump out. Someone should look.
		s.upN, s.belowSince = 0, time.Time{}
		return ev("jump", s.Level, fmt.Sprintf("the level jumped %+.0f cm between two frames: the camera may have moved, or a flood front arrived", *o.JumpCm))
	}
	if o.Blind || (o.LevelCm == nil && o.RiseCmPerMin == nil) {
		// Nothing measured: keep the level and the runs as they are.
		if !s.Blind {
			s.Blind = true
			if s.Level > Normal {
				return ev("blind", s.Level, "the camera cannot see the water; the level is kept until it sees again")
			}
		}
		return nil
	}
	s.Blind = false
	t, why := target(c, o)
	if t == s.quietAt && o.T.Before(s.quietUntil) {
		t = s.Level // a person said this was a false alarm: wait before raising to it again
	}
	switch {
	case t > s.Level:
		s.belowSince = time.Time{}
		if s.upN == 0 || t < s.upTo {
			s.upTo = t
		}
		s.upN++
		if s.upN >= max(c.Confirm, 1) {
			to := s.upTo
			s.upN = 0
			if to > s.Level {
				return ev("raise", to, why)
			}
		}
	case t < s.Level:
		s.upN = 0
		if s.belowSince.IsZero() {
			s.belowSince = o.T
		}
		if o.T.Sub(s.belowSince) >= time.Duration(c.LowerAfterMin*float64(time.Minute)) {
			s.belowSince = time.Time{}
			return ev("lower", t, why+fmt.Sprintf(" for %.0f min", c.LowerAfterMin))
		}
	default:
		s.upN, s.belowSince = 0, time.Time{}
	}
	return nil
}

// FalseAlarm brings the level back to normal on a person's word and keeps it
// from rising to the same level again for the given time.
func (s *State) FalseAlarm(now time.Time, by string, quiet time.Duration) *Event {
	if s.Level == Normal {
		return nil
	}
	e := &Event{T: now, Kind: "false_alarm", From: s.Level, To: Normal, Reason: "marked as a false alarm", By: by}
	s.quietAt, s.quietUntil = s.Level, now.Add(quiet)
	s.Level, s.Since, s.upN, s.belowSince = Normal, now, 0, time.Time{}
	return e
}

// Replay rebuilds the level from stored events.
func Replay(events []Event) State {
	var s State
	for _, e := range events {
		if e.Kind == "raise" || e.Kind == "lower" || e.Kind == "false_alarm" {
			s.Level, s.Since = e.To, e.T
		}
	}
	return s
}

// Rule returns the rule for a level (nil for normal).
func (c *Config) Rule(l Level) *Rule { return c.rule(l) }

func ptr(v float64) *float64 { return &v }

// TwinDefaults suits the digital twin: water from 0.2 to 3.6 m, a frame every
// 5 s, sessions of 5 min (so the level comes down after 2 min, not 15).
func TwinDefaults() Config {
	return Config{
		Place: "la quebrada del gemelo digital", PlaceEn: "the digital twin's stream", Confirm: 3, RiseWindowMin: 5, LowerAfterMin: 2,
		Note: "Demo values for the twin. In the field the community sets the lines with the installer.",
		Rules: Rules{
			Attention: Rule{Enabled: true, LevelCm: ptr(100), RiseCmPerMin: ptr(1), Audience: "leader", Channels: []string{"whatsapp", "push"},
				Why:    "The water starts to grow: someone should look at the stream.",
				Action: "Mira la quebrada y prepárate por si sigue subiendo.", ActionEn: "Watch the stream and get ready in case it keeps rising."},
			Alert: Rule{Enabled: true, LevelCm: ptr(180), RiseCmPerMin: ptr(3), Audience: "group", Channels: []string{"whatsapp", "push"},
				Why:    "A flash flood is likely: people must keep away from the banks.",
				Action: "Aléjate de la orilla y no cruces el puente.", ActionEn: "Keep away from the banks and do not cross the bridge."},
			Evacuate: Rule{Enabled: true, LevelCm: ptr(260), RiseCmPerMin: ptr(6), Audience: "everyone", Channels: []string{"whatsapp", "push", "siren"},
				Why:    "The water reaches the houses in minutes.",
				Action: "Sube ya a la parte alta y lleva a niños y mayores.", ActionEn: "Go to high ground now and take children and elders with you."},
		},
	}
}
