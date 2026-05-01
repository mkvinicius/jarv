package ratelimit

import (
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

// ErrCircuitOpen is returned when the circuit breaker is open.
var ErrCircuitOpen = errors.New("circuit breaker: open")

// ErrCircuitTimeout is returned when an operation times out.
var ErrCircuitTimeout = errors.New("circuit breaker: timeout")

// State represents the circuit breaker state.
type State int

const (
	StateClosed   State = 0
	StateOpen     State = 1
	StateHalfOpen State = 2
)

func (s State) String() string {
	switch s {
	case StateClosed:
		return "closed"
	case StateOpen:
		return "open"
	case StateHalfOpen:
		return "half-open"
	}
	return "unknown"
}

// CircuitBreaker implements the circuit breaker pattern.
type CircuitBreaker struct {
	name              string
	failureThreshold  int           // failures before opening
	successThreshold  int           // successes in half-open before closing
	timeout           time.Duration // time before trying half-open
	maxHalfOpen       int           // max concurrent half-open calls
	state             atomic.Int64
	failures          atomic.Int64
	successes         atomic.Int64
	lastFailure       atomic.Int64
	halfOpen          atomic.Int64
	mu                sync.Mutex
	onOpen            func(name string)
	onClose           func(name string)
}

// CircuitBreakerConfig holds the configuration for a circuit breaker.
type CircuitBreakerConfig struct {
	Name             string
	FailureThreshold int
	SuccessThreshold int
	Timeout          time.Duration
	MaxHalfOpen      int
	OnOpen           func(name string)
	OnClose          func(name string)
}

// NewCircuitBreaker creates a new circuit breaker.
func NewCircuitBreaker(cfg CircuitBreakerConfig) *CircuitBreaker {
	if cfg.FailureThreshold == 0 {
		cfg.FailureThreshold = 5
	}
	if cfg.SuccessThreshold == 0 {
		cfg.SuccessThreshold = 2
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Second
	}
	if cfg.MaxHalfOpen == 0 {
		cfg.MaxHalfOpen = 1
	}
	return &CircuitBreaker{
		name:             cfg.Name,
		failureThreshold: cfg.FailureThreshold,
		successThreshold: cfg.SuccessThreshold,
		timeout:          cfg.Timeout,
		maxHalfOpen:      cfg.MaxHalfOpen,
		onOpen:           cfg.OnOpen,
		onClose:          cfg.OnClose,
	}
}

// Execute runs the given function through the circuit breaker.
func (cb *CircuitBreaker) Execute(fn func() error) error {
	state := State(cb.state.Load())

	if state == StateOpen {
		// Check if timeout has passed
		lastFail := time.Unix(cb.lastFailure.Load(), 0)
		if time.Since(lastFail) < cb.timeout {
			return ErrCircuitOpen
		}

		// Try to transition to half-open
		if !cb.halfOpen.CompareAndSwap(0, 1) {
			return ErrCircuitOpen
		}
		cb.state.Store(int64(StateHalfOpen))
	}

	err := fn()

	if err != nil {
		cb.onFailure()
		return err
	}

	cb.onSuccess()
	return nil
}

func (cb *CircuitBreaker) onFailure() {
	failures := cb.failures.Add(1)
	cb.lastFailure.Store(time.Now().Unix())

	if State(cb.state.Load()) == StateHalfOpen {
		// Any failure in half-open goes back to open
		cb.state.Store(int64(StateOpen))
		cb.halfOpen.Store(0)
		cb.successes.Store(0)
		if cb.onOpen != nil {
			cb.onOpen(cb.name)
		}
		return
	}

	if failures >= int64(cb.failureThreshold) {
		cb.state.Store(int64(StateOpen))
		cb.halfOpen.Store(0)
		if cb.onOpen != nil {
			cb.onOpen(cb.name)
		}
	}
}

func (cb *CircuitBreaker) onSuccess() {
	if State(cb.state.Load()) == StateHalfOpen {
		successes := cb.successes.Add(1)
		if successes >= int64(cb.successThreshold) {
			cb.state.Store(int64(StateClosed))
			cb.failures.Store(0)
			cb.successes.Store(0)
			cb.halfOpen.Store(0)
			if cb.onClose != nil {
				cb.onClose(cb.name)
			}
		}
	} else {
		// Reset failure counter on success in closed state
		cb.failures.Store(0)
	}
}

// State returns the current state of the circuit breaker.
func (cb *CircuitBreaker) State() State {
	return State(cb.state.Load())
}

// Stats returns circuit breaker statistics.
func (cb *CircuitBreaker) Stats() map[string]any {
	return map[string]any{
		"state":     cb.state.Load(),
		"failures":  cb.failures.Load(),
		"successes": cb.successes.Load(),
		"name":      cb.name,
	}
}
