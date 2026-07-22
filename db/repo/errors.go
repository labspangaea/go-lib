// Package repo provides a generic, SOLID-compliant base repository for GORM-backed
// services, with cursor pagination, composable filters, and an optional cache decorator.
package repo

import "errors"

// ErrNotFound is returned by FindByID when the requested record does not exist.
// Service layer maps this to an application-specific CodeErrEnum before returning
// to callers — repo must not know HTTP status codes.
var ErrNotFound = errors.New("not found")

// ErrDuplicateRecord is returned by Create and Update when the operation is
// rejected by a unique-key or primary-key constraint.
// Triggered by PostgreSQL SQLSTATE 23505 and MySQL error 1062 via gorm.ErrDuplicatedKey.
var ErrDuplicateRecord = errors.New("duplicate record")

// ErrForeignKeyViolation is returned by Create, Update, and Delete when the
// operation would violate a foreign-key constraint (e.g. referencing a
// non-existent parent row, or deleting a row that is still referenced).
// Triggered by PostgreSQL SQLSTATE 23503 and MySQL errors 1451/1452 via gorm.ErrForeignKeyViolated.
var ErrForeignKeyViolation = errors.New("foreign key violation")

// ErrConstraintViolation is returned by Create and Update when the value
// fails a database-level CHECK constraint.
// Triggered by PostgreSQL SQLSTATE 23514 and MySQL error 3819 via gorm.ErrCheckConstraintViolated.
var ErrConstraintViolation = errors.New("constraint violation")
