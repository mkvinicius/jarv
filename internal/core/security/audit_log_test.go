// audit_log_test.go — Unit tests for the JARV Replicated Audit Log.
package security

import (
	"context"
	"crypto/rand"
	"os"
	"path/filepath"
	"testing"
)

func newTestAuditLog(t *testing.T) (*ReplicatedAuditLog, string) {
	t.Helper()
	dir := t.TempDir()
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	cfg := DefaultAuditLogConfig(dir, key)
	al, err := NewReplicatedAuditLog(cfg)
	if err != nil {
		t.Fatalf("failed to create audit log: %v", err)
	}
	t.Cleanup(func() { _ = al.Close() })
	return al, dir
}

func TestAuditLog_WriteAndRead(t *testing.T) {
	al, dir := newTestAuditLog(t)

	err := al.Log(context.Background(), AuditEntry{
		Event:    AuditEventAgentRequest,
		Actor:    "user:test",
		Severity: "info",
		Data:     map[string]interface{}{"message": "hello"},
	})
	if err != nil {
		t.Fatalf("failed to write audit entry: %v", err)
	}

	// Verify file exists and has content.
	logPath := filepath.Join(dir, "audit", "audit.jsonl")
	info, err := os.Stat(logPath)
	if err != nil {
		t.Fatalf("audit log file not found: %v", err)
	}
	if info.Size() == 0 {
		t.Error("audit log file is empty")
	}
}

func TestAuditLog_ChainIntegrity(t *testing.T) {
	al, _ := newTestAuditLog(t)

	// Write multiple entries.
	events := []AuditEventType{
		AuditEventAgentRequest,
		AuditEventShieldAllow,
		AuditEventAgentResponse,
		AuditEventMemoryWrite,
	}
	for _, event := range events {
		err := al.Log(context.Background(), AuditEntry{
			Event:    event,
			Actor:    "system",
			Severity: "info",
		})
		if err != nil {
			t.Fatalf("failed to write entry for event %s: %v", event, err)
		}
	}

	// Verify chain integrity.
	valid, violations, err := al.Verify(context.Background())
	if err != nil {
		t.Fatalf("verification failed: %v", err)
	}
	if len(violations) > 0 {
		t.Errorf("chain integrity violations: %v", violations)
	}
	// +1 for the system start event written in constructor.
	expected := len(events) + 1
	if valid != expected {
		t.Errorf("expected %d valid entries, got %d", expected, valid)
	}
}

func TestAuditLog_SequenceMonotonic(t *testing.T) {
	al, _ := newTestAuditLog(t)

	for i := 0; i < 5; i++ {
		_ = al.Log(context.Background(), AuditEntry{
			Event:    AuditEventAgentRequest,
			Actor:    "test",
			Severity: "info",
		})
	}

	// Sequence should be monotonically increasing.
	al.mu.Lock()
	seq := al.seq
	al.mu.Unlock()

	// 1 (system start) + 5 entries = 6
	if seq != 6 {
		t.Errorf("expected sequence 6, got %d", seq)
	}
}

func TestAuditLog_SecurityEvents(t *testing.T) {
	al, _ := newTestAuditLog(t)

	securityEvents := []AuditEventType{
		AuditEventShieldBlock,
		AuditEventInputViolation,
		AuditEventAuthFailure,
		AuditEventImmunityShare,
	}

	for _, event := range securityEvents {
		err := al.Log(context.Background(), AuditEntry{
			Event:    event,
			Actor:    "shield",
			Severity: "high",
			Data: map[string]interface{}{
				"threat": "prompt_injection",
				"source": "192.168.1.1",
			},
		})
		if err != nil {
			t.Errorf("failed to log security event %s: %v", event, err)
		}
	}
}
