// Package ollama implements the llm.Provider interface for local Ollama models.
// Uses only stdlib net/http — no SDK dependency.
//
// Ollama runs locally at http://localhost:11434 by default.
// Install: https://ollama.com
//
// Copyright (c) 2026 JARV contributors. All rights reserved.
// Proprietary and confidential.
package ollama

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/mkvinicius/jarv/internal/ports/llm"
)

const defaultOllamaURL = "http://localhost:11434"

// Provider implements llm.Provider for local Ollama models.
type Provider struct {
	baseURL string
	model   string
	client  *http.Client
}

// New creates an Ollama provider using the default local address.
func New(model string) *Provider {
	return NewWithURL(defaultOllamaURL, model)
}

// NewWithURL creates an Ollama provider pointing at a custom URL.
func NewWithURL(baseURL, model string) *Provider {
	return &Provider{
		baseURL: baseURL,
		model:   model,
		client:  &http.Client{Timeout: 300 * time.Second}, // models can be slow
	}
}

func (p *Provider) Name() string { return "ollama" }

func (p *Provider) Healthy(ctx context.Context) bool {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+"/api/tags", nil)
	resp, err := p.client.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func (p *Provider) Models(ctx context.Context) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+"/api/tags", nil)
	if err != nil {
		return nil, err
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ollama: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	names := make([]string, len(result.Models))
	for i, m := range result.Models {
		names[i] = m.Name
	}
	return names, nil
}

func (p *Provider) Complete(ctx context.Context, req llm.Request) (*llm.Response, error) {
	model := req.Model
	if model == "" {
		model = p.model
	}

	msgs := convertMessages(req.Messages, req.Images)
	body := ollamaRequest{
		Model:    model,
		Messages: msgs,
		Stream:   false,
	}
	if req.Temperature > 0 {
		body.Options = map[string]any{"temperature": req.Temperature}
	}

	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/api/chat", bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	start := time.Now()
	httpResp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("ollama: http: %w", err)
	}
	defer httpResp.Body.Close()

	raw, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, err
	}
	if httpResp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama: status %d: %s", httpResp.StatusCode, raw)
	}

	var resp ollamaResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("ollama: decode: %w", err)
	}

	promptTok := resp.PromptEvalCount
	completionTok := resp.EvalCount
	return &llm.Response{
		Content: resp.Message.Content,
		Model:   model,
		Latency: time.Since(start),
		Usage: llm.TokenUsage{
			PromptTokens:     promptTok,
			CompletionTokens: completionTok,
			TotalTokens:      promptTok + completionTok,
			EstimatedCostUSD: 0, // local model — no cost
		},
	}, nil
}

func (p *Provider) Stream(ctx context.Context, req llm.Request) (<-chan llm.StreamChunk, error) {
	model := req.Model
	if model == "" {
		model = p.model
	}

	body := ollamaRequest{
		Model:    model,
		Messages: convertMessages(req.Messages, req.Images),
		Stream:   true,
	}
	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/api/chat", bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	httpResp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("ollama: stream: %w", err)
	}

	ch := make(chan llm.StreamChunk, 64)
	go func() {
		defer close(ch)
		defer httpResp.Body.Close()

		scanner := bufio.NewScanner(httpResp.Body)
		var totalPrompt, totalCompletion int
		for scanner.Scan() {
			var chunk ollamaResponse
			if err := json.Unmarshal(scanner.Bytes(), &chunk); err != nil {
				continue
			}
			if chunk.Done {
				usage := llm.TokenUsage{
					PromptTokens:     totalPrompt,
					CompletionTokens: totalCompletion,
					TotalTokens:      totalPrompt + totalCompletion,
				}
				ch <- llm.StreamChunk{Done: true, Usage: &usage}
				return
			}
			totalPrompt += chunk.PromptEvalCount
			totalCompletion += chunk.EvalCount
			if chunk.Message.Content != "" {
				ch <- llm.StreamChunk{Delta: chunk.Message.Content}
			}
		}
		ch <- llm.StreamChunk{Done: true}
	}()

	return ch, nil
}

// PullModel downloads a model from the Ollama registry.
// progress receives status lines; close ctx to cancel.
func PullModel(ctx context.Context, baseURL, model string, progress chan<- string) error {
	client := &http.Client{Timeout: 0} // no timeout for downloads
	body, _ := json.Marshal(map[string]string{"name": model})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/api/pull", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("ollama pull: %w", err)
	}
	defer resp.Body.Close()

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		var line struct {
			Status    string `json:"status"`
			Completed int64  `json:"completed"`
			Total     int64  `json:"total"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
			continue
		}
		if progress != nil {
			msg := line.Status
			if line.Total > 0 {
				pct := int(float64(line.Completed) / float64(line.Total) * 100)
				msg = fmt.Sprintf("%s %d%%", line.Status, pct)
			}
			select {
			case progress <- msg:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		if line.Status == "success" {
			return nil
		}
	}
	return scanner.Err()
}

// ─── JSON types ──────────────────────────────────────────────────────────────

type ollamaRequest struct {
	Model    string            `json:"model"`
	Messages []ollamaMessage   `json:"messages"`
	Stream   bool              `json:"stream"`
	Options  map[string]any    `json:"options,omitempty"`
}

type ollamaMessage struct {
	Role    string   `json:"role"`
	Content string   `json:"content"`
	Images  []string `json:"images,omitempty"` // base64-encoded images for vision models (llava, moondream)
}

type ollamaResponse struct {
	Message         ollamaMessage `json:"message"`
	PromptEvalCount int           `json:"prompt_eval_count"`
	EvalCount       int           `json:"eval_count"`
	Done            bool          `json:"done"`
}

func convertMessages(msgs []llm.Message, images []llm.ImageData) []ollamaMessage {
	out := make([]ollamaMessage, 0, len(msgs))
	// Find index of last user message for image attachment
	lastUserIdx := -1
	for i, m := range msgs {
		if m.Role == llm.RoleUser {
			lastUserIdx = i
		}
	}
	for i, m := range msgs {
		om := ollamaMessage{
			Role:    string(m.Role),
			Content: m.Content,
		}
		// Attach images to the last user message
		if m.Role == llm.RoleUser && len(images) > 0 && i == lastUserIdx {
			for _, img := range images {
				om.Images = append(om.Images, base64.StdEncoding.EncodeToString(img.Data))
			}
		}
		out = append(out, om)
	}
	return out
}
