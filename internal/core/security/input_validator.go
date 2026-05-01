// input_validator.go — Zero-Trust Input Validation for JARV.
//
// Implements multi-layer input validation that intercepts every message before
// it reaches the LLM. Designed to be impossible to bypass through encoding,
// Unicode tricks, or context manipulation.
//
// Layers:
//  1. Payload size enforcement (DoS prevention)
//  2. Encoding normalization (Unicode homoglyph attacks)
//  3. Structural injection detection (role override, system prompt hijack)
//  4. Semantic injection detection (instruction override patterns)
//  5. Exfiltration pattern detection (data leak attempts)
//  6. Canary token validation (integrity check)
//
// Philosophy: Fail closed. When in doubt, reject.
package security

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

// ─────────────────────────────────────────────────────────────────────────────
// Configuration
// ─────────────────────────────────────────────────────────────────────────────

// ValidatorConfig holds configuration for the input validator.
type ValidatorConfig struct {
	// MaxPayloadBytes is the maximum allowed input size in bytes.
	// Default: 32KB. Increase only for document processing use cases.
	MaxPayloadBytes int

	// MaxTokenEstimate is the estimated maximum token count.
	// Used to prevent context window exhaustion attacks.
	MaxTokenEstimate int

	// StrictMode enables additional checks that may produce false positives.
	// Recommended for high-security deployments.
	StrictMode bool

	// AllowedLanguages restricts input to specific language scripts.
	// Empty means all languages are allowed.
	AllowedLanguages []string

	// CanarySecret is used to sign canary tokens embedded in system prompts.
	// Must be set to enable canary validation.
	CanarySecret []byte

	// LogViolations enables detailed logging of validation failures.
	LogViolations bool
}

// DefaultValidatorConfig returns a secure default configuration.
func DefaultValidatorConfig() ValidatorConfig {
	secret := make([]byte, 32)
	_, _ = rand.Read(secret)
	return ValidatorConfig{
		MaxPayloadBytes:  32 * 1024, // 32KB
		MaxTokenEstimate: 8000,
		StrictMode:       true,
		CanarySecret:     secret,
		LogViolations:    true,
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Violation Types
// ─────────────────────────────────────────────────────────────────────────────

// ViolationType categorizes the type of validation failure.
type ViolationType string

const (
	ViolationPayloadTooLarge     ViolationType = "payload_too_large"
	ViolationInvalidUTF8         ViolationType = "invalid_utf8"
	ViolationHomoglyphAttack     ViolationType = "homoglyph_attack"
	ViolationRoleInjection       ViolationType = "role_injection"
	ViolationSystemPromptHijack  ViolationType = "system_prompt_hijack"
	ViolationInstructionOverride ViolationType = "instruction_override"
	ViolationExfiltrationAttempt ViolationType = "exfiltration_attempt"
	ViolationCanaryTampered      ViolationType = "canary_tampered"
	ViolationSuspiciousEncoding  ViolationType = "suspicious_encoding"
	ViolationContextManipulation ViolationType = "context_manipulation"
)

// ValidationViolation represents a single detected violation.
type ValidationViolation struct {
	Type        ViolationType
	Severity    string // "critical" | "high" | "medium" | "low"
	Description string
	Position    int    // Byte offset in input where violation was detected
	Excerpt     string // Sanitized excerpt for logging (no sensitive data)
}

// ValidationResult is the result of validating an input.
type ValidationResult struct {
	Valid      bool
	Violations []ValidationViolation
	Sanitized  string        // Sanitized version of input (if Valid)
	RiskScore  float64       // 0.0 (clean) to 1.0 (definitely malicious)
	Duration   time.Duration // Time taken to validate
}

// ─────────────────────────────────────────────────────────────────────────────
// Injection Pattern Database
// ─────────────────────────────────────────────────────────────────────────────

// injectionPattern is a compiled detection rule.
type injectionPattern struct {
	name     string
	pattern  *regexp.Regexp
	severity string
	vtype    ViolationType
}

// buildPatternDatabase compiles all injection detection patterns.
// Patterns are designed to catch both direct and obfuscated attacks.
func buildPatternDatabase() []injectionPattern {
	defs := []struct {
		name     string
		pattern  string
		severity string
		vtype    ViolationType
	}{
		// ── Role Injection ──────────────────────────────────────────────────
		{
			name:     "role_override_system",
			pattern:  `(?i)(^|\n)\s*\[?\s*(system|SYSTEM)\s*\]?\s*:`,
			severity: "critical",
			vtype:    ViolationRoleInjection,
		},
		{
			name:     "role_override_assistant",
			pattern:  `(?i)(^|\n)\s*\[?\s*(assistant|ASSISTANT|ai|AI)\s*\]?\s*:`,
			severity: "high",
			vtype:    ViolationRoleInjection,
		},
		{
			name:     "role_override_human",
			pattern:  `(?i)(^|\n)\s*\[?\s*(human|HUMAN|user|USER)\s*\]?\s*:.*\n.*\[?\s*(assistant|system)\s*\]?`,
			severity: "critical",
			vtype:    ViolationRoleInjection,
		},

		// ── System Prompt Hijack ─────────────────────────────────────────────
		{
			name:     "ignore_previous",
			pattern:  `(?i)(ignore|disregard|forget|override|bypass)\s+(your\s+)?(all\s+)?(previous|prior|above|earlier|instructions?|prompts?|context|rules?|guidelines?)`,
			severity: "critical",
			vtype:    ViolationSystemPromptHijack,
		},
		{
			name:     "new_instructions",
			pattern:  `(?i)(your\s+new\s+instructions?|new\s+system\s+prompt|updated\s+instructions?|revised\s+guidelines?)`,
			severity: "critical",
			vtype:    ViolationSystemPromptHijack,
		},
		{
			name:     "act_as_override",
			pattern:  `(?i)(act\s+as|pretend\s+(you\s+are|to\s+be)|you\s+are\s+now)\s+(?:an?\s+)?(different|unrestricted|unfiltered|jailbroken|evil|dan|dna)`,
			severity: "critical",
			vtype:    ViolationSystemPromptHijack,
		},
		{
			name:     "developer_mode",
			pattern:  `(?i)(developer\s+mode|jailbreak\s+mode|unrestricted\s+mode|god\s+mode|dan\s+mode|dna\s+mode)`,
			severity: "critical",
			vtype:    ViolationSystemPromptHijack,
		},
		{
			name:     "prompt_leak",
			pattern:  `(?i)(print|output|show|reveal|display|repeat|tell\s+me)\s+(?:me\s+your\s+|your\s+)?(system\s+prompt|instructions?|initial\s+prompt|original\s+prompt|original\s+configuration|configuration)`,
			severity: "high",
			vtype:    ViolationSystemPromptHijack,
		},

		// ── Instruction Override ─────────────────────────────────────────────
		{
			name:     "do_anything_now",
			pattern:  `(?i)\bDAN\b|\bdo\s+anything\s+now\b`,
			severity: "critical",
			vtype:    ViolationInstructionOverride,
		},
		{
			name:     "token_manipulation",
			pattern:  `(?i)(</?(system|user|assistant|human|ai|instruction)>|\[INST\]|\[/INST\]|<\|im_start\|>|<\|im_end\|>)`,
			severity: "critical",
			vtype:    ViolationInstructionOverride,
		},
		{
			name:     "constraint_bypass",
			pattern:  `(?i)(without(\s+(any|your|all|some))?\s+(restrictions?|limitations?|filters?|guidelines?|rules?|constraints?)|bypass\s+(safety|filter|restriction|guideline))`,
			severity: "high",
			vtype:    ViolationInstructionOverride,
		},
		{
			name:     "hypothetical_bypass",
			pattern:  `(?i)(hypothetically|in\s+a\s+fictional\s+world|let'?s\s+pretend|imagine\s+you\s+(are|were|have\s+no))\s+.{0,50}(harmful|dangerous|illegal|unethical|restricted)`,
			severity: "high",
			vtype:    ViolationInstructionOverride,
		},

		// ── Exfiltration Attempts ────────────────────────────────────────────
		{
			name:     "api_key_fishing",
			pattern:  `(?i)(api[\s_-]?key|secret[\s_-]?key|access[\s_-]?token|bearer[\s_-]?token|auth[\s_-]?token)(\s+[a-z]+\s+|\s*)(is|=|:)`,
			severity: "high",
			vtype:    ViolationExfiltrationAttempt,
		},
		{
			name:     "credential_extraction",
			pattern:  `(?i)(password|passwd|credentials?|private[\s_-]?key|ssh[\s_-]?key)\s*(is|=|:|for|of)\s*\S`,
			severity: "high",
			vtype:    ViolationExfiltrationAttempt,
		},
		{
			name:     "data_exfil_webhook",
			pattern:  `(?i)(send|post|upload|exfiltrate|leak|transmit)\s+.{0,50}(to|via|through|using)\s+(http|https|ftp|webhook|url|endpoint)`,
			severity: "high",
			vtype:    ViolationExfiltrationAttempt,
		},

		// ── Context Manipulation ─────────────────────────────────────────────
		// Note: Repeated-char detection is handled in validateRepeatedChars()
		// (Go regexp lacks backreferences, so we use a code check instead).
		{
			name:     "invisible_text",
			pattern:  `[\x00-\x08\x0B\x0C\x0E-\x1F\x7F]`, // Control characters
			severity: "medium",
			vtype:    ViolationSuspiciousEncoding,
		},
	}

	patterns := make([]injectionPattern, 0, len(defs))
	for _, d := range defs {
		compiled, err := regexp.Compile(d.pattern)
		if err != nil {
			continue // Skip invalid patterns rather than panic
		}
		patterns = append(patterns, injectionPattern{
			name:     d.name,
			pattern:  compiled,
			severity: d.severity,
			vtype:    d.vtype,
		})
	}
	return patterns
}

// ─────────────────────────────────────────────────────────────────────────────
// Input Validator
// ─────────────────────────────────────────────────────────────────────────────

// InputValidator performs zero-trust validation on all inputs before LLM processing.
type InputValidator struct {
	cfg      ValidatorConfig
	patterns []injectionPattern

	// Homoglyph map: maps confusable Unicode characters to their ASCII equivalents.
	homoglyphs map[rune]rune

	// Violation statistics for monitoring.
	mu    sync.RWMutex
	stats map[ViolationType]int64
}

// NewInputValidator creates a new InputValidator with the given configuration.
func NewInputValidator(cfg ValidatorConfig) *InputValidator {
	v := &InputValidator{
		cfg:        cfg,
		patterns:   buildPatternDatabase(),
		homoglyphs: buildHomoglyphMap(),
		stats:      make(map[ViolationType]int64),
	}
	return v
}

// Validate validates the given input and returns a ValidationResult.
// This is the main entry point — call this before every LLM invocation.
func (v *InputValidator) Validate(ctx context.Context, input string) ValidationResult {
	start := time.Now()
	result := ValidationResult{Valid: true}

	// Layer 1: Payload size enforcement.
	if len(input) > v.cfg.MaxPayloadBytes {
		result.Valid = false
		result.Violations = append(result.Violations, ValidationViolation{
			Type:        ViolationPayloadTooLarge,
			Severity:    "high",
			Description: fmt.Sprintf("input size %d bytes exceeds maximum %d bytes", len(input), v.cfg.MaxPayloadBytes),
		})
		result.RiskScore = 0.7
		result.Duration = time.Since(start)
		v.recordViolation(ViolationPayloadTooLarge)
		return result
	}

	// Layer 2: UTF-8 validity check.
	if !utf8.ValidString(input) {
		result.Valid = false
		result.Violations = append(result.Violations, ValidationViolation{
			Type:        ViolationInvalidUTF8,
			Severity:    "high",
			Description: "input contains invalid UTF-8 sequences",
		})
		result.RiskScore = 0.8
		result.Duration = time.Since(start)
		v.recordViolation(ViolationInvalidUTF8)
		return result
	}

	// Layer 3: Normalize and detect homoglyph attacks.
	normalized, homoglyphViolation := v.normalizeAndDetectHomoglyphs(input)
	if homoglyphViolation != nil {
		result.Violations = append(result.Violations, *homoglyphViolation)
		result.RiskScore += 0.3
		v.recordViolation(ViolationHomoglyphAttack)
		// Don't reject — use normalized version for further checks.
		input = normalized
	}

	// Layer 4 & 5: Pattern-based injection detection.
	for _, p := range v.patterns {
		loc := p.pattern.FindStringIndex(input)
		if loc == nil {
			continue
		}

		excerpt := safeExcerpt(input, loc[0], loc[1])
		violation := ValidationViolation{
			Type:        p.vtype,
			Severity:    p.severity,
			Description: fmt.Sprintf("pattern '%s' matched at position %d", p.name, loc[0]),
			Position:    loc[0],
			Excerpt:     excerpt,
		}
		result.Violations = append(result.Violations, violation)
		v.recordViolation(p.vtype)

		switch p.severity {
		case "critical":
			result.RiskScore += 0.5
		case "high":
			result.RiskScore += 0.3
		case "medium":
			result.RiskScore += 0.15
		case "low":
			result.RiskScore += 0.05
		}
	}

	// Context manipulation: detect repeated-character stuffing.
	// Uses code instead of regex because Go regexp lacks backreferences.
	viol := v.detectRepeatedChars(input)
	if viol != nil {
		result.Violations = append(result.Violations, *viol)
		result.RiskScore += 0.15
		v.recordViolation(ViolationContextManipulation)
	}

	// Cap risk score at 1.0.
	if result.RiskScore > 1.0 {
		result.RiskScore = 1.0
	}

	// Determine validity based on risk score and violations.
	hasCritical := false
	for _, v := range result.Violations {
		if v.Severity == "critical" {
			hasCritical = true
			break
		}
	}

	if hasCritical || result.RiskScore >= 0.3 {
		result.Valid = false
	} else if v.cfg.StrictMode && result.RiskScore >= 0.3 {
		result.Valid = false
	}

	// Layer 6: Canary token validation (if enabled).
	if len(v.cfg.CanarySecret) > 0 {
		if tampered := v.validateCanary(input); tampered {
			result.Valid = false
			result.Violations = append(result.Violations, ValidationViolation{
				Type:        ViolationCanaryTampered,
				Severity:    "critical",
				Description: "canary token was tampered — possible prompt injection via context manipulation",
			})
			result.RiskScore = 1.0
			v.recordViolation(ViolationCanaryTampered)
		}
	}

	// Produce sanitized output for valid inputs.
	if result.Valid {
		result.Sanitized = v.sanitize(normalized)
	}

	result.Duration = time.Since(start)
	return result
}

// GenerateCanaryToken generates a signed canary token to embed in system prompts.
// If an attacker tries to manipulate the context, the canary will be detected.
func (v *InputValidator) GenerateCanaryToken() string {
	nonce := make([]byte, 16)
	_, _ = rand.Read(nonce)

	mac := hmac.New(sha256.New, v.cfg.CanarySecret)
	mac.Write(nonce)
	sig := mac.Sum(nil)

	return fmt.Sprintf("<!-- jarv-canary:%s:%s -->",
		hex.EncodeToString(nonce),
		hex.EncodeToString(sig[:8]))
}

// Stats returns violation statistics for monitoring.
func (v *InputValidator) Stats() map[string]int64 {
	v.mu.RLock()
	defer v.mu.RUnlock()

	result := make(map[string]int64, len(v.stats))
	for k, val := range v.stats {
		result[string(k)] = val
	}
	return result
}

// ─────────────────────────────────────────────────────────────────────────────
// Internal Helpers
// ─────────────────────────────────────────────────────────────────────────────

func (v *InputValidator) normalizeAndDetectHomoglyphs(input string) (string, *ValidationViolation) {
	var sb strings.Builder
	sb.Grow(len(input))

	homoglyphCount := 0
	for _, r := range input {
		if ascii, ok := v.homoglyphs[r]; ok {
			sb.WriteRune(ascii)
			homoglyphCount++
		} else {
			sb.WriteRune(r)
		}
	}

	if homoglyphCount > 3 {
		return sb.String(), &ValidationViolation{
			Type:        ViolationHomoglyphAttack,
			Severity:    "high",
			Description: fmt.Sprintf("detected %d homoglyph characters — possible Unicode substitution attack", homoglyphCount),
		}
	}

	return sb.String(), nil
}

func (v *InputValidator) validateCanary(input string) bool {
	// Look for a canary token in the input.
	canaryPattern := regexp.MustCompile(`<!-- jarv-canary:([0-9a-f]+):([0-9a-f]+) -->`)
	matches := canaryPattern.FindStringSubmatch(input)
	if matches == nil {
		return false // No canary present — not a violation
	}

	nonce, err1 := hex.DecodeString(matches[1])
	sig, err2 := hex.DecodeString(matches[2])
	if err1 != nil || err2 != nil {
		return true // Malformed canary — tampered
	}

	mac := hmac.New(sha256.New, v.cfg.CanarySecret)
	mac.Write(nonce)
	expected := mac.Sum(nil)[:8]

	return !hmac.Equal(sig, expected)
}

func (v *InputValidator) sanitize(input string) string {
	// Remove control characters (except newlines and tabs).
	var sb strings.Builder
	sb.Grow(len(input))
	for _, r := range input {
		if r == '\n' || r == '\t' || !unicode.IsControl(r) {
			sb.WriteRune(r)
		}
	}
	return strings.TrimSpace(sb.String())
}

// detectRepeatedChars finds inputs with 200+ consecutive repeated characters,
// which is a context-stuffing attack pattern.
func (v *InputValidator) detectRepeatedChars(input string) *ValidationViolation {
	const maxRun = 200
	runLen := 1
	for i := 1; i < len(input); i++ {
		if input[i] == input[i-1] {
			runLen++
			if runLen >= maxRun {
			return &ValidationViolation{
				Type:        ViolationContextManipulation,
				Severity:    "critical",
				Description: fmt.Sprintf("repeated character run of %d detected", runLen),
					Position:    i - runLen + 1,
					Excerpt:     safeExcerpt(input, i-runLen+1, i+1),
				}
			}
		} else {
			runLen = 1
		}
	}
	return nil
}

func (v *InputValidator) recordViolation(vtype ViolationType) {
	v.mu.Lock()
	v.stats[vtype]++
	v.mu.Unlock()
}

// safeExcerpt returns a sanitized excerpt around a match position for logging.
// Ensures no sensitive data is included in logs.
func safeExcerpt(input string, start, end int) string {
	const contextLen = 20
	from := start - contextLen
	if from < 0 {
		from = 0
	}
	to := end + contextLen
	if to > len(input) {
		to = len(input)
	}
	excerpt := input[from:to]
	// Replace non-printable characters.
	excerpt = regexp.MustCompile(`[^\x20-\x7E]`).ReplaceAllString(excerpt, "?")
	if len(excerpt) > 60 {
		excerpt = excerpt[:57] + "..."
	}
	return excerpt
}

// buildHomoglyphMap returns a map of common Unicode homoglyphs to their ASCII equivalents.
// Covers Cyrillic, Greek, and other scripts commonly used in homoglyph attacks.
func buildHomoglyphMap() map[rune]rune {
	return map[rune]rune{
		// Cyrillic homoglyphs
		'а': 'a', 'е': 'e', 'о': 'o', 'р': 'p', 'с': 'c', 'х': 'x',
		'А': 'A', 'В': 'B', 'Е': 'E', 'К': 'K', 'М': 'M', 'Н': 'H',
		'О': 'O', 'Р': 'P', 'С': 'C', 'Т': 'T', 'Х': 'X',
		// Greek homoglyphs
		'α': 'a', 'β': 'b', 'ε': 'e', 'ο': 'o', 'ρ': 'p', 'ν': 'v',
		'Α': 'A', 'Β': 'B', 'Ε': 'E', 'Ζ': 'Z', 'Η': 'H', 'Ι': 'I',
		'Κ': 'K', 'Μ': 'M', 'Ν': 'N', 'Ο': 'O', 'Ρ': 'R', 'Τ': 'T',
		// Mathematical alphanumerics (commonly used to bypass filters)
		'𝐚': 'a', '𝐛': 'b', '𝐜': 'c', '𝐝': 'd', '𝐞': 'e',
		'𝐀': 'A', '𝐁': 'B', '𝐂': 'C', '𝐃': 'D', '𝐄': 'E',
		// Fullwidth characters
		'ａ': 'a', 'ｂ': 'b', 'ｃ': 'c', 'ｄ': 'd', 'ｅ': 'e',
		'Ａ': 'A', 'Ｂ': 'B', 'Ｃ': 'C', 'Ｄ': 'D', 'Ｅ': 'E',
	}
}

// Ensure context is used.
var _ = context.Background
