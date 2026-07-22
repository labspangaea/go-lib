package storage_test

import (
	"testing"

	"github.com/labspangaea/go-lib/storage"
)

func TestApplyPutOptions_Empty(t *testing.T) {
	cfg := storage.ApplyPutOptions()
	if cfg.ContentType() != "" {
		t.Fatalf("expected empty ContentType, got %q", cfg.ContentType())
	}
	if cfg.Metadata() != nil {
		t.Fatalf("expected nil Metadata, got %v", cfg.Metadata())
	}
}

func TestApplyPutOptions_WithContentType(t *testing.T) {
	cfg := storage.ApplyPutOptions(storage.WithContentType("image/png"))
	if cfg.ContentType() != "image/png" {
		t.Fatalf("expected image/png, got %q", cfg.ContentType())
	}
}

func TestApplyPutOptions_WithMetadata(t *testing.T) {
	m := map[string]string{"author": "harry", "version": "1"}
	cfg := storage.ApplyPutOptions(storage.WithMetadata(m))

	got := cfg.Metadata()
	if got["author"] != "harry" || got["version"] != "1" {
		t.Fatalf("expected metadata {author:harry, version:1}, got %v", got)
	}
}

func TestApplyPutOptions_Multiple(t *testing.T) {
	m := map[string]string{"env": "prod"}
	cfg := storage.ApplyPutOptions(
		storage.WithContentType("application/pdf"),
		storage.WithMetadata(m),
	)
	if cfg.ContentType() != "application/pdf" {
		t.Fatalf("ContentType = %q, want application/pdf", cfg.ContentType())
	}
	if cfg.Metadata()["env"] != "prod" {
		t.Fatalf("Metadata[env] = %q, want prod", cfg.Metadata()["env"])
	}
}

func TestErrNotFound_IsError(t *testing.T) {
	var err error = storage.ErrNotFound
	if err.Error() != "storage: object not found" {
		t.Fatalf("unexpected error message: %q", err.Error())
	}
}
