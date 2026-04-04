// Package agent is the heart of JARV — the core intelligence engine.
//
// The Engine is a pure, stateless orchestrator. It receives a Request,
// executes the agent loop (plan → act → observe → reflect), and returns
// a Response. It has zero knowledge of HTTP, WebSockets, databases, or
// any specific LLM provider. All external concerns are injected via interfaces.
//
// Architecture: Hexagonal (Ports & Adapters)
//   - Input:  Request (text + media + session context)
//   - Output: Response (text + tool results + memory updates)
//   - Ports:  LLM Provider, Memory Store, Knowledge Graph, Tool Registry
//
// The agent loop implements a modified ReAct (Reasoning + Acting) pattern
// with JARV-specific enhancements:
//   1. Context Assembly  — build system prompt from skills + memory + graph
//   2. Intent Routing    — classify request and select optimal model tier
//   3. LLM Invocation    — call the selected model with full context
//   4. Tool Execution    — execute any requested tools in parallel
//   5. Self-Critique     — optionally refine the response before delivery
//   6. Memory Update     — persist new facts and conversation turns
//
// Copyright (c) 2026 JARV contributors. All rights reserved.
// Proprietary and confidential.
package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mkvinicius/jarv/internal/ports/llm"
	"github.com/mkvinicius/jarv/internal/ports/storage"
)

// ─────────────────────────────────────────────────────────────────────────────
// Engine Types
// ─────────────────────────────────────────────────────────────────────────────

// Request is the input to the agent engine.
type Request struct {
	SessionID   string            // conversation session identifier
	UserID      string            // who is sending this message
	Text        string            // the user's message
	Images      []llm.ImageData   // images for vision models
	MediaRefs   []string          // references to attached media
	SquadID     string            // which squad handles this (empty = default)
	Metadata    map[string]string // channel-specific metadata
	StreamCh    chan<- string      // if non-nil, stream tokens here
}

// Response is the output from the agent engine.
type Response struct {
	Text        string            // the agent's response text
	ToolResults []ToolResult      // results from tool executions
	MemoryIDs   []string          // IDs of memory entries created/updated
	ModelUsed   string            // which model was actually used
	Tier        llm.Tier          // which tier was selected
	TokenUsage  llm.TokenUsage    // token consumption
	FromCache   bool              // true if served from semantic cache
	Critiqued   bool              // true if self-critique was applied
	Latency     time.Duration
}

// ToolResult holds the output of a single tool execution.
type ToolResult struct {
	ToolName string
	CallID   string
	Output   string
	Error    error
	Duration time.Duration
}

// ─────────────────────────────────────────────────────────────────────────────
// Tool Registry
// ─────────────────────────────────────────────────────────────────────────────

// Tool is a capability the agent can invoke during its reasoning loop.
type Tool interface {
	// Name returns the tool's identifier (must be unique, snake_case).
	Name() string
	// Description returns a human-readable description for the LLM.
	Description() string
	// Schema returns the JSON Schema for the tool's parameters.
	Schema() any
	// Execute runs the tool with the given JSON arguments.
	Execute(ctx context.Context, args string) (string, error)
}

// ToolRegistry manages the set of tools available to an agent.
type ToolRegistry struct {
	tools map[string]Tool
	mu    sync.RWMutex
}

// NewToolRegistry creates an empty tool registry.
func NewToolRegistry() *ToolRegistry {
	return &ToolRegistry{tools: make(map[string]Tool)}
}

// Register adds a tool to the registry.
func (r *ToolRegistry) Register(t Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[t.Name()] = t
}

// Get retrieves a tool by name.
func (r *ToolRegistry) Get(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

// Definitions returns all tools as LLM tool definitions.
func (r *ToolRegistry) Definitions() []llm.ToolDefinition {
	r.mu.RLock()
	defer r.mu.RUnlock()
	defs := make([]llm.ToolDefinition, 0, len(r.tools))
	for _, t := range r.tools {
		defs = append(defs, llm.ToolDefinition{
			Name:        t.Name(),
			Description: t.Description(),
			Parameters:  t.Schema(),
		})
	}
	return defs
}

// ─────────────────────────────────────────────────────────────────────────────
// Engine Configuration
// ─────────────────────────────────────────────────────────────────────────────

// EngineConfig holds the configuration for a JARV engine instance.
type EngineConfig struct {
	// Identity
	Name        string // agent name (shown in responses)
	Persona     string // system prompt persona
	Language    string // default language ("pt-BR", "en-US")

	// Reasoning
	MaxIterations   int           // max tool-call iterations per request (default: 10)
	SelfCritique    bool          // enable self-critique loop
	MaxCritiqueRounds int         // max critique rounds (default: 1)
	Timeout         time.Duration // per-request timeout (default: 120s)

	// Memory
	MaxHistoryTurns int // max conversation turns to include in context (default: 20)
	EnableMemory    bool
	EnableGraph     bool

	// Cache
	EnableCache bool
	CacheTTL    time.Duration

	// Oracle
	EnableOracle bool
}

// DefaultEngineConfig returns sensible defaults for a JARV engine.
func DefaultEngineConfig() EngineConfig {
	return EngineConfig{
		Name:              "JARV",
		Persona:           "Você é JARV, um assistente de inteligência artificial avançado. Você é preciso, eficiente e sempre honesto.",
		Language:          "pt-BR",
		MaxIterations:     10,
		SelfCritique:      false, // enabled per-squad
		MaxCritiqueRounds: 1,
		Timeout:           120 * time.Second,
		MaxHistoryTurns:   20,
		EnableMemory:      true,
		EnableGraph:       true,
		EnableCache:       true,
		CacheTTL:          24 * time.Hour,
		EnableOracle:      true,
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Engine — The Core Intelligence
// ─────────────────────────────────────────────────────────────────────────────

// Engine is the central intelligence of JARV.
// It is safe for concurrent use — multiple sessions can run simultaneously.
type Engine struct {
	cfg      EngineConfig
	chain    llm.FallbackChain
	router   llm.Router
	memory   storage.MemoryStore
	graph    storage.KnowledgeGraph
	sessions storage.SessionStore
	tools    *ToolRegistry
	cache    *semanticCache
	active   atomic.Int64 // count of active requests
}

// NewEngine creates a new JARV engine with the given dependencies.
func NewEngine(
	cfg EngineConfig,
	chain llm.FallbackChain,
	router llm.Router,
	memory storage.MemoryStore,
	graph storage.KnowledgeGraph,
	sessions storage.SessionStore,
	tools *ToolRegistry,
) *Engine {
	var cache *semanticCache
	if cfg.EnableCache {
		cache = newSemanticCache(cfg.CacheTTL)
	}

	return &Engine{
		cfg:      cfg,
		chain:    chain,
		router:   router,
		memory:   memory,
		graph:    graph,
		sessions: sessions,
		tools:    tools,
		cache:    cache,
	}
}

// Process executes the full agent loop for a single request.
func (e *Engine) Process(ctx context.Context, req Request) (*Response, error) {
	start := time.Now()
	e.active.Add(1)
	defer e.active.Add(-1)

	// Apply timeout
	timeout := e.cfg.Timeout
	if timeout == 0 {
		timeout = 120 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	resp := &Response{}

	// ── Step 1: Check semantic cache ──────────────────────────────────
	if e.cache != nil {
		cacheKey := e.cache.key(req.Text, req.SessionID)
		if cached, ok := e.cache.get(cacheKey); ok {
			resp.Text = cached
			resp.FromCache = true
			resp.Latency = time.Since(start)
			return resp, nil
		}
	}

	// ── Step 2: Route to optimal model tier ───────────────────────────
	signal := e.extractSignal(ctx, req)
	tier := e.router.Route(signal)
	resp.Tier = tier

	// ── Step 3: Assemble context ──────────────────────────────────────
	messages, err := e.assembleContext(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("engine: context assembly: %w", err)
	}

	// ── Step 4: Agent loop (ReAct) ────────────────────────────────────
	finalText, usage, modelUsed, toolResults, err := e.runLoop(ctx, req, messages, tier)
	if err != nil {
		return nil, fmt.Errorf("engine: agent loop: %w", err)
	}

	resp.Text = finalText
	resp.TokenUsage = usage
	resp.ModelUsed = modelUsed
	resp.ToolResults = toolResults

	// ── Step 5: Self-critique (optional) ─────────────────────────────
	if e.cfg.SelfCritique && len(strings.Fields(finalText)) > 20 {
		refined, critiqued := e.selfCritique(ctx, req.Text, finalText)
		if critiqued {
			resp.Text = refined
			resp.Critiqued = true
		}
	}

	// ── Step 6: Update memory ─────────────────────────────────────────
	if e.cfg.EnableMemory && e.memory != nil {
		go e.updateMemory(context.Background(), req, resp.Text)
	}

	// ── Step 7: Cache the response ────────────────────────────────────
	if e.cache != nil && !resp.FromCache {
		cacheKey := e.cache.key(req.Text, req.SessionID)
		e.cache.set(cacheKey, resp.Text)
	}

	resp.Latency = time.Since(start)
	return resp, nil
}

// ActiveRequests returns the number of currently active requests.
func (e *Engine) ActiveRequests() int64 {
	return e.active.Load()
}

// ─────────────────────────────────────────────────────────────────────────────
// Internal Methods
// ─────────────────────────────────────────────────────────────────────────────

// extractSignal builds the routing signal from the request and session context.
func (e *Engine) extractSignal(ctx context.Context, req Request) llm.IntentSignal {
	signal := llm.IntentSignal{
		TokenEstimate:  estimateTokens(req.Text),
		HasAttachments: len(req.MediaRefs) > 0 || len(req.Images) > 0,
		HasCodeBlocks:  strings.Contains(req.Text, "```"),
	}

	// Check for explicit intent keywords
	lower := strings.ToLower(req.Text)
	switch {
	case strings.Contains(lower, "oracle") || strings.Contains(lower, "simul") || strings.Contains(lower, "previs"):
		signal.ExplicitIntent = "oracle"
	case strings.Contains(lower, "segur") || strings.Contains(lower, "ataque") || strings.Contains(lower, "shield"):
		signal.ExplicitIntent = "security"
	case strings.Contains(lower, "código") || strings.Contains(lower, "code") || strings.Contains(lower, "programa"):
		signal.ExplicitIntent = "code"
	}

	// Get conversation depth from session
	if e.sessions != nil {
		history, err := e.sessions.GetHistory(ctx, req.SessionID, 100)
		if err == nil {
			signal.ConversationDepth = len(history)
			// Count recent tool calls
			for i := len(history) - 1; i >= 0 && i >= len(history)-6; i-- {
				if history[i].Role == "tool" {
					signal.RecentToolCalls++
				}
			}
		}
	}

	return signal
}

// assembleContext builds the full message list for the LLM call.
func (e *Engine) assembleContext(ctx context.Context, req Request) ([]llm.Message, error) {
	var messages []llm.Message

	// System prompt
	systemPrompt := e.buildSystemPrompt(ctx, req)
	messages = append(messages, llm.Message{
		Role:    llm.RoleSystem,
		Content: systemPrompt,
	})

	// Conversation history
	if e.sessions != nil {
		history, err := e.sessions.GetHistory(ctx, req.SessionID, e.cfg.MaxHistoryTurns)
		if err == nil {
			for _, turn := range history {
				messages = append(messages, llm.Message{
					Role:    llm.Role(turn.Role),
					Content: turn.Content,
				})
			}
		}
	}

	// Current user message
	messages = append(messages, llm.Message{
		Role:    llm.RoleUser,
		Content: req.Text,
	})

	return messages, nil
}

// buildSystemPrompt assembles the system prompt from persona + memory + graph context.
func (e *Engine) buildSystemPrompt(ctx context.Context, req Request) string {
	var sb strings.Builder

	// Base persona
	sb.WriteString(e.cfg.Persona)
	sb.WriteString("\n\n")

	// Relevant memories
	if e.cfg.EnableMemory && e.memory != nil {
		results, err := e.memory.Search(ctx, storage.MemoryQuery{
			Text:     req.Text,
			Limit:    5,
			MinScore: 0.6,
		})
		if err == nil && len(results) > 0 {
			sb.WriteString("## Memória Relevante\n")
			for _, r := range results {
				sb.WriteString(fmt.Sprintf("- %s\n", r.Entry.Content))
			}
			sb.WriteString("\n")
		}
	}

	// Knowledge graph context
	if e.cfg.EnableGraph && e.graph != nil {
		triples, err := e.graph.Query(ctx, storage.GraphQuery{
			Subject: extractSubject(req.Text),
			MaxHops: 2,
			Limit:   10,
		})
		if err == nil && len(triples) > 0 {
			sb.WriteString("## Contexto de Relacionamentos\n")
			for _, t := range triples {
				sb.WriteString(fmt.Sprintf("- %s %s %s\n", t.Subject, t.Predicate, t.Object))
			}
			sb.WriteString("\n")
		}
	}

	// Language instruction
	lang := e.cfg.Language
	if lang == "" {
		lang = "pt-BR"
	}
	sb.WriteString(fmt.Sprintf("Responda sempre em %s, de forma clara e direta.\n", lang))

	return sb.String()
}

// runLoop executes the ReAct agent loop until a final response is produced.
func (e *Engine) runLoop(
	ctx context.Context,
	req Request,
	messages []llm.Message,
	tier llm.Tier,
) (string, llm.TokenUsage, string, []ToolResult, error) {
	var (
		totalUsage  llm.TokenUsage
		toolResults []ToolResult
		modelUsed   string
	)

	// Use tier-filtered chain
	chain := e.chain.SetTier(tier)

	for iteration := 0; iteration < e.cfg.MaxIterations; iteration++ {
		llmReq := llm.Request{
			Messages: messages,
			Images:   req.Images,
			Tools:    e.tools.Definitions(),
			Stream:   req.StreamCh != nil,
		}

		resp, err := chain.Execute(ctx, llmReq)
		if err != nil {
			return "", totalUsage, modelUsed, toolResults, err
		}

		totalUsage.PromptTokens += resp.Usage.PromptTokens
		totalUsage.CompletionTokens += resp.Usage.CompletionTokens
		totalUsage.TotalTokens += resp.Usage.TotalTokens
		totalUsage.EstimatedCostUSD += resp.Usage.EstimatedCostUSD
		modelUsed = resp.Model

		// Stream tokens if requested
		if req.StreamCh != nil && resp.Content != "" {
			select {
			case req.StreamCh <- resp.Content:
			case <-ctx.Done():
				return "", totalUsage, modelUsed, toolResults, ctx.Err()
			}
		}

		// No tool calls → we have the final response
		if len(resp.ToolCalls) == 0 {
			// Persist the turn
			if e.sessions != nil {
				_ = e.sessions.AppendTurn(ctx, storage.Turn{
					SessionID: req.SessionID,
					Role:      "assistant",
					Content:   resp.Content,
					Tokens:    resp.Usage.CompletionTokens,
				})
			}
			return resp.Content, totalUsage, modelUsed, toolResults, nil
		}

		// Execute tool calls in parallel
		results := e.executeTools(ctx, resp.ToolCalls)
		toolResults = append(toolResults, results...)

		// Add assistant message with tool calls to history
		messages = append(messages, llm.Message{
			Role:      llm.RoleAssistant,
			Content:   resp.Content,
			ToolCalls: resp.ToolCalls,
		})

		// Add tool results to history
		for _, r := range results {
			content := r.Output
			if r.Error != nil {
				content = fmt.Sprintf("Error: %v", r.Error)
			}
			messages = append(messages, llm.Message{
				Role:       llm.RoleTool,
				Content:    content,
				ToolCallID: r.CallID,
			})
		}
	}

	return "Atingi o limite máximo de iterações sem uma resposta final.", totalUsage, modelUsed, toolResults, nil
}

// executeTools runs all tool calls in parallel using goroutines.
func (e *Engine) executeTools(ctx context.Context, calls []llm.ToolCall) []ToolResult {
	results := make([]ToolResult, len(calls))
	var wg sync.WaitGroup

	for i, call := range calls {
		wg.Add(1)
		go func(idx int, tc llm.ToolCall) {
			defer wg.Done()
			start := time.Now()
			result := ToolResult{
				ToolName: tc.Name,
				CallID:   tc.ID,
			}

			tool, ok := e.tools.Get(tc.Name)
			if !ok {
				result.Error = fmt.Errorf("tool not found: %s", tc.Name)
			} else {
				output, err := tool.Execute(ctx, tc.Args)
				result.Output = output
				result.Error = err
			}
			result.Duration = time.Since(start)
			results[idx] = result
		}(i, call)
	}

	wg.Wait()
	return results
}

// selfCritique applies one round of self-critique to the response.
// Returns the refined response and whether it was actually improved.
func (e *Engine) selfCritique(ctx context.Context, originalPrompt, response string) (string, bool) {
	// Critique prompt
	critiquePrompt := fmt.Sprintf(
		"Analise criticamente esta resposta e liste até 3 problemas específicos (comece com '- '). Se estiver boa, responda 'SEM_PROBLEMAS'.\n\nPergunta: %s\n\nResposta: %s",
		originalPrompt, response,
	)

	critiqueReq := llm.Request{
		Messages: []llm.Message{
			{Role: llm.RoleSystem, Content: "Você é um revisor crítico de respostas de IA. Seja conciso e específico."},
			{Role: llm.RoleUser, Content: critiquePrompt},
		},
	}

	critiqueResp, err := e.chain.SetTier(llm.TierNano).Execute(ctx, critiqueReq)
	if err != nil || strings.Contains(strings.ToUpper(critiqueResp.Content), "SEM_PROBLEMAS") {
		return response, false
	}

	// Refinement prompt
	refinementPrompt := fmt.Sprintf(
		"Melhore esta resposta corrigindo os problemas identificados. Mantenha o que estava correto.\n\nPergunta original: %s\n\nResposta original: %s\n\nProblemas: %s",
		originalPrompt, response, critiqueResp.Content,
	)

	refinementReq := llm.Request{
		Messages: []llm.Message{
			{Role: llm.RoleSystem, Content: e.cfg.Persona},
			{Role: llm.RoleUser, Content: refinementPrompt},
		},
	}

	refinedResp, err := e.chain.Execute(ctx, refinementReq)
	if err != nil || refinedResp.Content == "" {
		return response, false
	}

	// Only accept if genuinely different and not shorter
	if refinedResp.Content == response {
		return response, false
	}

	return refinedResp.Content, true
}

// updateMemory persists the conversation turn and extracts new facts.
func (e *Engine) updateMemory(ctx context.Context, req Request, response string) {
	// Save conversation turn
	_ = e.memory.Save(ctx, storage.MemoryEntry{
		Kind:    storage.KindConversation,
		Content: fmt.Sprintf("Usuário: %s\nJARV: %s", req.Text, response),
		Tags:    []string{"conversation", req.SessionID},
	})

	// Extract and store knowledge graph triples
	if e.cfg.EnableGraph && e.graph != nil {
		combined := req.Text + " " + response
		_, _ = e.graph.ExtractAndStore(ctx, combined, "conversation:"+req.SessionID)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Semantic Cache (internal)
// ─────────────────────────────────────────────────────────────────────────────

type cacheEntry struct {
	value     string
	expiresAt time.Time
}

type semanticCache struct {
	entries map[string]cacheEntry
	mu      sync.RWMutex
	ttl     time.Duration
}

func newSemanticCache(ttl time.Duration) *semanticCache {
	if ttl == 0 {
		ttl = 24 * time.Hour
	}
	return &semanticCache{entries: make(map[string]cacheEntry), ttl: ttl}
}

func (c *semanticCache) key(text, sessionID string) string {
	normalized := strings.ToLower(strings.Join(strings.Fields(text), " "))
	return fmt.Sprintf("%s|%s", sessionID, normalized[:min(normalized, 200)])
}

func (c *semanticCache) get(key string) (string, bool) {
	c.mu.RLock()
	e, ok := c.entries[key]
	c.mu.RUnlock()
	if !ok || time.Now().After(e.expiresAt) {
		return "", false
	}
	return e.value, true
}

func (c *semanticCache) set(key, value string) {
	c.mu.Lock()
	c.entries[key] = cacheEntry{value: value, expiresAt: time.Now().Add(c.ttl)}
	c.mu.Unlock()
}

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

func estimateTokens(text string) int {
	// Rough estimate: ~4 characters per token
	return len(text) / 4
}

func extractSubject(text string) string {
	// Simple heuristic: first capitalized word or noun phrase
	words := strings.Fields(text)
	for _, w := range words {
		if len(w) > 3 && w[0] >= 'A' && w[0] <= 'Z' {
			return strings.Trim(w, ".,!?;:")
		}
	}
	return ""
}

func min(s string, n int) int {
	if len(s) < n {
		return len(s)
	}
	return n
}
