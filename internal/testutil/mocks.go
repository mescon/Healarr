// Package testutil provides test utilities including mocks, fixtures, and test database helpers.
package testutil

import (
	"sync"

	"github.com/mescon/Healarr/internal/domain"
	"github.com/mescon/Healarr/internal/eventbus"
	"github.com/mescon/Healarr/internal/integration"
)

// MockArrClient implements integration.ArrClient for testing.
// All methods delegate to configurable function fields, allowing test-specific behavior.
type MockArrClient struct {
	FindMediaByPathFunc             func(path string) (int64, error)
	DeleteFileFunc                  func(mediaID int64, path string) (map[string]interface{}, error)
	GetFilePathFunc                 func(mediaID int64, metadata map[string]interface{}, referencePath string) (string, error)
	GetAllFilePathsFunc             func(mediaID int64, metadata map[string]interface{}, referencePath string) ([]string, error)
	TriggerSearchFunc               func(mediaID int64, path string, episodeIDs []int64) error
	GetAllInstancesFunc             func() ([]*integration.ArrInstanceInfo, error)
	GetInstanceByIDFunc             func(id int64) (*integration.ArrInstanceInfo, error)
	GetQueueForPathFunc                  func(arrPath string) ([]integration.QueueItemInfo, error)
	FindQueueItemsByMediaIDForPathFunc   func(arrPath string, mediaID int64) ([]integration.QueueItemInfo, error)
	GetDownloadStatusForPathFunc         func(arrPath string, downloadID string) (status string, progress float64, errMsg string, err error)
	GetRecentHistoryForMediaByPathFunc   func(arrPath string, mediaID int64, limit int) ([]integration.HistoryItemInfo, error)
	RemoveFromQueueByPathFunc            func(arrPath string, queueID int64, removeFromClient, blocklist bool) error
	RefreshMonitoredDownloadsByPathFunc  func(arrPath string) error

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
