package dialect

import (
	"context"
	"fmt"
	"strings"
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

// SearchIndexWrite adds nothing to the insert: the FULLTEXT indexes on
// cms_search_docs read title, summary and body directly, so there is no
// derived column to keep in step with them.
func (MySQL) SearchIndexWrite(cfg, title, summary, body string) (string, string) {
	return "", ""
}

// SearchMatch scores a document twice: once across all three indexed
// fields, and once against the title alone, which is why the table carries
// a second FULLTEXT index over that column by itself. Adding the two is
// how the title outranks the body here — Postgres does the same job with
// setweight, but MATCH() can only name the columns some one index was
// built on, so the weighting has to happen in the query.
//
// The multiplier is chosen for the same reason Postgres's weights are:
// a page *about* the thing should beat a page that mentions it, by a
// margin that a longer body cannot close.
//
// Boolean mode rather than natural language mode, for two reasons. Natural
// language mode drops any word appearing in more than half the rows —
// which on a small site is most of its vocabulary, and produces the
// baffling result that the more a site writes about something the less
// findable it is. And boolean mode is the only one that can express the
// AND-by-default and exclusion that SearchQuery renders.
func (MySQL) SearchMatch(cfg, q string) (string, string) {
	all := "MATCH (title, summary, body) AGAINST (" + q + " IN BOOLEAN MODE)"
	title := "MATCH (title) AGAINST (" + q + " IN BOOLEAN MODE)"
	return all, all + " + 4 * " + title
}

// SearchQuery renders terms in boolean-mode syntax. Every included term
// gets a "+": without one, boolean mode treats a word as optional, so
// searching for two words would return the pages holding either. AND is
// what a search box means and what Postgres's websearch does.
//
// Nothing else reaches the engine. The terms arrive already stripped of
// boolean mode's operator characters (see content.ParseSearchQuery), so a
// visitor cannot type a "(" and get a syntax error, or a "*" and get a
// prefix search they did not ask for.
func (MySQL) SearchQuery(terms []SearchTerm) string {
	var b strings.Builder
	for _, t := range terms {
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		if t.Exclude {
			b.WriteByte('-')
		} else {
			b.WriteByte('+')
		}
		if t.Phrase {
			b.WriteByte('"')
			b.WriteString(t.Text)
			b.WriteByte('"')
			continue
		}
		b.WriteString(t.Text)
	}
	return b.String()
}

// SearchConfig returns "": neither engine has per-language text search
// configurations, so the parameter the query passes is unused.
func (MySQL) SearchConfig(locale string) string { return "" }

// SearchMinWordLen is 3, the innodb_ft_min_token_size default on both
// engines: shorter words are never put in the index in the first place.
// The variable is the server operator's and can be lowered, but a module
// that ships a schema cannot count on that having been done, and reading
// it per query to find out would cost more than the LIKE it saves.
func (MySQL) SearchMinWordLen() int { return 3 }
