// Package agent — Swarm Executor and Skills Engine.
//
// The SwarmExecutor enables parallel execution of multiple agents (squads),
// using Go's native goroutines and channels for maximum efficiency.
// Unlike sequential execution, a squad of 4 agents completes in ~1/4 the time.
//
// The SkillsEngine manages SKILL.md files — the knowledge units that make
// agents specialists in specific domains. It handles loading, validation,
// QA checking, and injection into agent system prompts.
//
// Copyright (c) 2026 JARV contributors. All rights reserved.
// Proprietary and confidential.
package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// Squad Definition
// ─────────────────────────────────────────────────────────────────────────────

// AgentSpec defines a single agent in a squad.
type AgentSpec struct {
	ID          string            // unique identifier within the squad
	Name        string            // display name
	Persona     string            // system prompt persona
	Skills      []string          // skill IDs to inject
	Tools       []string          // tool names this agent can use
	DependsOn   []string          // IDs of agents that must complete first
	Metadata    map[string]string // arbitrary metadata
}

// SquadSpec defines a squad of agents that work together.
type SquadSpec struct {
	ID          string      // unique squad identifier
	Name        string      // display name
	Description string      // what this squad does
	Agents      []AgentSpec // the agents in this squad
	MaxParallel int         // max agents running simultaneously (0 = unlimited)
}

// AgentResult holds the output of a single agent execution.
type AgentResult struct {
	AgentID  string
	Response *Response
	Error    error
	Duration time.Duration
}

// SwarmResult holds the combined output of a squad execution.
type SwarmResult struct {
	SquadID  string
	Results  []AgentResult
	Combined string        // synthesized final response
	Duration time.Duration
	Errors   []error
}

// ─────────────────────────────────────────────────────────────────────────────
// Swarm Executor
// ─────────────────────────────────────────────────────────────────────────────

// SwarmExecutor orchestrates parallel execution of agent squads.
// It respects dependency ordering and runs independent agents in parallel.
type SwarmExecutor struct {
	engine *Engine
	mu     sync.RWMutex
	squads map[string]*SquadSpec
}

// NewSwarmExecutor creates a new SwarmExecutor backed by the given engine.
func NewSwarmExecutor(engine *Engine) *SwarmExecutor {
	return &SwarmExecutor{
		engine: engine,
		squads: make(map[string]*SquadSpec),
	}
}

// RegisterSquad adds a squad to the executor's registry.
func (s *SwarmExecutor) RegisterSquad(spec *SquadSpec) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.squads[spec.ID] = spec
}

// Execute runs a squad for the given request.
// Agents with no dependencies run in parallel; dependent agents wait.
func (s *SwarmExecutor) Execute(ctx context.Context, squadID string, req Request) (*SwarmResult, error) {
	s.mu.RLock()
	spec, ok := s.squads[squadID]
	s.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("swarm: squad not found: %s", squadID)
	}

	start := time.Now()
	result := &SwarmResult{SquadID: squadID}

	// Build dependency graph
	completed := make(map[string]*AgentResult)
	var completedMu sync.Mutex

	// Topological execution: process agents in waves
	waves := buildExecutionWaves(spec.Agents)

	for _, wave := range waves {
		// Run all agents in this wave in parallel
		var wg sync.WaitGroup
		waveResults := make([]AgentResult, len(wave))

		// Respect MaxParallel limit
		maxParallel := spec.MaxParallel
		if maxParallel <= 0 || maxParallel > len(wave) {
			maxParallel = len(wave)
		}

		sem := make(chan struct{}, maxParallel)

		for i, agentSpec := range wave {
			wg.Add(1)
			go func(idx int, aSpec AgentSpec) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()

				agentStart := time.Now()

				// Build agent-specific request
				agentReq := req
				agentReq.SquadID = aSpec.ID

				// Inject context from completed dependencies
				if len(aSpec.DependsOn) > 0 {
					completedMu.Lock()
					var depContext strings.Builder
					for _, depID := range aSpec.DependsOn {
						if depResult, ok := completed[depID]; ok && depResult.Response != nil {
							depContext.WriteString(fmt.Sprintf("[%s]: %s\n\n", depID, depResult.Response.Text))
						}
					}
					completedMu.Unlock()

					if depContext.Len() > 0 {
						agentReq.Text = fmt.Sprintf("Contexto de agentes anteriores:\n%s\n\nTarefa atual: %s",
							depContext.String(), req.Text)
					}
				}

				// Override engine config for this agent
				agentEngine := s.buildAgentEngine(aSpec)
				resp, err := agentEngine.Process(ctx, agentReq)

				ar := AgentResult{
					AgentID:  aSpec.ID,
					Response: resp,
					Error:    err,
					Duration: time.Since(agentStart),
				}
				waveResults[idx] = ar

				completedMu.Lock()
				completed[aSpec.ID] = &ar
				completedMu.Unlock()
			}(i, agentSpec)
		}

		wg.Wait()
		result.Results = append(result.Results, waveResults...)
	}

	// Collect errors
	for _, r := range result.Results {
		if r.Error != nil {
			result.Errors = append(result.Errors, r.Error)
		}
	}

	// Synthesize combined response
	result.Combined = s.synthesize(result.Results)
	result.Duration = time.Since(start)

	return result, nil
}

// buildAgentEngine creates a temporary engine instance for a specific agent.
func (s *SwarmExecutor) buildAgentEngine(spec AgentSpec) *Engine {
	cfg := s.engine.cfg
	if spec.Persona != "" {
		cfg.Persona = spec.Persona
	}
	cfg.Name = spec.Name

	return &Engine{
		cfg:      cfg,
		chain:    s.engine.chain,
		router:   s.engine.router,
		memory:   s.engine.memory,
		graph:    s.engine.graph,
		sessions: s.engine.sessions,
		tools:    s.engine.tools,
		cache:    s.engine.cache,
	}
}

// synthesize combines multiple agent responses into a coherent final response.
func (s *SwarmExecutor) synthesize(results []AgentResult) string {
	if len(results) == 0 {
		return ""
	}
	if len(results) == 1 {
		if results[0].Response != nil {
			return results[0].Response.Text
		}
		return ""
	}

	var sb strings.Builder
	for _, r := range results {
		if r.Error != nil || r.Response == nil {
			continue
		}
		sb.WriteString(fmt.Sprintf("**%s:**\n%s\n\n", r.AgentID, r.Response.Text))
	}
	return sb.String()
}

// buildExecutionWaves groups agents into execution waves based on dependencies.
// Agents with no dependencies (or whose dependencies are in earlier waves) run together.
func buildExecutionWaves(agents []AgentSpec) [][]AgentSpec {
	if len(agents) == 0 {
		return nil
	}

	// Build dependency map
	agentMap := make(map[string]AgentSpec)
	for _, a := range agents {
		agentMap[a.ID] = a
	}

	var waves [][]AgentSpec
	completed := make(map[string]bool)

	for len(completed) < len(agents) {
		var wave []AgentSpec
		for _, a := range agents {
			if completed[a.ID] {
				continue
			}
			// Check if all dependencies are completed
			allDepsCompleted := true
			for _, dep := range a.DependsOn {
				if !completed[dep] {
					allDepsCompleted = false
					break
				}
			}
			if allDepsCompleted {
				wave = append(wave, a)
			}
		}

		if len(wave) == 0 {
			// Circular dependency or unresolvable — add remaining agents
			for _, a := range agents {
				if !completed[a.ID] {
					wave = append(wave, a)
				}
			}
		}

		for _, a := range wave {
			completed[a.ID] = true
		}
		waves = append(waves, wave)
	}

	return waves
}

// ─────────────────────────────────────────────────────────────────────────────
// Skills Engine
// ─────────────────────────────────────────────────────────────────────────────

// Skill represents a loaded SKILL.md file.
type Skill struct {
	ID          string
	Name        string
	Description string
	Triggers    []string
	Content     string // full SKILL.md content
	Path        string // file path
	LoadedAt    time.Time
}

// SkillQAResult holds the result of a skill quality check.
type SkillQAResult struct {
	Passed   bool
	Score    int // 0–10
	Issues   []string
	Warnings []string
}

// SkillsEngine manages SKILL.md files for JARV agents.
type SkillsEngine struct {
	skills    map[string]*Skill
	skillDirs []string
	mu        sync.RWMutex
}

// NewSkillsEngine creates a new SkillsEngine that loads skills from the given directories.
func NewSkillsEngine(skillDirs ...string) *SkillsEngine {
	return &SkillsEngine{
		skills:    make(map[string]*Skill),
		skillDirs: skillDirs,
	}
}

// LoadAll scans all skill directories and loads valid SKILL.md files.
func (e *SkillsEngine) LoadAll() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	for _, dir := range e.skillDirs {
		if err := e.loadDir(dir); err != nil {
			// Non-fatal: log and continue
			continue
		}
	}
	return nil
}

func (e *SkillsEngine) loadDir(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		skillPath := filepath.Join(dir, entry.Name(), "SKILL.md")
		content, err := os.ReadFile(skillPath)
		if err != nil {
			continue
		}

		skill := parseSkillMD(entry.Name(), string(content), skillPath)
		if skill != nil {
			e.skills[skill.ID] = skill
		}
	}

	return nil
}

// Get retrieves a skill by ID.
func (e *SkillsEngine) Get(id string) (*Skill, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	s, ok := e.skills[id]
	return s, ok
}

// FindByTrigger finds skills that match a given user message.
func (e *SkillsEngine) FindByTrigger(message string) []*Skill {
	e.mu.RLock()
	defer e.mu.RUnlock()

	lower := strings.ToLower(message)
	var matches []*Skill

	for _, skill := range e.skills {
		for _, trigger := range skill.Triggers {
			if strings.Contains(lower, strings.ToLower(trigger)) {
				matches = append(matches, skill)
				break
			}
		}
	}

	return matches
}

// InjectIntoPrompt returns the skill content formatted for injection into a system prompt.
func (e *SkillsEngine) InjectIntoPrompt(skillIDs []string) string {
	if len(skillIDs) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("\n## Skills Ativas\n\n")

	for _, id := range skillIDs {
		skill, ok := e.Get(id)
		if !ok {
			continue
		}
		sb.WriteString(fmt.Sprintf("### %s\n%s\n\n", skill.Name, skill.Content))
	}

	return sb.String()
}

// QACheck validates a SKILL.md content against JARV's quality standards.
// Returns a QAResult with score (0–10) and specific issues found.
func QACheck(content string) SkillQAResult {
	result := SkillQAResult{Score: 10}

	// Check 1: Has name
	if !strings.Contains(content, "# ") {
		result.Issues = append(result.Issues, "Falta título (# Nome da Skill)")
		result.Score -= 2
	}

	// Check 2: Has description with minimum length
	descIdx := strings.Index(strings.ToLower(content), "description")
	if descIdx < 0 || len(content[descIdx:]) < 50 {
		result.Issues = append(result.Issues, "Descrição muito curta (mínimo 50 palavras)")
		result.Score -= 2
	}

	// Check 3: Has triggers
	if !strings.Contains(strings.ToLower(content), "trigger") {
		result.Issues = append(result.Issues, "Falta seção de triggers (palavras que ativam a skill)")
		result.Score -= 1
	}

	// Check 4: Has steps/instructions
	hasSteps := strings.Contains(content, "## ") || strings.Contains(content, "1.") || strings.Contains(content, "- ")
	if !hasSteps {
		result.Issues = append(result.Issues, "Falta passos ou instruções estruturadas")
		result.Score -= 2
	}

	// Check 5: Has examples
	if !strings.Contains(strings.ToLower(content), "exemplo") && !strings.Contains(strings.ToLower(content), "example") {
		result.Warnings = append(result.Warnings, "Recomendado: adicionar exemplos concretos de input/output")
		result.Score -= 1
	}

	// Check 6: No vague language
	vagueTerms := []string{"handle appropriately", "as needed", "when necessary", "if applicable"}
	for _, term := range vagueTerms {
		if strings.Contains(strings.ToLower(content), term) {
			result.Issues = append(result.Issues, fmt.Sprintf("Linguagem vaga detectada: '%s'", term))
			result.Score--
		}
	}

	// Check 7: No hardcoded credentials
	credentialPatterns := []string{"sk-", "api_key=", "password=", "secret=", "token="}
	for _, pattern := range credentialPatterns {
		if strings.Contains(strings.ToLower(content), pattern) {
			result.Issues = append(result.Issues, "ALERTA: Possível credencial hardcoded detectada!")
			result.Score -= 3
		}
	}

	// Check 8: Minimum content length
	wordCount := len(strings.Fields(content))
	if wordCount < 100 {
		result.Issues = append(result.Issues, fmt.Sprintf("Conteúdo muito curto (%d palavras, mínimo 100)", wordCount))
		result.Score -= 1
	}

	if result.Score < 0 {
		result.Score = 0
	}
	result.Passed = result.Score >= 6 && len(result.Issues) == 0
	return result
}

// parseSkillMD extracts skill metadata from a SKILL.md file.
func parseSkillMD(id, content, path string) *Skill {
	skill := &Skill{
		ID:       id,
		Content:  content,
		Path:     path,
		LoadedAt: time.Now(),
	}

	lines := strings.Split(content, "\n")
	for i, line := range lines {
		line = strings.TrimSpace(line)

		// Extract name from first H1
		if strings.HasPrefix(line, "# ") && skill.Name == "" {
			skill.Name = strings.TrimPrefix(line, "# ")
			continue
		}

		// Extract description
		if strings.HasPrefix(strings.ToLower(line), "description:") {
			skill.Description = strings.TrimSpace(strings.TrimPrefix(line, "description:"))
			if skill.Description == "" && i+1 < len(lines) {
				skill.Description = strings.TrimSpace(lines[i+1])
			}
			continue
		}

		// Extract triggers
		if strings.HasPrefix(strings.ToLower(line), "triggers:") || strings.HasPrefix(strings.ToLower(line), "## triggers") {
			// Collect trigger lines until next section
			for j := i + 1; j < len(lines) && j < i+20; j++ {
				tLine := strings.TrimSpace(lines[j])
				if strings.HasPrefix(tLine, "##") {
					break
				}
				if strings.HasPrefix(tLine, "-") {
					trigger := strings.TrimSpace(strings.TrimPrefix(tLine, "-"))
					if trigger != "" {
						skill.Triggers = append(skill.Triggers, trigger)
					}
				}
			}
		}
	}

	if skill.Name == "" {
		skill.Name = id
	}

	return skill
}
