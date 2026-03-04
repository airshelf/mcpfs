package fuse

import (
	"sync"
	"time"
)

type cacheEntry struct {
	data      []byte
	expiresAt time.Time
}

// Cache is a TTL cache for MCP resource content.
type Cache struct {
	mu      sync.RWMutex
	entries map[string]cacheEntry
}

// NewCache creates a cache with background cleanup.
func NewCache() *Cache {
	c := &Cache{entries: make(map[string]cacheEntry)}
	go c.cleanup()
	return c
}

// Get returns cached data if not expired.
func (c *Cache) Get(key string) ([]byte, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.entries[key]
	if !ok || time.Now().After(e.expiresAt) {
		return nil, false
	}
	return e.data, true
}

// Set stores data with a TTL.
func (c *Cache) Set(key string, data []byte, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = cacheEntry{data: data, expiresAt: time.Now().Add(ttl)}
}

func (c *Cache) cleanup() {
	for {
		time.Sleep(10 * time.Second)
		c.mu.Lock()
		now := time.Now()
		for k, e := range c.entries {
			if now.After(e.expiresAt) {
				delete(c.entries, k)
			}
		}
		c.mu.Unlock()
	}
}
