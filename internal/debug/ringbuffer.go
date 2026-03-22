package debug

import (
	"sync"
)

// RingBuffer is a thread-safe circular buffer for storing items
type RingBuffer[T any] struct {
	items    []T
	capacity int
	head     int // Next write position
	count    int
	mu       sync.RWMutex
}

// NewRingBuffer creates a new ring buffer with the given capacity
func NewRingBuffer[T any](capacity int) *RingBuffer[T] {
	if capacity <= 0 {
		capacity = 1000
	}
	return &RingBuffer[T]{
		items:    make([]T, capacity),
		capacity: capacity,
	}
}

// Add adds an item to the ring buffer
func (r *RingBuffer[T]) Add(item T) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.items[r.head] = item
	r.head = (r.head + 1) % r.capacity
	if r.count < r.capacity {
		r.count++
	}
}

// GetAll returns all items in the buffer (oldest first)
func (r *RingBuffer[T]) GetAll() []T {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]T, r.count)
	if r.count == 0 {
		return result
	}

	// Calculate start position
	start := 0
	if r.count == r.capacity {
		start = r.head // Buffer is full, head points to oldest item
	}

	for i := 0; i < r.count; i++ {
		idx := (start + i) % r.capacity
		result[i] = r.items[idx]
	}

	return result
}

// GetLast returns the last n items (newest first)
func (r *RingBuffer[T]) GetLast(n int) []T {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if n > r.count {
		n = r.count
	}
	if n <= 0 {
		return []T{}
	}

	result := make([]T, n)

	for i := 0; i < n; i++ {
		// Work backwards from head-1 (most recent)
		idx := (r.head - 1 - i + r.capacity) % r.capacity
		result[i] = r.items[idx]
	}

	return result
}

// GetFiltered returns items that match the filter function (oldest first)
func (r *RingBuffer[T]) GetFiltered(filter func(T) bool, limit int) []T {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if limit <= 0 {
		limit = r.count
	}

	result := make([]T, 0, limit)
	if r.count == 0 {
		return result
	}

	// Calculate start position
	start := 0
	if r.count == r.capacity {
		start = r.head
	}

	for i := 0; i < r.count && len(result) < limit; i++ {
		idx := (start + i) % r.capacity
		if filter(r.items[idx]) {
			result = append(result, r.items[idx])
		}
	}

	return result
}

// Count returns the number of items in the buffer
func (r *RingBuffer[T]) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.count
}

// Clear removes all items from the buffer
func (r *RingBuffer[T]) Clear() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.head = 0
	r.count = 0
	// Zero out the slice to allow GC
	for i := range r.items {
		var zero T
		r.items[i] = zero
	}
}

// Capacity returns the maximum capacity of the buffer
func (r *RingBuffer[T]) Capacity() int {
	return r.capacity
}
