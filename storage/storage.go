// Package storage defines a minimal, S3-compatible object storage abstraction.
// Concrete backends live in sub-packages (obs/) so callers import only what they need.
//
//go:generate go run go.uber.org/mock/mockgen@v0.6.0 -source=storage.go -destination=mocks/mock_storage.go -package=mocks
package storage

import (
	"context"
	"errors"
	"io"
	"time"
)

// ErrNotFound is returned by Get, Stat, and Delete when the object does not exist.
var ErrNotFound = errors.New("storage: object not found")

// Storage is the minimal S3-compatible interface for object storage operations.
// Backends — Huawei OBS, AWS S3, MinIO, GCS — satisfy this interface so callers
// depend on the abstraction, not the SDK.
type Storage interface {
	// Put uploads body under bucket/key.
	Put(ctx context.Context, bucket, key string, body io.Reader, opts ...PutOption) error

	// Get downloads the object at bucket/key.
	// The caller must close the returned ReadCloser when done.
	// Returns ErrNotFound when the object does not exist.
	Get(ctx context.Context, bucket, key string) (io.ReadCloser, *ObjectMeta, error)

	// Delete removes the object at bucket/key.
	// Returns ErrNotFound when the object does not exist.
	Delete(ctx context.Context, bucket, key string) error

	// Stat returns object metadata without downloading the body.
	// Returns ErrNotFound when the object does not exist.
	Stat(ctx context.Context, bucket, key string) (*ObjectMeta, error)

	// PresignGet returns a pre-signed URL granting temporary GET access.
	PresignGet(ctx context.Context, bucket, key string, ttl time.Duration) (string, error)

	// PresignPut returns a pre-signed URL granting temporary PUT access.
	PresignPut(ctx context.Context, bucket, key string, ttl time.Duration) (string, error)
}

// ObjectMeta holds metadata returned by Get and Stat.
type ObjectMeta struct {
	ContentType   string
	ContentLength int64
	LastModified  time.Time
	ETag          string
}

// PutConfig holds resolved Put options. Concrete backends call ApplyPutOptions
// to read what the caller requested.
type PutConfig struct {
	contentType string
	metadata    map[string]string
}

// ContentType returns the MIME type to set on the uploaded object.
func (c *PutConfig) ContentType() string { return c.contentType }

// Metadata returns custom key-value pairs to attach to the object.
func (c *PutConfig) Metadata() map[string]string { return c.metadata }

// PutOption configures a Put call.
type PutOption func(*PutConfig)

// WithContentType sets the Content-Type of the uploaded object.
func WithContentType(ct string) PutOption {
	return func(c *PutConfig) { c.contentType = ct }
}

// WithMetadata attaches arbitrary key-value metadata to the uploaded object.
func WithMetadata(m map[string]string) PutOption {
	return func(c *PutConfig) { c.metadata = m }
}

// ApplyPutOptions applies opts and returns the resolved config.
// Concrete backends call this inside their Put implementation.
func ApplyPutOptions(opts ...PutOption) *PutConfig {
	c := &PutConfig{}
	for _, o := range opts {
		o(c)
	}
	return c
}
