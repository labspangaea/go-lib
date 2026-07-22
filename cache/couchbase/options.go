package couchbase

// Option configures a Cache at construction time.
type Option[V any] func(*Cache[V])

// WithKeyPrefix prepends prefix and ":" to every Couchbase document key.
// Use this to namespace entries by service or environment
// (e.g. "users:prod" → key "users:prod:42").
func WithKeyPrefix[V any](prefix string) Option[V] {
	return func(c *Cache[V]) { c.prefix = prefix }
}

// WithMarshal replaces the default JSON serializer.
func WithMarshal[V any](fn func(V) ([]byte, error)) Option[V] {
	return func(c *Cache[V]) { c.marshal = fn }
}

// WithUnmarshal replaces the default JSON deserializer.
func WithUnmarshal[V any](fn func([]byte, *V) error) Option[V] {
	return func(c *Cache[V]) { c.unmarshal = fn }
}
