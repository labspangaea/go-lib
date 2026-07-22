// Package db provides a thin GORM wrapper with connection pooling and a
// single-line structured SQL logger that integrates with the library's slog
// logger package.
//
// Open returns a plain *gorm.DB — callers use the full GORM API unchanged.
// The wrapper only handles: connection pool tuning, structured logging, and
// graceful shutdown via Close.
//
// Dialectors are provided by the driver sub-packages:
//
//	import "gorm.io/driver/postgres"
//
//	gormDB, err := db.Open(postgres.Open(dsn),
//	    db.WithMaxOpenConns(20),
//	    db.WithSlowThreshold(500*time.Millisecond),
//	)
package db

import (
	"fmt"

	"gorm.io/gorm"
)

// Open opens a database connection using dialector, configures connection
// pooling, and attaches the single-line slog logger.
//
// The returned *gorm.DB is the standard GORM handle; no wrapper type is used
// (ISP — callers depend on what they actually need, not a custom interface).
// Call Close during graceful shutdown.
func Open(dialector gorm.Dialector, opts ...Option) (*gorm.DB, error) {
	cfg := defaultConfig()
	for _, o := range opts {
		o(cfg)
	}

	gdb, err := gorm.Open(dialector, &gorm.Config{
		Logger:         newSlogAdapter(cfg),
		TranslateError: true, // maps driver errors (PG 23505, MySQL 1062) to gorm.ErrDuplicatedKey etc.
	})
	if err != nil {
		return nil, fmt.Errorf("db: open: %w", err)
	}

	sqlDB, err := gdb.DB()
	if err != nil {
		return nil, fmt.Errorf("db: get sql.DB: %w", err)
	}
	sqlDB.SetMaxOpenConns(cfg.maxOpenConns)
	sqlDB.SetMaxIdleConns(cfg.maxIdleConns)
	sqlDB.SetConnMaxLifetime(cfg.connMaxLifetime)
	sqlDB.SetConnMaxIdleTime(cfg.connMaxIdleTime)

	return gdb, nil
}

// Close drains in-flight queries and closes the underlying *sql.DB connection
// pool. Call this during graceful shutdown after the HTTP server has finished
// draining — active HTTP handlers may still hold open queries at that point.
func Close(gdb *gorm.DB) error {
	sqlDB, err := gdb.DB()
	if err != nil {
		return fmt.Errorf("db: get sql.DB for close: %w", err)
	}
	return sqlDB.Close()
}
