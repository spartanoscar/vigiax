package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/spartanoscar/vigiax/server/internal/alert"
	"github.com/spartanoscar/vigiax/server/internal/whatsapp"
)

// People subscribe themselves: they write "ALERTAS <station>" (Spanish) or
// "ALERTS <station>" (English) to the number. Nobody can sign up someone else's
// phone, and every alert falls within the 24 hours WhatsApp allows after a
// person's message, so it needs no template. A subscription ends 24 hours after
// the person's last message, and the number is deleted with it.

const (
	subscriptionTTL   = 24 * time.Hour
	maxSubsPerStation = 10
	maxSentPerPhone   = 40 // per subscription, so a flapping station cannot flood a phone
)

type subscriber struct {
	Phone   string    `json:"phone"`
	Station string    `json:"station"`
	Lang    string    `json:"lang"`
	LastIn  time.Time `json:"last_in"` // last message from the person
	Sent    int       `json:"sent"`
}

type subscribers struct {
	mu   sync.Mutex
	path string
	m    map[string]*subscriber // by phone
}

func loadSubscribers(path string) *subscribers {
	s := &subscribers{path: path, m: map[string]*subscriber{}}
	if b, err := os.ReadFile(path); err == nil {
		json.Unmarshal(b, &s.m)
	}
	return s
}

// expire drops subscriptions older than the TTL; s.mu must be held.
func (s *subscribers) expire(now time.Time) {
	for p, sub := range s.m {
		if now.Sub(sub.LastIn) > subscriptionTTL {
			delete(s.m, p)
		}
	}
}

func (s *subscribers) save() {
	b, _ := json.Marshal(s.m)
	if err := os.WriteFile(s.path, b, 0o600); err != nil {
		log.Printf("subscribers: %v", err)
	}
}

func (s *subscribers) count(station string, now time.Time) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expire(now)
	n := 0
	for _, sub := range s.m {
		if sub.Station == station {
			n++
		}
	}
	return n
}

func (s *subscribers) of(station string, now time.Time) []subscriber {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expire(now)
	var out []subscriber
	for _, sub := range s.m {
		if sub.Station == station && sub.Sent < maxSentPerPhone {
			sub.Sent++
			out = append(out, *sub)
		}
	}
	s.save()
	return out
}

func (s *subscribers) touch(phone string, now time.Time) *subscriber {
	s.mu.Lock()
	defer s.mu.Unlock()
	sub, ok := s.m[phone]
	if !ok {
		return nil
	}
	sub.LastIn = now
	s.save()
	c := *sub
	return &c
}

func (s *subscribers) add(phone, station, lang string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expire(now)
	n := 0
	for p, sub := range s.m {
		if sub.Station == station && p != phone {
			n++
		}
	}
	if n >= maxSubsPerStation {
		return fmt.Errorf("station %s has %d phones signed up already", station, n)
	}
	s.m[phone] = &subscriber{Phone: phone, Station: station, Lang: lang, LastIn: now}
	s.save()
	return nil
}

func (s *subscribers) remove(phone string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, phone)
	s.save()
}

var audienceName = map[string][2]string{ // es, en
	"leader":   {"el líder comunitario", "the community leader"},
	"group":    {"el grupo de la vereda", "the village group"},
	"everyone": {"toda la comunidad", "the whole community"},
}

// setupWhatsApp enables the WhatsApp channel when its settings are present.
func (s *server) setupWhatsApp(mux *http.ServeMux, dataDir string) {
	token, phoneID := os.Getenv("WA_TOKEN"), os.Getenv("WA_PHONE_ID")
	if token == "" || phoneID == "" {
		log.Print("WhatsApp off: WA_TOKEN or WA_PHONE_ID not set")
		return
	}
	s.waNumber = os.Getenv("WA_NUMBER") // shown to visitors only when the channel works
	wa := whatsapp.NewClient(token, phoneID, os.Getenv("WA_GRAPH_VERSION"))
	s.subs = loadSubscribers(dataDir + "/subscribers.json")
	s.notify = func(id string, c *alert.Config, e alert.Event) { s.sendAlert(wa, id, c, e) }
	h := &waHook{s: s, wa: wa, secret: os.Getenv("WA_APP_SECRET"), verify: os.Getenv("WA_VERIFY_TOKEN")}
	mux.HandleFunc("GET /api/whatsapp/webhook", h.verifyHook)
	mux.HandleFunc("POST /api/whatsapp/webhook", h.receive)
	log.Printf("WhatsApp on: %s", s.waNumber)
}

func (s *server) sendAlert(wa *whatsapp.Client, id string, c *alert.Config, e alert.Event) {
	if e.Kind == "jump" || e.Kind == "moved" {
		return // shown on the dashboard; the agent will check the camera before anyone is messaged
	}
	rule := c.Rule(e.To)
	if e.Kind == "lower" || e.Kind == "false_alarm" || e.Kind == "blind" {
		rule = c.Rule(e.From) // the people who heard the alert hear it is over
	}
	if rule == nil || !contains(rule.Channels, "whatsapp") {
		return
	}
	s.mu.Lock()
	st := s.stations[id]
	s.mu.Unlock()
	demo := st != nil && st.Config().Source == nil
	for _, sub := range s.subs.of(id, time.Now()) {
		en := sub.Lang == "en"
		lang := map[bool]string{true: "en", false: "es"}[en]
		body := alert.Text(alert.Fill(c, e, lang), lang)
		if demo {
			who := audienceName[rule.Audience][map[bool]int{true: 1, false: 0}[en]]
			if en {
				body += "\n\n(Demo: in the field this level goes to " + who + ".)"
			} else {
				body += "\n\n(Demo: en campo este nivel le llega a " + who + ".)"
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		var err error
		if e.Kind == "raise" {
			ack, falseAlarm := "Recibido", "Falsa alarma"
			if en {
				ack, falseAlarm = "Got it", "False alarm"
			}
			err = wa.Buttons(ctx, sub.Phone, body, []whatsapp.Button{{ID: "ack:" + id, Title: ack}, {ID: "false:" + id, Title: falseAlarm}})
		} else {
			err = wa.Text(ctx, sub.Phone, body)
		}
		cancel()
		if err != nil {
			log.Printf("%s: whatsapp to …%s: %v", id, last4(sub.Phone), err)
		}
	}
}

type waHook struct {
	s              *server
	wa             *whatsapp.Client
	secret, verify string
}

// verifyHook answers Meta's subscription check.
func (h *waHook) verifyHook(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if h.verify == "" || q.Get("hub.mode") != "subscribe" || q.Get("hub.verify_token") != h.verify {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	io.WriteString(w, q.Get("hub.challenge"))
}

// A sign-up names a station: "ALERTS twin-ab12cd", the whole line copied
// from the dashboard, or just the code. ALERTAS works too.
var (
	stationCode  = regexp.MustCompile(`(?i)\b(twin-[a-z0-9]{6}|el-reno)\b`)
	subscribeCmd = regexp.MustCompile(`(?i)\b(?:alerts?|alertas)\b[\s:]*([a-z0-9][a-z0-9-]{1,39})`)
	stopCmd      = regexp.MustCompile(`(?i)^\W*(stop|unsubscribe|salir|baja)\W*$`)
)

func stationIn(text string) string {
	if m := stationCode.FindStringSubmatch(text); m != nil {
		return strings.ToLower(m[1])
	}
	if m := subscribeCmd.FindStringSubmatch(text); m != nil {
		return strings.ToLower(m[1])
	}
	return ""
}

func (h *waHook) receive(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil || !whatsapp.ValidSignature(h.secret, body, r.Header.Get("X-Hub-Signature-256")) {
		http.Error(w, "bad signature", http.StatusUnauthorized)
		return
	}
	w.WriteHeader(http.StatusOK) // Meta retries anything slow or failing: answer first
	msgs, err := whatsapp.Parse(body)
	if err != nil {
		log.Printf("whatsapp webhook: %v", err)
		return
	}
	for _, m := range msgs {
		go h.handle(m)
	}
}

func (h *waHook) handle(m whatsapp.Incoming) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	reply := func(text string) {
		if err := h.wa.Text(ctx, m.From, text); err != nil {
			log.Printf("whatsapp reply to …%s: %v", last4(m.From), err)
		}
	}
	now := time.Now()
	if kind, id, ok := strings.Cut(m.ButtonID, ":"); ok {
		sub := h.s.subs.touch(m.From, now)
		if sub == nil || sub.Station != id {
			return
		}
		h.s.mu.Lock()
		st := h.s.stations[id]
		h.s.mu.Unlock()
		switch {
		case kind == "false" && st != nil:
			h.s.falseAlarm(st, "whatsapp …"+last4(m.From))
			reply("Marked as a false alarm. The level goes back to normal, and Vigiax waits before raising it again.")
		case kind == "ack":
			h.s.hub.publish("ack", map[string]string{"station": id, "by": "…" + last4(m.From)})
			reply("Thanks: Vigiax knows you got the alert.")
		}
		return
	}
	text := strings.TrimSpace(m.Text)
	if stopCmd.MatchString(text) {
		h.s.subs.remove(m.From)
		reply("Done: you will not receive more alerts from Vigiax.")
		return
	}
	if id := stationIn(text); id != "" {
		h.s.mu.Lock()
		_, known := h.s.stations[id]
		h.s.mu.Unlock()
		if !known && !strings.HasPrefix(id, "twin-") {
			reply("There is no station " + id + ". Copy the code from the Alerts panel at vigiax.bukivo.com/twin/ (it looks like twin-ab12cd).")
			return
		}
		if _, err := h.s.station(id); err != nil {
			reply("There is no station " + id + ". Copy the code from the Alerts panel at vigiax.bukivo.com/twin/ (it looks like twin-ab12cd).")
			return
		}
		if err := h.s.subs.add(m.From, id, "en", now); err != nil {
			reply("Station " + id + " already has as many phones as it can take. Open the twin in another browser to get a new station.")
			return
		}
		h.s.hub.publish("subscribed", map[string]string{"station": id})
		reply("You're in. For the next 24 hours you'll get the alerts of station " + id + " here.\n\nTry it: start the camera node in the twin and trigger a flash flood. Send STOP to leave.")
		return
	}
	// No code: someone who started on the phone. Already signed up: remind
	// them of their station. New: give them a twin station of their own, so
	// the flow works in either order.
	if sub := h.s.subs.touch(m.From, now); sub != nil {
		reply("You're getting the alerts of station " + sub.Station + ".\n\nOpen " + twinLink(sub.Station) +
			", start the camera node and trigger a flash flood.\n\nSend STOP to leave.")
		return
	}
	id := newTwinID()
	if _, err := h.s.station(id); err != nil {
		log.Printf("whatsapp: new station: %v", err)
		return
	}
	if err := h.s.subs.add(m.From, id, "en", now); err != nil {
		log.Printf("whatsapp: subscribe: %v", err)
		return
	}
	reply("Hi! This is Vigiax, flash-flood early warning.\n\nYour station is " + id + ", and for the next 24 hours you'll get its alerts here.\n\nOpen " +
		twinLink(id) + ", start the camera node and trigger a flash flood.\n\nSend STOP to leave.")
}

func twinLink(id string) string { return "https://vigiax.bukivo.com/twin/?station=" + id }

// newTwinID makes a station id like the dashboard's own: twin- and six base-36 characters.
func newTwinID() string {
	const alphabet = "0123456789abcdefghijklmnopqrstuvwxyz"
	b := make([]byte, 6)
	rand.Read(b)
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return "twin-" + string(b)
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func last4(p string) string {
	if len(p) <= 4 {
		return p
	}
	return p[len(p)-4:]
}
