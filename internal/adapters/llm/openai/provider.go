// Package openai implements the llm.Provider interface for the OpenAI API.
// Uses only stdlib net/http — no SDK dependency.
//
// Copyright (c) 2026 JARV contributors. All rights reserved.
// Proprietary and confidential.
package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/mkvinicius/jarv/internal/ports/llm"
)

const defaultBaseURL = "https://api.openai.com/v1"

// Provider implements llm.Provider for the OpenAI chat completions API.
type Provider struct {
	apiKey  string
	baseURL string
	model   string
	client  *http.Client
}

// New creates an OpenAI provider.
// model: e.g. "gpt-4o", "gpt-4o-mini", "gpt-3.5-turbo"
func New(apiKey, model string) *Provider {
	return &Provider{
		apiKey:  apiKey,
		baseURL: defaultBaseURL,
		model:   model,
		client:  &http.Client{Timeout: 120 * time.Second},
	}
}

// NewWithBaseURL creates a provider pointing at a custom URL (e.g. Azure, proxy).
func NewWithBaseURL(apiKey, model, baseURL string) *Provider {
	p := New(apiKey, model)
	p.baseURL = baseURL
	return p
}

func (p *Provider) Name() string { return "openai" }

func (p *Provider) Healthy(ctx context.Context) bool {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+"/models", nil)
	req.Header.Set("Authorization", "Bearer "+p.apiKey)
	resp, err := p.client.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func (p *Provider) Models(ctx context.Context) ([]string, error) {
	type modelsResp struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+"/models", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+p.apiKey)
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var mr modelsResp
	if err := json.NewDecoder(resp.Body).Decode(&mr); err != nil {
		return nil, err
	}
	out := make([]string, len(mr.Data))
	for i, m := range mr.Data {
		out[i] = m.ID
	}
	return out, nil
}

func (p *Provider) Complete(ctx context.Context, req llm.Request) (*llm.Response, error) {
	model := req.Model
	if model == "" {
		model = p.model
	}

	body := p.buildRequest(model, req, false)
	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/chat/completions", bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")

	start := time.Now()
	httpResp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("openai: http: %w", err)
	}
	defer httpResp.Body.Close()

	body2, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, err
	}
	if httpResp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("openai: status %d: %s", httpResp.StatusCode, body2)
	}

	var resp oaResponse
	if err := json.Unmarshal(body2, &resp); err != nil {
		return nil, fmt.Errorf("openai: decode: %w", err)
	}
	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("openai: empty choices")
	}

	msg := resp.Choices[0].Message
	out := &llm.Response{
		Content: msg.Content,
		Model:   resp.Model,
		Latency: time.Since(start),
		Usage: llm.TokenUsage{
			PromptTokens:     resp.Usage.PromptTokens,
			CompletionTokens: resp.Usage.CompletionTokens,
			TotalTokens:      resp.Usage.TotalTokens,
			EstimatedCostUSD: estimateCost(model, resp.Usage.PromptTokens, resp.Usage.CompletionTokens),
		},
	}

	for _, tc := range msg.ToolCalls {
		out.ToolCalls = append(out.ToolCalls, llm.ToolCall{
			ID:   tc.ID,
			Name: tc.Function.Name,
			Args: tc.Function.Arguments,
		})
	}

	return out, nil
}

func (p *Provider) Stream(ctx context.Context, req llm.Request) (<-chan llm.StreamChunk, error) {
	ch := make(chan llm.StreamChunk, 32)
	go func() {
		defer close(ch)
		resp, err := p.Complete(ctx, req)
		if err != nil {
			ch <- llm.StreamChunk{Delta: "", Done: true}
			return
		}
		ch <- llm.StreamChunk{Delta: resp.Content, Done: false}
		ch <- llm.StreamChunk{Done: true, Usage: &resp.Usage}
	}()
	return ch, nil
}

// ─── JSON types ──────────────────────────────────────────────────────────────

type oaRequest struct {
	Model       string       `json:"model"`
	Messages    []oaMessage  `json:"messages"`
	Tools       []oaTool     `json:"tools,omitempty"`
	MaxTokens   int          `json:"max_tokens,omitempty"`
	Temperature float64      `json:"temperature,omitempty"`
	Stream      bool         `json:"stream,omitempty"`
}

type oaMessage struct {
	Role       string        `json:"role"`
	Content    string        `json:"content,omitempty"`
	ToolCallID string        `json:"tool_call_id,omitempty"`
	ToolCalls  []oaToolCall  `json:"tool_calls,omitempty"`
}

type oaTool struct {
	Type     string         `json:"type"`
	Function oaToolFunction `json:"function"`
}

type oaToolFunction struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Parameters  any    `json:"parameters"`
}

type oaToolCall struct {
	ID       string          `json:"id"`
	Type     string          `json:"type"`
	Function oaToolCallFn    `json:"function"`
}

type oaToolCallFn struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type oaResponse struct {
	Choices []oaChoice `json:"choices"`
	Usage   oaUsage    `json:"usage"`
	Model   string     `json:"model"`
}

type oaChoice struct {
	Message oaMessage `json:"message"`
}

type oaUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

func (p *Provider) buildRequest(model string, req llm.Request, stream bool) oaRequest {
	msgs := make([]oaMessage, 0, len(req.Messages))
	for _, m := range req.Messages {
		om := oaMessage{
			Role:       string(m.Role),
			Content:    m.Content,
			ToolCallID: m.ToolCallID,
		}
		for _, tc := range m.ToolCalls {
			om.ToolCalls = append(om.ToolCalls, oaToolCall{
				ID:   tc.ID,
				Type: "function",
				Function: oaToolCallFn{
					Name:      tc.Name,
					Arguments: tc.Args,
				},
			})
		}
		msgs = append(msgs, om)
	}

	var tools []oaTool
	for _, td := range req.Tools {
		tools = append(tools, oaTool{
			Type: "function",
			Function: oaToolFunction{
				Name:        td.Name,
				Description: td.Description,
				Parameters:  td.Parameters,
			},
		})
	}

	maxTok := req.MaxTokens
	if maxTok == 0 {
		maxTok = 4096
	}

	return oaRequest{
		Model:       model,
		Messages:    msgs,
		Tools:       tools,
		MaxTokens:   maxTok,
		Temperature: req.Temperature,
		Stream:      stream,
	}
}

// estimateCost returns a rough USD estimate based on known pricing tiers.
func estimateCost(model string, promptTok, completionTok int) float64 {
	var promptPrice, completionPrice float64
	switch {
	case contains(model, "gpt-4o-mini"):
		promptPrice, completionPrice = 0.00015, 0.00060
	case contains(model, "gpt-4o"):
		promptPrice, completionPrice = 0.0025, 0.0100
	case contains(model, "gpt-4-turbo"):
		promptPrice, completionPrice = 0.010, 0.030
	default:
		promptPrice, completionPrice = 0.0010, 0.0020
	}
	return float64(promptTok)/1000*promptPrice + float64(completionTok)/1000*completionPrice
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub ||
		len(s) > 0 && containsStr(s, sub))
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
