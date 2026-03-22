package debug

import (
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"
)

// LogLevel represents the severity of a log entry
type LogLevel string

const (
	LevelDebug LogLevel = "debug"
	LevelInfo  LogLevel = "info"
	LevelWarn  LogLevel = "warn"
	LevelError LogLevel = "error"
)

// LogEntry represents a structured log entry
type LogEntry struct {
	Timestamp time.Time      `json:"timestamp"`
	Level     LogLevel       `json:"level"`
	Component string         `json:"component"`
	ProfileID string         `json:"profileId,omitempty"`
	Message   string         `json:"message"`
	Fields    map[string]any `json:"fields,omitempty"`
}

// Logger is a structured logger that stores entries in a ring buffer
type Logger struct {
	buffer    *RingBuffer[LogEntry]
	minLevel  LogLevel
	enabled   bool
	mu        sync.RWMutex
	listeners []func(LogEntry)
}

// Global logger instance
var (
	globalLogger *Logger
	loggerOnce   sync.Once
)

// GetLogger returns the global logger instance
func GetLogger() *Logger {
	loggerOnce.Do(func() {
		globalLogger = NewLogger(10000)
	})
	return globalLogger
}

// NewLogger creates a new logger with the specified buffer size
func NewLogger(bufferSize int) *Logger {
	return &Logger{
		buffer:   NewRingBuffer[LogEntry](bufferSize),
		minLevel: LevelDebug,
		enabled:  true,
	}
}

// SetMinLevel sets the minimum log level to record
func (l *Logger) SetMinLevel(level LogLevel) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.minLevel = level
}

// SetEnabled enables or disables logging
func (l *Logger) SetEnabled(enabled bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.enabled = enabled
}

// AddListener adds a listener that will be called for each log entry
func (l *Logger) AddListener(listener func(LogEntry)) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.listeners = append(l.listeners, listener)
}

// shouldLog returns true if the level should be logged
func (l *Logger) shouldLog(level LogLevel) bool {
	l.mu.RLock()
	defer l.mu.RUnlock()

	if !l.enabled {
		return false
	}

	levels := map[LogLevel]int{
		LevelDebug: 0,
		LevelInfo:  1,
		LevelWarn:  2,
		LevelError: 3,
	}

	return levels[level] >= levels[l.minLevel]
}

// log records a log entry
func (l *Logger) log(level LogLevel, component, profileID, message string, fields map[string]any) {
	if !l.shouldLog(level) {
		return
	}

	entry := LogEntry{
		Timestamp: time.Now(),
		Level:     level,
		Component: component,
		ProfileID: profileID,
		Message:   message,
		Fields:    fields,
	}

	l.buffer.Add(entry)

	// Also log to standard logger for console output
	if level == LevelError || level == LevelWarn {
		log.Printf("[%s] [%s] %s: %s", level, component, profileID, message)
	}

	// Notify listeners
	l.mu.RLock()
	listeners := l.listeners
	l.mu.RUnlock()

	for _, listener := range listeners {
		listener(entry)
	}
}

// Debug logs a debug message
func (l *Logger) Debug(component, message string, fields map[string]any) {
	l.log(LevelDebug, component, "", message, fields)
}

// DebugProfile logs a debug message for a specific profile
func (l *Logger) DebugProfile(component, profileID, message string, fields map[string]any) {
	l.log(LevelDebug, component, profileID, message, fields)
}

// Info logs an info message
func (l *Logger) Info(component, message string, fields map[string]any) {
	l.log(LevelInfo, component, "", message, fields)
}

// InfoProfile logs an info message for a specific profile
func (l *Logger) InfoProfile(component, profileID, message string, fields map[string]any) {
	l.log(LevelInfo, component, profileID, message, fields)
}

// Warn logs a warning message
func (l *Logger) Warn(component, message string, fields map[string]any) {
	l.log(LevelWarn, component, "", message, fields)
}

// WarnProfile logs a warning message for a specific profile
func (l *Logger) WarnProfile(component, profileID, message string, fields map[string]any) {
	l.log(LevelWarn, component, profileID, message, fields)
}

// Error logs an error message
func (l *Logger) Error(component, message string, fields map[string]any) {
	l.log(LevelError, component, "", message, fields)
}

// ErrorProfile logs an error message for a specific profile
func (l *Logger) ErrorProfile(component, profileID, message string, fields map[string]any) {
	l.log(LevelError, component, profileID, message, fields)
}

// GetLogs returns the most recent log entries
func (l *Logger) GetLogs(limit int) []LogEntry {
	return l.buffer.GetLast(limit)
}

// GetLogsFiltered returns filtered log entries
func (l *Logger) GetLogsFiltered(level LogLevel, component, profileID string, limit int) []LogEntry {
	filter := func(entry LogEntry) bool {
		if level != "" && entry.Level != level {
			return false
		}
		if component != "" && entry.Component != component {
			return false
		}
		if profileID != "" && entry.ProfileID != profileID {
			return false
		}
		return true
	}

	return l.buffer.GetFiltered(filter, limit)
}

// GetLogsJSON returns logs as a JSON string
func (l *Logger) GetLogsJSON(limit int) (string, error) {
	logs := l.GetLogs(limit)
	data, err := json.Marshal(logs)
	if err != nil {
		return "", fmt.Errorf("failed to marshal logs: %w", err)
	}
	return string(data), nil
}

// Clear removes all log entries
func (l *Logger) Clear() {
	l.buffer.Clear()
}

// Count returns the number of log entries
func (l *Logger) Count() int {
	return l.buffer.Count()
}

// Convenience functions for global logger

// Debug logs a debug message to the global logger
func Debug(component, message string, fields map[string]any) {
	GetLogger().Debug(component, message, fields)
}

// Info logs an info message to the global logger
func Info(component, message string, fields map[string]any) {
	GetLogger().Info(component, message, fields)
}

// Warn logs a warning message to the global logger
func Warn(component, message string, fields map[string]any) {
	GetLogger().Warn(component, message, fields)
}

// Error logs an error message to the global logger
func Error(component, message string, fields map[string]any) {
	GetLogger().Error(component, message, fields)
}
