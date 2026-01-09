package integration

import "testing"

func TestFormatDisplayTitle(t *testing.T) {
	tests := []struct {
		name     string
		details  *MediaDetails
		expected string
	}{
		{
			name:     "nil media details",
			details:  nil,
			expected: "",
		},
		{
			name: "TV show with season and episode",
			details: &MediaDetails{
				Title:         "Colony",
				MediaType:     "series",
				SeasonNumber:  1,
				EpisodeNumber: 8,
			},
			expected: "Colony S01E08",
		},
		{
			name: "TV show with double-digit season and episode",
			details: &MediaDetails{
				Title:         "Supernatural",
				MediaType:     "series",
				SeasonNumber:  15,
				EpisodeNumber: 20,
			},
			expected: "Supernatural S15E20",
		},
		{
			name: "Movie with year",
			details: &MediaDetails{
				Title: "The Matrix",
				Year:  1999,
			},
			expected: "The Matrix (1999)",
		},
		{
			name: "Movie without year",
			details: &MediaDetails{
				Title: "Unknown Movie",
			},
			expected: "Unknown Movie",
		},
		{
			name: "TV series without episode info but with year",
			details: &MediaDetails{
				Title:     "Breaking Bad",
				MediaType: "series",
				Year:      2008,
			},
			expected: "Breaking Bad (2008)",
		},
		{
			name: "Series type with only season (no episode)",
			details: &MediaDetails{
				Title:        "Game of Thrones",
				MediaType:    "series",
				SeasonNumber: 1,
			},
			expected: "Game of Thrones",
		},
		{
			name: "Series type with only episode (no season)",
			details: &MediaDetails{
				Title:         "Westworld",
				MediaType:     "series",
				EpisodeNumber: 5,
			},
			expected: "Westworld",
		},
		{
			name: "Movie type ignores season/episode fields",
			details: &MediaDetails{
				Title:         "Inception",
				MediaType:     "movie",
				SeasonNumber:  1,
				EpisodeNumber: 1,
				Year:          2010,
			},
			expected: "Inception (2010)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.details.FormatDisplayTitle()
			if result != tt.expected {
				t.Errorf("FormatDisplayTitle() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestPadZero(t *testing.T) {
	tests := []struct {
		input    int
		expected string
	}{
		{0, "00"},
		{1, "01"},
		{5, "05"},
		{9, "09"},
		{10, "10"},
		{15, "15"},
		{99, "99"},
		{100, "100"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			result := padZero(tt.input)
			if result != tt.expected {
				t.Errorf("padZero(%d) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestItoa(t *testing.T) {
	tests := []struct {
		input    int
		expected string
	}{
		{0, "0"},
		{1, "1"},
		{9, "9"},
		{10, "10"},
		{42, "42"},
		{100, "100"},
		{999, "999"},
		{1000, "1000"},
		{2024, "2024"},
		{-1, "-1"},
		{-42, "-42"},
		{-999, "-999"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			result := itoa(tt.input)
			if result != tt.expected {
				t.Errorf("itoa(%d) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}
