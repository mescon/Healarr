package pathutil

import "testing"

func TestIsWithinRoot(t *testing.T) {
	tests := []struct {
		name string
		root string
		path string
		want bool
	}{
		// Linux semantics
		{"exact match", "/media/tv", "/media/tv", true},
		{"file inside", "/media/tv", "/media/tv/show/ep.mkv", true},
		{"trailing slash root", "/media/tv/", "/media/tv/show/ep.mkv", true},
		{"trailing slash root exact", "/media/tv/", "/media/tv", true},
		{"sibling prefix", "/media/tv", "/media/tv2/ep.mkv", false},
		{"different tree", "/media/tv", "/data/tv/ep.mkv", false},
		{"empty path", "/media/tv", "", false},
		{"empty root", "", "/media/tv", false},

		// Windows / UNC semantics (the #298/#305/#322 class)
		{"UNC exact", `\\srv\media\TV Shows`, `\\srv\media\TV Shows`, true},
		{"UNC file inside", `\\srv\media\TV Shows`, `\\srv\media\TV Shows\Show\S01E01.mkv`, true},
		{"UNC trailing backslash root", `\\srv\media\TV Shows\`, `\\srv\media\TV Shows\Show\ep.mkv`, true},
		{"UNC sibling prefix", `\\srv\media\Movies`, `\\srv\media\MoviesArchive\f.mkv`, false},
		{"drive letter inside", `D:\Media\TV`, `D:\Media\TV\Show\ep.mkv`, true},
		{"drive letter sibling", `D:\Media\TV`, `D:\Media\TV2\ep.mkv`, false},

		// Mixed separators after the root (a Windows *arr remainder on a
		// forward-slash root) still counts as a boundary.
		{"mixed separator boundary", "/media/Movies", `/media/Movies\Film (2024)\f.mkv`, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsWithinRoot(tt.root, tt.path); got != tt.want {
				t.Errorf("IsWithinRoot(%q, %q) = %v, want %v", tt.root, tt.path, got, tt.want)
			}
		})
	}
}

func TestMatchedRootLen(t *testing.T) {
	if MatchedRootLen("/media/tv/") != MatchedRootLen("/media/tv") {
		t.Error("trailing slash must not change comparable root length")
	}
	if MatchedRootLen(`\\srv\share\`) != MatchedRootLen(`\\srv\share`) {
		t.Error("trailing backslash must not change comparable root length")
	}
}
