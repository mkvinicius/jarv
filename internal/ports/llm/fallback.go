// Package llm provides LLM provider implementations for JARV.
//
// This file implements the FallbackChain for multi-provider resilience.
//
// Copyright (c) 2026 JARV contributors. All rights reserved.
package llm

import (
	"context"
	"fmt"
	"sync"
)

// JARVFallbackChain implements the FallbackChain interface for multi-provider resilience.
// It tries each candidate in order until one succeeds, enabling zero-downtime
// provider switching and rate limit handling.
type JARVFallbackChain struct {
	candidates []Candidate
	tierFilter Tier
	mu         sync.RWMutex
	usage      map[string]TokenUsage
	usageMu    sync.Mutex
}

// NewJARVFallbackChain creates a new fallback chain.
func NewJARVFallbackChain() *JARVFallbackChain {
	return &JARVFallbackChain{
		candidates: make([]Candidate, 0),
		tierFilter: "",
		usage:      make(map[string]TokenUsage),
	}
}

// Execute tries each candidate in order, returning the first success.
func (c *JARVFallbackChain) Execute(ctx context.Context, req Request) (*Response, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	var lastErr error

	for _, candidate := range c.candidates {
		// Skip if tier filter is set and candidate doesn't match
		if c.tierFilter != "" && candidate.Tier != c.tierFilter {
			continue
		}

		// Try this candidate
		req.Model = candidate.Model
		resp, err := candidate.Provider.Complete(ctx, req)
		if err == nil {
			// Success! Track usage
			c.trackUsage(candidate.Provider.Name(), resp.Usage)
			resp.Model = candidate.Provider.Name() + "/" + candidate.Model
			return resp, nil
		}

		lastErr = err
		// Could implement retry logic here for rate limits
	}

	if lastErr != nil {
		return nil, fmt.Errorf("all providers failed, last error: %w", lastErr)
	}

	return nil, fmt.Errorf("no providers available")
}

// Stream tries each candidate in order for streaming, returning the first success.
func (c *JARVFallbackChain) Stream(ctx context.Context, req Request) (<-chan StreamChunk, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	for _, candidate := range c.candidates {
		if c.tierFilter != "" && candidate.Tier != c.tierFilter {
			continue
		}

		ch, err := candidate.Provider.Stream(ctx, req)
		if err == nil {
			return ch, nil
		}
	}

	return nil, fmt.Errorf("all providers failed for streaming")
}

// Add registers a new candidate in the chain.
func (c *JARVFallbackChain) Add(candidate Candidate) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Insert in priority order
	inserted := false
	for i, existing := range c.candidates {
		if candidate.Priority < existing.Priority {
			c.candidates = append(c.candidates[:i], append([]Candidate{candidate}, c.candidates[i:]...)...)
			inserted = true
			break
		}
	}
	if !inserted {
		c.candidates = append(c.candidates, candidate)
	}
}

// SetTier filters the chain to only use candidates of the given tier.
func (c *JARVFallbackChain) SetTier(tier Tier) FallbackChain {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.tierFilter = tier
	return c
}

// Clear removes all candidates.
func (c *JARVFallbackChain) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.candidates = make([]Candidate, 0)
}

// Candidates returns a copy of current candidates.
func (c *JARVFallbackChain) Candidates() []Candidate {
	c.mu.RLock()
	defer c.mu.RUnlock()

	result := make([]Candidate, len(c.candidates))
	copy(result, c.candidates)
	return result
}

// Usage returns aggregated token usage across all providers.
func (c *JARVFallbackChain) Usage() map[string]TokenUsage {
	c.usageMu.Lock()
	defer c.usageMu.Unlock()

	result := make(map[string]TokenUsage)
	for k, v := range c.usage {
		result[k] = v
	}
	return result
}

func (c *JARVFallbackChain) trackUsage(provider string, usage TokenUsage) {
	c.usageMu.Lock()
	defer c.usageMu.Unlock()

	current := c.usage[provider]
	current.PromptTokens += usage.PromptTokens
	current.CompletionTokens += usage.CompletionTokens
	current.TotalTokens += usage.TotalTokens
	current.EstimatedCostUSD += usage.EstimatedCostUSD
	c.usage[provider] = current
}

// Complete delegates to Execute for single-request interface.
func (c *JARVFallbackChain) Complete(ctx context.Context, req Request) (*Response, error) {
	return c.Execute(ctx, req)
}

// Models returns models from the highest priority provider.
func (c *JARVFallbackChain) Models(ctx context.Context) ([]string, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if len(c.candidates) == 0 {
		return nil, fmt.Errorf("no providers configured")
	}

	return c.candidates[0].Provider.Models(ctx)
}

// Name returns the chain name.
func (c *JARVFallbackChain) Name() string {
	return "fallback-chain"
}

// Healthy checks if any provider is healthy.
func (c *JARVFallbackChain) Healthy(ctx context.Context) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	for _, candidate := range c.candidates {
		if candidate.Provider.Healthy(ctx) {
			return true
		}
	}
	return false
}
