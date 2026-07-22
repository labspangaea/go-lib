package memory

// Option configures a Cache at construction time.
type Option[V any] func(*Cache[V])

// WithCapacity sets the maximum number of entries the cache holds.
// When the limit is reached, expired items are evicted first; if none
// are expired the least recently used item is removed.
// A capacity of 0 (the default) means unlimited.
func WithCapacity[V any](n int) Option[V] {
	return func(c *Cache[V]) { c.cap = n }
}
