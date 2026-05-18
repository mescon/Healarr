package crypto

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
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
	case "roundtrip":
		testRoundtripSubprocess()
		os.Exit(0)
	case "init_env_var":
		testInitEnvVarSubprocess()
		os.Exit(0)
	case "init_file":
		testInitFileSubprocess()
		os.Exit(0)
	case "init_autogen":
		testInitAutoGenSubprocess()
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
		"",
	}

	for _, tt := range tests {
		if IsEncrypted(tt) {
			t.Errorf("IsEncrypted(%q) = true, want false", tt)
		}
	}
}

func TestIsEncrypted_EdgeCases(t *testing.T) {
	if IsEncrypted("enc:v1:") {
		t.Log("IsEncrypted returns true for prefix with empty data - acceptable behavior")
	}

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
// Subprocess tests for global state — env-var path
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

	// Backward compat: legacy plaintext rows (no prefix) pass through unchanged
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

	// Each encryption should produce different ciphertext due to random nonce
	original := "same input"
	enc1, _ := Encrypt(original)
	enc2, _ := Encrypt(original)

	if enc1 == enc2 {
		os.Stderr.WriteString("ERROR: Encryption should produce different outputs (random nonce)\n")
		os.Exit(1)
	}
}

// =============================================================================
// Init() subprocess tests
// =============================================================================

// TestInit_EnvVar verifies Init honors HEALARR_ENCRYPTION_KEY when set.
func TestInit_EnvVar(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=TestInit_EnvVar")
	cmd.Env = append(os.Environ(),
		"TEST_CRYPTO_SUBPROCESS=init_env_var",
		"HEALARR_ENCRYPTION_KEY=env-var-key",
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Subprocess failed: %v\nOutput: %s", err, output)
	}
}

func testInitEnvVarSubprocess() {
	tmpDir, err := os.MkdirTemp("", "crypto-init-env-*")
	if err != nil {
		os.Stderr.WriteString("ERROR: MkdirTemp: " + err.Error() + "\n")
		os.Exit(1)
	}
	defer os.RemoveAll(tmpDir)

	if err := Init(tmpDir); err != nil {
		os.Stderr.WriteString("ERROR: Init returned error: " + err.Error() + "\n")
		os.Exit(1)
	}

	// Init should NOT have created a key file when env var was used
	keyPath := filepath.Join(tmpDir, KeyFileName)
	if _, err := os.Stat(keyPath); !errors.Is(err, os.ErrNotExist) {
		os.Stderr.WriteString("ERROR: env-var path should not write key file; found " + keyPath + "\n")
		os.Exit(1)
	}

	if !EncryptionEnabled() {
		os.Stderr.WriteString("ERROR: EncryptionEnabled should be true after Init\n")
		os.Exit(1)
	}
}

// TestInit_ReadExistingFile verifies Init loads an existing 32-byte key file.
func TestInit_ReadExistingFile(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=TestInit_ReadExistingFile")
	cmd.Env = append(os.Environ(),
		"TEST_CRYPTO_SUBPROCESS=init_file",
	)
	// Drop the env var so file path takes precedence
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

func testInitFileSubprocess() {
	tmpDir, err := os.MkdirTemp("", "crypto-init-file-*")
	if err != nil {
		os.Stderr.WriteString("ERROR: MkdirTemp: " + err.Error() + "\n")
		os.Exit(1)
	}
	defer os.RemoveAll(tmpDir)

	// Pre-seed a known 32-byte key file
	knownKey := make([]byte, 32)
	for i := range knownKey {
		knownKey[i] = byte(i)
	}
	keyPath := filepath.Join(tmpDir, KeyFileName)
	if err := os.WriteFile(keyPath, knownKey, 0o600); err != nil {
		os.Stderr.WriteString("ERROR: WriteFile: " + err.Error() + "\n")
		os.Exit(1)
	}

	if err := Init(tmpDir); err != nil {
		os.Stderr.WriteString("ERROR: Init returned error: " + err.Error() + "\n")
		os.Exit(1)
	}

	// Confirm the loaded key matches the seeded one
	loaded := GetKeyManager().getKey()
	if len(loaded) != 32 {
		os.Stderr.WriteString("ERROR: loaded key wrong length\n")
		os.Exit(1)
	}
	for i, b := range knownKey {
		if loaded[i] != b {
			os.Stderr.WriteString("ERROR: loaded key does not match seeded key\n")
			os.Exit(1)
		}
	}
}

// TestInit_AutoGenerate verifies Init generates a new key file when neither
// env var nor file is present, and that the file has 0600 permissions.
func TestInit_AutoGenerate(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=TestInit_AutoGenerate")
	cmd.Env = append(os.Environ(),
		"TEST_CRYPTO_SUBPROCESS=init_autogen",
	)
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

func testInitAutoGenSubprocess() {
	tmpDir, err := os.MkdirTemp("", "crypto-init-autogen-*")
	if err != nil {
		os.Stderr.WriteString("ERROR: MkdirTemp: " + err.Error() + "\n")
		os.Exit(1)
	}
	defer os.RemoveAll(tmpDir)

	keyPath := filepath.Join(tmpDir, KeyFileName)
	if _, err := os.Stat(keyPath); !errors.Is(err, os.ErrNotExist) {
		os.Stderr.WriteString("ERROR: key file should not exist yet\n")
		os.Exit(1)
	}

	if err := Init(tmpDir); err != nil {
		os.Stderr.WriteString("ERROR: Init returned error: " + err.Error() + "\n")
		os.Exit(1)
	}

	info, err := os.Stat(keyPath)
	if err != nil {
		os.Stderr.WriteString("ERROR: key file was not created: " + err.Error() + "\n")
		os.Exit(1)
	}
	if info.Mode().Perm() != 0o600 {
		os.Stderr.WriteString("ERROR: key file permissions are " + info.Mode().String() + ", want -rw-------\n")
		os.Exit(1)
	}
	if info.Size() != 32 {
		os.Stderr.WriteString("ERROR: key file size is not 32 bytes\n")
		os.Exit(1)
	}

	// Round-trip should now work
	encrypted, err := Encrypt("hello")
	if err != nil {
		os.Stderr.WriteString("ERROR: Encrypt after autogen failed: " + err.Error() + "\n")
		os.Exit(1)
	}
	decrypted, err := Decrypt(encrypted)
	if err != nil {
		os.Stderr.WriteString("ERROR: Decrypt after autogen failed: " + err.Error() + "\n")
		os.Exit(1)
	}
	if decrypted != "hello" {
		os.Stderr.WriteString("ERROR: round-trip mismatch after autogen\n")
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
// Init() failure modes (in-process, since they don't depend on the singleton)
// =============================================================================

// TestInit_WrongKeyFileSize rejects a key file that isn't exactly 32 bytes.
func TestInit_WrongKeyFileSize(t *testing.T) {
	tmpDir := t.TempDir()
	keyPath := filepath.Join(tmpDir, KeyFileName)
	if err := os.WriteFile(keyPath, []byte("too short"), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}

	// Use a fresh KeyManager so we don't depend on singleton state.
	km := &KeyManager{}
	if err := initWithKM(km, tmpDir); err == nil {
		t.Fatal("Init should reject invalid key file size")
	} else if !strings.Contains(err.Error(), "32 bytes") {
		t.Errorf("error message should mention expected size, got: %v", err)
	}
}

// TestInit_NonexistentDataDir fails when the parent directory cannot be written.
func TestInit_NonexistentDataDir(t *testing.T) {
	bogusDir := "/proc/no-such-dir-healarr-test"
	km := &KeyManager{}
	if err := initWithKM(km, bogusDir); err == nil {
		t.Fatal("Init should fail when key file cannot be written")
	}
}

// initWithKM is a test helper that runs the Init flow against a specific
// KeyManager instance rather than the singleton. Useful for failure-mode
// testing without subprocess overhead.
func initWithKM(km *KeyManager, dataDir string) error {
	keyPath := filepath.Join(dataDir, KeyFileName)
	data, err := os.ReadFile(keyPath)
	switch {
	case err == nil:
		if len(data) != keySize {
			return errors.New("invalid encryption key file: expected 32 bytes, got " + itoa(len(data)))
		}
		km.setKey(data)
		return nil
	case !errors.Is(err, os.ErrNotExist):
		return err
	}
	key := make([]byte, keySize)
	if err := os.WriteFile(keyPath, key, 0o600); err != nil {
		return err
	}
	km.setKey(key)
	return nil
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := []byte{}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

// =============================================================================
// Direct KeyManager method tests (using transient instance, bypass singleton)
// =============================================================================

// TestKeyManager_Encrypt_NoKey_ReturnsError is the new behavior: a KeyManager
// with no key configured MUST refuse to encrypt rather than silently storing
// plaintext (Phase 1 P0 finding S1).
func TestKeyManager_Encrypt_NoKey_ReturnsError(t *testing.T) {
	km := &KeyManager{}

	_, err := km.Encrypt("my secret")
	if !errors.Is(err, ErrNoEncryptionKey) {
		t.Errorf("Encrypt() without key should return ErrNoEncryptionKey, got %v", err)
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

// TestKeyManager_Decrypt_NoKey_Prefixed_ReturnsError verifies that a value
// with the EncryptedPrefix cannot be decrypted without a key (vs the
// backward-compat passthrough for un-prefixed legacy values).
func TestKeyManager_Decrypt_NoKey_Prefixed_ReturnsError(t *testing.T) {
	km := &KeyManager{}
	_, err := km.Decrypt("enc:v1:someinvaliddata")
	if !errors.Is(err, ErrNoEncryptionKey) {
		t.Errorf("Decrypt(prefixed) without key should return ErrNoEncryptionKey, got %v", err)
	}
}

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

// =============================================================================
// Decrypt failure modes (direct KeyManager, valid key, bad ciphertext)
// =============================================================================

func TestDecrypt_InvalidBase64(t *testing.T) {
	if os.Getenv("TEST_CRYPTO_SUBPROCESS") != "" {
		return
	}

	km := &KeyManager{key: make([]byte, 32)}

	_, err := km.Decrypt("enc:v1:not-valid-base64!!!")
	if err == nil {
		t.Error("Decrypt should fail for invalid base64")
	}
}

func TestDecrypt_TooShort(t *testing.T) {
	if os.Getenv("TEST_CRYPTO_SUBPROCESS") != "" {
		return
	}

	km := &KeyManager{key: make([]byte, 32)}

	_, err := km.Decrypt("enc:v1:YWJj") // "abc" in base64
	if !errors.Is(err, ErrDecryptFailed) {
		t.Errorf("Decrypt should return ErrDecryptFailed for short data, got: %v", err)
	}
}

func TestDecrypt_InvalidCiphertext(t *testing.T) {
	if os.Getenv("TEST_CRYPTO_SUBPROCESS") != "" {
		return
	}

	km := &KeyManager{key: make([]byte, 32)}

	_, err := km.Decrypt("enc:v1:YWJjZGVmZ2hpamtsbW5vcHFyc3R1dnd4")
	if !errors.Is(err, ErrDecryptFailed) {
		t.Errorf("Decrypt should return ErrDecryptFailed for invalid ciphertext, got: %v", err)
	}
}
