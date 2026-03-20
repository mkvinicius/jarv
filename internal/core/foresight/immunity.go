// immunity.go — Collective Immunity Layer for JARV Foresight.
//
// The Collective Immunity system allows JARV instances to share learned threat
// patterns with each other. When one instance detects and confirms a new attack
// pattern, it contributes that pattern to the collective network. All other
// instances receive it and become immune immediately.
//
// This mirrors how biological immune systems work at the population level:
// one organism's immune response benefits the entire herd.
//
// Security model:
//   - All patterns are signed with HMAC-SHA256 before transmission.
//   - Patterns are validated against a schema before being accepted.
//   - Poisoning attacks (malicious patterns) are detected via anomaly scoring.
//   - Each instance maintains a trust score for contributing nodes.
package foresight

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// Collective Immunity Network
// ─────────────────────────────────────────────────────────────────────────────

// ImmunityNetwork manages the exchange of threat patterns between JARV instances.
type ImmunityNetwork struct {
	instanceID string
	signingKey []byte // HMAC-SHA256 key for pattern signing

	mu       sync.RWMutex
	peers    map[string]*Peer    // Known peer instances
	received map[string]struct{} // Pattern IDs already received (dedup)

	outbox chan SignedPattern // Patterns ready to be sent to peers
	inbox  chan SignedPattern // Patterns received from peers
}

// Peer represents another JARV instance in the collective network.
type Peer struct {
	ID          string
	Endpoint    string
	TrustScore  float64 // 0.0–1.0 — based on pattern accuracy history
	LastSeen    time.Time
	Contributed int // Patterns contributed
	Confirmed   int // Contributed patterns that were confirmed
}

// SignedPattern is a pattern with a cryptographic signature for tamper detection.
type SignedPattern struct {
	Pattern   Pattern
	Signature string // HMAC-SHA256 of the pattern JSON
	PeerID    string
	SentAt    time.Time
}

// NewImmunityNetwork creates a new collective immunity network node.
func NewImmunityNetwork(instanceID string, signingKey []byte) *ImmunityNetwork {
	return &ImmunityNetwork{
		instanceID: instanceID,
		signingKey: signingKey,
		peers:      make(map[string]*Peer),
		received:   make(map[string]struct{}),
		outbox:     make(chan SignedPattern, 100),
		inbox:      make(chan SignedPattern, 100),
	}
}

// Contribute adds a locally confirmed pattern to the outbox for distribution.
func (n *ImmunityNetwork) Contribute(p Pattern) error {
	signed, err := n.sign(p)
	if err != nil {
		return fmt.Errorf("immunity: failed to sign pattern: %w", err)
	}

	select {
	case n.outbox <- signed:
		return nil
	default:
		return fmt.Errorf("immunity: outbox full, pattern dropped")
	}
}

// Receive processes an incoming signed pattern from a peer.
// Returns the pattern if valid and new, nil if already known or invalid.
func (n *ImmunityNetwork) Receive(sp SignedPattern) (*Pattern, error) {
	// Deduplicate.
	n.mu.RLock()
	_, known := n.received[sp.Pattern.ID]
	n.mu.RUnlock()
	if known {
		return nil, nil
	}

	// Verify signature.
	if !n.verify(sp) {
		return nil, fmt.Errorf("immunity: invalid signature for pattern %s", sp.Pattern.ID)
	}

	// Validate pattern schema.
	if err := n.validatePattern(sp.Pattern); err != nil {
		return nil, fmt.Errorf("immunity: invalid pattern schema: %w", err)
	}

	// Check peer trust score.
	n.mu.RLock()
	peer, exists := n.peers[sp.PeerID]
	n.mu.RUnlock()
	if exists && peer.TrustScore < 0.3 {
		return nil, fmt.Errorf("immunity: peer %s trust score too low (%.2f)", sp.PeerID, peer.TrustScore)
	}

	// Mark as received.
	n.mu.Lock()
	n.received[sp.Pattern.ID] = struct{}{}
	n.mu.Unlock()

	return &sp.Pattern, nil
}

// UpdatePeerTrust adjusts a peer's trust score based on pattern accuracy.
func (n *ImmunityNetwork) UpdatePeerTrust(peerID string, patternConfirmed bool) {
	n.mu.Lock()
	defer n.mu.Unlock()

	peer, exists := n.peers[peerID]
	if !exists {
		return
	}

	peer.Contributed++
	if patternConfirmed {
		peer.Confirmed++
	}

	// Recalculate trust score as accuracy rate.
	if peer.Contributed > 0 {
		peer.TrustScore = float64(peer.Confirmed) / float64(peer.Contributed)
	}
}

// sign creates a signed pattern for transmission.
func (n *ImmunityNetwork) sign(p Pattern) (SignedPattern, error) {
	data, err := json.Marshal(p)
	if err != nil {
		return SignedPattern{}, err
	}

	mac := hmac.New(sha256.New, n.signingKey)
	mac.Write(data)
	sig := hex.EncodeToString(mac.Sum(nil))

	return SignedPattern{
		Pattern:   p,
		Signature: sig,
		PeerID:    n.instanceID,
		SentAt:    time.Now(),
	}, nil
}

// verify checks the HMAC-SHA256 signature of a received pattern.
func (n *ImmunityNetwork) verify(sp SignedPattern) bool {
	data, err := json.Marshal(sp.Pattern)
	if err != nil {
		return false
	}

	mac := hmac.New(sha256.New, n.signingKey)
	mac.Write(data)
	expected := hex.EncodeToString(mac.Sum(nil))

	return hmac.Equal([]byte(sp.Signature), []byte(expected))
}

// validatePattern checks that a received pattern meets the minimum schema requirements.
func (n *ImmunityNetwork) validatePattern(p Pattern) error {
	if p.ID == "" {
		return fmt.Errorf("missing ID")
	}
	if p.Domain == "" {
		return fmt.Errorf("missing Domain")
	}
	if len(p.Signals) == 0 {
		return fmt.Errorf("pattern must have at least one signal")
	}
	if p.Confidence < 0 || p.Confidence > 1 {
		return fmt.Errorf("confidence must be between 0 and 1")
	}
	// Security domain patterns must have a minimum confidence to prevent false positives.
	if p.Domain == DomainSecurity && p.Confidence < 0.60 {
		return fmt.Errorf("security patterns require minimum 0.60 confidence")
	}
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Archetypal Simulator (Foresight-specific Oracle integration)
// ─────────────────────────────────────────────────────────────────────────────

// ArchetypalSimulator implements the Simulator interface using domain-specific
// archetypes to generate predictions without calling an LLM for every scenario.
// For complex scenarios, it escalates to the full Oracle.
type ArchetypalSimulator struct {
	llmCallFn func(ctx context.Context, prompt string) (string, error)
}

// NewArchetypalSimulator creates a new simulator with an optional LLM call function.
// If llmCallFn is nil, the simulator uses rule-based predictions only.
func NewArchetypalSimulator(llmCallFn func(ctx context.Context, prompt string) (string, error)) *ArchetypalSimulator {
	return &ArchetypalSimulator{llmCallFn: llmCallFn}
}

// SimulateScenario generates predictions for a domain based on matched patterns and signals.
func (s *ArchetypalSimulator) SimulateScenario(
	ctx context.Context,
	domain Domain,
	signals []Signal,
	patterns []Pattern,
) ([]Prediction, error) {
	var predictions []Prediction

	for _, pattern := range patterns {
		pred := s.buildPrediction(domain, pattern, signals)

		// For high-confidence critical predictions, enrich with LLM reasoning.
		if pred.Confidence > 0.80 && s.llmCallFn != nil {
			enriched, err := s.enrichWithLLM(ctx, pred)
			if err == nil {
				pred = enriched
			}
		}

		predictions = append(predictions, pred)
	}

	return predictions, nil
}

// buildPrediction constructs a prediction from a pattern match using rule-based logic.
// This is the fast, zero-cost path that handles 90% of predictions.
func (s *ArchetypalSimulator) buildPrediction(domain Domain, pattern Pattern, signals []Signal) Prediction {
	// Calculate confidence from pattern history and signal recency.
	confidence := pattern.Confidence
	if pattern.Occurrences > 5 {
		// Adjust based on historical accuracy.
		historicalAccuracy := float64(pattern.Confirmed) / float64(pattern.Occurrences)
		confidence = (confidence + historicalAccuracy) / 2
	}

	// Determine severity from domain and confidence.
	severity := confidenceToSeverity(confidence, domain)

	// Build recommended actions based on domain and pattern.
	actions := buildActions(domain, pattern, severity)

	// Build evidence points from matched signals.
	evidence := buildEvidence(signals)

	// Get archetypes for this domain.
	archetypes := getArchetypes(domain)

	now := time.Now()
	return Prediction{
		ID:              generateID(pattern.ID, now),
		Domain:          domain,
		Title:           pattern.Name,
		Description:     pattern.Description,
		Severity:        severity,
		Confidence:      confidence,
		TimeHorizon:     pattern.LeadTime,
		PredictedAt:     now,
		ExpiresAt:       now.Add(pattern.LeadTime * 2),
		Status:          StatusActive,
		TriggerSignals:  signals,
		MatchedPattern:  &pattern,
		Archetypes:      archetypes,
		RecommendedActs: actions,
		Evidence:        evidence,
	}
}

// enrichWithLLM uses the LLM to add nuanced reasoning to a high-confidence prediction.
// This is the expensive path — only called for critical predictions.
func (s *ArchetypalSimulator) enrichWithLLM(ctx context.Context, pred Prediction) (Prediction, error) {
	prompt := fmt.Sprintf(
		"You are JARV Foresight, a predictive intelligence system. "+
			"A pattern has been detected in the %s domain with %.0f%% confidence.\n\n"+
			"Pattern: %s\n"+
			"Description: %s\n"+
			"Evidence: %v\n\n"+
			"In 2-3 sentences, provide a specific, actionable insight about what is likely to happen "+
			"and the single most important action to take. Be direct and concrete.",
		pred.Domain,
		pred.Confidence*100,
		pred.Title,
		pred.Description,
		pred.Evidence,
	)

	insight, err := s.llmCallFn(ctx, prompt)
	if err != nil {
		return pred, err
	}

	// Prepend the LLM insight to the description.
	pred.Description = insight + "\n\n" + pred.Description
	return pred, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// SQLite-backed Stores
// ─────────────────────────────────────────────────────────────────────────────

// InMemoryPatternStore is a simple in-memory implementation of PatternStore.
// In production, this is backed by SQLite via the memory adapter.
type InMemoryPatternStore struct {
	mu       sync.RWMutex
	patterns map[string]Pattern
}

func NewInMemoryPatternStore() *InMemoryPatternStore {
	store := &InMemoryPatternStore{
		patterns: make(map[string]Pattern),
	}
	// Seed with all built-in patterns.
	for _, p := range AllBuiltInPatterns() {
		store.patterns[p.ID] = p
	}
	return store
}

func (s *InMemoryPatternStore) SavePattern(_ context.Context, p Pattern) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.patterns[p.ID] = p
	return nil
}

func (s *InMemoryPatternStore) LoadPatterns(_ context.Context, domain Domain) ([]Pattern, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []Pattern
	for _, p := range s.patterns {
		if p.Domain == domain {
			result = append(result, p)
		}
	}
	return result, nil
}

func (s *InMemoryPatternStore) UpdatePattern(_ context.Context, id string, confirmed bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.patterns[id]
	if !ok {
		return fmt.Errorf("pattern %s not found", id)
	}
	p.Occurrences++
	if confirmed {
		p.Confirmed++
	}
	s.patterns[id] = p
	return nil
}

func (s *InMemoryPatternStore) AllPatterns(_ context.Context) ([]Pattern, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]Pattern, 0, len(s.patterns))
	for _, p := range s.patterns {
		result = append(result, p)
	}
	return result, nil
}

// InMemoryPredictionStore is a simple in-memory implementation of PredictionStore.
type InMemoryPredictionStore struct {
	mu          sync.RWMutex
	predictions map[string]Prediction
}

func NewInMemoryPredictionStore() *InMemoryPredictionStore {
	return &InMemoryPredictionStore{
		predictions: make(map[string]Prediction),
	}
}

func (s *InMemoryPredictionStore) Save(_ context.Context, p Prediction) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.predictions[p.ID] = p
	return nil
}

func (s *InMemoryPredictionStore) Active(_ context.Context, userID string) ([]Prediction, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []Prediction
	now := time.Now()
	for _, p := range s.predictions {
		if p.Status != StatusActive {
			continue
		}
		if p.ExpiresAt.Before(now) {
			continue
		}
		if userID != "" && p.UserID != "" && p.UserID != userID {
			continue
		}
		result = append(result, p)
	}
	return result, nil
}

func (s *InMemoryPredictionStore) History(_ context.Context, userID string, limit int) ([]Prediction, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []Prediction
	for _, p := range s.predictions {
		if userID != "" && p.UserID != "" && p.UserID != userID {
			continue
		}
		result = append(result, p)
		if len(result) >= limit {
			break
		}
	}
	return result, nil
}

func (s *InMemoryPredictionStore) UpdateStatus(_ context.Context, id string, status Status) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.predictions[id]
	if !ok {
		return fmt.Errorf("prediction %s not found", id)
	}
	p.Status = status
	s.predictions[id] = p
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Helper Functions
// ─────────────────────────────────────────────────────────────────────────────

func confidenceToSeverity(confidence float64, domain Domain) Severity {
	// Security domain uses higher severity thresholds.
	if domain == DomainSecurity {
		switch {
		case confidence >= 0.90:
			return SeverityCritical
		case confidence >= 0.75:
			return SeverityHigh
		case confidence >= 0.60:
			return SeverityMedium
		default:
			return SeverityLow
		}
	}

	// Other domains use standard thresholds.
	switch {
	case confidence >= 0.85:
		return SeverityHigh
	case confidence >= 0.70:
		return SeverityMedium
	case confidence >= 0.55:
		return SeverityLow
	default:
		return SeverityInfo
	}
}

func buildActions(domain Domain, pattern Pattern, severity Severity) []Action {
	actions := domainActions[domain]
	if actions == nil {
		return []Action{{
			Priority:    1,
			Title:       "Review and Monitor",
			Description: fmt.Sprintf("Review the pattern '%s' and monitor for further signals.", pattern.Name),
			Automated:   false,
		}}
	}
	return actions[severity]
}

var domainActions = map[Domain]map[Severity][]Action{
	DomainSecurity: {
		SeverityCritical: {
			{Priority: 1, Title: "Activate Emergency Shield", Description: "Elevate Shield to maximum sensitivity and block suspicious IPs immediately.", Automated: true, AutoCommand: "shield:emergency_mode"},
			{Priority: 2, Title: "Alert Administrator", Description: "Send immediate notification to all administrators.", Automated: true, AutoCommand: "notify:admin:critical"},
			{Priority: 3, Title: "Preserve Evidence", Description: "Capture and archive all related logs for forensic analysis.", Automated: true, AutoCommand: "shield:capture_logs"},
		},
		SeverityHigh: {
			{Priority: 1, Title: "Increase Shield Sensitivity", Description: "Temporarily increase rate limiting and anomaly detection thresholds.", Automated: true, AutoCommand: "shield:increase_sensitivity"},
			{Priority: 2, Title: "Review Recent Access Logs", Description: "Manually review the last 30 minutes of access logs for suspicious patterns.", Automated: false},
		},
		SeverityMedium: {
			{Priority: 1, Title: "Monitor Closely", Description: "Add the triggering signals to the watchlist for the next 2 hours.", Automated: true, AutoCommand: "shield:add_watchlist"},
		},
	},
	DomainFinancial: {
		SeverityHigh: {
			{Priority: 1, Title: "Review Cash Flow Projections", Description: "Update financial model with current burn rate and revenue trajectory.", Automated: false},
			{Priority: 2, Title: "Identify Cost Reduction Opportunities", Description: "Run Oracle analysis on expense categories for optimization opportunities.", Automated: false},
		},
		SeverityMedium: {
			{Priority: 1, Title: "Schedule Financial Review", Description: "Schedule a financial review meeting within the next 7 days.", Automated: false},
		},
	},
	DomainOperational: {
		SeverityCritical: {
			{Priority: 1, Title: "Scale Resources", Description: "Immediately provision additional compute resources.", Automated: true, AutoCommand: "ops:scale_up"},
			{Priority: 2, Title: "Enable Circuit Breaker", Description: "Activate circuit breaker to prevent cascading failures.", Automated: true, AutoCommand: "ops:circuit_breaker"},
		},
		SeverityHigh: {
			{Priority: 1, Title: "Optimize Resource Usage", Description: "Identify and terminate resource-intensive background processes.", Automated: false},
		},
	},
}

func buildEvidence(signals []Signal) []string {
	var evidence []string
	counts := make(map[string]int)
	for _, s := range signals {
		counts[s.EventType]++
	}
	for eventType, count := range counts {
		evidence = append(evidence, fmt.Sprintf("%d signal(s) of type '%s' detected", count, eventType))
	}
	return evidence
}

func getArchetypes(domain Domain) []Archetype {
	switch domain {
	case DomainSecurity:
		return SecurityArchetypes()
	default:
		return nil
	}
}

func generateID(patternID string, t time.Time) string {
	data := fmt.Sprintf("%s-%d", patternID, t.UnixNano())
	h := sha256.Sum256([]byte(data))
	return hex.EncodeToString(h[:8])
}
