// Package foresight implements the JARV Foresight — Universal Predictive Intelligence.
//
// Foresight is the central nervous system of JARV. It continuously monitors all
// system domains, recognizes emerging threat and opportunity patterns, runs
// archetypal simulations, and delivers predictive alerts before events materialize.
//
// Architecture: OODA Loop (Observe → Orient → Decide → Act) applied to AI systems.
// Inspired by biological immune systems: continuous monitoring, pattern memory,
// collective immunity, and anticipatory response.
package foresight

import (
	"context"
	"fmt"
	"math"
	"sort"
	"sync"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// Core Types
// ─────────────────────────────────────────────────────────────────────────────

// Domain represents a predictive domain monitored by Foresight.
type Domain string

const (
	DomainSecurity    Domain = "security"
	DomainFinancial   Domain = "financial"
	DomainHR          Domain = "hr"
	DomainMarketing   Domain = "marketing"
	DomainProduct     Domain = "product"
	DomainOperational Domain = "operational"
)

// Severity classifies the urgency of a predictive alert.
type Severity string

const (
	SeverityInfo     Severity = "info"     // Opportunity or neutral observation
	SeverityLow      Severity = "low"      // Worth monitoring
	SeverityMedium   Severity = "medium"   // Action recommended
	SeverityHigh     Severity = "high"     // Immediate attention required
	SeverityCritical Severity = "critical" // Emergency — act now
)

// Status represents the lifecycle state of a prediction.
type Status string

const (
	StatusActive    Status = "active"    // Prediction is live and being tracked
	StatusConfirmed Status = "confirmed" // Event materialized as predicted
	StatusExpired   Status = "expired"   // Time window passed without event
	StatusDismissed Status = "dismissed" // User acknowledged and dismissed
)

// Signal is a raw data point observed from any domain.
// Signals are the input to the OODA loop's Observe phase.
type Signal struct {
	ID        string
	Domain    Domain
	Source    string            // e.g., "shield.rate_limiter", "memory.sync", "user.behavior"
	EventType string            // e.g., "prompt_injection_attempt", "cash_flow_drop"
	Value     float64           // Numeric magnitude of the signal
	Metadata  map[string]string // Arbitrary context
	Timestamp time.Time
}

// Pattern is a recognized combination of signals that historically precedes an event.
// Patterns are stored in the Knowledge Graph and improve over time.
type Pattern struct {
	ID          string
	Domain      Domain
	Name        string
	Description string
	Signals     []string      // Signal event types that compose this pattern
	Confidence  float64       // 0.0–1.0 — how reliable this pattern is historically
	LeadTime    time.Duration // How far in advance this pattern predicts the event
	Occurrences int           // How many times this pattern was observed
	Confirmed   int           // How many times the predicted event materialized
}

// Archetype represents a simulated actor used in predictive scenarios.
// Derived from the Oracle's archetypal sampling technique.
type Archetype struct {
	Name        string
	Role        string // e.g., "Attacker", "Disengaged Employee", "Churning Customer"
	Motivation  string
	Behavior    string
	Probability float64 // Likelihood this archetype is the active actor
}

// Prediction is the output of the Foresight Engine's OODA loop.
// It represents a specific future event with confidence, timeline, and recommended actions.
type Prediction struct {
	ID              string
	Domain          Domain
	Title           string
	Description     string
	Severity        Severity
	Confidence      float64       // 0.0–1.0
	TimeHorizon     time.Duration // How far into the future
	PredictedAt     time.Time
	ExpiresAt       time.Time
	Status          Status
	TriggerSignals  []Signal
	MatchedPattern  *Pattern
	Archetypes      []Archetype
	RecommendedActs []Action
	Evidence        []string // Human-readable evidence points
	UserID          string   // Empty = system-wide; set = user-specific
}

// Action is a concrete recommended step in response to a prediction.
type Action struct {
	Priority    int
	Title       string
	Description string
	Automated   bool   // Can JARV execute this automatically?
	AutoCommand string // If automated, the internal command to run
}

// ─────────────────────────────────────────────────────────────────────────────
// Interfaces
// ─────────────────────────────────────────────────────────────────────────────

// SignalSource is implemented by any component that can emit signals.
// The Shield, Memory, Dashboard, and all domain analyzers implement this.
type SignalSource interface {
	Name() string
	Domain() Domain
	Collect(ctx context.Context) ([]Signal, error)
}

// PatternStore persists and retrieves patterns from the Knowledge Graph.
type PatternStore interface {
	SavePattern(ctx context.Context, p Pattern) error
	LoadPatterns(ctx context.Context, domain Domain) ([]Pattern, error)
	UpdatePattern(ctx context.Context, id string, confirmed bool) error
	AllPatterns(ctx context.Context) ([]Pattern, error)
}

// PredictionStore persists predictions for history, learning, and UI display.
type PredictionStore interface {
	Save(ctx context.Context, p Prediction) error
	Active(ctx context.Context, userID string) ([]Prediction, error)
	History(ctx context.Context, userID string, limit int) ([]Prediction, error)
	UpdateStatus(ctx context.Context, id string, status Status) error
}

// Simulator runs archetypal simulations to generate predictions.
// Implemented by the Oracle package.
type Simulator interface {
	SimulateScenario(ctx context.Context, domain Domain, signals []Signal, patterns []Pattern) ([]Prediction, error)
}

// Notifier delivers alerts to users through configured channels.
type Notifier interface {
	Notify(ctx context.Context, p Prediction) error
}

// ─────────────────────────────────────────────────────────────────────────────
// Foresight Engine
// ─────────────────────────────────────────────────────────────────────────────

// Config holds the configuration for the Foresight Engine.
type Config struct {
	// ScanInterval controls how often the OODA loop runs.
	ScanInterval time.Duration

	// MinConfidence is the minimum confidence threshold to emit a prediction.
	MinConfidence float64

	// MaxActivePredictions limits concurrent active predictions per user.
	MaxActivePredictions int

	// AutoActThreshold — predictions above this confidence trigger automated actions.
	AutoActThreshold float64

	// CollectiveImmunity enables sharing of security patterns across instances.
	CollectiveImmunity bool

	// CollectiveImmunityEndpoint is the URL of the JARV collective network.
	CollectiveImmunityEndpoint string

	// Domains to monitor. Empty means all domains.
	EnabledDomains []Domain
}

// DefaultConfig returns a sensible default configuration.
func DefaultConfig() Config {
	return Config{
		ScanInterval:         30 * time.Second,
		MinConfidence:        0.55,
		MaxActivePredictions: 20,
		AutoActThreshold:     0.90,
		CollectiveImmunity:   true,
		EnabledDomains: []Domain{
			DomainSecurity,
			DomainFinancial,
			DomainHR,
			DomainMarketing,
			DomainProduct,
			DomainOperational,
		},
	}
}

// Engine is the JARV Foresight Engine.
// It orchestrates the OODA loop across all domains, correlates signals,
// matches patterns, runs simulations, and delivers predictive alerts.
type Engine struct {
	cfg       Config
	sources   []SignalSource
	patterns  PatternStore
	preds     PredictionStore
	simulator Simulator
	notifiers []Notifier

	// In-memory signal buffer — recent signals for correlation.
	mu      sync.RWMutex
	signals []Signal

	// Collective immunity — shared threat patterns.
	immunityMu sync.RWMutex
	immunity   []Pattern

	cancel context.CancelFunc
}

// New creates a new Foresight Engine with the given configuration.
func New(
	cfg Config,
	patterns PatternStore,
	preds PredictionStore,
	simulator Simulator,
) *Engine {
	return &Engine{
		cfg:       cfg,
		patterns:  patterns,
		preds:     preds,
		simulator: simulator,
		signals:   make([]Signal, 0, 1000),
		immunity:  make([]Pattern, 0, 100),
	}
}

// RegisterSource adds a signal source to be monitored.
func (e *Engine) RegisterSource(s SignalSource) {
	e.sources = append(e.sources, s)
}

// RegisterNotifier adds a notification channel for alerts.
func (e *Engine) RegisterNotifier(n Notifier) {
	e.notifiers = append(e.notifiers, n)
}

// Start begins the continuous OODA loop in the background.
func (e *Engine) Start(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	e.cancel = cancel

	go e.loop(ctx)

	if e.cfg.CollectiveImmunity {
		go e.immunitySync(ctx)
	}
}

// Stop gracefully shuts down the Foresight Engine.
func (e *Engine) Stop() {
	if e.cancel != nil {
		e.cancel()
	}
}

// Ingest allows external components to push signals directly into the engine.
// This is the primary integration point for the Shield, Memory, and other modules.
func (e *Engine) Ingest(s Signal) {
	e.mu.Lock()
	defer e.mu.Unlock()

	s.Timestamp = time.Now()
	e.signals = append(e.signals, s)

	// Keep only the last 10,000 signals to bound memory usage.
	if len(e.signals) > 10000 {
		e.signals = e.signals[len(e.signals)-10000:]
	}
}

// ActivePredictions returns all active predictions for a given user.
// Pass empty string for system-wide predictions.
func (e *Engine) ActivePredictions(ctx context.Context, userID string) ([]Prediction, error) {
	return e.preds.Active(ctx, userID)
}

// DismissPrediction marks a prediction as dismissed by the user.
func (e *Engine) DismissPrediction(ctx context.Context, id string) error {
	return e.preds.UpdateStatus(ctx, id, StatusDismissed)
}

// ─────────────────────────────────────────────────────────────────────────────
// OODA Loop
// ─────────────────────────────────────────────────────────────────────────────

// loop is the main OODA loop. It runs continuously at the configured interval.
func (e *Engine) loop(ctx context.Context) {
	ticker := time.NewTicker(e.cfg.ScanInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			e.ooda(ctx)
		}
	}
}

// ooda executes one full OODA cycle: Observe → Orient → Decide → Act.
func (e *Engine) ooda(ctx context.Context) {
	// ── OBSERVE ──────────────────────────────────────────────────────────────
	// Collect fresh signals from all registered sources.
	fresh := e.observe(ctx)

	// ── ORIENT ───────────────────────────────────────────────────────────────
	// Correlate signals with known patterns. Group by domain.
	matches := e.orient(ctx, fresh)

	// ── DECIDE ───────────────────────────────────────────────────────────────
	// For each domain with pattern matches, run archetypal simulation
	// to generate concrete predictions.
	predictions := e.decide(ctx, matches)

	// ── ACT ───────────────────────────────────────────────────────────────────
	// Persist predictions, notify users, and execute automated actions.
	e.act(ctx, predictions)
}

// observe collects signals from all sources and merges with the in-memory buffer.
func (e *Engine) observe(ctx context.Context) []Signal {
	var wg sync.WaitGroup
	var mu sync.Mutex
	var fresh []Signal

	for _, src := range e.sources {
		if !e.isDomainEnabled(src.Domain()) {
			continue
		}

		wg.Add(1)
		go func(s SignalSource) {
			defer wg.Done()
			signals, err := s.Collect(ctx)
			if err != nil {
				return
			}
			mu.Lock()
			fresh = append(fresh, signals...)
			mu.Unlock()
		}(src)
	}

	wg.Wait()

	// Ingest fresh signals into the buffer.
	for _, s := range fresh {
		e.Ingest(s)
	}

	// Return recent signals from buffer for correlation.
	e.mu.RLock()
	defer e.mu.RUnlock()

	// Use signals from the last 5 minutes for pattern matching.
	cutoff := time.Now().Add(-5 * time.Minute)
	var recent []Signal
	for _, s := range e.signals {
		if s.Timestamp.After(cutoff) {
			recent = append(recent, s)
		}
	}
	return recent
}

// PatternMatch represents a pattern that matched against observed signals.
type PatternMatch struct {
	Pattern    Pattern
	Signals    []Signal
	Domain     Domain
	Confidence float64
}

// orient correlates observed signals with known patterns.
// Returns pattern matches grouped by domain.
func (e *Engine) orient(ctx context.Context, signals []Signal) []PatternMatch {
	// Load all patterns (local + collective immunity).
	allPatterns, err := e.patterns.AllPatterns(ctx)
	if err != nil {
		return nil
	}

	// Add collective immunity patterns.
	e.immunityMu.RLock()
	allPatterns = append(allPatterns, e.immunity...)
	e.immunityMu.RUnlock()

	var matches []PatternMatch

	for _, pattern := range allPatterns {
		if !e.isDomainEnabled(pattern.Domain) {
			continue
		}

		matched, matchedSignals := e.matchPattern(pattern, signals)
		if !matched {
			continue
		}

		// Calculate dynamic confidence based on historical accuracy and signal strength.
		confidence := e.calculateConfidence(pattern, matchedSignals)
		if confidence < e.cfg.MinConfidence {
			continue
		}

		matches = append(matches, PatternMatch{
			Pattern:    pattern,
			Signals:    matchedSignals,
			Domain:     pattern.Domain,
			Confidence: confidence,
		})
	}

	// Sort by confidence descending.
	sort.Slice(matches, func(i, j int) bool {
		return matches[i].Confidence > matches[j].Confidence
	})

	return matches
}

// matchPattern checks if a pattern's required signals are present in the observed set.
func (e *Engine) matchPattern(pattern Pattern, signals []Signal) (bool, []Signal) {
	signalsByType := make(map[string][]Signal)
	for _, s := range signals {
		signalsByType[s.EventType] = append(signalsByType[s.EventType], s)
	}

	var matched []Signal
	for _, required := range pattern.Signals {
		sigs, ok := signalsByType[required]
		if !ok {
			return false, nil
		}
		matched = append(matched, sigs...)
	}

	return true, matched
}

// calculateConfidence computes a dynamic confidence score for a pattern match.
// It combines the pattern's historical accuracy with the signal strength.
func (e *Engine) calculateConfidence(pattern Pattern, signals []Signal) float64 {
	// Historical accuracy component (0.0–1.0).
	var historicalAccuracy float64
	if pattern.Occurrences > 0 {
		historicalAccuracy = float64(pattern.Confirmed) / float64(pattern.Occurrences)
	} else {
		// New pattern — use base confidence.
		historicalAccuracy = pattern.Confidence
	}

	// Signal strength component — more signals = higher confidence.
	signalStrength := math.Min(float64(len(signals))/10.0, 1.0)

	// Recency component — more recent signals = higher confidence.
	var recencyScore float64
	for _, s := range signals {
		age := time.Since(s.Timestamp).Seconds()
		recencyScore += math.Exp(-age / 120) // Exponential decay over 2 minutes.
	}
	if len(signals) > 0 {
		recencyScore /= float64(len(signals))
	}

	// Weighted combination.
	confidence := (historicalAccuracy * 0.5) + (signalStrength * 0.3) + (recencyScore * 0.2)
	return math.Min(confidence, 0.99) // Cap at 99% — never 100% certain.
}

// decide runs archetypal simulations for each pattern match to generate predictions.
func (e *Engine) decide(ctx context.Context, matches []PatternMatch) []Prediction {
	if len(matches) == 0 {
		return nil
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	var predictions []Prediction

	// Group matches by domain to run one simulation per domain.
	byDomain := make(map[Domain][]PatternMatch)
	for _, m := range matches {
		byDomain[m.Domain] = append(byDomain[m.Domain], m)
	}

	for domain, domainMatches := range byDomain {
		wg.Add(1)
		go func(d Domain, dm []PatternMatch) {
			defer wg.Done()

			// Collect all signals and patterns for this domain.
			var signals []Signal
			var patterns []Pattern
			for _, m := range dm {
				signals = append(signals, m.Signals...)
				patterns = append(patterns, m.Pattern)
			}

			// Run archetypal simulation.
			preds, err := e.simulator.SimulateScenario(ctx, d, signals, patterns)
			if err != nil {
				return
			}

			// Filter by minimum confidence.
			for _, p := range preds {
				if p.Confidence >= e.cfg.MinConfidence {
					mu.Lock()
					predictions = append(predictions, p)
					mu.Unlock()
				}
			}
		}(domain, domainMatches)
	}

	wg.Wait()

	// Sort by severity and confidence.
	sort.Slice(predictions, func(i, j int) bool {
		si := severityScore(predictions[i].Severity)
		sj := severityScore(predictions[j].Severity)
		if si != sj {
			return si > sj
		}
		return predictions[i].Confidence > predictions[j].Confidence
	})

	return predictions
}

// act persists predictions, sends notifications, and executes automated actions.
func (e *Engine) act(ctx context.Context, predictions []Prediction) {
	for _, p := range predictions {
		// Persist the prediction.
		if err := e.preds.Save(ctx, p); err != nil {
			continue
		}

		// Notify through all registered channels.
		for _, n := range e.notifiers {
			_ = n.Notify(ctx, p)
		}

		// Execute automated actions for high-confidence critical predictions.
		if p.Confidence >= e.cfg.AutoActThreshold {
			e.executeAutomatedActions(ctx, p)
		}

		// If this is a security prediction, contribute to collective immunity.
		if p.Domain == DomainSecurity && e.cfg.CollectiveImmunity && p.MatchedPattern != nil {
			e.contributeToImmunity(ctx, *p.MatchedPattern)
		}
	}
}

// executeAutomatedActions runs automated responses for high-confidence predictions.
func (e *Engine) executeAutomatedActions(ctx context.Context, p Prediction) {
	for _, action := range p.RecommendedActs {
		if !action.Automated || action.AutoCommand == "" {
			continue
		}
		// Commands are dispatched to the Shield or other modules via internal bus.
		// The actual execution is handled by the respective module.
		_ = e.dispatchCommand(ctx, action.AutoCommand, p)
	}
}

// dispatchCommand sends an internal command to the appropriate module.
func (e *Engine) dispatchCommand(_ context.Context, cmd string, p Prediction) error {
	// In production, this dispatches to the internal command bus.
	// For now, it logs the automated action.
	_ = fmt.Sprintf("[FORESIGHT AUTO-ACT] domain=%s severity=%s cmd=%s confidence=%.2f",
		p.Domain, p.Severity, cmd, p.Confidence)
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Collective Immunity
// ─────────────────────────────────────────────────────────────────────────────

// immunitySync periodically syncs security patterns with the collective network.
func (e *Engine) immunitySync(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			e.syncImmunity(ctx)
		}
	}
}

// syncImmunity fetches new threat patterns from the collective network
// and contributes local patterns back.
func (e *Engine) syncImmunity(_ context.Context) {
	if e.cfg.CollectiveImmunityEndpoint == "" {
		return
	}
	// In production, this performs an HTTP exchange with the JARV collective network.
	// Patterns are signed with HMAC-SHA256 to prevent poisoning attacks.
}

// contributeToImmunity adds a confirmed security pattern to the collective network.
func (e *Engine) contributeToImmunity(_ context.Context, p Pattern) {
	e.immunityMu.Lock()
	defer e.immunityMu.Unlock()

	// Check if pattern already exists.
	for i, existing := range e.immunity {
		if existing.ID == p.ID {
			e.immunity[i] = p
			return
		}
	}
	e.immunity = append(e.immunity, p)
}

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

func (e *Engine) isDomainEnabled(d Domain) bool {
	if len(e.cfg.EnabledDomains) == 0 {
		return true
	}
	for _, enabled := range e.cfg.EnabledDomains {
		if enabled == d {
			return true
		}
	}
	return false
}

func severityScore(s Severity) int {
	switch s {
	case SeverityCritical:
		return 5
	case SeverityHigh:
		return 4
	case SeverityMedium:
		return 3
	case SeverityLow:
		return 2
	case SeverityInfo:
		return 1
	default:
		return 0
	}
}
