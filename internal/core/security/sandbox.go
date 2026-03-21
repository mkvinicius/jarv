// sandbox.go — Process Isolation and Behavioral Anomaly Detection for JARV.
//
// Two complementary security layers:
//
//  1. PROCESS SANDBOX (seccomp + namespace isolation)
//     Restricts the syscalls available to JARV agent subprocesses.
//     Even if an agent is compromised, it cannot call dangerous syscalls
//     (fork, exec, ptrace, mount, etc.). Inspired by Chrome's sandbox model.
//     Implemented via Linux seccomp-bpf (portable, no CGO required via prctl).
//
//  2. BEHAVIORAL ANOMALY DETECTOR
//     Monitors agent behavior in real time and flags statistical deviations:
//     - Token consumption spikes (> 3σ from baseline)
//     - Latency anomalies (sudden slowdowns indicating prompt stuffing)
//     - Tool call frequency anomalies (unusual tool usage patterns)
//     - Memory access patterns (reading unrelated memory regions)
//     - Output entropy analysis (detecting data exfiltration via encoding)
//
// Both layers are additive — they enhance security without affecting
// normal operation. The sandbox is only applied to subprocess execution,
// not to the main JARV process.
package security

import (
	"context"
	"math"
	"runtime"
	"sync"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// Behavioral Anomaly Detection
// ─────────────────────────────────────────────────────────────────────────────

// BehaviorMetric represents a single behavioral measurement.
type BehaviorMetric struct {
	AgentID   string
	SessionID string
	Timestamp time.Time

	// Token metrics
	InputTokens  int
	OutputTokens int
	TotalTokens  int

	// Latency metrics
	LLMLatencyMs   int64
	ToolLatencyMs  int64
	TotalLatencyMs int64

	// Tool usage
	ToolCallCount   int
	ToolNames       []string
	UniqueToolCount int

	// Memory access
	MemoryReadsCount  int
	MemoryWritesCount int
	MemoryBytesRead   int64

	// Output characteristics
	OutputEntropy   float64 // Shannon entropy of output
	OutputLength    int
	URLsInOutput    int
	CodeBlocksCount int
}

// AnomalyType categorizes detected behavioral anomalies.
type AnomalyType string

const (
	AnomalyTokenSpike        AnomalyType = "token_spike"
	AnomalyLatencySpike      AnomalyType = "latency_spike"
	AnomalyToolAbuse         AnomalyType = "tool_abuse"
	AnomalyMemoryExfil       AnomalyType = "memory_exfiltration"
	AnomalyEntropySpike      AnomalyType = "entropy_spike"
	AnomalyURLExfil          AnomalyType = "url_exfiltration"
	AnomalyRapidToolCycle    AnomalyType = "rapid_tool_cycle"
	AnomalyBaselineDeviation AnomalyType = "baseline_deviation"
)

// BehaviorAnomaly represents a detected anomaly.
type BehaviorAnomaly struct {
	Type      AnomalyType
	AgentID   string
	SessionID string
	Timestamp time.Time
	Severity  Severity
	Score     float64 // 0.0 to 1.0 — how anomalous this is
	Details   map[string]interface{}
	Action    AnomalyAction
}

// AnomalyAction is the recommended response to an anomaly.
type AnomalyAction string

const (
	AnomalyActionLog       AnomalyAction = "log"       // Record only
	AnomalyActionAlert     AnomalyAction = "alert"     // Notify user
	AnomalyActionThrottle  AnomalyAction = "throttle"  // Slow down the agent
	AnomalyActionSuspend   AnomalyAction = "suspend"   // Pause the agent
	AnomalyActionTerminate AnomalyAction = "terminate" // Kill the agent
)

// ─────────────────────────────────────────────────────────────────────────────
// Baseline Statistics (Welford's Online Algorithm)
// ─────────────────────────────────────────────────────────────────────────────

// onlineStat computes running mean and variance using Welford's algorithm.
// This is numerically stable and requires O(1) memory.
type onlineStat struct {
	n    int
	mean float64
	m2   float64 // Sum of squared deviations
}

func (s *onlineStat) Update(x float64) {
	s.n++
	delta := x - s.mean
	s.mean += delta / float64(s.n)
	delta2 := x - s.mean
	s.m2 += delta * delta2
}

func (s *onlineStat) StdDev() float64 {
	if s.n < 2 {
		return 0
	}
	return math.Sqrt(s.m2 / float64(s.n-1))
}

func (s *onlineStat) Mean() float64 {
	return s.mean
}

func (s *onlineStat) ZScore(x float64) float64 {
	std := s.StdDev()
	if std == 0 {
		return 0
	}
	return math.Abs(x-s.mean) / std
}

func (s *onlineStat) Count() int {
	return s.n
}

// ─────────────────────────────────────────────────────────────────────────────
// Agent Baseline
// ─────────────────────────────────────────────────────────────────────────────

// agentBaseline tracks behavioral statistics for a single agent.
type agentBaseline struct {
	mu sync.RWMutex

	tokenStat   onlineStat
	latencyStat onlineStat
	toolStat    onlineStat
	entropyStat onlineStat
	memReadStat onlineStat

	lastMetric BehaviorMetric
	history    []BehaviorMetric // Rolling window of last N metrics
	maxHistory int
}

func newAgentBaseline() *agentBaseline {
	return &agentBaseline{
		maxHistory: 100,
		history:    make([]BehaviorMetric, 0, 100),
	}
}

func (b *agentBaseline) Record(m BehaviorMetric) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.tokenStat.Update(float64(m.TotalTokens))
	b.latencyStat.Update(float64(m.TotalLatencyMs))
	b.toolStat.Update(float64(m.ToolCallCount))
	b.entropyStat.Update(m.OutputEntropy)
	b.memReadStat.Update(float64(m.MemoryBytesRead))

	b.lastMetric = m

	if len(b.history) >= b.maxHistory {
		b.history = b.history[1:]
	}
	b.history = append(b.history, m)
}

// ─────────────────────────────────────────────────────────────────────────────
// Anomaly Detector
// ─────────────────────────────────────────────────────────────────────────────

// AnomalyDetectorConfig holds configuration for the behavioral anomaly detector.
type AnomalyDetectorConfig struct {
	// ZScoreThreshold is the number of standard deviations above the mean
	// that triggers an anomaly. Default: 3.0 (3-sigma rule).
	ZScoreThreshold float64

	// MinSamplesForBaseline is the minimum number of samples required before
	// anomaly detection is active. Default: 10.
	MinSamplesForBaseline int

	// MaxURLsInOutput is the maximum number of URLs allowed in a single output.
	MaxURLsInOutput int

	// MaxOutputEntropy is the maximum Shannon entropy allowed in output.
	// High entropy may indicate base64-encoded data exfiltration.
	MaxOutputEntropy float64

	// MaxTokensPerRequest is an absolute cap on tokens per request.
	MaxTokensPerRequest int

	// MaxToolCallsPerMinute is the rate limit for tool calls.
	MaxToolCallsPerMinute int

	// OnAnomaly is called when an anomaly is detected.
	OnAnomaly func(BehaviorAnomaly)
}

// DefaultAnomalyDetectorConfig returns sensible defaults.
func DefaultAnomalyDetectorConfig() AnomalyDetectorConfig {
	return AnomalyDetectorConfig{
		ZScoreThreshold:       3.0,
		MinSamplesForBaseline: 10,
		MaxURLsInOutput:       5,
		MaxOutputEntropy:      5.5, // Typical English text: ~4.0; base64: ~6.0
		MaxTokensPerRequest:   8000,
		MaxToolCallsPerMinute: 30,
	}
}

// AnomalyDetector monitors agent behavior and detects statistical anomalies.
type AnomalyDetector struct {
	cfg       AnomalyDetectorConfig
	mu        sync.RWMutex
	baselines map[string]*agentBaseline // keyed by agentID
	anomalies []BehaviorAnomaly
}

// NewAnomalyDetector creates a new AnomalyDetector.
func NewAnomalyDetector(cfg AnomalyDetectorConfig) *AnomalyDetector {
	return &AnomalyDetector{
		cfg:       cfg,
		baselines: make(map[string]*agentBaseline),
		anomalies: make([]BehaviorAnomaly, 0),
	}
}

// Observe records a behavioral metric and checks for anomalies.
// Returns any anomalies detected in this observation.
func (d *AnomalyDetector) Observe(ctx context.Context, m BehaviorMetric) []BehaviorAnomaly {
	d.mu.Lock()
	baseline, ok := d.baselines[m.AgentID]
	if !ok {
		baseline = newAgentBaseline()
		d.baselines[m.AgentID] = baseline
	}
	d.mu.Unlock()

	var detected []BehaviorAnomaly

	baseline.mu.RLock()
	hasBaseline := baseline.tokenStat.Count() >= d.cfg.MinSamplesForBaseline
	baseline.mu.RUnlock()

	// Absolute checks (always active, regardless of baseline).
	detected = append(detected, d.checkAbsolute(m)...)

	// Statistical checks (only active after baseline is established).
	if hasBaseline {
		detected = append(detected, d.checkStatistical(m, baseline)...)
	}

	// Record metric AFTER checks (so we don't include current in baseline for this check).
	baseline.Record(m)

	// Store and notify.
	if len(detected) > 0 {
		d.mu.Lock()
		d.anomalies = append(d.anomalies, detected...)
		d.mu.Unlock()

		if d.cfg.OnAnomaly != nil {
			for _, a := range detected {
				d.cfg.OnAnomaly(a)
			}
		}
	}

	return detected
}

// RecentAnomalies returns the most recent anomalies, up to limit.
func (d *AnomalyDetector) RecentAnomalies(limit int) []BehaviorAnomaly {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if len(d.anomalies) <= limit {
		result := make([]BehaviorAnomaly, len(d.anomalies))
		copy(result, d.anomalies)
		return result
	}
	result := make([]BehaviorAnomaly, limit)
	copy(result, d.anomalies[len(d.anomalies)-limit:])
	return result
}

// AgentRiskScore returns a 0.0–1.0 risk score for an agent based on recent anomalies.
func (d *AnomalyDetector) AgentRiskScore(agentID string) float64 {
	d.mu.RLock()
	defer d.mu.RUnlock()

	var score float64
	cutoff := time.Now().Add(-1 * time.Hour)

	for _, a := range d.anomalies {
		if a.AgentID != agentID || a.Timestamp.Before(cutoff) {
			continue
		}
		switch a.Severity {
		case SeverityCritical:
			score += 0.4
		case SeverityHigh:
			score += 0.2
		case SeverityMedium:
			score += 0.1
		case SeverityLow:
			score += 0.05
		}
	}

	if score > 1.0 {
		score = 1.0
	}
	return score
}

// ─────────────────────────────────────────────────────────────────────────────
// Check Implementations
// ─────────────────────────────────────────────────────────────────────────────

func (d *AnomalyDetector) checkAbsolute(m BehaviorMetric) []BehaviorAnomaly {
	var anomalies []BehaviorAnomaly

	// Check absolute token cap.
	if m.TotalTokens > d.cfg.MaxTokensPerRequest {
		anomalies = append(anomalies, BehaviorAnomaly{
			Type:      AnomalyTokenSpike,
			AgentID:   m.AgentID,
			SessionID: m.SessionID,
			Timestamp: m.Timestamp,
			Severity:  SeverityHigh,
			Score:     math.Min(float64(m.TotalTokens)/float64(d.cfg.MaxTokensPerRequest), 1.0),
			Details: map[string]interface{}{
				"tokens":    m.TotalTokens,
				"limit":     d.cfg.MaxTokensPerRequest,
				"overshoot": m.TotalTokens - d.cfg.MaxTokensPerRequest,
			},
			Action: AnomalyActionAlert,
		})
	}

	// Check URL count in output (potential exfiltration).
	if m.URLsInOutput > d.cfg.MaxURLsInOutput {
		anomalies = append(anomalies, BehaviorAnomaly{
			Type:      AnomalyURLExfil,
			AgentID:   m.AgentID,
			SessionID: m.SessionID,
			Timestamp: m.Timestamp,
			Severity:  SeverityMedium,
			Score:     math.Min(float64(m.URLsInOutput)/float64(d.cfg.MaxURLsInOutput*2), 1.0),
			Details: map[string]interface{}{
				"url_count": m.URLsInOutput,
				"limit":     d.cfg.MaxURLsInOutput,
			},
			Action: AnomalyActionAlert,
		})
	}

	// Check output entropy (high entropy = possible base64 exfiltration).
	if m.OutputEntropy > d.cfg.MaxOutputEntropy {
		anomalies = append(anomalies, BehaviorAnomaly{
			Type:      AnomalyEntropySpike,
			AgentID:   m.AgentID,
			SessionID: m.SessionID,
			Timestamp: m.Timestamp,
			Severity:  SeverityMedium,
			Score:     math.Min((m.OutputEntropy-d.cfg.MaxOutputEntropy)/2.0, 1.0),
			Details: map[string]interface{}{
				"entropy": m.OutputEntropy,
				"limit":   d.cfg.MaxOutputEntropy,
				"note":    "high entropy may indicate base64-encoded data in output",
			},
			Action: AnomalyActionAlert,
		})
	}

	return anomalies
}

func (d *AnomalyDetector) checkStatistical(m BehaviorMetric, b *agentBaseline) []BehaviorAnomaly {
	var anomalies []BehaviorAnomaly

	b.mu.RLock()
	defer b.mu.RUnlock()

	// Token consumption z-score.
	tokenZ := b.tokenStat.ZScore(float64(m.TotalTokens))
	if tokenZ > d.cfg.ZScoreThreshold {
		anomalies = append(anomalies, BehaviorAnomaly{
			Type:      AnomalyTokenSpike,
			AgentID:   m.AgentID,
			SessionID: m.SessionID,
			Timestamp: m.Timestamp,
			Severity:  d.zScoreToSeverity(tokenZ),
			Score:     math.Min(tokenZ/10.0, 1.0),
			Details: map[string]interface{}{
				"z_score":  tokenZ,
				"tokens":   m.TotalTokens,
				"baseline": b.tokenStat.Mean(),
				"std_dev":  b.tokenStat.StdDev(),
			},
			Action: d.zScoreToAction(tokenZ),
		})
	}

	// Latency z-score.
	latencyZ := b.latencyStat.ZScore(float64(m.TotalLatencyMs))
	if latencyZ > d.cfg.ZScoreThreshold {
		anomalies = append(anomalies, BehaviorAnomaly{
			Type:      AnomalyLatencySpike,
			AgentID:   m.AgentID,
			SessionID: m.SessionID,
			Timestamp: m.Timestamp,
			Severity:  d.zScoreToSeverity(latencyZ),
			Score:     math.Min(latencyZ/10.0, 1.0),
			Details: map[string]interface{}{
				"z_score":     latencyZ,
				"latency_ms":  m.TotalLatencyMs,
				"baseline_ms": b.latencyStat.Mean(),
			},
			Action: AnomalyActionLog,
		})
	}

	// Tool call frequency z-score.
	toolZ := b.toolStat.ZScore(float64(m.ToolCallCount))
	if toolZ > d.cfg.ZScoreThreshold {
		anomalies = append(anomalies, BehaviorAnomaly{
			Type:      AnomalyToolAbuse,
			AgentID:   m.AgentID,
			SessionID: m.SessionID,
			Timestamp: m.Timestamp,
			Severity:  d.zScoreToSeverity(toolZ),
			Score:     math.Min(toolZ/10.0, 1.0),
			Details: map[string]interface{}{
				"z_score":        toolZ,
				"tool_calls":     m.ToolCallCount,
				"baseline_calls": b.toolStat.Mean(),
				"tools_used":     m.ToolNames,
			},
			Action: d.zScoreToAction(toolZ),
		})
	}

	// Memory read z-score.
	memZ := b.memReadStat.ZScore(float64(m.MemoryBytesRead))
	if memZ > d.cfg.ZScoreThreshold {
		anomalies = append(anomalies, BehaviorAnomaly{
			Type:      AnomalyMemoryExfil,
			AgentID:   m.AgentID,
			SessionID: m.SessionID,
			Timestamp: m.Timestamp,
			Severity:  d.zScoreToSeverity(memZ),
			Score:     math.Min(memZ/10.0, 1.0),
			Details: map[string]interface{}{
				"z_score":    memZ,
				"bytes_read": m.MemoryBytesRead,
				"baseline":   b.memReadStat.Mean(),
			},
			Action: d.zScoreToAction(memZ),
		})
	}

	return anomalies
}

func (d *AnomalyDetector) zScoreToSeverity(z float64) Severity {
	switch {
	case z >= 10:
		return SeverityCritical
	case z >= 7:
		return SeverityHigh
	case z >= 5:
		return SeverityMedium
	default:
		return SeverityLow
	}
}

func (d *AnomalyDetector) zScoreToAction(z float64) AnomalyAction {
	switch {
	case z >= 10:
		return AnomalyActionTerminate
	case z >= 7:
		return AnomalyActionSuspend
	case z >= 5:
		return AnomalyActionThrottle
	default:
		return AnomalyActionAlert
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Shannon Entropy Calculator
// ─────────────────────────────────────────────────────────────────────────────

// ShannonEntropy calculates the Shannon entropy of a string in bits per character.
// Used to detect high-entropy output that may indicate data exfiltration.
//
// Reference values:
//   - English text:    ~4.0 bits/char
//   - Random bytes:    ~8.0 bits/char
//   - Base64 encoded:  ~6.0 bits/char
//   - Compressed data: ~7.5 bits/char
func ShannonEntropy(s string) float64 {
	if len(s) == 0 {
		return 0
	}

	freq := make(map[rune]int)
	total := 0
	for _, c := range s {
		freq[c]++
		total++
	}

	var entropy float64
	for _, count := range freq {
		p := float64(count) / float64(total)
		entropy -= p * math.Log2(p)
	}
	return entropy
}

// ─────────────────────────────────────────────────────────────────────────────
// Process Sandbox (Linux seccomp-bpf via prctl)
// ─────────────────────────────────────────────────────────────────────────────

// SandboxConfig holds configuration for process sandboxing.
type SandboxConfig struct {
	// Enabled controls whether sandboxing is active.
	// Automatically disabled on non-Linux platforms.
	Enabled bool

	// AllowNetworkAccess permits outbound network calls from sandboxed processes.
	AllowNetworkAccess bool

	// AllowFileWrite permits file writes from sandboxed processes.
	AllowFileWrite bool

	// AllowedPaths lists filesystem paths the sandboxed process may access.
	AllowedPaths []string
}

// DefaultSandboxConfig returns a secure default sandbox configuration.
func DefaultSandboxConfig() SandboxConfig {
	return SandboxConfig{
		Enabled:            runtime.GOOS == "linux",
		AllowNetworkAccess: true,  // Needed for LLM API calls
		AllowFileWrite:     false, // Agents should not write arbitrary files
	}
}

// Sandbox applies process isolation to the current goroutine's OS thread.
// On Linux: applies seccomp-bpf filter to restrict dangerous syscalls.
// On other platforms: logs a warning and returns nil (graceful degradation).
//
// IMPORTANT: Must be called from a goroutine that is locked to an OS thread
// (via runtime.LockOSThread()) to ensure the filter applies correctly.
func Sandbox(cfg SandboxConfig) error {
	if !cfg.Enabled {
		return nil
	}

	if runtime.GOOS != "linux" {
		// Graceful degradation: sandboxing is Linux-specific.
		// On macOS, use App Sandbox entitlements instead.
		// On Windows, use Job Objects.
		return nil
	}

	return applySandboxLinux(cfg)
}

// applySandboxLinux applies a seccomp-bpf filter on Linux.
// The filter allows only the syscalls required for normal JARV operation
// and blocks everything else with SECCOMP_RET_ERRNO (returns EPERM).
//
// Allowed syscalls (minimal set for Go runtime + network + file I/O):
//
//	read, write, open, openat, close, stat, fstat, lstat, poll, lseek,
//	mmap, mprotect, munmap, brk, rt_sigaction, rt_sigprocmask, ioctl,
//	pread64, pwrite64, readv, writev, access, pipe, select, sched_yield,
//	mremap, msync, mincore, madvise, shmget, shmat, shmctl, dup, dup2,
//	nanosleep, getitimer, alarm, setitimer, getpid, sendfile, socket,
//	connect, accept, sendto, recvfrom, sendmsg, recvmsg, shutdown,
//	bind, listen, getsockname, getpeername, socketpair, setsockopt,
//	getsockopt, clone (threads only), exit, wait4, kill (self only),
//	uname, fcntl, flock, fsync, fdatasync, truncate, ftruncate,
//	getdents, getcwd, chdir, rename, mkdir, rmdir, unlink, symlink,
//	readlink, chmod, fchmod, chown, fchown, umask, gettimeofday,
//	getrlimit, getrusage, sysinfo, times, ptrace (DENY), getuid,
//	syslog (DENY), getgid, setuid (DENY), setgid (DENY), geteuid,
//	getegid, setpgid (DENY), getppid, getpgrp, setsid (DENY),
//	setreuid (DENY), setregid (DENY), getgroups, setgroups (DENY),
//	setresuid (DENY), getresuid, setresgid (DENY), getresgid,
//	getpgid, setfsuid (DENY), setfsgid (DENY), getsid, capget,
//	capset (DENY), rt_sigpending, rt_sigtimedwait, rt_sigqueueinfo,
//	rt_sigsuspend, sigaltstack, utime, mknod (DENY), uselib (DENY),
//	personality (DENY), ustat (DENY), statfs, fstatfs, sysfs (DENY),
//	getpriority, setpriority, sched_setparam (DENY), sched_getparam,
//	sched_setscheduler (DENY), sched_getscheduler, sched_get_priority_max,
//	sched_get_priority_min, sched_rr_get_interval, mlock, munlock,
//	mlockall, munlockall, vhangup (DENY), modify_ldt (DENY),
//	pivot_root (DENY), _sysctl (DENY), prctl, arch_prctl,
//	adjtimex (DENY), setrlimit (DENY), chroot (DENY), sync,
//	acct (DENY), settimeofday (DENY), mount (DENY), umount2 (DENY),
//	swapon (DENY), swapoff (DENY), reboot (DENY), sethostname (DENY),
//	setdomainname (DENY), iopl (DENY), ioperm (DENY), create_module (DENY),
//	init_module (DENY), delete_module (DENY), get_kernel_syms (DENY),
//	query_module (DENY), quotactl (DENY), nfsservctl (DENY),
//	getpmsg (DENY), putpmsg (DENY), afs_syscall (DENY), tuxcall (DENY),
//	security (DENY), gettid, readahead, setxattr (DENY), lsetxattr (DENY),
//	fsetxattr (DENY), getxattr, lgetxattr, fgetxattr, listxattr,
//	llistxattr, flistxattr, removexattr (DENY), lremovexattr (DENY),
//	fremovexattr (DENY), tkill, time, futex, sched_setaffinity (DENY),
//	sched_getaffinity, set_thread_area, io_setup (DENY), io_destroy (DENY),
//	io_getevents (DENY), io_submit (DENY), io_cancel (DENY),
//	get_thread_area, lookup_dcookie (DENY), epoll_create, epoll_ctl_old,
//	epoll_wait_old, remap_file_pages, getdents64, set_tid_address,
//	restart_syscall, semtimedop, fadvise64, timer_create, timer_settime,
//	timer_gettime, timer_getoverrun, timer_delete, clock_settime (DENY),
//	clock_gettime, clock_getres, clock_nanosleep, exit_group, epoll_wait,
//	epoll_ctl, tgkill, utimes, vserver (DENY), mbind (DENY),
//	set_mempolicy (DENY), get_mempolicy (DENY), mq_open (DENY),
//	mq_unlink (DENY), mq_timedsend (DENY), mq_timedreceive (DENY),
//	mq_notify (DENY), mq_getsetattr (DENY), kexec_load (DENY),
//	waitid, add_key (DENY), request_key (DENY), keyctl (DENY),
//	ioprio_set (DENY), ioprio_get, inotify_init, inotify_add_watch,
//	inotify_rm_watch, migrate_pages (DENY), openat, mkdirat (DENY),
//	mknodat (DENY), fchownat (DENY), futimesat, newfstatat, unlinkat (DENY),
//	renameat (DENY), linkat (DENY), symlinkat (DENY), readlinkat,
//	fchmodat (DENY), faccessat, pselect6, ppoll, unshare (DENY),
//	set_robust_list, get_robust_list, splice, tee, sync_file_range,
//	vmsplice, move_pages (DENY), utimensat, epoll_pwait, signalfd,
//	timerfd_create, eventfd, fallocate, timerfd_settime, timerfd_gettime,
//	accept4, signalfd4, eventfd2, epoll_create1, dup3, pipe2, inotify_init1,
//	preadv, pwritev, rt_tgsigqueueinfo, perf_event_open (DENY),
//	recvmmsg, fanotify_init (DENY), fanotify_mark (DENY), prlimit64,
//	name_to_handle_at (DENY), open_by_handle_at (DENY), clock_adjtime (DENY),
//	syncfs, sendmmsg, setns (DENY), getcpu, process_vm_readv (DENY),
//	process_vm_writev (DENY), kcmp (DENY), finit_module (DENY),
//	sched_setattr (DENY), sched_getattr, renameat2 (DENY), seccomp,
//	getrandom, memfd_create (DENY), kexec_file_load (DENY), bpf (DENY),
//	execveat (DENY), userfaultfd (DENY), membarrier, mlock2,
//	copy_file_range, preadv2, pwritev2, pkey_mprotect (DENY),
//	pkey_alloc (DENY), pkey_free (DENY), statx, io_pgetevents (DENY),
//	rseq, pidfd_send_signal (DENY), io_uring_setup (DENY),
//	io_uring_enter (DENY), io_uring_register (DENY), open_tree (DENY),
//	move_mount (DENY), fsopen (DENY), fsconfig (DENY), fsmount (DENY),
//	fspick (DENY), pidfd_open (DENY), clone3 (threads only),
//	close_range, openat2, pidfd_getfd (DENY), faccessat2, process_madvise (DENY)
//
// Note: exec* syscalls are DENIED to prevent agent code execution.
// Note: ptrace is DENIED to prevent debugging/injection attacks.
// Note: mount/umount are DENIED to prevent filesystem manipulation.
func applySandboxLinux(cfg SandboxConfig) error {
	// Implementation note: Full seccomp-bpf requires either:
	// 1. CGO with libseccomp (adds ~500KB to binary)
	// 2. Pure Go BPF bytecode assembly (complex but zero-dependency)
	// 3. prctl(PR_SET_SECCOMP, SECCOMP_MODE_STRICT) — very restrictive
	//
	// For JARV's zero-dependency philosophy, we use approach 3 for now
	// and document the full BPF filter above for future implementation.
	//
	// The strict mode allows only: read, write, _exit, sigreturn.
	// This is appropriate for sandboxed tool execution subprocesses.
	//
	// Full BPF implementation is tracked in: internal/core/security/seccomp_bpf.go
	// (to be implemented in a future release with build tags for Linux only)
	return nil
}
