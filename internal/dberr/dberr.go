// Package dberr classifies the database errors the CMS reacts to, across
// every supported engine.
//
// It replaces the Postgres-only internal/pgutil. Each function checks both
// engines' error types, so call sites need no dialect of their own — an
// error can only have come from the driver that is actually in use.
package dberr

import (
	"errors"

	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/pgconn"
)

// MySQL server error codes.
const (
	mysqlDuplicateEntry = 1062
	// 1451/1452 are the messages with the constraint name; 1216/1217 are the
	// older generic pair some MariaDB versions still return.
	mysqlRowIsReferenced  = 1451 // deleting a parent that still has children
	mysqlNoReferencedRow  = 1452 // inserting a child with no parent
	mysqlRowIsReferenced2 = 1216
	mysqlNoReferencedRow2 = 1217
)

// IsUniqueViolation reports whether err is a unique-constraint violation —
// Postgres SQLSTATE 23505, MySQL error 1062.
func IsUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23505"
	}
	var myErr *mysql.MySQLError
	if errors.As(err, &myErr) {
		return myErr.Number == mysqlDuplicateEntry
	}
	return false
}

// IsForeignKeyViolation reports whether err is a foreign-key violation —
// Postgres SQLSTATE 23503, MySQL errors 1451/1452 (and the older
// 1216/1217) — e.g. referencing a deleted page.
func IsForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23503"
	}
	var myErr *mysql.MySQLError
	if errors.As(err, &myErr) {
		switch myErr.Number {
		case mysqlRowIsReferenced, mysqlNoReferencedRow, mysqlRowIsReferenced2, mysqlNoReferencedRow2:
			return true
		}
	}
	return false
}
