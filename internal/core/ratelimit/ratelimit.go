package ratelimit

import (
	"sync"
	"time"
)

// TokenBucket implements a token bucket rate limiter.
type TokenBucket struct {
	capacity   int64
	tokens     int64
	refillRate float64 // tokens per second
	lastRefill int64   // Unix timestamp
	mu         sync.Mutex
}

// NewTokenBucket creates a new token bucket with the given capacity and refill rate.
func NewTokenBucket(capacity int64, refillPerSecond float64) *TokenBucket {
	return &TokenBucket{
		capacity:   capacity,
		tokens:     capacity,
		refillRate: refillPerSecond,
		lastRefill: time.Now().Unix(),
	}
}

// Allow attempts to consume one token. Returns true if allowed.
func (b *TokenBucket) Allow() bool {
	return b.AllowN(1)
}

// AllowN attempts to consume n tokens. Returns true if all were allowed.
func (b *TokenBucket) AllowN(n int64) bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.refill()

	if b.tokens >= n {
		b.tokens -= n
		return true
	}
	return false
}

func (b *TokenBucket) refill() {
	now := time.Now().Unix()
	elapsed := float64(now - b.lastRefill)
	if elapsed > 0 {
		add := int64(elapsed * b.refillRate)
		b.tokens = min(b.tokens+add, b.capacity)
		b.lastRefill = now
	}
}

// WaitTime returns how long until n tokens are available.
func (b *TokenBucket) WaitTime(n int64) time.Duration {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.refill()

	if b.tokens >= n {
		return 0
	}
	needed := float64(n - b.tokens)
	return time.Duration(needed/b.refillRate) * time.Second
}
