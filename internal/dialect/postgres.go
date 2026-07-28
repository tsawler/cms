package dialect

import (
	"context"
	"fmt"
	"hash/fnv"
)

// Postgres is the canonical dialect: the SQL stores write is already
// Postgres SQL, so nearly every method here is the identity.
type Postgres struct{}

func (Postgres) Name() string { return "postgres" }

// Rewrite returns the statement untouched — canonical SQL is Postgres SQL.
func (Postgres) Rewrite(query string, args []any) (string, []any) { return query, args }

// InsertID appends RETURNING id and reads the generated key back, because
// the Postgres driver does not implement LastInsertId.
func (Postgres) InsertID(ctx context.Context, ex Execer, query string, args ...any) (int64, error) {
	var id int64
	err := ex.QueryRowContext(ctx, query+" RETURNING id", args...).Scan(&id)
	return id, err
}

// CaseInsensitiveLike uses ILIKE, which Postgres provides directly.
func (Postgres) CaseInsensitiveLike(col, placeholder string) string {
	return col + " ILIKE " + placeholder
}

// JSONText casts jsonb to text.
func (Postgres) JSONText(col string) string { return col + "::text" }

// Quote wraps an identifier in double quotes, the SQL standard spelling.
func (Postgres) Quote(ident string) string { return `"` + ident + `"` }

// Distinct uses IS DISTINCT FROM.
func (Postgres) Distinct(a, b string) string { return a + " IS DISTINCT FROM " + b }

// SplitStatements returns the script whole: Postgres executes a
// multi-statement string in one round trip, inside the caller's transaction.
func (Postgres) SplitStatements(script string) []string { return []string{script} }

// Lock takes a session-level advisory lock. The key is hashed to the int64
// pg_advisory_lock wants.
func (Postgres) Lock(ctx context.Context, ex Execer, key string) (func(), error) {
	id := advisoryKey(key)
	if _, err := ex.ExecContext(ctx, "SELECT pg_advisory_lock($1)", id); err != nil {
		return nil, fmt.Errorf("acquiring advisory lock: %w", err)
	}
	return func() {
		// Released on a background context so a cancelled migration still
		// gives the lock back.
		_, _ = ex.ExecContext(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock($1)", id)
	}, nil
}

func (Postgres) MigrationDir() string { return "postgres" }

// advisoryKey hashes a lock name into the int64 Postgres advisory locks use.
func advisoryKey(key string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(key))
	return int64(h.Sum64() >> 1) // >> 1 keeps it positive
}
