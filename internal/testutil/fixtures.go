package testutil

import (
	"time"

	"github.com/google/uuid"

	"github.com/mescon/Healarr/internal/domain"
	"github.com/mescon/Healarr/internal/integration"
)

// EventOption is a functional option for configuring test events.
type EventOption func(*domain.Event)

// WithAggregateID sets a specific aggregate ID.
func WithAggregateID(id string) EventOption {
	return func(e *domain.Event) {
		e.AggregateID = id
	}
}

// WithCreatedAt sets the event creation time.
func WithCreatedAt(t time.Time) EventOption {
	return func(e *domain.Event) {
		e.CreatedAt = t
	}
}

// WithEventData merges additional data into EventData.
func WithEventData(data map[string]interface{}) EventOption {
	return func(e *domain.Event) {
		if e.EventData == nil {
			e.EventData = make(map[string]interface{})
		}
		for k, v := range data {
			e.EventData[k] = v
		}
	}
}

// WithPathID sets the path_id in event data.
func WithPathID(pathID int64) EventOption {
	return func(e *domain.Event) {
		if e.EventData == nil {
			e.EventData = make(map[string]interface{})
		}
		e.EventData["path_id"] = pathID
	}
}

// WithAutoRemediate sets the auto_remediate flag in event data.
func WithAutoRemediate(autoRemediate bool) EventOption {
	return func(e *domain.Event) {
		if e.EventData == nil {
			e.EventData = make(map[string]interface{})
		}
		e.EventData["auto_remediate"] = autoRemediate
	}
}

// WithDryRun sets the dry_run flag in event data.
func WithDryRun(dryRun bool) EventOption {
	return func(e *domain.Event) {
		if e.EventData == nil {
			e.EventData = make(map[string]interface{})
		}
		e.EventData["dry_run"] = dryRun
	}
}

// NewCorruptionEvent creates a CorruptionDetected event for testing.
func NewCorruptionEvent(filePath string, opts ...EventOption) domain.Event {
	event := domain.Event{
		AggregateType: "corruption",
		AggregateID:   uuid.New().String(),
		EventType:     domain.CorruptionDetected,
		EventVersion:  1,
		CreatedAt:     time.Now(),
		EventData: map[string]interface{}{
			"file_path":       filePath,
			"corruption_type": integration.ErrorTypeCorruptHeader,
			"error_details":   "Test corruption detected",
			"auto_remediate":  false,
			"dry_run":         false,
		},
	}

	for _, opt := range opts {
		opt(&event)
	}

	return event
}

// NewCorruptionEventWithType creates a CorruptionDetected event with a specific error type.
func NewCorruptionEventWithType(filePath string, errorType string, opts ...EventOption) domain.Event {
	event := domain.Event{
		AggregateType: "corruption",
		AggregateID:   uuid.New().String(),
		EventType:     domain.CorruptionDetected,
		EventVersion:  1,
		CreatedAt:     time.Now(),
		EventData: map[string]interface{}{
			"file_path":       filePath,
			"corruption_type": errorType,
			"error_details":   "Error: " + errorType,
			"auto_remediate":  false,
			"dry_run":         false,
		},
	}

	for _, opt := range opts {
		opt(&event)
	}

	return event
}

// NewSearchCompletedEvent creates a SearchCompleted event for testing.
func NewSearchCompletedEvent(filePath string, mediaID int64, opts ...EventOption) domain.Event {
	event := domain.Event{
		AggregateType: "corruption",
		AggregateID:   uuid.New().String(),
		EventType:     domain.SearchCompleted,
		EventVersion:  1,
		CreatedAt:     time.Now(),
		EventData: map[string]interface{}{
			"file_path": filePath,
			"media_id":  mediaID,
		},
	}

	for _, opt := range opts {
		opt(&event)
	}

	return event
}

// NewDeletionCompletedEvent creates a DeletionCompleted event for testing.
func NewDeletionCompletedEvent(aggregateID string, mediaID int64, metadata map[string]interface{}) domain.Event {
	return domain.Event{
		AggregateType: "corruption",
		AggregateID:   aggregateID,
		EventType:     domain.DeletionCompleted,
		EventVersion:  1,
		CreatedAt:     time.Now(),
		EventData: map[string]interface{}{
			"media_id": mediaID,
			"metadata": metadata,
		},
	}
}

// NewRetryEvent creates a RetryScheduled event for testing.
func NewRetryEvent(aggregateID, filePath string, opts ...EventOption) domain.Event {
	event := domain.Event{
		AggregateType: "corruption",
		AggregateID:   aggregateID,
		EventType:     domain.RetryScheduled,
		EventVersion:  1,
		CreatedAt:     time.Now(),
		EventData: map[string]interface{}{
			"file_path": filePath,
		},
	}

	for _, opt := range opts {
		opt(&event)
	}

	return event
}

// NewVerificationSuccessEvent creates a VerificationSuccess event for testing.
func NewVerificationSuccessEvent(aggregateID, filePath string) domain.Event {
	return domain.Event{
		AggregateType: "corruption",
		AggregateID:   aggregateID,
		EventType:     domain.VerificationSuccess,
		EventVersion:  1,
		CreatedAt:     time.Now(),
		EventData: map[string]interface{}{
			"file_path": filePath,
		},
	}
}

// NewMaxRetriesReachedEvent creates a MaxRetriesReached event for testing.
func NewMaxRetriesReachedEvent(aggregateID, filePath string, retryCount int) domain.Event {
	return domain.Event{
		AggregateType: "corruption",
		AggregateID:   aggregateID,
		EventType:     domain.MaxRetriesReached,
		EventVersion:  1,
		CreatedAt:     time.Now(),
		EventData: map[string]interface{}{
			"file_path":   filePath,
			"retry_count": retryCount,
		},
	}
}

// HealthyCheckResult returns a healthy check result for mocking.
func HealthyCheckResult() (bool, *integration.HealthCheckError) {
	return true, nil
}

// CorruptCheckResult returns a corruption check result for mocking.
func CorruptCheckResult(errorType, message string) (bool, *integration.HealthCheckError) {
	return false, &integration.HealthCheckError{
		Type:    errorType,
		Message: message,
	}
}

// RecoverableCheckResult returns a recoverable error check result for mocking.
func RecoverableCheckResult(errorType, message string) (bool, *integration.HealthCheckError) {
	return false, &integration.HealthCheckError{
		Type:    errorType,
		Message: message,
	}
}

// TestFilePaths provides common test file paths.
var TestFilePaths = struct {
	Movie1    string
	Movie2    string
	TVEpisode string
	Corrupt   string
}{
	Movie1:    "/media/movies/Test Movie (2024)/Test Movie (2024).mkv",
	Movie2:    "/media/movies/Another Film (2023)/Another Film (2023).mp4",
	TVEpisode: "/media/tv/Test Show/Season 01/Test Show - S01E01 - Pilot.mkv",
	Corrupt:   "/media/movies/Corrupt File (2024)/Corrupt File (2024).mkv",
}

// TestCorruptionFlow returns a sequence of events representing a full corruption remediation flow.
func TestCorruptionFlow(filePath string, mediaID int64) []domain.Event {
	aggregateID := uuid.New().String()
	baseTime := time.Now()

	return []domain.Event{
		{
			AggregateType: "corruption",
			AggregateID:   aggregateID,
			EventType:     domain.CorruptionDetected,
			EventVersion:  1,
			CreatedAt:     baseTime,
			EventData: map[string]interface{}{
				"file_path":       filePath,
				"corruption_type": integration.ErrorTypeCorruptHeader,
				"auto_remediate":  true,
			},
		},
		{
			AggregateType: "corruption",
			AggregateID:   aggregateID,
			EventType:     domain.RemediationQueued,
			EventVersion:  1,
			CreatedAt:     baseTime.Add(1 * time.Second),
		},
		{
			AggregateType: "corruption",
			AggregateID:   aggregateID,
			EventType:     domain.DeletionStarted,
			EventVersion:  1,
			CreatedAt:     baseTime.Add(2 * time.Second),
		},
		{
			AggregateType: "corruption",
			AggregateID:   aggregateID,
			EventType:     domain.DeletionCompleted,
			EventVersion:  1,
			CreatedAt:     baseTime.Add(3 * time.Second),
			EventData: map[string]interface{}{
				"media_id": mediaID,
			},
		},
		{
			AggregateType: "corruption",
			AggregateID:   aggregateID,
			EventType:     domain.SearchStarted,
			EventVersion:  1,
			CreatedAt:     baseTime.Add(4 * time.Second),
		},
		{
			AggregateType: "corruption",
			AggregateID:   aggregateID,
			EventType:     domain.SearchCompleted,
			EventVersion:  1,
			CreatedAt:     baseTime.Add(5 * time.Second),
			EventData: map[string]interface{}{
				"file_path": filePath,
				"media_id":  mediaID,
			},
		},
	}
}
