# storage

Generic, interface-driven object storage abstraction for Go services, with a concrete Huawei OBS backend.

All backends satisfy the same `storage.Storage` interface, so swapping from OBS to AWS S3, MinIO, or any other S3-compatible service is a wiring change — no caller code changes.

---

## Package layout

```
storage/           ← Storage interface, ObjectMeta, PutOption, ErrNotFound
storage/obs/       ← Huawei OBS concrete backend (huaweicloud-sdk-go-obs)
storage/mocks/     ← MockStorage (generated — do not edit by hand)
```

Import only what you need. A service layer that accepts `storage.Storage` imports only the root package. The OBS backend is wired in at the application entry point.

---

## Interface

```go
type Storage interface {
    Put(ctx context.Context, bucket, key string, body io.Reader, opts ...PutOption) error
    Get(ctx context.Context, bucket, key string) (io.ReadCloser, *ObjectMeta, error)
    Delete(ctx context.Context, bucket, key string) error
    Stat(ctx context.Context, bucket, key string) (*ObjectMeta, error)
    PresignGet(ctx context.Context, bucket, key string, ttl time.Duration) (string, error)
    PresignPut(ctx context.Context, bucket, key string, ttl time.Duration) (string, error)
}
```

### Method contracts

| Method | Not-found behaviour | Notes |
|---|---|---|
| `Put` | n/a | Creates or overwrites the object |
| `Get` | returns `ErrNotFound` | Caller must `Close` the returned `io.ReadCloser` |
| `Delete` | returns `ErrNotFound` | |
| `Stat` | returns `ErrNotFound` | Downloads metadata only, no body |
| `PresignGet` | n/a | Returns a temporary URL; `ttl` rounded to seconds |
| `PresignPut` | n/a | Returns a temporary URL for a single upload |

### `ObjectMeta`

Returned by `Get` and `Stat`:

```go
type ObjectMeta struct {
    ContentType   string
    ContentLength int64
    LastModified  time.Time
    ETag          string
}
```

### `ErrNotFound`

```go
var ErrNotFound = errors.New("storage: object not found")
```

All backends translate their SDK-specific 404 error into `ErrNotFound`. Callers use `errors.Is` and never need to import the backend SDK:

```go
_, err := svc.Stat(ctx, bucket, key)
if errors.Is(err, storage.ErrNotFound) {
    // handle missing object
}
```

---

## Put options

```go
storage.WithContentType("image/jpeg")
storage.WithMetadata(map[string]string{"author": "alice"})
```

Pass zero or more `PutOption` values to `Put`:

```go
err := s.Put(ctx, "my-bucket", "images/photo.jpg", file,
    storage.WithContentType("image/jpeg"),
    storage.WithMetadata(map[string]string{"uploaded-by": userID}),
)
```

---

## Huawei OBS backend (`storage/obs`)

```go
import (
    "github.com/labspangaea/go-lib/storage"
    "github.com/labspangaea/go-lib/storage/obs"
)
```

### Creating a client

```go
client, err := obs.New(
    os.Getenv("OBS_ACCESS_KEY"),
    os.Getenv("OBS_SECRET_KEY"),
    "https://obs.ap-southeast-1.myhuaweicloud.com",
)
if err != nil {
    log.Fatal(err)
}
defer client.Close() // releases the HTTP connection pool
```

Endpoint format: `https://obs.<region>.myhuaweicloud.com`

| Region | Endpoint |
|---|---|
| AP Southeast (Singapore) | `obs.ap-southeast-1.myhuaweicloud.com` |
| CN North (Beijing) | `obs.cn-north-4.myhuaweicloud.com` |
| CN South (Guangzhou) | `obs.cn-south-1.myhuaweicloud.com` |
| EU West (Paris) | `obs.eu-west-0.myhuaweicloud.com` |

### Upload

```go
f, _ := os.Open("report.pdf")
defer f.Close()

err := client.Put(ctx, "my-bucket", "reports/2024/report.pdf", f,
    storage.WithContentType("application/pdf"),
)
```

### Download

```go
rc, meta, err := client.Get(ctx, "my-bucket", "reports/2024/report.pdf")
if err != nil {
    if errors.Is(err, storage.ErrNotFound) { /* handle */ }
    return err
}
defer rc.Close()

fmt.Printf("size: %d bytes, type: %s\n", meta.ContentLength, meta.ContentType)
_, _ = io.Copy(w, rc)
```

### Head (metadata only)

```go
meta, err := client.Stat(ctx, "my-bucket", "reports/2024/report.pdf")
if errors.Is(err, storage.ErrNotFound) {
    // object does not exist
}
```

### Delete

```go
err := client.Delete(ctx, "my-bucket", "reports/2024/report.pdf")
```

### Pre-signed URLs

Generate a time-limited URL — useful for direct browser uploads or downloads without
exposing credentials:

```go
// Temporary GET link (15 minutes)
getURL, err := client.PresignGet(ctx, "my-bucket", "reports/2024/report.pdf", 15*time.Minute)

// Temporary PUT link — client uploads directly to OBS, bypassing your server
putURL, err := client.PresignPut(ctx, "my-bucket", "uploads/photo.jpg", 10*time.Minute)
```

---

## Wiring in a service

Accept `storage.Storage` in your service constructor. The backend choice is a wiring detail
at `main` — the service is unaware of it:

```go
// service.go
type DocumentService struct {
    store storage.Storage
}

func NewDocumentService(store storage.Storage) *DocumentService {
    return &DocumentService{store: store}
}

func (s *DocumentService) Upload(ctx context.Context, id string, r io.Reader) error {
    return s.store.Put(ctx, "documents", id, r,
        storage.WithContentType("application/octet-stream"),
    )
}
```

```go
// main.go — production
obsClient, _ := obs.New(ak, sk, endpoint)
defer obsClient.Close()

svc := NewDocumentService(obsClient)
```

```go
// service_test.go — unit test
ctrl := gomock.NewController(t)
mockStore := storagemocks.NewMockStorage(ctrl)
mockStore.EXPECT().Put(gomock.Any(), "documents", "doc-1", gomock.Any()).Return(nil)

svc := NewDocumentService(mockStore)
```

---

## Regenerating mocks

```bash
go generate ./storage/...
```

The `//go:generate` directive is in `storage/storage.go`. Never edit `storage/mocks/mock_storage.go` by hand.

---

## Design notes

**`ErrNotFound` as a sentinel** — each backend translates its SDK-specific 404 error into `storage.ErrNotFound`. Callers never need to import `github.com/huaweicloud/huaweicloud-sdk-go-obs/obs` just to check for a missing object.

**`context.Context` accepted everywhere** — the OBS SDK does not support context cancellation internally, but the parameter is there for consistency, logging integration, and future-proofing when the SDK adds support.

**`PutOption` functional options** — adding a new option (`WithACL`, `WithEncryption`) is a non-breaking change. The interface and all existing callers remain unchanged.

**`Client.Close()`** — the OBS client holds an HTTP connection pool. Always defer `Close` to avoid leaking connections, especially in long-running services.
