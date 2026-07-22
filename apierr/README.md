# apierr

Typed API error codes for Go services. Provides a clean mapping from error category to HTTP status code, user-facing message, and machine-readable code — without coupling to any HTTP framework.

---

## Why a typed error code package?

Plain `errors.New("not found")` strings break `errors.Is` matching and carry no HTTP status or machine-readable code. A `CodeErrEnum` constant is:

- **Comparable** — `errors.As(err, &code)` works across package boundaries
- **Extensible** — services register domain-specific codes at `init` time
- **Framework-agnostic** — no Fiber, no Gin; works with `net/http`, gRPC, or any transport

---

## Interfaces

```go
// CodeErrEnum is a typed integer identifying an error category.
type CodeErrEnum int

// CodeErr carries the full error descriptor.
type CodeErr struct {
    Message    string // user-facing message
    Detail     string // internal detail (strip in production responses)
    Code       string // machine-readable code, e.g. "ERR404000"
    StatusCode int    // HTTP status code
}
```

Both types implement the `error` interface so they work with `errors.Is`/`errors.As`:

```go
func handler(w http.ResponseWriter, r *http.Request) {
    order, err := svc.GetOrder(r.Context(), id)
    if err != nil {
        var codeErr apierr.CodeErr
        if errors.As(err, &codeErr) {
            response.Error(codeErr).Write(w)
            return
        }
        response.Error(apierr.CodeErrGeneral.GetCodeErr()).Write(w)
        return
    }
    response.New[*OrderDTO]().WithData(toDTO(order)).Write(w)
}
```

---

## Built-in codes

| Constant | Code | HTTP |
|---|---|---|
| `CodeErrGeneral` | `ERR500000` | 500 |
| `CodeErrNotFound` | `ERR404000` | 404 |
| `CodeErrBadRequest` | `ERR400000` | 400 |
| `CodeErrValidation` | `ERR400001` | 400 |
| `CodeErrUnauthorized` | `ERR401000` | 401 |
| `CodeErrForbidden` | `ERR403000` | 403 |
| `CodeErrConflict` | `ERR409000` | 409 |
| `CodeErrTooManyRequests` | `ERR429000` | 429 |
| `CodeErrUnprocessable` | `ERR422000` | 422 |

---

## Usage

### Returning errors from service layer

```go
// Return a CodeErrEnum — caller can errors.As to get details
func (s *OrderService) GetOrder(ctx context.Context, id string) (*Order, error) {
    o, err := s.repo.Find(ctx, id)
    if errors.Is(err, repo.ErrNotFound) {
        return nil, apierr.CodeErrNotFound
    }
    return o, err
}

// Return a CodeErr with internal detail attached
func (s *OrderService) CreateOrder(ctx context.Context, req CreateRequest) (*Order, error) {
    if err := validate(req); err != nil {
        return nil, apierr.CodeErrValidation.WithDetail(err)
    }
    // ...
}
```

### Handling errors in HTTP handlers

```go
func respondErr(w http.ResponseWriter, err error) {
    var codeErr apierr.CodeErr
    if errors.As(err, &codeErr) {
        response.Error(codeErr).Write(w)
        return
    }
    var code apierr.CodeErrEnum
    if errors.As(err, &code) {
        response.Error(code.GetCodeErr()).Write(w)
        return
    }
    // unknown error — log it, return 500
    response.Error(apierr.CodeErrGeneral.GetCodeErr()).Write(w)
}
```

### Stripping internal detail in production

The `Detail` field carries the internal cause. Strip it from responses in production:

```go
func respondErr(w http.ResponseWriter, err error, debug bool) {
    var codeErr apierr.CodeErr
    if !errors.As(err, &codeErr) {
        codeErr = apierr.CodeErrGeneral.GetCodeErr()
        codeErr.Detail = err.Error()
    }
    if !debug {
        codeErr.Detail = "" // never expose internals in production
    }
    response.Error(codeErr).Write(w)
}
```

---

## Extending with service-specific codes

Define service codes in a separate const block starting well above 100 to avoid collisions with library-defined values:

```go
// order/errors.go
const (
    ErrOrderAlreadyShipped apierr.CodeErrEnum = 1000 + iota
    ErrOrderCancelled
    ErrInsufficientStock
)

func init() {
    apierr.AppendCodeErrMap(ErrOrderAlreadyShipped, apierr.CodeErr{
        Message:    "Order has already been shipped",
        Code:       "ERR409100",
        StatusCode: http.StatusConflict,
    })
    apierr.AppendCodeErrMap(ErrOrderCancelled, apierr.CodeErr{
        Message:    "Order has been cancelled",
        Code:       "ERR409101",
        StatusCode: http.StatusConflict,
    })
    apierr.AppendCodeErrMap(ErrInsufficientStock, apierr.CodeErr{
        Message:    "Insufficient stock",
        Code:       "ERR422100",
        StatusCode: http.StatusUnprocessableEntity,
    })
}
```

`AppendCodeErrMap` is not goroutine-safe — call only during `init()` or before `main` starts serving.
