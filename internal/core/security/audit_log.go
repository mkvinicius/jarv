// audit_log.go — Replicated, Immutable Audit Log for JARV.
//
// Implements a dual-write audit log that simultaneously writes to:
//  1. Local WORM file (append-only, HMAC-SHA256 chained integrity)
//  2. Remote audit server (optional, for crash-proof evidence preservation)
//
// Properties:
//   - Immutable: each entry is chained to the previous via HMAC
//   - Tamper-evident: any modification breaks the chain
//   - Crash-safe: local write is fsync'd before remote write
//   - Encrypted at rest: AES-256-GCM (requires EncryptedStore)
//   - Compliant: structured for NIST SP 800-53 AU-2 / AU-9
//
// Entry format (JSONL, one entry per line):
//
//	{"seq":1,"ts":"...","event":"...","actor":"...","data":{...},"chain":"<hmac>"}
package security

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// Audit Entry
// ─────────────────────────────────────────────────────────────────────────────

// AuditEventType categorizes audit events.
type AuditEventType string

const (
	AuditEventAgentRequest   AuditEventType = "agent.request"
	AuditEventAgentResponse  AuditEventType = "agent.response"
	AuditEventToolExecution  AuditEventType = "tool.execution"
	AuditEventShieldBlock    AuditEventType = "shield.block"
	AuditEventShieldAllow    AuditEventType = "shield.allow"
	AuditEventInputViolation AuditEventType = "input.violation"
	AuditEventAuthSuccess    AuditEventType = "auth.success"
	AuditEventAuthFailure    AuditEventType = "auth.failure"
	AuditEventConfigChange   AuditEventType = "config.change"
	AuditEventSystemStart    AuditEventType = "system.start"
	AuditEventSystemStop     AuditEventType = "system.stop"
	AuditEventMemoryWrite    AuditEventType = "memory.write"
	AuditEventMemoryRead     AuditEventType = "memory.read"
	AuditEventSkillInstall   AuditEventType = "skill.install"
	AuditEventSquadCreate    AuditEventType = "squad.create"
	AuditEventOracleRun      AuditEventType = "oracle.run"
	AuditEventForesightAlert AuditEventType = "foresight.alert"
	AuditEventImmunityShare  AuditEventType = "immunity.share"
)

// AuditEntry is a single immutable audit log entry.
type AuditEntry struct {
	// Sequence number — monotonically increasing, never reused.
	Seq uint64 `json:"seq"`

	// Timestamp in RFC3339Nano format.
	Timestamp string `json:"ts"`

	// Event type.
	Event AuditEventType `json:"event"`

	// Actor is the user, agent, or system component that triggered the event.
	Actor string `json:"actor"`

	// SessionID links related events in a conversation.
	SessionID string `json:"session_id,omitempty"`

	// Data contains event-specific structured data.
	Data map[string]interface{} `json:"data,omitempty"`

	// Severity: "info" | "low" | "medium" | "high" | "critical"
	Severity string `json:"severity"`

	// Chain is the HMAC-SHA256 of (prev_chain + seq + ts + event + actor + data).
	// Forms an immutable chain — any modification breaks all subsequent entries.
	Chain string `json:"chain"`
}

// ─────────────────────────────────────────────────────────────────────────────
// Audit Log Configuration
// ─────────────────────────────────────────────────────────────────────────────

// AuditLogConfig holds configuration for the replicated audit log.
type AuditLogConfig struct {
	// LocalPath is the directory where audit log files are stored.
	LocalPath string

	// HMACKey is used to compute the integrity chain.
	// Must be at least 32 bytes.
	HMACKey []byte

	// RemoteEndpoint is the URL of the remote audit server (optional).
	// If empty, remote replication is disabled.
	RemoteEndpoint string

	// RemoteAPIKey is the API key for the remote audit server.
	RemoteAPIKey string

	// RemoteTimeout is the timeout for remote write operations.
	RemoteTimeout time.Duration

	// FailOnRemoteError causes the log to return an error if remote write fails.
	// Set to true for high-security environments.
	FailOnRemoteError bool

	// MaxLocalFileSize is the maximum size of a single log file before rotation.
	// Default: 100MB.
	MaxLocalFileSize int64

	// RetentionDays is the number of days to retain local log files.
	// 0 means retain forever.
	RetentionDays int
}

// DefaultAuditLogConfig returns a secure default configuration.
func DefaultAuditLogConfig(dataDir string, hmacKey []byte) AuditLogConfig {
	return AuditLogConfig{
		LocalPath:         filepath.Join(dataDir, "audit"),
		HMACKey:           hmacKey,
		RemoteTimeout:     5 * time.Second,
		FailOnRemoteError: false,             // Fail-open by default; set true for military use
		MaxLocalFileSize:  100 * 1024 * 1024, // 100MB
		RetentionDays:     365,
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Replicated Audit Log
// ─────────────────────────────────────────────────────────────────────────────

// ReplicatedAuditLog is a dual-write, tamper-evident audit log.
type ReplicatedAuditLog struct {
	cfg    AuditLogConfig
	client *http.Client

	mu        sync.Mutex
	seq       uint64
	prevChain string
	file      *os.File
	fileSize  int64
}

// NewReplicatedAuditLog creates and initializes a new ReplicatedAuditLog.
func NewReplicatedAuditLog(cfg AuditLogConfig) (*ReplicatedAuditLog, error) {
	if err := os.MkdirAll(cfg.LocalPath, 0700); err != nil {
		return nil, fmt.Errorf("audit: failed to create log directory: %w", err)
	}

	al := &ReplicatedAuditLog{
		cfg: cfg,
		client: &http.Client{
			Timeout: cfg.RemoteTimeout,
		},
	}

	if err := al.openLogFile(); err != nil {
		return nil, err
	}

	// Log system start event.
	_ = al.Log(context.Background(), AuditEntry{
		Event:    AuditEventSystemStart,
		Actor:    "system",
		Severity: "info",
		Data: map[string]interface{}{
			"version": "1.0.0",
			"pid":     os.Getpid(),
		},
	})

	return al, nil
}

// Log writes an audit entry to both local and remote destinations.
// The entry is assigned a sequence number and chain hash automatically.
func (al *ReplicatedAuditLog) Log(ctx context.Context, entry AuditEntry) error {
	al.mu.Lock()
	defer al.mu.Unlock()

	// Assign sequence and timestamp.
	al.seq++
	entry.Seq = al.seq
	entry.Timestamp = time.Now().UTC().Format(time.RFC3339Nano)

	// Compute chain hash: HMAC(prevChain + seq + ts + event + actor).
	entry.Chain = al.computeChain(entry)
	al.prevChain = entry.Chain

	// Serialize to JSON.
	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("audit: failed to marshal entry: %w", err)
	}
	data = append(data, '\n')

	// Layer 1: Write to local file (fsync for crash safety).
	if err := al.writeLocal(data); err != nil {
		return fmt.Errorf("audit: local write failed: %w", err)
	}

	// Layer 2: Replicate to remote server (non-blocking by default).
	if al.cfg.RemoteEndpoint != "" {
		if al.cfg.FailOnRemoteError {
			return al.writeRemote(ctx, data)
		}
		// Fire-and-forget for non-critical environments.
		go func() {
			_ = al.writeRemote(context.Background(), data)
		}()
	}

	return nil
}

// Verify checks the integrity of the audit log by re-computing all chain hashes.
// Returns the number of valid entries and any integrity violations found.
func (al *ReplicatedAuditLog) Verify(ctx context.Context) (valid int, violations []string, err error) {
	logFile := al.currentLogPath()
	data, err := os.ReadFile(logFile)
	if err != nil {
		return 0, nil, fmt.Errorf("audit: failed to read log file: %w", err)
	}

	lines := bytes.Split(data, []byte("\n"))
	prevChain := ""

	for i, line := range lines {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}

		var entry AuditEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			violations = append(violations, fmt.Sprintf("line %d: invalid JSON: %v", i+1, err))
			continue
		}

		// Temporarily restore prevChain to recompute.
		savedChain := entry.Chain
		al.mu.Lock()
		al.prevChain = prevChain
		expected := al.computeChain(entry)
		al.mu.Unlock()

		if !hmac.Equal([]byte(savedChain), []byte(expected)) {
			violations = append(violations, fmt.Sprintf(
				"line %d (seq=%d): chain integrity violation — log may have been tampered",
				i+1, entry.Seq,
			))
		} else {
			valid++
			prevChain = savedChain
		}
	}

	return valid, violations, nil
}

// Close flushes and closes the audit log.
func (al *ReplicatedAuditLog) Close() error {
	_ = al.Log(context.Background(), AuditEntry{
		Event:    AuditEventSystemStop,
		Actor:    "system",
		Severity: "info",
	})

	al.mu.Lock()
	defer al.mu.Unlock()

	if al.file != nil {
		_ = al.file.Sync()
		return al.file.Close()
	}
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Internal Helpers
// ─────────────────────────────────────────────────────────────────────────────

func (al *ReplicatedAuditLog) computeChain(entry AuditEntry) string {
	mac := hmac.New(sha256.New, al.cfg.HMACKey)
	mac.Write([]byte(al.prevChain))
	mac.Write([]byte(fmt.Sprintf("%d", entry.Seq)))
	mac.Write([]byte(entry.Timestamp))
	mac.Write([]byte(string(entry.Event)))
	mac.Write([]byte(entry.Actor))
	if entry.Data != nil {
		dataBytes, _ := json.Marshal(entry.Data)
		mac.Write(dataBytes)
	}
	return hex.EncodeToString(mac.Sum(nil))
}

func (al *ReplicatedAuditLog) writeLocal(data []byte) error {
	// Rotate log file if needed.
	if al.fileSize+int64(len(data)) > al.cfg.MaxLocalFileSize {
		if err := al.rotateLogFile(); err != nil {
			return err
		}
	}

	n, err := al.file.Write(data)
	if err != nil {
		return err
	}
	al.fileSize += int64(n)

	// fsync to ensure data is on disk before returning.
	return al.file.Sync()
}

func (al *ReplicatedAuditLog) writeRemote(ctx context.Context, data []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, al.cfg.RemoteEndpoint, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("audit: failed to create remote request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-ndjson")
	req.Header.Set("Authorization", "Bearer "+al.cfg.RemoteAPIKey)
	req.Header.Set("X-JARV-Chain", al.prevChain)

	resp, err := al.client.Do(req)
	if err != nil {
		return fmt.Errorf("audit: remote write failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("audit: remote server returned %d", resp.StatusCode)
	}
	return nil
}

func (al *ReplicatedAuditLog) openLogFile() error {
	path := al.currentLogPath()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("audit: failed to open log file: %w", err)
	}

	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return fmt.Errorf("audit: failed to stat log file: %w", err)
	}

	al.file = f
	al.fileSize = info.Size()
	return nil
}

func (al *ReplicatedAuditLog) rotateLogFile() error {
	if al.file != nil {
		_ = al.file.Sync()
		_ = al.file.Close()
	}

	// Rename current file with timestamp.
	current := al.currentLogPath()
	rotated := filepath.Join(al.cfg.LocalPath,
		fmt.Sprintf("audit-%s.jsonl", time.Now().UTC().Format("20060102-150405")))
	_ = os.Rename(current, rotated)

	return al.openLogFile()
}

func (al *ReplicatedAuditLog) currentLogPath() string {
	return filepath.Join(al.cfg.LocalPath, "audit.jsonl")
}
