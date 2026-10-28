package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Muse Spark Open, Meta's open-weight model, served by Baseten through an
// OpenAI-compatible endpoint.
const (
	BasetenURL   = "https://inference.baseten.co/v1/chat/completions"
	DefaultModel = "meta-models/muse-spark-open-1026"
	// Baseten does not list a price for this model; these are the rates
	// aggregators quote (USD per million tokens). Logged as an estimate.
	inputUSDPerM  = 1.25
	outputUSDPerM = 4.25
)

type Baseten struct {
	APIKey string
	Model  string
	HTTP   *http.Client
}

func NewBaseten(key string) *Baseten {
	return &Baseten{APIKey: key, Model: DefaultModel, HTTP: &http.Client{Timeout: 90 * time.Second}}
}

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

func (u Usage) CostUSD() float64 {
	return (float64(u.PromptTokens)*inputUSDPerM + float64(u.CompletionTokens)*outputUSDPerM) / 1e6
}

// Chat sends the conversation and returns the reply, which must follow schema.
// reasoning_effort defaults to xhigh on Baseten and can spend every token on
// thinking: it is always set, with room for the answer.
func (b *Baseten) Chat(ctx context.Context, msgs []Message, schemaName string, schema map[string]any) (string, Usage, error) {
	body, _ := json.Marshal(map[string]any{
		"model":            b.Model,
		"messages":         msgs,
		"max_tokens":       4000,
		"reasoning_effort": "low",
		"response_format": map[string]any{"type": "json_schema",
			"json_schema": map[string]any{"name": schemaName, "strict": true, "schema": schema}},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, BasetenURL, bytes.NewReader(body))
	if err != nil {
		return "", Usage{}, err
	}
	req.Header.Set("Authorization", "Api-Key "+b.APIKey)
	req.Header.Set("Content-Type", "application/json")
	res, err := b.HTTP.Do(req)
	if err != nil {
		return "", Usage{}, err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode != http.StatusOK {
		return "", Usage{}, fmt.Errorf("baseten: %s: %.300s", res.Status, raw)
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content *string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage Usage `json:"usage"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", Usage{}, fmt.Errorf("baseten: %w", err)
	}
	if len(out.Choices) == 0 || out.Choices[0].Message.Content == nil {
		return "", out.Usage, fmt.Errorf("baseten: empty answer (%d output tokens)", out.Usage.CompletionTokens)
	}
	return *out.Choices[0].Message.Content, out.Usage, nil
}
