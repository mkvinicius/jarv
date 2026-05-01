// Package main — JARV quickstart example.
//
// This example demonstrates the minimum code needed to use JARV programmatically.
// It creates an engine with Ollama as the LLM provider and processes a single message.
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/mkvinicius/jarv/internal/core/agent"
	"github.com/mkvinicius/jarv/internal/adapters/storage"
	"github.com/mkvinicius/jarv/internal/core/reasoning"
	"github.com/mkvinicius/jarv/internal/ports/llm"
)

func main() {
	ctx := context.Background()

	// ── 1. Create LLM provider ──────────────────────────────────────────────
	ollamaURL := os.Getenv("OLLAMA_BASE_URL")
	if ollamaURL == "" {
		ollamaURL = "http://localhost:11434"
	}

	prov, err := llm.NewOllamaProvider(ollamaURL)
	if err != nil {
		log.Fatalf("create Ollama provider: %v", err)
	}
	if !prov.Healthy(ctx) {
		log.Fatalf("Ollama is not healthy at %s — is it running?", ollamaURL)
	}

	// ── 2. Create storage ──────────────────────────────────────────────────
	mem, err := storage.NewSQLiteMemoryStore("/tmp/jarv-memory.db")
	if err != nil {
		log.Fatalf("create memory store: %v", err)
	}
	graph, err := storage.NewSQLiteKnowledgeGraph("/tmp/jarv-graph.db")
	if err != nil {
		log.Fatalf("create knowledge graph: %v", err)
	}
	sessions, err := storage.NewSQLiteSessionStore("/tmp/jarv-sessions.db")
	if err != nil {
		log.Fatalf("create session store: %v", err)
	}

	// ── 3. Create router & fallback chain ───────────────────────────────────
	router := reasoning.NewSmartRouter("balanced")
	chain := llm.NewJARVFallbackChain()
	chain.Add(llm.Candidate{
		Provider: prov,
		Model:    "llama3.2:latest",
		Tier:     llm.TierStandard,
		Priority: 1,
	})

	// ── 4. Create engine ───────────────────────────────────────────────────
	toolReg := agent.NewToolRegistry()
	eng := agent.NewEngine(
		agent.EngineConfig{
			Name:     "JARV",
			Persona:  "You are JARV, a helpful AI assistant with advanced reasoning capabilities.",
			Language: "en",
		},
		chain,
		router,
		mem,
		graph,
		sessions,
		toolReg,
	)

	// ── 5. Process a message ───────────────────────────────────────────────
	resp, err := eng.Process(ctx, agent.Request{
		SessionID: "quickstart",
		UserID:    "example",
		Text:      "Hello! What can you help me with?",
	})
	if err != nil {
		log.Fatalf("process: %v", err)
	}

	fmt.Printf("JARV: %s\n", resp.Text)
	if resp.FromCache {
		fmt.Println("(served from cache)")
	}
	fmt.Printf("Model used: %s | Tier: %s | Latency: %v\n",
		resp.ModelUsed, resp.Tier, resp.Latency)
}
