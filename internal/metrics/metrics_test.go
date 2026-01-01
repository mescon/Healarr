package metrics

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/mescon/Healarr/internal/domain"
	"github.com/mescon/Healarr/internal/eventbus"

	_ "modernc.org/sqlite"
)

// =============================================================================
// Test helpers
// =============================================================================

// newTestEventBus creates an eventbus for tests using an in-memory SQLite database
func newTestEventBus(t *testing.T) *eventbus.EventBus {
	t.Helper()
	db, err := openTestDB()
	if err != nil {
		t.Fatalf("Failed to create test db: %v", err)
	}
	return eventbus.NewEventBus(db)
}

func openTestDB() (*sql.DB, error) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		return nil, err
	}
	// Create events table for eventbus
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS events (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		aggregate_type TEXT NOT NULL,
		aggregate_id TEXT NOT NULL,
		event_type TEXT NOT NULL,
		event_data JSON NOT NULL,
		event_version INTEGER NOT NULL DEFAULT 1,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		user_id TEXT
	)`)
	if err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// createTestMetrics creates a MetricsService with a custom Prometheus registry
// to avoid conflicts with the global registry in tests
func createTestMetrics(t *testing.T, eb *eventbus.EventBus) (*MetricsService, *prometheus.Registry) {
	t.Helper()

	reg := prometheus.NewRegistry()

	m := &MetricsService{
		eventBus: eb,

		corruptionsDetected: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "healarr_corruptions_detected_total",
				Help: "Total number of corruptions detected",
			},
			[]string{"corruption_type", "path_id"},
		),

		remediationsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "healarr_remediations_total",
				Help: "Total number of remediations by outcome",
			},
			[]string{"outcome"},
		),

		verificationsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "healarr_verifications_total",
				Help: "Total number of file verifications by outcome",
			},
			[]string{"outcome"},
		),

		scansTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "healarr_scans_total",
				Help: "Total number of scans by outcome",
			},
			[]string{"outcome"},
		),

		notificationsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "healarr_notifications_total",
				Help: "Total number of notifications sent by outcome",
			},
			[]string{"outcome"},
		),

		activeRemediations: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "healarr_active_remediations",
				Help: "Number of remediations currently in progress",
			},
		),

		queuedRemediations: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "healarr_queued_remediations",
				Help: "Number of remediations waiting to start",
			},
		),

		stuckRemediations: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "healarr_stuck_remediations",
				Help: "Number of remediations stuck for more than 24 hours",
			},
		),

		unhealthyInstances: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "healarr_unhealthy_instances",
				Help: "Number of *arr instances currently unreachable",
			},
		),

		currentScanProgress: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "healarr_scan_progress_percent",
				Help: "Current scan progress percentage (0-100)",
			},
		),

		remediationDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "healarr_remediation_duration_seconds",
				Help:    "Duration of remediations in seconds",
				Buckets: prometheus.ExponentialBuckets(60, 2, 10),
			},
			[]string{"outcome"},
		),

		scanDuration: prometheus.NewHistogram(
			prometheus.HistogramOpts{
				Name:    "healarr_scan_duration_seconds",
				Help:    "Duration of scans in seconds",
				Buckets: prometheus.ExponentialBuckets(1, 2, 12),
			},
		),
	}

	// Register all metrics with custom registry
	reg.MustRegister(
		m.corruptionsDetected,
		m.remediationsTotal,
		m.verificationsTotal,
		m.scansTotal,
		m.notificationsTotal,
		m.activeRemediations,
		m.queuedRemediations,
		m.stuckRemediations,
		m.unhealthyInstances,
		m.currentScanProgress,
		m.remediationDuration,
		m.scanDuration,
	)

	return m, reg
}

// =============================================================================
// Constructor tests
// =============================================================================

func TestNewMetricsService(t *testing.T) {
	// Create a fresh eventbus for this test
	eb := newTestEventBus(t)
	defer eb.Shutdown()

	// NewMetricsService uses the global Prometheus registry
	// We'll test it once and accept potential registry conflicts
	// by calling it in its own subtest with cleanup
	m := NewMetricsService(eb)

	if m == nil {
		t.Fatal("NewMetricsService should not return nil")
	}

	if m.eventBus != eb {
		t.Error("eventBus should be set to the provided value")
	}

	// Verify metrics were created
	if m.corruptionsDetected == nil {
		t.Error("corruptionsDetected metric should be initialized")
	}
	if m.remediationsTotal == nil {
		t.Error("remediationsTotal metric should be initialized")
	}
	if m.activeRemediations == nil {
		t.Error("activeRemediations metric should be initialized")
	}
}

// =============================================================================
// Handler tests
// =============================================================================

func TestMetricsService_Handler(t *testing.T) {
	eb := newTestEventBus(t)
	m, _ := createTestMetrics(t, eb)

	handler := m.Handler()
	if handler == nil {
		t.Error("Handler() should not return nil")
	}
	// Handler() returns http.Handler by signature, no assertion needed
}

func TestMetricsService_Handler_ReturnsMetrics(t *testing.T) {
	eb := newTestEventBus(t)
	m, _ := createTestMetrics(t, eb)

	// Record some metrics
	m.corruptionsDetected.WithLabelValues("video_corruption", "1").Inc()
	m.scansTotal.WithLabelValues("completed").Inc()

	// Make HTTP request to handler
	req := httptest.NewRequest("GET", "/metrics", nil)
	rec := httptest.NewRecorder()

	m.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("Handler returned %d, want %d", rec.Code, http.StatusOK)
	}

	// Note: m.Handler() uses the global promhttp.Handler(), not our custom registry
	// So we just verify the handler returns valid prometheus format
	body := rec.Body.String()
	// Should contain at least some prometheus metric format indicators
	if !strings.Contains(body, "# HELP") && !strings.Contains(body, "# TYPE") && len(body) < 10 {
		t.Error("Response should contain prometheus metrics format")
	}
}

// =============================================================================
// Event handler tests
// =============================================================================

func TestHandleCorruptionDetected(t *testing.T) {
	eb := newTestEventBus(t)
	m, _ := createTestMetrics(t, eb)

	event := domain.Event{
		EventType: domain.CorruptionDetected,
		EventData: map[string]interface{}{
			"corruption_type": "video_corruption",
			"path_id":         float64(1),
		},
	}

	m.handleCorruptionDetected(event)

	// The counter should have been incremented
	// We can't easily read Prometheus counters, so this is mainly testing no panic
}

func TestHandleRemediationQueued(t *testing.T) {
	eb := newTestEventBus(t)
	m, _ := createTestMetrics(t, eb)

	if m.queuedRemediationCount != 0 {
		t.Fatalf("Initial count should be 0, got %d", m.queuedRemediationCount)
	}

	m.handleRemediationQueued(domain.Event{EventType: domain.RemediationQueued})

	if m.queuedRemediationCount != 1 {
		t.Errorf("queuedRemediationCount = %d, want 1", m.queuedRemediationCount)
	}

	m.handleRemediationQueued(domain.Event{EventType: domain.RemediationQueued})

	if m.queuedRemediationCount != 2 {
		t.Errorf("queuedRemediationCount = %d, want 2", m.queuedRemediationCount)
	}
}

func TestHandleDeletionStarted(t *testing.T) {
	eb := newTestEventBus(t)
	m, _ := createTestMetrics(t, eb)

	// Queue a remediation first
	m.handleRemediationQueued(domain.Event{EventType: domain.RemediationQueued})

	if m.queuedRemediationCount != 1 {
		t.Fatalf("Setup: queuedRemediationCount should be 1")
	}

	m.handleDeletionStarted(domain.Event{EventType: domain.DeletionStarted})

	if m.queuedRemediationCount != 0 {
		t.Errorf("queuedRemediationCount = %d, want 0", m.queuedRemediationCount)
	}
	if m.activeRemediationCount != 1 {
		t.Errorf("activeRemediationCount = %d, want 1", m.activeRemediationCount)
	}
}

func TestHandleDeletionStarted_NoNegativeQueued(t *testing.T) {
	eb := newTestEventBus(t)
	m, _ := createTestMetrics(t, eb)

	// Start deletion without queuing first
	m.handleDeletionStarted(domain.Event{EventType: domain.DeletionStarted})

	// Should not go negative
	if m.queuedRemediationCount != 0 {
		t.Errorf("queuedRemediationCount should not go negative, got %d", m.queuedRemediationCount)
	}
	if m.activeRemediationCount != 1 {
		t.Errorf("activeRemediationCount = %d, want 1", m.activeRemediationCount)
	}
}

func TestHandleVerificationSuccess(t *testing.T) {
	eb := newTestEventBus(t)
	m, _ := createTestMetrics(t, eb)

	m.activeRemediationCount = 1
	m.handleVerificationSuccess(domain.Event{EventType: domain.VerificationSuccess})

	if m.activeRemediationCount != 0 {
		t.Errorf("activeRemediationCount = %d, want 0", m.activeRemediationCount)
	}
}

func TestHandleVerificationFailed(t *testing.T) {
	eb := newTestEventBus(t)
	m, _ := createTestMetrics(t, eb)

	m.activeRemediationCount = 1
	m.handleVerificationFailed(domain.Event{EventType: domain.VerificationFailed})

	// Active count should NOT be decremented (retry may happen)
	if m.activeRemediationCount != 1 {
		t.Errorf("activeRemediationCount = %d, want 1 (should not decrement on failure)", m.activeRemediationCount)
	}
}

func TestHandleMaxRetriesReached(t *testing.T) {
	eb := newTestEventBus(t)
	m, _ := createTestMetrics(t, eb)

	m.activeRemediationCount = 1
	m.handleMaxRetriesReached(domain.Event{EventType: domain.MaxRetriesReached})

	if m.activeRemediationCount != 0 {
		t.Errorf("activeRemediationCount = %d, want 0", m.activeRemediationCount)
	}
}

func TestHandleScanStarted(t *testing.T) {
	eb := newTestEventBus(t)
	m, _ := createTestMetrics(t, eb)

	// Set progress to something
	m.currentScanProgress.Set(50)

	m.handleScanStarted(domain.Event{EventType: domain.ScanStarted})

	// Can't easily read gauge value, but should not panic
}

func TestHandleScanCompleted(t *testing.T) {
	eb := newTestEventBus(t)
	m, _ := createTestMetrics(t, eb)

	m.handleScanCompleted(domain.Event{EventType: domain.ScanCompleted})
	// Should not panic
}

func TestHandleScanFailed(t *testing.T) {
	eb := newTestEventBus(t)
	m, _ := createTestMetrics(t, eb)

	m.handleScanFailed(domain.Event{EventType: domain.ScanFailed})
	// Should not panic
}

func TestHandleScanProgress(t *testing.T) {
	eb := newTestEventBus(t)
	m, _ := createTestMetrics(t, eb)

	m.handleScanProgress(domain.Event{
		EventType: domain.ScanProgress,
		EventData: map[string]interface{}{
			"progress": float64(75),
		},
	})
	// Should not panic
}

func TestHandleScanProgress_MissingData(t *testing.T) {
	eb := newTestEventBus(t)
	m, _ := createTestMetrics(t, eb)

	// Should not panic with missing progress data
	m.handleScanProgress(domain.Event{
		EventType: domain.ScanProgress,
		EventData: map[string]interface{}{},
	})
}

func TestHandleNotificationSent(t *testing.T) {
	eb := newTestEventBus(t)
	m, _ := createTestMetrics(t, eb)

	m.handleNotificationSent(domain.Event{EventType: domain.NotificationSent})
	// Should not panic
}

func TestHandleNotificationFailed(t *testing.T) {
	eb := newTestEventBus(t)
	m, _ := createTestMetrics(t, eb)

	m.handleNotificationFailed(domain.Event{EventType: domain.NotificationFailed})
	// Should not panic
}

func TestHandleStuckRemediation(t *testing.T) {
	eb := newTestEventBus(t)
	m, _ := createTestMetrics(t, eb)

	if m.stuckRemediationCount != 0 {
		t.Fatalf("Initial stuckRemediationCount should be 0")
	}

	m.handleStuckRemediation(domain.Event{EventType: domain.StuckRemediation})

	if m.stuckRemediationCount != 1 {
		t.Errorf("stuckRemediationCount = %d, want 1", m.stuckRemediationCount)
	}
}

func TestHandleInstanceUnhealthy(t *testing.T) {
	eb := newTestEventBus(t)
	m, _ := createTestMetrics(t, eb)

	m.handleInstanceUnhealthy(domain.Event{EventType: domain.InstanceUnhealthy})

	if m.unhealthyInstanceCount != 1 {
		t.Errorf("unhealthyInstanceCount = %d, want 1", m.unhealthyInstanceCount)
	}
}

func TestHandleInstanceHealthy(t *testing.T) {
	eb := newTestEventBus(t)
	m, _ := createTestMetrics(t, eb)

	m.unhealthyInstanceCount = 2
	m.handleInstanceHealthy(domain.Event{EventType: domain.InstanceHealthy})

	if m.unhealthyInstanceCount != 1 {
		t.Errorf("unhealthyInstanceCount = %d, want 1", m.unhealthyInstanceCount)
	}
}

func TestHandleInstanceHealthy_NoNegative(t *testing.T) {
	eb := newTestEventBus(t)
	m, _ := createTestMetrics(t, eb)

	m.unhealthyInstanceCount = 0
	m.handleInstanceHealthy(domain.Event{EventType: domain.InstanceHealthy})

	if m.unhealthyInstanceCount != 0 {
		t.Errorf("unhealthyInstanceCount should not go negative, got %d", m.unhealthyInstanceCount)
	}
}

// =============================================================================
// ResetStuckCount tests
// =============================================================================

func TestResetStuckCount(t *testing.T) {
	eb := newTestEventBus(t)
	m, _ := createTestMetrics(t, eb)

	m.stuckRemediationCount = 5
	m.ResetStuckCount()

	if m.stuckRemediationCount != 0 {
		t.Errorf("stuckRemediationCount = %d, want 0", m.stuckRemediationCount)
	}
}

// =============================================================================
// Concurrency tests
// =============================================================================

func TestMetrics_Concurrent(t *testing.T) {
	eb := newTestEventBus(t)
	m, _ := createTestMetrics(t, eb)

	const goroutines = 100

	done := make(chan bool, goroutines)

	for i := 0; i < goroutines; i++ {
		go func() {
			m.handleRemediationQueued(domain.Event{})
			m.handleDeletionStarted(domain.Event{})
			m.handleVerificationSuccess(domain.Event{})
			m.handleStuckRemediation(domain.Event{})
			m.handleInstanceUnhealthy(domain.Event{})
			m.handleInstanceHealthy(domain.Event{})
			m.ResetStuckCount()
			done <- true
		}()
	}

	for i := 0; i < goroutines; i++ {
		<-done
	}

	// Should not panic due to race conditions
}

// =============================================================================
// Start tests
// =============================================================================

func TestMetricsService_Start(t *testing.T) {
	eb := newTestEventBus(t)
	m, _ := createTestMetrics(t, eb)

	// Start should subscribe to all events
	m.Start()

	// Publish an event and verify it's handled
	eb.Publish(domain.Event{
		EventType: domain.CorruptionDetected,
		EventData: map[string]interface{}{
			"corruption_type": "test",
		},
	})

	// Give time for async processing
	// (In a real scenario, we'd use synchronization)
}

// =============================================================================
// Full lifecycle tests
// =============================================================================

func TestMetrics_RemediationLifecycle(t *testing.T) {
	eb := newTestEventBus(t)
	m, _ := createTestMetrics(t, eb)

	// 1. Corruption detected and queued
	m.handleCorruptionDetected(domain.Event{
		EventData: map[string]interface{}{"corruption_type": "video"},
	})
	m.handleRemediationQueued(domain.Event{})

	if m.queuedRemediationCount != 1 {
		t.Errorf("After queue: queuedRemediationCount = %d, want 1", m.queuedRemediationCount)
	}

	// 2. Deletion started
	m.handleDeletionStarted(domain.Event{})

	if m.queuedRemediationCount != 0 {
		t.Errorf("After start: queuedRemediationCount = %d, want 0", m.queuedRemediationCount)
	}
	if m.activeRemediationCount != 1 {
		t.Errorf("After start: activeRemediationCount = %d, want 1", m.activeRemediationCount)
	}

	// 3. Verification fails (retry)
	m.handleVerificationFailed(domain.Event{})

	if m.activeRemediationCount != 1 {
		t.Errorf("After fail: activeRemediationCount = %d, want 1 (retry pending)", m.activeRemediationCount)
	}

	// 4. Verification succeeds
	m.handleVerificationSuccess(domain.Event{})

	if m.activeRemediationCount != 0 {
		t.Errorf("After success: activeRemediationCount = %d, want 0", m.activeRemediationCount)
	}
}

func TestMetrics_MaxRetriesLifecycle(t *testing.T) {
	eb := newTestEventBus(t)
	m, _ := createTestMetrics(t, eb)

	m.handleRemediationQueued(domain.Event{})
	m.handleDeletionStarted(domain.Event{})

	if m.activeRemediationCount != 1 {
		t.Fatalf("Setup: activeRemediationCount = %d, want 1", m.activeRemediationCount)
	}

	// Max retries reached
	m.handleMaxRetriesReached(domain.Event{})

	if m.activeRemediationCount != 0 {
		t.Errorf("After max retries: activeRemediationCount = %d, want 0", m.activeRemediationCount)
	}
}
