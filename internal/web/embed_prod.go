//go:build embed_web

package web

import "embed"

// This file is only included when building with -tags embed_web
// The web/ directory must exist at build time with the frontend assets.

// Use "all:web" to embed everything including hidden files and subdirectories
//
//go:embed all:web
var prodFS embed.FS

func init() {
	embeddedFS = prodFS
	hasEmbedded = true
}
