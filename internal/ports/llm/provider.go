// Package llm defines the LLM provider port for JARV.
//
// This is the central abstraction that decouples the core intelligence engine
// from any specific AI provider (OpenAI, Anthropic, Ollama, etc.).
// All LLM communication flows through this interface — never directly.
//
// Design principles:
//   - Streaming-first: all responses support token-by-token streaming
//   - Provider-agnostic: the core never knows which provider is active
//   - Fallback-aware: the engine can chain multiple providers transparently
//   - Cost-tracked: every call reports token usage for budget management
//
// Copyright (c) 2026 JARV contributors. All rights reserved.
// Proprietary and confidential.
package llm

import (
	"context"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// Core Types
// ─────────────────────────────────────────────────────────────────────────────

// Role identifies the author of a message in a conversation.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Message represents a single turn in a conversation.
type Message struct {
	Role       Role        `json:"role"`
	Content    string      `json:"content"`
	ToolCallID string      `json:"tool_call_id,omitempty"` // for RoleTool
	ToolCalls  []ToolCall  `json:"tool_calls,omitempty"`   // for RoleAssistant
}

// ToolCall represents a function call requested by the model.
type ToolCall struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Args     string `json:"arguments"` // JSON string
}

// ToolDefinition describes a function the model can invoke.
type ToolDefinition struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Parameters  any    `json:"parameters"` // JSON Schema object
}

// Request encapsulates everything needed for a single LLM call.
type Request struct {
	Model       string           // model identifier (e.g., "gpt-4o", "claude-3-5-sonnet")
	Messages    []Message        // conversation history
	Tools       []ToolDefinition // available tools (nil = no tools)
	MaxTokens   int              // 0 = provider default
	Temperature float64          // 0.0 = deterministic, 1.0 = creative
	Stream      bool             // whether to stream the response
	Metadata    map[string]string // arbitrary metadata for logging
}

// Response holds the result of a successful LLM call.
type Response struct {
	Content   string     // text content of the response
	ToolCalls []ToolCall // tool calls requested by the model (may be empty)
	Usage     TokenUsage // token consumption for cost tracking
	Model     string     // actual model used (may differ from requested)
	Latency   time.Duration
}

// TokenUsage tracks token consumption for a single request.
type TokenUsage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	EstimatedCostUSD float64 // approximate cost in USD
}

// StreamChunk is a single token or partial response from a streaming call.
type StreamChunk struct {
	Delta     string     // incremental text content
	ToolCalls []ToolCall // incremental tool call data
	Done      bool       // true on the final chunk
	Usage     *TokenUsage // populated only on the final chunk
}

// ─────────────────────────────────────────────────────────────────────────────
// Provider Interface — The Core Port
// ─────────────────────────────────────────────────────────────────────────────

// Provider is the primary interface for all LLM communication in JARV.
// Every AI provider (OpenAI, Anthropic, Ollama, etc.) must implement this.
type Provider interface {
	// Complete sends a request and returns the full response.
	Complete(ctx context.Context, req Request) (*Response, error)

	// Stream sends a request and delivers tokens via the returned channel.
	// The channel is closed when streaming is complete or on error.
	Stream(ctx context.Context, req Request) (<-chan StreamChunk, error)

	// Models returns the list of available model identifiers.
	Models(ctx context.Context) ([]string, error)

	// Name returns the provider's human-readable name (e.g., "openai", "anthropic").
	Name() string

	// Healthy checks if the provider is reachable and operational.
	Healthy(ctx context.Context) bool
}

// ─────────────────────────────────────────────────────────────────────────────
// Router Interface — Smart Model Selection
// ─────────────────────────────────────────────────────────────────────────────

// Tier classifies a model by capability and cost.
type Tier string

const (
	TierNano    Tier = "nano"    // cheapest, fastest (chitchat, FAQ)
	TierMini    Tier = "mini"    // balanced (summaries, drafts)
	TierStandard Tier = "standard" // capable (analysis, code)
	TierPremium Tier = "premium" // most capable (complex reasoning, Oracle)
)

// IntentSignal carries the signals extracted from a user message for routing.
type IntentSignal struct {
	TokenEstimate     int
	HasCodeBlocks     bool
	HasAttachments    bool
	RecentToolCalls   int
	ConversationDepth int
	ExplicitIntent    string // "oracle", "security", "creative", etc.
}

// Router selects the appropriate model tier for a given request.
// This is the heart of JARV's cost optimization strategy.
type Router interface {
	// Route analyzes the signal and returns the recommended tier.
	Route(signal IntentSignal) Tier

	// Score returns the raw complexity score (0.0–1.0) for a signal.
	Score(signal IntentSignal) float64
}

// ─────────────────────────────────────────────────────────────────────────────
// FallbackChain — Resilient Multi-Provider Execution
// ─────────────────────────────────────────────────────────────────────────────

// Candidate is a provider+model pair in a fallback chain.
type Candidate struct {
	Provider Provider
	Model    string
	Tier     Tier
	Priority int // lower = higher priority
}

// FallbackChain tries candidates in order until one succeeds.
// This enables zero-downtime provider switching and rate limit handling.
type FallbackChain interface {
	// Execute tries each candidate in order, returning the first success.
	Execute(ctx context.Context, req Request) (*Response, error)

	// Add registers a new candidate in the chain.
	Add(c Candidate)

	// SetTier filters the chain to only use candidates of the given tier.
	SetTier(tier Tier) FallbackChain
}
