// Package storage — pure in-memory implementations of JARV's storage ports.
//
// This file is the default (no build tags). It provides the same interfaces as
// the SQLite-backed version but stores everything in RAM. Ideal for development,
// testing, and environments where persistence is not required.
//
// To use persistent SQLite storage, build with: -tags sqlite
//
// Copyright (c) 2026 JARV contributors. All rights reserved.
// Proprietary and confidential.
package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/mkvinicius/jarv/internal/ports/storage"
)

// ─────────────────────────────────────────────────────────────────────────────
// InMemoryStore — implements storage.MemoryStore
// ─────────────────────────────────────────────────────────────────────────────

// InMemoryStore holds all memory entries in a slice protected by a RWMutex.
type InMemoryStore struct {
	mu      sync.RWMutex
	entries []*storage.MemoryEntry
	nodeID  string
}

// NewInMemoryStore creates a new in-memory store.
func NewInMemoryStore(nodeID string) *InMemoryStore {
	return &InMemoryStore{nodeID: nodeID}
}

func (s *InMemoryStore) Save(_ context.Context, entry storage.MemoryEntry) error {
	if entry.ID == "" {
		entry.ID = uuid.NewString()
	}
	now := time.Now().UTC()
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = now
	}
	entry.UpdatedAt = now
	entry.SyncStatus = "pending"
	if entry.NodeID == "" {
		entry.NodeID = s.nodeID
	}
	if len(entry.Vector) > 0 {
		entry.Vector = l2Normalize(entry.Vector)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for i, e := range s.entries {
		if e.ID == entry.ID {
			entry.Version = e.Version + 1
			s.entries[i] = &entry
			return nil
		}
	}
	entry.Version = 1
	s.entries = append(s.entries, &entry)
	return nil
}

func (s *InMemoryStore) Get(_ context.Context, id string) (*storage.MemoryEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, e := range s.entries {
		if e.ID == id {
			cp := *e
			return &cp, nil
		}
	}
	return nil, fmt.Errorf("memory entry %q not found", id)
}

func (s *InMemoryStore) Search(_ context.Context, q storage.MemoryQuery) ([]storage.MemoryResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	limit := q.Limit
	if limit <= 0 {
		limit = 10
	}

	var results []storage.MemoryResult
	for _, e := range s.entries {
		if len(q.Kinds) > 0 {
			found := false
			for _, k := range q.Kinds {
				if e.Kind == k {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}
		if !q.Since.IsZero() && e.UpdatedAt.Before(q.Since) {
			continue
		}

		var score float32
		if len(q.Vector) > 0 && len(e.Vector) > 0 {
			score = dotProduct(q.Vector, e.Vector)
		} else if q.Text != "" {
			score = keywordScore(q.Text, e.Content)
		} else {
			score = e.Importance
		}

		if score >= q.MinScore {
			cp := *e
			results = append(results, storage.MemoryResult{Entry: cp, Score: score})
		}
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})
	if len(results) > limit {
		results = results[:limit]
	}
	return results, nil
}

func (s *InMemoryStore) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, e := range s.entries {
		if e.ID == id {
			s.entries = append(s.entries[:i], s.entries[i+1:]...)
			return nil
		}
	}
	return nil
}

func (s *InMemoryStore) Pending(_ context.Context, limit int) ([]storage.MemoryEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []storage.MemoryEntry
	for _, e := range s.entries {
		if e.SyncStatus == "pending" {
			out = append(out, *e)
			if len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

func (s *InMemoryStore) MarkSynced(_ context.Context, ids []string) error {
	idSet := make(map[string]bool, len(ids))
	for _, id := range ids {
		idSet[id] = true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.entries {
		if idSet[e.ID] {
			e.SyncStatus = "synced"
		}
	}
	return nil
}

func (s *InMemoryStore) Stats(_ context.Context) (map[string]int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var pending int64
	for _, e := range s.entries {
		if e.SyncStatus == "pending" {
			pending++
		}
	}
	return map[string]int64{
		"total":   int64(len(s.entries)),
		"pending": pending,
	}, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// InMemoryGraph — implements storage.KnowledgeGraph
// ─────────────────────────────────────────────────────────────────────────────

// InMemoryGraph stores knowledge graph triples in memory.
type InMemoryGraph struct {
	mu      sync.RWMutex
	triples []*storage.Triple
}

// NewInMemoryGraph creates a new in-memory knowledge graph.
func NewInMemoryGraph() *InMemoryGraph {
	return &InMemoryGraph{}
}

func (g *InMemoryGraph) AddTriple(_ context.Context, t storage.Triple) error {
	if t.ID == "" {
		t.ID = uuid.NewString()
	}
	now := time.Now().UTC()
	if t.CreatedAt.IsZero() {
		t.CreatedAt = now
	}
	t.UpdatedAt = now
	g.mu.Lock()
	g.triples = append(g.triples, &t)
	g.mu.Unlock()
	return nil
}

func (g *InMemoryGraph) Query(_ context.Context, q storage.GraphQuery) ([]storage.Triple, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	limit := q.Limit
	if limit <= 0 {
		limit = 50
	}
	var out []storage.Triple
	for _, t := range g.triples {
		if q.Subject != "" && !strings.EqualFold(t.Subject, q.Subject) {
			continue
		}
		if q.Predicate != "" && !strings.EqualFold(t.Predicate, q.Predicate) {
			continue
		}
		if q.Object != "" && !strings.EqualFold(t.Object, q.Object) {
			continue
		}
		out = append(out, *t)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (g *InMemoryGraph) Traverse(ctx context.Context, q storage.GraphQuery) ([]storage.GraphPath, error) {
	triples, err := g.Query(ctx, q)
	if err != nil || len(triples) == 0 {
		return nil, err
	}
	return []storage.GraphPath{{Triples: triples, Score: 1.0}}, nil
}

func (g *InMemoryGraph) ExtractAndStore(ctx context.Context, text, source string) ([]storage.Triple, error) {
	extracted := extractTriples(text, source)
	for _, t := range extracted {
		if err := g.AddTriple(ctx, t); err != nil {
			return nil, err
		}
	}
	return extracted, nil
}

func (g *InMemoryGraph) DeleteTriple(_ context.Context, id string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	for i, t := range g.triples {
		if t.ID == id {
			g.triples = append(g.triples[:i], g.triples[i+1:]...)
			return nil
		}
	}
	return nil
}

func (g *InMemoryGraph) Stats(_ context.Context) (map[string]int64, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	subjects := make(map[string]bool)
	for _, t := range g.triples {
		subjects[t.Subject] = true
	}
	return map[string]int64{
		"edges": int64(len(g.triples)),
		"nodes": int64(len(subjects)),
	}, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// InMemorySessionStore — implements storage.SessionStore
// ─────────────────────────────────────────────────────────────────────────────

// InMemorySessionStore stores conversation history in memory.
type InMemorySessionStore struct {
	mu       sync.RWMutex
	sessions map[string][]storage.Turn
}

// NewInMemorySessionStore creates a new in-memory session store.
func NewInMemorySessionStore() *InMemorySessionStore {
	return &InMemorySessionStore{sessions: make(map[string][]storage.Turn)}
}

func (s *InMemorySessionStore) AppendTurn(_ context.Context, turn storage.Turn) error {
	if turn.ID == "" {
		turn.ID = uuid.NewString()
	}
	if turn.CreatedAt.IsZero() {
		turn.CreatedAt = time.Now().UTC()
	}
	s.mu.Lock()
	s.sessions[turn.SessionID] = append(s.sessions[turn.SessionID], turn)
	s.mu.Unlock()
	return nil
}

func (s *InMemorySessionStore) GetHistory(_ context.Context, sessionID string, limit int) ([]storage.Turn, error) {
	s.mu.RLock()
	turns := s.sessions[sessionID]
	s.mu.RUnlock()
	if limit <= 0 || len(turns) <= limit {
		return turns, nil
	}
	return turns[len(turns)-limit:], nil
}

func (s *InMemorySessionStore) Summarize(_ context.Context, sessionID string, keepLast int) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	turns := s.sessions[sessionID]
	if len(turns) <= keepLast {
		return "", nil
	}
	summary := fmt.Sprintf("[Conversa anterior com %d mensagens resumida]", len(turns)-keepLast)
	s.sessions[sessionID] = turns[len(turns)-keepLast:]
	return summary, nil
}

func (s *InMemorySessionStore) Clear(_ context.Context, sessionID string) error {
	s.mu.Lock()
	delete(s.sessions, sessionID)
	s.mu.Unlock()
	return nil
}

func (s *InMemorySessionStore) ActiveSessions(_ context.Context, since time.Time) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var ids []string
	for id, turns := range s.sessions {
		if len(turns) > 0 && turns[len(turns)-1].CreatedAt.After(since) {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// InMemorySyncEngine — implements storage.SyncEngine (no-op offline)
// ─────────────────────────────────────────────────────────────────────────────

// InMemorySyncEngine is a no-op sync engine for offline/in-memory mode.
type InMemorySyncEngine struct{}

// NewInMemorySyncEngine creates a no-op sync engine.
func NewInMemorySyncEngine() *InMemorySyncEngine { return &InMemorySyncEngine{} }

func (e *InMemorySyncEngine) Start(_ context.Context) error        { return nil }
func (e *InMemorySyncEngine) Stop() error                          { return nil }
func (e *InMemorySyncEngine) ForceSync(_ context.Context) error    { return nil }
func (e *InMemorySyncEngine) Status() storage.SyncStatus           { return storage.SyncStatus{} }

// ─────────────────────────────────────────────────────────────────────────────
// Shared helpers (used by both in-memory and sqlite implementations)
// ─────────────────────────────────────────────────────────────────────────────

// l2Normalize returns a unit-length copy of v.
func l2Normalize(v []float32) []float32 {
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	if sum == 0 {
		return v
	}
	norm := float32(math.Sqrt(sum))
	out := make([]float32, len(v))
	for i, x := range v {
		out[i] = x / norm
	}
	return out
}

// dotProduct computes the dot product of two equal-length vectors.
func dotProduct(a, b []float32) float32 {
	var sum float32
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		sum += a[i] * b[i]
	}
	return sum
}

// keywordScore returns a simple word-overlap similarity score (0–1).
func keywordScore(query, content string) float32 {
	qWords := tokenize(query)
	if len(qWords) == 0 {
		return 0
	}
	cWords := tokenize(content)
	cSet := make(map[string]bool, len(cWords))
	for _, w := range cWords {
		cSet[w] = true
	}
	var hits int
	for _, w := range qWords {
		if cSet[w] {
			hits++
		}
	}
	return float32(hits) / float32(len(qWords))
}

func tokenize(text string) []string {
	lower := strings.ToLower(text)
	return strings.FieldsFunc(lower, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

// extractTriples parses text for simple "X is Y" / "X has Y" patterns.
func extractTriples(text, source string) []storage.Triple {
	var triples []storage.Triple
	sentences := strings.Split(text, ".")
	patterns := []struct{ pred, sep string }{
		{"is", " is "},
		{"has", " has "},
		{"é", " é "},
		{"tem", " tem "},
		{"foi", " foi "},
	}
	for _, sent := range sentences {
		sent = strings.TrimSpace(sent)
		for _, p := range patterns {
			if idx := strings.Index(strings.ToLower(sent), p.sep); idx > 0 {
				subj := strings.TrimSpace(sent[:idx])
				obj := strings.TrimSpace(sent[idx+len(p.sep):])
				if len(subj) > 2 && len(obj) > 2 && len(subj) < 80 && len(obj) < 80 {
					triples = append(triples, storage.Triple{
						ID:         uuid.NewString(),
						Subject:    subj,
						Predicate:  p.pred,
						Object:     obj,
						Confidence: 0.6,
						Source:     source,
						CreatedAt:  time.Now().UTC(),
						UpdatedAt:  time.Now().UTC(),
					})
				}
				break
			}
		}
	}
	return triples
}

// jsonMarshal is a helper to silence unused import warnings in sqlite build.
var _ = json.Marshal
