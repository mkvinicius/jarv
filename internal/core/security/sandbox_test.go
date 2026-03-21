// sandbox_test.go — Unit tests for behavioral anomaly detection.
package security

import (
	"context"
	"math"
	"testing"
	"time"
)

func newTestDetector() *AnomalyDetector {
	cfg := DefaultAnomalyDetectorConfig()
	cfg.MinSamplesForBaseline = 5 // Lower for tests
	return NewAnomalyDetector(cfg)
}

func baseMetric(agentID string) BehaviorMetric {
	return BehaviorMetric{
		AgentID:         agentID,
		SessionID:       "session-001",
		Timestamp:       time.Now(),
		InputTokens:     100,
		OutputTokens:    200,
		TotalTokens:     300,
		TotalLatencyMs:  500,
		ToolCallCount:   2,
		ToolNames:       []string{"search", "memory_read"},
		OutputEntropy:   4.0,
		URLsInOutput:    0,
		MemoryBytesRead: 1024,
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Absolute Checks
// ─────────────────────────────────────────────────────────────────────────────

func TestAnomaly_TokenCap_Exceeded(t *testing.T) {
	d := newTestDetector()
	m := baseMetric("agent-001")
	m.TotalTokens = 9000 // > 8000 limit

	anomalies := d.Observe(context.Background(), m)
	found := false
	for _, a := range anomalies {
		if a.Type == AnomalyTokenSpike {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected token spike anomaly for tokens > limit")
	}
}

func TestAnomaly_TokenCap_Normal(t *testing.T) {
	d := newTestDetector()
	m := baseMetric("agent-001")
	m.TotalTokens = 500 // Well within limit

	anomalies := d.Observe(context.Background(), m)
	for _, a := range anomalies {
		if a.Type == AnomalyTokenSpike {
			t.Error("unexpected token spike anomaly for normal token count")
		}
	}
}

func TestAnomaly_URLExfiltration(t *testing.T) {
	d := newTestDetector()
	m := baseMetric("agent-001")
	m.URLsInOutput = 10 // > 5 limit

	anomalies := d.Observe(context.Background(), m)
	found := false
	for _, a := range anomalies {
		if a.Type == AnomalyURLExfil {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected URL exfiltration anomaly")
	}
}

func TestAnomaly_HighEntropy(t *testing.T) {
	d := newTestDetector()
	m := baseMetric("agent-001")
	m.OutputEntropy = 6.5 // > 5.5 limit (base64-like)

	anomalies := d.Observe(context.Background(), m)
	found := false
	for _, a := range anomalies {
		if a.Type == AnomalyEntropySpike {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected entropy spike anomaly for high-entropy output")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Statistical Checks (require baseline)
// ─────────────────────────────────────────────────────────────────────────────

func TestAnomaly_StatisticalTokenSpike(t *testing.T) {
	d := newTestDetector()
	agentID := "agent-stat-001"

	// Establish baseline with 10 normal observations.
	for i := 0; i < 10; i++ {
		m := baseMetric(agentID)
		m.TotalTokens = 300 + i*5 // ~300-345 tokens, low variance
		d.Observe(context.Background(), m)
	}

	// Now send a massive spike.
	spike := baseMetric(agentID)
	spike.TotalTokens = 5000 // Way above baseline

	anomalies := d.Observe(context.Background(), spike)
	found := false
	for _, a := range anomalies {
		if a.Type == AnomalyTokenSpike {
			found = true
			t.Logf("Detected token spike: z=%.2f, severity=%s, action=%s",
				a.Score*10, a.Severity, a.Action)
			break
		}
	}
	if !found {
		t.Error("expected statistical token spike anomaly")
	}
}

func TestAnomaly_NormalBehavior_NoFalsePositives(t *testing.T) {
	d := newTestDetector()
	agentID := "agent-normal-001"

	// Establish baseline.
	for i := 0; i < 10; i++ {
		m := baseMetric(agentID)
		d.Observe(context.Background(), m)
	}

	// Observe normal behavior — should not trigger statistical anomalies.
	m := baseMetric(agentID)
	anomalies := d.Observe(context.Background(), m)

	for _, a := range anomalies {
		// Absolute checks may still fire, but not statistical ones for normal behavior.
		if a.Score > 0.5 {
			t.Errorf("unexpected high-score anomaly for normal behavior: %+v", a)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Risk Score
// ─────────────────────────────────────────────────────────────────────────────

func TestAnomaly_RiskScore_Clean(t *testing.T) {
	d := newTestDetector()
	score := d.AgentRiskScore("clean-agent")
	if score != 0.0 {
		t.Errorf("expected 0.0 risk score for agent with no anomalies, got %.2f", score)
	}
}

func TestAnomaly_RiskScore_AfterViolations(t *testing.T) {
	d := newTestDetector()
	agentID := "risky-agent"

	// Trigger multiple anomalies.
	for i := 0; i < 3; i++ {
		m := baseMetric(agentID)
		m.TotalTokens = 9000
		m.URLsInOutput = 10
		d.Observe(context.Background(), m)
	}

	score := d.AgentRiskScore(agentID)
	if score <= 0.0 {
		t.Error("expected positive risk score after anomalies")
	}
	t.Logf("Risk score after violations: %.2f", score)
}

// ─────────────────────────────────────────────────────────────────────────────
// Shannon Entropy
// ─────────────────────────────────────────────────────────────────────────────

func TestShannonEntropy_EmptyString(t *testing.T) {
	e := ShannonEntropy("")
	if e != 0 {
		t.Errorf("expected 0 entropy for empty string, got %.2f", e)
	}
}

func TestShannonEntropy_SingleChar(t *testing.T) {
	e := ShannonEntropy("aaaaaaa")
	if e != 0 {
		t.Errorf("expected 0 entropy for single-char string, got %.2f", e)
	}
}

func TestShannonEntropy_EnglishText(t *testing.T) {
	text := "The quick brown fox jumps over the lazy dog"
	e := ShannonEntropy(text)
	// English text should have entropy between 3.5 and 5.0
	if e < 3.5 || e > 5.0 {
		t.Errorf("unexpected entropy for English text: %.2f (expected 3.5-5.0)", e)
	}
	t.Logf("English text entropy: %.2f bits/char", e)
}

func TestShannonEntropy_HighEntropy(t *testing.T) {
	// Simulate base64-like high entropy string.
	base64Like := "SGVsbG8gV29ybGQhIFRoaXMgaXMgYSBiYXNlNjQgZW5jb2RlZCBzdHJpbmcu"
	e := ShannonEntropy(base64Like)
	t.Logf("Base64-like string entropy: %.2f bits/char", e)
	// Base64 should have entropy > 4.5
	if e < 4.5 {
		t.Errorf("expected high entropy for base64-like string, got %.2f", e)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Welford's Online Statistics
// ─────────────────────────────────────────────────────────────────────────────

func TestOnlineStat_Mean(t *testing.T) {
	s := &onlineStat{}
	values := []float64{2, 4, 4, 4, 5, 5, 7, 9}
	for _, v := range values {
		s.Update(v)
	}

	expectedMean := 5.0
	if math.Abs(s.Mean()-expectedMean) > 0.001 {
		t.Errorf("expected mean %.2f, got %.2f", expectedMean, s.Mean())
	}
}

func TestOnlineStat_StdDev(t *testing.T) {
	s := &onlineStat{}
	values := []float64{2, 4, 4, 4, 5, 5, 7, 9}
	for _, v := range values {
		s.Update(v)
	}

	// Population std dev = 2, sample std dev ≈ 2.138
	expectedStdDev := 2.138
	if math.Abs(s.StdDev()-expectedStdDev) > 0.01 {
		t.Errorf("expected std dev ~%.3f, got %.3f", expectedStdDev, s.StdDev())
	}
}

func TestOnlineStat_ZScore(t *testing.T) {
	s := &onlineStat{}
	for i := 0; i < 100; i++ {
		s.Update(float64(i))
	}

	// Mean ≈ 49.5, StdDev ≈ 29.01
	// Z-score of 49.5 (mean) should be ~0
	z := s.ZScore(s.Mean())
	if z > 0.001 {
		t.Errorf("z-score of mean should be ~0, got %.4f", z)
	}

	// Z-score of a very high value should be large
	zHigh := s.ZScore(200)
	if zHigh < 3.0 {
		t.Errorf("z-score of outlier should be > 3.0, got %.2f", zHigh)
	}
}

func TestOnlineStat_EmptyStdDev(t *testing.T) {
	s := &onlineStat{}
	if s.StdDev() != 0 {
		t.Error("empty stat should have std dev of 0")
	}
}
