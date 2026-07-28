// Package dialect isolates the SQL differences between the database engines
// the CMS supports.
//
// Stores write Postgres-flavoured SQL — $1 placeholders, ON CONFLICT ... DO
// UPDATE, RETURNING — because it is the more expressive of the two dialects
// and reads the same as the schema. A Dialect translates that canonical form
// on its way to the driver, so nothing above this package needs to know which
// engine it is talking to.
package dialect

import "context"

// Execer is the subset of a database handle the dialect helpers need. Both
// *sql.DB and *sql.Tx satisfy it, so an insert works the same inside and
// outside a transaction.
type Execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (Result, error)
	QueryRowContext(ctx context.Context, query string, args ...any) Row
}

// Result is the subset of sql.Result the helpers use.
type Result interface {
	LastInsertId() (int64, error)
	RowsAffected() (int64, error)
}

// Row is the subset of *sql.Row the helpers use.
type Row interface {
	Scan(dest ...any) error
}

// Dialect translates canonical (Postgres-flavoured) SQL for one engine.
type Dialect interface {
	// Name identifies the engine, e.g. "postgres" or "mysql".
	Name() string

	// Rewrite translates a canonical statement and its arguments into the
	// form this engine accepts. It is applied to every statement on its way
	// to the driver, so it must be safe on SQL that needs no translation.
	Rewrite(query string, args []any) (string, []any)

	// InsertID runs an INSERT that has no RETURNING clause and reports the
	// generated primary key. Postgres appends RETURNING id and reads the
	// result; MySQL uses LastInsertId, which Postgres does not support.
	InsertID(ctx context.Context, ex Execer, query string, args ...any) (int64, error)

	// CaseInsensitiveLike renders a case-insensitive LIKE comparison of col
	// against an already-rendered placeholder.
	CaseInsensitiveLike(col, placeholder string) string

	// JSONText renders a JSON column as text, for the comparisons that treat
	// a JSON value as an opaque string.
	JSONText(col string) string

	// Quote escapes an identifier that collides with a reserved word —
	// cms_settings.key, which MySQL reserves. The two engines disagree on
	// the quoting character, so there is no shared spelling.
	Quote(ident string) string

	// Distinct renders a NULL-safe inequality between two columns —
	// Postgres's IS DISTINCT FROM, which MySQL and MariaDB lack.
	Distinct(a, b string) string

	// SplitStatements splits a migration file into individually executable
	// statements. Postgres can take a whole file at once; MySQL's driver
	// rejects multiple statements per Exec.
	SplitStatements(script string) []string

	// Lock takes an advisory lock serializing concurrent migration runs, and
	// returns the function that releases it.
	Lock(ctx context.Context, ex Execer, key string) (unlock func(), err error)

	// MigrationDir is the subdirectory of migrations/sql holding this
	// engine's schema.
	MigrationDir() string
}

// For returns the Dialect named by name, or nil when it is unknown.
func For(name string) Dialect {
	switch name {
	case "", "postgres":
		return Postgres{}
	case "mysql", "mariadb":
		return MySQL{}
	}
	return nil
}
