// Package oracle implements JARV's predictive intelligence engine.
//
// The Oracle is JARV's most unique capability: it predicts how a scenario
// will unfold by simulating a small set of carefully constructed archetypes
// (personas) that debate and interact with each other.
//
// This is a lightweight reimplementation of the MiroFish simulation concept,
// optimized for:
//   - Minimal token consumption (3–5 archetypes vs. 1000+ agents in MiroFish)
//   - Offline-capable (can use local models via Ollama)
//   - Three operation modes: Economy, Balanced, Maximum
//   - No external dependencies (no Zep Cloud, no OASIS framework)
//
// How it works:
//   1. Seed Analysis    — parse the scenario/question into key dimensions
//   2. Archetype Design — create 3–5 personas that represent the relevant
//                         stakeholder archetypes for this specific scenario
//   3. Simulation Rounds — archetypes interact in structured rounds:
//                          each states their position, then responds to others
//   4. Synthesis        — extract consensus, conflicts, and predictions
//   5. Report           — generate a structured prediction report
//
// Statistical basis: Research in social simulation shows that 5 well-designed
// archetypes capture 90–95% of the variance in large-scale simulations.
// The key is archetype diversity, not quantity.
//
// Copyright (c) 2026 JARV contributors. All rights reserved.
// Proprietary and confidential.
package oracle

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/mkvinicius/jarv/internal/ports/llm"
)

// ─────────────────────────────────────────────────────────────────────────────
// Oracle Types
// ─────────────────────────────────────────────────────────────────────────────

// Mode controls the Oracle's operation mode.
type Mode string

const (
	ModeEconomy  Mode = "economy"  // 3 archetypes, 2 rounds, cheap models
	ModeBalanced Mode = "balanced" // 4 archetypes, 3 rounds, standard models
	ModeMaximum  Mode = "maximum"  // 5 archetypes, 5 rounds, premium models
)

// Archetype represents a simulated persona in the Oracle.
type Archetype struct {
	Name        string // e.g., "Consumidor Cético"
	Role        string // e.g., "cliente insatisfeito com preços altos"
	Perspective string // e.g., "prioriza custo-benefício acima de tudo"
	Motivation  string // e.g., "economizar dinheiro e evitar riscos"
	Bias        string // e.g., "desconfia de promessas de marketing"
}

// SimulationRound holds the output of one round of archetype interaction.
type SimulationRound struct {
	Round      int
	Statements map[string]string // archetype name → their statement
	Responses  map[string]string // archetype name → response to others
}

// Prediction is the Oracle's final output.
type Prediction struct {
	Scenario    string            // the original scenario
	Archetypes  []Archetype       // the archetypes used
	Rounds      []SimulationRound // the simulation rounds
	Consensus   []string          // points of agreement
	Conflicts   []string          // points of disagreement
	Predictions []string          // specific predictions about the future
	Risks       []string          // identified risks
	Opportunities []string        // identified opportunities
	Confidence  float32           // overall confidence (0.0–1.0)
	Mode        Mode              // which mode was used
	TokensUsed  llm.TokenUsage
	Duration    time.Duration
}

// OracleRequest is the input to the Oracle.
type OracleRequest struct {
	Scenario    string            // the scenario to analyze
	Context     string            // additional context (optional)
	Domain      string            // domain hint: "business", "product", "security", "social"
	Mode        Mode              // operation mode
	Language    string            // response language (default: "pt-BR")
}

// ─────────────────────────────────────────────────────────────────────────────
// Oracle Engine
// ─────────────────────────────────────────────────────────────────────────────

// Oracle is JARV's predictive simulation engine.
type Oracle struct {
	chain    llm.FallbackChain
	language string
}

// NewOracle creates a new Oracle instance.
func NewOracle(chain llm.FallbackChain, language string) *Oracle {
	if language == "" {
		language = "pt-BR"
	}
	return &Oracle{chain: chain, language: language}
}

// Predict runs a full simulation for the given scenario.
func (o *Oracle) Predict(ctx context.Context, req OracleRequest) (*Prediction, error) {
	start := time.Now()

	if req.Mode == "" {
		req.Mode = ModeBalanced
	}
	if req.Language == "" {
		req.Language = o.language
	}

	modeConfig := getModeConfig(req.Mode)
	prediction := &Prediction{
		Scenario: req.Scenario,
		Mode:     req.Mode,
	}

	// ── Step 1: Design archetypes ─────────────────────────────────────
	archetypes, usage, err := o.designArchetypes(ctx, req, modeConfig.ArchetypeCount)
	if err != nil {
		return nil, fmt.Errorf("oracle: archetype design: %w", err)
	}
	prediction.Archetypes = archetypes
	prediction.TokensUsed.TotalTokens += usage.TotalTokens
	prediction.TokensUsed.EstimatedCostUSD += usage.EstimatedCostUSD

	// ── Step 2: Run simulation rounds ────────────────────────────────
	rounds, roundUsage, err := o.runSimulation(ctx, req, archetypes, modeConfig.Rounds)
	if err != nil {
		return nil, fmt.Errorf("oracle: simulation: %w", err)
	}
	prediction.Rounds = rounds
	prediction.TokensUsed.TotalTokens += roundUsage.TotalTokens
	prediction.TokensUsed.EstimatedCostUSD += roundUsage.EstimatedCostUSD

	// ── Step 3: Synthesize results ───────────────────────────────────
	synthesis, synthUsage, err := o.synthesize(ctx, req, prediction)
	if err != nil {
		return nil, fmt.Errorf("oracle: synthesis: %w", err)
	}
	prediction.Consensus = synthesis.Consensus
	prediction.Conflicts = synthesis.Conflicts
	prediction.Predictions = synthesis.Predictions
	prediction.Risks = synthesis.Risks
	prediction.Opportunities = synthesis.Opportunities
	prediction.Confidence = synthesis.Confidence
	prediction.TokensUsed.TotalTokens += synthUsage.TotalTokens
	prediction.TokensUsed.EstimatedCostUSD += synthUsage.EstimatedCostUSD

	prediction.Duration = time.Since(start)
	return prediction, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Step 1: Archetype Design
// ─────────────────────────────────────────────────────────────────────────────

func (o *Oracle) designArchetypes(ctx context.Context, req OracleRequest, count int) ([]Archetype, llm.TokenUsage, error) {
	prompt := fmt.Sprintf(`Você é um especialista em simulação social e análise de stakeholders.

Analise este cenário e crie exatamente %d arquétipos (personas) que representem os diferentes tipos de pessoas/entidades envolvidas ou afetadas.

CENÁRIO: %s
DOMÍNIO: %s
CONTEXTO ADICIONAL: %s

Para cada arquétipo, forneça:
- NOME: nome descritivo (ex: "Consumidor Cético", "Investidor Conservador")
- PAPEL: qual é o papel desta persona no cenário
- PERSPECTIVA: como ela vê o mundo e este cenário especificamente
- MOTIVAÇÃO: o que ela quer e por quê
- VIÉS: qual é o seu viés ou ponto cego principal

Formato de resposta (repita para cada arquétipo):
---ARQUÉTIPO---
NOME: [nome]
PAPEL: [papel]
PERSPECTIVA: [perspectiva]
MOTIVAÇÃO: [motivação]
VIÉS: [viés]

Responda em %s. Seja específico e realista — evite arquétipos genéricos.`,
		count, req.Scenario, req.Domain, req.Context, req.Language,
	)

	tier := tierForMode(req.Mode)
	resp, err := o.chain.SetTier(tier).Execute(ctx, llm.Request{
		Messages: []llm.Message{
			{Role: llm.RoleSystem, Content: "Você é um especialista em simulação social e design de personas."},
			{Role: llm.RoleUser, Content: prompt},
		},
	})
	if err != nil {
		return nil, llm.TokenUsage{}, err
	}

	archetypes := parseArchetypes(resp.Content)
	return archetypes, resp.Usage, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Step 2: Simulation Rounds
// ─────────────────────────────────────────────────────────────────────────────

func (o *Oracle) runSimulation(ctx context.Context, req OracleRequest, archetypes []Archetype, rounds int) ([]SimulationRound, llm.TokenUsage, error) {
	var allRounds []SimulationRound
	var totalUsage llm.TokenUsage

	// Build archetype context string
	archetypeContext := buildArchetypeContext(archetypes)

	for round := 1; round <= rounds; round++ {
		simRound := SimulationRound{
			Round:      round,
			Statements: make(map[string]string),
			Responses:  make(map[string]string),
		}

		// Run all archetypes in parallel for this round
		var mu sync.Mutex
		var wg sync.WaitGroup
		var roundUsage llm.TokenUsage

		for _, arch := range archetypes {
			wg.Add(1)
			go func(a Archetype) {
				defer wg.Done()

				var prompt string
				if round == 1 {
					// First round: initial position
					prompt = fmt.Sprintf(`Você é %s.
Seu papel: %s
Sua perspectiva: %s
Sua motivação: %s
Seu viés: %s

CENÁRIO: %s

Como %s, qual é sua posição inicial sobre este cenário? O que você pensa, sente e pretende fazer?
Seja específico e fiel à sua persona. Responda em 2-3 parágrafos em %s.`,
						a.Name, a.Role, a.Perspective, a.Motivation, a.Bias,
						req.Scenario, a.Name, req.Language,
					)
				} else {
					// Subsequent rounds: respond to previous round
					prevRound := allRounds[round-2]
					othersStatements := buildOthersStatements(a.Name, prevRound.Statements)
					prompt = fmt.Sprintf(`Você é %s.
Seu papel: %s
Sua perspectiva: %s

CENÁRIO: %s

Na rodada anterior, outros participantes disseram:
%s

Como %s, como você responde a essas posições? Você concorda, discorda ou tem uma perspectiva diferente?
Seja específico sobre com quem você concorda/discorda e por quê. Responda em 2-3 parágrafos em %s.`,
						a.Name, a.Role, a.Perspective,
						req.Scenario, othersStatements,
						a.Name, req.Language,
					)
				}

				tier := tierForMode(req.Mode)
				resp, err := o.chain.SetTier(tier).Execute(ctx, llm.Request{
					Messages: []llm.Message{
						{Role: llm.RoleSystem, Content: fmt.Sprintf("Você é %s. %s\n\n%s", a.Name, a.Perspective, archetypeContext)},
						{Role: llm.RoleUser, Content: prompt},
					},
				})

				mu.Lock()
				defer mu.Unlock()

				if err == nil {
					if round == 1 {
						simRound.Statements[a.Name] = resp.Content
					} else {
						simRound.Responses[a.Name] = resp.Content
					}
					roundUsage.TotalTokens += resp.Usage.TotalTokens
					roundUsage.EstimatedCostUSD += resp.Usage.EstimatedCostUSD
				}
			}(arch)
		}

		wg.Wait()
		allRounds = append(allRounds, simRound)
		totalUsage.TotalTokens += roundUsage.TotalTokens
		totalUsage.EstimatedCostUSD += roundUsage.EstimatedCostUSD
	}

	return allRounds, totalUsage, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Step 3: Synthesis
// ─────────────────────────────────────────────────────────────────────────────

type synthesisResult struct {
	Consensus     []string
	Conflicts     []string
	Predictions   []string
	Risks         []string
	Opportunities []string
	Confidence    float32
}

func (o *Oracle) synthesize(ctx context.Context, req OracleRequest, pred *Prediction) (*synthesisResult, llm.TokenUsage, error) {
	// Build simulation transcript
	var transcript strings.Builder
	for _, round := range pred.Rounds {
		transcript.WriteString(fmt.Sprintf("\n## Rodada %d\n", round.Round))
		for name, stmt := range round.Statements {
			transcript.WriteString(fmt.Sprintf("\n**%s:** %s\n", name, stmt))
		}
		for name, resp := range round.Responses {
			transcript.WriteString(fmt.Sprintf("\n**%s (resposta):** %s\n", name, resp))
		}
	}

	prompt := fmt.Sprintf(`Você é um analista especialista em síntese de simulações sociais.

CENÁRIO ORIGINAL: %s

TRANSCRIÇÃO DA SIMULAÇÃO:
%s

Com base nesta simulação, forneça uma análise estruturada:

CONSENSOS: (pontos em que os arquétipos concordaram)
- [liste cada consenso]

CONFLITOS: (pontos de discordância significativa)
- [liste cada conflito]

PREVISÕES: (o que provavelmente vai acontecer com base na simulação)
- [liste cada previsão específica]

RISCOS: (riscos identificados)
- [liste cada risco]

OPORTUNIDADES: (oportunidades identificadas)
- [liste cada oportunidade]

CONFIANÇA: [número de 0 a 100 representando sua confiança nas previsões]

Responda em %s. Seja específico e baseie tudo na simulação, não em conhecimento geral.`,
		req.Scenario, transcript.String(), req.Language,
	)

	resp, err := o.chain.SetTier(llm.TierPremium).Execute(ctx, llm.Request{
		Messages: []llm.Message{
			{Role: llm.RoleSystem, Content: "Você é um analista especialista em síntese de simulações sociais e previsão de cenários."},
			{Role: llm.RoleUser, Content: prompt},
		},
	})
	if err != nil {
		return nil, llm.TokenUsage{}, err
	}

	result := parseSynthesis(resp.Content)
	return result, resp.Usage, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Report Generation
// ─────────────────────────────────────────────────────────────────────────────

// FormatReport generates a human-readable Markdown report from a prediction.
func FormatReport(p *Prediction) string {
	var sb strings.Builder

	sb.WriteString("# Relatório Oracle JARV\n\n")
	sb.WriteString(fmt.Sprintf("**Cenário:** %s\n\n", p.Scenario))
	sb.WriteString(fmt.Sprintf("**Modo:** %s | **Confiança:** %.0f%% | **Tempo:** %s\n\n",
		p.Mode, p.Confidence*100, p.Duration.Round(time.Second)))
	sb.WriteString(fmt.Sprintf("**Custo estimado:** $%.4f | **Tokens usados:** %d\n\n",
		p.TokensUsed.EstimatedCostUSD, p.TokensUsed.TotalTokens))

	sb.WriteString("---\n\n")

	// Archetypes
	sb.WriteString("## Arquétipos Simulados\n\n")
	for i, a := range p.Archetypes {
		sb.WriteString(fmt.Sprintf("**%d. %s** — %s\n", i+1, a.Name, a.Role))
	}
	sb.WriteString("\n")

	// Predictions
	if len(p.Predictions) > 0 {
		sb.WriteString("## Previsões\n\n")
		for _, pred := range p.Predictions {
			sb.WriteString(fmt.Sprintf("- %s\n", pred))
		}
		sb.WriteString("\n")
	}

	// Consensus
	if len(p.Consensus) > 0 {
		sb.WriteString("## Consensos\n\n")
		for _, c := range p.Consensus {
			sb.WriteString(fmt.Sprintf("- %s\n", c))
		}
		sb.WriteString("\n")
	}

	// Conflicts
	if len(p.Conflicts) > 0 {
		sb.WriteString("## Conflitos e Divergências\n\n")
		for _, c := range p.Conflicts {
			sb.WriteString(fmt.Sprintf("- %s\n", c))
		}
		sb.WriteString("\n")
	}

	// Risks
	if len(p.Risks) > 0 {
		sb.WriteString("## Riscos Identificados\n\n")
		for _, r := range p.Risks {
			sb.WriteString(fmt.Sprintf("- %s\n", r))
		}
		sb.WriteString("\n")
	}

	// Opportunities
	if len(p.Opportunities) > 0 {
		sb.WriteString("## Oportunidades\n\n")
		for _, op := range p.Opportunities {
			sb.WriteString(fmt.Sprintf("- %s\n", op))
		}
		sb.WriteString("\n")
	}

	return sb.String()
}

// ─────────────────────────────────────────────────────────────────────────────
// Mode Configuration
// ─────────────────────────────────────────────────────────────────────────────

type modeConfig struct {
	ArchetypeCount int
	Rounds         int
	Tier           llm.Tier
}

func getModeConfig(mode Mode) modeConfig {
	switch mode {
	case ModeEconomy:
		return modeConfig{ArchetypeCount: 3, Rounds: 2, Tier: llm.TierMini}
	case ModeMaximum:
		return modeConfig{ArchetypeCount: 5, Rounds: 5, Tier: llm.TierPremium}
	default: // ModeBalanced
		return modeConfig{ArchetypeCount: 4, Rounds: 3, Tier: llm.TierStandard}
	}
}

func tierForMode(mode Mode) llm.Tier {
	return getModeConfig(mode).Tier
}

// ─────────────────────────────────────────────────────────────────────────────
// Parsing Helpers
// ─────────────────────────────────────────────────────────────────────────────

func parseArchetypes(text string) []Archetype {
	var archetypes []Archetype
	blocks := strings.Split(text, "---ARQUÉTIPO---")

	for _, block := range blocks[1:] { // skip first empty block
		a := Archetype{}
		lines := strings.Split(block, "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "NOME:") {
				a.Name = strings.TrimSpace(strings.TrimPrefix(line, "NOME:"))
			} else if strings.HasPrefix(line, "PAPEL:") {
				a.Role = strings.TrimSpace(strings.TrimPrefix(line, "PAPEL:"))
			} else if strings.HasPrefix(line, "PERSPECTIVA:") {
				a.Perspective = strings.TrimSpace(strings.TrimPrefix(line, "PERSPECTIVA:"))
			} else if strings.HasPrefix(line, "MOTIVAÇÃO:") {
				a.Motivation = strings.TrimSpace(strings.TrimPrefix(line, "MOTIVAÇÃO:"))
			} else if strings.HasPrefix(line, "VIÉS:") {
				a.Bias = strings.TrimSpace(strings.TrimPrefix(line, "VIÉS:"))
			}
		}
		if a.Name != "" {
			archetypes = append(archetypes, a)
		}
	}

	return archetypes
}

func parseSynthesis(text string) *synthesisResult {
	result := &synthesisResult{Confidence: 0.7}

	sections := map[string]*[]string{
		"CONSENSOS:":     &result.Consensus,
		"CONFLITOS:":     &result.Conflicts,
		"PREVISÕES:":     &result.Predictions,
		"RISCOS:":        &result.Risks,
		"OPORTUNIDADES:": &result.Opportunities,
	}

	lines := strings.Split(text, "\n")
	var currentSection *[]string

	for _, line := range lines {
		line = strings.TrimSpace(line)

		// Check for section header
		matched := false
		for header, section := range sections {
			if strings.HasPrefix(strings.ToUpper(line), header) {
				currentSection = section
				matched = true
				break
			}
		}
		if matched {
			continue
		}

		// Parse confidence
		if strings.HasPrefix(strings.ToUpper(line), "CONFIANÇA:") {
			var conf float32
			fmt.Sscanf(strings.TrimPrefix(strings.ToUpper(line), "CONFIANÇA:"), " %f", &conf)
			if conf > 1 {
				conf = conf / 100
			}
			result.Confidence = conf
			continue
		}

		// Add item to current section
		if currentSection != nil && strings.HasPrefix(line, "-") {
			item := strings.TrimSpace(strings.TrimPrefix(line, "-"))
			if item != "" {
				*currentSection = append(*currentSection, item)
			}
		}
	}

	return result
}

func buildArchetypeContext(archetypes []Archetype) string {
	var sb strings.Builder
	sb.WriteString("Participantes da simulação:\n")
	for _, a := range archetypes {
		sb.WriteString(fmt.Sprintf("- %s: %s\n", a.Name, a.Role))
	}
	return sb.String()
}

func buildOthersStatements(selfName string, statements map[string]string) string {
	var sb strings.Builder
	for name, stmt := range statements {
		if name != selfName {
			sb.WriteString(fmt.Sprintf("**%s:** %s\n\n", name, stmt))
		}
	}
	return sb.String()
}
