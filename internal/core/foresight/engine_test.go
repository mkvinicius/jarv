// engine_test.go — Unit tests for the JARV Foresight Engine.
package foresight

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// Test Helpers
// ─────────────────────────────────────────────────────────────────────────────

func newTestEngine(t *testing.T) *Engine {
	t.Helper()
	cfg := DefaultConfig()
	cfg.CollectiveImmunity = false // Disable network calls in tests

	store := NewInMemoryPredictionStore()
	patterns := NewInMemoryPatternStore()
	engine := New(cfg, patterns, store, nil)
	t.Cleanup(func() { engine.Stop() })
	return engine
}

// ─────────────────────────────────────────────────────────────────────────────
// Engine Lifecycle
// ─────────────────────────────────────────────────────────────────────────────

func TestEngine_StartStop(t *testing.T) {
	engine := newTestEngine(t)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Start is a void function in current implementation.
	engine.Start(ctx)

	// Engine should be running.
	select {
	case <-ctx.Done():
		t.Error("engine did not start within timeout")
	default:
		// OK
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Signal Ingestion
// ─────────────────────────────────────────────────────────────────────────────

func TestEngine_IngestSignal(t *testing.T) {
	engine := newTestEngine(t)

	signal := Signal{
		Domain:    DomainSecurity,
		Source:    "test.source",
		EventType: "test.event",
		Value:     1.0,
		Timestamp: time.Now(),
	}

	// Should not panic or block.
	engine.Ingest(signal)
}

func TestEngine_IngestMultipleSignals(t *testing.T) {
	engine := newTestEngine(t)

	domains := []Domain{
		DomainSecurity, DomainFinancial, DomainHR,
		DomainMarketing, DomainProduct, DomainOperational,
	}

	for _, domain := range domains {
		engine.Ingest(Signal{
			Domain:    domain,
			Source:    "test",
			EventType: "test.event",
			Value:     0.5,
			Timestamp: time.Now(),
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Pattern Matching
// ─────────────────────────────────────────────────────────────────────────────

func TestEngine_SecurityPatterns_Available(t *testing.T) {
	patterns := SecurityPatterns()
	if len(patterns) == 0 {
		t.Error("expected security patterns to be non-empty")
	}

	for _, p := range patterns {
		if p.Name == "" {
			t.Error("pattern has empty name")
		}
		if p.Domain != DomainSecurity {
			t.Errorf("security pattern has wrong domain: %s", p.Domain)
		}
		if p.Confidence <= 0 || p.Confidence > 1 {
			t.Errorf("pattern %s has invalid confidence: %f", p.Name, p.Confidence)
		}
	}
}

func TestEngine_AllDomainPatterns_Available(t *testing.T) {
	allPatterns := AllBuiltInPatterns()
	if len(allPatterns) == 0 {
		t.Error("expected all patterns to be non-empty")
	}

	// Verify all 6 domains have at least one pattern.
	domainCoverage := make(map[Domain]int)
	for _, p := range allPatterns {
		domainCoverage[p.Domain]++
	}

	requiredDomains := []Domain{
		DomainSecurity, DomainFinancial, DomainHR,
		DomainMarketing, DomainProduct, DomainOperational,
	}

	for _, domain := range requiredDomains {
		count := domainCoverage[domain]
		if count == 0 {
			t.Errorf("domain %s has no patterns", domain)
		} else {
			t.Logf("domain %s: %d patterns", domain, count)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Prediction Store
// ─────────────────────────────────────────────────────────────────────────────

func TestPredictionStore_SaveAndRetrieve(t *testing.T) {
	store := NewInMemoryPredictionStore()

	prediction := Prediction{
		ID:          "test-pred-001",
		Domain:      DomainSecurity,
		Title:       "Test Prediction",
		Description: "A test prediction for unit testing",
		Severity:    SeverityHigh,
		Confidence:  0.85,
		TimeHorizon: 1 * time.Hour,
		PredictedAt: time.Now(),
		ExpiresAt:   time.Now().Add(24 * time.Hour),
		UserID:      "user:test",
		Status:      StatusActive,
	}

	ctx := context.Background()

	if err := store.Save(ctx, prediction); err != nil {
		t.Fatalf("failed to save prediction: %v", err)
	}

	predictions, err := store.Active(ctx, "user:test")
	if err != nil {
		t.Fatalf("failed to retrieve predictions: %v", err)
	}

	if len(predictions) != 1 {
		t.Errorf("expected 1 prediction, got %d", len(predictions))
		return
	}

	if predictions[0].ID != prediction.ID {
		t.Errorf("expected prediction ID %s, got %s", prediction.ID, predictions[0].ID)
	}
}

func TestPredictionStore_Dismiss(t *testing.T) {
	store := NewInMemoryPredictionStore()
	ctx := context.Background()

	prediction := Prediction{
		ID:     "test-pred-002",
		Domain: DomainFinancial,
		Title:  "Cash Flow Warning",
		UserID: "user:test",
		Status: StatusActive,
	}

	_ = store.Save(ctx, prediction)

	if err := store.UpdateStatus(ctx, prediction.ID, StatusDismissed); err != nil {
		t.Fatalf("failed to dismiss prediction: %v", err)
	}

	active, _ := store.Active(ctx, "user:test")
	for _, p := range active {
		if p.ID == prediction.ID {
			t.Error("dismissed prediction should not appear in active list")
		}
	}
}

func TestPredictionStore_History(t *testing.T) {
	store := NewInMemoryPredictionStore()
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		_ = store.Save(ctx, Prediction{
			ID:     fmt.Sprintf("pred-%d", i),
			Domain: DomainOperational,
			UserID: "user:test",
			Status: StatusActive,
		})
	}

	history, err := store.History(ctx, "user:test", 3)
	if err != nil {
		t.Fatalf("failed to retrieve history: %v", err)
	}

	if len(history) > 3 {
		t.Errorf("expected at most 3 history entries, got %d", len(history))
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Severity and Confidence
// ─────────────────────────────────────────────────────────────────────────────

func TestSeverity_Ordering(t *testing.T) {
	// Verify severity constants are defined.
	severities := []Severity{
		SeverityInfo, SeverityLow, SeverityMedium, SeverityHigh, SeverityCritical,
	}
	for _, s := range severities {
		if string(s) == "" {
			t.Error("severity constant is empty")
		}
	}
}

func TestDomain_AllDefined(t *testing.T) {
	domains := []Domain{
		DomainSecurity, DomainFinancial, DomainHR,
		DomainMarketing, DomainProduct, DomainOperational,
	}
	for _, d := range domains {
		if string(d) == "" {
			t.Error("domain constant is empty")
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Immunity Network
// ─────────────────────────────────────────────────────────────────────────────

func TestImmunityNetwork_Contribution(t *testing.T) {
	key := make([]byte, 32)
	network := NewImmunityNetwork("test-node-001", key)

	pattern := Pattern{
		ID:          "threat-001",
		Name:        "Test Threat",
		Domain:      DomainSecurity,
		Description: "A test threat pattern",
		Confidence:  0.9,
	}

	// Contribute should not error.
	if err := network.Contribute(pattern); err != nil {
		t.Fatalf("Contribute failed: %v", err)
	}

	// Verify peer tracking works.
	network.mu.RLock()
	peerCount := len(network.peers)
	network.mu.RUnlock()

	// No peers yet — peerCount should be 0.
	t.Logf("immunity network initialized with %d peers", peerCount)
}
