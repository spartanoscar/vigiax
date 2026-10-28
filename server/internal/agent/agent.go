// Package agent is the reasoning layer over the alert rules. The rules set a
// floor; when they change level (or the camera jumps or goes blind) the agent
// wakes, reads the measurements, the scene and the day's history, and decides
// whether the alert should go higher than the rules alone put it.
//
// It never writes what people receive (fixed templates do) and never lowers an
// alert. Every number it cites is checked against the measurements; after three
// rejected answers the rule level stands and a person is asked to decide.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/spartanoscar/vigiax/server/internal/alert"
	"github.com/spartanoscar/vigiax/server/internal/scene"
)

// Facts is everything the agent knows, all of it measured or configured.
type Facts struct {
	Station     string         `json:"station"`
	Place       string         `json:"place"`
	Now         time.Time      `json:"now"`
	Woke        alert.Event    `json:"woke_by"`    // the event that woke the agent
	RuleLevel   alert.Level    `json:"rule_level"` // the floor
	LevelCm     *float64       `json:"level_cm"`
	Rise5       *float64       `json:"rise_5min_cm_per_min"`
	Rise30      *float64       `json:"rise_30min_cm_per_min"`
	Recent      []Point        `json:"recent_levels"` // oldest first
	Lines       map[string]any `json:"rules"`
	Scene       *scene.Reading `json:"scene,omitempty"`
	History     []alert.Event  `json:"today"`
	Subscribers int            `json:"phones_signed_up"`
}

type Point struct {
	MinutesAgo float64 `json:"minutes_ago"`
	LevelCm    float64 `json:"level_cm"`
}

// Decision is the agent's answer.
type Decision struct {
	Level        string   `json:"level"`  // normal, attention, alert or evacuate
	Action       string   `json:"action"` // hold (keep the rule level) or raise
	Reason       string   `json:"reason"` // for the dashboard; never sent to people
	CitedLevelCm *float64 `json:"cited_level_cm"`
	CitedRise    *float64 `json:"cited_rise_cm_per_min"`
}

var decisionSchema = map[string]any{
	"type":                 "object",
	"additionalProperties": false,
	"required":             []string{"level", "action", "reason", "cited_level_cm", "cited_rise_cm_per_min"},
	"properties": map[string]any{
		"level":                 map[string]any{"type": "string", "enum": []string{"normal", "attention", "alert", "evacuate"}},
		"action":                map[string]any{"type": "string", "enum": []string{"hold", "raise"}},
		"reason":                map[string]any{"type": "string"},
		"cited_level_cm":        map[string]any{"type": []string{"number", "null"}},
		"cited_rise_cm_per_min": map[string]any{"type": []string{"number", "null"}},
	},
}

const instructions = `You are the duty officer of Vigiax, a flash-flood early-warning system for a mountain community.
Fixed rules have already set an alert level (rule_level) from the measured water level and rise rate.
Decide whether the level should stay (action "hold", level = rule_level) or go HIGHER (action "raise").

Raise only for a concrete reason in the facts, for example:
- people or vehicles near the water while the water is rising or above a line;
- muddy or turbulent water carrying debris while the water is rising (a flood front);
- the rise is accelerating (5-minute rate well above the 30-minute rate) and the next line is near.
Never go below rule_level. When in doubt between two levels, prefer the safer (higher) one only if the facts support it; otherwise hold.
A camera jump (woke_by.kind "jump") may be a moved camera or a flood front: weigh the scene before raising.

Cite only numbers that appear in the facts: cited_level_cm is the current level_cm (or null), cited_rise_cm_per_min is rise_5min or rise_30min (or null).
reason: at most 2 sentences in plain English for the people running the dashboard. Do not invent measurements.`

// Step is one line of the agent console.
type Step struct {
	T    time.Time `json:"t"`
	Kind string    `json:"kind"` // wake, scene, think, verify, decide, escalate, error
	Text string    `json:"text"`
	USD  float64   `json:"usd,omitempty"`
}

// Result is a finished run.
type Result struct {
	Facts     Facts     `json:"facts"`
	Decision  *Decision `json:"decision,omitempty"`
	Verified  bool      `json:"verified"`
	Escalated bool      `json:"escalated"` // three rejected answers: a person decides
	Attempts  int       `json:"attempts"`
	Tokens    Usage     `json:"tokens"`
	CostUSD   float64   `json:"cost_usd"` // model calls of this run (scene + decision); decision cost is an estimate
	Seconds   float64   `json:"seconds"`
	Steps     []Step    `json:"steps"`
}

const maxAttempts = 3

// Decide asks Muse Spark Open, checks the answer and asks again with the
// checker's feedback, up to three times. say receives each console step as it happens.
func Decide(ctx context.Context, b *Baseten, f Facts, say func(Step)) Result {
	start := time.Now()
	r := Result{Facts: f}
	step := func(kind, text string, usd float64) {
		s := Step{T: time.Now(), Kind: kind, Text: text, USD: usd}
		r.Steps = append(r.Steps, s)
		if say != nil {
			say(s)
		}
	}
	facts, _ := json.MarshalIndent(f, "", "  ")
	msgs := []Message{{Role: "system", Content: instructions}, {Role: "user", Content: "Facts:\n" + string(facts)}}
	for r.Attempts < maxAttempts {
		r.Attempts++
		text, u, err := b.Chat(ctx, msgs, "decision", decisionSchema)
		r.Tokens.PromptTokens += u.PromptTokens
		r.Tokens.CompletionTokens += u.CompletionTokens
		r.CostUSD += u.CostUSD()
		if err != nil {
			step("error", err.Error(), u.CostUSD())
			break
		}
		var d Decision
		if err := json.Unmarshal([]byte(text), &d); err != nil {
			step("verify", "answer is not valid JSON; asking again", u.CostUSD())
			msgs = append(msgs, Message{Role: "assistant", Content: text}, Message{Role: "user", Content: "Answer with the JSON object only."})
			continue
		}
		step("think", fmt.Sprintf("%s → %s: %s", d.Action, d.Level, d.Reason), u.CostUSD())
		if problems := Check(d, f); len(problems) > 0 {
			step("verify", "rejected: "+strings.Join(problems, "; "), 0)
			msgs = append(msgs, Message{Role: "assistant", Content: text},
				Message{Role: "user", Content: "Your answer failed these checks: " + strings.Join(problems, "; ") + ". Answer again, citing only the facts."})
			continue
		}
		step("verify", "numbers match the measurements", 0)
		r.Decision, r.Verified = &d, true
		break
	}
	if !r.Verified {
		r.Escalated = true
		step("escalate", fmt.Sprintf("no verified answer after %d attempt(s): the rule level (%s) stands and a person should decide", r.Attempts, f.RuleLevel), 0)
	}
	r.Seconds = time.Since(start).Seconds()
	return r
}

var (
	cmNumber  = regexp.MustCompile(`(\d+(?:\.\d+)?)\s*cm\b`)
	anyNumber = regexp.MustCompile(`\d+(?:\.\d+)?`)
)

// Check is the verifier: the level must be valid and not below the rules, and
// every number the agent cites (in its fields or in "123 cm" in its reason)
// must be one the facts contain.
func Check(d Decision, f Facts) []string {
	var p []string
	var lv alert.Level
	if err := lv.UnmarshalJSON([]byte(strconv.Quote(d.Level))); err != nil {
		return []string{"level must be normal, attention, alert or evacuate"}
	}
	switch {
	case lv < f.RuleLevel:
		p = append(p, fmt.Sprintf("level %s is below the rule level %s; the agent may never lower an alert", lv, f.RuleLevel))
	case d.Action == "hold" && lv != f.RuleLevel:
		p = append(p, fmt.Sprintf(`"hold" keeps the rule level %s, not %s`, f.RuleLevel, lv))
	case d.Action == "raise" && lv == f.RuleLevel:
		p = append(p, `"raise" needs a level above the rule level`)
	}
	if strings.TrimSpace(d.Reason) == "" || len(d.Reason) > 500 {
		p = append(p, "reason must be 1-2 sentences")
	}
	near := func(a, b, tol float64) bool { return math.Abs(a-b) <= tol }
	if d.CitedLevelCm != nil {
		if f.LevelCm == nil || !near(*d.CitedLevelCm, *f.LevelCm, max(5, *f.LevelCm*0.03)) {
			p = append(p, fmt.Sprintf("cited_level_cm %.1f is not the measured level", *d.CitedLevelCm))
		}
	}
	if d.CitedRise != nil {
		ok := false
		for _, r := range []*float64{f.Rise5, f.Rise30} {
			if r != nil && near(*d.CitedRise, *r, max(0.3, math.Abs(*r)*0.15)) {
				ok = true
			}
		}
		if !ok {
			p = append(p, fmt.Sprintf("cited rise %.2f cm/min is neither the 5-minute nor the 30-minute rate", *d.CitedRise))
		}
	}
	known := knownCm(f)
	for _, m := range cmNumber.FindAllStringSubmatch(d.Reason, -1) {
		v, _ := strconv.ParseFloat(m[1], 64)
		found := false
		for _, k := range known {
			if near(v, k, max(5, k*0.05)) {
				found = true
				break
			}
		}
		if !found {
			p = append(p, fmt.Sprintf("the reason cites %s cm, which is not in the facts", m[1]))
		}
	}
	return p
}

// knownCm lists the centimetre values the facts contain: the level, recent
// levels, the rule lines, and the rises over 5 and 30 minutes.
func knownCm(f Facts) []float64 {
	var k []float64
	if f.LevelCm != nil {
		k = append(k, *f.LevelCm)
	}
	for _, p := range f.Recent {
		k = append(k, p.LevelCm)
	}
	if f.Rise5 != nil {
		k = append(k, math.Abs(*f.Rise5)*5, math.Abs(*f.Rise5))
	}
	if f.Rise30 != nil {
		k = append(k, math.Abs(*f.Rise30)*30, math.Abs(*f.Rise30))
	}
	for _, m := range anyNumber.FindAllString(f.Woke.Reason, -1) { // e.g. the size of a jump
		if v, err := strconv.ParseFloat(m, 64); err == nil {
			k = append(k, math.Abs(v))
		}
	}
	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			for key, y := range x {
				if strings.HasSuffix(key, "_cm") {
					if n, ok := y.(float64); ok {
						k = append(k, n)
					}
				}
				walk(y)
			}
		case []any:
			for _, y := range x {
				walk(y)
			}
		}
	}
	walk(f.Lines)
	return k
}
