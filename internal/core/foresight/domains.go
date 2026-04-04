// domains.go — The 6 Predictive Domain Analyzers for JARV Foresight.
//
// Each domain analyzer is a SignalSource that collects domain-specific signals
// and knows the archetypal patterns relevant to its area.
//
// Domains: Security · Financial · HR · Marketing · Product · Operational
package foresight

import (
	"context"
	"fmt"
	"math/rand"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// Security Domain
// ─────────────────────────────────────────────────────────────────────────────

// SecurityAnalyzer monitors for threats, attacks, and anomalies.
// It integrates directly with the Shield module's event stream.
type SecurityAnalyzer struct {
	shieldEvents chan Signal // Receives events from the Shield in real-time.
	buffer       []Signal
	mu           chan struct{}
}

func NewSecurityAnalyzer(shieldEvents chan Signal) *SecurityAnalyzer {
	return &SecurityAnalyzer{
		shieldEvents: shieldEvents,
		mu:           make(chan struct{}, 1),
	}
}

func (a *SecurityAnalyzer) Name() string   { return "security.analyzer" }
func (a *SecurityAnalyzer) Domain() Domain { return DomainSecurity }

func (a *SecurityAnalyzer) Collect(_ context.Context) ([]Signal, error) {
	// Drain the Shield event channel into the buffer.
	for {
		select {
		case s := <-a.shieldEvents:
			a.buffer = append(a.buffer, s)
		default:
			goto done
		}
	}
done:
	signals := a.buffer
	a.buffer = nil
	return signals, nil
}

// SecurityPatterns returns the built-in security threat patterns.
// These are loaded into the PatternStore on first run.
func SecurityPatterns() []Pattern {
	return []Pattern{
		{
			ID:          "sec-001",
			Domain:      DomainSecurity,
			Name:        "Coordinated Prompt Injection",
			Description: "Multiple prompt injection attempts with similar structure from different sources, indicating a coordinated attack.",
			Signals:     []string{"prompt_injection_attempt", "rate_limit_exceeded"},
			Confidence:  0.75,
			LeadTime:    15 * time.Minute,
			Occurrences: 0,
			Confirmed:   0,
		},
		{
			ID:          "sec-002",
			Domain:      DomainSecurity,
			Name:        "Credential Harvesting Attempt",
			Description: "Repeated queries probing for API keys, passwords, or sensitive configuration data.",
			Signals:     []string{"credential_probe", "sensitive_query"},
			Confidence:  0.80,
			LeadTime:    5 * time.Minute,
			Occurrences: 0,
			Confirmed:   0,
		},
		{
			ID:          "sec-003",
			Domain:      DomainSecurity,
			Name:        "Jailbreak Escalation",
			Description: "Progressive jailbreak attempts becoming more sophisticated, suggesting a skilled attacker.",
			Signals:     []string{"jailbreak_attempt", "role_confusion_attempt"},
			Confidence:  0.70,
			LeadTime:    10 * time.Minute,
			Occurrences: 0,
			Confirmed:   0,
		},
		{
			ID:          "sec-004",
			Domain:      DomainSecurity,
			Name:        "Data Exfiltration Pattern",
			Description: "Unusual volume of data retrieval requests suggesting an attempt to extract memory contents.",
			Signals:     []string{"bulk_memory_query", "unusual_export_request"},
			Confidence:  0.85,
			LeadTime:    20 * time.Minute,
			Occurrences: 0,
			Confirmed:   0,
		},
		{
			ID:          "sec-005",
			Domain:      DomainSecurity,
			Name:        "Insider Threat Indicator",
			Description: "Authorized user exhibiting access patterns inconsistent with their normal behavior.",
			Signals:     []string{"off_hours_access", "unusual_permission_request", "bulk_data_access"},
			Confidence:  0.65,
			LeadTime:    60 * time.Minute,
			Occurrences: 0,
			Confirmed:   0,
		},
	}
}

// SecurityArchetypes returns the attacker archetypes used in security simulations.
func SecurityArchetypes() []Archetype {
	return []Archetype{
		{
			Name:        "Script Kiddie",
			Role:        "Automated Attacker",
			Motivation:  "Opportunistic — using off-the-shelf tools, no specific target",
			Behavior:    "High volume, low sophistication, easily blocked by rate limiting",
			Probability: 0.50,
		},
		{
			Name:        "Professional Hacker",
			Role:        "Targeted Attacker",
			Motivation:  "Financial gain or espionage — specifically targeting this system",
			Behavior:    "Low volume, high sophistication, adapts to defenses",
			Probability: 0.30,
		},
		{
			Name:        "Malicious Insider",
			Role:        "Internal Threat",
			Motivation:  "Revenge, financial gain, or coercion",
			Behavior:    "Uses legitimate credentials, operates during normal hours to blend in",
			Probability: 0.20,
		},
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Financial Domain
// ─────────────────────────────────────────────────────────────────────────────

// FinancialAnalyzer monitors financial signals from connected data sources.
type FinancialAnalyzer struct {
	metrics FinancialMetrics
}

// FinancialMetrics holds the current financial state of the user's business.
// Populated by the user's connected data sources (spreadsheets, accounting tools).
type FinancialMetrics struct {
	MonthlyCashFlow     float64
	CashFlowTrend       float64 // Positive = growing, negative = declining
	AccountsReceivable  float64
	BurnRate            float64
	RevenueGrowthRate   float64
	ChurnRate           float64
	CustomerAcquisition float64
}

func NewFinancialAnalyzer(metrics FinancialMetrics) *FinancialAnalyzer {
	return &FinancialAnalyzer{metrics: metrics}
}

func (a *FinancialAnalyzer) Name() string   { return "financial.analyzer" }
func (a *FinancialAnalyzer) Domain() Domain { return DomainFinancial }

func (a *FinancialAnalyzer) Collect(_ context.Context) ([]Signal, error) {
	var signals []Signal
	now := time.Now()

	// Emit signals based on metric thresholds.
	if a.metrics.CashFlowTrend < -0.15 {
		signals = append(signals, Signal{
			Domain:    DomainFinancial,
			Source:    "financial.analyzer",
			EventType: "cash_flow_declining",
			Value:     a.metrics.CashFlowTrend,
			Metadata:  map[string]string{"trend": "negative", "severity": "high"},
			Timestamp: now,
		})
	}

	if a.metrics.ChurnRate > 0.05 {
		signals = append(signals, Signal{
			Domain:    DomainFinancial,
			Source:    "financial.analyzer",
			EventType: "churn_rate_elevated",
			Value:     a.metrics.ChurnRate,
			Metadata:  map[string]string{"threshold": "0.05"},
			Timestamp: now,
		})
	}

	if a.metrics.BurnRate > 0 && a.metrics.MonthlyCashFlow > 0 {
		runway := a.metrics.MonthlyCashFlow / a.metrics.BurnRate
		if runway < 6 {
			signals = append(signals, Signal{
				Domain:    DomainFinancial,
				Source:    "financial.analyzer",
				EventType: "runway_critical",
				Value:     runway,
				Metadata:  map[string]string{"months_remaining": fmt.Sprintf("%.1f months", runway)},
				Timestamp: now,
			})
		}
	}

	return signals, nil
}

// FinancialPatterns returns built-in financial risk patterns.
func FinancialPatterns() []Pattern {
	return []Pattern{
		{
			ID:          "fin-001",
			Domain:      DomainFinancial,
			Name:        "Cash Flow Crisis Approaching",
			Description: "Declining cash flow combined with elevated burn rate suggests a cash crisis within 90 days.",
			Signals:     []string{"cash_flow_declining", "runway_critical"},
			Confidence:  0.80,
			LeadTime:    90 * 24 * time.Hour,
		},
		{
			ID:          "fin-002",
			Domain:      DomainFinancial,
			Name:        "Churn Acceleration",
			Description: "Rising churn rate combined with declining acquisition suggests revenue contraction.",
			Signals:     []string{"churn_rate_elevated", "acquisition_declining"},
			Confidence:  0.75,
			LeadTime:    60 * 24 * time.Hour,
		},
		{
			ID:          "fin-003",
			Domain:      DomainFinancial,
			Name:        "Revenue Growth Opportunity",
			Description: "Strong acquisition metrics with low churn suggests optimal timing for pricing increase.",
			Signals:     []string{"acquisition_accelerating", "churn_rate_low"},
			Confidence:  0.70,
			LeadTime:    30 * 24 * time.Hour,
		},
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// HR Domain
// ─────────────────────────────────────────────────────────────────────────────

// HRAnalyzer monitors team health and engagement signals.
type HRAnalyzer struct {
	teamSignals []Signal
}

func NewHRAnalyzer() *HRAnalyzer {
	return &HRAnalyzer{}
}

func (a *HRAnalyzer) Name() string   { return "hr.analyzer" }
func (a *HRAnalyzer) Domain() Domain { return DomainHR }

func (a *HRAnalyzer) Collect(_ context.Context) ([]Signal, error) {
	signals := a.teamSignals
	a.teamSignals = nil
	return signals, nil
}

// IngestHRSignal allows the HR module to push team health signals.
func (a *HRAnalyzer) IngestHRSignal(eventType string, value float64, meta map[string]string) {
	a.teamSignals = append(a.teamSignals, Signal{
		Domain:    DomainHR,
		Source:    "hr.analyzer",
		EventType: eventType,
		Value:     value,
		Metadata:  meta,
		Timestamp: time.Now(),
	})
}

// HRPatterns returns built-in team health patterns.
func HRPatterns() []Pattern {
	return []Pattern{
		{
			ID:          "hr-001",
			Domain:      DomainHR,
			Name:        "Flight Risk — Key Employee",
			Description: "Multiple disengagement indicators suggest a key team member may be considering leaving.",
			Signals:     []string{"engagement_drop", "productivity_decline", "communication_reduction"},
			Confidence:  0.70,
			LeadTime:    45 * 24 * time.Hour,
		},
		{
			ID:          "hr-002",
			Domain:      DomainHR,
			Name:        "Team Burnout Emerging",
			Description: "Sustained overwork patterns across multiple team members indicate burnout risk.",
			Signals:     []string{"overtime_sustained", "task_completion_declining", "error_rate_rising"},
			Confidence:  0.75,
			LeadTime:    30 * 24 * time.Hour,
		},
		{
			ID:          "hr-003",
			Domain:      DomainHR,
			Name:        "High Performance Window",
			Description: "Team engagement and productivity metrics are at peak — optimal time for ambitious goals.",
			Signals:     []string{"engagement_high", "productivity_peak", "collaboration_strong"},
			Confidence:  0.80,
			LeadTime:    14 * 24 * time.Hour,
		},
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Marketing Domain
// ─────────────────────────────────────────────────────────────────────────────

// MarketingAnalyzer monitors audience engagement and campaign performance signals.
type MarketingAnalyzer struct {
	campaignSignals []Signal
}

func NewMarketingAnalyzer() *MarketingAnalyzer {
	return &MarketingAnalyzer{}
}

func (a *MarketingAnalyzer) Name() string   { return "marketing.analyzer" }
func (a *MarketingAnalyzer) Domain() Domain { return DomainMarketing }

func (a *MarketingAnalyzer) Collect(_ context.Context) ([]Signal, error) {
	signals := a.campaignSignals
	a.campaignSignals = nil
	return signals, nil
}

// IngestMarketingSignal allows marketing tools to push performance signals.
func (a *MarketingAnalyzer) IngestMarketingSignal(eventType string, value float64, meta map[string]string) {
	a.campaignSignals = append(a.campaignSignals, Signal{
		Domain:    DomainMarketing,
		Source:    "marketing.analyzer",
		EventType: eventType,
		Value:     value,
		Metadata:  meta,
		Timestamp: time.Now(),
	})
}

// MarketingPatterns returns built-in marketing opportunity patterns.
func MarketingPatterns() []Pattern {
	return []Pattern{
		{
			ID:          "mkt-001",
			Domain:      DomainMarketing,
			Name:        "Viral Moment Window",
			Description: "Engagement velocity and topic resonance suggest an upcoming viral opportunity.",
			Signals:     []string{"engagement_velocity_high", "topic_trending", "share_rate_rising"},
			Confidence:  0.65,
			LeadTime:    48 * time.Hour,
		},
		{
			ID:          "mkt-002",
			Domain:      DomainMarketing,
			Name:        "Audience Fatigue",
			Description: "Declining open rates and engagement suggest content fatigue — time to change strategy.",
			Signals:     []string{"open_rate_declining", "engagement_dropping", "unsubscribe_rising"},
			Confidence:  0.75,
			LeadTime:    21 * 24 * time.Hour,
		},
		{
			ID:          "mkt-003",
			Domain:      DomainMarketing,
			Name:        "Optimal Launch Window",
			Description: "Audience engagement peaks and competitor activity is low — ideal for product launch.",
			Signals:     []string{"audience_engagement_peak", "competitor_activity_low", "sentiment_positive"},
			Confidence:  0.72,
			LeadTime:    7 * 24 * time.Hour,
		},
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Product Domain
// ─────────────────────────────────────────────────────────────────────────────

// ProductAnalyzer monitors product usage, feature adoption, and user behavior.
type ProductAnalyzer struct {
	usageSignals []Signal
}

func NewProductAnalyzer() *ProductAnalyzer {
	return &ProductAnalyzer{}
}

func (a *ProductAnalyzer) Name() string   { return "product.analyzer" }
func (a *ProductAnalyzer) Domain() Domain { return DomainProduct }

func (a *ProductAnalyzer) Collect(_ context.Context) ([]Signal, error) {
	signals := a.usageSignals
	a.usageSignals = nil
	return signals, nil
}

// IngestUsageSignal allows the dashboard to push product usage signals.
func (a *ProductAnalyzer) IngestUsageSignal(eventType string, value float64, meta map[string]string) {
	a.usageSignals = append(a.usageSignals, Signal{
		Domain:    DomainProduct,
		Source:    "product.analyzer",
		EventType: eventType,
		Value:     value,
		Metadata:  meta,
		Timestamp: time.Now(),
	})
}

// ProductPatterns returns built-in product health patterns.
func ProductPatterns() []Pattern {
	return []Pattern{
		{
			ID:          "prd-001",
			Domain:      DomainProduct,
			Name:        "Feature Abandonment",
			Description: "Users are consistently dropping off at a specific feature, indicating UX friction or value gap.",
			Signals:     []string{"feature_drop_off", "session_abandonment", "error_at_feature"},
			Confidence:  0.78,
			LeadTime:    14 * 24 * time.Hour,
		},
		{
			ID:          "prd-002",
			Domain:      DomainProduct,
			Name:        "Power User Emergence",
			Description: "A cohort of users is adopting advanced features rapidly — potential advocates and case studies.",
			Signals:     []string{"advanced_feature_usage", "session_depth_high", "return_frequency_high"},
			Confidence:  0.80,
			LeadTime:    7 * 24 * time.Hour,
		},
		{
			ID:          "prd-003",
			Domain:      DomainProduct,
			Name:        "Pre-Churn Behavior",
			Description: "Usage patterns matching historical pre-churn profiles — intervention window is open.",
			Signals:     []string{"session_frequency_declining", "feature_breadth_narrowing", "support_contact_recent"},
			Confidence:  0.73,
			LeadTime:    30 * 24 * time.Hour,
		},
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Operational Domain
// ─────────────────────────────────────────────────────────────────────────────

// OperationalAnalyzer monitors system health, performance, and infrastructure signals.
type OperationalAnalyzer struct {
	systemMetrics SystemMetrics
}

// SystemMetrics holds real-time system performance data.
type SystemMetrics struct {
	CPUUsagePercent    float64
	MemoryUsagePercent float64
	RequestLatencyP99  time.Duration
	ErrorRatePercent   float64
	QueueDepth         int
	DiskUsagePercent   float64
}

func NewOperationalAnalyzer(metrics SystemMetrics) *OperationalAnalyzer {
	return &OperationalAnalyzer{systemMetrics: metrics}
}

func (a *OperationalAnalyzer) Name() string   { return "operational.analyzer" }
func (a *OperationalAnalyzer) Domain() Domain { return DomainOperational }

func (a *OperationalAnalyzer) Collect(_ context.Context) ([]Signal, error) {
	var signals []Signal
	now := time.Now()

	if a.systemMetrics.CPUUsagePercent > 80 {
		signals = append(signals, Signal{
			Domain:    DomainOperational,
			Source:    "operational.analyzer",
			EventType: "cpu_high",
			Value:     a.systemMetrics.CPUUsagePercent,
			Timestamp: now,
		})
	}

	if a.systemMetrics.MemoryUsagePercent > 85 {
		signals = append(signals, Signal{
			Domain:    DomainOperational,
			Source:    "operational.analyzer",
			EventType: "memory_pressure",
			Value:     a.systemMetrics.MemoryUsagePercent,
			Timestamp: now,
		})
	}

	if a.systemMetrics.ErrorRatePercent > 1.0 {
		signals = append(signals, Signal{
			Domain:    DomainOperational,
			Source:    "operational.analyzer",
			EventType: "error_rate_elevated",
			Value:     a.systemMetrics.ErrorRatePercent,
			Timestamp: now,
		})
	}

	if a.systemMetrics.RequestLatencyP99 > 2*time.Second {
		signals = append(signals, Signal{
			Domain:    DomainOperational,
			Source:    "operational.analyzer",
			EventType: "latency_degraded",
			Value:     float64(a.systemMetrics.RequestLatencyP99.Milliseconds()),
			Timestamp: now,
		})
	}

	if a.systemMetrics.DiskUsagePercent > 90 {
		signals = append(signals, Signal{
			Domain:    DomainOperational,
			Source:    "operational.analyzer",
			EventType: "disk_critical",
			Value:     a.systemMetrics.DiskUsagePercent,
			Timestamp: now,
		})
	}

	return signals, nil
}

// OperationalPatterns returns built-in infrastructure failure patterns.
func OperationalPatterns() []Pattern {
	return []Pattern{
		{
			ID:          "ops-001",
			Domain:      DomainOperational,
			Name:        "Cascading Failure Risk",
			Description: "High CPU combined with memory pressure and elevated error rates — system approaching failure threshold.",
			Signals:     []string{"cpu_high", "memory_pressure", "error_rate_elevated"},
			Confidence:  0.85,
			LeadTime:    30 * time.Minute,
		},
		{
			ID:          "ops-002",
			Domain:      DomainOperational,
			Name:        "Traffic Spike Incoming",
			Description: "Request queue depth growing faster than processing rate — scale up recommended.",
			Signals:     []string{"queue_depth_growing", "latency_degraded"},
			Confidence:  0.80,
			LeadTime:    15 * time.Minute,
		},
		{
			ID:          "ops-003",
			Domain:      DomainOperational,
			Name:        "Storage Exhaustion",
			Description: "Disk usage trajectory will hit 100% within 7 days at current growth rate.",
			Signals:     []string{"disk_critical", "storage_growth_rate_high"},
			Confidence:  0.90,
			LeadTime:    7 * 24 * time.Hour,
		},
	}
}

// AllBuiltInPatterns returns all built-in patterns across all domains.
// Used to seed the PatternStore on first run.
func AllBuiltInPatterns() []Pattern {
	var all []Pattern
	all = append(all, SecurityPatterns()...)
	all = append(all, FinancialPatterns()...)
	all = append(all, HRPatterns()...)
	all = append(all, MarketingPatterns()...)
	all = append(all, ProductPatterns()...)
	all = append(all, OperationalPatterns()...)
	return all
}

// Ensure rand is used to avoid import error.
var _ = rand.Float64
