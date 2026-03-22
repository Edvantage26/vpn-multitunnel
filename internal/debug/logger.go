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
func (logger *Logger) SetMinLevel(level LogLevel) {
	logger.mu.Lock()
	defer logger.mu.Unlock()
	logger.minLevel = level
}

// SetEnabled enables or disables logging
func (logger *Logger) SetEnabled(enabled bool) {
	logger.mu.Lock()
	defer logger.mu.Unlock()
	logger.enabled = enabled
}

// AddListener adds a listener that will be called for each log entry
func (logger *Logger) AddListener(listener func(LogEntry)) {
	logger.mu.Lock()
	defer logger.mu.Unlock()
	logger.listeners = append(logger.listeners, listener)
}

// shouldLog returns true if the level should be logged
func (logger *Logger) shouldLog(level LogLevel) bool {
	logger.mu.RLock()
	defer logger.mu.RUnlock()

	if !logger.enabled {
		return false
	}

	levels := map[LogLevel]int{
		LevelDebug: 0,
		LevelInfo:  1,
		LevelWarn:  2,
		LevelError: 3,
	}

	return levels[level] >= levels[logger.minLevel]
}

// log records a log entry
func (logger *Logger) log(level LogLevel, component, profileID, message string, fields map[string]any) {
	if !logger.shouldLog(level) {
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

	logger.buffer.Add(entry)

	// Also log to standard logger for console output
	if level == LevelError || level == LevelWarn {
		log.Printf("[%s] [%s] %s: %s", level, component, profileID, message)
	}

	// Notify listeners
	logger.mu.RLock()
	listeners := logger.listeners
	logger.mu.RUnlock()

	for _, listener := range listeners {
		listener(entry)
	}
}

// Debug logs a debug message
func (logger *Logger) Debug(component, message string, fields map[string]any) {
	logger.log(LevelDebug, component, "", message, fields)
}

// DebugProfile logs a debug message for a specific profile
func (logger *Logger) DebugProfile(component, profileID, message string, fields map[string]any) {
	logger.log(LevelDebug, component, profileID, message, fields)
}

// Info logs an info message
func (logger *Logger) Info(component, message string, fields map[string]any) {
	logger.log(LevelInfo, component, "", message, fields)
}

// InfoProfile logs an info message for a specific profile
func (logger *Logger) InfoProfile(component, profileID, message string, fields map[string]any) {
	logger.log(LevelInfo, component, profileID, message, fields)
}

// Warn logs a warning message
func (logger *Logger) Warn(component, message string, fields map[string]any) {
	logger.log(LevelWarn, component, "", message, fields)
}

// WarnProfile logs a warning message for a specific profile
func (logger *Logger) WarnProfile(component, profileID, message string, fields map[string]any) {
	logger.log(LevelWarn, component, profileID, message, fields)
}

// Error logs an error message
func (logger *Logger) Error(component, message string, fields map[string]any) {
	logger.log(LevelError, component, "", message, fields)
}

// ErrorProfile logs an error message for a specific profile
func (logger *Logger) ErrorProfile(component, profileID, message string, fields map[string]any) {
	logger.log(LevelError, component, profileID, message, fields)
}

// GetLogs returns the most recent log entries
func (logger *Logger) GetLogs(limit int) []LogEntry {
	return logger.buffer.GetLast(limit)
}

// GetLogsFiltered returns filtered log entries
func (logger *Logger) GetLogsFiltered(level LogLevel, component, profileID string, limit int) []LogEntry {
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

	return logger.buffer.GetFiltered(filter, limit)
}

// GetLogsJSON returns logs as a JSON string
func (logger *Logger) GetLogsJSON(limit int) (string, error) {
	logs := logger.GetLogs(limit)
	data, err := json.Marshal(logs)
	if err != nil {
		return "", fmt.Errorf("failed to marshal logs: %w", err)
	}
	return string(data), nil
}

// Clear removes all log entries
func (logger *Logger) Clear() {
	logger.buffer.Clear()
}

// Count returns the number of log entries
func (logger *Logger) Count() int {
	return logger.buffer.Count()
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
