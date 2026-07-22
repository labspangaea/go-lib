# db

GORM wrapper with connection pooling, graceful shutdown, and a single-line structured SQL logger that integrates with the library's `log/slog`-based logger package.

`db.Open` returns a plain `*gorm.DB` — no wrapper type. The caller uses the full GORM API unchanged. The wrapper only handles three concerns:

1. **Connection pool** — tunable via options; sensible defaults for microservices.
2. **Structured logging** — every SQL query is one JSON log line with `request_id`, `trace_id`, `duration_ms`, and the SQL collapsed to a single line.
3. **Graceful shutdown** — `db.Close` drains in-flight queries and closes the pool.

---

## Quick start

```go
import (
    "gorm.io/driver/postgres"
    "github.com/labspangaea/go-lib/db"
)

gormDB, err := db.Open(
    postgres.Open("host=localhost user=app dbname=mydb sslmode=disable"),
    db.WithMaxOpenConns(20),
    db.WithSlowThreshold(500*time.Millisecond),
)
if err != nil {
    log.Fatal(err)
}
defer db.Close(gormDB)
```

---

## Connection pool

| Option | Default | What it controls |
|---|---|---|
| `WithMaxOpenConns(n)` | 25 | Max open connections (hard cap on DB load) |
| `WithMaxIdleConns(n)` | 5 | Connections kept alive when idle |
| `WithConnMaxLifetime(d)` | 5m | Max time a connection may be reused (avoids stale DNS) |
| `WithConnMaxIdleTime(d)` | 1m | Max idle time before a connection is closed |

**Sizing guidance:** `MaxOpenConns` should not exceed the DB's `max_connections` divided by the number of service replicas. A value of 10–25 is right for most microservices; only data-intensive batch jobs need higher values.

```go
gormDB, err := db.Open(dialector,
    db.WithMaxOpenConns(10),
    db.WithMaxIdleConns(3),
    db.WithConnMaxLifetime(10*time.Minute),
)
```

---

## Single-line SQL logger

Every executed query is emitted as **one JSON log line** containing:

| Field | Example |
|---|---|
| `db.statement` | `SELECT * FROM orders WHERE id = $1` |
| `db.rows_affected` | `1` |
| `duration_ms` | `2.47` |
| `request_id` | `a3f9c1b2` (from context logger) |
| `trace_id` | `4bf92f3...` (from context logger) |

GORM formats multi-table queries with indentation and newlines. The logger collapses all whitespace using `strings.Fields` so every query stays on one line — easy to grep, easy to forward to log aggregators.

**Example output (JSON, production):**

```json
{"time":"2026-04-24T10:01:23Z","level":"INFO","msg":"db query","db.statement":"SELECT id, status FROM orders WHERE user_id = $1 ORDER BY created_at DESC LIMIT $2","db.rows_affected":5,"duration_ms":1.83,"request_id":"a3f9c1b2","trace_id":"4bf92f3577b34da6"}
```

**Example output (slow query):**

```json
{"time":"...","level":"WARN","msg":"db slow query","db.statement":"SELECT ...","duration_ms":621.4}
```

### Logger options

| Option | Default | Notes |
|---|---|---|
| `WithSlowThreshold(d)` | 200ms | Queries above this threshold log at WARN |
| `WithLogLevel(lvl)` | `gormlogger.Warn` | `Info` = all queries; `Warn` = slow + errors; `Silent` = nothing |
| `WithIgnoreNotFound(bool)` | `true` | Suppress `ErrRecordNotFound` in error logs (it's a normal miss, not a fault) |

```go
gormDB, err := db.Open(dialector,
    db.WithSlowThreshold(500*time.Millisecond),
    db.WithLogLevel(gormlogger.Info), // log every query in development
)
```

Pass `context.WithValue` / `logger.WithLogger` on the context passed to GORM queries to correlate every SQL line to its originating HTTP request:

```go
// The context carries request_id and trace_id set by the server middleware.
result := gormDB.WithContext(r.Context()).Where("id = ?", id).First(&order)
```

---

## Graceful shutdown

`db.Close` calls `sqlDB.Close()` on the underlying `*sql.DB`, which waits for in-flight queries to complete and then closes all connections.

**Shutdown order matters — close DB after HTTP drains:**

```go
// 1. Stop accepting HTTP requests; active handlers (including DB queries) complete
drainCtx, drainCancel := context.WithTimeout(context.Background(), 30*time.Second)
defer drainCancel()
srv.Shutdown(drainCtx)

// 2. Close connection pool — all queries already done at this point
if err := db.Close(gormDB); err != nil {
    log.Error("db close", slog.Any("error", err))
}

// 3. Close pub/sub, flush telemetry, etc.
```

Closing the DB before HTTP drains risks cancelling queries mid-execution, leading to partial writes and inconsistent state.

---

## Choosing a driver

`db.Open` accepts any `gorm.Dialector`. Install the driver for your database:

```bash
# PostgreSQL
go get gorm.io/driver/postgres

# MySQL / TiDB
go get gorm.io/driver/mysql

# SQLite (development / tests)
go get gorm.io/driver/sqlite
```

```go
// PostgreSQL
db.Open(postgres.Open(dsn), opts...)

// MySQL
db.Open(mysql.Open(dsn), opts...)

// SQLite
db.Open(sqlite.Open("gorm.db"), opts...)
```

The `db` package has no dependency on any specific driver — it works with all of them.
