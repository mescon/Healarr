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

	// Gauges
	activeRemediations   prometheus.Gauge
	queuedRemediations   prometheus.Gauge
	stuckRemediations    prometheus.Gauge
	unhealthyInstances   prometheus.Gauge
	currentScanProgress  prometheus.Gauge

	// Histograms
	remediationDuration *prometheus.HistogramVec
	scanDuration        prometheus.Histogram

	// Internal tracking
	mu                      sync.Mutex
	activeRemediationCount  int
	queuedRemediationCount  int
	stuckRemediationCount   int
	unhealthyInstanceCount  int
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

// Start subscribes to events and updates metrics
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

func (m *MetricsService) handleRemediationQueued(event domain.Event) {
	m.mu.Lock()
	m.queuedRemediationCount++
	m.queuedRemediations.Set(float64(m.queuedRemediationCount))
	m.mu.Unlock()
}

func (m *MetricsService) handleDeletionStarted(event domain.Event) {
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

func (m *MetricsService) handleVerificationSuccess(event domain.Event) {
	m.verificationsTotal.WithLabelValues("success").Inc()
	m.remediationsTotal.WithLabelValues("success").Inc()

	m.mu.Lock()
	if m.activeRemediationCount > 0 {
		m.activeRemediationCount--
		m.activeRemediations.Set(float64(m.activeRemediationCount))
	}
	m.mu.Unlock()
}

func (m *MetricsService) handleVerificationFailed(event domain.Event) {
	m.verificationsTotal.WithLabelValues("failed").Inc()
	// Don't decrement active count yet - retry may happen
}

func (m *MetricsService) handleMaxRetriesReached(event domain.Event) {
	m.remediationsTotal.WithLabelValues("max_retries").Inc()

	m.mu.Lock()
	if m.activeRemediationCount > 0 {
		m.activeRemediationCount--
		m.activeRemediations.Set(float64(m.activeRemediationCount))
	}
	m.mu.Unlock()
}

func (m *MetricsService) handleScanStarted(event domain.Event) {
	m.currentScanProgress.Set(0)
}

func (m *MetricsService) handleScanCompleted(event domain.Event) {
	m.scansTotal.WithLabelValues("completed").Inc()
	m.currentScanProgress.Set(100)
}

func (m *MetricsService) handleScanFailed(event domain.Event) {
	m.scansTotal.WithLabelValues("failed").Inc()
	m.currentScanProgress.Set(0)
}

func (m *MetricsService) handleScanProgress(event domain.Event) {
	if progress, ok := event.EventData["progress"].(float64); ok {
		m.currentScanProgress.Set(progress)
	}
}

func (m *MetricsService) handleNotificationSent(event domain.Event) {
	m.notificationsTotal.WithLabelValues("sent").Inc()
}

func (m *MetricsService) handleNotificationFailed(event domain.Event) {
	m.notificationsTotal.WithLabelValues("failed").Inc()
}

func (m *MetricsService) handleStuckRemediation(event domain.Event) {
	m.mu.Lock()
	m.stuckRemediationCount++
	m.stuckRemediations.Set(float64(m.stuckRemediationCount))
	m.mu.Unlock()
}

func (m *MetricsService) handleInstanceUnhealthy(event domain.Event) {
	m.mu.Lock()
	m.unhealthyInstanceCount++
	m.unhealthyInstances.Set(float64(m.unhealthyInstanceCount))
	m.mu.Unlock()
}

func (m *MetricsService) handleInstanceHealthy(event domain.Event) {
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
