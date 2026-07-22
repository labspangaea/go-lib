package db

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	ourlogger "github.com/labspangaea/go-lib/logger"
)

// slogAdapter implements gorm.io/gorm/logger.Interface using the library's
// slog-based logger. Every SQL query is emitted as a single structured log line
// — the SQL is collapsed to remove embedded newlines and indentation so server
// logs remain grep-able.
type slogAdapter struct {
	cfg *config
}

func newSlogAdapter(cfg *config) gormlogger.Interface {
	return &slogAdapter{cfg: cfg}
}

// LogMode returns a copy of the adapter with the log level overridden.
// GORM calls this when a model is opened with db.Session(&gorm.Session{Logger: ...}).
func (l *slogAdapter) LogMode(lvl gormlogger.LogLevel) gormlogger.Interface {
	cp := *l
	cfgCopy := *l.cfg
	cfgCopy.logLevel = lvl
	cp.cfg = &cfgCopy
	return &cp
}

func (l *slogAdapter) Info(ctx context.Context, msg string, args ...any) {
	if l.cfg.logLevel >= gormlogger.Info {
		ourlogger.FromContext(ctx).Info(fmt.Sprintf(msg, args...))
	}
}

func (l *slogAdapter) Warn(ctx context.Context, msg string, args ...any) {
	if l.cfg.logLevel >= gormlogger.Warn {
		ourlogger.FromContext(ctx).Warn(fmt.Sprintf(msg, args...))
	}
}

func (l *slogAdapter) Error(ctx context.Context, msg string, args ...any) {
	if l.cfg.logLevel >= gormlogger.Error {
		ourlogger.FromContext(ctx).Error(fmt.Sprintf(msg, args...))
	}
}

// Trace logs a single SQL query as one structured log line.
//
// Log level rules:
//   - query error (excluding not-found when ignoreNotFound is true) → ERROR
//   - elapsed > slowThreshold → WARN ("db slow query")
//   - logLevel >= Info → INFO ("db query")
//   - otherwise → silent
//
// The SQL is collapsed with strings.Fields so multi-line GORM output becomes a
// single line: "SELECT * FROM users WHERE id = $1" rather than a multi-line block.
func (l *slogAdapter) Trace(ctx context.Context, begin time.Time, fc func() (string, int64), err error) {
	if l.cfg.logLevel <= gormlogger.Silent {
		return
	}

	elapsed := time.Since(begin)
	sql, rows := fc()
	cleanSQL := strings.Join(strings.Fields(sql), " ")

	log := ourlogger.FromContext(ctx)
	attrs := []any{
		slog.String("db.statement", cleanSQL),
		slog.Int64("db.rows_affected", rows),
		slog.Float64(ourlogger.KeyDurationMS, float64(elapsed.Microseconds())/1000.0),
	}

	switch {
	case err != nil && !(errors.Is(err, gorm.ErrRecordNotFound) && l.cfg.ignoreNotFound):
		log.Error("db query", append(attrs, slog.Any(ourlogger.KeyError, err))...)
	case l.cfg.slowThreshold > 0 && elapsed > l.cfg.slowThreshold:
		log.Warn("db slow query", attrs...)
	case l.cfg.logLevel >= gormlogger.Info:
		log.Info("db query", attrs...)
	}
}
