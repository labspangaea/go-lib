package bunrepo

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/uptrace/bun"

	"github.com/labspangaea/go-lib/logger"
)

// Model is implemented by each service's bun model struct.
// Placing these methods on the model keeps mapping logic out of the domain package.
//
// bun itself reads the table name and primary key from struct tags
// (`bun:"table:orders"`, `bun:"id,pk"`), not from these methods — TableName and
// PrimaryKey exist so the interface matches db/repo.Model and so this package can
// build WHERE clauses without reflecting over tags. Models must declare both.
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

// Repository is the full interface satisfied by BaseRepo.
// Services declare narrow subsets of this (OrderReader, OrderWriter) following ISP.
type Repository[T any, ID comparable] interface {
	FindByID(ctx context.Context, id ID) (*T, error)
	FindByIDs(ctx context.Context, ids []ID) ([]T, error)
	Create(ctx context.Context, entity *T) error
	Update(ctx context.Context, entity *T, columns []string) error
	Delete(ctx context.Context, id ID) error
	List(ctx context.Context, p CursorParams, filters ...Filter) ([]T, *CursorPage, error)
	ListIDs(ctx context.Context, p CursorParams, filters ...Filter) ([]ID, *CursorPage, error)
	DB(ctx context.Context) *bun.DB
}

// BaseRepo is a generic bun-backed repository. Embed *BaseRepo[YourModel, YourID]
// in a service-specific repository struct to inherit all CRUD and pagination methods.
// Add domain-specific queries as extra methods on the embedding struct.
type BaseRepo[T Model[ID], ID comparable] struct {
	db *bun.DB
}

// New returns a BaseRepo backed by db.
func New[T Model[ID], ID comparable](db *bun.DB) *BaseRepo[T, ID] {
	return &BaseRepo[T, ID]{db: db}
}

// FindByID retrieves a single record by primary key.
// Returns (nil, ErrNotFound) when no row exists — never (nil, nil).
func (r *BaseRepo[T, ID]) FindByID(ctx context.Context, id ID) (*T, error) {
	l := logger.FromContext(ctx)
	var zero T
	col := zero.PrimaryKey()

	var entity T
	err := r.db.NewSelect().
		Model(&entity).
		Where("? = ?", bun.Ident(col), id).
		Limit(1).
		Scan(ctx)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		l.Error("bunrepo: find by id", "error", err)
		return nil, fmt.Errorf("bunrepo: find by id: %w", err)
	}
	return &entity, nil
}

// FindByIDs retrieves all records whose primary key is in ids using a single IN query.
// Returns only found records — no error on partial miss.
// Returns an empty (non-nil) slice when none are found.
// NOTE: result order matches DB index order, not the ids input order.
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

	results := []T{}
	if err := r.db.NewSelect().
		Model(&results).
		Where("? IN (?)", bun.Ident(col), bun.In(args)).
		Scan(ctx); err != nil {
		l.Error("bunrepo: find by ids", "error", err)
		return nil, fmt.Errorf("bunrepo: find by ids: %w", err)
	}
	return results, nil
}

// Create inserts entity as a new row.
// Constraint errors are mapped to typed sentinels via MapError.
func (r *BaseRepo[T, ID]) Create(ctx context.Context, entity *T) error {
	l := logger.FromContext(ctx)
	if _, err := r.db.NewInsert().Model(entity).Exec(ctx); err != nil {
		if mapped := MapError(err); mapped != nil {
			return mapped
		}
		l.Error("bunrepo: create", "error", err)
		return fmt.Errorf("bunrepo: create: %w", err)
	}
	return nil
}

// Update saves entity back to the DB, matched on its primary key.
// columns lists specific column names to update; pass nil/empty to update every
// mapped column. Constraint errors are mapped to typed sentinels via MapError.
//
// Zero rows affected does not by itself mean the row is missing: MySQL reports 0
// when an UPDATE sets every column to the value it already held. So the
// zero-rows path falls through to an existence check and only then reports
// ErrNotFound — otherwise an idempotent update of an unchanged row would 404.
func (r *BaseRepo[T, ID]) Update(ctx context.Context, entity *T, columns []string) error {
	l := logger.FromContext(ctx)
	q := r.db.NewUpdate().Model(entity)
	if len(columns) > 0 {
		q = q.Column(columns...)
	}
	res, err := q.WherePK().Exec(ctx)
	if err != nil {
		if mapped := MapError(err); mapped != nil {
			return mapped
		}
		l.Error("bunrepo: update", "error", err)
		return fmt.Errorf("bunrepo: update: %w", err)
	}

	affected, err := res.RowsAffected()
	if err != nil || affected > 0 {
		// A driver that cannot report RowsAffected must not be treated as a miss.
		return nil
	}

	exists, err := r.exists(ctx, (*entity).GetPK())
	if err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}
	return nil
}

// Delete removes the row with the given primary key.
// Returns ErrNotFound when no row matched, ErrForeignKeyViolation when other
// rows still reference this record.
func (r *BaseRepo[T, ID]) Delete(ctx context.Context, id ID) error {
	l := logger.FromContext(ctx)
	var zero T
	col := zero.PrimaryKey()

	res, err := r.db.NewDelete().
		Model(&zero).
		Where("? = ?", bun.Ident(col), id).
		Exec(ctx)
	if err != nil {
		if mapped := MapError(err); mapped != nil {
			return mapped
		}
		l.Error("bunrepo: delete", "error", err)
		return fmt.Errorf("bunrepo: delete: %w", err)
	}
	if affected, err := res.RowsAffected(); err == nil && affected == 0 {
		return ErrNotFound
	}
	return nil
}

// List returns a page of records matching the given filters, ordered by p.OrderBy.
// Uses the limit+1 trick: fetches Limit+1 rows to detect a next page without a COUNT query.
func (r *BaseRepo[T, ID]) List(ctx context.Context, p CursorParams, filters ...Filter) ([]T, *CursorPage, error) {
	l := logger.FromContext(ctx)

	var rows []T
	q, err := r.buildListQuery(&rows, p, filters)
	if err != nil {
		return nil, nil, err
	}

	if err = q.Limit(p.Limit + 1).Scan(ctx); err != nil {
		l.Error("bunrepo: list", "error", err)
		return nil, nil, fmt.Errorf("bunrepo: list: %w", err)
	}

	page, rows := buildPage(rows, p.Limit, p.OrderBy)
	return rows, page, nil
}

// ListIDs returns only primary key values for a page of records matching filters.
// Selects PK + ORDER BY columns only (covering-index-friendly).
func (r *BaseRepo[T, ID]) ListIDs(ctx context.Context, p CursorParams, filters ...Filter) ([]ID, *CursorPage, error) {
	l := logger.FromContext(ctx)
	var zero T
	pkCol := zero.PrimaryKey()

	var rows []T
	q, err := r.buildListQuery(&rows, p, filters)
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

	if err = q.Column(selectCols...).Limit(p.Limit + 1).Scan(ctx); err != nil {
		l.Error("bunrepo: list ids", "error", err)
		return nil, nil, fmt.Errorf("bunrepo: list ids: %w", err)
	}

	page, rows := buildPage(rows, p.Limit, p.OrderBy)

	ids := make([]ID, len(rows))
	for i, row := range rows {
		ids[i] = row.GetPK()
	}
	return ids, page, nil
}

// DB returns the underlying *bun.DB. Escape hatch for complex joins, subqueries,
// or offset pagination that cannot be expressed through the Filter interface.
//
// ctx is accepted for signature parity with db/repo.Repository.DB but is unused:
// bun binds the context at execution time (Scan(ctx) / Exec(ctx)), not on the
// handle. Always pass ctx to the terminal call on the query you build.
func (r *BaseRepo[T, ID]) DB(_ context.Context) *bun.DB {
	return r.db
}

// buildListQuery constructs the base *bun.SelectQuery applying filters, ORDER BY,
// and the cursor keyset. dest must be a pointer to the destination slice or struct.
func (r *BaseRepo[T, ID]) buildListQuery(dest any, p CursorParams, filters []Filter) (*bun.SelectQuery, error) {
	q := r.db.NewSelect().Model(dest)
	q = applyFilters(q, filters)

	if len(p.OrderBy) > 0 {
		q = q.Order(orderClauses(p.OrderBy)...)
	}

	if p.Cursor != "" {
		rawVals, err := decodeCursor(p.Cursor)
		if err != nil {
			return nil, fmt.Errorf("bunrepo: invalid cursor: %w", err)
		}
		q, err = applyKeysetCondition(q, p.OrderBy, rawVals)
		if err != nil {
			return nil, err
		}
	}

	return q, nil
}

// exists reports whether a row with the given primary key is present.
func (r *BaseRepo[T, ID]) exists(ctx context.Context, id ID) (bool, error) {
	var zero T
	col := zero.PrimaryKey()
	ok, err := r.db.NewSelect().
		Model((*T)(nil)).
		Where("? = ?", bun.Ident(col), id).
		Exists(ctx)
	if err != nil {
		return false, fmt.Errorf("bunrepo: exists: %w", err)
	}
	return ok, nil
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
//
// Column names are interpolated as raw SQL, matching db/repo. Keys reaching here
// from HTTP have already passed validColumn in parseSortKeys; programmatic keys
// are trusted the same way a hand-written ORDER BY would be.
func applyKeysetCondition(q *bun.SelectQuery, keys []SortKey, rawVals map[string]json.RawMessage) (*bun.SelectQuery, error) {
	if len(keys) == 0 || len(rawVals) == 0 {
		return q, nil
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
				return nil, fmt.Errorf("bunrepo: decode cursor value %s: %w", prev.Column, err)
			}
			termParts = append(termParts, prev.Column+" = ?")
			termArgs = append(termArgs, prevVal)
		}

		// Comparison for the current key.
		val, err := unmarshalCursorValue(raw)
		if err != nil {
			return nil, fmt.Errorf("bunrepo: decode cursor value %s: %w", sk.Column, err)
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
		return q, nil
	}

	return q.Where(strings.Join(orParts, " OR "), allArgs...), nil
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
