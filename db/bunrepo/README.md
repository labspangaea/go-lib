# db/bunrepo

Generic [bun](https://bun.uptrace.dev)-backed repository with cursor pagination and
composable filters — the SQL-first counterpart of [`db/repo`](../repo).

Pick this when you want a lightweight query builder and explicit SQL. Pick `db/repo`
when you want GORM's conveniences or a cache decorator.

---

## Why both exist

`db/repo` is GORM-typed all the way down: its `Filter` applies to a `*gorm.DB`.
Importing it at all pulls GORM into your `go.mod`, which defeats the point of
choosing a lightweight builder.

So this package restates the surface — but *only* the `Filter` type actually differs.
`CursorParams`, `OffsetParams`, `CursorPage`, `SortKey`, `FilterSet`, the filter
constructors and the error sentinels are identical in name, shape, and behaviour.
A service switches ORMs by changing one import:

```go
repo "github.com/labspangaea/go-lib/db/bunrepo"
```

Every `repo.CursorParams` / `repo.Filter` / `repo.FilterSet` reference in your port,
service, and handler layers keeps compiling unchanged.

Cursors are wire-compatible between the two packages, so a service migrating from
GORM to bun does not invalidate tokens clients are already holding.

---

## Quick start

```go
bunDB, err := bunrepo.Open(cfg.DatabaseDSN,
    bunrepo.WithMaxOpenConns(cfg.DBMaxOpen),
    bunrepo.WithSlowThreshold(500*time.Millisecond),
    bunrepo.WithLogLevel(cfg.DBLogLevel),
)
if err != nil { ... }
defer bunDB.Close()
```

```go
// Model (adapter layer) — bun struct tags plus the Model[ID] methods.
type orderModel struct {
    bun.BaseModel `bun:"table:orders"`

    ID        string `bun:"id,pk"`
    Name      string `bun:"name"`
    CreatedAt int64  `bun:"created_at"`
}

func (orderModel) TableName() string  { return "orders" }
func (orderModel) PrimaryKey() string { return "id" }
func (m orderModel) GetPK() string    { return m.ID }
func (m orderModel) CursorValues() map[string]any {
    return map[string]any{"id": m.ID, "created_at": m.CreatedAt}
}

// Repository — embed BaseRepo.
type Order struct{ *bunrepo.BaseRepo[orderModel, string] }

func New(db *bun.DB) *Order { return &Order{bunrepo.New[orderModel, string](db)} }
```

bun reads the table name and primary key from the **struct tags**, not from
`TableName()` / `PrimaryKey()`. Both must be declared: the tags drive the driver,
the methods satisfy `Model[ID]` and let this package build WHERE clauses without
reflecting over tags.

---

## API

### Connection

```go
func Open(dsn string, opts ...Option) (*bun.DB, error)
```

Driver is selected from the DSN scheme:

| DSN | Driver |
|---|---|
| `postgres://user:pass@host:5432/db?sslmode=disable` | `pgdriver` + `pgdialect` |
| `user:pass@tcp(host:3306)/db?parseTime=true` | `go-sql-driver/mysql` + `mysqldialect` |

> **Gotcha:** `pgdriver` requires the **URL** form. The `host=… port=… user=…`
> key/value DSN that GORM's postgres driver accepts is *not* understood — a service
> moving from `gorm-postgres` to bun must rewrite `DATABASE_DSN`.

There is no `Close` helper: `*bun.DB` embeds `*sql.DB` and closes itself. Always
`defer bunDB.Close()`.

| Option | Default | Effect |
|---|---|---|
| `WithMaxOpenConns(n)` | 20 | `SetMaxOpenConns(n)` **and** `SetMaxIdleConns(n)` |
| `WithSlowThreshold(d)` | 500ms | queries ≥ d log at warn |
| `WithLogLevel(level)` | `"warn"` | `silent` · `error` · `warn` · `info` (`info` logs every statement) |

Query logging goes through `logger.FromContext(ctx)`, so query lines carry the same
`trace_id` / `span_id` as the request that issued them.

### Repository

```go
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

func New[T Model[ID], ID comparable](db *bun.DB) *BaseRepo[T, ID]
```

`DB(ctx)` accepts a context for signature parity with `db/repo` but ignores it — bun
binds the context at execution time (`Scan(ctx)` / `Exec(ctx)`), not on the handle.
Always pass `ctx` to the terminal call on the query you build:

```go
var total int64
total, err := r.DB(ctx).NewSelect().Model((*orderModel)(nil)).Count(ctx)
```

### Filters

```go
type Filter interface{ Apply(q *bun.SelectQuery) *bun.SelectQuery }

func Eq(col string, val any) Filter
func Like(col string, val string) Filter       // wraps val in %
func FullText(col string, val string) Filter   // MySQL MATCH … AGAINST
func In(col string, vals ...any) Filter
func IsNull(col string) Filter
func NotNull(col string) Filter
func Raw(query string, args ...any) Filter
```

`FilterSet` accumulates them, `AddIf` skips empty query params:

```go
func (q *ListOrdersInput) ToFilters() []repo.Filter {
    var fs repo.FilterSet
    fs.AddIf(q.Name != "", repo.Like("name", q.Name))
    return fs.Build()
}
```

### Pagination

```go
func NewCursorParams(cursor string, limit int, sorts ...SortKey) CursorParams
func NewOffsetParams(offset, limit int) OffsetParams
func CursorParamsFromRequest(r *http.Request, defaultLimit int, defaultOrderBy ...SortKey) CursorParams
func Asc(col string) SortKey
func Descending(col string) SortKey
```

`List` uses the limit+1 trick — it fetches `Limit+1` rows to detect a next page
without a `COUNT` query, then trims. The cursor encodes every `OrderBy` column value
of the last row, so multi-column mixed ASC/DESC ordering paginates correctly:

```
ORDER BY created_at DESC, id ASC
  →  WHERE (created_at < ?) OR (created_at = ? AND id > ?)
```

Works on MySQL 5.7+, MySQL 8, PostgreSQL and SQLite.

Always include a unique tiebreaker (usually the PK) as the last sort key. Without
one, rows sharing a `created_at` value can be skipped or repeated across pages.

Keyset pagination compares column values directly, so **`NULL`s in a sort column
break it** — `NULL > x` is unknown, not true. Sort on `NOT NULL` columns, or use
`OffsetParams`.

### Errors

```go
var ErrNotFound, ErrDuplicateRecord, ErrForeignKeyViolation, ErrConstraintViolation
func MapError(err error) error   // nil when err is not a recognised constraint violation
```

GORM gets this translation free via `TranslateError`. bun surfaces the raw driver
error, so the mapping is explicit — and therefore unit-testable without a database:

| Condition | PostgreSQL SQLSTATE | MySQL errno |
|---|---|---|
| `ErrDuplicateRecord` | 23505 | 1062 |
| `ErrForeignKeyViolation` | 23503 | 1451, 1452 |
| `ErrConstraintViolation` | 23502, 23514 | 1048, 3819 |

`FindByID` returns `ErrNotFound` on a miss — never `(nil, nil)`.

`Update` does **not** treat zero rows affected as a miss on its own: MySQL reports 0
when an UPDATE sets every column to the value it already held. The zero-rows path
falls through to an existence check, so an idempotent update of an unchanged row
does not 404.

---

## Not provided

**No cache decorator.** `db/repo.CachedRepo` is generic over a repository exposing
`DB(ctx) *gorm.DB` and cannot wrap a bun repository. Use `db/repo` if you need
`WithTTL` / `WithJitter` / `WithTwoPhaseList`.

**No `ListOffset` on `BaseRepo`.** Offset pagination is a per-repository concern —
build it on `DB(ctx)` with bun's `ScanAndCount`, which returns page and total in one
round trip:

```go
func (r *Order) ListOrdersOffset(ctx context.Context, p repo.OffsetParams, filters ...repo.Filter) ([]orderModel, int64, error) {
    var models []orderModel
    q := r.DB(ctx).NewSelect().Model(&models)
    for _, f := range filters {
        q = f.Apply(q)
    }
    total, err := q.Offset(p.Offset).Limit(p.Limit).ScanAndCount(ctx)
    return models, int64(total), err
}
```

---

## Do / Don't

| Do | Don't |
|----|-------|
| `errors.Is(err, bunrepo.ErrNotFound)` in the repo adapter | return an HTTP status code from the repo |
| pass an explicit `[]string` of columns to `Update` | pass nil and let every column be written |
| end `OrderBy` with a unique tiebreaker | sort on `created_at` alone |
| use the URL DSN form for PostgreSQL | reuse GORM's `host=… port=…` DSN |
| sort on `NOT NULL` columns for cursor pagination | keyset-paginate over a nullable column |
