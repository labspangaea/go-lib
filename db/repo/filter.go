package repo

import (
	"fmt"
	"strings"

	"gorm.io/gorm"
)

// Filter applies a WHERE condition to a GORM query.
// Each filter carries its own Apply logic — no struct mutation, no chaining bug possible.
type Filter interface {
	Apply(db *gorm.DB) *gorm.DB
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

func (f eqFilter) Apply(db *gorm.DB) *gorm.DB { return db.Where(f.col+" = ?", f.val) }

type likeFilter struct {
	col string
	val string
}

func (f likeFilter) Apply(db *gorm.DB) *gorm.DB {
	return db.Where(f.col+" LIKE ?", "%"+f.val+"%")
}

type fullTextFilter struct {
	col string
	val string
}

func (f fullTextFilter) Apply(db *gorm.DB) *gorm.DB {
	return db.Where(fmt.Sprintf("MATCH(%s) AGAINST (? IN BOOLEAN MODE)", f.col), f.val)
}

type inFilter struct {
	col  string
	vals []any
}

func (f inFilter) Apply(db *gorm.DB) *gorm.DB { return db.Where(f.col+" IN ?", f.vals) }

type isNullFilter struct{ col string }

func (f isNullFilter) Apply(db *gorm.DB) *gorm.DB {
	return db.Where(f.col + " IS NULL")
}

type notNullFilter struct{ col string }

func (f notNullFilter) Apply(db *gorm.DB) *gorm.DB {
	return db.Where(f.col + " IS NOT NULL")
}

type rawFilter struct {
	query string
	args  []any
}

func (f rawFilter) Apply(db *gorm.DB) *gorm.DB { return db.Where(f.query, f.args...) }

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

// Raw passes query and args directly to GORM Where — escape hatch for complex expressions.
func Raw(query string, args ...any) Filter { return rawFilter{query: query, args: args} }

// applyFilters chains every filter onto db, always assigning the result back.
// Caller must use the returned *gorm.DB — discarding it silently drops all filters.
func applyFilters(db *gorm.DB, filters []Filter) *gorm.DB {
	for _, f := range filters {
		db = f.Apply(db)
	}
	return db
}

// orderClauses builds a single ORDER BY string from a []SortKey slice,
// e.g. []SortKey{{Column:"created_at",Desc:true},{Column:"id"}} → "created_at DESC, id ASC".
func orderClauses(keys []SortKey) string {
	parts := make([]string, len(keys))
	for i, sk := range keys {
		parts[i] = sk.orderClause()
	}
	return strings.Join(parts, ", ")
}
