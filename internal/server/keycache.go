package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"time"
)

const (
	keyCacheTTL     = 3 * time.Minute
	keyCacheMaxSize = 10_000
)

// verifiedKey is what upti.my-api said about a valid, full-scope key.
type verifiedKey struct {
	uuid      string
	workspace string
	expiresAt *time.Time
}

// keyCache remembers successful key verifications for 3 minutes, keyed by a
// hash of the key, and lets concurrent lookups of one key share a single
// request to upti.my-api. MCP clients send bursts of requests (initialize,
// tools/list, several tool calls), each one authenticated. A revoked key
// keeps working for up to 4 minutes (the API caches its own answer for up to
// 1 minute too); refusals are never cached.
type keyCache struct {
	now func() time.Time

	mu       sync.Mutex
	entries  map[string]keyCacheEntry
	order    []string // insertion order, for eviction
	inFlight map[string]*keyLookup
}

type keyCacheEntry struct {
	key   verifiedKey
	until time.Time
}

type keyLookup struct {
	done chan struct{}
	key  verifiedKey
	err  error
}

func newKeyCache() *keyCache {
	return &keyCache{now: time.Now, entries: map[string]keyCacheEntry{}, inFlight: map[string]*keyLookup{}}
}

// get returns the cached answer for token, or calls verify once (however many
// callers are waiting) and caches a success.
func (c *keyCache) get(ctx context.Context, token string, verify func(context.Context) (verifiedKey, error)) (verifiedKey, error) {
	sum := sha256.Sum256([]byte(token))
	id := hex.EncodeToString(sum[:])

	c.mu.Lock()
	if e, ok := c.entries[id]; ok {
		if c.now().Before(e.until) {
			c.mu.Unlock()
			return e.key, nil
		}
		delete(c.entries, id)
	}
	if l, ok := c.inFlight[id]; ok {
		c.mu.Unlock()
		select {
		case <-l.done:
			return l.key, l.err
		case <-ctx.Done():
			return verifiedKey{}, ctx.Err()
		}
	}
	l := &keyLookup{done: make(chan struct{})}
	c.inFlight[id] = l
	c.mu.Unlock()

	// Detached from this request: other callers may be waiting on the lookup.
	l.key, l.err = verify(context.WithoutCancel(ctx))

	c.mu.Lock()
	delete(c.inFlight, id)
	if l.err == nil {
		c.store(id, l.key)
	}
	c.mu.Unlock()
	close(l.done)
	return l.key, l.err
}

// store caches a key, never past its own expiry. Callers hold c.mu.
func (c *keyCache) store(id string, key verifiedKey) {
	until := c.now().Add(keyCacheTTL)
	if key.expiresAt != nil && key.expiresAt.Before(until) {
		until = *key.expiresAt
	}
	if _, exists := c.entries[id]; !exists {
		for len(c.entries) >= keyCacheMaxSize && len(c.order) > 0 {
			delete(c.entries, c.order[0])
			c.order = c.order[1:]
		}
		c.order = append(c.order, id)
	}
	c.entries[id] = keyCacheEntry{key: key, until: until}
	// Keep the eviction list from outgrowing the cache when entries expire.
	if len(c.order) > 2*keyCacheMaxSize {
		live := c.order[:0]
		for _, k := range c.order {
			if _, ok := c.entries[k]; ok {
				live = append(live, k)
			}
		}
		c.order = live
	}
}
