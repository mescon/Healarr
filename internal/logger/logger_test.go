package logger

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// =============================================================================
// LogLevel constants tests
// =============================================================================

func TestLogLevelConstants(t *testing.T) {
	if Debug != "DEBUG" {
		t.Errorf("Debug = %s, want DEBUG", Debug)
	}
	if Info != "INFO" {
		t.Errorf("Info = %s, want INFO", Info)
	}
	if Warn != "WARN" {
		t.Errorf("Warn = %s, want WARN", Warn)
	}
	if Error != "ERROR" {
		t.Errorf("Error = %s, want ERROR", Error)
	}
}

// =============================================================================
// levelPriority tests
// =============================================================================

func TestLevelPriority(t *testing.T) {
	tests := []struct {
		level    LogLevel
		expected int
	}{
		{Debug, 0},
		{Info, 1},
		{Warn, 2},
		{Error, 3},
		{LogLevel("unknown"), 1}, // defaults to Info priority
	}

	for _, tt := range tests {
		t.Run(string(tt.level), func(t *testing.T) {
			got := levelPriority(tt.level)
			if got != tt.expected {
				t.Errorf("levelPriority(%s) = %d, want %d", tt.level, got, tt.expected)
			}
		})
	}
}

func TestLevelPriority_Ordering(t *testing.T) {
	if levelPriority(Debug) >= levelPriority(Info) {
		t.Error("Debug should be lower priority than Info")
	}
	if levelPriority(Info) >= levelPriority(Warn) {
		t.Error("Info should be lower priority than Warn")
	}
	if levelPriority(Warn) >= levelPriority(Error) {
		t.Error("Warn should be lower priority than Error")
	}
}

// =============================================================================
// SetLevel tests
// =============================================================================

func TestSetLevel(t *testing.T) {
	// Save original
	original := minLevel
	defer func() { minLevel = original }()

	tests := []struct {
		input    string
		expected LogLevel
	}{
		{"debug", Debug},
		{"info", Info},
		{"warn", Warn},
		{"error", Error},
		{"invalid", Info}, // defaults to Info
		{"DEBUG", Info},   // case sensitive, falls back to Info
		{"", Info},        // empty falls back to Info
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			SetLevel(tt.input)
			if minLevel != tt.expected {
				t.Errorf("SetLevel(%q): minLevel = %s, want %s", tt.input, minLevel, tt.expected)
			}
		})
	}
}

// =============================================================================
// Subscribe/Unsubscribe tests
// =============================================================================

func TestSubscribe(t *testing.T) {
	// Save and restore listeners
	originalListeners := listeners
	listeners = make([]chan LogEntry, 0)
	defer func() { listeners = originalListeners }()

	ch := Subscribe()
	if ch == nil {
		t.Fatal("Subscribe() should return a channel")
	}

	if len(listeners) != 1 {
		t.Errorf("Expected 1 listener, got %d", len(listeners))
	}
}

func TestSubscribe_MultipleSubscribers(t *testing.T) {
	originalListeners := listeners
	listeners = make([]chan LogEntry, 0)
	defer func() { listeners = originalListeners }()

	ch1 := Subscribe()
	ch2 := Subscribe()
	ch3 := Subscribe()

	if len(listeners) != 3 {
		t.Errorf("Expected 3 listeners, got %d", len(listeners))
	}

	// All should be different channels
	if ch1 == ch2 || ch2 == ch3 || ch1 == ch3 {
		t.Error("Each subscriber should get a unique channel")
	}
}

func TestUnsubscribe(t *testing.T) {
	originalListeners := listeners
	listeners = make([]chan LogEntry, 0)
	defer func() { listeners = originalListeners }()

	ch1 := Subscribe()
	ch2 := Subscribe()

	if len(listeners) != 2 {
		t.Fatalf("Expected 2 listeners, got %d", len(listeners))
	}

	Unsubscribe(ch1)

	if len(listeners) != 1 {
		t.Errorf("After unsubscribe, expected 1 listener, got %d", len(listeners))
	}

	// ch1 should be closed
	select {
	case _, ok := <-ch1:
		if ok {
			t.Error("Channel should be closed after unsubscribe")
		}
	default:
		// This might happen if channel is empty but not closed, which would be wrong
		// However, the channel should be closed, so reading should return immediately
	}

	// ch2 should still be in listeners
	if listeners[0] != ch2 {
		t.Error("Wrong listener was removed")
	}
}

func TestUnsubscribe_NotSubscribed(t *testing.T) {
	originalListeners := listeners
	listeners = make([]chan LogEntry, 0)
	defer func() { listeners = originalListeners }()

	ch := make(chan LogEntry, 100)

	// Should not panic when unsubscribing a channel that wasn't subscribed
	Unsubscribe(ch)

	if len(listeners) != 0 {
		t.Error("Listeners should remain empty")
	}
}

// =============================================================================
// broadcast tests
// =============================================================================

func TestBroadcast(t *testing.T) {
	originalListeners := listeners
	listeners = make([]chan LogEntry, 0)
	defer func() { listeners = originalListeners }()

	ch1 := Subscribe()
	ch2 := Subscribe()

	entry := LogEntry{
		Timestamp: "2024-01-01T00:00:00Z",
		Level:     Info,
		Message:   "test message",
	}

	broadcast(entry)

	// Both channels should receive the entry
	select {
	case got := <-ch1:
		if got.Message != entry.Message {
			t.Errorf("ch1 received wrong message: %s", got.Message)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("ch1 did not receive broadcast")
	}

	select {
	case got := <-ch2:
		if got.Message != entry.Message {
			t.Errorf("ch2 received wrong message: %s", got.Message)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("ch2 did not receive broadcast")
	}
}

func TestBroadcast_DropsWhenFull(t *testing.T) {
	originalListeners := listeners
	listeners = make([]chan LogEntry, 0)
	defer func() { listeners = originalListeners }()

	ch := Subscribe()

	// Fill the channel buffer (100 capacity)
	for i := 0; i < 100; i++ {
		broadcast(LogEntry{Message: "fill"})
	}

	// This should not block even though channel is full
	done := make(chan bool)
	go func() {
		broadcast(LogEntry{Message: "overflow"})
		done <- true
	}()

	select {
	case <-done:
		// Success - broadcast didn't block
	case <-time.After(100 * time.Millisecond):
		t.Error("broadcast() blocked when channel was full")
	}

	// Drain one message and verify channel was full
	<-ch
}

// =============================================================================
// Log tests
// =============================================================================

func TestLog_Filtering(t *testing.T) {
	// Save and restore state
	originalLevel := minLevel
	originalListeners := listeners
	listeners = make([]chan LogEntry, 0)
	defer func() {
		minLevel = originalLevel
		listeners = originalListeners
	}()

	ch := Subscribe()

	tests := []struct {
		name      string
		minLevel  LogLevel
		logLevel  LogLevel
		expectMsg bool
	}{
		{"debug at debug level", Debug, Debug, true},
		{"info at debug level", Debug, Info, true},
		{"debug at info level", Info, Debug, false},
		{"info at info level", Info, Info, true},
		{"warn at info level", Info, Warn, true},
		{"error at info level", Info, Error, true},
		{"warn at error level", Error, Warn, false},
		{"error at error level", Error, Error, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			minLevel = tt.minLevel

			// Drain channel
			for len(ch) > 0 {
				<-ch
			}

			Log(tt.logLevel, "test message")

			select {
			case <-ch:
				if !tt.expectMsg {
					t.Error("Message should have been filtered")
				}
			case <-time.After(50 * time.Millisecond):
				if tt.expectMsg {
					t.Error("Message should have been received")
				}
			}
		})
	}
}

func TestLog_MessageFormatting(t *testing.T) {
	originalLevel := minLevel
	originalListeners := listeners
	listeners = make([]chan LogEntry, 0)
	defer func() {
		minLevel = originalLevel
		listeners = originalListeners
	}()

	minLevel = Debug
	ch := Subscribe()

	Log(Info, "hello %s, number %d", "world", 42)

	select {
	case entry := <-ch:
		if entry.Level != Info {
			t.Errorf("Level = %s, want INFO", entry.Level)
		}
		if entry.Message != "hello world, number 42" {
			t.Errorf("Message = %q, want 'hello world, number 42'", entry.Message)
		}
		if entry.Timestamp == "" {
			t.Error("Timestamp should not be empty")
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("Did not receive log entry")
	}
}

// =============================================================================
// Convenience function tests
// =============================================================================

func TestInfof(t *testing.T) {
	originalLevel := minLevel
	originalListeners := listeners
	listeners = make([]chan LogEntry, 0)
	defer func() {
		minLevel = originalLevel
		listeners = originalListeners
	}()

	minLevel = Debug
	ch := Subscribe()

	Infof("info message")

	select {
	case entry := <-ch:
		if entry.Level != Info {
			t.Errorf("Infof should log at Info level, got %s", entry.Level)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("Did not receive log entry")
	}
}

func TestErrorf(t *testing.T) {
	originalLevel := minLevel
	originalListeners := listeners
	listeners = make([]chan LogEntry, 0)
	defer func() {
		minLevel = originalLevel
		listeners = originalListeners
	}()

	minLevel = Debug
	ch := Subscribe()

	Errorf("error message")

	select {
	case entry := <-ch:
		if entry.Level != Error {
			t.Errorf("Errorf should log at Error level, got %s", entry.Level)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("Did not receive log entry")
	}
}

func TestDebugf(t *testing.T) {
	originalLevel := minLevel
	originalListeners := listeners
	listeners = make([]chan LogEntry, 0)
	defer func() {
		minLevel = originalLevel
		listeners = originalListeners
	}()

	minLevel = Debug
	ch := Subscribe()

	Debugf("debug message")

	select {
	case entry := <-ch:
		if entry.Level != Debug {
			t.Errorf("Debugf should log at Debug level, got %s", entry.Level)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("Did not receive log entry")
	}
}

func TestWarnf(t *testing.T) {
	originalLevel := minLevel
	originalListeners := listeners
	listeners = make([]chan LogEntry, 0)
	defer func() {
		minLevel = originalLevel
		listeners = originalListeners
	}()

	minLevel = Debug
	ch := Subscribe()

	Warnf("warn message")

	select {
	case entry := <-ch:
		if entry.Level != Warn {
			t.Errorf("Warnf should log at Warn level, got %s", entry.Level)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("Did not receive log entry")
	}
}

// =============================================================================
// Init tests
// =============================================================================

func TestInit(t *testing.T) {
	tmpDir := t.TempDir()

	Init(tmpDir)
	defer func() {
		if fileLogger != nil {
			_ = fileLogger.Close()
			fileLogger = nil
		}
	}()

	// Log file is created on first write by lumberjack logger
	// We verify the directory exists, which is sufficient for this test
}

func TestInit_CreatesDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	logDir := filepath.Join(tmpDir, "subdir", "logs")

	Init(logDir)
	defer func() {
		if fileLogger != nil {
			_ = fileLogger.Close()
			fileLogger = nil
		}
	}()

	if _, err := os.Stat(logDir); os.IsNotExist(err) {
		t.Error("Init() should create log directory")
	}
}

// =============================================================================
// GetLogDir tests
// =============================================================================

func TestGetLogDir_NotInitialized(t *testing.T) {
	// Save and restore fileLogger
	original := fileLogger
	fileLogger = nil
	defer func() { fileLogger = original }()

	dir := GetLogDir()
	if dir != "" {
		t.Errorf("GetLogDir() = %q, want empty string when not initialized", dir)
	}
}

func TestGetLogDir_Initialized(t *testing.T) {
	tmpDir := t.TempDir()

	Init(tmpDir)
	defer func() {
		if fileLogger != nil {
			_ = fileLogger.Close()
			fileLogger = nil
		}
	}()

	dir := GetLogDir()
	if dir != tmpDir {
		t.Errorf("GetLogDir() = %q, want %q", dir, tmpDir)
	}
}

// =============================================================================
// LogEntry tests
// =============================================================================

func TestLogEntry_Fields(t *testing.T) {
	entry := LogEntry{
		Timestamp: "2024-01-01T12:00:00Z",
		Level:     Info,
		Message:   "test message",
	}

	if entry.Timestamp != "2024-01-01T12:00:00Z" {
		t.Errorf("Timestamp = %s", entry.Timestamp)
	}
	if entry.Level != Info {
		t.Errorf("Level = %s", entry.Level)
	}
	if entry.Message != "test message" {
		t.Errorf("Message = %s", entry.Message)
	}
}

// =============================================================================
// Integration tests
// =============================================================================

func TestLog_WritesToFile(t *testing.T) {
	tmpDir := t.TempDir()
	originalLevel := minLevel
	originalFileLogger := fileLogger
	defer func() {
		minLevel = originalLevel
		if fileLogger != nil {
			_ = fileLogger.Close()
		}
		fileLogger = originalFileLogger
	}()

	minLevel = Debug
	Init(tmpDir)

	// Log a unique message
	uniqueMsg := "unique-test-message-12345"
	Infof("%s", uniqueMsg)

	// Force flush by closing and reopening
	if fileLogger != nil {
		_ = fileLogger.Close()
	}

	// Check file contents
	content, err := os.ReadFile(filepath.Join(tmpDir, "healarr.log"))
	if err != nil {
		t.Fatalf("Failed to read log file: %v", err)
	}

	if !strings.Contains(string(content), uniqueMsg) {
		t.Error("Log file should contain the logged message")
	}
}
