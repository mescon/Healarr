// Package testutil provides test utilities including mocks, fixtures, and test database helpers.
package testutil

import (
	"net/http"
	"sync"
	"time"

	"github.com/mescon/Healarr/internal/clock"
	"github.com/mescon/Healarr/internal/domain"
	"github.com/mescon/Healarr/internal/eventbus"
	"github.com/mescon/Healarr/internal/integration"
)

// ScanProgress mirrors services.ScanProgress for testing without creating an import cycle.
// Only includes the JSON-exported fields needed for test assertions.
type ScanProgress struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	Path        string `json:"path"`
	PathID      int64  `json:"path_id,omitempty"`
	TotalFiles  int    `json:"total_files"`
	FilesDone   int    `json:"files_done"`
	CurrentFile string `json:"current_file"`
	Status      string `json:"status"`
	StartTime   string `json:"start_time"`
	ScanDBID    int64  `json:"scan_db_id,omitempty"`
}

// =============================================================================
// MockClock - Testable time abstraction
// =============================================================================

// MockClock implements services.Clock for testing, providing deterministic control
// over time-dependent operations like scheduled retries.
type MockClock struct {
	mu           sync.Mutex
	now          time.Time
	pendingFuncs []pendingFunc
}

type pendingFunc struct {
	executeAt time.Time
	fn        func()
	stopped   bool
}

// MockTimer implements services.Timer for testing.
type MockTimer struct {
	clock *MockClock
	index int
}

// Compile-time assertion that MockClock implements clock.Clock
var _ clock.Clock = (*MockClock)(nil)

// NewMockClock creates a new MockClock with the current time as initial value.
func NewMockClock() *MockClock {
	return &MockClock{
		now: time.Now(),
	}
}

// NewMockClockAt creates a new MockClock with a specific initial time.
func NewMockClockAt(t time.Time) *MockClock {
	return &MockClock{
		now: t,
	}
}

// Now returns the mock's current time.
func (m *MockClock) Now() time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.now
}

// SetNow sets the mock's current time without triggering pending functions.
func (m *MockClock) SetNow(t time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.now = t
}

// AfterFunc schedules f to be called after duration d.
// Returns a Timer that can be used to cancel the call.
func (m *MockClock) AfterFunc(d time.Duration, f func()) clock.Timer {
	m.mu.Lock()
	defer m.mu.Unlock()

	executeAt := m.now.Add(d)
	index := len(m.pendingFuncs)
	m.pendingFuncs = append(m.pendingFuncs, pendingFunc{
		executeAt: executeAt,
		fn:        f,
		stopped:   false,
	})

	return &MockTimer{clock: m, index: index}
}

// Advance moves time forward by the given duration and executes any functions
// whose scheduled time has passed. Returns the number of functions executed.
func (m *MockClock) Advance(d time.Duration) int {
	m.mu.Lock()
	newTime := m.now.Add(d)
	m.now = newTime

	// Collect functions to execute (those that haven't been stopped and are due)
	var toExecute []func()
	for i := range m.pendingFuncs {
		pf := &m.pendingFuncs[i]
		if !pf.stopped && !pf.executeAt.After(newTime) {
			toExecute = append(toExecute, pf.fn)
			pf.stopped = true // Mark as executed
		}
	}
	m.mu.Unlock()

	// Execute outside the lock to avoid deadlocks
	for _, fn := range toExecute {
		fn()
	}
	return len(toExecute)
}

// FireAll immediately executes all pending scheduled functions, regardless of
// their scheduled time. Useful for testing without worrying about delays.
func (m *MockClock) FireAll() int {
	m.mu.Lock()
	var toExecute []func()
	for i := range m.pendingFuncs {
		pf := &m.pendingFuncs[i]
		if !pf.stopped {
			toExecute = append(toExecute, pf.fn)
			pf.stopped = true
		}
	}
	m.mu.Unlock()

	for _, fn := range toExecute {
		fn()
	}
	return len(toExecute)
}

// PendingCount returns the number of scheduled functions that haven't been
// executed or stopped.
func (m *MockClock) PendingCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	count := 0
	for _, pf := range m.pendingFuncs {
		if !pf.stopped {
			count++
		}
	}
	return count
}

// Reset clears all pending scheduled functions and resets time to now.
func (m *MockClock) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pendingFuncs = nil
	m.now = time.Now()
}

// Stop prevents the timer from firing. Returns true if the timer was stopped,
// false if it had already fired or been stopped.
func (t *MockTimer) Stop() bool {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	if t.index < len(t.clock.pendingFuncs) && !t.clock.pendingFuncs[t.index].stopped {
		t.clock.pendingFuncs[t.index].stopped = true
		return true
	}
	return false
}

// MockArrClient implements integration.ArrClient for testing.
// All methods delegate to configurable function fields, allowing test-specific behavior.
type MockArrClient struct {
	FindMediaByPathFunc                 func(path string) (int64, error)
	DeleteFileFunc                      func(mediaID int64, path string) (map[string]interface{}, error)
	GetFilePathFunc                     func(mediaID int64, metadata map[string]interface{}, referencePath string) (string, error)
	GetAllFilePathsFunc                 func(mediaID int64, metadata map[string]interface{}, referencePath string) ([]string, error)
	TriggerSearchFunc                   func(mediaID int64, path string, episodeIDs []int64) error
	GetAllInstancesFunc                 func() ([]*integration.ArrInstanceInfo, error)
	GetInstanceByIDFunc                 func(id int64) (*integration.ArrInstanceInfo, error)
	GetQueueForPathFunc                 func(arrPath string) ([]integration.QueueItemInfo, error)
	FindQueueItemsByMediaIDForPathFunc  func(arrPath string, mediaID int64) ([]integration.QueueItemInfo, error)
	GetDownloadStatusForPathFunc        func(arrPath string, downloadID string) (status string, progress float64, errMsg string, err error)
	GetRecentHistoryForMediaByPathFunc  func(arrPath string, mediaID int64, limit int) ([]integration.HistoryItemInfo, error)
	RemoveFromQueueByPathFunc           func(arrPath string, queueID int64, removeFromClient, blocklist bool) error
	RefreshMonitoredDownloadsByPathFunc func(arrPath string) error
	GetMediaDetailsFunc                 func(mediaID int64, arrPath string) (*integration.MediaDetails, error)

	// Call tracking for assertions
	mu    sync.Mutex
	Calls []MockCall
}

// MockCall records a method call for verification in tests.
type MockCall struct {
	Method string
	Args   []interface{}
}

func (m *MockArrClient) recordCall(method string, args ...interface{}) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Calls = append(m.Calls, MockCall{Method: method, Args: args})
}

// CallCount returns the number of times a method was called.
func (m *MockArrClient) CallCount(method string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	count := 0
	for _, call := range m.Calls {
		if call.Method == method {
			count++
		}
	}
	return count
}

// ResetCalls clears the call history.
func (m *MockArrClient) ResetCalls() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Calls = nil
}

func (m *MockArrClient) FindMediaByPath(path string) (int64, error) {
	m.recordCall("FindMediaByPath", path)
	if m.FindMediaByPathFunc != nil {
		return m.FindMediaByPathFunc(path)
	}
	return 0, nil
}

func (m *MockArrClient) DeleteFile(mediaID int64, path string) (map[string]interface{}, error) {
	m.recordCall("DeleteFile", mediaID, path)
	if m.DeleteFileFunc != nil {
		return m.DeleteFileFunc(mediaID, path)
	}
	return nil, nil
}

func (m *MockArrClient) GetFilePath(mediaID int64, metadata map[string]interface{}, referencePath string) (string, error) {
	m.recordCall("GetFilePath", mediaID, metadata, referencePath)
	if m.GetFilePathFunc != nil {
		return m.GetFilePathFunc(mediaID, metadata, referencePath)
	}
	return "", nil
}

func (m *MockArrClient) GetAllFilePaths(mediaID int64, metadata map[string]interface{}, referencePath string) ([]string, error) {
	m.recordCall("GetAllFilePaths", mediaID, metadata, referencePath)
	if m.GetAllFilePathsFunc != nil {
		return m.GetAllFilePathsFunc(mediaID, metadata, referencePath)
	}
	return nil, nil
}

func (m *MockArrClient) TriggerSearch(mediaID int64, path string, episodeIDs []int64) error {
	m.recordCall("TriggerSearch", mediaID, path, episodeIDs)
	if m.TriggerSearchFunc != nil {
		return m.TriggerSearchFunc(mediaID, path, episodeIDs)
	}
	return nil
}

func (m *MockArrClient) GetAllInstances() ([]*integration.ArrInstanceInfo, error) {
	m.recordCall("GetAllInstances")
	if m.GetAllInstancesFunc != nil {
		return m.GetAllInstancesFunc()
	}
	return nil, nil
}

func (m *MockArrClient) GetInstanceByID(id int64) (*integration.ArrInstanceInfo, error) {
	m.recordCall("GetInstanceByID", id)
	if m.GetInstanceByIDFunc != nil {
		return m.GetInstanceByIDFunc(id)
	}
	return nil, nil
}

func (m *MockArrClient) GetQueueForPath(arrPath string) ([]integration.QueueItemInfo, error) {
	m.recordCall("GetQueueForPath", arrPath)
	if m.GetQueueForPathFunc != nil {
		return m.GetQueueForPathFunc(arrPath)
	}
	return nil, nil
}

func (m *MockArrClient) FindQueueItemsByMediaIDForPath(arrPath string, mediaID int64) ([]integration.QueueItemInfo, error) {
	m.recordCall("FindQueueItemsByMediaIDForPath", arrPath, mediaID)
	if m.FindQueueItemsByMediaIDForPathFunc != nil {
		return m.FindQueueItemsByMediaIDForPathFunc(arrPath, mediaID)
	}
	return nil, nil
}

func (m *MockArrClient) GetDownloadStatusForPath(arrPath string, downloadID string) (status string, progress float64, errMsg string, err error) {
	m.recordCall("GetDownloadStatusForPath", arrPath, downloadID)
	if m.GetDownloadStatusForPathFunc != nil {
		return m.GetDownloadStatusForPathFunc(arrPath, downloadID)
	}
	return "", 0, "", nil
}

func (m *MockArrClient) GetRecentHistoryForMediaByPath(arrPath string, mediaID int64, limit int) ([]integration.HistoryItemInfo, error) {
	m.recordCall("GetRecentHistoryForMediaByPath", arrPath, mediaID, limit)
	if m.GetRecentHistoryForMediaByPathFunc != nil {
		return m.GetRecentHistoryForMediaByPathFunc(arrPath, mediaID, limit)
	}
	return nil, nil
}

func (m *MockArrClient) RemoveFromQueueByPath(arrPath string, queueID int64, removeFromClient, blocklist bool) error {
	m.recordCall("RemoveFromQueueByPath", arrPath, queueID, removeFromClient, blocklist)
	if m.RemoveFromQueueByPathFunc != nil {
		return m.RemoveFromQueueByPathFunc(arrPath, queueID, removeFromClient, blocklist)
	}
	return nil
}

func (m *MockArrClient) RefreshMonitoredDownloadsByPath(arrPath string) error {
	m.recordCall("RefreshMonitoredDownloadsByPath", arrPath)
	if m.RefreshMonitoredDownloadsByPathFunc != nil {
		return m.RefreshMonitoredDownloadsByPathFunc(arrPath)
	}
	return nil
}

func (m *MockArrClient) GetMediaDetails(mediaID int64, arrPath string) (*integration.MediaDetails, error) {
	m.recordCall("GetMediaDetails", mediaID, arrPath)
	if m.GetMediaDetailsFunc != nil {
		return m.GetMediaDetailsFunc(mediaID, arrPath)
	}
	return nil, nil
}

// MockPathMapper implements integration.PathMapper for testing.
type MockPathMapper struct {
	ToArrPathFunc   func(localPath string) (string, error)
	ToLocalPathFunc func(arrPath string) (string, error)
	ReloadFunc      func() error

	mu    sync.Mutex
	Calls []MockCall
}

func (m *MockPathMapper) recordCall(method string, args ...interface{}) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Calls = append(m.Calls, MockCall{Method: method, Args: args})
}

func (m *MockPathMapper) ToArrPath(localPath string) (string, error) {
	m.recordCall("ToArrPath", localPath)
	if m.ToArrPathFunc != nil {
		return m.ToArrPathFunc(localPath)
	}
	// Default: return the same path
	return localPath, nil
}

func (m *MockPathMapper) ToLocalPath(arrPath string) (string, error) {
	m.recordCall("ToLocalPath", arrPath)
	if m.ToLocalPathFunc != nil {
		return m.ToLocalPathFunc(arrPath)
	}
	// Default: return the same path
	return arrPath, nil
}

func (m *MockPathMapper) Reload() error {
	m.recordCall("Reload")
	if m.ReloadFunc != nil {
		return m.ReloadFunc()
	}
	return nil
}

// CallCount returns the number of times a method was called.
func (m *MockPathMapper) CallCount(method string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	count := 0
	for _, call := range m.Calls {
		if call.Method == method {
			count++
		}
	}
	return count
}

// MockHealthChecker implements integration.HealthChecker for testing.
type MockHealthChecker struct {
	CheckFunc           func(path string, mode string) (bool, *integration.HealthCheckError)
	CheckWithConfigFunc func(path string, config integration.DetectionConfig) (bool, *integration.HealthCheckError)

	mu    sync.Mutex
	Calls []MockCall
}

func (m *MockHealthChecker) recordCall(method string, args ...interface{}) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Calls = append(m.Calls, MockCall{Method: method, Args: args})
}

func (m *MockHealthChecker) Check(path string, mode string) (bool, *integration.HealthCheckError) {
	m.recordCall("Check", path, mode)
	if m.CheckFunc != nil {
		return m.CheckFunc(path, mode)
	}
	// Default: file is healthy
	return true, nil
}

func (m *MockHealthChecker) CheckWithConfig(path string, config integration.DetectionConfig) (bool, *integration.HealthCheckError) {
	m.recordCall("CheckWithConfig", path, config)
	if m.CheckWithConfigFunc != nil {
		return m.CheckWithConfigFunc(path, config)
	}
	// Default: file is healthy
	return true, nil
}

// MockEventBus provides a simple in-memory event bus for testing.
// It captures all published events and allows synchronous subscription.
// Implements eventbus.Publisher interface.
type MockEventBus struct {
	mu              sync.Mutex
	PublishedEvents []domain.Event
	Subscribers     map[domain.EventType][]func(domain.Event)
}

// Compile-time assertion that MockEventBus implements eventbus.Publisher
var _ eventbus.Publisher = (*MockEventBus)(nil)

// NewMockEventBus creates a new mock event bus.
func NewMockEventBus() *MockEventBus {
	return &MockEventBus{
		Subscribers: make(map[domain.EventType][]func(domain.Event)),
	}
}

// Publish stores the event and notifies subscribers synchronously.
func (m *MockEventBus) Publish(event domain.Event) error {
	m.mu.Lock()
	m.PublishedEvents = append(m.PublishedEvents, event)
	subscribers := m.Subscribers[event.EventType]
	m.mu.Unlock()

	// Notify subscribers synchronously for deterministic testing
	for _, handler := range subscribers {
		handler(event)
	}
	return nil
}

// Subscribe registers a handler for the given event type.
func (m *MockEventBus) Subscribe(eventType domain.EventType, handler func(domain.Event)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Subscribers[eventType] = append(m.Subscribers[eventType], handler)
}

// GetEvents returns all published events of a given type.
func (m *MockEventBus) GetEvents(eventType domain.EventType) []domain.Event {
	m.mu.Lock()
	defer m.mu.Unlock()
	var result []domain.Event
	for _, e := range m.PublishedEvents {
		if e.EventType == eventType {
			result = append(result, e)
		}
	}
	return result
}

// GetAllEvents returns all published events.
func (m *MockEventBus) GetAllEvents() []domain.Event {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]domain.Event, len(m.PublishedEvents))
	copy(result, m.PublishedEvents)
	return result
}

// Reset clears all published events and subscribers.
func (m *MockEventBus) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.PublishedEvents = nil
	m.Subscribers = make(map[domain.EventType][]func(domain.Event))
}

// EventCount returns the number of events of a given type.
func (m *MockEventBus) EventCount(eventType domain.EventType) int {
	return len(m.GetEvents(eventType))
}

// LastEvent returns the most recently published event, or nil if none.
func (m *MockEventBus) LastEvent() *domain.Event {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.PublishedEvents) == 0 {
		return nil
	}
	return &m.PublishedEvents[len(m.PublishedEvents)-1]
}

// =============================================================================
// MockScannerService - Mock for services.ScannerService
// =============================================================================

// MockScannerService implements the Scanner interface for testing.
// Uses local ScanProgress type to avoid import cycle with services package.
type MockScannerService struct {
	ScanPathFunc            func(pathID int64, localPath string) error
	ScanFileFunc            func(localPath string) error
	GetActiveScansFunc      func() []ScanProgress
	IsPathBeingScanningFunc func(path string) bool
	IsFileBeingScannedFunc  func(localPath string) bool
	PauseScanFunc           func(scanID string) error
	ResumeScanFunc          func(scanID string) error
	CancelScanFunc          func(scanID string) error
	ShutdownFunc            func()

	mu    sync.Mutex
	Calls []MockCall
}

func (m *MockScannerService) recordCall(method string, args ...interface{}) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Calls = append(m.Calls, MockCall{Method: method, Args: args})
}

// CallCount returns the number of times a method was called.
func (m *MockScannerService) CallCount(method string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	count := 0
	for _, call := range m.Calls {
		if call.Method == method {
			count++
		}
	}
	return count
}

// ResetCalls clears the call history.
func (m *MockScannerService) ResetCalls() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Calls = nil
}

func (m *MockScannerService) ScanPath(pathID int64, localPath string) error {
	m.recordCall("ScanPath", pathID, localPath)
	if m.ScanPathFunc != nil {
		return m.ScanPathFunc(pathID, localPath)
	}
	return nil
}

func (m *MockScannerService) ScanFile(localPath string) error {
	m.recordCall("ScanFile", localPath)
	if m.ScanFileFunc != nil {
		return m.ScanFileFunc(localPath)
	}
	return nil
}

func (m *MockScannerService) GetActiveScans() []ScanProgress {
	m.recordCall("GetActiveScans")
	if m.GetActiveScansFunc != nil {
		return m.GetActiveScansFunc()
	}
	return nil
}

func (m *MockScannerService) IsPathBeingScanned(path string) bool {
	m.recordCall("IsPathBeingScanned", path)
	if m.IsPathBeingScanningFunc != nil {
		return m.IsPathBeingScanningFunc(path)
	}
	return false
}

func (m *MockScannerService) IsFileBeingScanned(localPath string) bool {
	m.recordCall("IsFileBeingScanned", localPath)
	if m.IsFileBeingScannedFunc != nil {
		return m.IsFileBeingScannedFunc(localPath)
	}
	return false
}

func (m *MockScannerService) PauseScan(scanID string) error {
	m.recordCall("PauseScan", scanID)
	if m.PauseScanFunc != nil {
		return m.PauseScanFunc(scanID)
	}
	return nil
}

func (m *MockScannerService) ResumeScan(scanID string) error {
	m.recordCall("ResumeScan", scanID)
	if m.ResumeScanFunc != nil {
		return m.ResumeScanFunc(scanID)
	}
	return nil
}

func (m *MockScannerService) CancelScan(scanID string) error {
	m.recordCall("CancelScan", scanID)
	if m.CancelScanFunc != nil {
		return m.CancelScanFunc(scanID)
	}
	return nil
}

func (m *MockScannerService) Shutdown() {
	m.recordCall("Shutdown")
	if m.ShutdownFunc != nil {
		m.ShutdownFunc()
	}
}

// =============================================================================
// MockSchedulerService - Mock for services.SchedulerService
// =============================================================================

// MockSchedulerService implements a mock for services.SchedulerService
type MockSchedulerService struct {
	StartFunc                    func()
	StopFunc                     func()
	LoadSchedulesFunc            func() error
	AddScheduleFunc              func(scanPathID int, cronExpr string) (int64, error)
	DeleteScheduleFunc           func(id int) error
	UpdateScheduleFunc           func(id int, cronExpr string, enabled bool) error
	CleanupOrphanedSchedulesFunc func() (int, error)

	mu    sync.Mutex
	Calls []MockCall
}

func (m *MockSchedulerService) recordCall(method string, args ...interface{}) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Calls = append(m.Calls, MockCall{Method: method, Args: args})
}

// CallCount returns the number of times a method was called.
func (m *MockSchedulerService) CallCount(method string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	count := 0
	for _, call := range m.Calls {
		if call.Method == method {
			count++
		}
	}
	return count
}

// ResetCalls clears the call history.
func (m *MockSchedulerService) ResetCalls() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Calls = nil
}

func (m *MockSchedulerService) Start() {
	m.recordCall("Start")
	if m.StartFunc != nil {
		m.StartFunc()
	}
}

func (m *MockSchedulerService) Stop() {
	m.recordCall("Stop")
	if m.StopFunc != nil {
		m.StopFunc()
	}
}

func (m *MockSchedulerService) LoadSchedules() error {
	m.recordCall("LoadSchedules")
	if m.LoadSchedulesFunc != nil {
		return m.LoadSchedulesFunc()
	}
	return nil
}

func (m *MockSchedulerService) AddSchedule(scanPathID int, cronExpr string) (int64, error) {
	m.recordCall("AddSchedule", scanPathID, cronExpr)
	if m.AddScheduleFunc != nil {
		return m.AddScheduleFunc(scanPathID, cronExpr)
	}
	return 1, nil // Return default ID
}

func (m *MockSchedulerService) DeleteSchedule(id int) error {
	m.recordCall("DeleteSchedule", id)
	if m.DeleteScheduleFunc != nil {
		return m.DeleteScheduleFunc(id)
	}
	return nil
}

func (m *MockSchedulerService) UpdateSchedule(id int, cronExpr string, enabled bool) error {
	m.recordCall("UpdateSchedule", id, cronExpr, enabled)
	if m.UpdateScheduleFunc != nil {
		return m.UpdateScheduleFunc(id, cronExpr, enabled)
	}
	return nil
}

func (m *MockSchedulerService) CleanupOrphanedSchedules() (int, error) {
	m.recordCall("CleanupOrphanedSchedules")
	if m.CleanupOrphanedSchedulesFunc != nil {
		return m.CleanupOrphanedSchedulesFunc()
	}
	return 0, nil
}

// =============================================================================
// MockNotifier - Mock for notifier.Notifier
// =============================================================================

// NotificationConfig mirrors notifier.NotificationConfig for testing
type NotificationConfig struct {
	ID              int64  `json:"id"`
	Name            string `json:"name"`
	ProviderType    string `json:"provider_type"`
	Config          string `json:"config"` // JSON string
	Events          string `json:"events"` // JSON array
	Enabled         bool   `json:"enabled"`
	ThrottleSeconds int    `json:"throttle_seconds"`
}

// NotificationLogEntry mirrors notifier.NotificationLogEntry for testing
type NotificationLogEntry struct {
	ID             int64  `json:"id"`
	NotificationID int64  `json:"notification_id"`
	EventType      string `json:"event_type"`
	Message        string `json:"message"`
	Status         string `json:"status"`
	ErrorMessage   string `json:"error_message"`
	SentAt         string `json:"sent_at"`
}

// MockNotifier implements a mock for notifier.Notifier
type MockNotifier struct {
	StartFunc                func() error
	StopFunc                 func()
	ReloadConfigsFunc        func()
	GetAllConfigsFunc        func() ([]*NotificationConfig, error)
	GetConfigFunc            func(id int64) (*NotificationConfig, error)
	CreateConfigFunc         func(cfg *NotificationConfig) (int64, error)
	UpdateConfigFunc         func(cfg *NotificationConfig) error
	DeleteConfigFunc         func(id int64) error
	SendTestNotificationFunc func(cfg *NotificationConfig) error
	GetNotificationLogFunc   func(notificationID int64, limit int) ([]NotificationLogEntry, error)

	mu    sync.Mutex
	Calls []MockCall
}

func (m *MockNotifier) recordCall(method string, args ...interface{}) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Calls = append(m.Calls, MockCall{Method: method, Args: args})
}

// CallCount returns the number of times a method was called.
func (m *MockNotifier) CallCount(method string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	count := 0
	for _, call := range m.Calls {
		if call.Method == method {
			count++
		}
	}
	return count
}

// ResetCalls clears the call history.
func (m *MockNotifier) ResetCalls() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Calls = nil
}

func (m *MockNotifier) Start() error {
	m.recordCall("Start")
	if m.StartFunc != nil {
		return m.StartFunc()
	}
	return nil
}

func (m *MockNotifier) Stop() {
	m.recordCall("Stop")
	if m.StopFunc != nil {
		m.StopFunc()
	}
}

func (m *MockNotifier) ReloadConfigs() {
	m.recordCall("ReloadConfigs")
	if m.ReloadConfigsFunc != nil {
		m.ReloadConfigsFunc()
	}
}

func (m *MockNotifier) GetAllConfigs() ([]*NotificationConfig, error) {
	m.recordCall("GetAllConfigs")
	if m.GetAllConfigsFunc != nil {
		return m.GetAllConfigsFunc()
	}
	return nil, nil
}

func (m *MockNotifier) GetConfig(id int64) (*NotificationConfig, error) {
	m.recordCall("GetConfig", id)
	if m.GetConfigFunc != nil {
		return m.GetConfigFunc(id)
	}
	return nil, nil
}

func (m *MockNotifier) CreateConfig(cfg *NotificationConfig) (int64, error) {
	m.recordCall("CreateConfig", cfg)
	if m.CreateConfigFunc != nil {
		return m.CreateConfigFunc(cfg)
	}
	return 1, nil
}

func (m *MockNotifier) UpdateConfig(cfg *NotificationConfig) error {
	m.recordCall("UpdateConfig", cfg)
	if m.UpdateConfigFunc != nil {
		return m.UpdateConfigFunc(cfg)
	}
	return nil
}

func (m *MockNotifier) DeleteConfig(id int64) error {
	m.recordCall("DeleteConfig", id)
	if m.DeleteConfigFunc != nil {
		return m.DeleteConfigFunc(id)
	}
	return nil
}

func (m *MockNotifier) SendTestNotification(cfg *NotificationConfig) error {
	m.recordCall("SendTestNotification", cfg)
	if m.SendTestNotificationFunc != nil {
		return m.SendTestNotificationFunc(cfg)
	}
	return nil
}

func (m *MockNotifier) GetNotificationLog(notificationID int64, limit int) ([]NotificationLogEntry, error) {
	m.recordCall("GetNotificationLog", notificationID, limit)
	if m.GetNotificationLogFunc != nil {
		return m.GetNotificationLogFunc(notificationID, limit)
	}
	return nil, nil
}

// =============================================================================
// MockMetricsService - Mock for metrics.MetricsService
// =============================================================================

// MockMetricsService implements a mock for metrics.MetricsService
type MockMetricsService struct {
	HandlerFunc         func() http.Handler
	StartFunc           func()
	ResetStuckCountFunc func()

	mu    sync.Mutex
	Calls []MockCall
}

func (m *MockMetricsService) recordCall(method string, args ...interface{}) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Calls = append(m.Calls, MockCall{Method: method, Args: args})
}

// CallCount returns the number of times a method was called.
func (m *MockMetricsService) CallCount(method string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	count := 0
	for _, call := range m.Calls {
		if call.Method == method {
			count++
		}
	}
	return count
}

func (m *MockMetricsService) Handler() http.Handler {
	m.recordCall("Handler")
	if m.HandlerFunc != nil {
		return m.HandlerFunc()
	}
	// Return a simple handler that returns empty metrics
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
	})
}

func (m *MockMetricsService) Start() {
	m.recordCall("Start")
	if m.StartFunc != nil {
		m.StartFunc()
	}
}

func (m *MockMetricsService) ResetStuckCount() {
	m.recordCall("ResetStuckCount")
	if m.ResetStuckCountFunc != nil {
		m.ResetStuckCountFunc()
	}
}
