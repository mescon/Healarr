// Package pathutil holds the shared path-matching semantics used everywhere
// Healarr compares a file path against a configured root: the path mapper,
// the *arr instance-ownership matcher, the scanner's per-file config lookup,
// and the remediator's consent resolution.
//
// The rules exist as ONE package because the same separator bug was fixed
// three times in three sibling matchers (#298, #305, #322): each had its own
// slightly different prefix/boundary logic, and each missed Windows
// backslash paths in its own way. Any new code that needs "is this file
// under this root?" must use IsWithinRoot rather than rolling its own
// strings.HasPrefix.
package pathutil

import "strings"

// Separators are the path separators we recognize. Forward slash covers
// Linux/macOS and *arr's normalized form on most setups; backslash covers
// Windows-native paths and UNC roots (\\server\share\folder) that
// Sonarr/Radarr report verbatim when running on Windows.
const Separators = "/\\"

// TrimTrailingSep removes trailing path separators (both / and \) so a
// configured root compares the same whether the operator typed a trailing
// slash or not. Safe for UNC paths: it only strips trailing characters,
// never the leading \\ anchor.
func TrimTrailingSep(p string) string {
	return strings.TrimRight(p, Separators)
}

// HasSepPrefix reports whether s begins with a recognized path separator.
// Used after a prefix match to confirm the match ends on a directory
// boundary, so /media/TV does not match /media/TV2 and \\srv\Movies does
// not match \\srv\MoviesArchive.
func HasSepPrefix(s string) bool {
	if s == "" {
		return false
	}
	return strings.ContainsRune(Separators, rune(s[0]))
}

// IsWithinRoot reports whether path is the configured root itself or a file
// or directory inside it, with separator-agnostic boundary semantics. The
// root is trailing-separator-trimmed before comparison so stored configs
// like "/media/tv/" match the same as "/media/tv".
func IsWithinRoot(root, path string) bool {
	root = TrimTrailingSep(root)
	if root == "" || !strings.HasPrefix(path, root) {
		return false
	}
	remainder := path[len(root):]
	return remainder == "" || HasSepPrefix(remainder)
}

// MatchedRootLen returns the comparable length of a root for
// longest-prefix-wins selection among multiple matching roots, using the
// same trailing-separator trim as IsWithinRoot so a root with a trailing
// slash does not outrank the same root without one.
func MatchedRootLen(root string) int {
	return len(TrimTrailingSep(root))
}
