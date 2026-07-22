# CLAUDE.md — go-lib

**Role**: Shared infrastructure library for Go services at Labspangaea.  
**Constraint**: Zero framework dependencies (no gin, echo, chi) in the module itself.  
**API reference**: `.claude/codebase.md` — full type/function signatures and usage examples.

---

## Commands

| Command | Scope | Purpose |
|---------|-------|---------|
| `go build ./...` | all packages | compile check |
| `go vet ./...` | all packages | static analysis |
| `go test ./...` | all packages | run all tests |
| `go test ./cache/...` | single package | test one package |
| `go test -run TestChain ./httpx/server/...` | single function | test one function |
| `go generate ./...` | all packages | regenerate all mocks |
| `go generate ./cache/...` | single package | regenerate one package's mocks |

---

## Go LSP (MCP)

**MUST**: Run `mcp__go-lsp__go_diagnose` after writing or editing any `.go` file before reporting done.  
**SHOULD**: Use `go build ./...` only to verify whole-module link.

| Task | Tool |
|------|------|
| Type errors / unused imports / syntax | `mcp__go-lsp__go_diagnose` |
| Jump to definition | `mcp__go-lsp__go_definition` |
| Hover docs / type info | `mcp__go-lsp__go_hover` |
| Find all references | `mcp__go-lsp__go_references` |
| List symbols in file | `mcp__go-lsp__go_symbols` |
| Rename symbol | `mcp__go-lsp__go_rename` |
| Interface implementations | `mcp__go-lsp__go_implementation` |
| Signature help | `mcp__go-lsp__go_signature` |
| Completions | `mcp__go-lsp__go_complete` |

---

## Architecture

### Design Patterns

| Pattern | Do | Don't |
|---------|----|-------|
| Interface segregation — accept narrowest interface | `func Load(g cache.Getter[V])` | `func Load(c cache.Cache[V])` when only reading |
| Functional options — defaults first, then overrides | `New(opts ...Option)` | `New(addr, timeout, maxConn string)` |
| No-op defaults — zero-cost test double for every abstraction | `cache.Nop[V]()` in unit tests | real Redis in unit tests |
| Mocks — `//go:generate` at top of interface file, output to `mocks/` | run `go generate ./...` | edit files inside `mocks/` by hand |

### Package Map

| Package | Key Interface(s) | Implementations | Test double |
|---------|-----------------|-----------------|-------------|
| `cache/` | `Cache[V]`, `Getter[V]`, `Setter[V]`, `Deleter` | `redis/`, `memory/`, `couchbase/` | `cache.Nop[V]()` |
| `pubsub/` | `Publisher`, `Consumer` | `kafka/`, `rabbitmq/`, `redis/` | mock via `go generate` |
| `distlock/` | `Locker`, `Lock` | `redis/` | mock via `go generate` |
| `storage/` | `Storage` | `obs/` (Huawei OBS) | mock via `go generate` |
| `db/` | returns `*gorm.DB` | PostgreSQL (`gorm.io/driver/postgres`) | — |
| `logger/` | wraps `*slog.Logger` | context-aware | `logger.Nop()` |
| `telemetry/` | `ShutdownFunc` | OTLP/gRPC (traces + logs) | — |
| `httpx/humaresponse/` | `DataOutput[T]`, `ListOutput[T]`, `ErrorBody` | huma v2 JSON envelope wrappers | — |
| `httpx/server/` | `Chain`, `Recover`, `RequestID`, `Logging` middleware | net/http | — |
| `httpx/client/` | `Doer` | typed generic helpers | mock `Doer` |
| `apierr/` | `CodeErrEnum`, `CodeErr` (both implement `huma.StatusError`) | — | — |

### Key Usage Rules

| Concern | Severity | Rule |
|---------|----------|------|
| Logger retrieval | MUST | `logger.FromContext(ctx)` — never nil, returns `Nop()` if absent |
| Logger injection | MUST NOT | inject `*slog.Logger` as a struct field |
| Logger storage | MUST | `logger.WithLogger(ctx, log)` at composition root |
| Error registry | MUST | `apierr.AppendCodeErrMap` called in `init()`, never inside handlers |
| Error dispatch | MUST | huma handlers — `return nil, err`; the `huma.NewError` override in main routes through `humares.NewError` so the JSON envelope (`status`, `message`, `error_detail`, `error_code`) stays consistent. `apierr.CodeErr` / `CodeErrEnum` implement `huma.StatusError` so they set the HTTP status. |
| Telemetry shutdown | MUST | `defer otelShutdown(ctx)` — non-fatal on setup error, continue without tracing |
| Trace correlation | SHOULD | `logger.WithTraceContext` / `WithRequestID` — attach OTel IDs to ctx before passing to handlers |
| Distributed lock fencing | MUST | pass `Lock.Token()` to storage writes inside `WithLock` closure |
| Mock files | MUST NOT | edit generated files in `mocks/` — run `go generate` to regenerate |

### Anti-Patterns

| Anti-pattern | Correct pattern | Why |
|---|---|---|
| `type S struct { log *slog.Logger }` | `l := logger.FromContext(ctx)` inside method | breaks trace correlation |
| `_ = err` | handle or wrap and return | silent failures |
| edit `mocks/*.go` | `go generate ./...` | overwritten on next generate |
| `apierr.AppendCodeErrMap` inside handler | call in `init()` only | race on first request |
| `service/` importing `adapter/` | `service/` imports `port/` only | violates DIP |
| real Redis/DB in unit tests | `cache.Nop[V]()`, mocks | slow, requires infra |
