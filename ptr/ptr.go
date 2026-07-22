// Package ptr provides generic helpers for working with pointer values.
package ptr

// Of returns a pointer to v. Useful for taking the address of a literal or
// expression where Go's address-of operator cannot be applied directly.
//
//	ptr.Of("hello")    // *string
//	ptr.Of(int64(42))  // *int64
//	ptr.Of(true)       // *bool
func Of[T any](v T) *T { return &v }

// Deref dereferences p. Returns fallback when p is nil.
//
//	ptr.Deref(s, "")        // "" when s == nil
//	ptr.Deref(n, int64(0))  // 0  when n == nil
func Deref[T any](p *T, fallback T) T {
	if p == nil {
		return fallback
	}
	return *p
}
