package api

import (
	"strings"
	"testing"
)

func TestValidateDetectionArgs_Empty(t *testing.T) {
	if err := validateDetectionArgs(nil); err != nil {
		t.Errorf("nil args should be valid, got %v", err)
	}
	if err := validateDetectionArgs([]string{}); err != nil {
		t.Errorf("empty args should be valid, got %v", err)
	}
}

func TestValidateDetectionArgs_AllowedFlags(t *testing.T) {
	cases := [][]string{
		{"-threads", "4"},
		{"-probesize", "1000000"},
		{"-analyzeduration", "10000000"},
		{"-fpsprobesize", "50"},
		{"-loglevel", "warning"},
		{"-v", "error"},
		{"-hide_banner"},
		{"-nostats"},
		{"-threads", "2", "-probesize", "5000000", "-hide_banner"}, // combined
	}
	for _, args := range cases {
		if err := validateDetectionArgs(args); err != nil {
			t.Errorf("validateDetectionArgs(%v) = %v, want nil", args, err)
		}
	}
}

func TestValidateDetectionArgs_RejectsDisallowedFlags(t *testing.T) {
	cases := [][]string{
		{"-i", "/etc/passwd"},          // additional input
		{"-f", "data"},                 // output format change
		{"-protocol_whitelist", "all"}, // bypass protocol restriction
		{"-y"},                         // overwrite flag
		{"-c:v", "libx264"},            // codec flag
		{"--something"},                // long form
		{"-unknown_flag"},              // anything not in allowlist
	}
	for _, args := range cases {
		err := validateDetectionArgs(args)
		if err == nil {
			t.Errorf("validateDetectionArgs(%v) = nil, want rejection", args)
			continue
		}
		if !strings.Contains(err.Error(), "not permitted") {
			t.Errorf("validateDetectionArgs(%v) error = %v, expected 'not permitted'", args, err)
		}
	}
}

func TestValidateDetectionArgs_RejectsURLs(t *testing.T) {
	cases := [][]string{
		{"-threads", "http://attacker.example/x"},
		{"-threads", "https://evil.local/y"},
		{"http://payload-as-flag-value"},
		{"-i", "https://x"}, // both -i and URL — should fail on -i first
	}
	for _, args := range cases {
		err := validateDetectionArgs(args)
		if err == nil {
			t.Errorf("validateDetectionArgs(%v) = nil, want rejection", args)
			continue
		}
		// Either "not permitted" (flag rejected first) or "URL" / "URI" (URL rejected first)
		msg := err.Error()
		if !strings.Contains(msg, "URLs are not permitted") &&
			!strings.Contains(msg, "not permitted") {
			t.Errorf("validateDetectionArgs(%v) error = %v, expected URL or flag rejection", args, err)
		}
	}
}

func TestValidateDetectionArgs_RejectsFileURI(t *testing.T) {
	cases := [][]string{
		{"-threads", "file:/etc/passwd"},
		{"-threads", "FILE:/etc/passwd"}, // case insensitive
		{"file:/etc/shadow"},
	}
	for _, args := range cases {
		err := validateDetectionArgs(args)
		if err == nil {
			t.Errorf("validateDetectionArgs(%v) = nil, want rejection", args)
			continue
		}
		if !strings.Contains(err.Error(), "file:") && !strings.Contains(err.Error(), "URLs") {
			t.Errorf("validateDetectionArgs(%v) error = %v, expected file: rejection", args, err)
		}
	}
}

func TestValidateDetectionArgs_RejectsPipe(t *testing.T) {
	cases := [][]string{
		{"-threads", "pipe:0"},
		{"pipe:1"},
	}
	for _, args := range cases {
		err := validateDetectionArgs(args)
		if err == nil {
			t.Errorf("validateDetectionArgs(%v) = nil, want rejection", args)
			continue
		}
		if !strings.Contains(err.Error(), "pipe:") {
			t.Errorf("validateDetectionArgs(%v) error = %v, expected pipe: rejection", args, err)
		}
	}
}

func TestValidateDetectionArgs_RejectsEmptyString(t *testing.T) {
	err := validateDetectionArgs([]string{"-threads", ""})
	if err == nil {
		t.Fatal("empty string in args should be rejected")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("error = %v, expected 'empty' in message", err)
	}
}

func TestAllowedFlagsList_StableAndComplete(t *testing.T) {
	got := allowedFlagsList()
	// Should be sorted alphabetically and include every entry in the map
	for flag := range allowedDetectionFlags {
		if !strings.Contains(got, flag) {
			t.Errorf("allowedFlagsList() missing %q; got %q", flag, got)
		}
	}
	// Sorted check: split and verify increasing order
	parts := strings.Split(got, ", ")
	for i := 1; i < len(parts); i++ {
		if parts[i-1] > parts[i] {
			t.Errorf("allowedFlagsList() not sorted: %q comes before %q", parts[i-1], parts[i])
		}
	}
}
