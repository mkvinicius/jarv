// JARV — Just A Rather Very intelligent agent
//
// Entry point for the JARV CLI.
//
// Commands:
//   jarv setup   — first-time setup wizard (choose online/offline, download model)
//   jarv start   — start the dashboard + CLI agent loop
//   jarv chat    — send a single message and exit
//   jarv oracle  — run an Oracle prediction scenario
//   jarv version — print version info
//
// Copyright (c) 2026 JARV contributors. All rights reserved.
// Proprietary and confidential.
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

	"github.com/mkvinicius/jarv/internal/adapters/channel/cli"
	llmadapterAnthropicPkg "github.com/mkvinicius/jarv/internal/adapters/llm/anthropic"
	llmadapterOpenAIPkg "github.com/mkvinicius/jarv/internal/adapters/llm/openai"
	"github.com/mkvinicius/jarv/internal/adapters/llm/ollama"
	storagePkg "github.com/mkvinicius/jarv/internal/adapters/storage"
	"github.com/mkvinicius/jarv/internal/core/agent"
	"github.com/mkvinicius/jarv/internal/core/reasoning"
	"github.com/mkvinicius/jarv/internal/ports/channel"
	"github.com/mkvinicius/jarv/internal/ports/llm"
	"github.com/google/uuid"
)

const version = "0.1.0"

// ─────────────────────────────────────────────────────────────────────────────
// Config
// ─────────────────────────────────────────────────────────────────────────────

// Config is persisted to ~/.jarv/config.json
type Config struct {
	Mode     string    `json:"mode"`     // "online" or "offline"
	Name     string    `json:"name"`     // agent persona name
	Language string    `json:"language"` // e.g. "pt-BR"
	Online   OnlineCfg `json:"online,omitempty"`
	Offline  OfflineCfg `json:"offline,omitempty"`
}

type OnlineCfg struct {
	Provider     string `json:"provider"`      // "openai" or "anthropic"
	OpenAIKey    string `json:"openai_key,omitempty"`
	OpenAIModel  string `json:"openai_model,omitempty"`
	AnthropicKey string `json:"anthropic_key,omitempty"`
	AnthropicModel string `json:"anthropic_model,omitempty"`
}

type OfflineCfg struct {
	OllamaURL string `json:"ollama_url"` // default: http://localhost:11434
	Model     string `json:"model"`
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

// ─────────────────────────────────────────────────────────────────────────────
// Setup wizard
// ─────────────────────────────────────────────────────────────────────────────

// offlineModels is the curated list shown during offline setup.
var offlineModels = []struct {
	Key         string
	Name        string
	Size        string
	Description string
}{
	{"llama3.2:3b", "Llama 3.2 3B", "~2 GB", "Rápido, ideal para conversas e perguntas simples"},
	{"llama3.1:8b", "Llama 3.1 8B", "~5 GB", "Uso geral, boa qualidade de raciocínio"},
	{"mistral:7b", "Mistral 7B", "~4 GB", "Excelente para código e análise técnica"},
	{"qwen2.5:14b", "Qwen 2.5 14B", "~9 GB", "Alta qualidade — GPU recomendada"},
	{"deepseek-r1:7b", "DeepSeek-R1 7B", "~5 GB", "Forte em raciocínio matemático e lógico"},
}

func cmdSetup() {
	scanner := bufio.NewScanner(os.Stdin)
	ask := func(prompt, defaultVal string) string {
		if defaultVal != "" {
			fmt.Fprintf(os.Stdout, "%s [%s]: ", prompt, defaultVal)
		} else {
			fmt.Fprintf(os.Stdout, "%s: ", prompt)
		}
		if !scanner.Scan() {
			return defaultVal
		}
		v := strings.TrimSpace(scanner.Text())
		if v == "" {
			return defaultVal
		}
		return v
	}

	fmt.Println()
	fmt.Println("╔══════════════════════════════════════════╗")
	fmt.Println("║        JARV — Assistente de IA           ║")
	fmt.Println("║          Assistente de Configuração      ║")
	fmt.Println("╚══════════════════════════════════════════╝")
	fmt.Println()

	cfg := &Config{
		Name:     "JARV",
		Language: "pt-BR",
	}

	// Step 1: Name
	cfg.Name = ask("Nome do assistente", cfg.Name)
	cfg.Language = ask("Idioma padrão (pt-BR / en-US)", cfg.Language)

	fmt.Println()
	fmt.Println("Modo de operação:")
	fmt.Println("  1) Online  — usar APIs externas (OpenAI, Anthropic)")
	fmt.Println("  2) Offline — modelos locais via Ollama (sem internet, sem custo por uso)")
	fmt.Println()

	modeChoice := ask("Escolha o modo (1/2)", "2")

	if modeChoice == "1" {
		// Online setup
		cfg.Mode = "online"
		fmt.Println()
		fmt.Println("Provedor:")
		fmt.Println("  1) OpenAI    (GPT-4o, GPT-4o-mini)")
		fmt.Println("  2) Anthropic (Claude Sonnet, Haiku)")
		provChoice := ask("Escolha o provedor (1/2)", "1")

		if provChoice == "2" {
			cfg.Online.Provider = "anthropic"
			cfg.Online.AnthropicKey = ask("Anthropic API Key", "")
			cfg.Online.AnthropicModel = ask("Modelo", "claude-sonnet-4-6")
		} else {
			cfg.Online.Provider = "openai"
			cfg.Online.OpenAIKey = ask("OpenAI API Key", "")
			cfg.Online.OpenAIModel = ask("Modelo", "gpt-4o-mini")
		}
	} else {
		// Offline setup
		cfg.Mode = "offline"
		cfg.Offline.OllamaURL = "http://localhost:11434"

		fmt.Println()
		fmt.Println("Verificando se o Ollama está instalado...")

		ollamaOK := isOllamaRunning(cfg.Offline.OllamaURL)
		if !ollamaOK {
			fmt.Println()
			fmt.Println("⚠️  Ollama não encontrado em " + cfg.Offline.OllamaURL)
			fmt.Println()
			fmt.Println("Para instalar o Ollama:")
			fmt.Println("  Linux/macOS: curl -fsSL https://ollama.com/install.sh | sh")
			fmt.Println("  Windows:     https://ollama.com/download")
			fmt.Println()
			fmt.Println("Após instalar, execute 'jarv setup' novamente ou continue para")
			fmt.Println("configurar o modelo (o download iniciará quando o Ollama estiver ativo).")
			fmt.Println()
		} else {
			fmt.Println("✓  Ollama está rodando.")
		}

		fmt.Println()
		fmt.Println("Escolha o modelo para download:")
		fmt.Println()
		for i, m := range offlineModels {
			fmt.Printf("  %d) %-20s %-8s — %s\n", i+1, m.Name, m.Size, m.Description)
		}
		fmt.Println()

		modelChoice := ask("Modelo (1-5)", "1")
		idx := 0
		switch modelChoice {
		case "2":
			idx = 1
		case "3":
			idx = 2
		case "4":
			idx = 3
		case "5":
			idx = 4
		}
		selected := offlineModels[idx]
		cfg.Offline.Model = selected.Key

		fmt.Printf("\nModelo selecionado: %s (%s)\n", selected.Name, selected.Size)

		if ollamaOK {
			fmt.Println()
			startDownloadYN := ask("Iniciar download agora? (s/n)", "s")
			if strings.ToLower(startDownloadYN) == "s" {
				fmt.Printf("Baixando %s em segundo plano...\n", selected.Key)
				fmt.Println("(Você pode continuar usando o JARV. O modelo ficará disponível quando o download terminar.)")
				fmt.Println()

				progress := make(chan string, 16)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()

				go func() {
					if err := ollama.PullModel(ctx, cfg.Offline.OllamaURL, selected.Key, progress); err != nil {
						fmt.Fprintf(os.Stderr, "\nErro no download: %v\n", err)
					} else {
						fmt.Printf("\n✓  Modelo %s baixado com sucesso!\n", selected.Key)
					}
				}()

				// Show progress for a few seconds, then continue
				timeout := time.After(5 * time.Second)
				for {
					select {
					case msg, ok := <-progress:
						if !ok {
							goto doneProgress
						}
						fmt.Printf("  → %s\n", msg)
					case <-timeout:
						fmt.Println("  (download continuando em segundo plano...)")
						goto doneProgress
					}
				}
			doneProgress:
			}
		}
	}

	// Save
	if err := saveConfig(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "Erro ao salvar configuração: %v\n", err)
		os.Exit(1)
	}

	fmt.Println()
	fmt.Println("╔══════════════════════════════════════════╗")
	fmt.Println("║     Configuração salva com sucesso!      ║")
	fmt.Println("╚══════════════════════════════════════════╝")
	fmt.Println()
	fmt.Printf("  Config: %s\n", configPath())
	fmt.Printf("  Modo:   %s\n", cfg.Mode)
	fmt.Println()
	fmt.Println("  Para iniciar:  jarv start")
	fmt.Println("  Para conversar: jarv chat \"Olá, como vai?\"")
	fmt.Println()
}

// ─────────────────────────────────────────────────────────────────────────────
// Build engine from config
// ─────────────────────────────────────────────────────────────────────────────

func buildEngine(cfg *Config) (*agent.Engine, error) {
	chain := reasoning.NewFallbackChain()

	switch cfg.Mode {
	case "online":
		switch cfg.Online.Provider {
		case "anthropic":
			if cfg.Online.AnthropicKey == "" {
				return nil, fmt.Errorf("anthropic API key não configurada. Execute 'jarv setup'")
			}
			p := llmadapterAnthropicPkg.New(cfg.Online.AnthropicKey, cfg.Online.AnthropicModel)
			chain.Add(llm.Candidate{Provider: p, Model: cfg.Online.AnthropicModel, Tier: llm.TierStandard, Priority: 1})
		default: // openai
			if cfg.Online.OpenAIKey == "" {
				return nil, fmt.Errorf("OpenAI API key não configurada. Execute 'jarv setup'")
			}
			p := llmadapterOpenAIPkg.New(cfg.Online.OpenAIKey, cfg.Online.OpenAIModel)
			chain.Add(llm.Candidate{Provider: p, Model: cfg.Online.OpenAIModel, Tier: llm.TierStandard, Priority: 1})
		}
	default: // offline
		ollamaURL := cfg.Offline.OllamaURL
		if ollamaURL == "" {
			ollamaURL = "http://localhost:11434"
		}
		model := cfg.Offline.Model
		if model == "" {
			model = "llama3.2:3b"
		}
		p := ollama.NewWithURL(ollamaURL, model)
		chain.Add(llm.Candidate{Provider: p, Model: model, Tier: llm.TierStandard, Priority: 1})
	}

	router := reasoning.NewSmartRouter("balanced")

	store := storagePkg.NewInMemoryStore(uuid.NewString())
	graph := storagePkg.NewInMemoryGraph()
	sessions := storagePkg.NewInMemorySessionStore()
	tools := agent.NewToolRegistry()

	engineCfg := agent.DefaultEngineConfig()
	engineCfg.Name = cfg.Name
	if cfg.Language != "" {
		engineCfg.Language = cfg.Language
	}
	engineCfg.Persona = fmt.Sprintf(
		"Você é %s, um assistente de inteligência artificial avançado. "+
			"Seja preciso, eficiente e sempre honesto.",
		cfg.Name,
	)

	return agent.NewEngine(engineCfg, chain, router, store, graph, sessions, tools), nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Commands
// ─────────────────────────────────────────────────────────────────────────────

func cmdStart(cfg *Config) {
	eng, err := buildEngine(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Erro: %v\n", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cliCh := cli.New(cfg.Name)

	fmt.Printf("JARV %s iniciado (modo: %s)\n", version, cfg.Mode)

	if err := cliCh.Start(ctx, func(msg channel.InboundMessage) {
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
		_ = cliCh.Send(context.Background(), channel.OutboundMessage{
			Text: resp.Text,
		})
	}); err != nil {
		fmt.Fprintf(os.Stderr, "Erro: %v\n", err)
		os.Exit(1)
	}
}

func cmdChat(cfg *Config, text string) {
	eng, err := buildEngine(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Erro: %v\n", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	resp, err := eng.Process(ctx, agent.Request{
		SessionID: "cli-oneshot",
		UserID:    "user",
		Text:      text,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Erro: %v\n", err)
		os.Exit(1)
	}

	fmt.Println(resp.Text)

	if resp.TokenUsage.TotalTokens > 0 {
		fmt.Fprintf(os.Stderr, "\n[%s | %d tokens | $%.4f | %s]\n",
			resp.ModelUsed,
			resp.TokenUsage.TotalTokens,
			resp.TokenUsage.EstimatedCostUSD,
			resp.Latency.Round(time.Millisecond),
		)
	}
}

func cmdOracle(cfg *Config, scenario string) {
	eng, err := buildEngine(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Erro: %v\n", err)
		os.Exit(1)
	}

	prompt := fmt.Sprintf(
		"[ORACLE] Analise o seguinte cenário simulando 4 perspectivas distintas "+
			"(otimista, pessimista, realista, disruptivo). Para cada perspectiva, "+
			"indique: probabilidade, principais riscos e oportunidade principal.\n\n"+
			"Cenário: %s",
		scenario,
	)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	resp, err := eng.Process(ctx, agent.Request{
		SessionID: "oracle-" + uuid.NewString()[:8],
		UserID:    "user",
		Text:      prompt,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Erro: %v\n", err)
		os.Exit(1)
	}

	fmt.Println(resp.Text)
}

// ─────────────────────────────────────────────────────────────────────────────
// Main
// ─────────────────────────────────────────────────────────────────────────────

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "JARV v%s — Just A Rather Very intelligent agent\n\n", version)
		fmt.Fprintf(os.Stderr, "Uso: jarv <comando> [opções]\n\n")
		fmt.Fprintf(os.Stderr, "Comandos:\n")
		fmt.Fprintf(os.Stderr, "  setup           — Assistente de configuração inicial\n")
		fmt.Fprintf(os.Stderr, "  start           — Iniciar o assistente interativo\n")
		fmt.Fprintf(os.Stderr, "  chat <mensagem> — Enviar mensagem e receber resposta\n")
		fmt.Fprintf(os.Stderr, "  oracle <cenário>— Executar análise preditiva Oracle\n")
		fmt.Fprintf(os.Stderr, "  version         — Exibir versão\n\n")
		fmt.Fprintf(os.Stderr, "Exemplo:\n")
		fmt.Fprintf(os.Stderr, "  jarv setup\n")
		fmt.Fprintf(os.Stderr, "  jarv start\n")
		fmt.Fprintf(os.Stderr, "  jarv chat \"Qual é a capital do Brasil?\"\n")
		fmt.Fprintf(os.Stderr, "  jarv oracle \"Minha startup lança em 3 meses com $50k de runway\"\n\n")
	}
	flag.Parse()

	args := flag.Args()
	if len(args) == 0 {
		flag.Usage()
		os.Exit(1)
	}

	cmd := args[0]

	switch cmd {
	case "setup":
		cmdSetup()

	case "version", "--version", "-v":
		fmt.Printf("JARV v%s\n", version)

	case "start":
		cfg, err := loadConfig()
		if err != nil {
			fmt.Fprintln(os.Stderr, "Configuração não encontrada. Execute 'jarv setup' primeiro.")
			os.Exit(1)
		}
		cmdStart(cfg)

	case "chat":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Uso: jarv chat \"<mensagem>\"")
			os.Exit(1)
		}
		cfg, err := loadConfig()
		if err != nil {
			fmt.Fprintln(os.Stderr, "Configuração não encontrada. Execute 'jarv setup' primeiro.")
			os.Exit(1)
		}
		cmdChat(cfg, strings.Join(args[1:], " "))

	case "oracle":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Uso: jarv oracle \"<cenário>\"")
			os.Exit(1)
		}
		cfg, err := loadConfig()
		if err != nil {
			fmt.Fprintln(os.Stderr, "Configuração não encontrada. Execute 'jarv setup' primeiro.")
			os.Exit(1)
		}
		cmdOracle(cfg, strings.Join(args[1:], " "))

	default:
		fmt.Fprintf(os.Stderr, "Comando desconhecido: %q\n\n", cmd)
		flag.Usage()
		os.Exit(1)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

func isOllamaRunning(baseURL string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	p := ollama.New("") // empty model just for health check
	return p.Healthy(ctx)
}
