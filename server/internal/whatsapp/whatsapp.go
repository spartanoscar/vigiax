// Package whatsapp sends messages through the WhatsApp Business Cloud API and
// reads its webhooks (incoming messages and button replies).
package whatsapp

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const DefaultVersion = "v25.0"

type Client struct {
	Token   string // system user token with whatsapp_business_messaging
	PhoneID string // phone number id of the sender
	Version string
	HTTP    *http.Client
}

func NewClient(token, phoneID, version string) *Client {
	if version == "" {
		version = DefaultVersion
	}
	return &Client{Token: token, PhoneID: phoneID, Version: version, HTTP: &http.Client{Timeout: 20 * time.Second}}
}

// Button is a quick reply button: Title is shown (at most 20 characters), ID comes back in the webhook.
type Button struct{ ID, Title string }

// Text sends a free-form message. WhatsApp allows it only within 24 hours of
// the person's last message.
func (c *Client) Text(ctx context.Context, to, body string) error {
	return c.send(ctx, map[string]any{"to": to, "type": "text", "text": map[string]any{"body": body}})
}

// Buttons sends a message with up to three reply buttons (also within 24 hours).
func (c *Client) Buttons(ctx context.Context, to, body string, buttons []Button) error {
	var bs []map[string]any
	for _, b := range buttons {
		bs = append(bs, map[string]any{"type": "reply", "reply": map[string]string{"id": b.ID, "title": b.Title}})
	}
	return c.send(ctx, map[string]any{"to": to, "type": "interactive", "interactive": map[string]any{
		"type": "button", "body": map[string]string{"text": body}, "action": map[string]any{"buttons": bs}}})
}

// Template sends an approved template with its body parameters, at any time.
// Templates with quick reply buttons also need payloads for them.
func (c *Client) Template(ctx context.Context, to, name, lang string, params []string, buttonPayloads []string) error {
	var ps []map[string]string
	for _, p := range params {
		ps = append(ps, map[string]string{"type": "text", "text": p})
	}
	comps := []map[string]any{{"type": "body", "parameters": ps}}
	for i, p := range buttonPayloads {
		comps = append(comps, map[string]any{"type": "button", "sub_type": "quick_reply", "index": fmt.Sprint(i),
			"parameters": []map[string]string{{"type": "payload", "payload": p}}})
	}
	return c.send(ctx, map[string]any{"to": to, "type": "template", "template": map[string]any{
		"name": name, "language": map[string]string{"code": lang}, "components": comps}})
}

// Register activates the number on the Cloud API with a six-digit PIN (two-step verification).
func (c *Client) Register(ctx context.Context, pin string) error {
	return c.post(ctx, c.PhoneID+"/register", map[string]any{"messaging_product": "whatsapp", "pin": pin})
}

func (c *Client) send(ctx context.Context, msg map[string]any) error {
	msg["messaging_product"] = "whatsapp"
	msg["recipient_type"] = "individual"
	return c.post(ctx, c.PhoneID+"/messages", msg)
}

func (c *Client) post(ctx context.Context, path string, body any) error {
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://graph.facebook.com/"+c.Version+"/"+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")
	res, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	out, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
	if res.StatusCode != http.StatusOK {
		var e struct {
			Error struct {
				Message string `json:"message"`
				Code    int    `json:"code"`
			} `json:"error"`
		}
		json.Unmarshal(out, &e)
		return fmt.Errorf("whatsapp: %s: %d %s", res.Status, e.Error.Code, e.Error.Message)
	}
	return nil
}

// ValidSignature checks the X-Hub-Signature-256 header of a webhook body.
func ValidSignature(appSecret string, body []byte, header string) bool {
	sig, ok := strings.CutPrefix(header, "sha256=")
	if !ok || appSecret == "" {
		return false
	}
	want, err := hex.DecodeString(sig)
	if err != nil {
		return false
	}
	m := hmac.New(sha256.New, []byte(appSecret))
	m.Write(body)
	return hmac.Equal(m.Sum(nil), want)
}

// Incoming is a message a person sent to the number.
type Incoming struct {
	From     string // phone number, digits only
	Text     string // typed text
	ButtonID string // id or payload of the button pressed, if any
	T        time.Time
}

// Parse extracts the incoming messages of a webhook body (statuses are ignored).
func Parse(body []byte) ([]Incoming, error) {
	var w struct {
		Entry []struct {
			Changes []struct {
				Value struct {
					Messages []struct {
						From      string `json:"from"`
						Timestamp string `json:"timestamp"`
						Type      string `json:"type"`
						Text      struct {
							Body string `json:"body"`
						} `json:"text"`
						Button struct {
							Payload string `json:"payload"`
							Text    string `json:"text"`
						} `json:"button"` // quick reply of a template
						Interactive struct {
							ButtonReply struct {
								ID    string `json:"id"`
								Title string `json:"title"`
							} `json:"button_reply"`
						} `json:"interactive"`
					} `json:"messages"`
				} `json:"value"`
			} `json:"changes"`
		} `json:"entry"`
	}
	if err := json.Unmarshal(body, &w); err != nil {
		return nil, err
	}
	var out []Incoming
	for _, e := range w.Entry {
		for _, ch := range e.Changes {
			for _, m := range ch.Value.Messages {
				in := Incoming{From: m.From, T: time.Now()}
				var sec int64
				if _, err := fmt.Sscan(m.Timestamp, &sec); err == nil {
					in.T = time.Unix(sec, 0)
				}
				switch m.Type {
				case "text":
					in.Text = m.Text.Body
				case "button":
					in.ButtonID, in.Text = m.Button.Payload, m.Button.Text
				case "interactive":
					in.ButtonID, in.Text = m.Interactive.ButtonReply.ID, m.Interactive.ButtonReply.Title
				default:
					continue
				}
				out = append(out, in)
			}
		}
	}
	return out, nil
}
