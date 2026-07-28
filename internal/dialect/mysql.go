package dialect

import (
	"context"
	"fmt"
)

// MySQL is the dialect for MySQL 8.0.31+ and MariaDB 10.6+.
//
// The 8.0.31 floor comes from EXCEPT, which the change-detection query in
// content/block.go uses and which MySQL only gained in that release.
// MariaDB has had it since 10.3.
type MySQL struct{}

func (MySQL) Name() string { return "mysql" }

// Rewrite translates placeholders and upserts; see rewrite.
func (MySQL) Rewrite(query string, args []any) (string, []any) { return rewrite(query, args) }

// InsertID uses LastInsertId, since neither engine supports RETURNING on
// every insert shape the CMS needs.
func (m MySQL) InsertID(ctx context.Context, ex Execer, query string, args ...any) (int64, error) {
	res, err := ex.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// CaseInsensitiveLike is a plain LIKE: the default collations on both
// engines compare case-insensitively.
func (MySQL) CaseInsensitiveLike(col, placeholder string) string {
	return col + " LIKE " + placeholder
}

// JSONText returns the column as-is. MySQL's JSON and MariaDB's LONGTEXT
// alias both compare as text without a cast.
func (MySQL) JSONText(col string) string { return col }

// Quote wraps an identifier in backticks. MySQL only accepts double quotes
// as identifier quoting under ANSI_QUOTES, which is not the default.
func (MySQL) Quote(ident string) string { return "`" + ident + "`" }

// Distinct uses the NULL-safe equality operator negated, since neither
// engine has IS DISTINCT FROM.
func (MySQL) Distinct(a, b string) string { return "NOT (" + a + " <=> " + b + ")" }

// SplitStatements breaks a migration into single statements, which the
// driver requires unless multiStatements is enabled — and that conflicts
// with prepared statements.
func (MySQL) SplitStatements(script string) []string { return splitStatements(script) }

// Lock takes a named advisory lock with GET_LOCK. The timeout is generous:
// it only has to outlast another instance applying the same migrations.
func (MySQL) Lock(ctx context.Context, ex Execer, key string) (func(), error) {
	var got *int64
	if err := ex.QueryRowContext(ctx, "SELECT GET_LOCK($1, $2)", key, 60).Scan(&got); err != nil {
		return nil, fmt.Errorf("acquiring advisory lock: %w", err)
	}
	// GET_LOCK returns 1 on success, 0 on timeout, NULL on error.
	if got == nil || *got != 1 {
		return nil, fmt.Errorf("timed out waiting for advisory lock %q", key)
	}
	return func() {
		_, _ = ex.ExecContext(context.WithoutCancel(ctx), "SELECT RELEASE_LOCK($1)", key)
	}, nil
}

func (MySQL) MigrationDir() string { return "mysql" }
