// Package llm provides LLM provider implementations for JARV.
//
// This file implements the Anthropic provider using the official Anthropic SDK.
//
// Copyright (c) 2026 JARV contributors. All rights reserved.
package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// AnthropicProvider implements the Provider interface for Anthropic's API.
type AnthropicProvider struct {
	apiKey  string
	baseURL string
	client  *http.Client
}

// NewAnthropicProvider creates a new Anthropic provider.
func NewAnthropicProvider(apiKey, baseURL string) (*AnthropicProvider, error) {
	if apiKey == "" {
		apiKey = "dummy" // For testing
	}
	if baseURL == "" {
		baseURL = "https://api.anthropic.com/v1"
	}

	return &AnthropicProvider{
		apiKey:  apiKey,
		baseURL: strings.TrimSuffix(baseURL, "/"),
		client: &http.Client{
			Timeout: 300 * time.Second,
		},
	}, nil
}

// Complete sends a request and returns the full response.
func (p *AnthropicProvider) Complete(ctx context.Context, req Request) (*Response, error) {
	start := time.Now()

	// Build messages array (Anthropic format)
	messages := make([]map[string]interface{}, len(req.Messages))
	for i, msg := range req.Messages {
		messages[i] = map[string]interface{}{
			"role":    string(msg.Role),
			"content": msg.Content,
		}
	}

	// Build request body
	body := map[string]interface{}{
		"model":      req.Model,
		"messages":   messages,
		"max_tokens": req.MaxTokens,
	}

	if req.Temperature > 0 {
		body["temperature"] = req.Temperature
	}

	// Anthropic uses system message separately
	var systemPrompt string
	if len(req.Messages) > 0 && req.Messages[0].Role == RoleSystem {
		systemPrompt = req.Messages[0].Content
		body["messages"] = req.Messages[1:]
	}
	if systemPrompt != "" {
		body["system"] = systemPrompt
	}

	// Add tools if present
	if len(req.Tools) > 0 {
		tools := make([]map[string]interface{}, len(req.Tools))
		for i, tool := range req.Tools {
			tools[i] = map[string]interface{}{
				"name":        tool.Name,
				"description": tool.Description,
				"input_schema": tool.Parameters,
			}
		}
		body["tools"] = tools
	}

	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Make request
	httpReq, err := http.NewRequestWithContext(ctx, "POST", p.baseURL+"/messages", bytes.NewReader(bodyJSON))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", p.apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")

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
		ID       string `json:"id"`
		Type     string `json:"type"`
		Role     string `json:"role"`
		Content  []struct {
			Type        string `json:"type"`
			Text        string `json:"text"`
			ToolUse     *struct {
				ID       string `json:"id"`
				Name     string `json:"name"`
				Input    string `json:"input"`
			} `json:"tool_use,omitempty"`
		} `json:"content"`
		Model       string `json:"model"`
		StopReason string `json:"stop_reason"`
		Usage       struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}

	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	response := &Response{
		Model:   result.Model,
		Latency: time.Since(start),
		Usage: TokenUsage{
			PromptTokens:     result.Usage.InputTokens,
			CompletionTokens: result.Usage.OutputTokens,
			TotalTokens:      result.Usage.InputTokens + result.Usage.OutputTokens,
		},
	}

	// Parse content blocks
	for _, block := range result.Content {
		if block.Type == "text" {
			response.Content += block.Text
		} else if block.Type == "tool_use" && block.ToolUse != nil {
			response.ToolCalls = append(response.ToolCalls, ToolCall{
				ID:   block.ToolUse.ID,
				Name: block.ToolUse.Name,
				Args: block.ToolUse.Input,
			})
		}
	}

	return response, nil
}

// Stream sends a request and delivers tokens via the returned channel.
func (p *AnthropicProvider) Stream(ctx context.Context, req Request) (<-chan StreamChunk, error) {
	ch := make(chan StreamChunk, 100)
	go func() {
		defer close(ch)

		// Similar to Complete but with streaming
		var systemPrompt string
		messages := req.Messages
		if len(req.Messages) > 0 && req.Messages[0].Role == RoleSystem {
			systemPrompt = req.Messages[0].Content
			messages = req.Messages[1:]
		}

		body := map[string]interface{}{
			"model":      req.Model,
			"messages":   messages,
			"max_tokens": req.MaxTokens,
			"stream":     true,
		}
		if systemPrompt != "" {
			body["system"] = systemPrompt
		}
		if req.Temperature > 0 {
			body["temperature"] = req.Temperature
		}

		bodyJSON, err := json.Marshal(body)
		if err != nil {
			ch <- StreamChunk{Delta: fmt.Sprintf("error: %v", err)}
			return
		}

		httpReq, err := http.NewRequestWithContext(ctx, "POST", p.baseURL+"/messages", bytes.NewReader(bodyJSON))
		if err != nil {
			ch <- StreamChunk{Delta: fmt.Sprintf("error: %v", err)}
			return
		}
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("x-api-key", p.apiKey)
		httpReq.Header.Set("anthropic-version", "2023-06-01")

		resp, err := p.client.Do(httpReq)
		if err != nil {
			ch <- StreamChunk{Delta: fmt.Sprintf("error: %v", err)}
			return
		}
		defer resp.Body.Close()

		reader := bufio.NewScanner(resp.Body)
		for reader.Scan() {
			line := reader.Text()

			if !strings.HasPrefix(string(line), "data: ") {
				continue
			}

			data := strings.TrimPrefix(string(line), "data: ")
			if data == "[DONE]" {
				ch <- StreamChunk{Done: true}
				return
			}

			var chunk struct {
				Type   string `json:"type"`
				Index  int    `json:"index"`
				Delta  struct {
					Type         string `json:"type"`
					Text         string `json:"text"`
					PartialJSON  string `json:"partial_json"`
					ToolID       string `json:"tool_id"`
					ToolName     string `json:"tool_name"`
					ToolInput    string `json:"tool_input"`
				} `json:"delta"`
			}

			if err := json.Unmarshal([]byte(data), &chunk); err != nil {
				continue
			}

			streamChunk := StreamChunk{}
			if chunk.Delta.Type == "text_delta" {
				streamChunk.Delta = chunk.Delta.Text
			}
			ch <- streamChunk
		}
	}()

	return ch, nil
}

// Models returns the list of available model identifiers.
func (p *AnthropicProvider) Models(ctx context.Context) ([]string, error) {
	return []string{
		"claude-sonnet-4-20250514",
		"claude-3-5-sonnet-latest",
		"claude-3-opus-latest",
		"claude-3-haiku-latest",
	}, nil
}

// Name returns the provider's human-readable name.
func (p *AnthropicProvider) Name() string {
	return "anthropic"
}

// Healthy checks if the provider is reachable and operational.
func (p *AnthropicProvider) Healthy(ctx context.Context) bool {
	// Simple health check via credits endpoint
	req, err := http.NewRequestWithContext(ctx, "GET", p.baseURL+"/credits", nil)
	if err != nil {
		return false
	}
	req.Header.Set("x-api-key", p.apiKey)

	resp, err := p.client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	return resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNotFound
}
