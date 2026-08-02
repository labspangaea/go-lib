package bunrepo

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql" // database/sql driver for the mysql DSN branch
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/mysqldialect"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/driver/pgdriver"

	"github.com/labspangaea/go-lib/logger"
)

// options carries the tunables applied by Open. Mirrors the db package's option
// set so switching a service between GORM and bun does not change main.go's shape.
type options struct {
	maxOpenConns  int
	slowThreshold time.Duration
	logLevel      string
}

// Option customises Open.
type Option func(*options)

// WithMaxOpenConns caps the pool at n connections. Idle connections are capped
// at the same value — a pool that opens n connections then lets them go idle
// only to reopen them is the common misconfiguration.
func WithMaxOpenConns(n int) Option {
	return func(o *options) { o.maxOpenConns = n }
}

// WithSlowThreshold logs any query taking at least d at warn level.
func WithSlowThreshold(d time.Duration) Option {
	return func(o *options) { o.slowThreshold = d }
}

// WithLogLevel sets query-log verbosity: "silent" | "error" | "warn" (default) | "info".
// "info" logs every statement — development only.
func WithLogLevel(level string) Option {
	return func(o *options) { o.logLevel = level }
}

// Open connects to PostgreSQL or MySQL and returns a *bun.DB with the query hook
// installed. The driver is selected from the DSN scheme:
//
//	postgres://user:pass@host:5432/db?sslmode=disable   → pgdriver + pgdialect
//	user:pass@tcp(host:3306)/db?parseTime=true          → mysql + mysqldialect
//
// Note pgdriver requires the URL form; the key=value DSN that GORM's postgres
// driver accepts ("host=… port=… user=…") is not understood here.
//
// Callers must defer db.Close(). There is no Close helper — *bun.DB embeds
// *sql.DB and closes itself.
func Open(dsn string, opts ...Option) (*bun.DB, error) {
	if strings.TrimSpace(dsn) == "" {
		return nil, fmt.Errorf("bunrepo: open: empty DSN")
	}

	o := options{maxOpenConns: 20, slowThreshold: 500 * time.Millisecond, logLevel: "warn"}
	for _, opt := range opts {
		opt(&o)
	}

	var db *bun.DB
	switch {
	case isPostgresDSN(dsn):
		sqldb := sql.OpenDB(pgdriver.NewConnector(pgdriver.WithDSN(dsn)))
		db = bun.NewDB(sqldb, pgdialect.New())
	default:
		sqldb, err := sql.Open("mysql", dsn)
		if err != nil {
			return nil, fmt.Errorf("bunrepo: open mysql: %w", err)
		}
		db = bun.NewDB(sqldb, mysqldialect.New())
	}

	if o.maxOpenConns > 0 {
		db.DB.SetMaxOpenConns(o.maxOpenConns)
		db.DB.SetMaxIdleConns(o.maxOpenConns)
	}

	if o.logLevel != "silent" {
		db.AddQueryHook(queryHook{slowThreshold: o.slowThreshold, verbose: o.logLevel == "info"})
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("bunrepo: ping: %w", err)
	}

	return db, nil
}

// isPostgresDSN reports whether dsn addresses PostgreSQL. Both schemes are
// accepted by pgdriver and both appear in the wild.
func isPostgresDSN(dsn string) bool {
	return strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://")
}

// queryHook logs statements through the context logger, so query lines carry the
// same trace_id / span_id as the request that issued them.
//
// This is the bun equivalent of the db package's GORM logger: errors always log,
// slow queries warn, and everything else is silent unless log level is "info".
type queryHook struct {
	slowThreshold time.Duration
	verbose       bool
}

func (queryHook) BeforeQuery(ctx context.Context, _ *bun.QueryEvent) context.Context {
	return ctx
}

func (h queryHook) AfterQuery(ctx context.Context, event *bun.QueryEvent) {
	l := logger.FromContext(ctx)
	elapsed := time.Since(event.StartTime)

	switch {
	case event.Err != nil && !errorsIsNoRows(event.Err):
		l.Error("db query",
			slog.String("query", event.Query),
			slog.Any(logger.KeyError, event.Err),
			slog.Int64(logger.KeyDurationMS, elapsed.Milliseconds()),
		)
	case h.slowThreshold > 0 && elapsed >= h.slowThreshold:
		l.Warn("db slow query",
			slog.String("query", event.Query),
			slog.Int64(logger.KeyDurationMS, elapsed.Milliseconds()),
		)
	case h.verbose:
		l.Info("db query",
			slog.String("query", event.Query),
			slog.Int64(logger.KeyDurationMS, elapsed.Milliseconds()),
		)
	}
}

// errorsIsNoRows keeps sql.ErrNoRows out of the error log — a FindByID miss is a
// normal outcome the repository already translates to ErrNotFound.
func errorsIsNoRows(err error) bool {
	return err == sql.ErrNoRows || strings.Contains(err.Error(), sql.ErrNoRows.Error())
}
