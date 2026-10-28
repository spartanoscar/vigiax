package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"log"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/spartanoscar/vigiax/server/internal/alert"
	"github.com/spartanoscar/vigiax/server/internal/station"
)

// stationAlerts is the alert level of one station and its history.
type stationAlerts struct {
	mu     sync.Mutex
	state  alert.State
	events []alert.Event
	path   string
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
	for _, l := range liveStations {
		if l.ID == cfg.ID && l.Alerts != nil {
			return *l.Alerts
		}
	}
	return alert.TwinDefaults()
}

// smoothSpan bounds the median: three frames of the twin (5 s apart), but a
// single photo of an hourly camera, where confirm_readings does the work.
const smoothSpan = 2 * time.Minute

// A jump is a level change between two frames close in time that water does
// not make at that pace: the twin's fastest flood moves about 20 cm a frame.
const (
	jumpCm     = 50.0
	jumpWithin = time.Minute
)

// observe turns the latest reading into what the rules see: the median of the
// last three levels (one odd frame cannot move it) and the rise rate, both
// over the readings after the last jump, so a camera that moved does not
// count as rising water.
func observe(st *station.Station, c *alert.Config, rd station.Reading) alert.Obs {
	o := alert.Obs{T: rd.T, Blind: rd.Blind}
	if rd.Blind || rd.LevelCm == nil {
		o.Blind = true
		return o
	}
	window := time.Duration(c.RiseWindowMin * float64(time.Minute))
	var rs []station.Reading
	for _, r := range st.Readings(rd.T.Add(-window)) {
		if r.LevelCm != nil && !r.T.After(rd.T) {
			rs = append(rs, r)
		}
	}
	from := 0
	for i := 1; i < len(rs); i++ {
		if d := *rs[i].LevelCm - *rs[i-1].LevelCm; math.Abs(d) >= jumpCm && rs[i].T.Sub(rs[i-1].T) <= jumpWithin {
			from = i
			if i == len(rs)-1 {
				o.JumpCm = &d
			}
		}
	}
	rs = rs[from:]
	var lv []float64
	for i := len(rs) - 1; i >= 0 && len(lv) < 3 && rd.T.Sub(rs[i].T) <= smoothSpan; i-- {
		lv = append(lv, *rs[i].LevelCm)
	}
	if len(lv) == 0 { // the reading itself is always there; guard anyway
		lv = []float64{*rd.LevelCm}
	}
	sort.Float64s(lv)
	m := lv[len(lv)/2]
	o.LevelCm, o.LastCm = &m, rd.LevelCm
	if v, ok := slope(rs, window); ok {
		o.RiseCmPerMin = &v
	}
	return o
}

// slope is the least-squares rise in cm per minute, with the same demands as
// Station.Rise: three readings covering at least a third of the window.
func slope(rs []station.Reading, window time.Duration) (float64, bool) {
	if len(rs) < 3 || rs[len(rs)-1].T.Sub(rs[0].T) < window/3 {
		return 0, false
	}
	var st, sl, stt, stl float64
	for _, r := range rs {
		t := r.T.Sub(rs[0].T).Minutes()
		st, sl, stt, stl = st+t, sl+*r.LevelCm, stt+t*t, stl+t**r.LevelCm
	}
	n := float64(len(rs))
	den := n*stt - st*st
	if den == 0 {
		return 0, false
	}
	return (n*stl - st*sl) / den, true
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
	cfg := st.Config()
	c := alertConfig(cfg)
	a := s.alerts(cfg.ID)
	a.mu.Lock()
	e := a.state.Step(&c, observe(st, &c, rd))
	if e != nil {
		a.record(*e)
	}
	a.mu.Unlock()
	if e != nil {
		s.announce(cfg.ID, &c, *e)
	}
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
