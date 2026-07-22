// Package obs provides a Huawei OBS (Object Storage Service) backend that
// satisfies the storage.Storage interface. OBS is S3-compatible; swapping to
// another backend only requires changing the wiring, not the caller code.
//
// # Context cancellation
//
// The Huawei OBS SDK (v3.x) does not expose per-request context or timeout on
// individual operation inputs. Context cancellation is therefore enforced at two
// points only:
//
//  1. Pre-flight: if ctx is already done when a method is called, the method
//     returns immediately with ctx.Err() — no SDK call is made.
//  2. Client-level socket timeout: New accepts a WithDefaultTimeout option that
//     sets the SDK socket/header timeout for all operations on that client.
//     When the caller provides a context with a deadline that is tighter than
//     the socket timeout, use a short-lived client or the WithDefaultTimeout
//     option to match the desired bound.
//
// In-flight cancellation (context cancelled while the SDK is waiting on I/O) is
// NOT supported; the underlying HTTP round-trip will continue until the SDK
// socket timeout fires. This is a known limitation of the SDK.
package obs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	goobs "github.com/huaweicloud/huaweicloud-sdk-go-obs/obs"

	"github.com/labspangaea/go-lib/storage"
)

const defaultTimeoutSec = 30

// Client wraps the Huawei OBS SDK and implements storage.Storage.
type Client struct {
	obs            *goobs.ObsClient
	defaultTimeout int // socket/header timeout in seconds, applied at client level
}

// Option configures the OBS client at construction time.
type Option func(*clientConfig)

type clientConfig struct {
	defaultTimeout int
}

// WithDefaultTimeout sets the SDK socket and header timeout (in seconds) for
// all operations on the client. Defaults to 30 s when not specified.
// Use this to align the client timeout with the tightest context deadline your
// callers are likely to supply.
func WithDefaultTimeout(seconds int) Option {
	return func(cfg *clientConfig) {
		if seconds > 0 {
			cfg.defaultTimeout = seconds
		}
	}
}

// New creates a Client connected to the given OBS endpoint.
//
// ak and sk are the Access Key ID and Secret Access Key.
// endpoint format: "https://obs.<region>.myhuaweicloud.com"
//
// The returned Client must be closed with Close when no longer needed.
func New(ak, sk, endpoint string, opts ...Option) (*Client, error) {
	cfg := &clientConfig{defaultTimeout: defaultTimeoutSec}
	for _, o := range opts {
		o(cfg)
	}

	c, err := goobs.New(ak, sk, endpoint,
		goobs.WithSocketTimeout(cfg.defaultTimeout),
		goobs.WithHeaderTimeout(cfg.defaultTimeout),
	)
	if err != nil {
		return nil, fmt.Errorf("obs: create client: %w", err)
	}
	return &Client{obs: c, defaultTimeout: cfg.defaultTimeout}, nil
}

// Close releases the underlying HTTP connection pool. Always call it — typically
// via defer — before the process exits or when the client is no longer needed.
func (c *Client) Close() {
	c.obs.Close()
}

// Put uploads body to bucket/key.
// Use storage.WithContentType and storage.WithMetadata to set object attributes.
//
// Returns ctx.Err() immediately if ctx is already done before the SDK call.
// In-flight cancellation is not supported; see package-level doc for details.
func (c *Client) Put(ctx context.Context, bucket, key string, body io.Reader, opts ...storage.PutOption) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	cfg := storage.ApplyPutOptions(opts...)

	input := &goobs.PutObjectInput{}
	input.Bucket = bucket
	input.Key = key
	input.Body = body
	input.ContentType = cfg.ContentType()
	input.Metadata = cfg.Metadata()

	if _, err := c.obs.PutObject(input); err != nil {
		return fmt.Errorf("obs: put %s/%s: %w", bucket, key, wrapOBSErr(err))
	}
	return nil
}

// Get downloads the object at bucket/key.
// The caller must close the returned ReadCloser when done.
// Returns storage.ErrNotFound when the object does not exist.
//
// Returns ctx.Err() immediately if ctx is already done before the SDK call.
// In-flight cancellation is not supported; see package-level doc for details.
func (c *Client) Get(ctx context.Context, bucket, key string) (io.ReadCloser, *storage.ObjectMeta, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}

	input := &goobs.GetObjectInput{}
	input.Bucket = bucket
	input.Key = key

	out, err := c.obs.GetObject(input)
	if err != nil {
		return nil, nil, fmt.Errorf("obs: get %s/%s: %w", bucket, key, wrapOBSErr(err))
	}
	return out.Body, &storage.ObjectMeta{
		ContentType:   out.ContentType,
		ContentLength: out.ContentLength,
		LastModified:  out.LastModified,
		ETag:          out.ETag,
	}, nil
}

// Delete removes the object at bucket/key.
// Returns storage.ErrNotFound when the object does not exist.
//
// Returns ctx.Err() immediately if ctx is already done before the SDK call.
// In-flight cancellation is not supported; see package-level doc for details.
func (c *Client) Delete(ctx context.Context, bucket, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	input := &goobs.DeleteObjectInput{}
	input.Bucket = bucket
	input.Key = key

	if _, err := c.obs.DeleteObject(input); err != nil {
		return fmt.Errorf("obs: delete %s/%s: %w", bucket, key, wrapOBSErr(err))
	}
	return nil
}

// Stat returns object metadata without downloading the body.
// Returns storage.ErrNotFound when the object does not exist.
//
// Returns ctx.Err() immediately if ctx is already done before the SDK call.
// In-flight cancellation is not supported; see package-level doc for details.
func (c *Client) Stat(ctx context.Context, bucket, key string) (*storage.ObjectMeta, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	input := &goobs.GetObjectMetadataInput{}
	input.Bucket = bucket
	input.Key = key

	out, err := c.obs.GetObjectMetadata(input)
	if err != nil {
		return nil, fmt.Errorf("obs: stat %s/%s: %w", bucket, key, wrapOBSErr(err))
	}
	return &storage.ObjectMeta{
		ContentType:   out.ContentType,
		ContentLength: out.ContentLength,
		LastModified:  out.LastModified,
		ETag:          out.ETag,
	}, nil
}

// PresignGet returns a pre-signed URL granting temporary GET access to bucket/key.
// ttl is rounded down to the nearest second; minimum effective value is 1s.
//
// Returns ctx.Err() immediately if ctx is already done before the SDK call.
func (c *Client) PresignGet(ctx context.Context, bucket, key string, ttl time.Duration) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return c.presign(goobs.HttpMethodGet, bucket, key, ttl)
}

// PresignPut returns a pre-signed URL granting temporary PUT access to bucket/key.
// ttl is rounded down to the nearest second; minimum effective value is 1s.
//
// Returns ctx.Err() immediately if ctx is already done before the SDK call.
func (c *Client) PresignPut(ctx context.Context, bucket, key string, ttl time.Duration) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return c.presign(goobs.HttpMethodPut, bucket, key, ttl)
}

func (c *Client) presign(method goobs.HttpMethodType, bucket, key string, ttl time.Duration) (string, error) {
	input := &goobs.CreateSignedUrlInput{
		Method:  method,
		Bucket:  bucket,
		Key:     key,
		Expires: int(ttl.Seconds()),
	}
	out, err := c.obs.CreateSignedUrl(input)
	if err != nil {
		return "", fmt.Errorf("obs: presign %s %s/%s: %w", method, bucket, key, err)
	}
	return out.SignedUrl, nil
}

// wrapOBSErr converts a 404 ObsError into storage.ErrNotFound so callers can
// use errors.Is without importing the OBS SDK. The original error is preserved
// in the chain for debugging (e.g., OBS request ID).
func wrapOBSErr(err error) error {
	var obsErr goobs.ObsError
	if errors.As(err, &obsErr) && obsErr.StatusCode == http.StatusNotFound {
		return fmt.Errorf("%w: %v", storage.ErrNotFound, err)
	}
	return err
}
