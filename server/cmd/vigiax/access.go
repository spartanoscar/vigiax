package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Roles. Anyone can configure and test their own twin station; a judge's code
// (given in the submission) also unlocks the real stations and a larger model
// budget; the admin code is the owner's.
const (
	rolePublic = "public"
	roleJudge  = "judge"
	roleAdmin  = "admin"
)

const accessCookie = "vigiax_access"

type access struct {
	admin, judge string // codes from the environment; empty disables the role
	key          []byte // signs the cookie; derived from the codes, so new codes end old sessions

	mu       sync.Mutex
	attempts map[string][]time.Time // wrong codes per IP, to stop guessing
}

func newAccess(admin, judge string) *access {
	k := sha256.Sum256([]byte("vigiax-access\x00" + admin + "\x00" + judge))
	return &access{admin: admin, judge: judge, key: k[:], attempts: map[string][]time.Time{}}
}

func (a *access) sign(role string) string {
	m := hmac.New(sha256.New, a.key)
	m.Write([]byte(role))
	return role + "." + hex.EncodeToString(m.Sum(nil))
}

// role reads the visitor's role from the signed cookie.
func (a *access) role(r *http.Request) string {
	c, err := r.Cookie(accessCookie)
	if err != nil {
		return rolePublic
	}
	role, _, _ := strings.Cut(c.Value, ".")
	if (role == roleAdmin && a.admin != "") || (role == roleJudge && a.judge != "") {
		if hmac.Equal([]byte(c.Value), []byte(a.sign(role))) {
			return role
		}
	}
	return rolePublic
}

const (
	maxAttempts   = 8
	attemptWindow = time.Hour
)

func (a *access) tooMany(ip string, now time.Time) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	keep := a.attempts[ip][:0]
	for _, t := range a.attempts[ip] {
		if now.Sub(t) < attemptWindow {
			keep = append(keep, t)
		}
	}
	a.attempts[ip] = keep
	return len(keep) >= maxAttempts
}

func (a *access) failed(ip string, now time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.attempts[ip] = append(a.attempts[ip], now)
}

func equalCode(given, want string) bool {
	return want != "" && subtle.ConstantTimeCompare([]byte(given), []byte(want)) == 1
}

// GET /api/access → {"role": ...}; POST {"code": ...} signs in; DELETE signs out.
func (s *server) handleAccess(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, map[string]string{"role": s.access.role(r)})
	case http.MethodDelete:
		http.SetCookie(w, &http.Cookie{Name: accessCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode})
		writeJSON(w, map[string]string{"role": rolePublic})
	case http.MethodPost:
		now, ip := time.Now(), clientIP(r)
		if s.access.tooMany(ip, now) {
			httpError(w, http.StatusTooManyRequests, errors.New("too many wrong codes; try again in an hour"))
			return
		}
		var body struct{ Code string }
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&body); err != nil {
			httpError(w, http.StatusBadRequest, err)
			return
		}
		code := strings.TrimSpace(body.Code)
		role := ""
		switch {
		case equalCode(code, s.access.admin):
			role = roleAdmin
		case equalCode(code, s.access.judge):
			role = roleJudge
		}
		if role == "" {
			s.access.failed(ip, now)
			httpError(w, http.StatusForbidden, errors.New("that code is not valid"))
			return
		}
		http.SetCookie(w, &http.Cookie{Name: accessCookie, Value: s.access.sign(role), Path: "/", MaxAge: 14 * 24 * 3600,
			HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode})
		writeJSON(w, map[string]string{"role": role})
	default:
		httpError(w, http.StatusMethodNotAllowed, errors.New("use GET, POST or DELETE"))
	}
}
