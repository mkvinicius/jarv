// Package storage defines the persistence ports for JARV.
//
// JARV uses a three-layer memory architecture:
//   L1 — In-process cache (sync.Map, zero latency)
//   L2 — Local SQLite + vector file (milliseconds, offline-first)
//   L3 — Cloud sync (Supabase/pgvector, when online)
//
// All layers implement the same interfaces, allowing transparent
// fallback and synchronization without the core knowing the details.
//
// Copyright (c) 2026 JARV contributors. All rights reserved.
// Proprietary and confidential.
package storage

import (
	"context"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// Memory Store — Conversation & Facts
// ─────────────────────────────────────────────────────────────────────────────

// MemoryKind classifies the type of a memory entry.
type MemoryKind string

const (
	KindConversation MemoryKind = "conversation"
	KindFact         MemoryKind = "fact"
	KindPreference   MemoryKind = "preference"
	KindTask         MemoryKind = "task"
	KindContext      MemoryKind = "context"
	KindSkill        MemoryKind = "skill"
)

// MemoryEntry is the atomic unit of JARV's long-term memory.
type MemoryEntry struct {
	ID          string            `json:"id"`
	Kind        MemoryKind        `json:"kind"`
	Content     string            `json:"content"`
	Summary     string            `json:"summary,omitempty"`
	Tags        []string          `json:"tags,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
	Importance  float32           `json:"importance"` // 0.0–1.0
	Vector      []float32         `json:"vector,omitempty"`
	NodeID      string            `json:"node_id"`   // which JARV instance owns this
	SyncStatus  string            `json:"sync_status"` // "pending", "synced"
	Version     int               `json:"version"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
}

// MemoryQuery defines search parameters for memory retrieval.
type MemoryQuery struct {
	Text      string     // semantic search text
	Vector    []float32  // direct vector search (optional)
	Kinds     []MemoryKind
	Tags      []string
	Limit     int
	MinScore  float32 // minimum similarity score (0.0–1.0)
	Since     time.Time
}

// MemoryResult pairs a memory entry with its relevance score.
type MemoryResult struct {
	Entry MemoryEntry
	Score float32 // cosine similarity (0.0–1.0)
}

// MemoryStore is the interface for all memory persistence in JARV.
type MemoryStore interface {
	// Save creates or updates a memory entry.
	Save(ctx context.Context, entry MemoryEntry) error

	// Get retrieves a memory entry by ID.
	Get(ctx context.Context, id string) (*MemoryEntry, error)

	// Search finds relevant memories using semantic similarity.
	Search(ctx context.Context, query MemoryQuery) ([]MemoryResult, error)

	// Delete removes a memory entry permanently.
	Delete(ctx context.Context, id string) error

	// Pending returns entries that have not yet been synced to the cloud.
	Pending(ctx context.Context, limit int) ([]MemoryEntry, error)

	// MarkSynced marks entries as successfully synced.
	MarkSynced(ctx context.Context, ids []string) error

	// Stats returns storage statistics (entry count, size, etc.).
	Stats(ctx context.Context) (map[string]int64, error)
}

// ─────────────────────────────────────────────────────────────────────────────
// Knowledge Graph — Relational Memory
// ─────────────────────────────────────────────────────────────────────────────

// Triple is the atomic unit of the knowledge graph: Subject → Predicate → Object.
// Example: "João" → "é CEO de" → "Empresa X"
type Triple struct {
	ID         string    `json:"id"`
	Subject    string    `json:"subject"`
	Predicate  string    `json:"predicate"`
	Object     string    `json:"object"`
	Confidence float32   `json:"confidence"` // 0.0–1.0
	Source     string    `json:"source"`     // where this fact came from
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// GraphQuery defines parameters for knowledge graph traversal.
type GraphQuery struct {
	Subject   string // starting node (empty = any)
	Predicate string // relationship filter (empty = any)
	Object    string // ending node (empty = any)
	MaxHops   int    // maximum traversal depth (0 = direct only)
	Limit     int
}

// GraphPath represents a path found during graph traversal.
type GraphPath struct {
	Triples []Triple
	Score   float32 // relevance score for the path
}

// KnowledgeGraph is the interface for relational memory in JARV.
// Unlike flat memory entries, the graph stores *relationships* between entities.
type KnowledgeGraph interface {
	// AddTriple stores a new fact in the graph.
	AddTriple(ctx context.Context, t Triple) error

	// Query finds triples matching the given pattern.
	Query(ctx context.Context, q GraphQuery) ([]Triple, error)

	// Traverse performs multi-hop graph traversal starting from a subject.
	// Returns all paths up to MaxHops deep.
	Traverse(ctx context.Context, q GraphQuery) ([]GraphPath, error)

	// ExtractAndStore parses text and automatically extracts triples.
	ExtractAndStore(ctx context.Context, text, source string) ([]Triple, error)

	// DeleteTriple removes a specific triple by ID.
	DeleteTriple(ctx context.Context, id string) error

	// Stats returns graph statistics (node count, edge count, etc.).
	Stats(ctx context.Context) (map[string]int64, error)
}

// ─────────────────────────────────────────────────────────────────────────────
// Session Store — Conversation History
// ─────────────────────────────────────────────────────────────────────────────

// Turn represents a single exchange in a conversation.
type Turn struct {
	ID        string    `json:"id"`
	SessionID string    `json:"session_id"`
	Role      string    `json:"role"` // "user", "assistant", "system", "tool"
	Content   string    `json:"content"`
	Tokens    int       `json:"tokens,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// SessionStore manages conversation history with automatic summarization.
type SessionStore interface {
	// AppendTurn adds a new turn to a session.
	AppendTurn(ctx context.Context, turn Turn) error

	// GetHistory returns the N most recent turns for a session.
	GetHistory(ctx context.Context, sessionID string, limit int) ([]Turn, error)

	// Summarize compresses old turns into a summary to manage context length.
	Summarize(ctx context.Context, sessionID string, keepLast int) (string, error)

	// Clear removes all turns for a session.
	Clear(ctx context.Context, sessionID string) error

	// ActiveSessions returns all session IDs with recent activity.
	ActiveSessions(ctx context.Context, since time.Time) ([]string, error)
}

// ─────────────────────────────────────────────────────────────────────────────
// Sync Engine — Cloud Mirroring
// ─────────────────────────────────────────────────────────────────────────────

// SyncEngine handles bidirectional synchronization between local and cloud storage.
// It runs in background and never blocks the main agent loop.
type SyncEngine interface {
	// Start begins the background sync loop.
	Start(ctx context.Context) error

	// Stop gracefully halts the sync loop.
	Stop() error

	// ForceSync triggers an immediate sync cycle (for testing or manual trigger).
	ForceSync(ctx context.Context) error

	// Status returns the current sync status and statistics.
	Status() SyncStatus
}

// SyncStatus holds the current state of the sync engine.
type SyncStatus struct {
	Online         bool
	LastSyncAt     time.Time
	PendingEntries int
	TotalPushed    int64
	TotalPulled    int64
	LastError      string
}
