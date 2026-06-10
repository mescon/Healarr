package services

import "testing"

// TestScannerService_ScanOverlapsActive pins the boundary-aware overlap
// check: the old exact-compare IsPathBeingScanned guard let /media and
// /media/TV scan concurrently over the same files (duplicate scan_files
// rows, duplicate corruption journeys), while naive prefix matching would
// wrongly conflict /media/TV with /media/TV2.
func TestScannerService_ScanOverlapsActive(t *testing.T) {
	t.Parallel()
	s := &ScannerService{activeScans: map[string]*ScanProgress{
		"scan-1": {ID: "scan-1", Type: "path", Path: "/media/TV"},
		"scan-2": {ID: "scan-2", Type: "file", Path: "/downloads/movie.mkv"},
	}}

	cases := []struct {
		name         string
		path         string
		wantConflict string
		wantOverlap  bool
	}{
		{"exact match", "/media/TV", "/media/TV", true},
		{"descendant of active scan", "/media/TV/Show S01", "/media/TV", true},
		{"ancestor of active scan", "/media", "/media/TV", true},
		{"trailing separator", "/media/TV/", "/media/TV", true},
		{"sibling sharing name prefix", "/media/TV2", "", false},
		{"unrelated path", "/data", "", false},
		{"file scans never block path scans", "/downloads/movie.mkv", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			conflict, overlaps := s.ScanOverlapsActive(tc.path)
			if overlaps != tc.wantOverlap || conflict != tc.wantConflict {
				t.Errorf("ScanOverlapsActive(%q) = (%q, %v), want (%q, %v)",
					tc.path, conflict, overlaps, tc.wantConflict, tc.wantOverlap)
			}
		})
	}
}
