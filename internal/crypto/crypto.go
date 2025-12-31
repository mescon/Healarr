package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"os"
	"sync"
)

const (
	// EncryptedPrefix is prepended to encrypted values to identify them
	EncryptedPrefix = "enc:v1:"
)

var (
	// Singleton key manager
	keyManager     *KeyManager
	keyManagerOnce sync.Once

	ErrNoEncryptionKey = errors.New("no encryption key configured")
	ErrDecryptFailed   = errors.New("decryption failed: invalid ciphertext")
)

// KeyManager handles encryption key derivation and storage
type KeyManager struct {
	key []byte
}

// GetKeyManager returns the singleton key manager instance
func GetKeyManager() *KeyManager {
	keyManagerOnce.Do(func() {
		keyManager = &KeyManager{}
		keyManager.initialize()
	})
	return keyManager
}

// initialize sets up the encryption key from environment or generates one
func (km *KeyManager) initialize() {
	// First, try environment variable
	envKey := os.Getenv("HEALARR_ENCRYPTION_KEY")
	if envKey != "" {
		// Derive a 32-byte key from the provided key using SHA-256
		hash := sha256.Sum256([]byte(envKey))
		km.key = hash[:]
		return
	}

	// If no key is configured, encryption will be disabled
	// This allows backwards compatibility with existing installations
	km.key = nil
}

// HasKey returns true if an encryption key is configured
func (km *KeyManager) HasKey() bool {
	return km.key != nil
}

// Encrypt encrypts plaintext using AES-GCM
// Returns the encrypted value with the EncryptedPrefix
func (km *KeyManager) Encrypt(plaintext string) (string, error) {
	if !km.HasKey() {
		// No encryption key configured, return plaintext
		return plaintext, nil
	}

	block, err := aes.NewCipher(km.key)
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
	encoded := base64.StdEncoding.EncodeToString(ciphertext)

	return EncryptedPrefix + encoded, nil
}

// Decrypt decrypts ciphertext that was encrypted with Encrypt
// If the value doesn't have the EncryptedPrefix, it's returned as-is (for backwards compatibility)
func (km *KeyManager) Decrypt(ciphertext string) (string, error) {
	// Check if this is an encrypted value
	if len(ciphertext) <= len(EncryptedPrefix) || ciphertext[:len(EncryptedPrefix)] != EncryptedPrefix {
		// Not encrypted, return as-is for backwards compatibility
		return ciphertext, nil
	}

	if !km.HasKey() {
		// Value is encrypted but no key configured
		return "", ErrNoEncryptionKey
	}

	// Remove prefix and decode
	encoded := ciphertext[len(EncryptedPrefix):]
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", err
	}

	block, err := aes.NewCipher(km.key)
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

// Convenience functions using the singleton key manager

// Encrypt encrypts plaintext using the global key manager
func Encrypt(plaintext string) (string, error) {
	return GetKeyManager().Encrypt(plaintext)
}

// Decrypt decrypts ciphertext using the global key manager
func Decrypt(ciphertext string) (string, error) {
	return GetKeyManager().Decrypt(ciphertext)
}

// IsEncrypted checks if a value appears to be encrypted
func IsEncrypted(value string) bool {
	return len(value) > len(EncryptedPrefix) && value[:len(EncryptedPrefix)] == EncryptedPrefix
}

// EncryptionEnabled returns true if encryption is enabled
func EncryptionEnabled() bool {
	return GetKeyManager().HasKey()
}
