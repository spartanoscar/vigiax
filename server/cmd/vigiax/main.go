// vigiax is the station service: camera nodes post frames, SAM 3.1 turns each
// into a water level, and the dashboard follows the readings over SSE. Live
// stations (usgs.go) are real webcams the service follows on its own.
//
//	POST /api/stations/{id}/frames       JPEG body → reading
//	POST /api/stations/{id}/calibration  JPEG body (read the gauge) or JSON two-point calibration
//	GET  /api/stations                   stations with their last reading and rise rates
//	GET  /api/stations/{id}              one station
//	GET  /api/stations/{id}/readings     ?minutes=60
//	GET  /api/events                     server-sent events
//
// Environment: MODEL_API_KEY (required), ADDR (:8790), DATA_DIR (var),
// MIN_FRAME_SECONDS (5), DAILY_BUDGET_USD (3), IP_DAILY_BUDGET_USD (0.5),
// LIVE_DAILY_BUDGET_USD (0.5, live stations, apart from the visitors' budget),
// JUDGE_DAILY_BUDGET_USD (2, per judge), ADMIN_CODE and JUDGE_CODE (access codes),
// WA_TOKEN, WA_PHONE_ID, WA_NUMBER, WA_APP_SECRET, WA_VERIFY_TOKEN and
// WA_GRAPH_VERSION (WhatsApp alerts; off without a token).
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/spartanoscar/vigiax/server/internal/alert"
	"github.com/spartanoscar/vigiax/server/internal/gauge"
	"github.com/spartanoscar/vigiax/server/internal/meta"
	"github.com/spartanoscar/vigiax/server/internal/scene"
	"github.com/spartanoscar/vigiax/server/internal/station"
)

const maxFrameBytes = 2 << 20

type server struct {
	meta     *meta.Client
	dir      string
	minFrame time.Duration
	budget   *budget // visitors: frames and calibrations posted to the API
	live     *budget // live stations, so visitors cannot use up their day
	hub      *hub

	access  *access
	judgeIP float64 // per-visitor budget with a judge's or the admin's code

	notify   func(id string, c *alert.Config, e alert.Event) // WhatsApp; nil when not configured
	subs     *subscribers
	waNumber string

	mu         sync.Mutex
	stations   map[string]*station.Station
	lastAt     map[string]time.Time
	alertState map[string]*stationAlerts
}

func main() {
	key := os.Getenv("MODEL_API_KEY")
	if key == "" {
		log.Fatal("MODEL_API_KEY is not set")
	}
	dir := env("DATA_DIR", "var")
	if err := os.MkdirAll(filepath.Join(dir, "stations"), 0o755); err != nil {
		log.Fatal(err)
	}
	s := &server{
		meta:       meta.NewClient(key),
		dir:        filepath.Join(dir, "stations"),
		minFrame:   time.Duration(envFloat("MIN_FRAME_SECONDS", 5) * float64(time.Second)),
		budget:     loadBudget(filepath.Join(dir, "budget.json"), envFloat("DAILY_BUDGET_USD", 3), envFloat("IP_DAILY_BUDGET_USD", 0.5)),
		live:       loadBudget(filepath.Join(dir, "budget-live.json"), envFloat("LIVE_DAILY_BUDGET_USD", 0.5), envFloat("LIVE_DAILY_BUDGET_USD", 0.5)),
		hub:        newHub(),
		access:     newAccess(os.Getenv("ADMIN_CODE"), os.Getenv("JUDGE_CODE")),
		judgeIP:    envFloat("JUDGE_DAILY_BUDGET_USD", 2),
		stations:   map[string]*station.Station{},
		lastAt:     map[string]time.Time{},
		alertState: map[string]*stationAlerts{},
	}
	if err := s.loadStations(); err != nil {
		log.Fatal(err)
	}
	if err := s.startLiveStations(); err != nil {
		log.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/stations/{id}/frames", s.postFrame)
	mux.HandleFunc("POST /api/stations/{id}/calibration", s.postCalibration)
	mux.HandleFunc("GET /api/stations", s.getStations)
	mux.HandleFunc("GET /api/stations/{id}", s.getStation)
	mux.HandleFunc("GET /api/stations/{id}/readings", s.getReadings)
	mux.HandleFunc("GET /api/events", s.hub.serve)
	mux.HandleFunc("/api/access", s.handleAccess)
	mux.HandleFunc("/api/stations/{id}/alerts", s.handleAlerts)
	mux.HandleFunc("POST /api/stations/{id}/alerts/false-alarm", s.postFalseAlarm)
	s.setupWhatsApp(mux, dir)

	addr := env("ADDR", ":8790")
	log.Printf("vigiax listening on %s, data in %s, budget %.2f USD/day (%.2f per visitor)", addr, dir, s.budget.limit, s.budget.perIP)
	log.Fatal(http.ListenAndServe(addr, mux))
}

func (s *server) loadStations() error {
	files, _ := filepath.Glob(filepath.Join(s.dir, "*.json"))
	for _, f := range files {
		if _, err := s.station(strings.TrimSuffix(filepath.Base(f), ".json")); err != nil {
			return err
		}
	}
	return nil
}

func (s *server) station(id string) (*station.Station, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if st, ok := s.stations[id]; ok {
		return st, nil
	}
	st, err := station.Open(s.dir, id)
	if err != nil {
		return nil, err
	}
	s.stations[id] = st
	return st, nil
}

// summary is what the dashboard shows for a station.
type summary struct {
	station.Config
	Last           *station.Reading `json:"last,omitempty"`
	Rise5CmPerMin  *float64         `json:"rise_5min_cm_per_min,omitempty"`
	Rise30CmPerMin *float64         `json:"rise_30min_cm_per_min,omitempty"`
	Rise3hCmPerMin *float64         `json:"rise_3h_cm_per_min,omitempty"` // for hourly cameras
}

func summarize(st *station.Station, now time.Time) summary {
	out := summary{Config: st.Config()}
	if r, ok := st.Last(); ok {
		out.Last = &r
	}
	if v, _, ok := st.Rise(now, 5*time.Minute); ok {
		out.Rise5CmPerMin = &v
	}
	if v, _, ok := st.Rise(now, 30*time.Minute); ok {
		out.Rise30CmPerMin = &v
	}
	if v, _, ok := st.Rise(now, 3*time.Hour); ok {
		out.Rise3hCmPerMin = &v
	}
	return out
}

func (s *server) postFrame(w http.ResponseWriter, r *http.Request) {
	st, err := s.station(r.PathValue("id"))
	if err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	if st.Config().Source != nil {
		httpError(w, http.StatusForbidden, errors.New("this is a live station: the service measures its own camera"))
		return
	}
	img, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxFrameBytes))
	if err != nil || len(img) == 0 {
		httpError(w, http.StatusBadRequest, fmt.Errorf("send one JPEG frame of at most %d bytes", maxFrameBytes))
		return
	}
	now := time.Now()
	id := st.Config().ID
	s.mu.Lock()
	// 10 % slack: a node timing its frames to the interval must not be refused
	// for arriving a few milliseconds early.
	wait := s.lastAt[id].Add(s.minFrame * 9 / 10).Sub(now)
	if wait <= 0 {
		s.lastAt[id] = now
	}
	s.mu.Unlock()
	if wait > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
		httpError(w, http.StatusTooManyRequests, fmt.Errorf("one frame every %s per station", s.minFrame))
		return
	}
	ip := clientIP(r)
	if err := s.budget.allow(now, ip, s.perIP(r)); err != nil {
		httpError(w, http.StatusServiceUnavailable, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	rd, err := st.Measure(ctx, s.meta, img, now)
	s.budget.spend(now, ip, rd.CostUSD)
	if err != nil {
		httpError(w, http.StatusBadGateway, err)
		return
	}
	sum := summarize(st, now)
	s.hub.publish("reading", sum)
	s.checkAlerts(st, rd)
	writeJSON(w, sum)
}

type twoPoint struct {
	Y1, V1, Y2, V2 float64
	Unit           string
	X1, X2         int
}

func (s *server) postCalibration(w http.ResponseWriter, r *http.Request) {
	st, err := s.station(r.PathValue("id"))
	if err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	if st.Config().Source != nil {
		httpError(w, http.StatusForbidden, errors.New("this is a live station: the service measures its own camera"))
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxFrameBytes))
	if err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	var cal *gauge.Calibration
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		var p twoPoint
		if err := json.Unmarshal(body, &p); err != nil {
			httpError(w, http.StatusBadRequest, err)
			return
		}
		cal, err = gauge.TwoPoint(p.Y1, p.V1, p.Y2, p.V2, p.Unit, p.X1, p.X2)
	} else {
		now, ip := time.Now(), clientIP(r)
		if err := s.budget.allow(now, ip, s.perIP(r)); err != nil {
			httpError(w, http.StatusServiceUnavailable, err)
			return
		}
		var cost float64
		cal, cost, err = gauge.Calibrate(r.Context(), s.meta, scene.DefaultModel, body)
		s.budget.spend(now, ip, cost)
	}
	if err != nil {
		code := http.StatusBadGateway
		if errors.Is(err, gauge.ErrRejected) {
			code = http.StatusUnprocessableEntity
		}
		httpError(w, code, err)
		return
	}
	if err := st.SetCalibration(cal); err != nil {
		httpError(w, http.StatusInternalServerError, err)
		return
	}
	sum := summarize(st, time.Now())
	s.hub.publish("station", sum)
	writeJSON(w, sum)
}

func (s *server) getStations(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	sts := make([]*station.Station, 0, len(s.stations))
	for _, st := range s.stations {
		sts = append(sts, st)
	}
	s.mu.Unlock()
	now := time.Now()
	out := make([]summary, 0, len(sts))
	for _, st := range sts {
		out = append(out, summarize(st, now))
	}
	writeJSON(w, map[string]any{"stations": out, "spent_today_usd": s.budget.today(now) + s.live.today(now),
		"budget_usd": s.budget.limit + s.live.limit})
}

func (s *server) getStation(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	st, ok := s.stations[r.PathValue("id")]
	s.mu.Unlock()
	if !ok {
		httpError(w, http.StatusNotFound, errors.New("no such station"))
		return
	}
	writeJSON(w, summarize(st, time.Now()))
}

func (s *server) getReadings(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	st, ok := s.stations[r.PathValue("id")]
	s.mu.Unlock()
	if !ok {
		httpError(w, http.StatusNotFound, errors.New("no such station"))
		return
	}
	minutes, err := strconv.Atoi(r.URL.Query().Get("minutes"))
	if err != nil || minutes <= 0 {
		minutes = 60
	}
	writeJSON(w, st.Readings(time.Now().Add(-time.Duration(minutes)*time.Minute)))
}

// budget caps model spending per calendar day (Colombia time), in total and
// per visitor, so one open tab cannot use up the day for everyone. It is saved
// after every charge, so a restart does not reset the day.
type budget struct {
	mu    sync.Mutex
	path  string
	limit float64
	perIP float64
	day   string
	spent float64
	byIP  map[string]float64
}

type budgetFile struct {
	Day   string             `json:"day"`
	Spent float64            `json:"spent_usd"`
	ByIP  map[string]float64 `json:"by_ip_usd"`
}

func loadBudget(path string, limit, perIP float64) *budget {
	b := &budget{path: path, limit: limit, perIP: perIP}
	var f budgetFile
	if data, err := os.ReadFile(path); err == nil && json.Unmarshal(data, &f) == nil {
		b.day, b.spent, b.byIP = f.Day, f.Spent, f.ByIP
	}
	return b
}

// save writes the day's spending; b.mu must be held.
func (b *budget) save() {
	data, _ := json.Marshal(budgetFile{Day: b.day, Spent: b.spent, ByIP: b.byIP})
	tmp := b.path + ".tmp"
	err := os.WriteFile(tmp, data, 0o644)
	if err == nil {
		err = os.Rename(tmp, b.path)
	}
	if err != nil {
		log.Printf("budget: %v", err)
	}
}

var bogota = time.FixedZone("COT", -5*3600)

func (b *budget) roll(now time.Time) {
	if d := now.In(bogota).Format(time.DateOnly); d != b.day || b.byIP == nil {
		b.day, b.spent, b.byIP = d, 0, map[string]float64{}
	}
}

// perIP is the visitor's daily budget: larger with a judge's code, none for the admin.
func (s *server) perIP(r *http.Request) float64 {
	switch s.access.role(r) {
	case roleAdmin:
		return s.budget.limit
	case roleJudge:
		return s.judgeIP
	}
	return s.budget.perIP
}

func (b *budget) allow(now time.Time, ip string, perIP float64) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.roll(now)
	if b.spent >= b.limit {
		return errors.New("the demo's model budget for today is used up; it resets at midnight Colombia time")
	}
	if b.byIP[ip] >= perIP {
		return errors.New("you have used today's demo budget for your connection; it resets at midnight Colombia time")
	}
	return nil
}

func (b *budget) spend(now time.Time, ip string, usd float64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.roll(now)
	b.spent += usd
	b.byIP[ip] += usd
	b.save()
}

func (b *budget) today(now time.Time) float64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.roll(now)
	return b.spent
}

// clientIP is the visitor's address. X-Real-IP is trusted only from a proxy on
// this machine (nginx, which resolves it from Cloudflare's CF-Connecting-IP).
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		if real := r.Header.Get("X-Real-IP"); real != "" {
			return real
		}
	}
	return host
}

// hub fans events out to every connected dashboard.
type hub struct {
	mu   sync.Mutex
	subs map[chan []byte]struct{}
}

func newHub() *hub { return &hub{subs: map[chan []byte]struct{}{}} }

func (h *hub) publish(event string, data any) {
	b, _ := json.Marshal(data)
	msg := []byte(fmt.Sprintf("event: %s\ndata: %s\n\n", event, b))
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.subs {
		select {
		case c <- msg:
		default: // a slow client misses an event rather than blocking the others
		}
	}
}

func (h *hub) serve(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		httpError(w, http.StatusInternalServerError, errors.New("streaming unsupported"))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	c := make(chan []byte, 16)
	h.mu.Lock()
	h.subs[c] = struct{}{}
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		delete(h.subs, c)
		h.mu.Unlock()
	}()
	fmt.Fprint(w, ": connected\n\n")
	fl.Flush()
	// Proxies (Cloudflare) drop connections idle for about 100 s.
	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case msg := <-c:
			w.Write(msg)
			fl.Flush()
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
			fl.Flush()
		}
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func httpError(w http.ResponseWriter, code int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func envFloat(k string, def float64) float64 {
	if v, err := strconv.ParseFloat(os.Getenv(k), 64); err == nil {
		return v
	}
	return def
}
