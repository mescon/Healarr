package crypto

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// =============================================================================
// Helper for subprocess tests
// Due to sync.Once global state, some tests must run in subprocesses
// =============================================================================

func TestMain(m *testing.M) {
	// Check if we're in a subprocess test
	switch os.Getenv("TEST_CRYPTO_SUBPROCESS") {
	case "encrypt_with_key":
		testEncryptWithKeySubprocess()
		os.Exit(0)
	case "decrypt_with_key":
		testDecryptWithKeySubprocess()
		os.Exit(0)
	case "decrypt_no_key":
		testDecryptNoKeySubprocess()
		os.Exit(0)
	case "roundtrip":
		testRoundtripSubprocess()
		os.Exit(0)
	}

	os.Exit(m.Run())
}

// =============================================================================
// EncryptedPrefix tests
// =============================================================================

func TestEncryptedPrefix(t *testing.T) {
	if EncryptedPrefix != "enc:v1:" {
		t.Errorf("EncryptedPrefix = %q, want %q", EncryptedPrefix, "enc:v1:")
	}
}

// =============================================================================
// IsEncrypted tests (no global state dependency)
// =============================================================================

func TestIsEncrypted_WithPrefix(t *testing.T) {
	if !IsEncrypted("enc:v1:somedata") {
		t.Error("IsEncrypted() should return true for values with prefix")
	}
}

func TestIsEncrypted_WithoutPrefix(t *testing.T) {
	tests := []string{
		"plaintext",
		"enc:",
		"enc:v",
		"enc:v1",
		"enc:v1", // Exactly the prefix length
		"",
	}

	for _, tt := range tests {
		if IsEncrypted(tt) {
			t.Errorf("IsEncrypted(%q) = true, want false", tt)
		}
	}
}

func TestIsEncrypted_EdgeCases(t *testing.T) {
	// Just the prefix without data after
	if IsEncrypted("enc:v1:") {
		t.Log("IsEncrypted returns true for prefix with empty data - acceptable behavior")
	}

	// Wrong version
	if IsEncrypted("enc:v2:data") {
		t.Error("IsEncrypted() should return false for wrong version prefix")
	}
}

// =============================================================================
// Error variable tests
// =============================================================================

func TestErrorVariables(t *testing.T) {
	if ErrNoEncryptionKey == nil {
		t.Error("ErrNoEncryptionKey should not be nil")
	}
	if ErrDecryptFailed == nil {
		t.Error("ErrDecryptFailed should not be nil")
	}

	if !strings.Contains(ErrNoEncryptionKey.Error(), "encryption key") {
		t.Errorf("ErrNoEncryptionKey message unexpected: %s", ErrNoEncryptionKey.Error())
	}
	if !strings.Contains(ErrDecryptFailed.Error(), "decrypt") {
		t.Errorf("ErrDecryptFailed message unexpected: %s", ErrDecryptFailed.Error())
	}
}

// =============================================================================
// Subprocess tests for global state
// =============================================================================

func TestEncryptWithKey_Subprocess(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=TestEncryptWithKey_Subprocess")
	cmd.Env = append(os.Environ(),
		"TEST_CRYPTO_SUBPROCESS=encrypt_with_key",
		"HEALARR_ENCRYPTION_KEY=test-key-12345",
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Subprocess failed: %v\nOutput: %s", err, output)
	}
}

func testEncryptWithKeySubprocess() {
	km := GetKeyManager()
	if !km.HasKey() {
		os.Stderr.WriteString("ERROR: Expected HasKey() = true\n")
		os.Exit(1)
	}

	encrypted, err := km.Encrypt("secret")
	if err != nil {
		os.Stderr.WriteString("ERROR: Encrypt failed: " + err.Error() + "\n")
		os.Exit(1)
	}

	if !strings.HasPrefix(encrypted, EncryptedPrefix) {
		os.Stderr.WriteString("ERROR: Encrypted value missing prefix\n")
		os.Exit(1)
	}

	// Verify package-level function also works
	encrypted2, err := Encrypt("secret")
	if err != nil {
		os.Stderr.WriteString("ERROR: Package Encrypt failed: " + err.Error() + "\n")
		os.Exit(1)
	}

	if !strings.HasPrefix(encrypted2, EncryptedPrefix) {
		os.Stderr.WriteString("ERROR: Package Encrypt missing prefix\n")
		os.Exit(1)
	}

	if !EncryptionEnabled() {
		os.Stderr.WriteString("ERROR: EncryptionEnabled should be true\n")
		os.Exit(1)
	}
}

func TestDecryptWithKey_Subprocess(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=TestDecryptWithKey_Subprocess")
	cmd.Env = append(os.Environ(),
		"TEST_CRYPTO_SUBPROCESS=decrypt_with_key",
		"HEALARR_ENCRYPTION_KEY=test-key-12345",
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Subprocess failed: %v\nOutput: %s", err, output)
	}
}

func testDecryptWithKeySubprocess() {
	km := GetKeyManager()

	original := "my secret data"
	encrypted, err := km.Encrypt(original)
	if err != nil {
		os.Stderr.WriteString("ERROR: Encrypt failed: " + err.Error() + "\n")
		os.Exit(1)
	}

	decrypted, err := km.Decrypt(encrypted)
	if err != nil {
		os.Stderr.WriteString("ERROR: Decrypt failed: " + err.Error() + "\n")
		os.Exit(1)
	}

	if decrypted != original {
		os.Stderr.WriteString("ERROR: Decrypted value mismatch\n")
		os.Exit(1)
	}

	// Test backward compatibility - plain text should pass through
	plaintext := "not encrypted"
	result, err := km.Decrypt(plaintext)
	if err != nil {
		os.Stderr.WriteString("ERROR: Decrypt plain failed: " + err.Error() + "\n")
		os.Exit(1)
	}
	if result != plaintext {
		os.Stderr.WriteString("ERROR: Plain text not passed through\n")
		os.Exit(1)
	}
}

func TestDecryptNoKey_Subprocess(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=TestDecryptNoKey_Subprocess")
	cmd.Env = append(os.Environ(),
		"TEST_CRYPTO_SUBPROCESS=decrypt_no_key",
	)
	// Explicitly remove encryption key
	filteredEnv := []string{}
	for _, e := range cmd.Env {
		if !strings.HasPrefix(e, "HEALARR_ENCRYPTION_KEY=") {
			filteredEnv = append(filteredEnv, e)
		}
	}
	cmd.Env = filteredEnv

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Subprocess failed: %v\nOutput: %s", err, output)
	}
}

func testDecryptNoKeySubprocess() {
	km := GetKeyManager()

	if km.HasKey() {
		os.Stderr.WriteString("ERROR: Expected HasKey() = false\n")
		os.Exit(1)
	}

	if EncryptionEnabled() {
		os.Stderr.WriteString("ERROR: EncryptionEnabled should be false\n")
		os.Exit(1)
	}

	// Encrypt without key should return plaintext
	plaintext := "my data"
	result, err := km.Encrypt(plaintext)
	if err != nil {
		os.Stderr.WriteString("ERROR: Encrypt without key failed: " + err.Error() + "\n")
		os.Exit(1)
	}
	if result != plaintext {
		os.Stderr.WriteString("ERROR: Encrypt without key should return plaintext\n")
		os.Exit(1)
	}

	// Decrypt encrypted value without key should fail
	encryptedValue := "enc:v1:someinvaliddata"
	_, err = km.Decrypt(encryptedValue)
	if err != ErrNoEncryptionKey {
		os.Stderr.WriteString("ERROR: Expected ErrNoEncryptionKey, got: " + err.Error() + "\n")
		os.Exit(1)
	}

	// Decrypt plain text without key should pass through
	result, err = km.Decrypt("plain text")
	if err != nil {
		os.Stderr.WriteString("ERROR: Decrypt plain without key failed: " + err.Error() + "\n")
		os.Exit(1)
	}
	if result != "plain text" {
		os.Stderr.WriteString("ERROR: Plain text not passed through\n")
		os.Exit(1)
	}
}

func TestRoundtrip_Subprocess(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=TestRoundtrip_Subprocess")
	cmd.Env = append(os.Environ(),
		"TEST_CRYPTO_SUBPROCESS=roundtrip",
		"HEALARR_ENCRYPTION_KEY=roundtrip-test-key",
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Subprocess failed: %v\nOutput: %s", err, output)
	}
}

func testRoundtripSubprocess() {
	testCases := []string{
		"simple",
		"with spaces",
		"special!@#$%^&*()",
		"unicode: 日本語 🎉",
		"",
		strings.Repeat("long", 100),
	}

	for _, original := range testCases {
		encrypted, err := Encrypt(original)
		if err != nil {
			os.Stderr.WriteString("ERROR: Encrypt failed for: " + original[:min(20, len(original))] + "\n")
			os.Exit(1)
		}

		decrypted, err := Decrypt(encrypted)
		if err != nil {
			os.Stderr.WriteString("ERROR: Decrypt failed for: " + original[:min(20, len(original))] + "\n")
			os.Exit(1)
		}

		if decrypted != original {
			os.Stderr.WriteString("ERROR: Roundtrip mismatch for: " + original[:min(20, len(original))] + "\n")
			os.Exit(1)
		}
	}

	// Verify each encryption produces different ciphertext (random nonce)
	original := "same input"
	enc1, _ := Encrypt(original)
	enc2, _ := Encrypt(original)

	if enc1 == enc2 {
		os.Stderr.WriteString("ERROR: Encryption should produce different outputs (random nonce)\n")
		os.Exit(1)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// =============================================================================
// Decrypt error cases (can test without subprocess since they don't use key)
// =============================================================================

func TestDecrypt_InvalidBase64(t *testing.T) {
	// Skip if running as subprocess
	if os.Getenv("TEST_CRYPTO_SUBPROCESS") != "" {
		return
	}

	// Create a new key manager instance directly for testing
	// (bypasses singleton for this specific test)
	km := &KeyManager{
		key: make([]byte, 32), // Dummy key
	}

	// Invalid base64 after prefix
	_, err := km.Decrypt("enc:v1:not-valid-base64!!!")
	if err == nil {
		t.Error("Decrypt should fail for invalid base64")
	}
}

func TestDecrypt_TooShort(t *testing.T) {
	if os.Getenv("TEST_CRYPTO_SUBPROCESS") != "" {
		return
	}

	km := &KeyManager{
		key: make([]byte, 32),
	}

	// Valid base64 but too short for nonce
	_, err := km.Decrypt("enc:v1:YWJj") // "abc" in base64
	if err != ErrDecryptFailed {
		t.Errorf("Decrypt should return ErrDecryptFailed for short data, got: %v", err)
	}
}

func TestDecrypt_InvalidCiphertext(t *testing.T) {
	if os.Getenv("TEST_CRYPTO_SUBPROCESS") != "" {
		return
	}

	km := &KeyManager{
		key: make([]byte, 32),
	}

	// Long enough for nonce but invalid ciphertext (won't decrypt)
	// 12 bytes nonce + some garbage that won't decrypt
	_, err := km.Decrypt("enc:v1:YWJjZGVmZ2hpamtsbW5vcHFyc3R1dnd4") // 24+ bytes
	if err != ErrDecryptFailed {
		t.Errorf("Decrypt should return ErrDecryptFailed for invalid ciphertext, got: %v", err)
	}
}

// =============================================================================
// KeyManager method tests (using direct instance)
// =============================================================================

func TestKeyManager_HasKey_NilKey(t *testing.T) {
	km := &KeyManager{key: nil}
	if km.HasKey() {
		t.Error("HasKey() should return false when key is nil")
	}
}

func TestKeyManager_HasKey_WithKey(t *testing.T) {
	km := &KeyManager{key: []byte("some-key")}
	if !km.HasKey() {
		t.Error("HasKey() should return true when key is set")
	}
}

func TestKeyManager_Encrypt_NoKey_ReturnsPlaintext(t *testing.T) {
	km := &KeyManager{key: nil}

	plaintext := "my secret"
	result, err := km.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}

	if result != plaintext {
		t.Errorf("Encrypt() without key should return plaintext, got %q", result)
	}
}

func TestKeyManager_Decrypt_NoPrefix_ReturnsInput(t *testing.T) {
	km := &KeyManager{key: make([]byte, 32)}

	input := "not encrypted"
	result, err := km.Decrypt(input)
	if err != nil {
		t.Fatalf("Decrypt() error = %v", err)
	}

	if result != input {
		t.Errorf("Decrypt() without prefix should return input, got %q", result)
	}
}
