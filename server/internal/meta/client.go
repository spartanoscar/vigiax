// Package meta is a minimal client for the Meta Model API Responses endpoint
// (https://api.meta.ai/v1/responses), shared by SAM 3.1 and Muse Spark.
package meta

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const DefaultBaseURL = "https://api.meta.ai/v1"

type Client struct {
	BaseURL string
	APIKey  string
	HTTP    *http.Client
}

func NewClient(apiKey string) *Client {
	return &Client{BaseURL: DefaultBaseURL, APIKey: apiKey, HTTP: &http.Client{Timeout: 60 * time.Second}}
}

// Content is one part of a user message: input_text or input_image.
type Content struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	ImageURL string `json:"image_url,omitempty"`
}

func Text(s string) Content { return Content{Type: "input_text", Text: s} }

// JPEG inlines image bytes as a base64 data URL.
func JPEG(b []byte) Content {
	return Content{Type: "input_image", ImageURL: "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(b)}
}

type Message struct {
	Type    string    `json:"type"`
	Role    string    `json:"role"`
	Content []Content `json:"content"`
}

func User(parts ...Content) Message { return Message{Type: "message", Role: "user", Content: parts} }

// Format is the Responses API structured-output setting (text.format).
type Format struct {
	Type   string          `json:"type"`
	Name   string          `json:"name"`
	Schema json.RawMessage `json:"schema"`
	Strict bool            `json:"strict,omitempty"`
}

type Request struct {
	Model        string            `json:"model"`
	Instructions string            `json:"instructions,omitempty"`
	Input        []Message         `json:"input"`
	Metadata     map[string]string `json:"metadata,omitempty"`
	Text         *struct {
		Format Format `json:"format"`
	} `json:"text,omitempty"`
	Reasoning *struct {
		Effort string `json:"effort"`
	} `json:"reasoning,omitempty"`
}

// WithSchema constrains the output to a JSON schema.
func (r *Request) WithSchema(name string, schema json.RawMessage) {
	r.Text = &struct {
		Format Format `json:"format"`
	}{Format{Type: "json_schema", Name: name, Schema: schema, Strict: true}}
}

type Usage struct {
	InputTokens        int `json:"input_tokens"`
	InputTokensDetails struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"input_tokens_details"`
	OutputTokens    int `json:"output_tokens"`
	ImagesProcessed int `json:"images_processed"`
}

type Response struct {
	ID     string `json:"id"`
	Model  string `json:"model"`
	Status string `json:"status"`
	Output []struct {
		Type    string `json:"type"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"output"`
	Usage Usage `json:"usage"`
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`

	Latency time.Duration `json:"-"`
}

// OutputText concatenates every output_text part of the response.
func (r *Response) OutputText() string {
	var sb strings.Builder
	for _, o := range r.Output {
		for _, c := range o.Content {
			if c.Type == "output_text" {
				sb.WriteString(c.Text)
			}
		}
	}
	return sb.String()
}

// Create sends a non-streaming Responses request.
func (c *Client) Create(ctx context.Context, req *Request) (*Response, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/responses", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	hreq.Header.Set("Authorization", "Bearer "+c.APIKey)
	hreq.Header.Set("Content-Type", "application/json")

	start := time.Now()
	hres, err := c.HTTP.Do(hreq)
	if err != nil {
		return nil, err
	}
	defer hres.Body.Close()
	raw, err := io.ReadAll(hres.Body)
	if err != nil {
		return nil, err
	}
	if hres.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("meta %s: HTTP %d: %s", req.Model, hres.StatusCode, truncate(raw, 500))
	}
	var res Response
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, fmt.Errorf("meta %s: decode: %w", req.Model, err)
	}
	if res.Error != nil {
		return nil, fmt.Errorf("meta %s: %s: %s", req.Model, res.Error.Code, res.Error.Message)
	}
	res.Latency = time.Since(start)
	return &res, nil
}

func truncate(b []byte, n int) string {
	if len(b) > n {
		return string(b[:n]) + "…"
	}
	return string(b)
}
