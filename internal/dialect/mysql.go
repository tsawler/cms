package dialect

import (
	"context"
	"fmt"
	"strings"
	"time"
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

// BinaryText casts the column to binary, so it compares as its bytes
// rather than under a case- and accent-insensitive collation.
func (MySQL) BinaryText(col string) string { return "CAST(" + col + " AS BINARY)" }

// Distinct uses the NULL-safe equality operator negated, since neither
// engine has IS DISTINCT FROM — on the columns' bytes, as Postgres's
// IS DISTINCT FROM compares them, so a change of case or accent counts.
func (m MySQL) Distinct(a, b string) string {
	return "NOT (" + m.BinaryText(a) + " <=> " + m.BinaryText(b) + ")"
}

// SplitStatements breaks a migration into single statements, which the
// driver requires unless multiStatements is enabled — and that conflicts
// with prepared statements.
func (MySQL) SplitStatements(script string) []string { return splitStatements(script) }

// lockPoll is how long one GET_LOCK call waits before Lock asks again.
// A variable so tests need not wait it out.
var lockPoll = 5 * time.Second

// Lock takes a named advisory lock with GET_LOCK, waiting for as long as
// ctx allows — the way Postgres's pg_advisory_lock waits. A single
// GET_LOCK with a fixed timeout gave up after a minute, so an instance
// starting while another ran a long migration (a FULLTEXT index build on
// a big table, a media restore) failed to start at all. The wait is cut
// into lockPoll rounds because the server does not see ctx: a round is
// how long a cancelled ctx can go unnoticed.
func (MySQL) Lock(ctx context.Context, ex Execer, key string) (func(), error) {
	for {
		var got *int64
		if err := ex.QueryRowContext(ctx, "SELECT GET_LOCK($1, $2)", key, max(1, int(lockPoll/time.Second))).Scan(&got); err != nil {
			return nil, fmt.Errorf("acquiring advisory lock: %w", err)
		}
		// GET_LOCK returns 1 on success, 0 on timeout, NULL on error.
		if got == nil {
			return nil, fmt.Errorf("acquiring advisory lock %q: GET_LOCK failed", key)
		}
		if *got == 1 {
			break
		}
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("waiting for advisory lock %q: %w", key, err)
		}
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
		// A hyphen or an apostrophe inside a word is an operator, or a
		// word break, to the boolean-mode parser: +well-known reads as
		// +well -known and excludes every page that says "known". As a
		// phrase the term matches its words in order, which is what the
		// index holds them as.
		if t.Phrase || strings.ContainsAny(t.Text, "-'’") {
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

// RequiredDSN is the query string every MySQL or MariaDB DSN the CMS uses
// must carry; see CheckSession for what each setting is for.
const RequiredDSN = "parseTime=true&loc=UTC&time_zone=%27%2B00%3A00%27&clientFoundRows=true"

// CheckSession reports the DSN settings the connection is missing. They
// live in the host's DSN, which the CMS never sees — database/sql keeps
// it inside the driver — so the only way to know is to ask the server how
// the connection behaves:
//
//   - parseTime: without it a DATETIME will not scan into time.Time.
//   - loc=UTC: the driver otherwise reads timestamps as local time.
//   - time_zone='+00:00': otherwise SQL now() is the server's local time
//     while Go writes UTC, so session expiry and publish dates drift by
//     the server's offset.
//   - clientFoundRows: otherwise an UPDATE reports rows changed rather
//     than rows matched, and the stores read "0 rows" as ErrNotFound — a
//     save that changes nothing fails.
//
// The clientFoundRows probe is a no-op UPDATE of one cms_schema_migrations
// row, so it needs the migrations to have run. It returns nil when every
// setting is in place, and an error naming each missing one otherwise.
func (MySQL) CheckSession(ctx context.Context, ex Execer) error {
	var missing []string

	// A winter and a summer date, so a zone that only sits at UTC for
	// part of the year (Europe/London) is caught whatever the season.
	var winter, summer time.Time
	err := ex.QueryRowContext(ctx, `SELECT CAST('2000-01-01 00:00:00' AS DATETIME),
		CAST('2000-07-01 00:00:00' AS DATETIME)`).Scan(&winter, &summer)
	switch {
	case err != nil && strings.Contains(err.Error(), "unsupported Scan"):
		missing = append(missing, "parseTime=true (timestamps do not scan into time.Time)")
	case err != nil:
		return fmt.Errorf("checking the MySQL connection: %w", err)
	case !isUTC(winter) || !isUTC(summer):
		missing = append(missing, "loc=UTC (the driver reads timestamps as "+winter.Location().String()+")")
	}

	var offset int64
	if err := ex.QueryRowContext(ctx,
		"SELECT TIMESTAMPDIFF(SECOND, UTC_TIMESTAMP(), NOW())").Scan(&offset); err != nil {
		return fmt.Errorf("checking the MySQL connection: %w", err)
	}
	if offset != 0 {
		missing = append(missing, fmt.Sprintf(
			"time_zone='+00:00' (the session's now() is %+d seconds from UTC)", offset))
	}

	res, err := ex.ExecContext(ctx,
		"UPDATE cms_schema_migrations SET version = version ORDER BY version LIMIT 1")
	if err != nil {
		return fmt.Errorf("checking the MySQL connection: %w", err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return fmt.Errorf("checking the MySQL connection: %w", err)
	} else if n == 0 {
		missing = append(missing, "clientFoundRows=true (an UPDATE that changes nothing reports no rows, which reads as not found)")
	}

	if len(missing) > 0 {
		return fmt.Errorf("the MySQL DSN is missing settings the CMS depends on: %s; add ?%s to it",
			strings.Join(missing, "; "), RequiredDSN)
	}
	return nil
}

// isUTC reports whether t is at UTC's offset, whatever its zone is called.
func isUTC(t time.Time) bool {
	_, offset := t.Zone()
	return offset == 0
}
