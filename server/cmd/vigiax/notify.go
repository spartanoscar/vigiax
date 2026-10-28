package main

import (
	"context"
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
	if e.Kind == "jump" {
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

var subscribeCmd = regexp.MustCompile(`(?i)^\s*(alertas|alerts)\s+([a-z0-9][a-z0-9-]{0,39})\s*$`)

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
		en := sub.Lang == "en"
		h.s.mu.Lock()
		st := h.s.stations[id]
		h.s.mu.Unlock()
		switch {
		case kind == "false" && st != nil:
			h.s.falseAlarm(st, "whatsapp …"+last4(m.From))
			reply(pick(en, "Marked as a false alarm. The level goes back to normal and Vigiax waits before raising it again.",
				"Marcada como falsa alarma. El nivel vuelve a normal y Vigiax espera antes de volver a subirlo."))
		case kind == "ack":
			h.s.hub.publish("ack", map[string]string{"station": id, "by": "…" + last4(m.From)})
			reply(pick(en, "Thanks: Vigiax knows you got the alert.", "Gracias: Vigiax sabe que recibiste la alerta."))
		}
		return
	}
	text := strings.TrimSpace(m.Text)
	if mm := subscribeCmd.FindStringSubmatch(text); mm != nil {
		en := strings.EqualFold(mm[1], "alerts")
		id := strings.ToLower(mm[2])
		h.s.mu.Lock()
		_, known := h.s.stations[id]
		h.s.mu.Unlock()
		if !known && !strings.HasPrefix(id, "twin-") {
			reply(pick(en, "That station does not exist.", "Esa estación no existe."))
			return
		}
		if _, err := h.s.station(id); err != nil {
			reply(pick(en, "That station does not exist.", "Esa estación no existe."))
			return
		}
		if err := h.s.subs.add(m.From, id, map[bool]string{true: "en", false: "es"}[en], now); err != nil {
			reply(pick(en, "This station already has the most phones it can take. Try another one.",
				"Esta estación ya tiene el máximo de teléfonos. Prueba con otra."))
			return
		}
		h.s.hub.publish("subscribed", map[string]string{"station": id})
		reply(pick(en,
			"You will receive the alerts of station "+id+" for 24 hours. Trigger a flash flood in the twin to try it. Write STOP to leave.",
			"Recibirás las alertas de la estación "+id+" durante 24 horas. Dispara una creciente en el gemelo para probarlo. Escribe SALIR para dejar de recibirlas."))
		return
	}
	switch strings.ToUpper(text) {
	case "STOP", "SALIR", "BAJA":
		h.s.subs.remove(m.From)
		reply(pick(strings.EqualFold(text, "stop"), "Done: you will not receive more alerts.", "Listo: no recibirás más alertas."))
		return
	}
	sub := h.s.subs.touch(m.From, now)
	en := sub != nil && sub.Lang == "en"
	reply(pick(en,
		"This is Vigiax, flash-flood early warning. To receive a station's alerts write ALERTS followed by the station code shown in the twin (vigiax.bukivo.com/twin/). STOP to leave.",
		"Hola, este es Vigiax, alertas tempranas de crecientes. Para recibir las alertas de una estación escribe ALERTAS y el código que muestra el gemelo (vigiax.bukivo.com/twin/). SALIR para dejar de recibirlas."))
}

func pick(en bool, english, spanish string) string {
	if en {
		return english
	}
	return spanish
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
