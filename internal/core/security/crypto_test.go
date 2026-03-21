// crypto_test.go — Unit tests for JARV AES-256-GCM encryption.
package security

import (
	"bytes"
	"crypto/rand"
	"io"
	"testing"
)

func TestEncryptDecrypt_RoundTrip(t *testing.T) {
	key, _ := GenerateKey()
	keyID := KeyID("test-key-001")

	plaintext := []byte("Hello, JARV! This is a secret message.")

	ciphertext, err := Encrypt(plaintext, key, keyID)
	if err != nil {
		t.Fatalf("encryption failed: %v", err)
	}

	decrypted, err := Decrypt(ciphertext, func(id KeyID) ([]byte, error) {
		if id == keyID {
			return key, nil
		}
		return nil, ErrKeyNotFound
	})
	if err != nil {
		t.Fatalf("decryption failed: %v", err)
	}

	if !bytes.Equal(plaintext, decrypted) {
		t.Errorf("decrypted text does not match original")
	}
}

func TestEncrypt_ProducesUniqueCiphertexts(t *testing.T) {
	key, _ := GenerateKey()
	keyID := KeyID("test-key-001")
	plaintext := []byte("same plaintext")

	ct1, _ := Encrypt(plaintext, key, keyID)
	ct2, _ := Encrypt(plaintext, key, keyID)

	// Due to random nonce, ciphertexts must differ.
	if bytes.Equal(ct1, ct2) {
		t.Error("two encryptions of the same plaintext produced identical ciphertexts (nonce reuse!)")
	}
}

func TestDecrypt_TamperedCiphertext(t *testing.T) {
	key, _ := GenerateKey()
	keyID := KeyID("test-key-001")
	plaintext := []byte("sensitive data")

	ciphertext, _ := Encrypt(plaintext, key, keyID)

	// Tamper with the last byte of the ciphertext (authentication tag).
	ciphertext[len(ciphertext)-1] ^= 0xFF

	_, err := Decrypt(ciphertext, func(id KeyID) ([]byte, error) {
		return key, nil
	})
	if err == nil {
		t.Error("expected decryption to fail on tampered ciphertext, but it succeeded")
	}
}

func TestDecrypt_WrongKey(t *testing.T) {
	key1, _ := GenerateKey()
	key2, _ := GenerateKey()
	keyID := KeyID("test-key-001")
	plaintext := []byte("secret")

	ciphertext, _ := Encrypt(plaintext, key1, keyID)

	_, err := Decrypt(ciphertext, func(id KeyID) ([]byte, error) {
		return key2, nil // Wrong key
	})
	if err == nil {
		t.Error("expected decryption to fail with wrong key, but it succeeded")
	}
}

func TestDecrypt_InvalidBlob(t *testing.T) {
	_, err := Decrypt([]byte("not a valid blob"), func(id KeyID) ([]byte, error) {
		return nil, nil
	})
	if err == nil {
		t.Error("expected error for invalid blob")
	}
}

func TestDecrypt_EmptyBlob(t *testing.T) {
	_, err := Decrypt([]byte{}, func(id KeyID) ([]byte, error) {
		return nil, nil
	})
	if err == nil {
		t.Error("expected error for empty blob")
	}
}

func TestDeriveKey_Deterministic(t *testing.T) {
	salt, _ := GenerateSalt()
	passphrase := "my-super-secret-passphrase"

	key1, err := DeriveKey(passphrase, salt)
	if err != nil {
		t.Fatalf("key derivation failed: %v", err)
	}

	key2, err := DeriveKey(passphrase, salt)
	if err != nil {
		t.Fatalf("key derivation failed: %v", err)
	}

	if !bytes.Equal(key1, key2) {
		t.Error("PBKDF2 is not deterministic — same inputs must produce same output")
	}
}

func TestDeriveKey_DifferentSalts(t *testing.T) {
	passphrase := "same-passphrase"
	salt1, _ := GenerateSalt()
	salt2, _ := GenerateSalt()

	key1, _ := DeriveKey(passphrase, salt1)
	key2, _ := DeriveKey(passphrase, salt2)

	if bytes.Equal(key1, key2) {
		t.Error("different salts must produce different keys")
	}
}

func TestDeriveKey_EmptyPassphrase(t *testing.T) {
	salt, _ := GenerateSalt()
	_, err := DeriveKey("", salt)
	if err == nil {
		t.Error("expected error for empty passphrase")
	}
}

func TestGenerateKey_Length(t *testing.T) {
	key, err := GenerateKey()
	if err != nil {
		t.Fatalf("key generation failed: %v", err)
	}
	if len(key) != aesKeySize {
		t.Errorf("expected key length %d, got %d", aesKeySize, len(key))
	}
}

func TestGenerateKey_Uniqueness(t *testing.T) {
	key1, _ := GenerateKey()
	key2, _ := GenerateKey()
	if bytes.Equal(key1, key2) {
		t.Error("two generated keys are identical — RNG may be broken")
	}
}

func TestEncrypt_LargePayload(t *testing.T) {
	key, _ := GenerateKey()
	keyID := KeyID("test-key-001")

	// 1MB payload
	plaintext := make([]byte, 1*1024*1024)
	_, _ = io.ReadFull(rand.Reader, plaintext)

	ciphertext, err := Encrypt(plaintext, key, keyID)
	if err != nil {
		t.Fatalf("encryption of large payload failed: %v", err)
	}

	decrypted, err := Decrypt(ciphertext, func(id KeyID) ([]byte, error) {
		return key, nil
	})
	if err != nil {
		t.Fatalf("decryption of large payload failed: %v", err)
	}

	if !bytes.Equal(plaintext, decrypted) {
		t.Error("large payload round-trip failed")
	}
}

func TestKeyManager_RotateAndDecrypt(t *testing.T) {
	dir := t.TempDir()
	kek, _ := GenerateKey()

	km, err := NewKeyManager(dir, kek)
	if err != nil {
		t.Fatalf("failed to create key manager: %v", err)
	}

	plaintext := []byte("data encrypted before rotation")

	// Encrypt with initial key.
	ciphertext, err := km.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("encryption failed: %v", err)
	}

	// Rotate keys.
	newID, err := km.Rotate()
	if err != nil {
		t.Fatalf("key rotation failed: %v", err)
	}
	if newID == "" {
		t.Error("rotation returned empty key ID")
	}

	// Old data must still be decryptable after rotation.
	decrypted, err := km.Decrypt(ciphertext)
	if err != nil {
		t.Fatalf("decryption after rotation failed: %v", err)
	}

	if !bytes.Equal(plaintext, decrypted) {
		t.Error("data decrypted after rotation does not match original")
	}
}

func BenchmarkEncrypt_1KB(b *testing.B) {
	key, _ := GenerateKey()
	keyID := KeyID("bench-key")
	plaintext := make([]byte, 1024)
	_, _ = io.ReadFull(rand.Reader, plaintext)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = Encrypt(plaintext, key, keyID)
	}
}

func BenchmarkDecrypt_1KB(b *testing.B) {
	key, _ := GenerateKey()
	keyID := KeyID("bench-key")
	plaintext := make([]byte, 1024)
	ciphertext, _ := Encrypt(plaintext, key, keyID)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = Decrypt(ciphertext, func(id KeyID) ([]byte, error) {
			return key, nil
		})
	}
}
