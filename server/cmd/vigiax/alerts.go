package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/spartanoscar/vigiax/server/internal/alert"
	"github.com/spartanoscar/vigiax/server/internal/gauge"
	"github.com/spartanoscar/vigiax/server/internal/station"
)

// stationAlerts is the alert level of one station and its history.
type stationAlerts struct {
	mu      sync.Mutex
	state   alert.State
	events  []alert.Event
	path    string
	restart time.Time // readings before it belong to an earlier run of the twin
}

const keepEvents = 200

func (s *server) alerts(id string) *stationAlerts {
	s.mu.Lock()
	defer s.mu.Unlock()
	if a, ok := s.alertState[id]; ok {
		return a
	}
	a := &stationAlerts{path: filepath.Join(s.dir, id+".alerts.jsonl")}
	if f, err := os.Open(a.path); err == nil {
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			var e alert.Event
			if json.Unmarshal(sc.Bytes(), &e) == nil {
				a.events = append(a.events, e)
			}
		}
		f.Close()
		if len(a.events) > keepEvents {
			a.events = a.events[len(a.events)-keepEvents:]
		}
		a.state = alert.Replay(a.events)
		for _, e := range a.events {
			if e.Kind == "restart" {
				a.restart = e.T
			}
		}
	}
	s.alertState[id] = a
	return a
}

func (a *stationAlerts) record(e alert.Event) {
	a.events = append(a.events, e)
	if len(a.events) > keepEvents {
		a.events = a.events[len(a.events)-keepEvents:]
	}
	f, err := os.OpenFile(a.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		log.Printf("alerts: %v", err)
		return
	}
	defer f.Close()
	b, _ := json.Marshal(e)
	f.Write(append(b, '\n'))
}

// alertConfig is the station's settings, or the defaults for its kind.
func alertConfig(cfg station.Config) alert.Config {
	if cfg.Alerts != nil {
		return *cfg.Alerts
	}
	return defaultAlerts(cfg)
}

func defaultAlerts(cfg station.Config) alert.Config {
	for _, l := range station.Live {
		if l.ID == cfg.ID && l.Alerts != nil {
			return *l.Alerts
		}
	}
	return alert.TwinDefaults()
}

// alertEvent is what the dashboards and the notifiers receive.
type alertEvent struct {
	Station string       `json:"station"`
	Event   alert.Event  `json:"event"`
	Rule    *alert.Rule  `json:"rule,omitempty"`
	Message alertMessage `json:"message"`
}

type alertMessage struct {
	Es string `json:"es"`
	En string `json:"en"`
}

func messages(c *alert.Config, e alert.Event) alertMessage {
	return alertMessage{Es: alert.Text(alert.Fill(c, e, "es"), "es"), En: alert.Text(alert.Fill(c, e, "en"), "en")}
}

// checkAlerts runs the rules on a new reading and sends what changes.
func (s *server) checkAlerts(st *station.Station, rd station.Reading) {
	id := st.Config().ID
	a := s.alerts(id)
	a.mu.Lock()
	wasBlind := a.state.Blind
	a.mu.Unlock()
	e, c := s.stepAlerts(st, rd)
	if e != nil {
		s.announce(id, c, *e)
		return
	}
	a.mu.Lock()
	blind, level := a.state.Blind, a.state.Level
	a.mu.Unlock()
	// The camera stopped seeing the water with nothing raised. A debris flow does
	// that (mud is not water to SAM), so the agent looks at the scene; nobody is
	// told unless it, or the scene rules, raise the level.
	if !wasBlind && blind && level == alert.Normal {
		go s.wakeAgent(id, alert.Event{T: rd.T, Kind: "blind", From: level, To: level, Reason: "the camera stopped seeing the water"})
	}
}

// stepAlerts applies one reading to the station's alert state and records the
// event it causes, without announcing it: readings that arrive late (a node
// back online) are stepped through, and only what holds now is announced.
func (s *server) stepAlerts(st *station.Station, rd station.Reading) (*alert.Event, *alert.Config) {
	cfg := st.Config()
	c := alertConfig(cfg)
	a := s.alerts(cfg.ID)
	a.mu.Lock()
	since := a.restart
	a.mu.Unlock()
	o := st.Observe(&c, rd, since)
	if o.JumpCm != nil && cfg.Calib != nil {
		s.checkPlacement(cfg, rd.T)
	}
	s.mu.Lock()
	why, moved := s.moved[cfg.ID]
	s.mu.Unlock()
	if moved {
		o = alert.Obs{T: rd.T, Moved: true, MovedWhy: why}
	}
	a.mu.Lock()
	e := a.state.Step(&c, o)
	if e != nil {
		a.record(*e)
	}
	a.mu.Unlock()
	return e, &c
}

// checkPlacement asks SAM where the gauge is after a jump: if it is not where
// it was calibrated, the camera moved and its readings stop counting until the
// gauge is back in place or the station is calibrated again.
func (s *server) checkPlacement(cfg station.Config, now time.Time) {
	s.mu.Lock()
	f, ok := s.frames[cfg.ID]
	s.mu.Unlock()
	if !ok || now.Sub(f.at) > frameFresh {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	p, err := gauge.Check(ctx, s.meta, f.jpeg, cfg.Calib)
	s.budget.spend(now, "gauge-check", p.CostUSD)
	if err != nil {
		log.Printf("%s: gauge check: %v", cfg.ID, err)
		return
	}
	a := s.alerts(cfg.ID)
	a.mu.Lock()
	flood := a.state.Level >= alert.Alert
	a.mu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case !p.Found && flood:
		// In a flood already raised, a gauge that vanished is under the water or
		// the debris, not elsewhere: readings go on and the level is kept.
		delete(s.moved, cfg.ID)
	case !p.Found:
		s.moved[cfg.ID] = "after a jump in the level the gauge cannot be found in the frame: the camera moved or something covers it"
	case p.Moved:
		s.moved[cfg.ID] = fmt.Sprintf("after a jump in the level the gauge is %+.0f px sideways and %+.0f px vertically from where it was calibrated: the camera moved", p.ShiftX, p.ShiftY)
	default:
		delete(s.moved, cfg.ID) // the gauge is where it was: the jump is the water
	}
	log.Printf("%s: gauge check: found %v, moved %v (%+.0f, %+.0f px)", cfg.ID, p.Found, p.Moved, p.ShiftX, p.ShiftY)
}

func (s *server) announce(id string, c *alert.Config, e alert.Event) {
	ev := alertEvent{Station: id, Event: e, Rule: c.Rule(e.To), Message: messages(c, e)}
	s.hub.publish("alert", ev)
	log.Printf("%s: %s %s → %s (%s)", id, e.Kind, e.From, e.To, e.Reason)
	if s.notify != nil {
		go s.notify(id, c, e)
	}
	go s.wakeAgent(id, e)
}

// editable tells whether this visitor may change a station's alerts: anyone
// for a twin station, judges and the admin for the real ones.
func (s *server) editable(r *http.Request, cfg station.Config) bool {
	return cfg.Source == nil || s.access.role(r) != rolePublic
}

type alertsView struct {
	Station     string        `json:"station"`
	Config      alert.Config  `json:"config"`
	Custom      bool          `json:"custom"` // false: the defaults
	State       alert.State   `json:"state"`
	Events      []alert.Event `json:"events"`
	Editable    bool          `json:"editable"`
	Role        string        `json:"role"`
	Preview     alertMessage  `json:"preview"` // a sample alert with the current settings
	Subscribers int           `json:"subscribers"`
	WhatsApp    string        `json:"whatsapp,omitempty"` // number to write to, to receive this station's alerts
}

func (s *server) alertsView(r *http.Request, st *station.Station) alertsView {
	cfg := st.Config()
	c := alertConfig(cfg)
	a := s.alerts(cfg.ID)
	a.mu.Lock()
	state, events := a.state, append([]alert.Event(nil), a.events...)
	a.mu.Unlock()
	if len(events) > 20 {
		events = events[len(events)-20:]
	}
	sample := alert.Event{T: time.Now(), Kind: "raise", From: alert.Attention, To: alert.Alert}
	if r := c.Rule(alert.Alert); r != nil && r.LevelCm != nil {
		lv, rise := *r.LevelCm+12, 0.0
		if r.RiseCmPerMin != nil {
			rise = *r.RiseCmPerMin * 1.2
		}
		sample.LevelCm, sample.RiseCmPerMin = &lv, &rise
	}
	v := alertsView{Station: cfg.ID, Config: c, Custom: cfg.Alerts != nil, State: state, Events: events,
		Editable: s.editable(r, cfg), Role: s.access.role(r), Preview: messages(&c, sample), WhatsApp: s.waNumber}
	if s.subs != nil {
		v.Subscribers = s.subs.count(cfg.ID, time.Now())
	}
	return v
}

// GET, PUT (new settings) and DELETE (back to the defaults) /api/stations/{id}/alerts.
func (s *server) handleAlerts(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if r.Method == http.MethodGet {
		s.mu.Lock()
		st, ok := s.stations[id]
		s.mu.Unlock()
		if !ok { // a twin that has not sent a frame yet: show the defaults without creating it
			c := alert.TwinDefaults()
			writeJSON(w, alertsView{Station: id, Config: c, Editable: true, Role: s.access.role(r), WhatsApp: s.waNumber,
				Preview: messages(&c, alert.Event{T: time.Now(), Kind: "raise", From: alert.Attention, To: alert.Alert,
					LevelCm: ptr(192), RiseCmPerMin: ptr(3.6)})})
			return
		}
		writeJSON(w, s.alertsView(r, st))
		return
	}
	st, err := s.station(id)
	if err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	if !s.editable(r, st.Config()) {
		httpError(w, http.StatusForbidden, errors.New("the real stations are read-only; enter a judge's code to change them"))
		return
	}
	switch r.Method {
	case http.MethodPut:
		var c alert.Config
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&c); err != nil {
			httpError(w, http.StatusBadRequest, err)
			return
		}
		if err := c.Validate(); err != nil {
			httpError(w, http.StatusUnprocessableEntity, err)
			return
		}
		err = st.Update(func(cfg *station.Config) { cfg.Alerts = &c })
	case http.MethodDelete:
		err = st.Update(func(cfg *station.Config) { cfg.Alerts = nil })
	default:
		httpError(w, http.StatusMethodNotAllowed, errors.New("use GET, PUT or DELETE"))
		return
	}
	if err != nil {
		httpError(w, http.StatusInternalServerError, err)
		return
	}
	v := s.alertsView(r, st)
	s.hub.publish("alerts_config", map[string]any{"station": id})
	writeJSON(w, v)
}

// POST /api/stations/{id}/alerts/false-alarm: a person on site says the alert is wrong.
func (s *server) postFalseAlarm(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	st, ok := s.stations[r.PathValue("id")]
	s.mu.Unlock()
	if !ok {
		httpError(w, http.StatusNotFound, errors.New("no such station"))
		return
	}
	if !s.editable(r, st.Config()) {
		httpError(w, http.StatusForbidden, errors.New("the real stations are read-only; enter a judge's code"))
		return
	}
	s.falseAlarm(st, "dashboard")
	writeJSON(w, s.alertsView(r, st))
}

func (s *server) falseAlarm(st *station.Station, by string) {
	cfg := st.Config()
	c := alertConfig(cfg)
	a := s.alerts(cfg.ID)
	a.mu.Lock()
	// The rules stay quiet for this level for a while (in the twin, the rest of a session).
	e := a.state.FalseAlarm(time.Now(), by, time.Duration(max(c.LowerAfterMin*3, 10)*float64(time.Minute)))
	if e != nil {
		a.record(*e)
	}
	a.mu.Unlock()
	if e != nil {
		s.announce(cfg.ID, &c, *e)
	}
}

func ptr(v float64) *float64 { return &v }
