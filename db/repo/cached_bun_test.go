package repo_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/driver/pgdriver"

	"github.com/labspangaea/go-lib/cache/memory"
	"github.com/labspangaea/go-lib/db/repo"
)

// The point of this file: a repository whose driver escape hatch returns
// *bun.DB can be decorated by repo.NewCached. Before Repository took the
// handle as a type parameter it could not — the interface named *gorm.DB, so
// this file would not compile, which is the failure this test exists to catch.
//
// Deliberately a fake rather than a real bunrepo.BaseRepo: the thing that was
// broken is type-level, the caching logic is driver-agnostic, and a fake lets
// the hit/miss assertion count calls without a live database.

type bunOrder struct {
	ID       string
	Quantity int64
}

func (bunOrder) TableName() string  { return "orders" }
func (bunOrder) PrimaryKey() string { return "id" }
func (m bunOrder) GetPK() string    { return m.ID }
func (m bunOrder) CursorValues() map[string]any {
	return map[string]any{"id": m.ID, "quantity": m.Quantity}
}

// bunBackedRepo satisfies repo.Repository[bunOrder, string, *bun.DB].
type bunBackedRepo struct {
	db        *bun.DB
	findCalls int
	row       bunOrder
}

func (r *bunBackedRepo) FindByID(_ context.Context, id string) (*bunOrder, error) {
	r.findCalls++
	out := r.row
	out.ID = id
	return &out, nil
}

func (r *bunBackedRepo) FindByIDs(context.Context, []string) ([]bunOrder, error) {
	return nil, nil
}
func (r *bunBackedRepo) Create(context.Context, *bunOrder) error           { return nil }
func (r *bunBackedRepo) Update(context.Context, *bunOrder, []string) error { return nil }
func (r *bunBackedRepo) Delete(context.Context, string) error              { return nil }
func (r *bunBackedRepo) List(context.Context, repo.CursorParams, ...repo.Filter) ([]bunOrder, *repo.CursorPage, error) {
	return nil, nil, nil
}
func (r *bunBackedRepo) ListIDs(context.Context, repo.CursorParams, ...repo.Filter) ([]string, *repo.CursorPage, error) {
	return nil, nil, nil
}
func (r *bunBackedRepo) DB(context.Context) *bun.DB { return r.db }

func newBunDB(t *testing.T) *bun.DB {
	t.Helper()
	// sql.OpenDB is lazy — nothing dials, and nothing here issues a query.
	sqldb := sql.OpenDB(pgdriver.NewConnector(
		pgdriver.WithDSN("postgres://u:p@127.0.0.1:5432/testdb?sslmode=disable"),
	))
	t.Cleanup(func() { _ = sqldb.Close() })
	return bun.NewDB(sqldb, pgdialect.New())
}

func TestCachedRepo_WrapsABunBackedRepository(t *testing.T) {
	ctx := context.Background()
	base := &bunBackedRepo{db: newBunDB(t), row: bunOrder{Quantity: 7}}

	cached := repo.NewCached[bunOrder, string, *bun.DB](
		base, memory.New[*bunOrder](), "order",
	)

	first, err := cached.FindByID(ctx, "abc")
	if err != nil {
		t.Fatalf("first FindByID: %v", err)
	}
	if first.Quantity != 7 {
		t.Fatalf("first FindByID quantity = %d, want 7", first.Quantity)
	}

	// Second read must come from the cache, not the repository.
	if _, err = cached.FindByID(ctx, "abc"); err != nil {
		t.Fatalf("second FindByID: %v", err)
	}
	if base.findCalls != 1 {
		t.Errorf("underlying repo hit %d times, want 1 — the cache is not serving reads", base.findCalls)
	}

	// Delete must invalidate, sending the next read back to the repository.
	if err = cached.Delete(ctx, "abc"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err = cached.FindByID(ctx, "abc"); err != nil {
		t.Fatalf("FindByID after Delete: %v", err)
	}
	if base.findCalls != 2 {
		t.Errorf("underlying repo hit %d times after invalidation, want 2", base.findCalls)
	}
}

func TestCachedRepo_ForwardsTheBunHandle(t *testing.T) {
	db := newBunDB(t)
	base := &bunBackedRepo{db: db}
	cached := repo.NewCached[bunOrder, string, *bun.DB](base, memory.New[*bunOrder](), "order")

	// The escape hatch keeps its driver type through the decorator — this is
	// what lets a scaffolded bun repository embed the cached one and still run
	// its custom bun queries.
	if got := cached.DB(context.Background()); got != db {
		t.Error("CachedRepo.DB did not forward the underlying *bun.DB")
	}
}
