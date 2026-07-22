# Mocks

Generated mocks for all public interfaces, using [`go.uber.org/mock`](https://github.com/uber-go/mock) — the maintained successor to `golang/mock`.

---

## Package layout

```
cache/mocks/mock_cache.go          ← MockGetter[V], MockSetter[V], MockDeleter, MockCache[V]
httpx/client/mocks/mock_doer.go    ← MockDoer
```

---

## Regenerating mocks

Run after changing any interface:

```bash
go generate ./cache/...
go generate ./httpx/client/...
```

Or all at once:

```bash
go generate ./...
```

Each `//go:generate` directive is co-located with the interface it mocks:

| Source file | Directive location | Output |
|---|---|---|
| `cache/cache.go` | top of file | `cache/mocks/mock_cache.go` |
| `httpx/client/request.go` | top of file | `httpx/client/mocks/mock_doer.go` |

Never edit generated files by hand — they are overwritten on every `go generate`.

---

## Importing mocks

```go
import (
    cachemocks  "github.com/labspangaea/go-lib/cache/mocks"
    clientmocks "github.com/labspangaea/go-lib/httpx/client/mocks"
    "go.uber.org/mock/gomock"
)
```

---

## Usage examples

### `MockCache[V]` — cache-aside service test

Scenario: `UserService.GetUser` reads from cache on a hit and fetches + writes on a miss.

```go
func TestUserService_GetUser_CacheHit(t *testing.T) {
    ctrl := gomock.NewController(t)

    want := &User{ID: 42, Name: "alice"}

    c := cachemocks.NewMockCache[*User](ctrl)
    c.EXPECT().
        Get(gomock.Any(), "users:42").
        Return(want, true, nil)
    // Set and Delete must NOT be called — the mock will fail the test if they are.

    svc := NewUserService(c)
    got, err := svc.GetUser(context.Background(), 42)
    if err != nil {
        t.Fatal(err)
    }
    if got != want {
        t.Errorf("got %+v, want %+v", got, want)
    }
}

func TestUserService_GetUser_CacheMiss(t *testing.T) {
    ctrl := gomock.NewController(t)

    want := &User{ID: 42, Name: "alice"}

    c := cachemocks.NewMockCache[*User](ctrl)
    c.EXPECT().
        Get(gomock.Any(), "users:42").
        Return(nil, false, nil)                             // miss
    c.EXPECT().
        Set(gomock.Any(), "users:42", want, 5*time.Minute).
        Return(nil)                                         // write-back

    svc := NewUserService(c)
    got, err := svc.GetUser(context.Background(), 42)
    if err != nil {
        t.Fatal(err)
    }
    if got != want {
        t.Errorf("got %+v, want %+v", got, want)
    }
}
```

### `MockCache[V]` — cache error path

```go
func TestUserService_GetUser_CacheError(t *testing.T) {
    ctrl := gomock.NewController(t)

    c := cachemocks.NewMockCache[*User](ctrl)
    c.EXPECT().
        Get(gomock.Any(), "users:42").
        Return(nil, false, errors.New("redis: connection refused"))

    svc := NewUserService(c)
    _, err := svc.GetUser(context.Background(), 42)
    if err == nil {
        t.Fatal("expected error, got nil")
    }
}
```

### `MockGetter[V]` — read-only dependency

When a function only reads from the cache, its parameter should be `cache.Getter[V]`,
not the full `cache.Cache[V]`. Use `MockGetter` to match that narrower type:

```go
func FindActiveUsers(ctx context.Context, g cache.Getter[*User], ids []int64) ([]*User, error) { ... }

func TestFindActiveUsers(t *testing.T) {
    ctrl := gomock.NewController(t)

    g := cachemocks.NewMockGetter[*User](ctrl)
    g.EXPECT().Get(gomock.Any(), "users:1").Return(&User{ID: 1}, true, nil)
    g.EXPECT().Get(gomock.Any(), "users:2").Return(nil, false, nil)

    users, err := FindActiveUsers(context.Background(), g, []int64{1, 2})
    // ...
}
```

### `MockDoer` — HTTP client test

`MockDoer` replaces `*http.Client` in tests so no real server is needed.

```go
func TestOrderClient_FetchOrder(t *testing.T) {
    ctrl := gomock.NewController(t)

    body, _ := json.Marshal(Order{ID: 99, Status: "shipped"})
    mockResp := &http.Response{
        StatusCode: http.StatusOK,
        Header:     http.Header{"Content-Type": {"application/json"}},
        Body:       io.NopCloser(bytes.NewReader(body)),
    }

    d := clientmocks.NewMockDoer(ctrl)
    d.EXPECT().
        Do(gomock.Any()).
        Return(mockResp, nil)

    c := NewOrderClient(d)
    order, err := c.FetchOrder(context.Background(), 99)
    if err != nil {
        t.Fatal(err)
    }
    if order.Status != "shipped" {
        t.Errorf("status = %q, want shipped", order.Status)
    }
}
```

### `MockDoer` — asserting request shape

Use `gomock.AssertAction` / a custom matcher to verify the outbound request:

```go
d.EXPECT().
    Do(gomock.AssertThat(func(r *http.Request) bool {
        return r.Method == http.MethodPost &&
            r.Header.Get("X-Tenant") == "acme"
    })).
    Return(mockResp, nil)
```

Or with a named matcher for a clearer failure message:

```go
type methodMatcher struct{ method string }

func (m methodMatcher) Matches(x any) bool {
    r, ok := x.(*http.Request)
    return ok && r.Method == m.method
}
func (m methodMatcher) String() string { return "request method " + m.method }

d.EXPECT().Do(methodMatcher{http.MethodDelete}).Return(emptyResp, nil)
```

---

## gomock quick reference

| Call | Meaning |
|---|---|
| `EXPECT().Method(gomock.Any())` | called with any argument |
| `EXPECT().Method(gomock.Eq(v))` | called with exactly `v` |
| `.Return(...)` | what the mock returns |
| `.Times(n)` | must be called exactly `n` times |
| `.AnyTimes()` | may be called zero or more times |
| `.DoAndReturn(fn)` | call `fn` and use its return values |
| `gomock.InOrder(c1, c2, c3)` | enforce call ordering |

`gomock.NewController(t)` automatically verifies all expectations at the end of the test — no `ctrl.Finish()` call needed in Go 1.14+.
