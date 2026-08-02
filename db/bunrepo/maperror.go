package bunrepo

import (
	"database/sql"
	"errors"

	"github.com/go-sql-driver/mysql"
)

// sqlstateField is the PostgreSQL ErrorResponse field code carrying the SQLSTATE
// value, per the wire protocol. pgdriver.Error.Field('C') returns it.
const sqlstateField = 'C'

// PostgreSQL SQLSTATE class 23 — integrity constraint violation.
const (
	pgNotNullViolation    = "23502"
	pgForeignKeyViolation = "23503"
	pgUniqueViolation     = "23505"
	pgCheckViolation      = "23514"
)

// MySQL server error numbers for the same conditions.
const (
	myDuplicateEntry     = 1062
	myRowIsReferenced    = 1451
	myNoReferencedRow    = 1452
	myBadNullError       = 1048
	myCheckConstraintErr = 3819
)

// MapError translates a driver error to a repo sentinel, returning nil when err
// is not a recognised constraint violation.
//
// db/repo gets this for free from GORM's TranslateError. bun is SQL-first and
// surfaces the raw driver error, so the mapping is explicit here — which also
// means it is testable without a live database.
func MapError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}

	// Matched through an interface rather than the concrete pgdriver.Error so the
	// SQLSTATE mapping is unit-testable: pgdriver.Error's fields are unexported
	// and it has no constructor, so a test can never build a 23505 instance.
	var pgErr interface{ Field(byte) string }
	if errors.As(err, &pgErr) {
		switch pgErr.Field(sqlstateField) {
		case pgUniqueViolation:
			return ErrDuplicateRecord
		case pgForeignKeyViolation:
			return ErrForeignKeyViolation
		case pgNotNullViolation, pgCheckViolation:
			return ErrConstraintViolation
		}
		return nil
	}

	var myErr *mysql.MySQLError
	if errors.As(err, &myErr) {
		switch myErr.Number {
		case myDuplicateEntry:
			return ErrDuplicateRecord
		case myRowIsReferenced, myNoReferencedRow:
			return ErrForeignKeyViolation
		case myBadNullError, myCheckConstraintErr:
			return ErrConstraintViolation
		}
	}

	return nil
}
