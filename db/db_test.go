package db_test

import (
	"testing"
	"time"

	gormlogger "gorm.io/gorm/logger"

	"github.com/labspangaea/go-lib/db"
)

func TestWithMaxOpenConns(t *testing.T) {
	opt := db.WithMaxOpenConns(50)
	if opt == nil {
		t.Fatal("WithMaxOpenConns returned nil")
	}
}

func TestWithMaxIdleConns(t *testing.T) {
	opt := db.WithMaxIdleConns(10)
	if opt == nil {
		t.Fatal("WithMaxIdleConns returned nil")
	}
}

func TestWithConnMaxLifetime(t *testing.T) {
	opt := db.WithConnMaxLifetime(10 * time.Minute)
	if opt == nil {
		t.Fatal("WithConnMaxLifetime returned nil")
	}
}

func TestWithConnMaxIdleTime(t *testing.T) {
	opt := db.WithConnMaxIdleTime(2 * time.Minute)
	if opt == nil {
		t.Fatal("WithConnMaxIdleTime returned nil")
	}
}

func TestWithSlowThreshold(t *testing.T) {
	opt := db.WithSlowThreshold(500 * time.Millisecond)
	if opt == nil {
		t.Fatal("WithSlowThreshold returned nil")
	}
}

func TestWithLogLevel(t *testing.T) {
	opt := db.WithLogLevel(gormlogger.Info)
	if opt == nil {
		t.Fatal("WithLogLevel returned nil")
	}
}

func TestWithIgnoreNotFound(t *testing.T) {
	opt := db.WithIgnoreNotFound(false)
	if opt == nil {
		t.Fatal("WithIgnoreNotFound returned nil")
	}
}
