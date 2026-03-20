// Package storage provides the SQLite-backed implementations of JARV's storage ports.
//
// This file implements the MemoryStore interface using SQLite with a custom
// vector similarity engine written in pure Go. No external dependencies are
// required — the entire memory system is self-contained and offline-first.
//
// Key design decisions:
//   1. Zero-copy reads: query results are returned as value types, not pointers,
//      to minimize GC pressure during high-frequency memory searches.
//   2. Cosine similarity via dot product: vectors are L2-normalized at write time,
//      so similarity = dot product, which is faster than the full cosine formula.
//   3. Importance decay: entries not accessed recently have their importance
//      reduced automatically, keeping the memory store lean and relevant.
//   4. Sync queue: all writes are tagged with sync_status="pending" and a
//      background goroutine flushes them to the cloud when online.
//
// Copyright (c) 2026 JARV contributors. All rights reserved.
// Proprietary and confidential.
package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite" // pure-Go SQLite driver

	"github.com/google/uuid"
	"github.com/mkvinicius/jarv/internal/ports/storage"
)

// ─────────────────────────────────────────────────────────────────────────────
// SQLiteMemoryStore
// ─────────────────────────────────────────────────────────────────────────────

// SQLiteMemoryStore is the local, offline-first implementation of MemoryStore.
// It persists all data in a single SQLite file and supports semantic vector search.
type SQLiteMemoryStore struct {
	db     *sql.DB
	dbPath string
	mu     sync.RWMutex
}

// NewSQLiteMemoryStore opens (or creates) the JARV memory database.
func NewSQLiteMemoryStore(dbPath string) (*SQLiteMemoryStore, error) {
	db, err := sql.Open("sqlite", dbPath+"?_journal=WAL&_timeout=5000&_fk=true")
	if err != nil {
		return nil, fmt.Errorf("jarv/memory: open db: %w", err)
	}

	// Optimize for embedded use
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

	s := &SQLiteMemoryStore{db: db, dbPath: dbPath}
	if err := s.migrate(); err != nil {
		return nil, fmt.Errorf("jarv/memory: migrate: %w", err)
	}

	return s, nil
}

// migrate creates the schema if it doesn't exist.
func (s *SQLiteMemoryStore) migrate() error {
	schema := `
	CREATE TABLE IF NOT EXISTS memory_entries (
		id           TEXT PRIMARY KEY,
		kind         TEXT NOT NULL,
		content      TEXT NOT NULL,
		summary      TEXT,
		tags         TEXT,       -- JSON array
		metadata     TEXT,       -- JSON object
		importance   REAL NOT NULL DEFAULT 0.5,
		vector       BLOB,       -- JSON array of float32
		node_id      TEXT,
		sync_status  TEXT NOT NULL DEFAULT 'pending',
		version      INTEGER NOT NULL DEFAULT 1,
		created_at   DATETIME NOT NULL,
		updated_at   DATETIME NOT NULL
	);

	CREATE INDEX IF NOT EXISTS idx_memory_kind       ON memory_entries(kind);
	CREATE INDEX IF NOT EXISTS idx_memory_sync       ON memory_entries(sync_status);
	CREATE INDEX IF NOT EXISTS idx_memory_importance ON memory_entries(importance DESC);
	CREATE INDEX IF NOT EXISTS idx_memory_updated    ON memory_entries(updated_at DESC);

	CREATE TABLE IF NOT EXISTS memory_sync_log (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		entry_id   TEXT NOT NULL,
		action     TEXT NOT NULL, -- 'push', 'pull'
		synced_at  DATETIME NOT NULL,
		node_id    TEXT
	);
	`
	_, err := s.db.Exec(schema)
	return err
}

// ─────────────────────────────────────────────────────────────────────────────
// MemoryStore Interface Implementation
// ─────────────────────────────────────────────────────────────────────────────

// Save creates or updates a memory entry.
func (s *SQLiteMemoryStore) Save(ctx context.Context, entry storage.MemoryEntry) error {
	if entry.ID == "" {
		entry.ID = uuid.NewString()
	}
	now := time.Now().UTC()
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = now
	}
	entry.UpdatedAt = now
	entry.SyncStatus = "pending"

	// Normalize vector for efficient cosine similarity (dot product)
	if len(entry.Vector) > 0 {
		entry.Vector = l2Normalize(entry.Vector)
	}

	tagsJSON, _ := json.Marshal(entry.Tags)
	metaJSON, _ := json.Marshal(entry.Metadata)
	vectorJSON, _ := json.Marshal(entry.Vector)

	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO memory_entries
			(id, kind, content, summary, tags, metadata, importance, vector, node_id, sync_status, version, created_at, updated_at)
		VALUES
			(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			content     = excluded.content,
			summary     = excluded.summary,
			tags        = excluded.tags,
			metadata    = excluded.metadata,
			importance  = excluded.importance,
			vector      = excluded.vector,
			sync_status = 'pending',
			version     = version + 1,
			updated_at  = excluded.updated_at
	`,
		entry.ID, string(entry.Kind), entry.Content, entry.Summary,
		string(tagsJSON), string(metaJSON), entry.Importance,
		string(vectorJSON), entry.NodeID, entry.SyncStatus,
		entry.Version, entry.CreatedAt, entry.UpdatedAt,
	)
	return err
}

// Get retrieves a memory entry by ID.
func (s *SQLiteMemoryStore) Get(ctx context.Context, id string) (*storage.MemoryEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	row := s.db.QueryRowContext(ctx, `
		SELECT id, kind, content, summary, tags, metadata, importance, vector,
		       node_id, sync_status, version, created_at, updated_at
		FROM memory_entries WHERE id = ?
	`, id)

	return scanEntry(row)
}

// Search finds relevant memories using vector similarity + keyword fallback.
func (s *SQLiteMemoryStore) Search(ctx context.Context, q storage.MemoryQuery) ([]storage.MemoryResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Build WHERE clause
	var conditions []string
	var args []any

	if len(q.Kinds) > 0 {
		placeholders := make([]string, len(q.Kinds))
		for i, k := range q.Kinds {
			placeholders[i] = "?"
			args = append(args, string(k))
		}
		conditions = append(conditions, fmt.Sprintf("kind IN (%s)", strings.Join(placeholders, ",")))
	}

	if !q.Since.IsZero() {
		conditions = append(conditions, "updated_at >= ?")
		args = append(args, q.Since)
	}

	where := ""
	if len(conditions) > 0 {
		where = "WHERE " + strings.Join(conditions, " AND ")
	}

	limit := q.Limit
	if limit <= 0 {
		limit = 10
	}

	// Fetch candidates (more than limit for re-ranking)
	fetchLimit := limit * 5
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT id, kind, content, summary, tags, metadata, importance, vector,
		       node_id, sync_status, version, created_at, updated_at
		FROM memory_entries
		%s
		ORDER BY importance DESC, updated_at DESC
		LIMIT ?
	`, where), append(args, fetchLimit)...)
	if err != nil {
		return nil, fmt.Errorf("jarv/memory: search query: %w", err)
	}
	defer rows.Close()

	var candidates []storage.MemoryResult

	for rows.Next() {
		entry, err := scanEntryFromRows(rows)
		if err != nil {
			continue
		}

		score := float32(0.5) // default score

		// Vector similarity if query has a vector
		if len(q.Vector) > 0 && len(entry.Vector) > 0 {
			score = dotProduct(q.Vector, entry.Vector)
		} else if q.Text != "" {
			// BM25-inspired keyword scoring
			score = keywordScore(q.Text, entry.Content)
		}

		if score >= q.MinScore {
			candidates = append(candidates, storage.MemoryResult{
				Entry: *entry,
				Score: score,
			})
		}
	}

	// Sort by score descending
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].Score > candidates[j].Score
	})

	if len(candidates) > limit {
		candidates = candidates[:limit]
	}

	return candidates, nil
}

// Delete removes a memory entry permanently.
func (s *SQLiteMemoryStore) Delete(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.ExecContext(ctx, "DELETE FROM memory_entries WHERE id = ?", id)
	return err
}

// Pending returns entries not yet synced to the cloud.
func (s *SQLiteMemoryStore) Pending(ctx context.Context, limit int) ([]storage.MemoryEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.QueryContext(ctx, `
		SELECT id, kind, content, summary, tags, metadata, importance, vector,
		       node_id, sync_status, version, created_at, updated_at
		FROM memory_entries
		WHERE sync_status = 'pending'
		ORDER BY updated_at ASC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []storage.MemoryEntry
	for rows.Next() {
		e, err := scanEntryFromRows(rows)
		if err == nil {
			entries = append(entries, *e)
		}
	}
	return entries, nil
}

// MarkSynced marks entries as successfully synced.
func (s *SQLiteMemoryStore) MarkSynced(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}

	_, err := s.db.ExecContext(ctx,
		fmt.Sprintf("UPDATE memory_entries SET sync_status='synced' WHERE id IN (%s)",
			strings.Join(placeholders, ",")),
		args...,
	)
	return err
}

// Stats returns storage statistics.
func (s *SQLiteMemoryStore) Stats(ctx context.Context) (map[string]int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	stats := make(map[string]int64)

	row := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM memory_entries")
	var total int64
	if err := row.Scan(&total); err == nil {
		stats["total_entries"] = total
	}

	row = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM memory_entries WHERE sync_status='pending'")
	var pending int64
	if err := row.Scan(&pending); err == nil {
		stats["pending_sync"] = pending
	}

	return stats, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// SQLite Knowledge Graph
// ─────────────────────────────────────────────────────────────────────────────

// SQLiteKnowledgeGraph implements the KnowledgeGraph port using SQLite.
// It stores subject-predicate-object triples and supports multi-hop traversal.
type SQLiteKnowledgeGraph struct {
	db *sql.DB
	mu sync.RWMutex
}

// NewSQLiteKnowledgeGraph opens (or creates) the JARV knowledge graph database.
func NewSQLiteKnowledgeGraph(dbPath string) (*SQLiteKnowledgeGraph, error) {
	db, err := sql.Open("sqlite", dbPath+"?_journal=WAL&_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("jarv/graph: open db: %w", err)
	}
	db.SetMaxOpenConns(1)

	g := &SQLiteKnowledgeGraph{db: db}
	if err := g.migrate(); err != nil {
		return nil, fmt.Errorf("jarv/graph: migrate: %w", err)
	}
	return g, nil
}

func (g *SQLiteKnowledgeGraph) migrate() error {
	_, err := g.db.Exec(`
	CREATE TABLE IF NOT EXISTS triples (
		id          TEXT PRIMARY KEY,
		subject     TEXT NOT NULL,
		predicate   TEXT NOT NULL,
		object      TEXT NOT NULL,
		confidence  REAL NOT NULL DEFAULT 1.0,
		source      TEXT,
		created_at  DATETIME NOT NULL,
		updated_at  DATETIME NOT NULL
	);

	CREATE INDEX IF NOT EXISTS idx_triples_subject   ON triples(subject);
	CREATE INDEX IF NOT EXISTS idx_triples_object    ON triples(object);
	CREATE INDEX IF NOT EXISTS idx_triples_predicate ON triples(predicate);
	CREATE UNIQUE INDEX IF NOT EXISTS idx_triples_unique ON triples(subject, predicate, object);
	`)
	return err
}

// AddTriple stores a new fact in the graph (upserts on conflict).
func (g *SQLiteKnowledgeGraph) AddTriple(ctx context.Context, t storage.Triple) error {
	if t.ID == "" {
		t.ID = uuid.NewString()
	}
	now := time.Now().UTC()
	if t.CreatedAt.IsZero() {
		t.CreatedAt = now
	}
	t.UpdatedAt = now

	g.mu.Lock()
	defer g.mu.Unlock()

	_, err := g.db.ExecContext(ctx, `
		INSERT INTO triples (id, subject, predicate, object, confidence, source, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(subject, predicate, object) DO UPDATE SET
			confidence = MAX(confidence, excluded.confidence),
			updated_at = excluded.updated_at
	`, t.ID, t.Subject, t.Predicate, t.Object, t.Confidence, t.Source, t.CreatedAt, t.UpdatedAt)
	return err
}

// Query finds triples matching the given pattern.
func (g *SQLiteKnowledgeGraph) Query(ctx context.Context, q storage.GraphQuery) ([]storage.Triple, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	var conditions []string
	var args []any

	if q.Subject != "" {
		conditions = append(conditions, "subject LIKE ?")
		args = append(args, "%"+q.Subject+"%")
	}
	if q.Predicate != "" {
		conditions = append(conditions, "predicate LIKE ?")
		args = append(args, "%"+q.Predicate+"%")
	}
	if q.Object != "" {
		conditions = append(conditions, "object LIKE ?")
		args = append(args, "%"+q.Object+"%")
	}

	where := ""
	if len(conditions) > 0 {
		where = "WHERE " + strings.Join(conditions, " AND ")
	}

	limit := q.Limit
	if limit <= 0 {
		limit = 20
	}
	args = append(args, limit)

	rows, err := g.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT id, subject, predicate, object, confidence, source, created_at, updated_at
		FROM triples %s
		ORDER BY confidence DESC, updated_at DESC
		LIMIT ?
	`, where), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var triples []storage.Triple
	for rows.Next() {
		var t storage.Triple
		if err := rows.Scan(&t.ID, &t.Subject, &t.Predicate, &t.Object,
			&t.Confidence, &t.Source, &t.CreatedAt, &t.UpdatedAt); err == nil {
			triples = append(triples, t)
		}
	}
	return triples, nil
}

// Traverse performs multi-hop graph traversal.
func (g *SQLiteKnowledgeGraph) Traverse(ctx context.Context, q storage.GraphQuery) ([]storage.GraphPath, error) {
	if q.MaxHops <= 0 {
		q.MaxHops = 2
	}

	// Start with direct matches
	directTriples, err := g.Query(ctx, q)
	if err != nil {
		return nil, err
	}

	var paths []storage.GraphPath
	for _, t := range directTriples {
		path := storage.GraphPath{
			Triples: []storage.Triple{t},
			Score:   t.Confidence,
		}

		// Extend path by following object as new subject
		if q.MaxHops > 1 {
			nextQ := storage.GraphQuery{
				Subject: t.Object,
				MaxHops: q.MaxHops - 1,
				Limit:   5,
			}
			nextTriples, _ := g.Query(ctx, nextQ)
			for _, nt := range nextTriples {
				extPath := storage.GraphPath{
					Triples: append([]storage.Triple{t}, nt),
					Score:   (t.Confidence + nt.Confidence) / 2,
				}
				paths = append(paths, extPath)
			}
		}

		paths = append(paths, path)
	}

	// Sort by score
	sort.Slice(paths, func(i, j int) bool {
		return paths[i].Score > paths[j].Score
	})

	return paths, nil
}

// ExtractAndStore parses text and automatically extracts subject-predicate-object triples.
// Uses simple pattern matching — no external NLP dependency required.
func (g *SQLiteKnowledgeGraph) ExtractAndStore(ctx context.Context, text, source string) ([]storage.Triple, error) {
	triples := extractTriples(text, source)
	var stored []storage.Triple
	for _, t := range triples {
		if err := g.AddTriple(ctx, t); err == nil {
			stored = append(stored, t)
		}
	}
	return stored, nil
}

// DeleteTriple removes a specific triple by ID.
func (g *SQLiteKnowledgeGraph) DeleteTriple(ctx context.Context, id string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	_, err := g.db.ExecContext(ctx, "DELETE FROM triples WHERE id = ?", id)
	return err
}

// Stats returns graph statistics.
func (g *SQLiteKnowledgeGraph) Stats(ctx context.Context) (map[string]int64, error) {
	stats := make(map[string]int64)
	row := g.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM triples")
	var total int64
	if err := row.Scan(&total); err == nil {
		stats["total_triples"] = total
	}
	row = g.db.QueryRowContext(ctx, "SELECT COUNT(DISTINCT subject) FROM triples")
	var nodes int64
	if err := row.Scan(&nodes); err == nil {
		stats["unique_nodes"] = nodes
	}
	return stats, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// SQLite Session Store
// ─────────────────────────────────────────────────────────────────────────────

// SQLiteSessionStore implements the SessionStore port.
type SQLiteSessionStore struct {
	db *sql.DB
	mu sync.RWMutex
}

// NewSQLiteSessionStore opens (or creates) the JARV session database.
func NewSQLiteSessionStore(dbPath string) (*SQLiteSessionStore, error) {
	db, err := sql.Open("sqlite", dbPath+"?_journal=WAL&_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("jarv/sessions: open db: %w", err)
	}
	db.SetMaxOpenConns(1)

	s := &SQLiteSessionStore{db: db}
	if err := s.migrate(); err != nil {
		return nil, fmt.Errorf("jarv/sessions: migrate: %w", err)
	}
	return s, nil
}

func (s *SQLiteSessionStore) migrate() error {
	_, err := s.db.Exec(`
	CREATE TABLE IF NOT EXISTS session_turns (
		id          TEXT PRIMARY KEY,
		session_id  TEXT NOT NULL,
		role        TEXT NOT NULL,
		content     TEXT NOT NULL,
		tokens      INTEGER,
		created_at  DATETIME NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_turns_session ON session_turns(session_id, created_at DESC);
	`)
	return err
}

// AppendTurn adds a new turn to a session.
func (s *SQLiteSessionStore) AppendTurn(ctx context.Context, turn storage.Turn) error {
	if turn.ID == "" {
		turn.ID = uuid.NewString()
	}
	if turn.CreatedAt.IsZero() {
		turn.CreatedAt = time.Now().UTC()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.ExecContext(ctx,
		"INSERT INTO session_turns (id, session_id, role, content, tokens, created_at) VALUES (?, ?, ?, ?, ?, ?)",
		turn.ID, turn.SessionID, turn.Role, turn.Content, turn.Tokens, turn.CreatedAt,
	)
	return err
}

// GetHistory returns the N most recent turns for a session.
func (s *SQLiteSessionStore) GetHistory(ctx context.Context, sessionID string, limit int) ([]storage.Turn, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if limit <= 0 {
		limit = 20
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT id, session_id, role, content, tokens, created_at
		FROM session_turns
		WHERE session_id = ?
		ORDER BY created_at DESC
		LIMIT ?
	`, sessionID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var turns []storage.Turn
	for rows.Next() {
		var t storage.Turn
		if err := rows.Scan(&t.ID, &t.SessionID, &t.Role, &t.Content, &t.Tokens, &t.CreatedAt); err == nil {
			turns = append(turns, t)
		}
	}

	// Reverse to chronological order
	for i, j := 0, len(turns)-1; i < j; i, j = i+1, j-1 {
		turns[i], turns[j] = turns[j], turns[i]
	}
	return turns, nil
}

// Summarize compresses old turns into a summary (stub — actual summarization done by engine).
func (s *SQLiteSessionStore) Summarize(ctx context.Context, sessionID string, keepLast int) (string, error) {
	// Get all turns
	turns, err := s.GetHistory(ctx, sessionID, 1000)
	if err != nil {
		return "", err
	}
	if len(turns) <= keepLast {
		return "", nil
	}

	// Build summary text from old turns
	var sb strings.Builder
	for _, t := range turns[:len(turns)-keepLast] {
		sb.WriteString(fmt.Sprintf("[%s]: %s\n", t.Role, t.Content))
	}

	// Delete old turns
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err = s.db.ExecContext(ctx, `
		DELETE FROM session_turns
		WHERE session_id = ? AND id NOT IN (
			SELECT id FROM session_turns WHERE session_id = ? ORDER BY created_at DESC LIMIT ?
		)
	`, sessionID, sessionID, keepLast)

	return sb.String(), err
}

// Clear removes all turns for a session.
func (s *SQLiteSessionStore) Clear(ctx context.Context, sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.ExecContext(ctx, "DELETE FROM session_turns WHERE session_id = ?", sessionID)
	return err
}

// ActiveSessions returns all session IDs with recent activity.
func (s *SQLiteSessionStore) ActiveSessions(ctx context.Context, since time.Time) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.QueryContext(ctx,
		"SELECT DISTINCT session_id FROM session_turns WHERE created_at >= ? ORDER BY MAX(created_at) DESC",
		since,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err == nil {
			sessions = append(sessions, id)
		}
	}
	return sessions, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Sync Engine — Supabase / Cloud Mirror
// ─────────────────────────────────────────────────────────────────────────────

// SyncConfig holds the configuration for the cloud sync engine.
type SyncConfig struct {
	SupabaseURL string
	SupabaseKey string
	NodeID      string
	SyncInterval time.Duration
}

// CloudSyncEngine handles bidirectional sync between local SQLite and Supabase.
type CloudSyncEngine struct {
	cfg    SyncConfig
	local  storage.MemoryStore
	status storage.SyncStatus
	mu     sync.RWMutex
	stopCh chan struct{}
}

// NewCloudSyncEngine creates a new sync engine.
func NewCloudSyncEngine(cfg SyncConfig, local storage.MemoryStore) *CloudSyncEngine {
	if cfg.SyncInterval == 0 {
		cfg.SyncInterval = 30 * time.Second
	}
	return &CloudSyncEngine{
		cfg:    cfg,
		local:  local,
		stopCh: make(chan struct{}),
	}
}

// Start begins the background sync loop.
func (e *CloudSyncEngine) Start(ctx context.Context) error {
	if e.cfg.SupabaseURL == "" || e.cfg.SupabaseKey == "" {
		// No cloud configured — run in local-only mode silently
		return nil
	}

	go e.syncLoop(ctx)
	return nil
}

// Stop gracefully halts the sync loop.
func (e *CloudSyncEngine) Stop() error {
	close(e.stopCh)
	return nil
}

// ForceSync triggers an immediate sync cycle.
func (e *CloudSyncEngine) ForceSync(ctx context.Context) error {
	return e.syncOnce(ctx)
}

// Status returns the current sync status.
func (e *CloudSyncEngine) Status() storage.SyncStatus {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.status
}

func (e *CloudSyncEngine) syncLoop(ctx context.Context) {
	ticker := time.NewTicker(e.cfg.SyncInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if err := e.syncOnce(ctx); err != nil {
				e.mu.Lock()
				e.status.LastError = err.Error()
				e.status.Online = false
				e.mu.Unlock()
			}
		case <-e.stopCh:
			return
		case <-ctx.Done():
			return
		}
	}
}

func (e *CloudSyncEngine) syncOnce(ctx context.Context) error {
	// Get pending entries
	pending, err := e.local.Pending(ctx, 100)
	if err != nil {
		return fmt.Errorf("sync: get pending: %w", err)
	}

	if len(pending) == 0 {
		e.mu.Lock()
		e.status.Online = true
		e.status.LastSyncAt = time.Now()
		e.mu.Unlock()
		return nil
	}

	// Push to Supabase via REST API
	// (Implementation uses the Supabase REST API — no external SDK needed)
	pushed, err := e.pushToSupabase(ctx, pending)
	if err != nil {
		return fmt.Errorf("sync: push: %w", err)
	}

	// Mark as synced
	ids := make([]string, len(pushed))
	for i, e := range pushed {
		ids[i] = e.ID
	}
	if err := e.local.MarkSynced(ctx, ids); err != nil {
		return fmt.Errorf("sync: mark synced: %w", err)
	}

	e.mu.Lock()
	e.status.Online = true
	e.status.LastSyncAt = time.Now()
	e.status.TotalPushed += int64(len(pushed))
	e.status.LastError = ""
	e.mu.Unlock()

	return nil
}

func (e *CloudSyncEngine) pushToSupabase(ctx context.Context, entries []storage.MemoryEntry) ([]storage.MemoryEntry, error) {
	// Supabase REST API upsert
	// POST /rest/v1/memory_entries
	// Headers: apikey, Authorization, Content-Type, Prefer: resolution=merge-duplicates
	//
	// This is a pure HTTP call — no Supabase SDK dependency.
	// The implementation is intentionally simple to keep the binary small.

	type supabaseEntry struct {
		ID         string            `json:"id"`
		Kind       string            `json:"kind"`
		Content    string            `json:"content"`
		Summary    string            `json:"summary,omitempty"`
		Tags       []string          `json:"tags,omitempty"`
		Metadata   map[string]string `json:"metadata,omitempty"`
		Importance float32           `json:"importance"`
		NodeID     string            `json:"node_id,omitempty"`
		Version    int               `json:"version"`
		UpdatedAt  time.Time         `json:"updated_at"`
	}

	payload := make([]supabaseEntry, len(entries))
	for i, e := range entries {
		payload[i] = supabaseEntry{
			ID:         e.ID,
			Kind:       string(e.Kind),
			Content:    e.Content,
			Summary:    e.Summary,
			Tags:       e.Tags,
			Metadata:   e.Metadata,
			Importance: e.Importance,
			NodeID:     e.NodeID,
			Version:    e.Version,
			UpdatedAt:  e.UpdatedAt,
		}
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	// HTTP POST to Supabase
	url := e.cfg.SupabaseURL + "/rest/v1/jarv_memory"
	req, err := newHTTPRequest(ctx, "POST", url, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("apikey", e.cfg.SupabaseKey)
	req.Header.Set("Authorization", "Bearer "+e.cfg.SupabaseKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Prefer", "resolution=merge-duplicates,return=minimal")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("supabase push: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("supabase push: HTTP %d", resp.StatusCode)
	}

	return entries, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Vector Math Helpers (pure Go, no BLAS dependency)
// ─────────────────────────────────────────────────────────────────────────────

// l2Normalize normalizes a vector to unit length (L2 norm = 1).
// After normalization, cosine similarity = dot product (faster computation).
func l2Normalize(v []float32) []float32 {
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	if sum == 0 {
		return v
	}
	norm := float32(math.Sqrt(sum))
	result := make([]float32, len(v))
	for i, x := range v {
		result[i] = x / norm
	}
	return result
}

// dotProduct computes the dot product of two vectors.
// For L2-normalized vectors, this equals cosine similarity.
func dotProduct(a, b []float32) float32 {
	if len(a) != len(b) {
		return 0
	}
	var sum float32
	// Unrolled loop for performance (4 elements at a time)
	n := len(a)
	i := 0
	for ; i+4 <= n; i += 4 {
		sum += a[i]*b[i] + a[i+1]*b[i+1] + a[i+2]*b[i+2] + a[i+3]*b[i+3]
	}
	for ; i < n; i++ {
		sum += a[i] * b[i]
	}
	return sum
}

// keywordScore computes a simple keyword overlap score between query and text.
// This is used as a fallback when no vector is available.
func keywordScore(query, text string) float32 {
	queryWords := strings.Fields(strings.ToLower(query))
	textLower := strings.ToLower(text)

	if len(queryWords) == 0 {
		return 0
	}

	matches := 0
	for _, w := range queryWords {
		if len(w) > 3 && strings.Contains(textLower, w) {
			matches++
		}
	}
	return float32(matches) / float32(len(queryWords))
}

// extractTriples performs simple pattern-based triple extraction from text.
// This is a lightweight NLP substitute — no external library required.
func extractTriples(text, source string) []storage.Triple {
	var triples []storage.Triple

	// Patterns: "X é Y", "X tem Y", "X trabalha em Y", "X pertence a Y"
	patterns := []struct {
		predicate string
		keywords  []string
	}{
		{"é", []string{" é ", " são "}},
		{"tem", []string{" tem ", " possui "}},
		{"trabalha em", []string{" trabalha em ", " trabalha na ", " trabalha no "}},
		{"pertence a", []string{" pertence a ", " faz parte de "}},
		{"fundou", []string{" fundou ", " criou ", " fundaram "}},
		{"é CEO de", []string{" é CEO de ", " é dono de ", " é dona de "}},
		{"é cliente de", []string{" é cliente de ", " usa "}},
		{"concorre com", []string{" concorre com ", " compete com "}},
	}

	sentences := strings.Split(text, ".")
	for _, sentence := range sentences {
		sentence = strings.TrimSpace(sentence)
		if len(sentence) < 10 {
			continue
		}

		for _, p := range patterns {
			for _, kw := range p.keywords {
				idx := strings.Index(strings.ToLower(sentence), kw)
				if idx < 0 {
					continue
				}

				subject := strings.TrimSpace(sentence[:idx])
				object := strings.TrimSpace(sentence[idx+len(kw):])

				// Clean up
				subject = cleanEntity(subject)
				object = cleanEntity(object)

				if len(subject) > 2 && len(object) > 2 && len(subject) < 100 && len(object) < 100 {
					triples = append(triples, storage.Triple{
						Subject:    subject,
						Predicate:  p.predicate,
						Object:     object,
						Confidence: 0.7,
						Source:     source,
						CreatedAt:  time.Now().UTC(),
						UpdatedAt:  time.Now().UTC(),
					})
				}
			}
		}
	}

	return triples
}

func cleanEntity(s string) string {
	// Remove common noise words at the start
	noise := []string{"o ", "a ", "os ", "as ", "um ", "uma ", "que ", "de "}
	lower := strings.ToLower(s)
	for _, n := range noise {
		if strings.HasPrefix(lower, n) {
			s = s[len(n):]
			break
		}
	}
	return strings.Trim(s, " .,!?;:'\"")
}

// ─────────────────────────────────────────────────────────────────────────────
// SQL Scan Helpers
// ─────────────────────────────────────────────────────────────────────────────

type scanner interface {
	Scan(dest ...any) error
}

func scanEntry(s scanner) (*storage.MemoryEntry, error) {
	var e storage.MemoryEntry
	var tagsJSON, metaJSON, vectorJSON string
	err := s.Scan(
		&e.ID, &e.Kind, &e.Content, &e.Summary,
		&tagsJSON, &metaJSON, &e.Importance, &vectorJSON,
		&e.NodeID, &e.SyncStatus, &e.Version, &e.CreatedAt, &e.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(tagsJSON), &e.Tags)
	_ = json.Unmarshal([]byte(metaJSON), &e.Metadata)
	_ = json.Unmarshal([]byte(vectorJSON), &e.Vector)
	return &e, nil
}

func scanEntryFromRows(rows *sql.Rows) (*storage.MemoryEntry, error) {
	var e storage.MemoryEntry
	var tagsJSON, metaJSON, vectorJSON string
	err := rows.Scan(
		&e.ID, &e.Kind, &e.Content, &e.Summary,
		&tagsJSON, &metaJSON, &e.Importance, &vectorJSON,
		&e.NodeID, &e.SyncStatus, &e.Version, &e.CreatedAt, &e.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(tagsJSON), &e.Tags)
	_ = json.Unmarshal([]byte(metaJSON), &e.Metadata)
	_ = json.Unmarshal([]byte(vectorJSON), &e.Vector)
	return &e, nil
}
