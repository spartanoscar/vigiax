// Package gpu is a client for an optional SAM 3.1 server on a rented GPU (the
// open weights, served over HTTP). Vigiax uses it only to draw the mask live
// over the camera view, a few frames a second; the level is still measured by
// SAM 3.1 through the Meta Model API. Without it, the live view falls back to
// the mask of the last measured frame.
//
// The GPU is rented for a session and gets a new address and token each time,
// so they are read from a small JSON file ({"url": ..., "token": ...}) that can
// be replaced while the service runs, or from GPU_URL and GPU_TOKEN.
package gpu

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// ErrOff means no GPU is linked, or it is not answering.
var ErrOff = errors.New("gpu: off")

type Client struct {
	path string // the link file; empty uses only the environment
	http *http.Client

	mu     sync.Mutex
	mod    time.Time
	link   link
	set    *link // given by Link (the service turned the GPU on); wins over the file
	health Health
	at     time.Time // when health was asked
}

type link struct {
	URL   string `json:"url"`
	Token string `json:"token"`
}

// Health is what the GPU says about itself: installing, loading, ready or error,
// and minutes left before it shuts itself down.
type Health struct {
	Stage       string  `json:"stage"`
	MinutesLeft float64 `json:"minutes_left"`
}

// New reads the link from path when it exists, else from GPU_URL and GPU_TOKEN.
func New(path string) *Client {
	return &Client{path: path, http: &http.Client{Timeout: 20 * time.Second}}
}

// Link points the client at a GPU the service turned on; an empty url unlinks it.
func (c *Client) Link(url, token string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.set, c.at = &link{URL: url, Token: token}, time.Time{}
	if url == "" {
		c.set = nil
	}
}

// Linked reports whether the client has a GPU to talk to.
func (c *Client) Linked() bool {
	l := c.current()
	return l.URL != "" && l.Token != ""
}

func (c *Client) current() link {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.set != nil {
		return *c.set
	}
	if c.path != "" {
		if fi, err := os.Stat(c.path); err == nil {
			if !fi.ModTime().Equal(c.mod) {
				var l link
				if b, err := os.ReadFile(c.path); err == nil && json.Unmarshal(b, &l) == nil {
					c.link, c.mod, c.at = l, fi.ModTime(), time.Time{}
				}
			}
			return c.link
		}
	}
	return link{URL: os.Getenv("GPU_URL"), Token: os.Getenv("GPU_TOKEN")}
}

// Health asks the GPU how it is, at most every 10 s; "off" when none is linked
// or it does not answer.
func (c *Client) Health(ctx context.Context) Health {
	l := c.current()
	if l.URL == "" || l.Token == "" {
		return Health{Stage: "off"}
	}
	c.mu.Lock()
	if time.Since(c.at) < 10*time.Second {
		h := c.health
		c.mu.Unlock()
		return h
	}
	c.mu.Unlock()
	// A linked GPU that does not answer yet is still starting; one that is gone
	// is unlinked by whoever linked it.
	h := Health{Stage: "starting"}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(l.URL, "/")+"/health", nil)
	if res, err := c.http.Do(req); err == nil {
		if res.StatusCode == http.StatusNotFound {
			h = Health{Stage: "off"}
		}
		// Before the API starts, the GPU host answers with its own HTML page.
		if res.StatusCode == http.StatusOK && json.NewDecoder(res.Body).Decode(&h) != nil {
			h = Health{Stage: "starting"}
		}
		res.Body.Close()
	}
	c.mu.Lock()
	c.health, c.at = h, time.Now()
	c.mu.Unlock()
	return h
}

// Object is one match: the phrase that found it, its box (x0, y0, x1, y1 in
// frame pixels), score, area in pixels and mask (a frame-sized 1-bit PNG,
// white on black, base64).
type Object struct {
	Label string     `json:"label"`
	Box   [4]float64 `json:"box"`
	Score float64    `json:"score"`
	Area  int        `json:"area"`
	Mask  string     `json:"mask,omitempty"`
}

type Result struct {
	Width   int      `json:"width"`
	Height  int      `json:"height"`
	Ms      float64  `json:"ms"` // GPU time
	Objects []Object `json:"objects"`
}

// Segment finds every phrase in one JPEG frame, with masks.
func (c *Client) Segment(ctx context.Context, jpeg []byte, prompts []string, threshold float64) (*Result, error) {
	l := c.current()
	if l.URL == "" || l.Token == "" {
		return nil, ErrOff
	}
	body, _ := json.Marshal(map[string]any{
		"image":     base64.StdEncoding.EncodeToString(jpeg),
		"prompts":   prompts,
		"threshold": threshold,
		"masks":     "png",
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(l.URL, "/")+"/segment", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+l.Token)
	res, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrOff, err)
	}
	defer res.Body.Close()
	b, err := io.ReadAll(io.LimitReader(res.Body, 32<<20))
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		if res.StatusCode >= 500 || res.StatusCode == http.StatusUnauthorized || !json.Valid(b) {
			return nil, fmt.Errorf("%w: %s", ErrOff, res.Status)
		}
		return nil, fmt.Errorf("gpu: %s: %.200s", res.Status, b)
	}
	var r Result
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, fmt.Errorf("gpu: %w", err)
	}
	return &r, nil
}
