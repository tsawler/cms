package snippets

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/tsawler/cms/internal/dberr"
	"github.com/tsawler/cms/internal/sqldb"
)

// CodeSnippet is one custom-code block: markup that may carry its own
// <script> (or <style>), stored under a key and referenced from page
// content rather than pasted into it.
//
// The split matters. Region and section content is sanitized on every
// non-admin save, so executable markup left inside it would either be
// stripped — silently deleting an admin's widget the first time an editor
// fixed a typo in the same section — or have to be safelisted, which
// would hand every editor a script-injection hole. What a page stores
// instead is an inert placeholder naming this key
// (<div class="cms-code" data-cms-code="key"></div>): safe to carry
// through the sanitizer untouched, meaningless on its own, and swapped
// for the HTML below on a public render.
type CodeSnippet struct {
	ID        int64
	Key       string // referenced by data-cms-code; see ValidCodeKey
	Name      string // what the editor shows a human
	HTML      string // markup, script tags and all
	CreatedAt time.Time
	UpdatedAt time.Time
}

// ErrDuplicateCodeKey is returned when a key is already taken.
var ErrDuplicateCodeKey = errors.New("snippets: code key already exists")

// codeKeyChars is the key vocabulary, kept deliberately narrow: a key
// appears in an HTML attribute the sanitizer must be able to bound with a
// regex of its own, so nothing here may need escaping. The two patterns
// below are both built from it, so neither can drift from the other.
const codeKeyChars = `[a-z0-9][a-z0-9-]{0,63}`

// codeKeyRe matches a key on its own.
var codeKeyRe = regexp.MustCompile(`^` + codeKeyChars + `$`)

// codeRefRe finds a key where a page names one: the data-cms-code
// attribute of a custom-code placeholder. Deliberately looser than the
// placeholder pattern the renderer expands, which insists on an empty
// body — this only has to find what a page refers to, and a reference
// with something in it is still a reference.
var codeRefRe = regexp.MustCompile(`(?i)\bdata-cms-code="(` + codeKeyChars + `)"`)

// ValidCodeKey reports whether key is a usable code-snippet key: a
// lowercase letter or digit followed by up to 63 more of those or
// hyphens.
func ValidCodeKey(key string) bool { return codeKeyRe.MatchString(key) }

// CodeKeyPattern is the same vocabulary as a pattern, for callers that
// need the regexp rather than the predicate — the editor's HTML
// sanitizer bounds the placeholder attribute with it, and having one
// definition is what keeps the two from drifting apart.
func CodeKeyPattern() *regexp.Regexp { return codeKeyRe }

// CodeKeysIn returns the code-snippet keys html refers to, deduplicated
// and in the order they first appear. Nothing to do with whether those
// keys exist: it reports what the markup asks for, which is the question
// a caller collecting a page's dependencies has.
func CodeKeysIn(html string) []string {
	if !strings.Contains(html, "data-cms-code=") {
		return nil
	}
	var keys []string
	seen := map[string]bool{}
	for _, m := range codeRefRe.FindAllStringSubmatch(html, -1) {
		key := strings.ToLower(m[1])
		if seen[key] {
			continue
		}
		seen[key] = true
		keys = append(keys, key)
	}
	return keys
}

// CodeKeyFor turns a human name into a key candidate: lowercased, runs of
// anything outside [a-z0-9] collapsed to a single hyphen, trimmed to the
// column's width. Returns "" when nothing usable survives, which callers
// treat as "ask for a key instead of guessing one".
func CodeKeyFor(name string) string {
	var sb strings.Builder
	dash := false
	for _, r := range strings.ToLower(name) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			sb.WriteRune(r)
			dash = false
		case !dash && sb.Len() > 0:
			sb.WriteByte('-')
			dash = true
		}
	}
	key := strings.Trim(sb.String(), "-")
	if len(key) > 64 {
		key = strings.Trim(key[:64], "-")
	}
	if !ValidCodeKey(key) {
		return ""
	}
	return key
}

// CodeStore reads and writes the custom-code library.
type CodeStore struct {
	db *sqldb.DB
}

// NewCodeStore returns a CodeStore backed by db.
func NewCodeStore(db *sqldb.DB) *CodeStore {
	return &CodeStore{db: db}
}

const codeColumns = "id, code_key, name, html, created_at, updated_at"

func scanCode(row sqldb.Scanner) (*CodeSnippet, error) {
	var c CodeSnippet
	err := row.Scan(&c.ID, &c.Key, &c.Name, &c.HTML, &c.CreatedAt, &c.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// All returns every code snippet, ordered by name.
func (s *CodeStore) All(ctx context.Context) ([]CodeSnippet, error) {
	rows, err := s.db.Query(ctx, "SELECT "+codeColumns+" FROM cms_code_snippets ORDER BY name")
	if err != nil {
		return nil, err
	}
	return sqldb.CollectRows(rows, func(row sqldb.Scanner) (CodeSnippet, error) {
		c, err := scanCode(row)
		if err != nil {
			return CodeSnippet{}, err
		}
		return *c, nil
	})
}

// ByKey returns one code snippet, or ErrNotFound.
func (s *CodeStore) ByKey(ctx context.Context, key string) (*CodeSnippet, error) {
	return scanCode(s.db.QueryRow(ctx,
		"SELECT "+codeColumns+" FROM cms_code_snippets WHERE code_key = $1", key))
}

// Insert stores a new code snippet and returns its id. A key already in
// use comes back as ErrDuplicateCodeKey.
func (s *CodeStore) Insert(ctx context.Context, c *CodeSnippet) (int64, error) {
	id, err := s.db.InsertID(ctx,
		"INSERT INTO cms_code_snippets (code_key, name, html) VALUES ($1, $2, $3)",
		c.Key, c.Name, c.HTML)
	if err != nil {
		if dberr.IsUniqueViolation(err) {
			return 0, ErrDuplicateCodeKey
		}
		return 0, err
	}
	c.ID = id
	return c.ID, nil
}

// Update saves a code snippet's name and HTML. The key is its identity
// and never changes: pages point at it.
func (s *CodeStore) Update(ctx context.Context, c *CodeSnippet) error {
	tag, err := s.db.Exec(ctx,
		"UPDATE cms_code_snippets SET name = $1, html = $2, updated_at = now() WHERE code_key = $3",
		c.Name, c.HTML, c.Key)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Delete removes a code snippet. Placeholders in pages that named it are
// left alone and render as nothing, so a delete is recoverable by
// creating the key again.
func (s *CodeStore) Delete(ctx context.Context, key string) error {
	tag, err := s.db.Exec(ctx, "DELETE FROM cms_code_snippets WHERE code_key = $1", key)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Lookup returns a func that resolves keys to markup for one render,
// remembering what it has already fetched: a page naming the same block
// twice costs one query, and a page naming none costs none. Errors
// resolve to "not found" — a failing widget must not take the page down —
// and are reported to onErr when one is given.
func (s *CodeStore) Lookup(ctx context.Context, onErr func(key string, err error)) func(string) (string, bool) {
	cache := make(map[string]string)
	return func(key string) (string, bool) {
		if html, ok := cache[key]; ok {
			return html, html != ""
		}
		c, err := s.ByKey(ctx, key)
		switch {
		case errors.Is(err, ErrNotFound):
			cache[key] = ""
			return "", false
		case err != nil:
			if onErr != nil {
				onErr(key, err)
			}
			cache[key] = ""
			return "", false
		}
		cache[key] = c.HTML
		return c.HTML, c.HTML != ""
	}
}
