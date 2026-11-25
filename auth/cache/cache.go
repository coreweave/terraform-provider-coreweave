// Package cache coordinates storage of opaque authentication credentials.
package cache

import (
	"context"
	"slices"
	"sync"
)

// Cache stores credential records. A cache shared by processes must
// synchronize access across those processes.
type Cache interface {
	// WithEntry gives use exclusive access to key for the duration of the call.
	// Waiting for access must honor ctx. The entry is only valid during use.
	WithEntry(ctx context.Context, key string, use func(Entry) error) error
}

// Entry stores an opaque credential record.
type Entry interface {
	// Load returns nil, nil when no record exists.
	Load() ([]byte, error)
	Save([]byte) error
	Delete() error
}

// NewMemory creates an in-process credential cache.
func NewMemory() Cache {
	return &memoryCache{entries: make(map[string]*memoryCacheEntry)}
}

type memoryCache struct {
	mu      sync.Mutex
	entries map[string]*memoryCacheEntry
}

type memoryCacheEntry struct {
	gate  chan struct{}
	users int
	data  []byte
}

func (c *memoryCache) WithEntry(ctx context.Context, key string, use func(Entry) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	entry := c.entries[key]
	if entry == nil {
		entry = &memoryCacheEntry{gate: make(chan struct{}, 1)}
		c.entries[key] = entry
	}
	entry.users++
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		entry.users--
		if entry.users == 0 && len(entry.data) == 0 {
			delete(c.entries, key)
		}
		c.mu.Unlock()
	}()

	select {
	case entry.gate <- struct{}{}:
		defer func() { <-entry.gate }()
	case <-ctx.Done():
		return ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return use(entry)
}

func (e *memoryCacheEntry) Load() ([]byte, error) {
	return slices.Clone(e.data), nil
}

func (e *memoryCacheEntry) Save(data []byte) error {
	e.data = slices.Clone(data)
	return nil
}

func (e *memoryCacheEntry) Delete() error {
	e.data = nil
	return nil
}
