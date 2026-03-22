package debug

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// ErrorEntry represents a recorded error with context
type ErrorEntry struct {
	Timestamp  time.Time      `json:"timestamp"`
	Component  string         `json:"component"`
	ProfileID  string         `json:"profileId,omitempty"`
	Operation  string         `json:"operation"`
	Error      string         `json:"error"`
	StackTrace string         `json:"stackTrace,omitempty"`
	Context    map[string]any `json:"context,omitempty"`
	Resolved   bool           `json:"resolved"`
	ResolvedAt *time.Time     `json:"resolvedAt,omitempty"`
}

// ErrorCollector collects and manages application errors
type ErrorCollector struct {
	buffer *RingBuffer[ErrorEntry]
	mu     sync.RWMutex
}

// Global error collector instance
var (
	globalErrors    *ErrorCollector
	errorsOnce      sync.Once
)

// GetErrorCollector returns the global error collector
func GetErrorCollector() *ErrorCollector {
	errorsOnce.Do(func() {
		globalErrors = NewErrorCollector(1000)
	})
	return globalErrors
}

// NewErrorCollector creates a new error collector with the specified buffer size
func NewErrorCollector(bufferSize int) *ErrorCollector {
	return &ErrorCollector{
		buffer: NewRingBuffer[ErrorEntry](bufferSize),
	}
}

// Record records a new error
func (error_collector *ErrorCollector) Record(component, operation string, err error, context map[string]any) {
	entry := ErrorEntry{
		Timestamp: time.Now(),
		Component: component,
		Operation: operation,
		Error:     err.Error(),
		Context:   context,
		Resolved:  false,
	}
	error_collector.buffer.Add(entry)

	// Also log to the debug logger
	GetLogger().Error(component, fmt.Sprintf("%s: %s", operation, err.Error()), context)
}

// RecordWithProfile records an error with a profile ID
func (error_collector *ErrorCollector) RecordWithProfile(component, profileID, operation string, err error, context map[string]any) {
	entry := ErrorEntry{
		Timestamp: time.Now(),
		Component: component,
		ProfileID: profileID,
		Operation: operation,
		Error:     err.Error(),
		Context:   context,
		Resolved:  false,
	}
	error_collector.buffer.Add(entry)

	// Also log to the debug logger
	fields := make(map[string]any)
	for context_key, context_value := range context {
		fields[context_key] = context_value
	}
	GetLogger().ErrorProfile(component, profileID, fmt.Sprintf("%s: %s", operation, err.Error()), fields)
}

// GetRecent returns the most recent errors
func (error_collector *ErrorCollector) GetRecent(limit int) []ErrorEntry {
	return error_collector.buffer.GetLast(limit)
}

// GetByComponent returns errors for a specific component
func (error_collector *ErrorCollector) GetByComponent(component string, limit int) []ErrorEntry {
	filter := func(entry ErrorEntry) bool {
		return entry.Component == component
	}
	return error_collector.buffer.GetFiltered(filter, limit)
}

// GetByProfile returns errors for a specific profile
func (error_collector *ErrorCollector) GetByProfile(profileID string, limit int) []ErrorEntry {
	filter := func(entry ErrorEntry) bool {
		return entry.ProfileID == profileID
	}
	return error_collector.buffer.GetFiltered(filter, limit)
}

// GetUnresolved returns all unresolved errors
func (error_collector *ErrorCollector) GetUnresolved(limit int) []ErrorEntry {
	filter := func(entry ErrorEntry) bool {
		return !entry.Resolved
	}
	return error_collector.buffer.GetFiltered(filter, limit)
}

// GetRecentJSON returns recent errors as JSON
func (error_collector *ErrorCollector) GetRecentJSON(limit int) (string, error) {
	errors := error_collector.GetRecent(limit)
	data, err := json.Marshal(errors)
	if err != nil {
		return "", fmt.Errorf("failed to marshal errors: %w", err)
	}
	return string(data), nil
}

// Count returns the total number of recorded errors
func (error_collector *ErrorCollector) Count() int {
	return error_collector.buffer.Count()
}

// CountUnresolved returns the number of unresolved errors
func (error_collector *ErrorCollector) CountUnresolved() int {
	errors := error_collector.GetUnresolved(error_collector.buffer.Capacity())
	return len(errors)
}

// Clear removes all error entries
func (error_collector *ErrorCollector) Clear() {
	error_collector.buffer.Clear()
}

// Convenience functions for global error collector

// RecordError records an error to the global collector
func RecordError(component, operation string, err error, context map[string]any) {
	GetErrorCollector().Record(component, operation, err, context)
}

// RecordErrorWithProfile records an error with a profile ID to the global collector
func RecordErrorWithProfile(component, profileID, operation string, err error, context map[string]any) {
	GetErrorCollector().RecordWithProfile(component, profileID, operation, err, context)
}
