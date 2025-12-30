package auth

import (
	"encoding/base64"
	"strings"
	"testing"
)

// =============================================================================
// GenerateAPIKey tests
// =============================================================================

func TestGenerateAPIKey_ReturnsValidBase64(t *testing.T) {
	key, err := GenerateAPIKey()
	if err != nil {
		t.Fatalf("GenerateAPIKey() error = %v", err)
	}

	// Should be valid base64url encoded
	decoded, err := base64.URLEncoding.DecodeString(key)
	if err != nil {
		t.Errorf("GenerateAPIKey() returned invalid base64url: %v", err)
	}

	// Should decode to 32 bytes
	if len(decoded) != 32 {
		t.Errorf("GenerateAPIKey() decoded length = %d, want 32", len(decoded))
	}
}

func TestGenerateAPIKey_Length(t *testing.T) {
	key, err := GenerateAPIKey()
	if err != nil {
		t.Fatalf("GenerateAPIKey() error = %v", err)
	}

	// Base64 encoding: 32 bytes -> 44 characters (with padding)
	expectedLen := 44
	if len(key) != expectedLen {
		t.Errorf("GenerateAPIKey() length = %d, want %d", len(key), expectedLen)
	}
}

func TestGenerateAPIKey_Uniqueness(t *testing.T) {
	keys := make(map[string]bool)
	const iterations = 100

	for i := 0; i < iterations; i++ {
		key, err := GenerateAPIKey()
		if err != nil {
			t.Fatalf("GenerateAPIKey() iteration %d error = %v", i, err)
		}

		if keys[key] {
			t.Errorf("GenerateAPIKey() produced duplicate key on iteration %d", i)
		}
		keys[key] = true
	}
}

func TestGenerateAPIKey_URLSafe(t *testing.T) {
	key, err := GenerateAPIKey()
	if err != nil {
		t.Fatalf("GenerateAPIKey() error = %v", err)
	}

	// Should not contain + or / (standard base64 chars not in URL safe variant)
	if strings.ContainsAny(key, "+/") {
		t.Errorf("GenerateAPIKey() contains non-URL-safe characters: %s", key)
	}
}

// =============================================================================
// HashPassword tests
// =============================================================================

func TestHashPassword_ReturnsValidHash(t *testing.T) {
	hash, err := HashPassword("mypassword")
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}

	// bcrypt hashes start with $2a$, $2b$, or $2y$
	if !strings.HasPrefix(hash, "$2") {
		t.Errorf("HashPassword() returned non-bcrypt hash: %s", hash)
	}
}

func TestHashPassword_DifferentSalts(t *testing.T) {
	password := "samepassword"

	hash1, err := HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword() first call error = %v", err)
	}

	hash2, err := HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword() second call error = %v", err)
	}

	// Same password should produce different hashes (different salts)
	if hash1 == hash2 {
		t.Error("HashPassword() should produce different hashes for same password (random salt)")
	}
}

func TestHashPassword_EmptyPassword(t *testing.T) {
	hash, err := HashPassword("")
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}

	if hash == "" {
		t.Error("HashPassword() should return hash even for empty password")
	}
}

func TestHashPassword_LongPassword(t *testing.T) {
	// bcrypt has a max length of 72 bytes - longer passwords return an error
	longPassword := strings.Repeat("a", 100)

	_, err := HashPassword(longPassword)
	if err == nil {
		t.Error("HashPassword() should return error for passwords over 72 bytes")
	}

	// Password exactly at 72 bytes should work
	maxPassword := strings.Repeat("a", 72)
	hash, err := HashPassword(maxPassword)
	if err != nil {
		t.Errorf("HashPassword() should accept 72-byte passwords: %v", err)
	}
	if hash == "" {
		t.Error("HashPassword() should return hash for 72-byte password")
	}
}

func TestHashPassword_SpecialCharacters(t *testing.T) {
	specialPassword := "p@$$w0rd!#$%^&*()"

	hash, err := HashPassword(specialPassword)
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}

	if hash == "" {
		t.Error("HashPassword() should handle special characters")
	}
}

// =============================================================================
// CheckPasswordHash tests
// =============================================================================

func TestCheckPasswordHash_CorrectPassword(t *testing.T) {
	password := "correctpassword"

	hash, err := HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}

	if !CheckPasswordHash(password, hash) {
		t.Error("CheckPasswordHash() should return true for correct password")
	}
}

func TestCheckPasswordHash_IncorrectPassword(t *testing.T) {
	password := "correctpassword"
	wrongPassword := "wrongpassword"

	hash, err := HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}

	if CheckPasswordHash(wrongPassword, hash) {
		t.Error("CheckPasswordHash() should return false for incorrect password")
	}
}

func TestCheckPasswordHash_EmptyPassword(t *testing.T) {
	hash, err := HashPassword("")
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}

	if !CheckPasswordHash("", hash) {
		t.Error("CheckPasswordHash() should verify empty password correctly")
	}

	if CheckPasswordHash("notempty", hash) {
		t.Error("CheckPasswordHash() should reject non-empty when empty was hashed")
	}
}

func TestCheckPasswordHash_InvalidHash(t *testing.T) {
	// Should return false for invalid hash format
	if CheckPasswordHash("anypassword", "invalid-hash") {
		t.Error("CheckPasswordHash() should return false for invalid hash format")
	}
}

func TestCheckPasswordHash_CaseSensitive(t *testing.T) {
	password := "CaseSensitive"

	hash, err := HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}

	if CheckPasswordHash("casesensitive", hash) {
		t.Error("CheckPasswordHash() should be case-sensitive")
	}

	if CheckPasswordHash("CASESENSITIVE", hash) {
		t.Error("CheckPasswordHash() should be case-sensitive")
	}
}

// =============================================================================
// Integration tests
// =============================================================================

func TestHashAndVerify_RoundTrip(t *testing.T) {
	passwords := []string{
		"simple",
		"P@$$w0rd!",
		"unicode: 日本語",
		"with spaces",
		strings.Repeat("a", 72), // Max bcrypt length
	}

	for _, password := range passwords {
		t.Run(password[:min(len(password), 20)], func(t *testing.T) {
			hash, err := HashPassword(password)
			if err != nil {
				t.Fatalf("HashPassword() error = %v", err)
			}

			if !CheckPasswordHash(password, hash) {
				t.Error("Round-trip verification failed")
			}
		})
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
