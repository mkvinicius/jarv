// Package anthropic implements the llm.Provider interface for the Anthropic API.
// Uses only stdlib net/http — no SDK dependency.
//
// Copyright (c) 2026 JARV contributors. All rights reserved.
// Proprietary and confidential.
package anthropic

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

const (
	baseURL        = "https://api.anthropic.com/v1"
	anthropicVersion = "2023-06-01"
	defaultMaxTokens = 4096
)

// Provider implements llm.Provider for the Anthropic Messages API.
type Provider struct {
	apiKey string
	model  string
	client *http.Client
}

// New creates an Anthropic provider.
// model: e.g. "claude-sonnet-4-6", "claude-haiku-4-5-20251001"
func New(apiKey, model string) *Provider {
	return &Provider{
		apiKey: apiKey,
		model:  model,
		client: &http.Client{Timeout: 120 * time.Second},
	}
}

func (p *Provider) Name() string { return "anthropic" }

func (p *Provider) Healthy(ctx context.Context) bool {
	// Anthropic has no lightweight health endpoint; try a minimal message
	req := llm.Request{
		Messages:  []llm.Message{{Role: llm.RoleUser, Content: "ping"}},
		MaxTokens: 5,
	}
	_, err := p.Complete(ctx, req)
	return err == nil
}

func (p *Provider) Models(_ context.Context) ([]string, error) {
	return []string{
		"claude-opus-4-6",
		"claude-sonnet-4-6",
		"claude-haiku-4-5-20251001",
	}, nil
}

func (p *Provider) Complete(ctx context.Context, req llm.Request) (*llm.Response, error) {
	model := req.Model
	if model == "" {
		model = p.model
	}

	body := p.buildRequest(model, req)
	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/messages", bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("x-api-key", p.apiKey)
	httpReq.Header.Set("anthropic-version", anthropicVersion)
	httpReq.Header.Set("Content-Type", "application/json")

	start := time.Now()
	httpResp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("anthropic: http: %w", err)
	}
	defer httpResp.Body.Close()

	body2, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, err
	}
	if httpResp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("anthropic: status %d: %s", httpResp.StatusCode, body2)
	}

	var resp anResponse
	if err := json.Unmarshal(body2, &resp); err != nil {
		return nil, fmt.Errorf("anthropic: decode: %w", err)
	}

	out := &llm.Response{
		Model:   resp.Model,
		Latency: time.Since(start),
		Usage: llm.TokenUsage{
			PromptTokens:     resp.Usage.InputTokens,
			CompletionTokens: resp.Usage.OutputTokens,
			TotalTokens:      resp.Usage.InputTokens + resp.Usage.OutputTokens,
			EstimatedCostUSD: estimateCost(model, resp.Usage.InputTokens, resp.Usage.OutputTokens),
		},
	}

	for _, c := range resp.Content {
		switch c.Type {
		case "text":
			out.Content += c.Text
		case "tool_use":
			argsJSON, _ := json.Marshal(c.Input)
			out.ToolCalls = append(out.ToolCalls, llm.ToolCall{
				ID:   c.ID,
				Name: c.Name,
				Args: string(argsJSON),
			})
		}
	}

	return out, nil
}

func (p *Provider) Stream(ctx context.Context, req llm.Request) (<-chan llm.StreamChunk, error) {
	ch := make(chan llm.StreamChunk, 32)
	go func() {
		defer close(ch)
		resp, err := p.Complete(ctx, req)
		if err != nil {
			ch <- llm.StreamChunk{Done: true}
			return
		}
		ch <- llm.StreamChunk{Delta: resp.Content}
		ch <- llm.StreamChunk{Done: true, Usage: &resp.Usage}
	}()
	return ch, nil
}

// ─── JSON types ──────────────────────────────────────────────────────────────

type anRequest struct {
	Model     string      `json:"model"`
	MaxTokens int         `json:"max_tokens"`
	System    string      `json:"system,omitempty"`
	Messages  []anMessage `json:"messages"`
	Tools     []anTool    `json:"tools,omitempty"`
}

type anMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type anTool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	InputSchema any    `json:"input_schema"`
}

type anResponse struct {
	Content []anContent `json:"content"`
	Usage   anUsage     `json:"usage"`
	Model   string      `json:"model"`
}

type anContent struct {
	Type  string `json:"type"`
	Text  string `json:"text,omitempty"`
	ID    string `json:"id,omitempty"`
	Name  string `json:"name,omitempty"`
	Input any    `json:"input,omitempty"`
}

type anUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

func (p *Provider) buildRequest(model string, req llm.Request) anRequest {
	ar := anRequest{
		Model:     model,
		MaxTokens: req.MaxTokens,
	}
	if ar.MaxTokens == 0 {
		ar.MaxTokens = defaultMaxTokens
	}

	for _, m := range req.Messages {
		if m.Role == llm.RoleSystem {
			ar.System = m.Content
			continue
		}
		ar.Messages = append(ar.Messages, anMessage{
			Role:    string(m.Role),
			Content: m.Content,
		})
	}

	for _, td := range req.Tools {
		ar.Tools = append(ar.Tools, anTool{
			Name:        td.Name,
			Description: td.Description,
			InputSchema: td.Parameters,
		})
	}

	return ar
}

func estimateCost(model string, inputTok, outputTok int) float64 {
	var inPrice, outPrice float64
	switch {
	case containsStr(model, "opus"):
		inPrice, outPrice = 0.015, 0.075
	case containsStr(model, "sonnet"):
		inPrice, outPrice = 0.003, 0.015
	default: // haiku
		inPrice, outPrice = 0.00025, 0.00125
	}
	return float64(inputTok)/1000*inPrice + float64(outputTok)/1000*outPrice
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
