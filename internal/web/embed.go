package web

import (
	"embed"
	"io/fs"
	"net/http"
)

// WebFS holds the embedded web assets.
// This will be populated at build time if the web/ directory exists.
// The //go:embed directive is in embed_prod.go (with build tag)
// For development, embed_dev.go provides a no-op that uses filesystem.

// GetFS returns the filesystem to use for serving web assets.
// In production builds (with embedded assets), this returns the embedded FS.
// In development, it returns nil and the caller should fall back to disk.
func GetFS() fs.FS {
	return getEmbeddedFS()
}

// GetHTTPFS returns an http.FileSystem for use with http.FileServer.
// Returns nil if no embedded assets are available.
func GetHTTPFS() http.FileSystem {
	efs := GetFS()
	if efs == nil {
		return nil
	}
	return http.FS(efs)
}

// HasEmbeddedAssets returns true if web assets are embedded in the binary.
func HasEmbeddedAssets() bool {
	efs := GetFS()
	if efs == nil {
		return false
	}
	// Verify index.html exists in the embedded filesystem
	_, err := fs.Stat(efs, "index.html")
	return err == nil
}

// ListEmbeddedFiles returns a list of all embedded files for debugging.
// Returns nil if no embedded assets are available.
func ListEmbeddedFiles() []string {
	if !hasEmbedded {
		return nil
	}

	var files []string
	fs.WalkDir(embeddedFS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() {
			files = append(files, path)
		}
		return nil
	})
	return files
}

// embeddedFS is set by the platform-specific files
var embeddedFS embed.FS
var hasEmbedded bool

// cachedSubFS caches the sub-filesystem to avoid repeated fs.Sub calls
var cachedSubFS fs.FS
var subFSInitialized bool

func getEmbeddedFS() fs.FS {
	if !hasEmbedded {
		return nil
	}

	// Return cached sub-filesystem if already initialized
	if subFSInitialized {
		return cachedSubFS
	}

	// The embed directive "//go:embed all:web" creates paths like "web/index.html"
	// We need to return a sub-filesystem rooted at "web" so callers can access "index.html" directly
	sub, err := fs.Sub(embeddedFS, "web")
	if err != nil {
		subFSInitialized = true
		cachedSubFS = nil
		return nil
	}

	subFSInitialized = true
	cachedSubFS = sub
	return sub
}
