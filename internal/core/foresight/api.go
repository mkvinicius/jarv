// api.go — HTTP API and Dashboard Integration for JARV Foresight.
//
// Exposes the Foresight Engine through a clean REST API and Server-Sent Events
// stream for real-time dashboard updates. The dashboard's Foresight panel
// subscribes to the SSE stream and receives live prediction alerts.
//
// Endpoints:
//
//	GET  /api/foresight/predictions          — Active predictions for current user
//	GET  /api/foresight/predictions/history  — Historical predictions
//	POST /api/foresight/predictions/:id/dismiss — Dismiss a prediction
//	POST /api/foresight/signals              — Ingest a signal from external source
//	GET  /api/foresight/stream               — SSE stream for live alerts
//	GET  /api/foresight/domains              — Domain health summary
//	GET  /api/foresight/immunity             — Collective immunity status
package foresight

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// API Handler
// ─────────────────────────────────────────────────────────────────────────────

// APIHandler exposes the Foresight Engine through HTTP.
type APIHandler struct {
	engine  *Engine
	network *ImmunityNetwork

	// SSE subscribers — each is a channel that receives prediction JSON.
	subsMu      sync.RWMutex
	subscribers map[string]chan []byte // key = subscriber ID
}

// NewAPIHandler creates a new Foresight API handler.
func NewAPIHandler(engine *Engine, network *ImmunityNetwork) *APIHandler {
	h := &APIHandler{
		engine:      engine,
		network:     network,
		subscribers: make(map[string]chan []byte),
	}

	// Register the API handler as a notifier so predictions flow to SSE clients.
	engine.RegisterNotifier(h)

	return h
}

// RegisterRoutes registers all Foresight API routes on the given mux.
func (h *APIHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/foresight/predictions", h.handlePredictions)
	mux.HandleFunc("/api/foresight/predictions/history", h.handleHistory)
	mux.HandleFunc("/api/foresight/predictions/dismiss", h.handleDismiss)
	mux.HandleFunc("/api/foresight/signals", h.handleIngestSignal)
	mux.HandleFunc("/api/foresight/stream", h.handleSSEStream)
	mux.HandleFunc("/api/foresight/domains", h.handleDomainSummary)
	mux.HandleFunc("/api/foresight/immunity", h.handleImmunityStatus)
}

// ─────────────────────────────────────────────────────────────────────────────
// HTTP Handlers
// ─────────────────────────────────────────────────────────────────────────────

// handlePredictions returns all active predictions for the current user.
func (h *APIHandler) handlePredictions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	userID := r.Header.Get("X-User-ID")
	predictions, err := h.engine.ActivePredictions(r.Context(), userID)
	if err != nil {
		http.Error(w, "failed to fetch predictions", http.StatusInternalServerError)
		return
	}

	// Build dashboard-friendly response.
	response := PredictionsResponse{
		Predictions: toDashboardPredictions(predictions),
		Summary:     buildSummary(predictions),
		GeneratedAt: time.Now(),
	}

	writeJSON(w, response)
}

// handleHistory returns historical predictions for the current user.
func (h *APIHandler) handleHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	userID := r.Header.Get("X-User-ID")
	predictions, err := h.engine.preds.History(r.Context(), userID, 50)
	if err != nil {
		http.Error(w, "failed to fetch history", http.StatusInternalServerError)
		return
	}

	writeJSON(w, map[string]interface{}{
		"predictions": toDashboardPredictions(predictions),
		"total":       len(predictions),
	})
}

// handleDismiss marks a prediction as dismissed.
func (h *APIHandler) handleDismiss(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if err := h.engine.DismissPrediction(r.Context(), req.ID); err != nil {
		http.Error(w, "failed to dismiss prediction", http.StatusInternalServerError)
		return
	}

	writeJSON(w, map[string]string{"status": "dismissed"})
}

// handleIngestSignal accepts a signal from an external source (e.g., a webhook).
func (h *APIHandler) handleIngestSignal(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req SignalIngestionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	signal := Signal{
		Domain:    Domain(req.Domain),
		Source:    req.Source,
		EventType: req.EventType,
		Value:     req.Value,
		Metadata:  req.Metadata,
		Timestamp: time.Now(),
	}

	h.engine.Ingest(signal)
	writeJSON(w, map[string]string{"status": "ingested"})
}

// handleSSEStream opens a Server-Sent Events stream for real-time prediction alerts.
// The dashboard subscribes to this endpoint to receive live Foresight notifications.
func (h *APIHandler) handleSSEStream(w http.ResponseWriter, r *http.Request) {
	// Set SSE headers.
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	// Register subscriber.
	subID := fmt.Sprintf("sub-%d", time.Now().UnixNano())
	ch := make(chan []byte, 50)

	h.subsMu.Lock()
	h.subscribers[subID] = ch
	h.subsMu.Unlock()

	defer func() {
		h.subsMu.Lock()
		delete(h.subscribers, subID)
		h.subsMu.Unlock()
		close(ch)
	}()

	// Send initial connection confirmation.
	fmt.Fprintf(w, "event: connected\ndata: {\"status\":\"foresight_stream_active\"}\n\n")
	flusher.Flush()

	// Stream predictions as they arrive.
	for {
		select {
		case <-r.Context().Done():
			return
		case data, ok := <-ch:
			if !ok {
				return
			}
			fmt.Fprintf(w, "event: prediction\ndata: %s\n\n", data)
			flusher.Flush()
		case <-time.After(30 * time.Second):
			// Heartbeat to keep connection alive.
			fmt.Fprintf(w, "event: heartbeat\ndata: {\"ts\":%d}\n\n", time.Now().Unix())
			flusher.Flush()
		}
	}
}

// handleDomainSummary returns a health summary for all monitored domains.
func (h *APIHandler) handleDomainSummary(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	userID := r.Header.Get("X-User-ID")
	predictions, err := h.engine.ActivePredictions(r.Context(), userID)
	if err != nil {
		http.Error(w, "failed to fetch predictions", http.StatusInternalServerError)
		return
	}

	summary := buildDomainSummary(predictions)
	writeJSON(w, summary)
}

// handleImmunityStatus returns the collective immunity network status.
func (h *APIHandler) handleImmunityStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	h.engine.immunityMu.RLock()
	immunityCount := len(h.engine.immunity)
	h.engine.immunityMu.RUnlock()

	h.network.mu.RLock()
	peerCount := len(h.network.peers)
	h.network.mu.RUnlock()

	writeJSON(w, map[string]interface{}{
		"active":          h.engine.cfg.CollectiveImmunity,
		"shared_patterns": immunityCount,
		"connected_peers": peerCount,
		"last_sync":       time.Now().Format(time.RFC3339),
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// Notifier Implementation (SSE broadcast)
// ─────────────────────────────────────────────────────────────────────────────

// Notify implements the Notifier interface. It broadcasts predictions to all SSE subscribers.
func (h *APIHandler) Notify(_ context.Context, p Prediction) error {
	dp := toDashboardPrediction(p)
	data, err := json.Marshal(dp)
	if err != nil {
		return err
	}

	h.subsMu.RLock()
	defer h.subsMu.RUnlock()

	for _, ch := range h.subscribers {
		select {
		case ch <- data:
		default:
			// Subscriber is slow — skip to avoid blocking.
		}
	}

	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Response Types (Dashboard-optimized)
// ─────────────────────────────────────────────────────────────────────────────

// DashboardPrediction is the dashboard-friendly representation of a prediction.
// Designed for the Foresight panel in the JARV dashboard UI.
type DashboardPrediction struct {
	ID              string               `json:"id"`
	Domain          string               `json:"domain"`
	DomainIcon      string               `json:"domain_icon"`
	Title           string               `json:"title"`
	Description     string               `json:"description"`
	Severity        string               `json:"severity"`
	SeverityColor   string               `json:"severity_color"`
	ConfidenceLabel string               `json:"confidence_label"`
	ConfidencePct   int                  `json:"confidence_pct"`
	TimeHorizon     string               `json:"time_horizon"`
	PredictedAt     string               `json:"predicted_at"`
	Evidence        []string             `json:"evidence"`
	Actions         []DashboardAction    `json:"actions"`
	Archetypes      []DashboardArchetype `json:"archetypes,omitempty"`
}

// DashboardAction is the dashboard-friendly representation of a recommended action.
type DashboardAction struct {
	Priority    int    `json:"priority"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Automated   bool   `json:"automated"`
	ButtonLabel string `json:"button_label"`
}

// DashboardArchetype is the dashboard-friendly representation of a simulation archetype.
type DashboardArchetype struct {
	Name        string `json:"name"`
	Role        string `json:"role"`
	Probability int    `json:"probability_pct"`
}

// PredictionsResponse is the full response for the predictions endpoint.
type PredictionsResponse struct {
	Predictions []DashboardPrediction `json:"predictions"`
	Summary     PredictionSummary     `json:"summary"`
	GeneratedAt time.Time             `json:"generated_at"`
}

// PredictionSummary provides aggregate statistics for the dashboard header.
type PredictionSummary struct {
	Total    int            `json:"total"`
	Critical int            `json:"critical"`
	High     int            `json:"high"`
	Medium   int            `json:"medium"`
	Low      int            `json:"low"`
	ByDomain map[string]int `json:"by_domain"`
}

// DomainHealthSummary provides a per-domain health overview for the dashboard.
type DomainHealthSummary struct {
	Domain      string `json:"domain"`
	Icon        string `json:"icon"`
	Status      string `json:"status"`       // "healthy" | "warning" | "critical"
	StatusColor string `json:"status_color"` // Hex color for UI
	AlertCount  int    `json:"alert_count"`
	TopAlert    string `json:"top_alert,omitempty"`
}

// SignalIngestionRequest is the request body for the signal ingestion endpoint.
type SignalIngestionRequest struct {
	Domain    string            `json:"domain"`
	Source    string            `json:"source"`
	EventType string            `json:"event_type"`
	Value     float64           `json:"value"`
	Metadata  map[string]string `json:"metadata,omitempty"`
}

// ─────────────────────────────────────────────────────────────────────────────
// Conversion Helpers
// ─────────────────────────────────────────────────────────────────────────────

func toDashboardPredictions(predictions []Prediction) []DashboardPrediction {
	result := make([]DashboardPrediction, 0, len(predictions))
	for _, p := range predictions {
		result = append(result, toDashboardPrediction(p))
	}
	return result
}

func toDashboardPrediction(p Prediction) DashboardPrediction {
	actions := make([]DashboardAction, 0, len(p.RecommendedActs))
	for _, a := range p.RecommendedActs {
		label := "Review"
		if a.Automated {
			label = "Apply Fix"
		}
		actions = append(actions, DashboardAction{
			Priority:    a.Priority,
			Title:       a.Title,
			Description: a.Description,
			Automated:   a.Automated,
			ButtonLabel: label,
		})
	}

	archetypes := make([]DashboardArchetype, 0, len(p.Archetypes))
	for _, a := range p.Archetypes {
		archetypes = append(archetypes, DashboardArchetype{
			Name:        a.Name,
			Role:        a.Role,
			Probability: int(a.Probability * 100),
		})
	}

	return DashboardPrediction{
		ID:              p.ID,
		Domain:          string(p.Domain),
		DomainIcon:      domainIcon(p.Domain),
		Title:           p.Title,
		Description:     p.Description,
		Severity:        string(p.Severity),
		SeverityColor:   severityColor(p.Severity),
		ConfidenceLabel: confidenceLabel(p.Confidence),
		ConfidencePct:   int(p.Confidence * 100),
		TimeHorizon:     formatDuration(p.TimeHorizon),
		PredictedAt:     p.PredictedAt.Format("02 Jan 2006 15:04"),
		Evidence:        p.Evidence,
		Actions:         actions,
		Archetypes:      archetypes,
	}
}

func buildSummary(predictions []Prediction) PredictionSummary {
	s := PredictionSummary{
		Total:    len(predictions),
		ByDomain: make(map[string]int),
	}
	for _, p := range predictions {
		s.ByDomain[string(p.Domain)]++
		switch p.Severity {
		case SeverityCritical:
			s.Critical++
		case SeverityHigh:
			s.High++
		case SeverityMedium:
			s.Medium++
		case SeverityLow:
			s.Low++
		}
	}
	return s
}

func buildDomainSummary(predictions []Prediction) []DomainHealthSummary {
	byDomain := make(map[Domain][]Prediction)
	for _, p := range predictions {
		byDomain[p.Domain] = append(byDomain[p.Domain], p)
	}

	allDomains := []Domain{
		DomainSecurity, DomainFinancial, DomainHR,
		DomainMarketing, DomainProduct, DomainOperational,
	}

	result := make([]DomainHealthSummary, 0, len(allDomains))
	for _, d := range allDomains {
		preds := byDomain[d]
		status, color := domainStatus(preds)
		topAlert := ""
		if len(preds) > 0 {
			topAlert = preds[0].Title
		}
		result = append(result, DomainHealthSummary{
			Domain:      string(d),
			Icon:        domainIcon(d),
			Status:      status,
			StatusColor: color,
			AlertCount:  len(preds),
			TopAlert:    topAlert,
		})
	}
	return result
}

func domainStatus(predictions []Prediction) (string, string) {
	if len(predictions) == 0 {
		return "healthy", "#22c55e" // green
	}
	for _, p := range predictions {
		if p.Severity == SeverityCritical {
			return "critical", "#ef4444" // red
		}
	}
	for _, p := range predictions {
		if p.Severity == SeverityHigh {
			return "warning", "#f97316" // orange
		}
	}
	return "attention", "#eab308" // yellow
}

func domainIcon(d Domain) string {
	icons := map[Domain]string{
		DomainSecurity:    "🛡️",
		DomainFinancial:   "💰",
		DomainHR:          "👥",
		DomainMarketing:   "📣",
		DomainProduct:     "📦",
		DomainOperational: "⚙️",
	}
	if icon, ok := icons[d]; ok {
		return icon
	}
	return "🔮"
}

func severityColor(s Severity) string {
	colors := map[Severity]string{
		SeverityCritical: "#ef4444",
		SeverityHigh:     "#f97316",
		SeverityMedium:   "#eab308",
		SeverityLow:      "#3b82f6",
		SeverityInfo:     "#6b7280",
	}
	if color, ok := colors[s]; ok {
		return color
	}
	return "#6b7280"
}

func confidenceLabel(c float64) string {
	switch {
	case c >= 0.90:
		return "Very High"
	case c >= 0.75:
		return "High"
	case c >= 0.60:
		return "Medium"
	default:
		return "Low"
	}
}

func formatDuration(d time.Duration) string {
	switch {
	case d >= 24*time.Hour:
		days := int(d.Hours() / 24)
		if days == 1 {
			return "within 1 day"
		}
		return fmt.Sprintf("within %d days", days)
	case d >= time.Hour:
		hours := int(d.Hours())
		if hours == 1 {
			return "within 1 hour"
		}
		return fmt.Sprintf("within %d hours", hours)
	default:
		minutes := int(d.Minutes())
		return fmt.Sprintf("within %d minutes", minutes)
	}
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// Ensure APIHandler implements Notifier.
var _ Notifier = (*APIHandler)(nil)

// Ensure context is used (avoid unused import).
var _ = context.Background
