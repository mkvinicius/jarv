// JARV — Just A Rather Very intelligent agent
//
// Commands:
//
//	jarv setup              — first-time setup wizard
//	jarv start              — start interactive agent loop
//	jarv chat <text>        — single message, print response and exit
//	jarv oracle <scenario>  — Oracle predictive analysis
//	jarv status             — show system status
//	jarv skills list        — list installed skills
//	jarv skills install <url> — install a skill from URL
//	jarv skills remove <name> — remove a skill
//	jarv cron list          — list scheduled tasks
//	jarv cron add           — add a scheduled task (interactive)
//	jarv cron remove <id>   — remove a scheduled task
//	jarv version            — print version
//
// Copyright (c) 2026 JARV contributors. All rights reserved.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/mkvinicius/jarv/internal/adapters/channel/cli"
	llmAnthropic "github.com/mkvinicius/jarv/internal/adapters/llm/anthropic"
	llmOllama "github.com/mkvinicius/jarv/internal/adapters/llm/ollama"
	llmOpenAI "github.com/mkvinicius/jarv/internal/adapters/llm/openai"
	storagePkg "github.com/mkvinicius/jarv/internal/adapters/storage"
	"github.com/mkvinicius/jarv/internal/core/agent"
	"github.com/mkvinicius/jarv/internal/core/reasoning"
	"github.com/mkvinicius/jarv/internal/core/scheduler"
	"github.com/mkvinicius/jarv/internal/core/skills"
	"github.com/mkvinicius/jarv/internal/ports/channel"
	"github.com/mkvinicius/jarv/internal/ports/llm"
	"github.com/mkvinicius/jarv/internal/tools/websearch"
)

const version = "0.2.0"

// ─────────────────────────────────────────────────────────────────────────────
// Config — inspired by PicoClaw's model_list format
// ─────────────────────────────────────────────────────────────────────────────

// Config is persisted at ~/.jarv/config.json
type Config struct {
	Name      string        `json:"name"`
	Language  string        `json:"language"`
	ModelList []ModelConfig `json:"model_list"`
	Tools     ToolsConfig   `json:"tools"`
	Cron      []scheduler.Task `json:"cron,omitempty"`

	// Legacy fields (kept for backwards compat with v0.1.0)
	Mode    string    `json:"mode,omitempty"`
	Online  OnlineCfg `json:"online,omitempty"`
	Offline OfflineCfg `json:"offline,omitempty"`
}

// ModelConfig defines a single model in the model_list.
type ModelConfig struct {
	Name     string `json:"name"`       // display name, e.g. "gpt-4o-mini"
	Model    string `json:"model"`      // actual model id, e.g. "openai/gpt-4o-mini"
	Provider string `json:"provider"`   // "openai", "anthropic", "ollama"
	APIKey   string `json:"api_key,omitempty"`
	BaseURL  string `json:"base_url,omitempty"` // for custom endpoints / Ollama
	Tier     string `json:"tier,omitempty"`     // "nano", "mini", "standard", "premium"
	Priority int    `json:"priority,omitempty"` // lower = tried first
}

// ToolsConfig configures available tools.
type ToolsConfig struct {
	WebSearch WebSearchConfig `json:"web_search"`
}

// WebSearchConfig selects which search backend to use.
type WebSearchConfig struct {
	DuckDuckGo struct {
		Enabled bool `json:"enabled"`
	} `json:"duckduckgo"`
	Brave struct {
		Enabled bool   `json:"enabled"`
		APIKey  string `json:"api_key,omitempty"`
	} `json:"brave"`
	Tavily struct {
		Enabled bool   `json:"enabled"`
		APIKey  string `json:"api_key,omitempty"`
	} `json:"tavily"`
}

// Legacy fields kept for v0.1.0 config compatibility
type OnlineCfg struct {
	Provider       string `json:"provider,omitempty"`
	OpenAIKey      string `json:"openai_key,omitempty"`
	OpenAIModel    string `json:"openai_model,omitempty"`
	AnthropicKey   string `json:"anthropic_key,omitempty"`
	AnthropicModel string `json:"anthropic_model,omitempty"`
}

type OfflineCfg struct {
	OllamaURL string `json:"ollama_url,omitempty"`
	Model     string `json:"model,omitempty"`
}

func configPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".jarv", "config.json")
}

func loadConfig() (*Config, error) {
	data, err := os.ReadFile(configPath())
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	// Migrate legacy v0.1.0 config
	if len(cfg.ModelList) == 0 {
		cfg.ModelList = migrateOldConfig(&cfg)
	}
	return &cfg, nil
}

func saveConfig(cfg *Config) error {
	path := configPath()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

func migrateOldConfig(cfg *Config) []ModelConfig {
	if cfg.Mode == "offline" {
		url := cfg.Offline.OllamaURL
		if url == "" {
			url = "http://localhost:11434"
		}
		return []ModelConfig{{
			Name:     cfg.Offline.Model,
			Model:    cfg.Offline.Model,
			Provider: "ollama",
			BaseURL:  url,
			Tier:     "standard",
			Priority: 1,
		}}
	}
	switch cfg.Online.Provider {
	case "anthropic":
		return []ModelConfig{{
			Name:     cfg.Online.AnthropicModel,
			Model:    cfg.Online.AnthropicModel,
			Provider: "anthropic",
			APIKey:   cfg.Online.AnthropicKey,
			Tier:     "standard",
			Priority: 1,
		}}
	default:
		return []ModelConfig{{
			Name:     cfg.Online.OpenAIModel,
			Model:    cfg.Online.OpenAIModel,
			Provider: "openai",
			APIKey:   cfg.Online.OpenAIKey,
			Tier:     "standard",
			Priority: 1,
		}}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Engine builder
// ─────────────────────────────────────────────────────────────────────────────

func buildEngine(cfg *Config, skillsMgr *skills.Manager) (*agent.Engine, error) {
	if len(cfg.ModelList) == 0 {
		return nil, fmt.Errorf("model_list vazio. Execute 'jarv setup'")
	}

	chain := reasoning.NewFallbackChain()

	for _, m := range cfg.ModelList {
		model := m.Model
		if model == "" {
			model = m.Name
		}
		tier := parseTier(m.Tier)
		priority := m.Priority

		switch m.Provider {
		case "anthropic":
			if m.APIKey == "" {
				continue
			}
			p := llmAnthropic.New(m.APIKey, model)
			chain.Add(llm.Candidate{Provider: p, Model: model, Tier: tier, Priority: priority})
		case "ollama":
			baseURL := m.BaseURL
			if baseURL == "" {
				baseURL = "http://localhost:11434"
			}
			p := llmOllama.NewWithURL(baseURL, model)
			chain.Add(llm.Candidate{Provider: p, Model: model, Tier: tier, Priority: priority})
		default: // openai or openai-compatible
			if m.APIKey == "" {
				continue
			}
			var p *llmOpenAI.Provider
			if m.BaseURL != "" {
				p = llmOpenAI.NewWithBaseURL(m.APIKey, model, m.BaseURL)
			} else {
				p = llmOpenAI.New(m.APIKey, model)
			}
			chain.Add(llm.Candidate{Provider: p, Model: model, Tier: tier, Priority: priority})
		}
	}

	router := reasoning.NewSmartRouter("balanced")
	store := storagePkg.NewInMemoryStore(uuid.NewString())
	graph := storagePkg.NewInMemoryGraph()
	sessions := storagePkg.NewInMemorySessionStore()
	tools := agent.NewToolRegistry()

	// Register web search tools
	registerSearchTools(tools, cfg)

	engineCfg := agent.DefaultEngineConfig()
	engineCfg.Name = cfg.Name
	if cfg.Language != "" {
		engineCfg.Language = cfg.Language
	}

	persona := fmt.Sprintf(
		"Você é %s, um assistente de inteligência artificial avançado. "+
			"Seja preciso, eficiente e sempre honesto.",
		cfg.Name,
	)
	if skillsMgr != nil {
		persona = skillsMgr.InjectIntoPrompt(persona)
	}
	engineCfg.Persona = persona

	return agent.NewEngine(engineCfg, chain, router, store, graph, sessions, tools), nil
}

func registerSearchTools(tools *agent.ToolRegistry, cfg *Config) {
	ws := cfg.Tools.WebSearch
	if ws.Tavily.Enabled && ws.Tavily.APIKey != "" {
		tools.Register(websearch.NewTavilySearch(ws.Tavily.APIKey))
		return
	}
	if ws.Brave.Enabled && ws.Brave.APIKey != "" {
		tools.Register(websearch.NewBraveSearch(ws.Brave.APIKey))
		return
	}
	if ws.DuckDuckGo.Enabled {
		tools.Register(websearch.NewDDGSearch())
	}
}

func parseTier(s string) llm.Tier {
	switch strings.ToLower(s) {
	case "nano":
		return llm.TierNano
	case "mini":
		return llm.TierMini
	case "premium":
		return llm.TierPremium
	default:
		return llm.TierStandard
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Setup wizard
// ─────────────────────────────────────────────────────────────────────────────

var offlineModels = []struct{ Key, Name, Size, Desc string }{
	{"llama3.2:3b", "Llama 3.2 3B", "~2 GB", "Rápido, ideal para conversas"},
	{"llama3.1:8b", "Llama 3.1 8B", "~5 GB", "Uso geral, boa qualidade"},
	{"mistral:7b", "Mistral 7B", "~4 GB", "Excelente para código"},
	{"qwen2.5:14b", "Qwen 2.5 14B", "~9 GB", "Alta qualidade (GPU recomendada)"},
	{"deepseek-r1:7b", "DeepSeek-R1 7B", "~5 GB", "Forte em raciocínio lógico"},
}

func cmdSetup() {
	sc := bufio.NewScanner(os.Stdin)
	ask := func(prompt, def string) string {
		if def != "" {
			fmt.Fprintf(os.Stdout, "%s [%s]: ", prompt, def)
		} else {
			fmt.Fprintf(os.Stdout, "%s: ", prompt)
		}
		if !sc.Scan() {
			return def
		}
		v := strings.TrimSpace(sc.Text())
		if v == "" {
			return def
		}
		return v
	}

	fmt.Println()
	fmt.Println("╔══════════════════════════════════════════╗")
	fmt.Println("║        JARV v" + version + " — Setup Wizard          ║")
	fmt.Println("╚══════════════════════════════════════════╝")
	fmt.Println()

	cfg := &Config{Name: "JARV", Language: "pt-BR"}
	cfg.Tools.WebSearch.DuckDuckGo.Enabled = true // on by default

	cfg.Name = ask("Nome do assistente", cfg.Name)
	cfg.Language = ask("Idioma (pt-BR / en-US)", cfg.Language)

	fmt.Println()
	fmt.Println("Você pode adicionar múltiplos modelos (model_list).")
	fmt.Println("O JARV tentará cada um em ordem de prioridade.")
	fmt.Println()

	addMore := true
	priority := 1
	for addMore {
		fmt.Println("Provedor:")
		fmt.Println("  1) OpenAI    (GPT-4o, GPT-4o-mini)")
		fmt.Println("  2) Anthropic (Claude Sonnet, Haiku)")
		fmt.Println("  3) Ollama    (modelos locais, offline)")
		fmt.Println("  4) Outro     (OpenAI-compatible: Groq, Together, etc.)")
		prov := ask("Escolha (1-4)", "3")

		mc := ModelConfig{Priority: priority}
		priority++

		switch prov {
		case "1":
			mc.Provider = "openai"
			mc.APIKey = ask("OpenAI API Key", "")
			mc.Model = ask("Modelo", "gpt-4o-mini")
			mc.Name = mc.Model
			mc.Tier = ask("Tier (nano/mini/standard/premium)", "standard")
		case "2":
			mc.Provider = "anthropic"
			mc.APIKey = ask("Anthropic API Key", "")
			mc.Model = ask("Modelo", "claude-sonnet-4-6")
			mc.Name = mc.Model
			mc.Tier = ask("Tier", "standard")
		case "4":
			mc.Provider = "openai"
			mc.BaseURL = ask("Base URL (ex: https://api.groq.com/openai/v1)", "")
			mc.APIKey = ask("API Key", "")
			mc.Model = ask("Modelo", "")
			mc.Name = mc.Model
			mc.Tier = ask("Tier", "standard")
		default: // ollama
			mc.Provider = "ollama"
			mc.BaseURL = ask("Ollama URL", "http://localhost:11434")

			ollamaOK := llmOllama.NewWithURL(mc.BaseURL, "").Healthy(context.Background())
			if !ollamaOK {
				fmt.Println("\n⚠️  Ollama não encontrado. Instale em https://ollama.com")
			} else {
				fmt.Println("\n✓  Ollama está rodando.")
			}

			fmt.Println("\nEscolha o modelo:")
			for i, m := range offlineModels {
				fmt.Printf("  %d) %-20s %-8s — %s\n", i+1, m.Name, m.Size, m.Desc)
			}
			choice := ask("Modelo (1-5)", "1")
			idx := 0
			if n := int(choice[0] - '1'); n >= 0 && n < len(offlineModels) {
				idx = n
			}
			mc.Model = offlineModels[idx].Key
			mc.Name = offlineModels[idx].Name
			mc.Tier = "standard"

			if ollamaOK {
				if ask("Baixar modelo agora? (s/n)", "s") == "s" {
					fmt.Printf("Baixando %s em segundo plano...\n", mc.Model)
					progress := make(chan string, 16)
					ctx, cancel := context.WithCancel(context.Background())
					defer cancel()
					go func() {
						_ = llmOllama.PullModel(ctx, mc.BaseURL, mc.Model, progress)
					}()
					timeout := time.After(6 * time.Second)
					for {
						select {
						case msg, ok := <-progress:
							if !ok {
								goto done
							}
							fmt.Printf("  → %s\n", msg)
						case <-timeout:
							fmt.Println("  (download continuando em segundo plano...)")
							goto done
						}
					}
				done:
				}
			}
		}
		cfg.ModelList = append(cfg.ModelList, mc)

		addMore = ask("\nAdicionar outro modelo? (s/n)", "n") == "s"
	}

	// Web search setup
	fmt.Println()
	fmt.Println("Busca web:")
	fmt.Println("  1) DuckDuckGo (grátis, sem API key)")
	fmt.Println("  2) Brave Search (pago, melhor qualidade)")
	fmt.Println("  3) Tavily (1000 consultas/mês grátis)")
	fmt.Println("  4) Desativada")
	wsChoice := ask("Backend de busca (1-4)", "1")
	switch wsChoice {
	case "2":
		cfg.Tools.WebSearch.DuckDuckGo.Enabled = false
		cfg.Tools.WebSearch.Brave.Enabled = true
		cfg.Tools.WebSearch.Brave.APIKey = ask("Brave API Key", "")
	case "3":
		cfg.Tools.WebSearch.DuckDuckGo.Enabled = false
		cfg.Tools.WebSearch.Tavily.Enabled = true
		cfg.Tools.WebSearch.Tavily.APIKey = ask("Tavily API Key", "")
	case "4":
		cfg.Tools.WebSearch.DuckDuckGo.Enabled = false
	default:
		cfg.Tools.WebSearch.DuckDuckGo.Enabled = true
	}

	if err := saveConfig(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "Erro ao salvar: %v\n", err)
		os.Exit(1)
	}

	fmt.Println()
	fmt.Println("╔══════════════════════════════════════════╗")
	fmt.Println("║     Configuração salva com sucesso!      ║")
	fmt.Println("╚══════════════════════════════════════════╝")
	fmt.Printf("\n  Config: %s\n", configPath())
	fmt.Printf("  Modelos: %d configurado(s)\n", len(cfg.ModelList))
	fmt.Println()
	fmt.Println("  jarv start             → modo interativo")
	fmt.Println("  jarv chat \"Olá\"        → mensagem rápida")
	fmt.Println("  jarv skills install <url> → instalar skill")
	fmt.Println()
}

// ─────────────────────────────────────────────────────────────────────────────
// Commands
// ─────────────────────────────────────────────────────────────────────────────

func cmdStart(cfg *Config) {
	skillsMgr := skills.NewManager("")
	_ = skillsMgr.Load()

	eng, err := buildEngine(cfg, skillsMgr)
	if err != nil {
		fatalf("Erro ao iniciar engine: %v\n", err)
	}

	sched := scheduler.New(func(tasks []scheduler.Task) {
		cfg.Cron = tasks
		_ = saveConfig(cfg)
	})
	sched.SetTasks(cfg.Cron)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Start scheduler in background
	go sched.Start(ctx, func(ctx context.Context, task scheduler.Task) error {
		resp, err := eng.Process(ctx, agent.Request{
			SessionID: "cron-" + task.ID,
			UserID:    "scheduler",
			Text:      task.Prompt,
		})
		if err != nil {
			return err
		}
		fmt.Printf("\n[cron: %s]\nJARV: %s\n", task.Name, resp.Text)
		return nil
	})

	searchLabel := "desativada"
	if cfg.Tools.WebSearch.Tavily.Enabled {
		searchLabel = "Tavily"
	} else if cfg.Tools.WebSearch.Brave.Enabled {
		searchLabel = "Brave"
	} else if cfg.Tools.WebSearch.DuckDuckGo.Enabled {
		searchLabel = "DuckDuckGo"
	}

	fmt.Printf("JARV v%s — %d modelo(s) | busca: %s | %d skill(s) | %d tarefa(s) agendada(s)\n",
		version, len(cfg.ModelList), searchLabel,
		len(skillsMgr.List()), len(cfg.Cron))
	fmt.Println("Digite /ajuda para ver os comandos disponíveis.")

	cliCh := cli.New(cfg.Name)
	if err := cliCh.Start(ctx, func(msg channel.InboundMessage) {
		// Built-in commands
		if strings.HasPrefix(msg.Text, "/") {
			handleCommand(msg.Text, cfg, skillsMgr, sched)
			return
		}
		resp, err := eng.Process(context.Background(), agent.Request{
			SessionID: msg.SessionID,
			UserID:    msg.Sender.ID,
			Text:      msg.Text,
		})
		if err != nil {
			_ = cliCh.Send(context.Background(), channel.OutboundMessage{
				Text: fmt.Sprintf("Erro: %v", err),
			})
			return
		}
		_ = cliCh.Send(context.Background(), channel.OutboundMessage{Text: resp.Text})
	}); err != nil {
		fmt.Fprintf(os.Stderr, "Erro: %v\n", err)
	}
}

func handleCommand(text string, cfg *Config, skillsMgr *skills.Manager, sched *scheduler.Scheduler) {
	parts := strings.Fields(text)
	cmd := parts[0]
	switch cmd {
	case "/ajuda", "/help":
		fmt.Println(`
Comandos disponíveis:
  /status          — status do sistema
  /skills          — listar skills instaladas
  /cron            — listar tarefas agendadas
  /modelos         — listar modelos configurados
  /limpar          — limpar a tela
  /sair, /exit     — encerrar`)
	case "/status":
		printStatus(cfg, skillsMgr, sched)
	case "/skills":
		for _, s := range skillsMgr.List() {
			fmt.Printf("  • %s (%s) — %s\n", s.Name, s.Version, s.Description)
		}
	case "/cron":
		for _, t := range sched.List() {
			status := "✓"
			if !t.Enabled {
				status = "✗"
			}
			fmt.Printf("  %s [%s] %s — %q\n", status, t.ID, t.Schedule, t.Name)
		}
	case "/modelos":
		for i, m := range cfg.ModelList {
			fmt.Printf("  %d. %s (%s) tier:%s\n", i+1, m.Name, m.Provider, m.Tier)
		}
	case "/limpar":
		fmt.Print("\033[H\033[2J")
	}
}

func cmdChat(cfg *Config, text string) {
	skillsMgr := skills.NewManager("")
	_ = skillsMgr.Load()

	eng, err := buildEngine(cfg, skillsMgr)
	if err != nil {
		fatalf("Erro: %v\n", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	resp, err := eng.Process(ctx, agent.Request{
		SessionID: "oneshot-" + uuid.NewString()[:8],
		UserID:    "user",
		Text:      text,
	})
	if err != nil {
		fatalf("Erro: %v\n", err)
	}

	fmt.Println(resp.Text)
	if resp.TokenUsage.TotalTokens > 0 {
		fmt.Fprintf(os.Stderr, "\n[%s | %d tokens | $%.4f | %s]\n",
			resp.ModelUsed, resp.TokenUsage.TotalTokens,
			resp.TokenUsage.EstimatedCostUSD, resp.Latency.Round(time.Millisecond))
	}
}

func cmdOracle(cfg *Config, scenario string) {
	skillsMgr := skills.NewManager("")
	_ = skillsMgr.Load()

	eng, err := buildEngine(cfg, skillsMgr)
	if err != nil {
		fatalf("Erro: %v\n", err)
	}

	prompt := fmt.Sprintf(
		"[ORACLE] Analise o cenário a seguir simulando 4 perspectivas distintas "+
			"(otimista, pessimista, realista, disruptivo). Para cada uma: probabilidade, "+
			"riscos principais e oportunidade-chave.\n\nCenário: %s", scenario)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	resp, err := eng.Process(ctx, agent.Request{
		SessionID: "oracle-" + uuid.NewString()[:8],
		UserID:    "user",
		Text:      prompt,
	})
	if err != nil {
		fatalf("Erro: %v\n", err)
	}
	fmt.Println(resp.Text)
}

func cmdStatus(cfg *Config) {
	skillsMgr := skills.NewManager("")
	_ = skillsMgr.Load()
	sched := scheduler.New(nil)
	sched.SetTasks(cfg.Cron)
	printStatus(cfg, skillsMgr, sched)
}

func printStatus(cfg *Config, skillsMgr *skills.Manager, sched *scheduler.Scheduler) {
	fmt.Printf("\nJARV v%s\n", version)
	fmt.Println(strings.Repeat("─", 40))
	fmt.Printf("Config:   %s\n", configPath())
	fmt.Printf("Nome:     %s\n", cfg.Name)
	fmt.Printf("Idioma:   %s\n", cfg.Language)
	fmt.Println()

	fmt.Printf("Modelos (%d):\n", len(cfg.ModelList))
	for i, m := range cfg.ModelList {
		fmt.Printf("  %d. %-20s provider:%-12s tier:%s\n", i+1, m.Name, m.Provider, m.Tier)
	}
	fmt.Println()

	// Check Ollama
	for _, m := range cfg.ModelList {
		if m.Provider == "ollama" {
			url := m.BaseURL
			if url == "" {
				url = "http://localhost:11434"
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			ok := llmOllama.NewWithURL(url, "").Healthy(ctx)
			cancel()
			if ok {
				fmt.Printf("Ollama:   ✓ rodando em %s\n", url)
			} else {
				fmt.Printf("Ollama:   ✗ não encontrado em %s\n", url)
			}
			break
		}
	}

	ws := cfg.Tools.WebSearch
	searchLabel := "desativada"
	if ws.Tavily.Enabled {
		searchLabel = "Tavily"
	} else if ws.Brave.Enabled {
		searchLabel = "Brave"
	} else if ws.DuckDuckGo.Enabled {
		searchLabel = "DuckDuckGo (grátis)"
	}
	fmt.Printf("Busca:    %s\n", searchLabel)

	skList := skillsMgr.List()
	fmt.Printf("Skills:   %d instalada(s)\n", len(skList))
	for _, s := range skList {
		fmt.Printf("          • %s\n", s.Name)
	}

	tasks := sched.List()
	fmt.Printf("Cron:     %d tarefa(s)\n", len(tasks))
	for _, t := range tasks {
		fmt.Printf("          • [%s] %s — %s\n", t.ID, t.Name, t.Schedule)
	}
	fmt.Println()
}

// ─────────────────────────────────────────────────────────────────────────────
// Skills subcommands
// ─────────────────────────────────────────────────────────────────────────────

func cmdSkills(args []string) {
	mgr := skills.NewManager("")
	_ = mgr.Load()

	if len(args) == 0 || args[0] == "list" {
		list := mgr.List()
		if len(list) == 0 {
			fmt.Println("Nenhuma skill instalada.")
			fmt.Printf("Diretório: %s\n", skillsDir())
			fmt.Println("Use: jarv skills install <url>")
			return
		}
		fmt.Printf("Skills instaladas (%d):\n", len(list))
		for _, s := range list {
			fmt.Printf("  • %-20s v%-6s %s\n", s.Name, s.Version, s.Description)
		}
		return
	}

	if args[0] == "install" {
		if len(args) < 2 {
			fatalf("Uso: jarv skills install <url>\n")
		}
		fmt.Printf("Instalando skill de %s...\n", args[1])
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		sk, err := mgr.Install(ctx, args[1])
		if err != nil {
			fatalf("Erro: %v\n", err)
		}
		fmt.Printf("✓ Skill instalada: %s\n", sk.Name)
		return
	}

	if args[0] == "remove" {
		if len(args) < 2 {
			fatalf("Uso: jarv skills remove <nome>\n")
		}
		if err := mgr.Remove(strings.Join(args[1:], " ")); err != nil {
			fatalf("Erro: %v\n", err)
		}
		fmt.Printf("✓ Skill removida: %s\n", args[1])
		return
	}

	fatalf("Subcomando desconhecido: %q\nUso: jarv skills list|install|remove\n", args[0])
}

// ─────────────────────────────────────────────────────────────────────────────
// Cron subcommands
// ─────────────────────────────────────────────────────────────────────────────

func cmdCron(args []string, cfg *Config) {
	sched := scheduler.New(func(tasks []scheduler.Task) {
		cfg.Cron = tasks
		_ = saveConfig(cfg)
	})
	sched.SetTasks(cfg.Cron)

	if len(args) == 0 || args[0] == "list" {
		tasks := sched.List()
		if len(tasks) == 0 {
			fmt.Println("Nenhuma tarefa agendada.")
			fmt.Println("Use: jarv cron add")
			return
		}
		fmt.Printf("Tarefas agendadas (%d):\n", len(tasks))
		for _, t := range tasks {
			status := "✓"
			if !t.Enabled {
				status = "✗"
			}
			fmt.Printf("  %s [%s] %-12s %-20s %q\n",
				status, t.ID, t.Schedule, t.Name, truncate(t.Prompt, 40))
		}
		return
	}

	if args[0] == "add" {
		sc := bufio.NewScanner(os.Stdin)
		ask := func(prompt, def string) string {
			if def != "" {
				fmt.Fprintf(os.Stdout, "%s [%s]: ", prompt, def)
			} else {
				fmt.Fprintf(os.Stdout, "%s: ", prompt)
			}
			if !sc.Scan() {
				return def
			}
			v := strings.TrimSpace(sc.Text())
			if v == "" {
				return def
			}
			return v
		}
		name := ask("Nome da tarefa", "Lembrete")
		schedule := ask("Frequência (ex: every 1h, every 30m, daily 09:00)", "every 1h")
		prompt := ask("Prompt para o agente", "")
		if prompt == "" {
			fatalf("Prompt não pode ser vazio\n")
		}
		task, err := sched.Add(name, schedule, prompt)
		if err != nil {
			fatalf("Erro: %v\n", err)
		}
		fmt.Printf("✓ Tarefa criada: [%s] %s — %s\n", task.ID, task.Name, task.Schedule)
		return
	}

	if args[0] == "remove" {
		if len(args) < 2 {
			fatalf("Uso: jarv cron remove <id>\n")
		}
		if err := sched.Remove(args[1]); err != nil {
			fatalf("Erro: %v\n", err)
		}
		fmt.Printf("✓ Tarefa [%s] removida\n", args[1])
		return
	}

	if args[0] == "disable" {
		if len(args) < 2 {
			fatalf("Uso: jarv cron disable <id>\n")
		}
		if err := sched.Disable(args[1]); err != nil {
			fatalf("Erro: %v\n", err)
		}
		fmt.Printf("✓ Tarefa [%s] desativada\n", args[1])
		return
	}

	fatalf("Subcomando desconhecido: %q\nUso: jarv cron list|add|remove|disable\n", args[0])
}

// ─────────────────────────────────────────────────────────────────────────────
// Main
// ─────────────────────────────────────────────────────────────────────────────

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "JARV v%s — Just A Rather Very intelligent agent\n\n", version)
		fmt.Fprintf(os.Stderr, "Uso: jarv <comando> [args]\n\n")
		fmt.Fprintf(os.Stderr, "Comandos:\n")
		fmt.Fprintf(os.Stderr, "  setup                  — configuração inicial\n")
		fmt.Fprintf(os.Stderr, "  start                  — modo interativo\n")
		fmt.Fprintf(os.Stderr, "  chat <mensagem>        — mensagem única\n")
		fmt.Fprintf(os.Stderr, "  oracle <cenário>       — análise preditiva\n")
		fmt.Fprintf(os.Stderr, "  status                 — status do sistema\n")
		fmt.Fprintf(os.Stderr, "  skills list            — listar skills\n")
		fmt.Fprintf(os.Stderr, "  skills install <url>   — instalar skill\n")
		fmt.Fprintf(os.Stderr, "  skills remove <nome>   — remover skill\n")
		fmt.Fprintf(os.Stderr, "  cron list              — listar tarefas agendadas\n")
		fmt.Fprintf(os.Stderr, "  cron add               — adicionar tarefa\n")
		fmt.Fprintf(os.Stderr, "  cron remove <id>       — remover tarefa\n")
		fmt.Fprintf(os.Stderr, "  version                — versão\n\n")
	}
	flag.Parse()

	args := flag.Args()
	if len(args) == 0 {
		flag.Usage()
		os.Exit(1)
	}

	cmd := args[0]
	rest := args[1:]

	switch cmd {
	case "setup":
		cmdSetup()

	case "version", "--version", "-v":
		fmt.Printf("JARV v%s\n", version)

	case "start":
		cfg := mustLoadConfig()
		cmdStart(cfg)

	case "chat":
		if len(rest) == 0 {
			fatalf("Uso: jarv chat \"<mensagem>\"\n")
		}
		cmdChat(mustLoadConfig(), strings.Join(rest, " "))

	case "oracle":
		if len(rest) == 0 {
			fatalf("Uso: jarv oracle \"<cenário>\"\n")
		}
		cmdOracle(mustLoadConfig(), strings.Join(rest, " "))

	case "status":
		cmdStatus(mustLoadConfig())

	case "skills":
		cmdSkills(rest)

	case "cron":
		cmdCron(rest, mustLoadConfig())

	default:
		fmt.Fprintf(os.Stderr, "Comando desconhecido: %q\n\n", cmd)
		flag.Usage()
		os.Exit(1)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

func mustLoadConfig() *Config {
	cfg, err := loadConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Configuração não encontrada. Execute 'jarv setup' primeiro.")
		os.Exit(1)
	}
	return cfg
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format, args...)
	os.Exit(1)
}

func skillsDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".jarv", "skills")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-3] + "..."
}
