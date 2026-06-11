package integration

import (
	"os"
	"path/filepath"
	"testing"
)

// TestProbeTimeoutFor pins the size-aware probe budget: large 4K-class files
// get a longer ffprobe timeout so a saturated NAS doesn't make every scan
// silently skip their content analysis, while ordinary files keep the short
// timeout so a hung probe can't stall a scan worker for minutes.
func TestProbeTimeoutFor(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	smallFile := filepath.Join(dir, "episode-1080p.mkv")
	if err := os.WriteFile(smallFile, []byte("x"), 0o644); err != nil {
		t.Fatalf("write small file: %v", err)
	}

	// Sparse file: reports 5 GiB without writing it.
	largeFile := filepath.Join(dir, "episode-2160p.mkv")
	f, err := os.Create(largeFile)
	if err != nil {
		t.Fatalf("create large file: %v", err)
	}
	if err := f.Truncate(5 << 30); err != nil {
		f.Close()
		t.Skipf("cannot create sparse 5 GiB file on this filesystem: %v", err)
	}
	f.Close()

	if got := probeTimeoutFor(smallFile); got != probeTimeoutDefault {
		t.Errorf("small file timeout = %v, want %v", got, probeTimeoutDefault)
	}
	if got := probeTimeoutFor(largeFile); got != probeTimeoutLargeFile {
		t.Errorf("large file timeout = %v, want %v", got, probeTimeoutLargeFile)
	}
	if got := probeTimeoutFor(filepath.Join(dir, "missing.mkv")); got != probeTimeoutDefault {
		t.Errorf("missing file timeout = %v, want default %v (probe surfaces the real error)", got, probeTimeoutDefault)
	}
}
