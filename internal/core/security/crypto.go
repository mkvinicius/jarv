// crypto.go — AES-256-GCM Encryption at Rest for JARV.
//
// Provides FIPS 140-2 compliant encryption for all data stored on disk:
//   - Memory entries (SQLite WAL + data files)
//   - Audit log entries
//   - Knowledge graph triplets
//   - Oracle simulation results
//   - Skill definitions
//
// Key Derivation: PBKDF2-SHA256 (FIPS 140-2 approved) or HKDF-SHA256
// Encryption:     AES-256-GCM (FIPS 140-2 approved)
// Integrity:      GCM authentication tag (128-bit)
// Key Rotation:   Envelope encryption with DEK/KEK model
//
// Wire format (all encrypted blobs):
//
//	[4 bytes: version] [12 bytes: nonce] [4 bytes: key_id len] [key_id] [ciphertext+tag]
package security

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"golang.org/x/crypto/pbkdf2"
)

// ─────────────────────────────────────────────────────────────────────────────
// Constants
// ─────────────────────────────────────────────────────────────────────────────

const (
	// cryptoVersion identifies the encryption format.
	// Increment when the format changes to enable migration.
	cryptoVersion uint32 = 1

	// aesKeySize is the AES-256 key size in bytes.
	aesKeySize = 32

	// gcmNonceSize is the GCM nonce size in bytes (96-bit, NIST recommended).
	gcmNonceSize = 12

	// gcmTagSize is the GCM authentication tag size in bytes (128-bit).
	gcmTagSize = 16

	// pbkdf2Iterations is the PBKDF2 iteration count.
	// NIST SP 800-132 recommends >= 10,000; we use 600,000 (OWASP 2023 recommendation).
	pbkdf2Iterations = 600_000

	// saltSize is the PBKDF2 salt size in bytes.
	saltSize = 32
)

// ─────────────────────────────────────────────────────────────────────────────
// Errors
// ─────────────────────────────────────────────────────────────────────────────

var (
	ErrInvalidCiphertext = errors.New("crypto: invalid ciphertext")
	ErrVersionMismatch   = errors.New("crypto: unsupported encryption version")
	ErrKeyNotFound       = errors.New("crypto: encryption key not found")
	ErrKeyRotationFailed = errors.New("crypto: key rotation failed")
	ErrDecryptionFailed  = errors.New("crypto: decryption failed — data may be corrupted or tampered")
)

// ─────────────────────────────────────────────────────────────────────────────
// Key Material
// ─────────────────────────────────────────────────────────────────────────────

// KeyID uniquely identifies an encryption key version.
type KeyID string

// KeyMaterial holds an AES-256 key with metadata.
type KeyMaterial struct {
	ID        KeyID
	Key       []byte // 32 bytes, AES-256
	CreatedAt time.Time
	ExpiresAt time.Time
	Active    bool
}

// ─────────────────────────────────────────────────────────────────────────────
// Key Derivation
// ─────────────────────────────────────────────────────────────────────────────

// DeriveKey derives an AES-256 key from a passphrase using PBKDF2-SHA256.
// This is FIPS 140-2 compliant (PBKDF2 + SHA-256 + AES-256).
//
// The salt should be stored alongside the encrypted data and is NOT secret.
// The passphrase IS secret and must never be stored.
func DeriveKey(passphrase string, salt []byte) ([]byte, error) {
	if len(passphrase) == 0 {
		return nil, errors.New("crypto: passphrase must not be empty")
	}
	if len(salt) < saltSize {
		return nil, fmt.Errorf("crypto: salt must be at least %d bytes", saltSize)
	}

	key := pbkdf2.Key([]byte(passphrase), salt, pbkdf2Iterations, aesKeySize, sha256.New)
	return key, nil
}

// GenerateSalt generates a cryptographically secure random salt.
func GenerateSalt() ([]byte, error) {
	salt := make([]byte, saltSize)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return nil, fmt.Errorf("crypto: failed to generate salt: %w", err)
	}
	return salt, nil
}

// GenerateKey generates a new random AES-256 key.
func GenerateKey() ([]byte, error) {
	key := make([]byte, aesKeySize)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, fmt.Errorf("crypto: failed to generate key: %w", err)
	}
	return key, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Encryption / Decryption Primitives
// ─────────────────────────────────────────────────────────────────────────────

// Encrypt encrypts plaintext using AES-256-GCM with a random nonce.
// Returns the full encrypted blob including version, nonce, key_id, and ciphertext.
func Encrypt(plaintext []byte, key []byte, keyID KeyID) ([]byte, error) {
	if len(key) != aesKeySize {
		return nil, fmt.Errorf("crypto: key must be %d bytes, got %d", aesKeySize, len(key))
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("crypto: failed to create AES cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("crypto: failed to create GCM: %w", err)
	}

	// Generate random nonce.
	nonce := make([]byte, gcmNonceSize)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("crypto: failed to generate nonce: %w", err)
	}

	// Encrypt with GCM (appends authentication tag automatically).
	ciphertext := gcm.Seal(nil, nonce, plaintext, nil)

	// Build wire format:
	// [4: version][12: nonce][4: keyID len][keyID bytes][ciphertext+tag]
	keyIDBytes := []byte(keyID)
	totalLen := 4 + gcmNonceSize + 4 + len(keyIDBytes) + len(ciphertext)
	blob := make([]byte, 0, totalLen)

	versionBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(versionBytes, cryptoVersion)
	blob = append(blob, versionBytes...)
	blob = append(blob, nonce...)

	keyIDLenBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(keyIDLenBytes, uint32(len(keyIDBytes)))
	blob = append(blob, keyIDLenBytes...)
	blob = append(blob, keyIDBytes...)
	blob = append(blob, ciphertext...)

	return blob, nil
}

// Decrypt decrypts a blob produced by Encrypt.
// The keyFn is called with the KeyID to retrieve the corresponding key.
func Decrypt(blob []byte, keyFn func(KeyID) ([]byte, error)) ([]byte, error) {
	// Minimum: 4 (version) + 12 (nonce) + 4 (keyID len) + 0 (keyID) + 16 (tag)
	if len(blob) < 4+gcmNonceSize+4+gcmTagSize {
		return nil, ErrInvalidCiphertext
	}

	offset := 0

	// Parse version.
	version := binary.BigEndian.Uint32(blob[offset : offset+4])
	offset += 4
	if version != cryptoVersion {
		return nil, fmt.Errorf("%w: got %d, expected %d", ErrVersionMismatch, version, cryptoVersion)
	}

	// Parse nonce.
	nonce := blob[offset : offset+gcmNonceSize]
	offset += gcmNonceSize

	// Parse key ID.
	keyIDLen := int(binary.BigEndian.Uint32(blob[offset : offset+4]))
	offset += 4
	if offset+keyIDLen > len(blob) {
		return nil, ErrInvalidCiphertext
	}
	keyID := KeyID(blob[offset : offset+keyIDLen])
	offset += keyIDLen

	// Remaining bytes are ciphertext + GCM tag.
	ciphertext := blob[offset:]
	if len(ciphertext) < gcmTagSize {
		return nil, ErrInvalidCiphertext
	}

	// Retrieve key.
	key, err := keyFn(keyID)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrKeyNotFound, err)
	}
	if len(key) != aesKeySize {
		return nil, fmt.Errorf("crypto: retrieved key has wrong size: %d", len(key))
	}

	// Decrypt.
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("crypto: failed to create AES cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("crypto: failed to create GCM: %w", err)
	}

	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, ErrDecryptionFailed
	}

	return plaintext, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Key Manager (Envelope Encryption — DEK/KEK Model)
// ─────────────────────────────────────────────────────────────────────────────

// KeyManager manages encryption keys with support for rotation.
//
// Uses envelope encryption:
//   - KEK (Key Encryption Key): derived from user passphrase, never stored
//   - DEK (Data Encryption Key): random, encrypted with KEK, stored on disk
//
// This means the user's passphrase only needs to be entered once at startup.
// All data is encrypted with DEKs, which are themselves encrypted with the KEK.
type KeyManager struct {
	mu      sync.RWMutex
	keys    map[KeyID]*KeyMaterial
	active  KeyID
	dataDir string
	kek     []byte // Key Encryption Key — in memory only, never persisted
}

// NewKeyManager creates a new KeyManager.
// The kek (Key Encryption Key) is derived from the user's passphrase and
// is held in memory only — never written to disk.
func NewKeyManager(dataDir string, kek []byte) (*KeyManager, error) {
	if len(kek) != aesKeySize {
		return nil, fmt.Errorf("crypto: KEK must be %d bytes", aesKeySize)
	}

	km := &KeyManager{
		keys:    make(map[KeyID]*KeyMaterial),
		dataDir: dataDir,
		kek:     kek,
	}

	if err := os.MkdirAll(filepath.Join(dataDir, "keys"), 0700); err != nil {
		return nil, fmt.Errorf("crypto: failed to create key directory: %w", err)
	}

	// Load existing keys or generate the first DEK.
	if err := km.loadOrInit(); err != nil {
		return nil, err
	}

	return km, nil
}

// Encrypt encrypts data using the active DEK.
func (km *KeyManager) Encrypt(plaintext []byte) ([]byte, error) {
	km.mu.RLock()
	activeKey, ok := km.keys[km.active]
	km.mu.RUnlock()

	if !ok {
		return nil, ErrKeyNotFound
	}

	return Encrypt(plaintext, activeKey.Key, km.active)
}

// Decrypt decrypts data, automatically selecting the correct DEK by key ID.
func (km *KeyManager) Decrypt(blob []byte) ([]byte, error) {
	return Decrypt(blob, func(id KeyID) ([]byte, error) {
		km.mu.RLock()
		defer km.mu.RUnlock()

		key, ok := km.keys[id]
		if !ok {
			return nil, fmt.Errorf("key %s not found", id)
		}
		return key.Key, nil
	})
}

// Rotate generates a new DEK and marks it as active.
// Old DEKs are retained for decryption of existing data.
// Background re-encryption of existing data is handled separately.
func (km *KeyManager) Rotate() (KeyID, error) {
	newDEK, err := GenerateKey()
	if err != nil {
		return "", fmt.Errorf("crypto: failed to generate new DEK: %w", err)
	}

	newID := KeyID(fmt.Sprintf("dek-%d", time.Now().UnixNano()))
	material := &KeyMaterial{
		ID:        newID,
		Key:       newDEK,
		CreatedAt: time.Now(),
		ExpiresAt: time.Now().Add(90 * 24 * time.Hour), // 90-day rotation
		Active:    true,
	}

	// Deactivate current active key.
	km.mu.Lock()
	if old, ok := km.keys[km.active]; ok {
		old.Active = false
	}
	km.keys[newID] = material
	km.active = newID
	km.mu.Unlock()

	// Persist the new DEK (encrypted with KEK).
	if err := km.persistKey(material); err != nil {
		return "", fmt.Errorf("%w: %v", ErrKeyRotationFailed, err)
	}

	return newID, nil
}

// ActiveKeyID returns the ID of the currently active DEK.
func (km *KeyManager) ActiveKeyID() KeyID {
	km.mu.RLock()
	defer km.mu.RUnlock()
	return km.active
}

// KeyCount returns the number of keys managed (active + historical).
func (km *KeyManager) KeyCount() int {
	km.mu.RLock()
	defer km.mu.RUnlock()
	return len(km.keys)
}

// ─────────────────────────────────────────────────────────────────────────────
// Key Persistence (DEKs encrypted with KEK)
// ─────────────────────────────────────────────────────────────────────────────

func (km *KeyManager) loadOrInit() error {
	keyDir := filepath.Join(km.dataDir, "keys")
	entries, err := os.ReadDir(keyDir)
	if err != nil {
		return fmt.Errorf("crypto: failed to read key directory: %w", err)
	}

	loaded := 0
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".key" {
			continue
		}

		path := filepath.Join(keyDir, entry.Name())
		material, err := km.loadKeyFile(path)
		if err != nil {
			// Log but don't fail — a corrupt key file shouldn't prevent startup.
			continue
		}

		km.keys[material.ID] = material
		if material.Active {
			km.active = material.ID
		}
		loaded++
	}

	// If no keys exist, generate the first DEK.
	if loaded == 0 {
		_, err := km.Rotate()
		return err
	}

	// If no active key was found (all expired), rotate.
	if km.active == "" {
		_, err := km.Rotate()
		return err
	}

	return nil
}

func (km *KeyManager) persistKey(material *KeyMaterial) error {
	// Serialize key material to JSON.
	type keyFile struct {
		ID        string `json:"id"`
		Key       []byte `json:"key"` // Will be encrypted before writing
		CreatedAt int64  `json:"created_at"`
		ExpiresAt int64  `json:"expires_at"`
		Active    bool   `json:"active"`
	}

	kf := keyFile{
		ID:        string(material.ID),
		Key:       material.Key,
		CreatedAt: material.CreatedAt.Unix(),
		ExpiresAt: material.ExpiresAt.Unix(),
		Active:    material.Active,
	}

	// Encrypt the DEK with the KEK.
	plaintext := fmt.Sprintf("%s:%x:%d:%d:%v",
		kf.ID, kf.Key, kf.CreatedAt, kf.ExpiresAt, kf.Active)
	encrypted, err := Encrypt([]byte(plaintext), km.kek, "kek")
	if err != nil {
		return fmt.Errorf("crypto: failed to encrypt DEK: %w", err)
	}

	path := filepath.Join(km.dataDir, "keys", string(material.ID)+".key")
	return os.WriteFile(path, encrypted, 0600)
}

func (km *KeyManager) loadKeyFile(path string) (*KeyMaterial, error) {
	encrypted, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("crypto: failed to read key file: %w", err)
	}

	plaintext, err := Decrypt(encrypted, func(id KeyID) ([]byte, error) {
		if id == "kek" {
			return km.kek, nil
		}
		return nil, fmt.Errorf("unexpected key ID: %s", id)
	})
	if err != nil {
		return nil, fmt.Errorf("crypto: failed to decrypt key file: %w", err)
	}

	// Parse: "id:hex_key:created_at:expires_at:active"
	var id string
	var keyHex string
	var createdAt, expiresAt int64
	var active bool

	_, err = fmt.Sscanf(string(plaintext), "%s:%s:%d:%d:%v",
		&id, &keyHex, &createdAt, &expiresAt, &active)
	if err != nil {
		return nil, fmt.Errorf("crypto: failed to parse key file: %w", err)
	}

	key := make([]byte, aesKeySize)
	if _, err := fmt.Sscanf(keyHex, "%x", &key); err != nil {
		return nil, fmt.Errorf("crypto: failed to parse key hex: %w", err)
	}

	return &KeyMaterial{
		ID:        KeyID(id),
		Key:       key,
		CreatedAt: time.Unix(createdAt, 0),
		ExpiresAt: time.Unix(expiresAt, 0),
		Active:    active,
	}, nil
}
