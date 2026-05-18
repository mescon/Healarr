// Package network provides outbound-request safety helpers, primarily
// SSRF (Server-Side Request Forgery) protection for user-supplied URLs.
package network

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
)

// ErrPrivateDestination is returned when ValidateDestination is configured
// to block private/internal addresses and the resolved host is one.
var ErrPrivateDestination = errors.New("destination resolves to a private or internal address")

// ErrCannotResolveHost is returned when DNS resolution fails for the host.
var ErrCannotResolveHost = errors.New("cannot resolve destination host")

// envBlockPrivate enables strict SSRF protection: any RFC1918, loopback,
// link-local, multicast, or unspecified destination is refused.
//
// Default (unset): only enforce that the URL parses and has a host. This
// preserves the typical Healarr deployment where *arr services live on the
// same private LAN as Healarr (e.g., Sonarr at 192.168.1.10).
const envBlockPrivate = "HEALARR_BLOCK_PRIVATE_TARGETS"

// ValidateDestination checks that rawURL's host is safe to send authenticated
// requests to. When HEALARR_BLOCK_PRIVATE_TARGETS=true, all resolved IPs are
// checked against private/loopback/link-local/multicast ranges and the call
// fails if any match.
//
// When the env var is unset, only basic URL well-formedness is enforced and
// any host (private or public) is accepted. This is the homelab-friendly
// default; operators in stricter environments should set the flag.
func ValidateDestination(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("parse URL: %w", err)
	}

	host := parsed.Hostname()
	if host == "" {
		return errors.New("URL has no host")
	}

	if os.Getenv(envBlockPrivate) != "true" {
		return nil
	}

	ips, err := net.LookupIP(host)
	if err != nil || len(ips) == 0 {
		return fmt.Errorf("%w: %s", ErrCannotResolveHost, host)
	}

	for _, ip := range ips {
		if isPrivateOrInternal(ip) {
			return fmt.Errorf("%w: %s resolves to %s", ErrPrivateDestination, host, ip)
		}
	}

	return nil
}

// isPrivateOrInternal returns true for any IP that an attacker should not be
// able to direct authenticated outbound requests to: RFC1918 ranges,
// loopback, link-local (including the cloud metadata address 169.254.169.254),
// multicast, and the unspecified address.
func isPrivateOrInternal(ip net.IP) bool {
	return ip.IsLoopback() ||
		ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() ||
		ip.IsMulticast() ||
		ip.IsUnspecified()
}
