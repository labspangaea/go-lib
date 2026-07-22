package db

import (
	"time"

	gormlogger "gorm.io/gorm/logger"
)

type config struct {
	maxOpenConns    int
	maxIdleConns    int
	connMaxLifetime time.Duration
	connMaxIdleTime time.Duration

	slowThreshold  time.Duration
	logLevel       gormlogger.LogLevel
	ignoreNotFound bool
}

func defaultConfig() *config {
	return &config{
		maxOpenConns:    25,
		maxIdleConns:    5,
		connMaxLifetime: 5 * time.Minute,
		connMaxIdleTime: 1 * time.Minute,

		slowThreshold:  200 * time.Millisecond,
		logLevel:       gormlogger.Warn,
		ignoreNotFound: true,
	}
}

// Option configures a DB connection at construction time.
type Option func(*config)

// WithMaxOpenConns sets the maximum number of open connections in the pool.
// Default: 25.
func WithMaxOpenConns(n int) Option { return func(c *config) { c.maxOpenConns = n } }

// WithMaxIdleConns sets the maximum number of idle connections in the pool.
// Default: 5.
func WithMaxIdleConns(n int) Option { return func(c *config) { c.maxIdleConns = n } }

// WithConnMaxLifetime sets the maximum time a connection may be reused.
// Default: 5m.
func WithConnMaxLifetime(d time.Duration) Option {
	return func(c *config) { c.connMaxLifetime = d }
}

// WithConnMaxIdleTime sets the maximum time a connection may sit idle before
// being closed. Default: 1m.
func WithConnMaxIdleTime(d time.Duration) Option {
	return func(c *config) { c.connMaxIdleTime = d }
}

// WithSlowThreshold sets the elapsed time above which a query is logged at WARN
// level as a slow query. Default: 200ms.
func WithSlowThreshold(d time.Duration) Option {
	return func(c *config) { c.slowThreshold = d }
}

// WithLogLevel sets the GORM log level. Use gormlogger.Info to log all queries,
// gormlogger.Warn for slow queries and errors only (default), gormlogger.Silent
// to suppress all query logging.
func WithLogLevel(lvl gormlogger.LogLevel) Option {
	return func(c *config) { c.logLevel = lvl }
}

// WithIgnoreNotFound controls whether gorm.ErrRecordNotFound is suppressed in
// query error logs. Default: true (not-found is normal; no need to log as ERROR).
func WithIgnoreNotFound(v bool) Option { return func(c *config) { c.ignoreNotFound = v } }
