// Package cache provides a simple concurrency-safe in-memory key-value store.
//
// It is used to cache the ISRC → Spotify-ID mapping. An ISRC identifies an
// immutable recording, so that mapping never changes and is safe to cache for the
// lifetime of the process without expiry.
package cache

import "sync"

// Memory is a goroutine-safe in-memory map.
type Memory[K comparable, V any] struct {
	mu    sync.RWMutex
	items map[K]V
}

// NewMemory creates an empty in-memory cache.
func NewMemory[K comparable, V any]() *Memory[K, V] {
	return &Memory[K, V]{items: make(map[K]V)}
}

// Get returns the value for key and whether it was present.
func (m *Memory[K, V]) Get(key K) (V, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	v, ok := m.items[key]
	return v, ok
}

// Set stores value under key, overwriting any existing entry.
func (m *Memory[K, V]) Set(key K, value V) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.items[key] = value
}
