package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/spartanoscar/vigiax/server/internal/alert"
	"github.com/spartanoscar/vigiax/server/internal/station"
)

func testServer(t *testing.T) *server {
	dir := t.TempDir()
	return &server{dir: dir, hub: newHub(), access: newAccess("admin-code", "judge-code"),
		budget: loadBudget(dir+"/b.json", 3, 0.5), live: loadBudget(dir+"/l.json", 0.5, 0.5),
		stations: map[string]*station.Station{}, lastAt: map[string]time.Time{}, alertState: map[string]*stationAlerts{}}
}

func signIn(t *testing.T, s *server, code string) (*http.Cookie, int) {
	w := httptest.NewRecorder()
	s.handleAccess(w, httptest.NewRequest("POST", "/api/access", strings.NewReader(`{"code":"`+code+`"}`)))
	for _, c := range w.Result().Cookies() {
		return c, w.Code
	}
	return nil, w.Code
}

func TestAccessCodes(t *testing.T) {
	s := testServer(t)
	c, code := signIn(t, s, "judge-code")
	if code != 200 || c == nil || !c.HttpOnly {
		t.Fatalf("judge sign-in: %d %v", code, c)
	}
	r := httptest.NewRequest("GET", "/", nil)
	r.AddCookie(c)
	if got := s.access.role(r); got != roleJudge {
		t.Fatalf("role %q", got)
	}
	forged := httptest.NewRequest("GET", "/", nil)
	forged.AddCookie(&http.Cookie{Name: accessCookie, Value: "admin." + strings.Repeat("0", 64)})
	if got := s.access.role(forged); got != rolePublic {
		t.Fatalf("forged cookie gave %q", got)
	}
	for range maxAttempts {
		if _, code := signIn(t, s, "guess"); code != http.StatusForbidden {
			t.Fatalf("wrong code: %d", code)
		}
	}
	if _, code := signIn(t, s, "admin-code"); code != http.StatusTooManyRequests {
		t.Fatalf("after %d wrong codes: %d, want 429", maxAttempts, code)
	}
}

func TestRealStationsNeedACode(t *testing.T) {
	s := testServer(t)
	st, _ := s.station("el-reno")
	st.Update(func(c *station.Config) { c.Source = &station.Source{Kind: "usgs"} })
	put := func(id string, c *http.Cookie) int {
		cfg := alert.TwinDefaults()
		b, _ := json.Marshal(cfg)
		r := httptest.NewRequest("PUT", "/api/stations/"+id+"/alerts", strings.NewReader(string(b)))
		r.SetPathValue("id", id)
		if c != nil {
			r.AddCookie(c)
		}
		w := httptest.NewRecorder()
		s.handleAlerts(w, r)
		return w.Code
	}
	if code := put("twin-abc123", nil); code != 200 {
		t.Fatalf("own twin: %d", code)
	}
	if code := put("el-reno", nil); code != http.StatusForbidden {
		t.Fatalf("real station without code: %d", code)
	}
	c, _ := signIn(t, s, "judge-code")
	if code := put("el-reno", c); code != 200 {
		t.Fatalf("real station with judge code: %d", code)
	}
}

func stationReading(t time.Time, cm float64) station.Reading {
	return station.Reading{T: t, LevelCm: &cm, Confidence: 0.9}
}

func TestStationIn(t *testing.T) {
	for text, want := range map[string]string{
		"ALERTS twin-gg263i":                            "twin-gg263i",
		"Send ALERTAS twin-gg263i to +57 300 112 8564.": "twin-gg263i",
		"twin-GG263I":                                   "twin-gg263i",
		"alerts el-reno":                                "el-reno",
		"ALERTS: some-station":                          "some-station",
		"hola":                                          "",
		"I want alerts":                                 "",
	} {
		if got := stationIn(text); got != want {
			t.Errorf("%q: got %q, want %q", text, got, want)
		}
	}
	if !stopCmd.MatchString("Stop.") || stopCmd.MatchString("don't stop") {
		t.Error("STOP matching")
	}
}
