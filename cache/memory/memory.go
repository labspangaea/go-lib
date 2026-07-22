// Package memory provides a goroutine-safe, in-process LRU cache with per-entry TTL.
package memory

import (
	"container/list"
	"context"
	"sync"
	"time"
)

type item[V any] struct {
	key       string
	value     V
	expiresAt time.Time // zero = no expiry
}

func (it *item[V]) expired() bool {
	return !it.expiresAt.IsZero() && time.Now().After(it.expiresAt)
}

// Cache is a goroutine-safe LRU cache with optional TTL per entry.
// The zero value is not usable; construct with New.
type Cache[V any] struct {
	mu    sync.Mutex
	cap   int // 0 = unlimited
	index map[string]*list.Element
	lru   *list.List // front = most recently used, back = least recently used
}

// New returns an initialised Cache configured by the supplied options.
func New[V any](opts ...Option[V]) *Cache[V] {
	c := &Cache[V]{
		index: make(map[string]*list.Element),
		lru:   list.New(),
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// Get returns the value for key. Returns (zero, false, nil) on a miss or
// if the entry has expired.
func (c *Cache[V]) Get(_ context.Context, key string) (v V, ok bool, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	el, found := c.index[key]
	if !found {
		return
	}
	it := el.Value.(*item[V])
	if it.expired() {
		c.removeLocked(el)
		return
	}
	c.lru.MoveToFront(el)
	return it.value, true, nil
}

// Set stores value under key with the given TTL. A ttl of 0 (cache.NoTTL)
// means the entry never expires. If the cache is at capacity, expired items
// are evicted first; if still full, the least recently used item is removed.
func (c *Cache[V]) Set(_ context.Context, key string, value V, ttl time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	var exp time.Time
	if ttl > 0 {
		exp = time.Now().Add(ttl)
	}

	if el, exists := c.index[key]; exists {
		it := el.Value.(*item[V])
		it.value = value
		it.expiresAt = exp
		c.lru.MoveToFront(el)
		return nil
	}

	if c.cap > 0 && len(c.index) >= c.cap {
		c.evictLocked()
	}

	el := c.lru.PushFront(&item[V]{key: key, value: value, expiresAt: exp})
	c.index[key] = el
	return nil
}

// Delete removes the entry for key. Deleting a missing key is a no-op.
func (c *Cache[V]) Delete(_ context.Context, key string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.index[key]; ok {
		c.removeLocked(el)
	}
	return nil
}

// Len returns the number of entries currently held, including entries that
// have expired but not yet been lazily removed.
func (c *Cache[V]) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.index)
}

// evictLocked sweeps expired items from the LRU list, then removes the
// least recently used item if the cache is still at capacity.
// Must be called with c.mu held.
func (c *Cache[V]) evictLocked() {
	for el := c.lru.Back(); el != nil; {
		prev := el.Prev()
		if el.Value.(*item[V]).expired() {
			c.removeLocked(el)
		}
		el = prev
	}
	if c.cap > 0 && len(c.index) >= c.cap {
		if el := c.lru.Back(); el != nil {
			c.removeLocked(el)
		}
	}
}

func (c *Cache[V]) removeLocked(el *list.Element) {
	c.lru.Remove(el)
	delete(c.index, el.Value.(*item[V]).key)
}
