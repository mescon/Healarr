package metrics

import (
	"net/http"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/mescon/Healarr/internal/domain"
	"github.com/mescon/Healarr/internal/eventbus"
	"github.com/mescon/Healarr/internal/logger"
)

// MetricsService exposes Prometheus metrics for Healarr
type MetricsService struct {
	eventBus *eventbus.EventBus

	// Counters
	corruptionsDetected *prometheus.CounterVec
	remediationsTotal   *prometheus.CounterVec
	verificationsTotal  *prometheus.CounterVec
	scansTotal          *prometheus.CounterVec
	notificationsTotal  *prometheus.CounterVec

	// HTTP metrics
	httpRequestsTotal    *prometheus.CounterVec
	httpRequestDuration  *prometheus.HistogramVec
	websocketConnections prometheus.Gauge

	// Database metrics
	dbQueriesTotal   *prometheus.CounterVec
	dbQueryDuration  *prometheus.HistogramVec
	dbConnectionPool *prometheus.GaugeVec

	// Gauges
	activeRemediations  prometheus.Gauge
	queuedRemediations  prometheus.Gauge
	stuckRemediations   prometheus.Gauge
	unhealthyInstances  prometheus.Gauge
	currentScanProgress prometheus.Gauge

	// Histograms
	remediationDuration *prometheus.HistogramVec
	scanDuration        prometheus.Histogram

	// Internal tracking
	mu                     sync.Mutex
	activeRemediationCount int
	queuedRemediationCount int
	stuckRemediationCount  int
	unhealthyInstanceCount int
}

// NewMetricsService creates and registers Prometheus metrics
func NewMetricsService(eb *eventbus.EventBus) *MetricsService {
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
			[]string{"outcome"}, // success, failed, max_retries
		),

		verificationsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "healarr_verifications_total",
				Help: "Total number of file verifications by outcome",
			},
			[]string{"outcome"}, // success, failed
		),

		scansTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "healarr_scans_total",
				Help: "Total number of scans by outcome",
			},
			[]string{"outcome"}, // completed, failed
		),

		notificationsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "healarr_notifications_total",
				Help: "Total number of notifications sent by outcome",
			},
			[]string{"outcome"}, // sent, failed
		),

		httpRequestsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "healarr_http_requests_total",
				Help: "Total number of HTTP requests by method, path, and status",
			},
			[]string{"method", "path", "status"},
		),

		httpRequestDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "healarr_http_request_duration_seconds",
				Help:    "HTTP request duration in seconds",
				Buckets: prometheus.DefBuckets,
			},
			[]string{"method", "path", "status"},
		),

		websocketConnections: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "healarr_websocket_connections",
				Help: "Current number of active WebSocket connections",
			},
		),

		dbQueriesTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "healarr_db_queries_total",
				Help: "Total number of database queries by operation type",
			},
			[]string{"operation"}, // select, insert, update, delete, other
		),

		dbQueryDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "healarr_db_query_duration_seconds",
				Help:    "Database query duration in seconds",
				Buckets: prometheus.ExponentialBuckets(0.001, 2, 12), // 1ms to ~4s
			},
			[]string{"operation"},
		),

		dbConnectionPool: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "healarr_db_connection_pool",
				Help: "Database connection pool statistics",
			},
			[]string{"state"}, // open, in_use, idle
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
				Buckets: prometheus.ExponentialBuckets(60, 2, 10), // 1min to ~17hours
			},
			[]string{"outcome"},
		),

		scanDuration: prometheus.NewHistogram(
			prometheus.HistogramOpts{
				Name:    "healarr_scan_duration_seconds",
				Help:    "Duration of scans in seconds",
				Buckets: prometheus.ExponentialBuckets(1, 2, 12), // 1s to ~1hour
			},
		),
	}

	// Register all metrics
	prometheus.MustRegister(
		m.corruptionsDetected,
		m.remediationsTotal,
		m.verificationsTotal,
		m.scansTotal,
		m.notificationsTotal,
		m.httpRequestsTotal,
		m.httpRequestDuration,
		m.websocketConnections,
		m.dbQueriesTotal,
		m.dbQueryDuration,
		m.dbConnectionPool,
		m.activeRemediations,
		m.queuedRemediations,
		m.stuckRemediations,
		m.unhealthyInstances,
		m.currentScanProgress,
		m.remediationDuration,
		m.scanDuration,
	)

	return m
}

// Start subscribes to events and updates Prometheus metrics.
// All subscriber goroutines are managed by the EventBus — they are stopped
// when EventBus.Shutdown() is called. No separate Stop method is needed.
func (m *MetricsService) Start() {
	// Subscribe to all relevant events
	m.eventBus.Subscribe(domain.CorruptionDetected, m.handleCorruptionDetected)
	m.eventBus.Subscribe(domain.RemediationQueued, m.handleRemediationQueued)
	m.eventBus.Subscribe(domain.DeletionStarted, m.handleDeletionStarted)
	m.eventBus.Subscribe(domain.VerificationSuccess, m.handleVerificationSuccess)
	m.eventBus.Subscribe(domain.VerificationFailed, m.handleVerificationFailed)
	m.eventBus.Subscribe(domain.MaxRetriesReached, m.handleMaxRetriesReached)
	m.eventBus.Subscribe(domain.ScanStarted, m.handleScanStarted)
	m.eventBus.Subscribe(domain.ScanCompleted, m.handleScanCompleted)
	m.eventBus.Subscribe(domain.ScanFailed, m.handleScanFailed)
	m.eventBus.Subscribe(domain.ScanProgress, m.handleScanProgress)
	m.eventBus.Subscribe(domain.NotificationSent, m.handleNotificationSent)
	m.eventBus.Subscribe(domain.NotificationFailed, m.handleNotificationFailed)
	m.eventBus.Subscribe(domain.StuckRemediation, m.handleStuckRemediation)
	m.eventBus.Subscribe(domain.InstanceUnhealthy, m.handleInstanceUnhealthy)
	m.eventBus.Subscribe(domain.InstanceHealthy, m.handleInstanceHealthy)

	logger.Infof("Metrics service started")
}

// Handler returns the Prometheus HTTP handler for /metrics endpoint
func (m *MetricsService) Handler() http.Handler {
	return promhttp.Handler()
}

// Event handlers

func (m *MetricsService) handleCorruptionDetected(event domain.Event) {
	corruptionType := "unknown"
	if ct, ok := event.EventData["corruption_type"].(string); ok {
		corruptionType = ct
	}
	pathID := "unknown"
	if pid, ok := event.EventData["path_id"].(float64); ok {
		pathID = string(rune(int(pid)))
	}
	m.corruptionsDetected.WithLabelValues(corruptionType, pathID).Inc()
}

func (m *MetricsService) handleRemediationQueued(_ domain.Event) {
	m.mu.Lock()
	m.queuedRemediationCount++
	m.queuedRemediations.Set(float64(m.queuedRemediationCount))
	m.mu.Unlock()
}

func (m *MetricsService) handleDeletionStarted(_ domain.Event) {
	m.mu.Lock()
	// Move from queued to active
	if m.queuedRemediationCount > 0 {
		m.queuedRemediationCount--
		m.queuedRemediations.Set(float64(m.queuedRemediationCount))
	}
	m.activeRemediationCount++
	m.activeRemediations.Set(float64(m.activeRemediationCount))
	m.mu.Unlock()
}

func (m *MetricsService) handleVerificationSuccess(_ domain.Event) {
	m.verificationsTotal.WithLabelValues("success").Inc()
	m.remediationsTotal.WithLabelValues("success").Inc()

	m.mu.Lock()
	if m.activeRemediationCount > 0 {
		m.activeRemediationCount--
		m.activeRemediations.Set(float64(m.activeRemediationCount))
	}
	m.mu.Unlock()
}

func (m *MetricsService) handleVerificationFailed(_ domain.Event) {
	m.verificationsTotal.WithLabelValues("failed").Inc()
	// Don't decrement active count yet - retry may happen
}

func (m *MetricsService) handleMaxRetriesReached(_ domain.Event) {
	m.remediationsTotal.WithLabelValues("max_retries").Inc()

	m.mu.Lock()
	if m.activeRemediationCount > 0 {
		m.activeRemediationCount--
		m.activeRemediations.Set(float64(m.activeRemediationCount))
	}
	m.mu.Unlock()
}

func (m *MetricsService) handleScanStarted(_ domain.Event) {
	m.currentScanProgress.Set(0)
}

func (m *MetricsService) handleScanCompleted(_ domain.Event) {
	m.scansTotal.WithLabelValues("completed").Inc()
	m.currentScanProgress.Set(100)
}

func (m *MetricsService) handleScanFailed(_ domain.Event) {
	m.scansTotal.WithLabelValues("failed").Inc()
	m.currentScanProgress.Set(0)
}

func (m *MetricsService) handleScanProgress(event domain.Event) {
	if progress, ok := event.EventData["progress"].(float64); ok {
		m.currentScanProgress.Set(progress)
	}
}

func (m *MetricsService) handleNotificationSent(_ domain.Event) {
	m.notificationsTotal.WithLabelValues("sent").Inc()
}

func (m *MetricsService) handleNotificationFailed(_ domain.Event) {
	m.notificationsTotal.WithLabelValues("failed").Inc()
}

func (m *MetricsService) handleStuckRemediation(_ domain.Event) {
	m.mu.Lock()
	m.stuckRemediationCount++
	m.stuckRemediations.Set(float64(m.stuckRemediationCount))
	m.mu.Unlock()
}

func (m *MetricsService) handleInstanceUnhealthy(_ domain.Event) {
	m.mu.Lock()
	m.unhealthyInstanceCount++
	m.unhealthyInstances.Set(float64(m.unhealthyInstanceCount))
	m.mu.Unlock()
}

func (m *MetricsService) handleInstanceHealthy(_ domain.Event) {
	m.mu.Lock()
	if m.unhealthyInstanceCount > 0 {
		m.unhealthyInstanceCount--
		m.unhealthyInstances.Set(float64(m.unhealthyInstanceCount))
	}
	m.mu.Unlock()
}

// ResetStuckCount resets the stuck remediation counter (called after health check clears)
func (m *MetricsService) ResetStuckCount() {
	m.mu.Lock()
	m.stuckRemediationCount = 0
	m.stuckRemediations.Set(0)
	m.mu.Unlock()
}

// RecordHTTPRequest records metrics for an HTTP request.
// Called by the metrics middleware after each request completes.
func (m *MetricsService) RecordHTTPRequest(method, path, status string, duration float64) {
	m.httpRequestsTotal.WithLabelValues(method, path, status).Inc()
	m.httpRequestDuration.WithLabelValues(method, path, status).Observe(duration)
}

// WebSocketConnected increments the WebSocket connection count.
func (m *MetricsService) WebSocketConnected() {
	m.websocketConnections.Inc()
}

// WebSocketDisconnected decrements the WebSocket connection count.
func (m *MetricsService) WebSocketDisconnected() {
	m.websocketConnections.Dec()
}

// RecordDBQuery records metrics for a database query.
// operation should be "select", "insert", "update", "delete", or "other".
func (m *MetricsService) RecordDBQuery(operation string, duration float64) {
	m.dbQueriesTotal.WithLabelValues(operation).Inc()
	m.dbQueryDuration.WithLabelValues(operation).Observe(duration)
}

// RecordDBPoolStats records database connection pool statistics.
func (m *MetricsService) RecordDBPoolStats(openConns, inUse, idle int) {
	m.dbConnectionPool.WithLabelValues("open").Set(float64(openConns))
	m.dbConnectionPool.WithLabelValues("in_use").Set(float64(inUse))
	m.dbConnectionPool.WithLabelValues("idle").Set(float64(idle))
}
