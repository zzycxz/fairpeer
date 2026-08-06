package builtin

import (
	"strings"
	"sync"
	"time"
)

// SearchCache provides an in-memory cache for web search results. This avoids
// repeated searches for the same query within a session. The cache is process-
// scoped (no SQLite, no files) so it never leaks credentials or stale results
// to disk, and it adds zero external dependencies (no CGO, no go-sqlite3).
type SearchCache struct {
	mu    sync.RWMutex
	ttl   time.Duration
	store map[string]cacheEntry
}

type cacheEntry struct {
	results []searchResultItem
	engine  string
	expires time.Time
}

// NewSearchCache creates a new in-memory search cache with the given TTL.
// dbPath is accepted for API compatibility but ignored (in-memory only).
func NewSearchCache(_ string, ttl time.Duration) (*SearchCache, error) {
	return &SearchCache{
		ttl:   ttl,
		store: make(map[string]cacheEntry),
	}, nil
}

// Get retrieves cached search results for a query.
// Returns results, engine, and true if found and not expired.
func (c *SearchCache) Get(query string) ([]searchResultItem, string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	key := hashQuery(query)
	entry, ok := c.store[key]
	if !ok || time.Now().After(entry.expires) {
		return nil, "", false
	}
	return entry.results, entry.engine, true
}

// Set stores search results in the cache.
func (c *SearchCache) Set(query string, results []searchResultItem, engine string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	key := hashQuery(query)
	c.store[key] = cacheEntry{
		results: results,
		engine:  engine,
		expires: time.Now().Add(c.ttl),
	}
	return nil
}

// Clear removes all cached results.
func (c *SearchCache) Clear() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.store = make(map[string]cacheEntry)
	return nil
}

// Prune removes expired entries (called opportunistically).
func (c *SearchCache) Prune() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	for k, v := range c.store {
		if now.After(v.expires) {
			delete(c.store, k)
		}
	}
	return nil
}

// Close is a no-op for in-memory cache (API compatibility).
func (c *SearchCache) Close() error { return nil }

// hashQuery normalizes a query string into a cache key.
func hashQuery(query string) string {
	normalized := strings.ToLower(strings.TrimSpace(query))
	if len(normalized) > 100 {
		normalized = normalized[:100]
	}
	return normalized
}
