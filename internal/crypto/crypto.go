package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

const (
	// EncryptedPrefix is prepended to encrypted values to identify them
	EncryptedPrefix = "enc:v1:"

	// KeyFileName is the basename of the auto-generated key file under DataDir.
	KeyFileName = ".encryption_key"

	// keySize is the AES-256 key length in bytes.
	keySize = 32
)

var (
	// Singleton key manager
	keyManager     *KeyManager
	keyManagerOnce sync.Once

	ErrNoEncryptionKey = errors.New("no encryption key configured")
	ErrDecryptFailed   = errors.New("decryption failed: invalid ciphertext")
)

// KeyManager handles encryption key derivation and storage.
type KeyManager struct {
	mu  sync.RWMutex
	key []byte
}

// GetKeyManager returns the singleton key manager instance, lazily initializing
// it on first use. Production callers should call Init before relying on
// encryption; the lazy initializer only handles the env-var and test-binary
// cases (see initialize).
func GetKeyManager() *KeyManager {
	keyManagerOnce.Do(func() {
		keyManager = &KeyManager{}
		keyManager.initialize()
	})
	return keyManager
}

// Init configures the encryption key for production use. Call once from main
// before any Encrypt/Decrypt. Lookup order:
//  1. HEALARR_ENCRYPTION_KEY env var (SHA-256 derived to 32 bytes)
//  2. {dataDir}/.encryption_key file (must be exactly 32 bytes)
//  3. Auto-generate a 32-byte random key, write to {dataDir}/.encryption_key (0600), use it
//
// Returns an error only if the file system operations fail; production should
// treat the error as fatal so the service never runs with secrets unprotected.
func Init(dataDir string) error {
	km := GetKeyManager()

	if envKey := os.Getenv("HEALARR_ENCRYPTION_KEY"); envKey != "" {
		hash := sha256.Sum256([]byte(envKey))
		km.setKey(hash[:])
		return nil
	}

	keyPath := filepath.Join(dataDir, KeyFileName)
	data, err := os.ReadFile(keyPath)
	switch {
	case err == nil:
		if len(data) != keySize {
			return fmt.Errorf("invalid encryption key file %s: expected %d bytes, got %d", keyPath, keySize, len(data))
		}
		km.setKey(data)
		return nil
	case !errors.Is(err, os.ErrNotExist):
		return fmt.Errorf("read encryption key file %s: %w", keyPath, err)
	}

	// File does not exist — generate and persist a new key.
	key := make([]byte, keySize)
	if _, err := rand.Read(key); err != nil {
		return fmt.Errorf("generate encryption key: %w", err)
	}
	if err := os.WriteFile(keyPath, key, 0o600); err != nil {
		return fmt.Errorf("write encryption key file %s: %w", keyPath, err)
	}
	km.setKey(key)
	return nil
}

// initialize is the lazy fallback used when Init is not explicitly called.
// It handles two scenarios:
//   - The HEALARR_ENCRYPTION_KEY env var is set (legacy production deploys)
//   - The binary is running under `go test` — installs a deterministic test
//     key so unit tests can encrypt/decrypt without per-test setup
//
// Production binaries that neither set the env var nor call Init will see
// HasKey() == false and Encrypt will return ErrNoEncryptionKey — fail closed.
func (km *KeyManager) initialize() {
	if envKey := os.Getenv("HEALARR_ENCRYPTION_KEY"); envKey != "" {
		hash := sha256.Sum256([]byte(envKey))
		km.setKey(hash[:])
		return
	}

	if testing.Testing() {
		hash := sha256.Sum256([]byte("healarr-test-key-do-not-use-in-production"))
		km.setKey(hash[:])
		return
	}

	// Leave km.key == nil; Encrypt will error, Decrypt will pass through
	// legacy plaintext rows. Production code must call Init.
}

func (km *KeyManager) setKey(key []byte) {
	km.mu.Lock()
	defer km.mu.Unlock()
	km.key = key
}

func (km *KeyManager) getKey() []byte {
	km.mu.RLock()
	defer km.mu.RUnlock()
	return km.key
}

// HasKey returns true if an encryption key is configured.
func (km *KeyManager) HasKey() bool {
	return km.getKey() != nil
}

// Encrypt encrypts plaintext using AES-GCM and returns the value prefixed
// with EncryptedPrefix. Returns ErrNoEncryptionKey if no key is configured.
func (km *KeyManager) Encrypt(plaintext string) (string, error) {
	key := km.getKey()
	if key == nil {
		return "", ErrNoEncryptionKey
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}

	aesGCM, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	nonce := make([]byte, aesGCM.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}

	ciphertext := aesGCM.Seal(nonce, nonce, []byte(plaintext), nil)
	return EncryptedPrefix + base64.StdEncoding.EncodeToString(ciphertext), nil
}

// Decrypt reverses Encrypt. Values without EncryptedPrefix are returned
// unchanged for backwards compatibility with rows written before encryption
// was mandatory.
func (km *KeyManager) Decrypt(ciphertext string) (string, error) {
	if len(ciphertext) <= len(EncryptedPrefix) || ciphertext[:len(EncryptedPrefix)] != EncryptedPrefix {
		return ciphertext, nil
	}

	key := km.getKey()
	if key == nil {
		return "", ErrNoEncryptionKey
	}

	encoded := ciphertext[len(EncryptedPrefix):]
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", err
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}

	aesGCM, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	nonceSize := aesGCM.NonceSize()
	if len(data) < nonceSize {
		return "", ErrDecryptFailed
	}

	nonce, ciphertextBytes := data[:nonceSize], data[nonceSize:]
	plaintext, err := aesGCM.Open(nil, nonce, ciphertextBytes, nil)
	if err != nil {
		return "", ErrDecryptFailed
	}

	return string(plaintext), nil
}

// Convenience functions using the singleton key manager.

func Encrypt(plaintext string) (string, error) {
	return GetKeyManager().Encrypt(plaintext)
}

func Decrypt(ciphertext string) (string, error) {
	return GetKeyManager().Decrypt(ciphertext)
}

func IsEncrypted(value string) bool {
	return len(value) > len(EncryptedPrefix) && value[:len(EncryptedPrefix)] == EncryptedPrefix
}

func EncryptionEnabled() bool {
	return GetKeyManager().HasKey()
}
