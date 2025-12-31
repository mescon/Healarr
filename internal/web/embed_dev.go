//go:build !embed_web

package web

// This file is included for development builds (without -tags embed_web).
// Web assets are served from disk, allowing hot-reload during development.

func init() {
	hasEmbedded = false
}
