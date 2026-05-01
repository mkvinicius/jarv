// Package llm provides LLM provider implementations for JARV.
//
// This file implements the OpenAI provider using the official OpenAI Go SDK.
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

// OpenAIProvider implements the Provider interface for OpenAI's API.
type OpenAIProvider struct {
	apiKey   string
	baseURL  string
	client   *http.Client
	model    string
}

// NewOpenAIProvider creates a new OpenAI provider.
func NewOpenAIProvider(apiKey, baseURL string) (*OpenAIProvider, error) {
	if apiKey == "" {
		apiKey = "dummy" // For testing without API key
	}
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}

	return &OpenAIProvider{
		apiKey:  apiKey,
		baseURL: strings.TrimSuffix(baseURL, "/"),
		client: &http.Client{
			Timeout: 60 * time.Second,
		},
	}, nil
}

// Complete sends a request and returns the full response.
func (p *OpenAIProvider) Complete(ctx context.Context, req Request) (*Response, error) {
	start := time.Now()

	// Build messages array
	messages := make([]map[string]interface{}, len(req.Messages))
	for i, msg := range req.Messages {
		m := map[string]interface{}{
			"role":    string(msg.Role),
			"content": msg.Content,
		}
		messages[i] = m
	}

	// Build request body
	body := map[string]interface{}{
		"model": req.Model,
		"messages": messages,
	}

	if req.MaxTokens > 0 {
		body["max_tokens"] = req.MaxTokens
	}
	if req.Temperature > 0 {
		body["temperature"] = req.Temperature
	}

	// Add tools if present
	if len(req.Tools) > 0 {
		tools := make([]map[string]interface{}, len(req.Tools))
		for i, tool := range req.Tools {
			tools[i] = map[string]interface{}{
				"type": "function",
				"function": map[string]interface{}{
					"name":        tool.Name,
					"description": tool.Description,
					"parameters":  tool.Parameters,
				},
			}
		}
		body["tools"] = tools
	}

	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Make request
	httpReq, err := http.NewRequestWithContext(ctx, "POST", p.baseURL+"/chat/completions", bytes.NewReader(bodyJSON))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)

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
		ID      string `json:"id"`
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Role      string `json:"role"`
				Content   string `json:"content"`
				ToolCalls []struct {
					ID       string `json:"id"`
					Type     string `json:"type"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
	}

	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if len(result.Choices) == 0 {
		return nil, fmt.Errorf("no choices in response")
	}

	choice := result.Choices[0]
	response := &Response{
		Content: choice.Message.Content,
		Model:   result.Model,
		Latency: time.Since(start),
		Usage: TokenUsage{
			PromptTokens:     result.Usage.PromptTokens,
			CompletionTokens: result.Usage.CompletionTokens,
			TotalTokens:      result.Usage.TotalTokens,
		},
	}

	// Convert tool calls
	for _, tc := range choice.Message.ToolCalls {
		response.ToolCalls = append(response.ToolCalls, ToolCall{
			ID:   tc.ID,
			Name: tc.Function.Name,
			Args: tc.Function.Arguments,
		})
	}

	return response, nil
}

// Stream sends a request and delivers tokens via the returned channel.
func (p *OpenAIProvider) Stream(ctx context.Context, req Request) (<-chan StreamChunk, error) {
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
			body["max_tokens"] = req.MaxTokens
		}
		if req.Temperature > 0 {
			body["temperature"] = req.Temperature
		}

		bodyJSON, err := json.Marshal(body)
		if err != nil {
			ch <- StreamChunk{Delta: fmt.Sprintf("error: %v", err)}
			return
		}

		httpReq, err := http.NewRequestWithContext(ctx, "POST", p.baseURL+"/chat/completions", bytes.NewReader(bodyJSON))
		if err != nil {
			ch <- StreamChunk{Delta: fmt.Sprintf("error: %v", err)}
			return
		}
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)

		resp, err := p.client.Do(httpReq)
		if err != nil {
			ch <- StreamChunk{Delta: fmt.Sprintf("error: %v", err)}
			return
		}
		defer resp.Body.Close()

		reader := bufio.NewScanner(resp.Body)
		for reader.Scan() {
			line := reader.Text()

			// SSE format: data: {...}
			if !strings.HasPrefix(string(line), "data: ") {
				continue
			}

			data := strings.TrimPrefix(string(line), "data: ")
			if data == "[DONE]" {
				ch <- StreamChunk{Done: true}
				return
			}

			var chunk struct {
				Choices []struct {
					Delta struct {
						Content string `json:"content"`
					} `json:"delta"`
				} `json:"choices"`
			}

			if err := json.Unmarshal([]byte(data), &chunk); err != nil {
				continue
			}

			if len(chunk.Choices) > 0 {
				streamChunk := StreamChunk{Delta: chunk.Choices[0].Delta.Content}
				ch <- streamChunk
			}
		}
	}()

	return ch, nil
}

// Models returns the list of available model identifiers.
func (p *OpenAIProvider) Models(ctx context.Context) ([]string, error) {
	return []string{
		"gpt-4o",
		"gpt-4o-mini",
		"gpt-4-turbo",
		"gpt-3.5-turbo",
	}, nil
}

// Name returns the provider's human-readable name.
func (p *OpenAIProvider) Name() string {
	return "openai"
}

// Healthy checks if the provider is reachable and operational.
func (p *OpenAIProvider) Healthy(ctx context.Context) bool {
	req, err := http.NewRequestWithContext(ctx, "GET", p.baseURL+"/models", nil)
	if err != nil {
		return false
	}
	req.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	return resp.StatusCode == http.StatusOK
}
