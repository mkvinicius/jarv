// Package config handles JARV configuration loading and management.
//
// Configuration is loaded from YAML files with support for environment
// variable interpolation and default values.
//
// Default config locations (in order of priority):
//   1. Path specified via --config flag
//   2. ~/.jarv/config.yaml
//   3. Built-in defaults
//
// Copyright (c) 2026 JARV contributors. All rights reserved.
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Config is the root configuration structure for JARV.
type Config struct {
	// Mode is the operation mode: economy|balanced|maximum
	Mode string `yaml:"mode"`

	// LLMProviders defines the available LLM providers
	LLMProviders []LLMProviderConfig `yaml:"llm_providers"`

	// Dashboard controls the web dashboard settings
	Dashboard DashboardConfig `yaml:"dashboard"`

	// Shield controls security settings
	Shield ShieldConfig `yaml:"shield"`

	// Storage controls data persistence
	Storage StorageConfig `yaml:"storage"`

	// Agent controls the agent behavior
	Agent AgentConfig `yaml:"agent"`

	// Foresight controls predictive capabilities
	Foresight ForesightConfig `yaml:"foresight"`
}

// LLMProviderConfig describes a single LLM provider.
type LLMProviderConfig struct {
	// Type is the provider type: openai|anthropic|ollama
	Type string `yaml:"type"`

	// Name is the display name for this provider
	Name string `yaml:"name"`

	// APIKey is the API key (or env var reference like ${OPENAI_API_KEY})
	APIKey string `yaml:"api_key,omitempty"`

	// BaseURL is the base URL for the API (for proxies/self-hosted)
	BaseURL string `yaml:"base_url,omitempty"`

	// Models defines the available models for this provider
	Models []ModelConfig `yaml:"models,omitempty"`

	// Config holds additional provider-specific settings
	Config map[string]string `yaml:"config,omitempty"`
}

// ModelConfig describes a single model from a provider.
type ModelConfig struct {
	// Name is the model identifier (e.g., "gpt-4o", "claude-3-5-sonnet")
	Name string `yaml:"name"`

	// Tier is the capability tier: nano|mini|standard|premium
	Tier string `yaml:"tier"`

	// Priority is the fallback priority (lower = higher priority)
	Priority int `yaml:"priority,omitempty"`

	// MaxTokens is the maximum output tokens
	MaxTokens int `yaml:"max_tokens,omitempty"`

	// Config holds additional model-specific settings
	Config map[string]string `yaml:"config,omitempty"`
}

// DashboardConfig controls the web dashboard.
type DashboardConfig struct {
	// Host is the listen address (default: 127.0.0.1)
	Host string `yaml:"host"`

	// Port is the listen port (default: 7777)
	Port int `yaml:"port"`

	// OpenBrowser auto-opens the browser on start
	OpenBrowser bool `yaml:"open_browser"`

	// DefaultMode is the initial UI mode: focus|advanced
	DefaultMode string `yaml:"default_mode"`
}

// ShieldConfig controls security settings.
type ShieldConfig struct {
	// Enabled enables the security shield
	Enabled bool `yaml:"enabled"`

	// LogRequests logs all requests to audit log
	LogRequests bool `yaml:"log_requests"`

	// BlockHighThreat blocks high-threat requests
	BlockHighThreat bool `yaml:"block_high_threat"`

	// CustomPatterns is a list of custom threat patterns
	CustomPatterns []string `yaml:"custom_patterns,omitempty"`

	// ToShieldConfig converts to the core shield config
	ToShieldConfig func() ShieldCoreConfig `yaml:"-"`
}

// ShieldCoreConfig is the core shield configuration.
type ShieldCoreConfig struct {
	Enabled         bool
	LogRequests     bool
	BlockHighThreat bool
	CustomPatterns  []string
}

// StorageConfig controls data persistence.
type StorageConfig struct {
	// Path is the base directory for SQLite databases
	Path string `yaml:"path"`

	// CloudSync enables cloud synchronization
	CloudSync bool `yaml:"cloud_sync"`

	// CloudURL is the Supabase URL for cloud sync
	CloudURL string `yaml:"cloud_url,omitempty"`

	// CloudKey is the Supabase anon key
	CloudKey string `yaml:"cloud_key,omitempty"`
}

// AgentConfig controls the agent behavior.
type AgentConfig struct {
	// Name is the agent's name
	Name string `yaml:"name"`

	// Persona is the agent's persona description
	Persona string `yaml:"persona"`

	// Language is the default language (ISO 639-1 code)
	Language string `yaml:"language"`

	// MaxHistory is the maximum conversation history turns
	MaxHistory int `yaml:"max_history"`

	// SystemPrompt is the system prompt override
	SystemPrompt string `yaml:"system_prompt,omitempty"`
}

// ForesightConfig controls predictive capabilities.
type ForesightConfig struct {
	// Enabled enables Oracle predictions
	Enabled bool `yaml:"enabled"`

	// DefaultMode is the default Oracle mode
	DefaultMode string `yaml:"default_mode"`

	// MaxArchetypes is the maximum archetypes per simulation
	MaxArchetypes int `yaml:"max_archetypes"`

	// MaxRounds is the maximum simulation rounds
	MaxRounds int `yaml:"max_rounds"`
}

// Default returns a default configuration.
func Default() *Config {
	return &Config{
		Mode: "balanced",
		LLMProviders: []LLMProviderConfig{
			{
				Type:   "ollama",
				Name:   "Local Ollama",
				BaseURL: "http://localhost:11434",
				Models: []ModelConfig{
					{Name: "llama3.2:latest", Tier: "standard", Priority: 1},
					{Name: "llama3.2-nano:latest", Tier: "nano", Priority: 2},
				},
			},
		},
		Dashboard: DashboardConfig{
			Host:        "127.0.0.1",
			Port:        7777,
			OpenBrowser: false,
			DefaultMode: "focus",
		},
		Shield: ShieldConfig{
			Enabled:         true,
			LogRequests:     true,
			BlockHighThreat: true,
			CustomPatterns:  []string{},
		},
		Storage: StorageConfig{
			Path:      "",
			CloudSync: false,
		},
		Agent: AgentConfig{
			Name:     "JARV",
			Persona: "A helpful, intelligent AI assistant with advanced reasoning capabilities.",
			Language: "en",
			MaxHistory: 100,
		},
		Foresight: ForesightConfig{
			Enabled:       true,
			DefaultMode:   "balanced",
			MaxArchetypes: 5,
			MaxRounds:     5,
		},
	}
}

// Load reads and parses a configuration file.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config: %w", err)
	}

	cfg := Default()

	// Expand environment variables
	expanded := os.ExpandEnv(string(data))

	if err := yaml.Unmarshal([]byte(expanded), cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}

	// Post-process: resolve env var references in API keys
	for i := range cfg.LLMProviders {
		if cfg.LLMProviders[i].APIKey != "" {
			cfg.LLMProviders[i].APIKey = os.ExpandEnv(cfg.LLMProviders[i].APIKey)
		}
	}

	return cfg, nil
}

// Save writes the configuration to a file.
func (c *Config) Save(path string) error {
	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write config: %w", err)
	}

	return nil
}

// Validate checks the configuration for errors.
func (c *Config) Validate() error {
	if c.Mode != "" && c.Mode != "economy" && c.Mode != "balanced" && c.Mode != "maximum" {
		return fmt.Errorf("invalid mode: %s (must be economy|balanced|maximum)", c.Mode)
	}

	if len(c.LLMProviders) == 0 {
		return fmt.Errorf("at least one LLM provider is required")
	}

	for i, prov := range c.LLMProviders {
		if prov.Type == "" {
			return fmt.Errorf("provider %d: type is required", i)
		}
		if len(prov.Models) == 0 {
			return fmt.Errorf("provider %s: at least one model is required", prov.Type)
		}
		for j, model := range prov.Models {
			if model.Name == "" {
				return fmt.Errorf("provider %s model %d: name is required", prov.Type, j)
			}
			if model.Tier != "" && model.Tier != "nano" && model.Tier != "mini" &&
				model.Tier != "standard" && model.Tier != "premium" {
				return fmt.Errorf("provider %s model %s: invalid tier", prov.Type, model.Name)
			}
		}
	}

	return nil
}
