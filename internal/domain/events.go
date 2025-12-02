package domain

import (
	"time"
)

type EventType string

const (
	CorruptionDetected   EventType = "CorruptionDetected"
	RemediationQueued    EventType = "RemediationQueued"
	DeletionStarted      EventType = "DeletionStarted"
	DeletionCompleted    EventType = "DeletionCompleted"
	DeletionFailed       EventType = "DeletionFailed"
	SearchStarted        EventType = "SearchStarted"
	SearchCompleted      EventType = "SearchCompleted"
	SearchFailed         EventType = "SearchFailed"
	FileDetected         EventType = "FileDetected"
	VerificationStarted  EventType = "VerificationStarted"
	VerificationSuccess  EventType = "VerificationSuccess"
	VerificationFailed   EventType = "VerificationFailed"
	DownloadTimeout      EventType = "DownloadTimeout"
	DownloadProgress     EventType = "DownloadProgress"
	DownloadFailed       EventType = "DownloadFailed"
	ImportBlocked        EventType = "ImportBlocked"        // *arr import blocked - requires manual intervention
	ManuallyRemoved      EventType = "ManuallyRemoved"      // Item manually removed from *arr queue
	RetryScheduled       EventType = "RetryScheduled"
	MaxRetriesReached    EventType = "MaxRetriesReached"
	ScanStarted          EventType = "ScanStarted"
	ScanCompleted        EventType = "ScanCompleted"
	ScanFailed           EventType = "ScanFailed"
	ScanProgress         EventType = "ScanProgress"
	NotificationSent     EventType = "NotificationSent"
	NotificationFailed   EventType = "NotificationFailed"
	CorruptionIgnored    EventType = "CorruptionIgnored"
	SystemHealthDegraded EventType = "SystemHealthDegraded"

	// Health monitoring events
	StuckRemediation  EventType = "StuckRemediation"
	InstanceUnhealthy EventType = "InstanceUnhealthy"
	InstanceHealthy   EventType = "InstanceHealthy"
)

type Event struct {
	ID            int64                  `json:"id"`
	AggregateType string                 `json:"aggregate_type"`
	AggregateID   string                 `json:"aggregate_id"`
	EventType     EventType              `json:"event_type"`
	EventData     map[string]interface{} `json:"event_data"`
	EventVersion  int                    `json:"event_version"`
	CreatedAt     time.Time              `json:"created_at"`
	UserID        string                 `json:"user_id,omitempty"`
}

// =============================================================================
// Type-safe event data accessors
// These helpers provide compile-time safety when extracting data from events.
// =============================================================================

// GetString safely extracts a string field from EventData.
// Returns the value and true if found and is a string, otherwise empty string and false.
func (e *Event) GetString(key string) (string, bool) {
	if e.EventData == nil {
		return "", false
	}
	v, ok := e.EventData[key].(string)
	return v, ok
}

// GetStringOr extracts a string field or returns the default value.
func (e *Event) GetStringOr(key, defaultVal string) string {
	if v, ok := e.GetString(key); ok {
		return v
	}
	return defaultVal
}

// GetInt64 safely extracts an int64 field from EventData.
// Handles both int64 and float64 (JSON unmarshaling produces float64).
func (e *Event) GetInt64(key string) (int64, bool) {
	if e.EventData == nil {
		return 0, false
	}
	switch v := e.EventData[key].(type) {
	case int64:
		return v, true
	case float64:
		return int64(v), true
	case int:
		return int64(v), true
	default:
		return 0, false
	}
}

// GetInt64Or extracts an int64 field or returns the default value.
func (e *Event) GetInt64Or(key string, defaultVal int64) int64 {
	if v, ok := e.GetInt64(key); ok {
		return v
	}
	return defaultVal
}

// GetFloat64 safely extracts a float64 field from EventData.
func (e *Event) GetFloat64(key string) (float64, bool) {
	if e.EventData == nil {
		return 0, false
	}
	switch v := e.EventData[key].(type) {
	case float64:
		return v, true
	case int64:
		return float64(v), true
	case int:
		return float64(v), true
	default:
		return 0, false
	}
}

// GetBool safely extracts a bool field from EventData.
func (e *Event) GetBool(key string) (bool, bool) {
	if e.EventData == nil {
		return false, false
	}
	v, ok := e.EventData[key].(bool)
	return v, ok
}

// GetBoolOr extracts a bool field or returns the default value.
func (e *Event) GetBoolOr(key string, defaultVal bool) bool {
	if v, ok := e.GetBool(key); ok {
		return v
	}
	return defaultVal
}

// GetMap safely extracts a nested map from EventData.
func (e *Event) GetMap(key string) (map[string]interface{}, bool) {
	if e.EventData == nil {
		return nil, false
	}
	v, ok := e.EventData[key].(map[string]interface{})
	return v, ok
}

// GetStringSlice safely extracts a string slice from EventData.
func (e *Event) GetStringSlice(key string) ([]string, bool) {
	if e.EventData == nil {
		return nil, false
	}
	// Handle []string directly
	if v, ok := e.EventData[key].([]string); ok {
		return v, true
	}
	// Handle []interface{} (from JSON unmarshaling)
	if v, ok := e.EventData[key].([]interface{}); ok {
		result := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				result = append(result, s)
			}
		}
		return result, true
	}
	return nil, false
}

// =============================================================================
// Typed event data structures for common events
// =============================================================================

// CorruptionEventData contains data for CorruptionDetected events.
type CorruptionEventData struct {
	FilePath        string `json:"file_path"`
	PathID          int64  `json:"path_id,omitempty"`
	CorruptionType  string `json:"corruption_type"`
	ErrorDetails    string `json:"error_details,omitempty"`
	Source          string `json:"source,omitempty"` // "webhook", "scan", "rescan_worker"
	AutoRemediate   bool   `json:"auto_remediate"`
	DryRun          bool   `json:"dry_run"`
	BatchThrottled  bool   `json:"batch_throttled,omitempty"`
}

// ParseCorruptionEventData extracts typed corruption data from an event.
func (e *Event) ParseCorruptionEventData() (CorruptionEventData, bool) {
	filePath, ok := e.GetString("file_path")
	if !ok {
		return CorruptionEventData{}, false
	}
	return CorruptionEventData{
		FilePath:        filePath,
		PathID:          e.GetInt64Or("path_id", 0),
		CorruptionType:  e.GetStringOr("corruption_type", ""),
		ErrorDetails:    e.GetStringOr("error_details", ""),
		Source:          e.GetStringOr("source", ""),
		AutoRemediate:   e.GetBoolOr("auto_remediate", false),
		DryRun:          e.GetBoolOr("dry_run", false),
		BatchThrottled:  e.GetBoolOr("batch_throttled", false),
	}, true
}

// SearchCompletedEventData contains data for SearchCompleted events.
type SearchCompletedEventData struct {
	FilePath string                 `json:"file_path"`
	MediaID  int64                  `json:"media_id"`
	PathID   int64                  `json:"path_id,omitempty"`
	Metadata map[string]interface{} `json:"metadata,omitempty"`
	IsRetry  bool                   `json:"is_retry,omitempty"`
}

// ParseSearchCompletedEventData extracts typed search completed data from an event.
func (e *Event) ParseSearchCompletedEventData() (SearchCompletedEventData, bool) {
	filePath, ok := e.GetString("file_path")
	if !ok {
		return SearchCompletedEventData{}, false
	}
	metadata, _ := e.GetMap("metadata")
	return SearchCompletedEventData{
		FilePath: filePath,
		MediaID:  e.GetInt64Or("media_id", 0),
		PathID:   e.GetInt64Or("path_id", 0),
		Metadata: metadata,
		IsRetry:  e.GetBoolOr("is_retry", false),
	}, true
}

// RetryEventData contains data for RetryScheduled events.
type RetryEventData struct {
	FilePath string `json:"file_path"`
	PathID   int64  `json:"path_id,omitempty"`
}

// ParseRetryEventData extracts typed retry data from an event.
func (e *Event) ParseRetryEventData() (RetryEventData, bool) {
	filePath, ok := e.GetString("file_path")
	if !ok {
		return RetryEventData{}, false
	}
	return RetryEventData{
		FilePath: filePath,
		PathID:   e.GetInt64Or("path_id", 0),
	}, true
}
