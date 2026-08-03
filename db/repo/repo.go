package repo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"

	"github.com/labspangaea/go-lib/logger"
)

// Model is implemented by each service's GORM model struct.
// Placing these methods on the model keeps mapping logic out of the domain package.
type Model[ID comparable] interface {
	TableName() string
	// PrimaryKey returns the column name used as the primary key, e.g. "id".
	PrimaryKey() string
	// GetPK returns the actual primary key value of this row.
	GetPK() ID
	// CursorValues returns the values of all columns that may appear in OrderBy.
	// Called by buildPage to encode the full keyset into the next cursor.
	// Return every sortable column; filterCursorValues trims to the active OrderBy.
	CursorValues() map[string]any
}

// Repository is the full interface satisfied by both BaseRepo and CachedRepo.
// Services declare narrow subsets of this (OrderReader, OrderWriter) following ISP.
//
// DB is the driver handle type the escape hatch returns — *gorm.DB for BaseRepo
// here, *bun.DB for the bunrepo flavour. It is a type parameter rather than a
// concrete type so that CachedRepo, which needs none of the seven methods above
// to be driver-specific, can decorate either. Naming *gorm.DB here was the only
// thing that made the cache layer GORM-only.
type Repository[T any, ID comparable, DB any] interface {
	FindByID(ctx context.Context, id ID) (*T, error)
	FindByIDs(ctx context.Context, ids []ID) ([]T, error)
	Create(ctx context.Context, entity *T) error
	Update(ctx context.Context, entity *T, columns []string) error
	Delete(ctx context.Context, id ID) error
	List(ctx context.Context, p CursorParams, filters ...Filter) ([]T, *CursorPage, error)
	ListIDs(ctx context.Context, p CursorParams, filters ...Filter) ([]ID, *CursorPage, error)
	DB(ctx context.Context) DB
}

// BaseRepo is a generic GORM-backed repository. Embed *BaseRepo[YourModel, YourID]
// in a service-specific repository struct to inherit all CRUD and pagination methods.
// Add domain-specific queries as extra methods on the embedding struct.
type BaseRepo[T Model[ID], ID comparable] struct {
	db *gorm.DB
}

// New returns a BaseRepo backed by db.
func New[T Model[ID], ID comparable](db *gorm.DB) *BaseRepo[T, ID] {
	return &BaseRepo[T, ID]{db: db}
}

// FindByID retrieves a single record by primary key.
// Returns (nil, ErrNotFound) when no row exists — never (nil, nil).
func (r *BaseRepo[T, ID]) FindByID(ctx context.Context, id ID) (*T, error) {
	l := logger.FromContext(ctx)
	var zero T
	col := zero.PrimaryKey()

	var entity T
	err := r.db.WithContext(ctx).Where(col+" = ?", id).First(&entity).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		l.Error("repo: find by id", "error", err)
		return nil, fmt.Errorf("repo: find by id: %w", err)
	}
	return &entity, nil
}

// FindByIDs retrieves all records whose primary key is in ids using a single IN query.
// Returns only found records — no error on partial miss.
// Returns an empty (non-nil) slice when none are found.
// NOTE: result order matches DB index order, not the ids input order.
// CachedRepo.FindByIDs reorders to match input order; callers that need order must do the same.
func (r *BaseRepo[T, ID]) FindByIDs(ctx context.Context, ids []ID) ([]T, error) {
	if len(ids) == 0 {
		return []T{}, nil
	}
	l := logger.FromContext(ctx)
	var zero T
	col := zero.PrimaryKey()

	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}

	var results []T
	if err := r.db.WithContext(ctx).Where(col+" IN ?", args).Find(&results).Error; err != nil {
		l.Error("repo: find by ids", "error", err)
		return nil, fmt.Errorf("repo: find by ids: %w", err)
	}
	return results, nil
}

// Create inserts entity as a new row.
// Constraint errors are mapped to typed sentinels — requires TranslateError: true in db.Open.
func (r *BaseRepo[T, ID]) Create(ctx context.Context, entity *T) error {
	l := logger.FromContext(ctx)
	if err := r.db.WithContext(ctx).Create(entity).Error; err != nil {
		if mapped := mapConstraintErr(err); mapped != nil {
			return mapped
		}
		l.Error("repo: create", "error", err)
		return fmt.Errorf("repo: create: %w", err)
	}
	return nil
}

// Update saves entity back to the DB.
// columns lists specific column names to update; pass nil/empty to update all non-zero fields.
// Constraint errors are mapped to typed sentinels — requires TranslateError: true in db.Open.
func (r *BaseRepo[T, ID]) Update(ctx context.Context, entity *T, columns []string) error {
	l := logger.FromContext(ctx)
	q := r.db.WithContext(ctx)
	if len(columns) > 0 {
		q = q.Select(columns)
	}
	result := q.Save(entity)
	if err := result.Error; err != nil {
		if mapped := mapConstraintErr(err); mapped != nil {
			return mapped
		}
		l.Error("repo: update", "error", err)
		return fmt.Errorf("repo: update: %w", err)
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// Delete removes the row with the given primary key.
// Returns ErrForeignKeyViolation when other rows still reference this record.
func (r *BaseRepo[T, ID]) Delete(ctx context.Context, id ID) error {
	l := logger.FromContext(ctx)
	var zero T
	col := zero.PrimaryKey()
	if err := r.db.WithContext(ctx).Where(col+" = ?", id).Delete(&zero).Error; err != nil {
		if errors.Is(err, gorm.ErrForeignKeyViolated) {
			return ErrForeignKeyViolation
		}
		l.Error("repo: delete", "error", err)
		return fmt.Errorf("repo: delete: %w", err)
	}
	return nil
}

// mapConstraintErr translates GORM constraint sentinels to repo-level errors.
// Returns nil when err is not a constraint error.
func mapConstraintErr(err error) error {
	switch {
	case errors.Is(err, gorm.ErrDuplicatedKey):
		return ErrDuplicateRecord
	case errors.Is(err, gorm.ErrForeignKeyViolated):
		return ErrForeignKeyViolation
	case errors.Is(err, gorm.ErrCheckConstraintViolated):
		return ErrConstraintViolation
	}
	return nil
}

// List returns a page of records matching the given filters, ordered by p.OrderBy.
// Uses the limit+1 trick: fetches Limit+1 rows to detect a next page without a COUNT query.
func (r *BaseRepo[T, ID]) List(ctx context.Context, p CursorParams, filters ...Filter) ([]T, *CursorPage, error) {
	l := logger.FromContext(ctx)
	db, err := r.buildListQuery(ctx, p, filters)
	if err != nil {
		return nil, nil, err
	}

	var rows []T
	if err = db.Limit(p.Limit + 1).Find(&rows).Error; err != nil {
		l.Error("repo: list", "error", err)
		return nil, nil, fmt.Errorf("repo: list: %w", err)
	}

	page, rows := buildPage(rows, p.Limit, p.OrderBy)
	return rows, page, nil
}

// ListIDs returns only primary key values for a page of records matching filters.
// Selects PK + ORDER BY columns only (covering-index-friendly) — called by CachedRepo two-phase list.
func (r *BaseRepo[T, ID]) ListIDs(ctx context.Context, p CursorParams, filters ...Filter) ([]ID, *CursorPage, error) {
	l := logger.FromContext(ctx)
	var zero T
	pkCol := zero.PrimaryKey()

	db, err := r.buildListQuery(ctx, p, filters)
	if err != nil {
		return nil, nil, err
	}

	// Select PK + all ORDER BY columns so buildPage can encode the full keyset cursor.
	selectCols := []string{pkCol}
	seen := map[string]bool{pkCol: true}
	for _, sk := range p.OrderBy {
		if !seen[sk.Column] {
			selectCols = append(selectCols, sk.Column)
			seen[sk.Column] = true
		}
	}

	var rows []T
	if err = db.Select(selectCols).Limit(p.Limit + 1).Find(&rows).Error; err != nil {
		l.Error("repo: list ids", "error", err)
		return nil, nil, fmt.Errorf("repo: list ids: %w", err)
	}

	page, rows := buildPage(rows, p.Limit, p.OrderBy)

	ids := make([]ID, len(rows))
	for i, row := range rows {
		ids[i] = row.GetPK()
	}
	return ids, page, nil
}

// DB returns a *gorm.DB scoped to ctx. Escape hatch for complex joins or subqueries
// that cannot be expressed through the Filter interface.
func (r *BaseRepo[T, ID]) DB(ctx context.Context) *gorm.DB {
	return r.db.WithContext(ctx)
}

// buildListQuery constructs the base *gorm.DB applying filters, ORDER BY, and cursor keyset.
func (r *BaseRepo[T, ID]) buildListQuery(ctx context.Context, p CursorParams, filters []Filter) (*gorm.DB, error) {
	var zero T
	db := r.db.WithContext(ctx).Model(&zero)
	db = applyFilters(db, filters)

	if len(p.OrderBy) > 0 {
		db = db.Order(orderClauses(p.OrderBy))
	}

	if p.Cursor != "" {
		rawVals, err := decodeCursor(p.Cursor)
		if err != nil {
			return nil, fmt.Errorf("repo: invalid cursor: %w", err)
		}
		db, err = applyKeysetCondition(db, p.OrderBy, rawVals)
		if err != nil {
			return nil, err
		}
	}

	return db, nil
}

// applyKeysetCondition builds the expanded OR keyset WHERE clause from decoded cursor values.
//
// For ORDER BY col1 ASC, col2 DESC with last row (col1=v1, col2=v2):
//
//	WHERE (col1 > ?)
//	   OR (col1 = ? AND col2 < ?)
//
// Args: [v1, v1, v2] — positional, in term order.
// Works on MySQL 5.7+, MySQL 8, PostgreSQL, SQLite.
func applyKeysetCondition(db *gorm.DB, keys []SortKey, rawVals map[string]json.RawMessage) (*gorm.DB, error) {
	if len(keys) == 0 || len(rawVals) == 0 {
		return db, nil
	}

	var orParts []string
	var allArgs []any

	for i, sk := range keys {
		raw, ok := rawVals[sk.Column]
		if !ok {
			break // stop at the first missing key — partial cursors not supported
		}

		var termParts []string
		var termArgs []any

		// Equality conditions for all preceding keys.
		for j := 0; j < i; j++ {
			prev := keys[j]
			prevRaw, prevOk := rawVals[prev.Column]
			if !prevOk {
				break
			}
			prevVal, err := unmarshalCursorValue(prevRaw)
			if err != nil {
				return nil, fmt.Errorf("repo: decode cursor value %s: %w", prev.Column, err)
			}
			termParts = append(termParts, prev.Column+" = ?")
			termArgs = append(termArgs, prevVal)
		}

		// Comparison for the current key.
		val, err := unmarshalCursorValue(raw)
		if err != nil {
			return nil, fmt.Errorf("repo: decode cursor value %s: %w", sk.Column, err)
		}
		op := ">"
		if sk.Desc {
			op = "<"
		}
		termParts = append(termParts, sk.Column+" "+op+" ?")
		termArgs = append(termArgs, val)

		orParts = append(orParts, "("+strings.Join(termParts, " AND ")+")")
		allArgs = append(allArgs, termArgs...)
	}

	if len(orParts) == 0 {
		return db, nil
	}

	return db.Where(strings.Join(orParts, " OR "), allArgs...), nil
}

// buildPage applies the limit+1 trick and encodes the next cursor from the last row's full keyset.
// Both List and ListIDs use this — ListIDs scans partial models (PK + sort cols only) but
// buildPage only reads GetPK() and CursorValues(), which are populated by the partial select.
func buildPage[T Model[ID], ID comparable](rows []T, limit int, orderBy []SortKey) (*CursorPage, []T) {
	page := &CursorPage{Limit: limit}
	if len(rows) > limit {
		page.HasNext = true
		rows = rows[:limit]
		if len(rows) > 0 {
			last := rows[len(rows)-1]
			cursorVals := filterCursorValues(last.CursorValues(), orderBy)
			if encoded, err := encodeCursor(cursorVals); err == nil {
				page.NextCursor = encoded
			}
		}
	}
	return page, rows
}

// filterCursorValues keeps only the values for columns present in orderBy.
// Prevents encoding unused columns (e.g. updated_at when sorting by created_at, id).
func filterCursorValues(all map[string]any, orderBy []SortKey) map[string]any {
	out := make(map[string]any, len(orderBy))
	for _, sk := range orderBy {
		if v, ok := all[sk.Column]; ok {
			out[sk.Column] = v
		}
	}
	return out
}
