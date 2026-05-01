package plugins

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"plugin"
	"sync"
)

// Plugin is the interface that all JARV plugins must implement.
type Plugin interface {
	Name() string
	Version() string
	Init(cfg json.RawMessage) error
	Tools() []Tool
	Shutdown() error
}

// Tool represents a callable tool provided by a plugin.
type Tool struct {
	Name        string
	Description string
	InputSchema json.RawMessage
	Handler     func(ctx context.Context, args map[string]any) (any, error)
}

// Manager loads and manages JARV plugins.
type Manager struct {
	pluginsDir string
	loaded     map[string]Plugin
	mu         sync.RWMutex
}

// NewManager creates a new plugin manager.
func NewManager(pluginsDir string) *Manager {
	if pluginsDir == "" {
		home, _ := os.UserHomeDir()
		pluginsDir = filepath.Join(home, ".jarv", "plugins")
	}
	return &Manager{
		pluginsDir: pluginsDir,
		loaded:     make(map[string]Plugin),
	}
}

// Load loads all plugins from the plugins directory.
func (m *Manager) Load(ctx context.Context) error {
	entries, err := os.ReadDir(m.pluginsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // no plugins dir, that's fine
		}
		return fmt.Errorf("read plugins dir: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".so" {
			continue
		}
		path := filepath.Join(m.pluginsDir, entry.Name())
		if err := m.LoadPlugin(ctx, path); err != nil {
			// Log but don't fail — plugins are optional
			fmt.Fprintf(os.Stderr, "WARN: failed to load plugin %s: %v\n", entry.Name(), err)
		}
	}
	return nil
}

// LoadPlugin loads a single .so plugin file.
func (m *Manager) LoadPlugin(ctx context.Context, path string) error {
	p, err := plugin.Open(path)
	if err != nil {
		return fmt.Errorf("open plugin: %w", err)
	}

	sym, err := p.Lookup("JARVPlugin")
	if err != nil {
		return fmt.Errorf("lookup JARVPlugin symbol: %w", err)
	}

	pluginInterface, ok := sym.(*Plugin)
	if !ok {
		return fmt.Errorf("invalid plugin type")
	}

	pl := *pluginInterface
	if err := pl.Init(nil); err != nil {
		return fmt.Errorf("init plugin: %w", err)
	}

	m.mu.Lock()
	m.loaded[pl.Name()] = pl
	m.mu.Unlock()

	return nil
}

// AllTools returns all tools from all loaded plugins.
func (m *Manager) AllTools() []Tool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var all []Tool
	for _, pl := range m.loaded {
		all = append(all, pl.Tools()...)
	}
	return all
}

// Get returns a loaded plugin by name.
func (m *Manager) Get(name string) (Plugin, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	p, ok := m.loaded[name]
	return p, ok
}
