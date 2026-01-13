package logger

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"gopkg.in/natefinch/lumberjack.v2"
)

// LogLevel represents the severity level of a log message.
type LogLevel string

const (
	Debug LogLevel = "DEBUG"
	Info  LogLevel = "INFO"
	Warn  LogLevel = "WARN"
	Error LogLevel = "ERROR"
)

// minLevel is the minimum log level to output. Messages below this level are filtered.
var minLevel LogLevel = Info

// levelPriority returns the numeric priority of a log level (higher = more severe)
func levelPriority(level LogLevel) int {
	switch level {
	case Debug:
		return 0
	case Info:
		return 1
	case Warn:
		return 2
	case Error:
		return 3
	default:
		return 1
	}
}

// SetLevel sets the minimum log level. Valid values: "debug", "info", "warn", "error"
func SetLevel(level string) {
	switch level {
	case "debug":
		minLevel = Debug
	case "info":
		minLevel = Info
	case "warn":
		minLevel = Warn
	case "error":
		minLevel = Error
	default:
		minLevel = Info
	}
	log.Printf("Log level set to: %s", minLevel)
}

// LogEntry represents a single log message with metadata for streaming to clients.
type LogEntry struct {
	Timestamp string   `json:"timestamp"`
	Level     LogLevel `json:"level"`
	Message   string   `json:"message"`
}

var (
	listeners  []chan LogEntry
	mu         sync.Mutex
	fileLogger *lumberjack.Logger
)

func init() {
	listeners = make([]chan LogEntry, 0)
	// Default to stdout only until Init() is called with proper config
	log.SetOutput(os.Stdout)
	log.SetFlags(0) // Disable standard flags (date/time) so we can control format
}

// Init initializes the logger with the specified log directory.
// Should be called after config is loaded.
func Init(logDir string) {
	// Ensure logs directory exists with restricted permissions (owner only)
	if err := os.MkdirAll(logDir, 0700); err != nil {
		log.Printf("Failed to create log directory: %v", err)
		return
	}

	// Configure lumberjack for log rotation
	fileLogger = &lumberjack.Logger{
		Filename:   filepath.Join(logDir, "healarr.log"),
		MaxSize:    100, // megabytes
		MaxBackups: 3,
		MaxAge:     28,   // days
		Compress:   true, // disabled by default
	}

	// Write to both stdout and file
	mw := io.MultiWriter(os.Stdout, fileLogger)
	log.SetOutput(mw)
}

// GetLogDir returns the directory where log files are stored
func GetLogDir() string {
	if fileLogger != nil {
		return filepath.Dir(fileLogger.Filename)
	}
	return ""
}

// Subscribe returns a channel that receives all log entries for real-time streaming.
func Subscribe() chan LogEntry {
	mu.Lock()
	defer mu.Unlock()
	ch := make(chan LogEntry, 100)
	listeners = append(listeners, ch)
	return ch
}

// Unsubscribe removes a log listener channel and closes it.
func Unsubscribe(ch chan LogEntry) {
	mu.Lock()
	defer mu.Unlock()
	for i, l := range listeners {
		if l == ch {
			listeners = append(listeners[:i], listeners[i+1:]...)
			close(ch)
			break
		}
	}
}

func broadcast(entry LogEntry) {
	mu.Lock()
	defer mu.Unlock()
	for _, ch := range listeners {
		select {
		case ch <- entry:
		default:
			// Drop message if channel is full to prevent blocking
		}
	}
}

// Log writes a formatted message at the specified level to stdout, file, and subscribers.
func Log(level LogLevel, format string, v ...interface{}) {
	// Filter messages below minimum level
	if levelPriority(level) < levelPriority(minLevel) {
		return
	}

	msg := fmt.Sprintf(format, v...)
	timestamp := time.Now().Format(time.RFC3339)

	// Print to stdout and file (via log.SetOutput in init)
	// Format: timestamp [LEVEL] message
	log.Printf("%s [%s] %s", timestamp, level, msg)

	// Broadcast
	broadcast(LogEntry{
		Timestamp: timestamp,
		Level:     level,
		Message:   msg,
	})
}

// Infof logs a formatted message at INFO level.
func Infof(format string, v ...interface{}) {
	Log(Info, format, v...)
}

// Errorf logs a formatted message at ERROR level.
func Errorf(format string, v ...interface{}) {
	Log(Error, format, v...)
}

// Debugf logs a formatted message at DEBUG level.
func Debugf(format string, v ...interface{}) {
	Log(Debug, format, v...)
}

// Warnf logs a formatted message at WARN level.
func Warnf(format string, v ...interface{}) {
	Log(Warn, format, v...)
}
