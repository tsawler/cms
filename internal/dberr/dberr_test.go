package dberr

import (
	"database/sql"
	"errors"
	"fmt"
	"testing"

	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/pgconn"
)

// These two functions are how the CMS tells "you already have one of
// those" and "that thing is still referenced" apart from a real database
// failure — a duplicate slug becomes a form error, anything else becomes a
// 500. The codes are per-engine, and a wrong constant is invisible until
// someone runs on the engine that uses it, so each one is pinned here
// against a fabricated driver error rather than a live database.

func TestIsUniqueViolation(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"postgres 23505", &pgconn.PgError{Code: "23505"}, true},
		{"mysql 1062", &mysql.MySQLError{Number: 1062}, true},
		// Wrapping is the normal case: the stores return fmt.Errorf("%w").
		{"wrapped postgres", fmt.Errorf("inserting page: %w", &pgconn.PgError{Code: "23505"}), true},
		{"wrapped mysql", fmt.Errorf("inserting page: %w", &mysql.MySQLError{Number: 1062}), true},

		{"postgres foreign key", &pgconn.PgError{Code: "23503"}, false},
		{"postgres not null", &pgconn.PgError{Code: "23502"}, false},
		{"mysql foreign key", &mysql.MySQLError{Number: 1452}, false},
		{"mysql deadlock", &mysql.MySQLError{Number: 1213}, false},
		{"no rows", sql.ErrNoRows, false},
		{"plain error", errors.New("connection refused"), false},
		{"nil", nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := IsUniqueViolation(c.err); got != c.want {
				t.Errorf("IsUniqueViolation(%v) = %v, want %v", c.err, got, c.want)
			}
		})
	}
}

func TestIsForeignKeyViolation(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"postgres 23503", &pgconn.PgError{Code: "23503"}, true},
		// 1451 is deleting a parent that still has children, 1452 is
		// inserting a child with no parent, and 1216/1217 are the older
		// generic pair some MariaDB versions still return — all four have
		// to read as the same thing to a call site with no dialect.
		{"mysql 1451", &mysql.MySQLError{Number: 1451}, true},
		{"mysql 1452", &mysql.MySQLError{Number: 1452}, true},
		{"mariadb 1216", &mysql.MySQLError{Number: 1216}, true},
		{"mariadb 1217", &mysql.MySQLError{Number: 1217}, true},
		{"wrapped postgres", fmt.Errorf("deleting page: %w", &pgconn.PgError{Code: "23503"}), true},
		{"wrapped mysql", fmt.Errorf("deleting page: %w", &mysql.MySQLError{Number: 1451}), true},

		{"postgres unique", &pgconn.PgError{Code: "23505"}, false},
		{"mysql duplicate", &mysql.MySQLError{Number: 1062}, false},
		{"mysql other", &mysql.MySQLError{Number: 1146}, false},
		{"no rows", sql.ErrNoRows, false},
		{"plain error", errors.New("connection refused"), false},
		{"nil", nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := IsForeignKeyViolation(c.err); got != c.want {
				t.Errorf("IsForeignKeyViolation(%v) = %v, want %v", c.err, got, c.want)
			}
		})
	}
}

// The two classifications must not overlap: a call site that checks one
// then the other would otherwise turn a duplicate into a "still in use"
// message, or the reverse.
func TestClassificationsAreDisjoint(t *testing.T) {
	errs := []error{
		&pgconn.PgError{Code: "23505"},
		&pgconn.PgError{Code: "23503"},
		&mysql.MySQLError{Number: 1062},
		&mysql.MySQLError{Number: 1451},
		&mysql.MySQLError{Number: 1452},
		&mysql.MySQLError{Number: 1216},
		&mysql.MySQLError{Number: 1217},
	}
	for _, err := range errs {
		if IsUniqueViolation(err) && IsForeignKeyViolation(err) {
			t.Errorf("%v classifies as both a unique and a foreign-key violation", err)
		}
	}
}
