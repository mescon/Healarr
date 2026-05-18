package network

import (
	"errors"
	"net"
	"strings"
	"testing"
)

func TestValidateDestination_NoBlockFlag_AllowsPrivate(t *testing.T) {
	// Default: HEALARR_BLOCK_PRIVATE_TARGETS unset → private hosts pass.
	t.Setenv("HEALARR_BLOCK_PRIVATE_TARGETS", "")

	cases := []string{
		"http://192.168.1.10:8989",
		"http://10.0.0.5",
		"http://127.0.0.1:3000",
		"https://example.com",
	}
	for _, u := range cases {
		if err := ValidateDestination(u); err != nil {
			t.Errorf("ValidateDestination(%q) = %v, want nil with block flag unset", u, err)
		}
	}
}

func TestValidateDestination_BadURL(t *testing.T) {
	// Malformed URLs are always rejected regardless of flag.
	cases := []string{
		"not a url",
		"",
		"http://",
	}
	for _, u := range cases {
		if err := ValidateDestination(u); err == nil {
			t.Errorf("ValidateDestination(%q) = nil, want error", u)
		}
	}
}

func TestValidateDestination_BlockFlag_RejectsPrivate(t *testing.T) {
	t.Setenv("HEALARR_BLOCK_PRIVATE_TARGETS", "true")

	// IP literals: deterministic, no DNS dependency.
	cases := []string{
		"http://192.168.1.1",     // RFC1918
		"http://10.0.0.1",        // RFC1918
		"http://172.16.0.1",      // RFC1918
		"http://127.0.0.1",       // loopback
		"http://169.254.169.254", // cloud metadata
		"http://0.0.0.0",         // unspecified
		"http://[::1]",           // IPv6 loopback
		"http://[fe80::1]",       // IPv6 link-local
	}
	for _, u := range cases {
		err := ValidateDestination(u)
		if err == nil {
			t.Errorf("ValidateDestination(%q) = nil, want ErrPrivateDestination", u)
			continue
		}
		if !errors.Is(err, ErrPrivateDestination) {
			t.Errorf("ValidateDestination(%q) error = %v, want ErrPrivateDestination", u, err)
		}
	}
}

func TestValidateDestination_BlockFlag_AllowsPublic(t *testing.T) {
	t.Setenv("HEALARR_BLOCK_PRIVATE_TARGETS", "true")

	// Public IPs (Cloudflare, Google DNS) — known to be public.
	cases := []string{
		"http://1.1.1.1",
		"http://8.8.8.8:80",
	}
	for _, u := range cases {
		if err := ValidateDestination(u); err != nil {
			t.Errorf("ValidateDestination(%q) = %v, want nil for public IP", u, err)
		}
	}
}

func TestValidateDestination_BlockFlag_UnresolvableHost(t *testing.T) {
	t.Setenv("HEALARR_BLOCK_PRIVATE_TARGETS", "true")

	err := ValidateDestination("http://this-host-does-not-exist-for-sure.invalid")
	if err == nil {
		t.Fatal("ValidateDestination on unresolvable host = nil, want error")
	}
	if !errors.Is(err, ErrCannotResolveHost) {
		t.Errorf("error = %v, want ErrCannotResolveHost wrapped", err)
	}
}

func TestIsPrivateOrInternal(t *testing.T) {
	cases := []struct {
		ip      string
		private bool
	}{
		{"127.0.0.1", true},
		{"10.0.0.1", true},
		{"172.16.0.1", true},
		{"192.168.1.1", true},
		{"169.254.169.254", true}, // cloud metadata
		{"0.0.0.0", true},
		{"::1", true},
		{"fe80::1", true},
		{"1.1.1.1", false},
		{"8.8.8.8", false},
		{"93.184.215.14", false}, // example.com
	}
	for _, tc := range cases {
		ip := net.ParseIP(tc.ip)
		if ip == nil {
			t.Fatalf("ParseIP(%q) returned nil", tc.ip)
		}
		got := isPrivateOrInternal(ip)
		if got != tc.private {
			t.Errorf("isPrivateOrInternal(%s) = %v, want %v", tc.ip, got, tc.private)
		}
	}
}

func TestValidateDestination_BlockFlag_NoHost(t *testing.T) {
	t.Setenv("HEALARR_BLOCK_PRIVATE_TARGETS", "true")
	if err := ValidateDestination("file:///etc/passwd"); err == nil {
		t.Error("ValidateDestination(file:///...) should error on missing host")
	} else if !strings.Contains(err.Error(), "host") {
		t.Errorf("error should mention host, got: %v", err)
	}
}
