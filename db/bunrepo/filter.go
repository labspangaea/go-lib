package bunrepo

import (
	"fmt"

	"github.com/uptrace/bun"
)

// Filter applies a WHERE condition to a bun SELECT query.
// Each filter carries its own Apply logic — no struct mutation, no chaining bug possible.
//
// This is the only type in this package whose shape differs from db/repo: the
// GORM flavour takes a *gorm.DB. Everything a handler or service touches
// (FilterSet, the constructors, CursorParams) is identical.
type Filter interface {
	Apply(q *bun.SelectQuery) *bun.SelectQuery
}

// Filterable is implemented by HTTP query parameter structs that can convert
// themselves to a slice of Filter values. Keeps filter mapping co-located with
// the request struct and out of handler bodies.
type Filterable interface {
	ToFilters() []Filter
}

// FilterSet accumulates Filter values.
type FilterSet []Filter

// Add appends f unconditionally.
func (fs *FilterSet) Add(f Filter) *FilterSet {
	*fs = append(*fs, f)
	return fs
}

// AddIf appends f only when condition is true. Use to skip empty query params.
func (fs *FilterSet) AddIf(condition bool, f Filter) *FilterSet {
	if condition {
		*fs = append(*fs, f)
	}
	return fs
}

// Build returns the accumulated filters as a plain slice.
func (fs FilterSet) Build() []Filter {
	return []Filter(fs)
}

// — filter implementations —

type eqFilter struct {
	col string
	val any
}

func (f eqFilter) Apply(q *bun.SelectQuery) *bun.SelectQuery {
	return q.Where("? = ?", bun.Ident(f.col), f.val)
}

type likeFilter struct {
	col string
	val string
}

func (f likeFilter) Apply(q *bun.SelectQuery) *bun.SelectQuery {
	return q.Where("? LIKE ?", bun.Ident(f.col), "%"+f.val+"%")
}

type fullTextFilter struct {
	col string
	val string
}

func (f fullTextFilter) Apply(q *bun.SelectQuery) *bun.SelectQuery {
	return q.Where(fmt.Sprintf("MATCH(%s) AGAINST (? IN BOOLEAN MODE)", f.col), f.val)
}

type inFilter struct {
	col  string
	vals []any
}

func (f inFilter) Apply(q *bun.SelectQuery) *bun.SelectQuery {
	return q.Where("? IN (?)", bun.Ident(f.col), bun.In(f.vals))
}

type isNullFilter struct{ col string }

func (f isNullFilter) Apply(q *bun.SelectQuery) *bun.SelectQuery {
	return q.Where("? IS NULL", bun.Ident(f.col))
}

type notNullFilter struct{ col string }

func (f notNullFilter) Apply(q *bun.SelectQuery) *bun.SelectQuery {
	return q.Where("? IS NOT NULL", bun.Ident(f.col))
}

type rawFilter struct {
	query string
	args  []any
}

func (f rawFilter) Apply(q *bun.SelectQuery) *bun.SelectQuery {
	return q.Where(f.query, f.args...)
}

// — constructors —

// Eq matches rows where col equals val.
func Eq(col string, val any) Filter { return eqFilter{col: col, val: val} }

// Like matches rows where col contains val (wrapped in %).
func Like(col string, val string) Filter { return likeFilter{col: col, val: val} }

// FullText applies a MySQL full-text MATCH … AGAINST search. PostgreSQL services
// should use Raw with a tsvector expression instead.
func FullText(col string, val string) Filter { return fullTextFilter{col: col, val: val} }

// In matches rows where col is one of vals.
func In(col string, vals ...any) Filter {
	flat := make([]any, len(vals))
	copy(flat, vals)
	return inFilter{col: col, vals: flat}
}

// IsNull matches rows where col IS NULL.
func IsNull(col string) Filter { return isNullFilter{col: col} }

// NotNull matches rows where col IS NOT NULL.
func NotNull(col string) Filter { return notNullFilter{col: col} }

// Raw passes query and args directly to bun's Where — escape hatch for complex
// expressions. Note bun uses ? placeholders for both identifiers (bun.Ident) and
// values, same as this package's other filters.
func Raw(query string, args ...any) Filter { return rawFilter{query: query, args: args} }

// applyFilters chains every filter onto q, always assigning the result back.
// Caller must use the returned *bun.SelectQuery — discarding it silently drops all filters.
func applyFilters(q *bun.SelectQuery, filters []Filter) *bun.SelectQuery {
	for _, f := range filters {
		q = f.Apply(q)
	}
	return q
}

// orderClauses builds one ORDER BY fragment per SortKey,
// e.g. []SortKey{{Column:"created_at",Desc:true},{Column:"id"}} → ["created_at DESC", "id ASC"].
//
// Returns a slice rather than db/repo's comma-joined string because bun's
// SelectQuery.Order parses each argument individually (splitting on the space to
// find the direction) — a single "a DESC, b ASC" string would be quoted as one
// malformed identifier.
func orderClauses(keys []SortKey) []string {
	parts := make([]string, len(keys))
	for i, sk := range keys {
		parts[i] = sk.orderClause()
	}
	return parts
}
