package dialect

import (
	"context"
	"fmt"
	"hash/fnv"
	"strings"
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

// SearchIndexWrite fills cms_search_docs.tsv: one weighted vector built
// from the three indexed fields, so a hit in the title outranks a hit in
// the body. The weights are Postgres's own A/B/D labels, whose numeric
// values ts_rank_cd supplies (1.0, 0.4, 0.1 by default) — D rather than C
// for the body so the gap between a title match and a passing mention in
// the prose is wide.
//
// The configuration arrives as a parameter cast to regconfig rather than
// being written into the SQL, which is what lets one statement index every
// locale.
func (Postgres) SearchIndexWrite(cfg, title, summary, body string) (string, string) {
	return "tsv", "setweight(to_tsvector(" + cfg + "::regconfig, " + title + "), 'A') || " +
		"setweight(to_tsvector(" + cfg + "::regconfig, " + summary + "), 'B') || " +
		"setweight(to_tsvector(" + cfg + "::regconfig, " + body + "), 'D')"
}

// SearchMatch matches against the stored vector and scores with
// ts_rank_cd, which — unlike ts_rank — accounts for how close the matched
// words are to each other. On a page that mentions two search words in the
// same sentence rather than ten paragraphs apart, that is the difference
// between the right result and a plausible one.
func (Postgres) SearchMatch(cfg, q string) (string, string) {
	query := "websearch_to_tsquery(" + cfg + "::regconfig, " + q + ")"
	return "tsv @@ " + query, "ts_rank_cd(tsv, " + query + ")"
}

// SearchQuery renders terms in websearch_to_tsquery's syntax — the one
// Postgres provides precisely so that a string from a search box cannot
// raise a syntax error. Bare words are AND-ed, quotes make a phrase, and a
// leading "-" excludes.
func (Postgres) SearchQuery(terms []SearchTerm) string {
	var b strings.Builder
	for _, t := range terms {
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		if t.Exclude {
			b.WriteByte('-')
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

// SearchConfig maps a locale to the text search configuration that stems
// it. Postgres ships a dictionary for a couple of dozen languages under
// their English names; a locale with none — or none installed — falls back
// to "simple", which does no stemming and no stop words. That is a worse
// search, not a broken one: exact words still match.
//
// Only the language part of the tag is consulted, so "pt-br" and "pt" both
// reach Portuguese.
func (Postgres) SearchConfig(locale string) string {
	lang, _, _ := strings.Cut(strings.ToLower(locale), "-")
	if cfg, ok := searchConfigs[lang]; ok {
		return cfg
	}
	return "simple"
}

// searchConfigs holds the configurations in a stock Postgres install. It
// is deliberately not exhaustive of the world's languages — it is
// exhaustive of what the server can be relied on to have, and anything
// missing lands on "simple" above.
var searchConfigs = map[string]string{
	"ar": "arabic", "ca": "catalan", "da": "danish", "de": "german",
	"el": "greek", "en": "english", "es": "spanish", "eu": "basque",
	"fi": "finnish", "fr": "french", "ga": "irish", "hi": "hindi",
	"hu": "hungarian", "hy": "armenian", "id": "indonesian", "it": "italian",
	"lt": "lithuanian", "ne": "nepali", "nl": "dutch", "no": "norwegian",
	"pt": "portuguese", "ro": "romanian", "ru": "russian", "sr": "serbian",
	"sv": "swedish", "ta": "tamil", "tr": "turkish", "yi": "yiddish",
}

// SearchMinWordLen is 1: Postgres indexes every word it is given. Stop
// words are dropped by the language's dictionary, but that is a decision
// about "the" and "and", not about length.
func (Postgres) SearchMinWordLen() int { return 1 }
