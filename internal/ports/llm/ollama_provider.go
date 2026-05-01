// Package llm provides LLM provider implementations for JARV.
//
// This file implements the Ollama provider using the local Ollama API.
//
// Copyright (c) 2026 JARV contributors. All rights reserved.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// OllamaProvider implements the Provider interface for local Ollama.
type OllamaProvider struct {
	baseURL string
	client  *http.Client
}

// NewOllamaProvider creates a new Ollama provider.
func NewOllamaProvider(baseURL string) (*OllamaProvider, error) {
	if baseURL == "" {
		baseURL = "http://localhost:11434"
	}

	return &OllamaProvider{
		baseURL: strings.TrimSuffix(baseURL, "/"),
		client: &http.Client{
			Timeout: 120 * time.Second, // Ollama can be slow
		},
	}, nil
}

// Complete sends a request and returns the full response.
func (p *OllamaProvider) Complete(ctx context.Context, req Request) (*Response, error) {
	start := time.Now()

	// Build messages array
	messages := make([]map[string]interface{}, len(req.Messages))
	for i, msg := range req.Messages {
		messages[i] = map[string]interface{}{
			"role":    string(msg.Role),
			"content": msg.Content,
		}
	}

	// Build request body
	body := map[string]interface{}{
		"model":    req.Model,
		"messages": messages,
		"stream":   false,
	}

	if req.MaxTokens > 0 {
		body["options"] = map[string]interface{}{
			"num_predict": req.MaxTokens,
		}
	}
	if req.Temperature > 0 {
		if body["options"] == nil {
			body["options"] = map[string]interface{}{}
		}
		body["options"].(map[string]interface{})["temperature"] = req.Temperature
	}

	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Make request
	httpReq, err := http.NewRequestWithContext(ctx, "POST", p.baseURL+"/api/chat", bytes.NewReader(bodyJSON))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, string(respBody))
	}

	// Parse response
	var result struct {
		Model     string `json:"model"`
		Message   struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"message"`
		Done           bool   `json:"done"`
		TotalDuration  int64  `json:"total_duration,omitempty"`
		PromptEvalCount int   `json:"prompt_eval_count,omitempty"`
		EvalCount      int    `json:"eval_count,omitempty"`
	}

	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	response := &Response{
		Content: result.Message.Content,
		Model:   result.Model,
		Latency: time.Since(start),
		Usage: TokenUsage{
			PromptTokens:     result.PromptEvalCount,
			CompletionTokens: result.EvalCount,
			TotalTokens:      result.PromptEvalCount + result.EvalCount,
		},
	}

	return response, nil
}

// Stream sends a request and delivers tokens via the returned channel.
func (p *OllamaProvider) Stream(ctx context.Context, req Request) (<-chan StreamChunk, error) {
	ch := make(chan StreamChunk, 100)
	go func() {
		defer close(ch)

		// Build messages
		messages := make([]map[string]interface{}, len(req.Messages))
		for i, msg := range req.Messages {
			messages[i] = map[string]interface{}{
				"role":    string(msg.Role),
				"content": msg.Content,
			}
		}

		body := map[string]interface{}{
			"model":    req.Model,
			"messages": messages,
			"stream":   true,
		}

		if req.MaxTokens > 0 {
			body["options"] = map[string]interface{}{
				"num_predict": req.MaxTokens,
			}
		}
		if req.Temperature > 0 {
			if body["options"] == nil {
				body["options"] = map[string]interface{}{}
			}
			body["options"].(map[string]interface{})["temperature"] = req.Temperature
		}

		bodyJSON, err := json.Marshal(body)
		if err != nil {
			ch <- StreamChunk{Delta: fmt.Sprintf("error: %v", err)}
			return
		}

		httpReq, err := http.NewRequestWithContext(ctx, "POST", p.baseURL+"/api/chat", bytes.NewReader(bodyJSON))
		if err != nil {
			ch <- StreamChunk{Delta: fmt.Sprintf("error: %v", err)}
			return
		}
		httpReq.Header.Set("Content-Type", "application/json")

		resp, err := p.client.Do(httpReq)
		if err != nil {
			ch <- StreamChunk{Delta: fmt.Sprintf("error: %v", err)}
			return
		}
		defer resp.Body.Close()

		reader := resp.Body
		decoder := json.NewDecoder(reader)

		for {
			var chunk struct {
				Message struct {
					Role    string `json:"role"`
					Content string `json:"content"`
				} `json:"message"`
				Done bool `json:"done"`
			}

			if err := decoder.Decode(&chunk); err != nil {
				break
			}

			streamChunk := StreamChunk{Delta: chunk.Message.Content}
			if chunk.Done {
				streamChunk.Done = true
			}
			ch <- streamChunk

			if chunk.Done {
				return
			}
		}
	}()

	return ch, nil
}

// Models returns the list of available model identifiers from Ollama.
func (p *OllamaProvider) Models(ctx context.Context) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", p.baseURL+"/api/tags", nil)
	if err != nil {
		return nil, err
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Ollama API error: %d", resp.StatusCode)
	}

	var result struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}

	models := make([]string, len(result.Models))
	for i, m := range result.Models {
		models[i] = m.Name
	}

	return models, nil
}

// Name returns the provider's human-readable name.
func (p *OllamaProvider) Name() string {
	return "ollama"
}

// Healthy checks if the provider is reachable and operational.
func (p *OllamaProvider) Healthy(ctx context.Context) bool {
	req, err := http.NewRequestWithContext(ctx, "GET", p.baseURL+"/api/tags", nil)
	if err != nil {
		return false
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	return resp.StatusCode == http.StatusOK
}
