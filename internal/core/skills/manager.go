// Package skills manages JARV's skill system.
//
// Skills are Markdown files that specialize the agent for a particular domain.
// They live in ~/.jarv/skills/ and are loaded at startup, injected into the
// agent's system prompt.
//
// A skill file looks like:
//
//	---
//	name: Full Stack Engineer
//	version: 1.0
//	description: Expert in web and backend development
//	author: jarv-team
//	---
//	You are an expert full stack engineer...
//
// Copyright (c) 2026 JARV contributors. All rights reserved.
package skills

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// Skill types
// ─────────────────────────────────────────────────────────────────────────────

// Skill represents a loaded skill with its metadata and content.
type Skill struct {
	Name        string
	Version     string
	Description string
	Author      string
	Content     string // the markdown body (without frontmatter)
	Filename    string
}

// ─────────────────────────────────────────────────────────────────────────────
// Manager
// ─────────────────────────────────────────────────────────────────────────────

// Manager handles loading, listing, and installing skills.
type Manager struct {
	dir    string
	skills []Skill
}

// NewManager creates a skills manager pointing at the given directory.
// If dir is empty, defaults to ~/.jarv/skills/
func NewManager(dir string) *Manager {
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".jarv", "skills")
	}
	return &Manager{dir: dir}
}

// Load reads all .md files from the skills directory.
func (m *Manager) Load() error {
	if err := os.MkdirAll(m.dir, 0755); err != nil {
		return fmt.Errorf("skills: mkdir: %w", err)
	}

	entries, err := os.ReadDir(m.dir)
	if err != nil {
		return fmt.Errorf("skills: readdir: %w", err)
	}

	m.skills = nil
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(m.dir, e.Name()))
		if err != nil {
			continue
		}
		skill := parseSkill(string(data), e.Name())
		m.skills = append(m.skills, skill)
	}
	return nil
}

// List returns all loaded skills.
func (m *Manager) List() []Skill {
	return m.skills
}

// InjectIntoPrompt prepends active skill content to the base persona.
func (m *Manager) InjectIntoPrompt(persona string) string {
	if len(m.skills) == 0 {
		return persona
	}
	var sb strings.Builder
	for _, s := range m.skills {
		sb.WriteString(fmt.Sprintf("## Skill: %s\n%s\n\n", s.Name, s.Content))
	}
	sb.WriteString(persona)
	return sb.String()
}

// Install downloads a skill from a URL and saves it to the skills directory.
func (m *Manager) Install(ctx context.Context, rawURL string) (Skill, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return Skill{}, fmt.Errorf("skills: install: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return Skill{}, fmt.Errorf("skills: download: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return Skill{}, fmt.Errorf("skills: download: status %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return Skill{}, fmt.Errorf("skills: read: %w", err)
	}

	skill := parseSkill(string(data), "")
	filename := sanitizeFilename(skill.Name) + ".md"
	if skill.Name == "" {
		// Derive name from URL
		parts := strings.Split(strings.TrimRight(rawURL, "/"), "/")
		filename = parts[len(parts)-1]
		if !strings.HasSuffix(filename, ".md") {
			filename += ".md"
		}
		skill = parseSkill(string(data), filename)
	}
	skill.Filename = filename

	if err := os.MkdirAll(m.dir, 0755); err != nil {
		return Skill{}, fmt.Errorf("skills: mkdir: %w", err)
	}

	path := filepath.Join(m.dir, filename)
	if err := os.WriteFile(path, data, 0644); err != nil {
		return Skill{}, fmt.Errorf("skills: write: %w", err)
	}

	m.skills = append(m.skills, skill)
	return skill, nil
}

// Remove deletes a skill by name.
func (m *Manager) Remove(name string) error {
	for i, s := range m.skills {
		if strings.EqualFold(s.Name, name) {
			path := filepath.Join(m.dir, s.Filename)
			if err := os.Remove(path); err != nil {
				return err
			}
			m.skills = append(m.skills[:i], m.skills[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("skill %q not found", name)
}

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

// parseSkill parses a skill markdown file with optional YAML-like frontmatter.
func parseSkill(content, filename string) Skill {
	skill := Skill{Filename: filename}

	// Parse frontmatter (--- ... ---)
	content = strings.TrimSpace(content)
	if strings.HasPrefix(content, "---") {
		end := strings.Index(content[3:], "---")
		if end >= 0 {
			frontmatter := content[3 : end+3]
			skill.Content = strings.TrimSpace(content[end+6:])
			for _, line := range strings.Split(frontmatter, "\n") {
				line = strings.TrimSpace(line)
				if after, ok := cutPrefix(line, "name:"); ok {
					skill.Name = strings.TrimSpace(after)
				} else if after, ok := cutPrefix(line, "version:"); ok {
					skill.Version = strings.TrimSpace(after)
				} else if after, ok := cutPrefix(line, "description:"); ok {
					skill.Description = strings.TrimSpace(after)
				} else if after, ok := cutPrefix(line, "author:"); ok {
					skill.Author = strings.TrimSpace(after)
				}
			}
		}
	} else {
		skill.Content = content
	}

	// Derive name from filename if not set
	if skill.Name == "" && filename != "" {
		base := strings.TrimSuffix(filepath.Base(filename), ".md")
		skill.Name = strings.ReplaceAll(base, "-", " ")
		skill.Name = strings.ReplaceAll(skill.Name, "_", " ")
	}

	return skill
}

func sanitizeFilename(name string) string {
	name = strings.ToLower(name)
	name = strings.ReplaceAll(name, " ", "-")
	var sb strings.Builder
	for _, c := range name {
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' {
			sb.WriteRune(c)
		}
	}
	return sb.String()
}

func cutPrefix(s, prefix string) (string, bool) {
	if strings.HasPrefix(s, prefix) {
		return s[len(prefix):], true
	}
	return "", false
}
