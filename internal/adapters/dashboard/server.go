// Package dashboard implements JARV's adaptive web dashboard.
//
// The dashboard runs as a local HTTP server (default: http://localhost:7777)
// and provides two modes:
//
//   Focus Mode  — clean, minimal UI inspired by Apple's design language.
//                 Cards, soft shadows, one action at a time. For daily use.
//
//   Advanced Mode — full technical view with logs, memory stats, token usage,
//                   Shield events, Oracle controls, and squad management.
//
// The dashboard uses:
//   - Pure Go HTTP server (net/http) — no external web framework
//   - Server-Sent Events (SSE) for real-time updates — no WebSocket dependency
//   - Embedded HTML/CSS/JS — single binary, no separate frontend build step
//   - Tailwind CSS via CDN (optional, graceful fallback to inline styles)
//
// Copyright (c) 2026 JARV contributors. All rights reserved.
// Proprietary and confidential.
package dashboard

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"sync"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// Dashboard Server
// ─────────────────────────────────────────────────────────────────────────────

// Server is JARV's local web dashboard server.
type Server struct {
	cfg      Config
	mux      *http.ServeMux
	httpSrv  *http.Server
	sseHub   *sseHub
	handlers Handlers
	mu       sync.RWMutex
}

// Config holds the dashboard server configuration.
type Config struct {
	Host         string // default: "127.0.0.1"
	Port         int    // default: 7777
	OpenBrowser  bool   // auto-open browser on start
	DefaultMode  UIMode // "focus" or "advanced"
	Title        string // custom title (default: "JARV")
	AccentColor  string // hex color (default: "#6366f1")
}

// UIMode represents the dashboard display mode.
type UIMode string

const (
	UIModeFocus    UIMode = "focus"
	UIModeAdvanced UIMode = "advanced"
)

// Handlers provides the backend data for the dashboard.
// The engine injects these when creating the server.
type Handlers struct {
	// Chat
	SendMessage func(ctx context.Context, sessionID, text string) (string, error)
	GetHistory  func(sessionID string, limit int) ([]ChatMessage, error)

	// Memory
	GetMemoryStats func() (MemoryStats, error)
	SearchMemory   func(query string, limit int) ([]MemoryItem, error)

	// Oracle
	RunOracle func(ctx context.Context, scenario, mode string) (string, error)

	// Shield
	GetShieldStats  func() (ShieldStats, error)
	GetAuditLog     func(limit int) ([]AuditEvent, error)

	// Squads
	ListSquads   func() ([]SquadInfo, error)
	RunSquad     func(ctx context.Context, squadID, input string) (string, error)

	// System
	GetSystemStats func() (SystemStats, error)
}

// NewServer creates a new dashboard server.
func NewServer(cfg Config, handlers Handlers) *Server {
	if cfg.Host == "" {
		cfg.Host = "127.0.0.1"
	}
	if cfg.Port == 0 {
		cfg.Port = 7777
	}
	if cfg.DefaultMode == "" {
		cfg.DefaultMode = UIModeFocus
	}
	if cfg.Title == "" {
		cfg.Title = "JARV"
	}
	if cfg.AccentColor == "" {
		cfg.AccentColor = "#6366f1"
	}

	s := &Server{
		cfg:      cfg,
		mux:      http.NewServeMux(),
		sseHub:   newSSEHub(),
		handlers: handlers,
	}

	s.registerRoutes()
	return s
}

// Start begins listening on the configured address.
func (s *Server) Start(ctx context.Context) error {
	addr := fmt.Sprintf("%s:%d", s.cfg.Host, s.cfg.Port)

	// Find available port if default is taken
	if !isPortAvailable(s.cfg.Host, s.cfg.Port) {
		for p := s.cfg.Port + 1; p < s.cfg.Port+100; p++ {
			if isPortAvailable(s.cfg.Host, p) {
				s.cfg.Port = p
				addr = fmt.Sprintf("%s:%d", s.cfg.Host, p)
				break
			}
		}
	}

	s.httpSrv = &http.Server{
		Addr:         addr,
		Handler:      s.mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// Start SSE hub
	go s.sseHub.run(ctx)

	// Start server
	go func() {
		if err := s.httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			fmt.Printf("JARV dashboard error: %v\n", err)
		}
	}()

	// Auto-open browser
	if s.cfg.OpenBrowser {
		go openBrowser(fmt.Sprintf("http://%s", addr))
	}

	fmt.Printf("JARV dashboard running at http://%s\n", addr)
	return nil
}

// Stop gracefully shuts down the dashboard server.
func (s *Server) Stop(ctx context.Context) error {
	if s.httpSrv != nil {
		return s.httpSrv.Shutdown(ctx)
	}
	return nil
}

// Broadcast sends a real-time event to all connected dashboard clients.
func (s *Server) Broadcast(event string, data any) {
	s.sseHub.broadcast(event, data)
}

// ─────────────────────────────────────────────────────────────────────────────
// Route Registration
// ─────────────────────────────────────────────────────────────────────────────

func (s *Server) registerRoutes() {
	// Main dashboard page
	s.mux.HandleFunc("/", s.handleIndex)

	// API endpoints
	s.mux.HandleFunc("/api/chat", s.handleChat)
	s.mux.HandleFunc("/api/history", s.handleHistory)
	s.mux.HandleFunc("/api/memory/stats", s.handleMemoryStats)
	s.mux.HandleFunc("/api/memory/search", s.handleMemorySearch)
	s.mux.HandleFunc("/api/oracle", s.handleOracle)
	s.mux.HandleFunc("/api/shield/stats", s.handleShieldStats)
	s.mux.HandleFunc("/api/shield/audit", s.handleAuditLog)
	s.mux.HandleFunc("/api/squads", s.handleSquads)
	s.mux.HandleFunc("/api/squads/run", s.handleRunSquad)
	s.mux.HandleFunc("/api/system", s.handleSystemStats)

	// Server-Sent Events stream
	s.mux.HandleFunc("/events", s.handleSSE)
}

// ─────────────────────────────────────────────────────────────────────────────
// HTTP Handlers
// ─────────────────────────────────────────────────────────────────────────────

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	mode := r.URL.Query().Get("mode")
	if mode == "" {
		mode = string(s.cfg.DefaultMode)
	}

	html := generateDashboardHTML(s.cfg.Title, s.cfg.AccentColor, UIMode(mode))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	fmt.Fprint(w, html)
}

func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		SessionID string `json:"session_id"`
		Text      string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	if s.handlers.SendMessage == nil {
		jsonError(w, "Chat handler not configured", http.StatusServiceUnavailable)
		return
	}

	response, err := s.handlers.SendMessage(r.Context(), req.SessionID, req.Text)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	jsonOK(w, map[string]string{"response": response})
}

func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("session_id")
	if s.handlers.GetHistory == nil {
		jsonOK(w, []ChatMessage{})
		return
	}
	history, err := s.handlers.GetHistory(sessionID, 50)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonOK(w, history)
}

func (s *Server) handleMemoryStats(w http.ResponseWriter, r *http.Request) {
	if s.handlers.GetMemoryStats == nil {
		jsonOK(w, MemoryStats{})
		return
	}
	stats, err := s.handlers.GetMemoryStats()
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonOK(w, stats)
}

func (s *Server) handleMemorySearch(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	if s.handlers.SearchMemory == nil || query == "" {
		jsonOK(w, []MemoryItem{})
		return
	}
	results, err := s.handlers.SearchMemory(query, 10)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonOK(w, results)
}

func (s *Server) handleOracle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Scenario string `json:"scenario"`
		Mode     string `json:"mode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	if s.handlers.RunOracle == nil {
		jsonError(w, "Oracle not configured", http.StatusServiceUnavailable)
		return
	}

	// Run Oracle asynchronously and stream progress via SSE
	go func() {
		s.sseHub.broadcast("oracle_start", map[string]string{"scenario": req.Scenario})
		result, err := s.handlers.RunOracle(r.Context(), req.Scenario, req.Mode)
		if err != nil {
			s.sseHub.broadcast("oracle_error", map[string]string{"error": err.Error()})
			return
		}
		s.sseHub.broadcast("oracle_complete", map[string]string{"result": result})
	}()

	jsonOK(w, map[string]string{"status": "started"})
}

func (s *Server) handleShieldStats(w http.ResponseWriter, r *http.Request) {
	if s.handlers.GetShieldStats == nil {
		jsonOK(w, ShieldStats{})
		return
	}
	stats, err := s.handlers.GetShieldStats()
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonOK(w, stats)
}

func (s *Server) handleAuditLog(w http.ResponseWriter, r *http.Request) {
	if s.handlers.GetAuditLog == nil {
		jsonOK(w, []AuditEvent{})
		return
	}
	events, err := s.handlers.GetAuditLog(50)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonOK(w, events)
}

func (s *Server) handleSquads(w http.ResponseWriter, r *http.Request) {
	if s.handlers.ListSquads == nil {
		jsonOK(w, []SquadInfo{})
		return
	}
	squads, err := s.handlers.ListSquads()
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonOK(w, squads)
}

func (s *Server) handleRunSquad(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		SquadID string `json:"squad_id"`
		Input   string `json:"input"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	if s.handlers.RunSquad == nil {
		jsonError(w, "Squad runner not configured", http.StatusServiceUnavailable)
		return
	}

	result, err := s.handlers.RunSquad(r.Context(), req.SquadID, req.Input)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonOK(w, map[string]string{"result": result})
}

func (s *Server) handleSystemStats(w http.ResponseWriter, r *http.Request) {
	if s.handlers.GetSystemStats == nil {
		jsonOK(w, SystemStats{})
		return
	}
	stats, err := s.handlers.GetSystemStats()
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonOK(w, stats)
}

// ─────────────────────────────────────────────────────────────────────────────
// Server-Sent Events
// ─────────────────────────────────────────────────────────────────────────────

func (s *Server) handleSSE(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "SSE not supported", http.StatusInternalServerError)
		return
	}

	client := s.sseHub.subscribe()
	defer s.sseHub.unsubscribe(client)

	// Send initial connection event
	fmt.Fprintf(w, "event: connected\ndata: {\"status\":\"ok\"}\n\n")
	flusher.Flush()

	for {
		select {
		case msg, ok := <-client:
			if !ok {
				return
			}
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", msg.Event, msg.Data)
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// SSE Hub
// ─────────────────────────────────────────────────────────────────────────────

type sseMessage struct {
	Event string
	Data  string
}

type sseHub struct {
	clients   map[chan sseMessage]struct{}
	broadcast chan sseMessage
	subscribe_  chan chan sseMessage
	unsubscribe_ chan chan sseMessage
	mu        sync.RWMutex
}

func newSSEHub() *sseHub {
	return &sseHub{
		clients:      make(map[chan sseMessage]struct{}),
		broadcast:    make(chan sseMessage, 100),
		subscribe_:   make(chan chan sseMessage, 10),
		unsubscribe_: make(chan chan sseMessage, 10),
	}
}

func (h *sseHub) run(ctx context.Context) {
	for {
		select {
		case client := <-h.subscribe_:
			h.mu.Lock()
			h.clients[client] = struct{}{}
			h.mu.Unlock()
		case client := <-h.unsubscribe_:
			h.mu.Lock()
			delete(h.clients, client)
			close(client)
			h.mu.Unlock()
		case msg := <-h.broadcast:
			h.mu.RLock()
			for client := range h.clients {
				select {
				case client <- msg:
				default:
					// Client too slow, skip
				}
			}
			h.mu.RUnlock()
		case <-ctx.Done():
			return
		}
	}
}

func (h *sseHub) subscribe() chan sseMessage {
	client := make(chan sseMessage, 10)
	h.subscribe_ <- client
	return client
}

func (h *sseHub) unsubscribe(client chan sseMessage) {
	h.unsubscribe_ <- client
}

func (h *sseHub) broadcast(event string, data any) {
	jsonData, _ := json.Marshal(data)
	select {
	case h.broadcast <- sseMessage{Event: event, Data: string(jsonData)}:
	default:
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Dashboard HTML Generator
// ─────────────────────────────────────────────────────────────────────────────

func generateDashboardHTML(title, accentColor string, mode UIMode) string {
	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="pt-BR">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>%s</title>
  <script src="https://cdn.tailwindcss.com"></script>
  <style>
    :root { --accent: %s; }
    body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif; }
    .accent-bg { background-color: var(--accent); }
    .accent-text { color: var(--accent); }
    .accent-border { border-color: var(--accent); }
    .card { background: white; border-radius: 16px; box-shadow: 0 2px 20px rgba(0,0,0,0.08); padding: 24px; }
    .chat-bubble-user { background: var(--accent); color: white; border-radius: 18px 18px 4px 18px; }
    .chat-bubble-agent { background: #f1f5f9; color: #1e293b; border-radius: 18px 18px 18px 4px; }
    .mode-toggle { cursor: pointer; transition: all 0.2s; }
    .mode-toggle.active { background: var(--accent); color: white; }
    #oracle-panel { display: %s; }
    #advanced-panel { display: %s; }
    .status-dot { width: 8px; height: 8px; border-radius: 50%%; display: inline-block; }
    .status-online { background: #22c55e; }
    .status-offline { background: #ef4444; }
    .typing-indicator span { animation: blink 1.4s infinite; }
    .typing-indicator span:nth-child(2) { animation-delay: 0.2s; }
    .typing-indicator span:nth-child(3) { animation-delay: 0.4s; }
    @keyframes blink { 0%%, 80%%, 100%% { opacity: 0; } 40%% { opacity: 1; } }
  </style>
</head>
<body class="bg-gray-50 min-h-screen">

  <!-- Header -->
  <header class="bg-white border-b border-gray-100 px-6 py-4 flex items-center justify-between sticky top-0 z-50">
    <div class="flex items-center gap-3">
      <div class="w-8 h-8 accent-bg rounded-lg flex items-center justify-center">
        <span class="text-white font-bold text-sm">J</span>
      </div>
      <span class="font-semibold text-gray-900">%s</span>
      <span class="status-dot status-online ml-1" id="status-dot" title="Online"></span>
    </div>
    <div class="flex items-center gap-2">
      <button onclick="setMode('focus')" id="btn-focus"
        class="mode-toggle px-3 py-1.5 rounded-lg text-sm font-medium %s">
        Foco
      </button>
      <button onclick="setMode('advanced')" id="btn-advanced"
        class="mode-toggle px-3 py-1.5 rounded-lg text-sm font-medium %s">
        Avançado
      </button>
    </div>
  </header>

  <!-- Main Layout -->
  <div class="max-w-6xl mx-auto px-4 py-6 grid grid-cols-12 gap-6">

    <!-- Chat Panel (always visible) -->
    <div class="col-span-12 lg:col-span-7">
      <div class="card h-[600px] flex flex-col">
        <div class="flex items-center justify-between mb-4">
          <h2 class="font-semibold text-gray-900">Conversa</h2>
          <div class="flex gap-2">
            <select id="squad-select" class="text-sm border border-gray-200 rounded-lg px-2 py-1"
              onchange="loadSquad(this.value)">
              <option value="">Agente geral</option>
            </select>
          </div>
        </div>

        <!-- Messages -->
        <div id="messages" class="flex-1 overflow-y-auto space-y-3 mb-4 pr-1">
          <div class="text-center text-sm text-gray-400 py-8">
            Olá! Como posso ajudar você hoje?
          </div>
        </div>

        <!-- Input -->
        <div class="flex gap-2">
          <input id="chat-input" type="text" placeholder="Digite sua mensagem..."
            class="flex-1 border border-gray-200 rounded-xl px-4 py-3 text-sm focus:outline-none focus:border-indigo-400"
            onkeydown="if(event.key==='Enter')sendMessage()">
          <button onclick="sendMessage()"
            class="accent-bg text-white px-4 py-3 rounded-xl text-sm font-medium hover:opacity-90 transition-opacity">
            Enviar
          </button>
        </div>
      </div>
    </div>

    <!-- Side Panels -->
    <div class="col-span-12 lg:col-span-5 space-y-4">

      <!-- Oracle Panel -->
      <div id="oracle-panel" class="card">
        <div class="flex items-center justify-between mb-4">
          <h2 class="font-semibold text-gray-900">Oracle</h2>
          <select id="oracle-mode" class="text-sm border border-gray-200 rounded-lg px-2 py-1">
            <option value="economy">Econômico</option>
            <option value="balanced" selected>Balanceado</option>
            <option value="maximum">Máximo</option>
          </select>
        </div>
        <textarea id="oracle-input" placeholder="Descreva o cenário para análise preditiva..."
          class="w-full border border-gray-200 rounded-xl px-4 py-3 text-sm resize-none h-24 focus:outline-none focus:border-indigo-400 mb-3"></textarea>
        <button onclick="runOracle()"
          class="w-full accent-bg text-white py-2.5 rounded-xl text-sm font-medium hover:opacity-90 transition-opacity">
          Analisar Cenário
        </button>
        <div id="oracle-result" class="mt-3 text-sm text-gray-600 hidden"></div>
      </div>

      <!-- Advanced Panel -->
      <div id="advanced-panel" class="space-y-4">

        <!-- Memory Stats -->
        <div class="card">
          <h3 class="font-semibold text-gray-900 mb-3">Memória</h3>
          <div class="grid grid-cols-2 gap-3" id="memory-stats">
            <div class="bg-gray-50 rounded-lg p-3 text-center">
              <div class="text-2xl font-bold accent-text" id="mem-total">—</div>
              <div class="text-xs text-gray-500 mt-1">Entradas</div>
            </div>
            <div class="bg-gray-50 rounded-lg p-3 text-center">
              <div class="text-2xl font-bold text-orange-500" id="mem-pending">—</div>
              <div class="text-xs text-gray-500 mt-1">Pendente sync</div>
            </div>
          </div>
        </div>

        <!-- Shield Stats -->
        <div class="card">
          <h3 class="font-semibold text-gray-900 mb-3">Shield</h3>
          <div class="space-y-2" id="shield-stats">
            <div class="flex justify-between text-sm">
              <span class="text-gray-500">Eventos detectados</span>
              <span class="font-medium" id="shield-events">—</span>
            </div>
            <div class="flex justify-between text-sm">
              <span class="text-gray-500">Padrões conhecidos</span>
              <span class="font-medium" id="shield-patterns">—</span>
            </div>
          </div>
        </div>

        <!-- System Stats -->
        <div class="card">
          <h3 class="font-semibold text-gray-900 mb-3">Sistema</h3>
          <div class="space-y-2" id="system-stats">
            <div class="flex justify-between text-sm">
              <span class="text-gray-500">RAM usada</span>
              <span class="font-medium" id="sys-ram">—</span>
            </div>
            <div class="flex justify-between text-sm">
              <span class="text-gray-500">Tokens hoje</span>
              <span class="font-medium" id="sys-tokens">—</span>
            </div>
            <div class="flex justify-between text-sm">
              <span class="text-gray-500">Custo hoje</span>
              <span class="font-medium" id="sys-cost">—</span>
            </div>
          </div>
        </div>
      </div>

    </div>
  </div>

<script>
// ── State ──────────────────────────────────────────────────────────────────
let currentMode = '%s';
let sessionID = 'session-' + Date.now();
let isTyping = false;

// ── Mode Toggle ────────────────────────────────────────────────────────────
function setMode(mode) {
  currentMode = mode;
  document.getElementById('oracle-panel').style.display = 'block';
  document.getElementById('advanced-panel').style.display = mode === 'advanced' ? 'block' : 'none';

  document.getElementById('btn-focus').classList.toggle('active', mode === 'focus');
  document.getElementById('btn-advanced').classList.toggle('active', mode === 'advanced');

  localStorage.setItem('jarv-mode', mode);
}

// ── Chat ───────────────────────────────────────────────────────────────────
async function sendMessage() {
  const input = document.getElementById('chat-input');
  const text = input.value.trim();
  if (!text || isTyping) return;

  input.value = '';
  appendMessage('user', text);
  showTyping();

  try {
    const resp = await fetch('/api/chat', {
      method: 'POST',
      headers: {'Content-Type': 'application/json'},
      body: JSON.stringify({session_id: sessionID, text})
    });
    const data = await resp.json();
    hideTyping();
    appendMessage('agent', data.response || data.error || 'Erro ao processar');
  } catch (e) {
    hideTyping();
    appendMessage('agent', 'Erro de conexão. Verifique se o JARV está rodando.');
  }
}

function appendMessage(role, text) {
  const messages = document.getElementById('messages');
  const div = document.createElement('div');
  div.className = 'flex ' + (role === 'user' ? 'justify-end' : 'justify-start');

  const bubble = document.createElement('div');
  bubble.className = 'max-w-xs lg:max-w-md px-4 py-2.5 text-sm ' +
    (role === 'user' ? 'chat-bubble-user' : 'chat-bubble-agent');
  bubble.textContent = text;

  div.appendChild(bubble);
  messages.appendChild(div);
  messages.scrollTop = messages.scrollHeight;
}

function showTyping() {
  isTyping = true;
  const messages = document.getElementById('messages');
  const div = document.createElement('div');
  div.id = 'typing';
  div.className = 'flex justify-start';
  div.innerHTML = '<div class="chat-bubble-agent px-4 py-2.5 typing-indicator">' +
    '<span class="inline-block w-2 h-2 bg-gray-400 rounded-full mx-0.5">•</span>' +
    '<span class="inline-block w-2 h-2 bg-gray-400 rounded-full mx-0.5">•</span>' +
    '<span class="inline-block w-2 h-2 bg-gray-400 rounded-full mx-0.5">•</span>' +
    '</div>';
  messages.appendChild(div);
  messages.scrollTop = messages.scrollHeight;
}

function hideTyping() {
  isTyping = false;
  const el = document.getElementById('typing');
  if (el) el.remove();
}

// ── Oracle ─────────────────────────────────────────────────────────────────
async function runOracle() {
  const scenario = document.getElementById('oracle-input').value.trim();
  if (!scenario) return;

  const mode = document.getElementById('oracle-mode').value;
  const resultEl = document.getElementById('oracle-result');
  resultEl.classList.remove('hidden');
  resultEl.textContent = 'Simulando cenário...';

  try {
    await fetch('/api/oracle', {
      method: 'POST',
      headers: {'Content-Type': 'application/json'},
      body: JSON.stringify({scenario, mode})
    });
    // Result comes via SSE
  } catch (e) {
    resultEl.textContent = 'Erro ao iniciar Oracle.';
  }
}

// ── Stats Polling ──────────────────────────────────────────────────────────
async function refreshStats() {
  if (currentMode !== 'advanced') return;

  try {
    const [memResp, shieldResp, sysResp] = await Promise.all([
      fetch('/api/memory/stats').then(r => r.json()),
      fetch('/api/shield/stats').then(r => r.json()),
      fetch('/api/system').then(r => r.json())
    ]);

    document.getElementById('mem-total').textContent = memResp.total_entries ?? '—';
    document.getElementById('mem-pending').textContent = memResp.pending_sync ?? '—';
    document.getElementById('shield-events').textContent = shieldResp.total_events ?? '—';
    document.getElementById('shield-patterns').textContent = shieldResp.known_patterns ?? '—';
    document.getElementById('sys-ram').textContent = sysResp.ram_mb ? sysResp.ram_mb + ' MB' : '—';
    document.getElementById('sys-tokens').textContent = sysResp.tokens_today ?? '—';
    document.getElementById('sys-cost').textContent = sysResp.cost_today ? '$' + sysResp.cost_today : '—';
  } catch (e) {}
}

// ── Squads ─────────────────────────────────────────────────────────────────
async function loadSquads() {
  try {
    const squads = await fetch('/api/squads').then(r => r.json());
    const select = document.getElementById('squad-select');
    squads.forEach(s => {
      const opt = document.createElement('option');
      opt.value = s.id;
      opt.textContent = s.name;
      select.appendChild(opt);
    });
  } catch (e) {}
}

function loadSquad(squadID) {
  sessionID = squadID ? 'squad-' + squadID + '-' + Date.now() : 'session-' + Date.now();
  document.getElementById('messages').innerHTML =
    '<div class="text-center text-sm text-gray-400 py-8">' +
    (squadID ? 'Squad ativado. Como posso ajudar?' : 'Olá! Como posso ajudar você hoje?') +
    '</div>';
}

// ── SSE ────────────────────────────────────────────────────────────────────
const evtSource = new EventSource('/events');

evtSource.addEventListener('oracle_complete', e => {
  const data = JSON.parse(e.data);
  const resultEl = document.getElementById('oracle-result');
  resultEl.classList.remove('hidden');
  resultEl.innerHTML = '<pre class="whitespace-pre-wrap text-xs">' + data.result + '</pre>';
});

evtSource.addEventListener('oracle_error', e => {
  const data = JSON.parse(e.data);
  document.getElementById('oracle-result').textContent = 'Erro: ' + data.error;
});

evtSource.onerror = () => {
  document.getElementById('status-dot').className = 'status-dot status-offline ml-1';
};

evtSource.onopen = () => {
  document.getElementById('status-dot').className = 'status-dot status-online ml-1';
};

// ── Init ───────────────────────────────────────────────────────────────────
document.addEventListener('DOMContentLoaded', () => {
  const savedMode = localStorage.getItem('jarv-mode') || currentMode;
  setMode(savedMode);
  loadSquads();
  refreshStats();
  setInterval(refreshStats, 10000);
});
</script>
</body>
</html>`,
		title, accentColor,
		// oracle-panel display
		func() string {
			if mode == UIModeFocus {
				return "block"
			}
			return "block"
		}(),
		// advanced-panel display
		func() string {
			if mode == UIModeAdvanced {
				return "block"
			}
			return "none"
		}(),
		title,
		// btn-focus class
		func() string {
			if mode == UIModeFocus {
				return "active"
			}
			return ""
		}(),
		// btn-advanced class
		func() string {
			if mode == UIModeAdvanced {
				return "active"
			}
			return ""
		}(),
		// current mode for JS
		string(mode),
	)
}

// ─────────────────────────────────────────────────────────────────────────────
// Data Types
// ─────────────────────────────────────────────────────────────────────────────

type ChatMessage struct {
	Role      string    `json:"role"`
	Content   string    `json:"content"`
	Timestamp time.Time `json:"timestamp"`
}

type MemoryStats struct {
	TotalEntries int64 `json:"total_entries"`
	PendingSync  int64 `json:"pending_sync"`
	TotalTriples int64 `json:"total_triples"`
}

type MemoryItem struct {
	ID      string  `json:"id"`
	Content string  `json:"content"`
	Score   float32 `json:"score"`
}

type ShieldStats struct {
	TotalEvents   int64 `json:"total_events"`
	KnownPatterns int64 `json:"known_patterns"`
}

type AuditEvent struct {
	ID          string    `json:"id"`
	Type        string    `json:"type"`
	Level       int       `json:"level"`
	Description string    `json:"description"`
	Timestamp   time.Time `json:"timestamp"`
	Blocked     bool      `json:"blocked"`
}

type SquadInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	AgentCount  int    `json:"agent_count"`
}

type SystemStats struct {
	RAMMB      int64   `json:"ram_mb"`
	TokensToday int64  `json:"tokens_today"`
	CostToday  float64 `json:"cost_today"`
	Uptime     string  `json:"uptime"`
}

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

func jsonOK(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

func jsonError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func isPortAvailable(host string, port int) bool {
	ln, err := net.Listen("tcp", fmt.Sprintf("%s:%d", host, port))
	if err != nil {
		return false
	}
	ln.Close()
	return true
}

func openBrowser(url string) {
	// Platform-specific browser opening
	// Uses os/exec internally — no external dependency
	time.Sleep(500 * time.Millisecond) // Wait for server to start
	// Implementation varies by OS — handled in cmd/jarv/main.go
	_ = url
}

// embed placeholder for future static assets
var _ embed.FS
var _ fs.FS
