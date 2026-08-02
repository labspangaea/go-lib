// Package bunrepo provides a generic, SOLID-compliant base repository for
// bun-backed services, with cursor pagination and composable filters.
//
// It is the SQL-first counterpart of db/repo. The exported surface is
// deliberately identical wherever the port / service / handler layers touch it
// (CursorParams, OffsetParams, CursorPage, SortKey, Filter, FilterSet and the
// error sentinels), so a service can switch ORMs by changing one import:
//
//	repo "github.com/labspangaea/go-lib/db/bunrepo"
//
// The one type that cannot be shared is Filter: db/repo applies conditions to a
// *gorm.DB, this package to a *bun.SelectQuery.
//
// Not provided here: the cache decorator. db/repo.CachedRepo is generic over a
// repository exposing DB(ctx) *gorm.DB and cannot wrap a bun repository. Use
// db/repo if you need a cache wrapper.
package bunrepo

import "errors"

// ErrNotFound is returned by FindByID when the requested record does not exist.
// Service layer maps this to an application-specific CodeErrEnum before returning
// to callers — repo must not know HTTP status codes.
var ErrNotFound = errors.New("not found")

// ErrDuplicateRecord is returned by Create and Update when the operation is
// rejected by a unique-key or primary-key constraint.
// Triggered by PostgreSQL SQLSTATE 23505 and MySQL error 1062.
var ErrDuplicateRecord = errors.New("duplicate record")

// ErrForeignKeyViolation is returned by Create, Update, and Delete when the
// operation would violate a foreign-key constraint (e.g. referencing a
// non-existent parent row, or deleting a row that is still referenced).
// Triggered by PostgreSQL SQLSTATE 23503 and MySQL errors 1451/1452.
var ErrForeignKeyViolation = errors.New("foreign key violation")

// ErrConstraintViolation is returned by Create and Update when the value fails a
// NOT NULL or database-level CHECK constraint.
// Triggered by PostgreSQL SQLSTATE 23502/23514 and MySQL errors 1048/3819.
var ErrConstraintViolation = errors.New("constraint violation")
