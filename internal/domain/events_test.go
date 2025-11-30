package domain

import (
	"testing"
)

// TestEvent_GetString tests the GetString accessor method.
func TestEvent_GetString(t *testing.T) {
	tests := []struct {
		name      string
		eventData map[string]interface{}
		key       string
		wantValue string
		wantOk    bool
	}{
		{
			name:      "existing string key",
			eventData: map[string]interface{}{"file_path": "/media/movies/test.mkv"},
			key:       "file_path",
			wantValue: "/media/movies/test.mkv",
			wantOk:    true,
		},
		{
			name:      "missing key",
			eventData: map[string]interface{}{"other": "value"},
			key:       "file_path",
			wantValue: "",
			wantOk:    false,
		},
		{
			name:      "nil event data",
			eventData: nil,
			key:       "file_path",
			wantValue: "",
			wantOk:    false,
		},
		{
			name:      "wrong type",
			eventData: map[string]interface{}{"count": 123},
			key:       "count",
			wantValue: "",
			wantOk:    false,
		},
		{
			name:      "empty string",
			eventData: map[string]interface{}{"empty": ""},
			key:       "empty",
			wantValue: "",
			wantOk:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := &Event{EventData: tt.eventData}
			got, ok := e.GetString(tt.key)
			if got != tt.wantValue || ok != tt.wantOk {
				t.Errorf("GetString(%q) = (%q, %v), want (%q, %v)", tt.key, got, ok, tt.wantValue, tt.wantOk)
			}
		})
	}
}

// TestEvent_GetStringOr tests the GetStringOr accessor method.
func TestEvent_GetStringOr(t *testing.T) {
	tests := []struct {
		name       string
		eventData  map[string]interface{}
		key        string
		defaultVal string
		want       string
	}{
		{
			name:       "existing key returns value",
			eventData:  map[string]interface{}{"name": "test"},
			key:        "name",
			defaultVal: "default",
			want:       "test",
		},
		{
			name:       "missing key returns default",
			eventData:  map[string]interface{}{},
			key:        "name",
			defaultVal: "default",
			want:       "default",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := &Event{EventData: tt.eventData}
			if got := e.GetStringOr(tt.key, tt.defaultVal); got != tt.want {
				t.Errorf("GetStringOr() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestEvent_GetInt64 tests the GetInt64 accessor method.
func TestEvent_GetInt64(t *testing.T) {
	tests := []struct {
		name      string
		eventData map[string]interface{}
		key       string
		wantValue int64
		wantOk    bool
	}{
		{
			name:      "int64 value",
			eventData: map[string]interface{}{"media_id": int64(123)},
			key:       "media_id",
			wantValue: 123,
			wantOk:    true,
		},
		{
			name:      "float64 value (JSON unmarshaling)",
			eventData: map[string]interface{}{"media_id": float64(456)},
			key:       "media_id",
			wantValue: 456,
			wantOk:    true,
		},
		{
			name:      "int value",
			eventData: map[string]interface{}{"media_id": int(789)},
			key:       "media_id",
			wantValue: 789,
			wantOk:    true,
		},
		{
			name:      "missing key",
			eventData: map[string]interface{}{},
			key:       "media_id",
			wantValue: 0,
			wantOk:    false,
		},
		{
			name:      "wrong type",
			eventData: map[string]interface{}{"media_id": "not a number"},
			key:       "media_id",
			wantValue: 0,
			wantOk:    false,
		},
		{
			name:      "nil event data",
			eventData: nil,
			key:       "media_id",
			wantValue: 0,
			wantOk:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := &Event{EventData: tt.eventData}
			got, ok := e.GetInt64(tt.key)
			if got != tt.wantValue || ok != tt.wantOk {
				t.Errorf("GetInt64(%q) = (%d, %v), want (%d, %v)", tt.key, got, ok, tt.wantValue, tt.wantOk)
			}
		})
	}
}

// TestEvent_GetFloat64 tests the GetFloat64 accessor method.
func TestEvent_GetFloat64(t *testing.T) {
	tests := []struct {
		name      string
		eventData map[string]interface{}
		key       string
		wantValue float64
		wantOk    bool
	}{
		{
			name:      "float64 value",
			eventData: map[string]interface{}{"progress": 75.5},
			key:       "progress",
			wantValue: 75.5,
			wantOk:    true,
		},
		{
			name:      "int64 value",
			eventData: map[string]interface{}{"progress": int64(100)},
			key:       "progress",
			wantValue: 100.0,
			wantOk:    true,
		},
		{
			name:      "missing key",
			eventData: map[string]interface{}{},
			key:       "progress",
			wantValue: 0,
			wantOk:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := &Event{EventData: tt.eventData}
			got, ok := e.GetFloat64(tt.key)
			if got != tt.wantValue || ok != tt.wantOk {
				t.Errorf("GetFloat64(%q) = (%f, %v), want (%f, %v)", tt.key, got, ok, tt.wantValue, tt.wantOk)
			}
		})
	}
}

// TestEvent_GetBool tests the GetBool accessor method.
func TestEvent_GetBool(t *testing.T) {
	tests := []struct {
		name      string
		eventData map[string]interface{}
		key       string
		wantValue bool
		wantOk    bool
	}{
		{
			name:      "true value",
			eventData: map[string]interface{}{"auto_remediate": true},
			key:       "auto_remediate",
			wantValue: true,
			wantOk:    true,
		},
		{
			name:      "false value",
			eventData: map[string]interface{}{"dry_run": false},
			key:       "dry_run",
			wantValue: false,
			wantOk:    true,
		},
		{
			name:      "missing key",
			eventData: map[string]interface{}{},
			key:       "auto_remediate",
			wantValue: false,
			wantOk:    false,
		},
		{
			name:      "wrong type",
			eventData: map[string]interface{}{"auto_remediate": "true"},
			key:       "auto_remediate",
			wantValue: false,
			wantOk:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := &Event{EventData: tt.eventData}
			got, ok := e.GetBool(tt.key)
			if got != tt.wantValue || ok != tt.wantOk {
				t.Errorf("GetBool(%q) = (%v, %v), want (%v, %v)", tt.key, got, ok, tt.wantValue, tt.wantOk)
			}
		})
	}
}

// TestEvent_GetMap tests the GetMap accessor method.
func TestEvent_GetMap(t *testing.T) {
	tests := []struct {
		name      string
		eventData map[string]interface{}
		key       string
		wantOk    bool
	}{
		{
			name: "existing map",
			eventData: map[string]interface{}{
				"metadata": map[string]interface{}{"season": 1, "episode": 5},
			},
			key:    "metadata",
			wantOk: true,
		},
		{
			name:      "missing key",
			eventData: map[string]interface{}{},
			key:       "metadata",
			wantOk:    false,
		},
		{
			name:      "wrong type",
			eventData: map[string]interface{}{"metadata": "not a map"},
			key:       "metadata",
			wantOk:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := &Event{EventData: tt.eventData}
			_, ok := e.GetMap(tt.key)
			if ok != tt.wantOk {
				t.Errorf("GetMap(%q) ok = %v, want %v", tt.key, ok, tt.wantOk)
			}
		})
	}
}

// TestEvent_GetStringSlice tests the GetStringSlice accessor method.
func TestEvent_GetStringSlice(t *testing.T) {
	tests := []struct {
		name      string
		eventData map[string]interface{}
		key       string
		wantLen   int
		wantOk    bool
	}{
		{
			name:      "string slice directly",
			eventData: map[string]interface{}{"tags": []string{"movie", "action"}},
			key:       "tags",
			wantLen:   2,
			wantOk:    true,
		},
		{
			name:      "interface slice (JSON unmarshaling)",
			eventData: map[string]interface{}{"tags": []interface{}{"movie", "action", "2024"}},
			key:       "tags",
			wantLen:   3,
			wantOk:    true,
		},
		{
			name:      "missing key",
			eventData: map[string]interface{}{},
			key:       "tags",
			wantLen:   0,
			wantOk:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := &Event{EventData: tt.eventData}
			got, ok := e.GetStringSlice(tt.key)
			if ok != tt.wantOk {
				t.Errorf("GetStringSlice(%q) ok = %v, want %v", tt.key, ok, tt.wantOk)
			}
			if ok && len(got) != tt.wantLen {
				t.Errorf("GetStringSlice(%q) len = %d, want %d", tt.key, len(got), tt.wantLen)
			}
		})
	}
}

// TestEvent_ParseCorruptionEventData tests parsing corruption event data.
func TestEvent_ParseCorruptionEventData(t *testing.T) {
	t.Run("valid corruption event", func(t *testing.T) {
		e := &Event{
			EventType: CorruptionDetected,
			EventData: map[string]interface{}{
				"file_path":       "/media/movies/test.mkv",
				"path_id":         float64(1), // JSON unmarshaling produces float64
				"corruption_type": "CorruptHeader",
				"error_details":   "Invalid EBML header",
				"auto_remediate":  true,
				"dry_run":         false,
			},
		}

		data, ok := e.ParseCorruptionEventData()
		if !ok {
			t.Fatal("ParseCorruptionEventData() returned false for valid event")
		}
		if data.FilePath != "/media/movies/test.mkv" {
			t.Errorf("FilePath = %q, want %q", data.FilePath, "/media/movies/test.mkv")
		}
		if data.PathID != 1 {
			t.Errorf("PathID = %d, want %d", data.PathID, 1)
		}
		if data.CorruptionType != "CorruptHeader" {
			t.Errorf("CorruptionType = %q, want %q", data.CorruptionType, "CorruptHeader")
		}
		if !data.AutoRemediate {
			t.Error("AutoRemediate should be true")
		}
	})

	t.Run("missing file_path", func(t *testing.T) {
		e := &Event{
			EventType: CorruptionDetected,
			EventData: map[string]interface{}{
				"corruption_type": "CorruptHeader",
			},
		}

		_, ok := e.ParseCorruptionEventData()
		if ok {
			t.Error("ParseCorruptionEventData() should return false when file_path is missing")
		}
	})
}

// TestEvent_ParseSearchCompletedEventData tests parsing search completed event data.
func TestEvent_ParseSearchCompletedEventData(t *testing.T) {
	t.Run("valid search completed event", func(t *testing.T) {
		e := &Event{
			EventType: SearchCompleted,
			EventData: map[string]interface{}{
				"file_path": "/media/movies/test.mkv",
				"media_id":  float64(456),
				"path_id":   float64(1),
				"is_retry":  true,
				"metadata":  map[string]interface{}{"title": "Test Movie"},
			},
		}

		data, ok := e.ParseSearchCompletedEventData()
		if !ok {
			t.Fatal("ParseSearchCompletedEventData() returned false for valid event")
		}
		if data.FilePath != "/media/movies/test.mkv" {
			t.Errorf("FilePath = %q, want %q", data.FilePath, "/media/movies/test.mkv")
		}
		if data.MediaID != 456 {
			t.Errorf("MediaID = %d, want %d", data.MediaID, 456)
		}
		if !data.IsRetry {
			t.Error("IsRetry should be true")
		}
		if data.Metadata["title"] != "Test Movie" {
			t.Errorf("Metadata[title] = %q, want %q", data.Metadata["title"], "Test Movie")
		}
	})

	t.Run("missing file_path", func(t *testing.T) {
		e := &Event{
			EventType: SearchCompleted,
			EventData: map[string]interface{}{
				"media_id": float64(456),
			},
		}

		_, ok := e.ParseSearchCompletedEventData()
		if ok {
			t.Error("ParseSearchCompletedEventData() should return false when file_path is missing")
		}
	})
}

// TestEvent_ParseRetryEventData tests parsing retry event data.
func TestEvent_ParseRetryEventData(t *testing.T) {
	t.Run("valid retry event", func(t *testing.T) {
		e := &Event{
			EventType: RetryScheduled,
			EventData: map[string]interface{}{
				"file_path": "/media/movies/test.mkv",
				"path_id":   float64(1),
			},
		}

		data, ok := e.ParseRetryEventData()
		if !ok {
			t.Fatal("ParseRetryEventData() returned false for valid event")
		}
		if data.FilePath != "/media/movies/test.mkv" {
			t.Errorf("FilePath = %q, want %q", data.FilePath, "/media/movies/test.mkv")
		}
		if data.PathID != 1 {
			t.Errorf("PathID = %d, want %d", data.PathID, 1)
		}
	})
}

// TestEventType_Constants verifies event type constants are correctly defined.
func TestEventType_Constants(t *testing.T) {
	// Verify key event types are defined as expected strings
	eventTypes := map[EventType]string{
		CorruptionDetected:  "CorruptionDetected",
		RemediationQueued:   "RemediationQueued",
		DeletionStarted:     "DeletionStarted",
		DeletionCompleted:   "DeletionCompleted",
		DeletionFailed:      "DeletionFailed",
		SearchStarted:       "SearchStarted",
		SearchCompleted:     "SearchCompleted",
		SearchFailed:        "SearchFailed",
		VerificationSuccess: "VerificationSuccess",
		VerificationFailed:  "VerificationFailed",
		RetryScheduled:      "RetryScheduled",
		MaxRetriesReached:   "MaxRetriesReached",
	}

	for eventType, expectedString := range eventTypes {
		if string(eventType) != expectedString {
			t.Errorf("EventType %v = %q, want %q", eventType, string(eventType), expectedString)
		}
	}
}
