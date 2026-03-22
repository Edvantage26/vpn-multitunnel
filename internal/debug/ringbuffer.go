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
func (ring_buffer *RingBuffer[T]) Add(item T) {
	ring_buffer.mu.Lock()
	defer ring_buffer.mu.Unlock()

	ring_buffer.items[ring_buffer.head] = item
	ring_buffer.head = (ring_buffer.head + 1) % ring_buffer.capacity
	if ring_buffer.count < ring_buffer.capacity {
		ring_buffer.count++
	}
}

// GetAll returns all items in the buffer (oldest first)
func (ring_buffer *RingBuffer[T]) GetAll() []T {
	ring_buffer.mu.RLock()
	defer ring_buffer.mu.RUnlock()

	result := make([]T, ring_buffer.count)
	if ring_buffer.count == 0 {
		return result
	}

	// Calculate start position
	start := 0
	if ring_buffer.count == ring_buffer.capacity {
		start = ring_buffer.head // Buffer is full, head points to oldest item
	}

	for idx_item := 0; idx_item < ring_buffer.count; idx_item++ {
		idx := (start + idx_item) % ring_buffer.capacity
		result[idx_item] = ring_buffer.items[idx]
	}

	return result
}

// GetLast returns the last n items (newest first)
func (ring_buffer *RingBuffer[T]) GetLast(item_count int) []T {
	ring_buffer.mu.RLock()
	defer ring_buffer.mu.RUnlock()

	if item_count > ring_buffer.count {
		item_count = ring_buffer.count
	}
	if item_count <= 0 {
		return []T{}
	}

	result := make([]T, item_count)

	for idx_item := 0; idx_item < item_count; idx_item++ {
		// Work backwards from head-1 (most recent)
		idx := (ring_buffer.head - 1 - idx_item + ring_buffer.capacity) % ring_buffer.capacity
		result[idx_item] = ring_buffer.items[idx]
	}

	return result
}

// GetFiltered returns items that match the filter function (oldest first)
func (ring_buffer *RingBuffer[T]) GetFiltered(filter func(T) bool, limit int) []T {
	ring_buffer.mu.RLock()
	defer ring_buffer.mu.RUnlock()

	if limit <= 0 {
		limit = ring_buffer.count
	}

	result := make([]T, 0, limit)
	if ring_buffer.count == 0 {
		return result
	}

	// Calculate start position
	start := 0
	if ring_buffer.count == ring_buffer.capacity {
		start = ring_buffer.head
	}

	for idx_item := 0; idx_item < ring_buffer.count && len(result) < limit; idx_item++ {
		idx := (start + idx_item) % ring_buffer.capacity
		if filter(ring_buffer.items[idx]) {
			result = append(result, ring_buffer.items[idx])
		}
	}

	return result
}

// Count returns the number of items in the buffer
func (ring_buffer *RingBuffer[T]) Count() int {
	ring_buffer.mu.RLock()
	defer ring_buffer.mu.RUnlock()
	return ring_buffer.count
}

// Clear removes all items from the buffer
func (ring_buffer *RingBuffer[T]) Clear() {
	ring_buffer.mu.Lock()
	defer ring_buffer.mu.Unlock()
	ring_buffer.head = 0
	ring_buffer.count = 0
	// Zero out the slice to allow GC
	for idx_item := range ring_buffer.items {
		var zero T
		ring_buffer.items[idx_item] = zero
	}
}

// Capacity returns the maximum capacity of the buffer
func (ring_buffer *RingBuffer[T]) Capacity() int {
	return ring_buffer.capacity
}
