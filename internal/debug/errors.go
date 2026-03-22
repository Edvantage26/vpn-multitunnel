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
func (c *ErrorCollector) Record(component, operation string, err error, context map[string]any) {
	entry := ErrorEntry{
		Timestamp: time.Now(),
		Component: component,
		Operation: operation,
		Error:     err.Error(),
		Context:   context,
		Resolved:  false,
	}
	c.buffer.Add(entry)

	// Also log to the debug logger
	GetLogger().Error(component, fmt.Sprintf("%s: %s", operation, err.Error()), context)
}

// RecordWithProfile records an error with a profile ID
func (c *ErrorCollector) RecordWithProfile(component, profileID, operation string, err error, context map[string]any) {
	entry := ErrorEntry{
		Timestamp: time.Now(),
		Component: component,
		ProfileID: profileID,
		Operation: operation,
		Error:     err.Error(),
		Context:   context,
		Resolved:  false,
	}
	c.buffer.Add(entry)

	// Also log to the debug logger
	fields := make(map[string]any)
	for k, v := range context {
		fields[k] = v
	}
	GetLogger().ErrorProfile(component, profileID, fmt.Sprintf("%s: %s", operation, err.Error()), fields)
}

// GetRecent returns the most recent errors
func (c *ErrorCollector) GetRecent(limit int) []ErrorEntry {
	return c.buffer.GetLast(limit)
}

// GetByComponent returns errors for a specific component
func (c *ErrorCollector) GetByComponent(component string, limit int) []ErrorEntry {
	filter := func(entry ErrorEntry) bool {
		return entry.Component == component
	}
	return c.buffer.GetFiltered(filter, limit)
}

// GetByProfile returns errors for a specific profile
func (c *ErrorCollector) GetByProfile(profileID string, limit int) []ErrorEntry {
	filter := func(entry ErrorEntry) bool {
		return entry.ProfileID == profileID
	}
	return c.buffer.GetFiltered(filter, limit)
}

// GetUnresolved returns all unresolved errors
func (c *ErrorCollector) GetUnresolved(limit int) []ErrorEntry {
	filter := func(entry ErrorEntry) bool {
		return !entry.Resolved
	}
	return c.buffer.GetFiltered(filter, limit)
}

// GetRecentJSON returns recent errors as JSON
func (c *ErrorCollector) GetRecentJSON(limit int) (string, error) {
	errors := c.GetRecent(limit)
	data, err := json.Marshal(errors)
	if err != nil {
		return "", fmt.Errorf("failed to marshal errors: %w", err)
	}
	return string(data), nil
}

// Count returns the total number of recorded errors
func (c *ErrorCollector) Count() int {
	return c.buffer.Count()
}

// CountUnresolved returns the number of unresolved errors
func (c *ErrorCollector) CountUnresolved() int {
	errors := c.GetUnresolved(c.buffer.Capacity())
	return len(errors)
}

// Clear removes all error entries
func (c *ErrorCollector) Clear() {
	c.buffer.Clear()
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
