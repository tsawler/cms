package dialect

import (
	"testing"
)

func TestRewritePlaceholders(t *testing.T) {
	cases := []struct {
		name     string
		query    string
		args     []any
		want     string
		wantArgs []any
	}{
		{
			name:     "sequential",
			query:    "SELECT * FROM t WHERE a = $1 AND b = $2",
			args:     []any{"a", "b"},
			want:     "SELECT * FROM t WHERE a = ? AND b = ?",
			wantArgs: []any{"a", "b"},
		},
		{
			// content/page.go GetByID renders $2 and $3 in the JOIN before
			// $1 in the WHERE. A rewriter that replaced left to right
			// without reordering would bind the wrong values here, and the
			// query would still run — silently returning wrong rows.
			name:     "out of order",
			query:    "SELECT 1 FROM p JOIN m ON m.locale = $2 AND m.d = $3 WHERE p.id = $1",
			args:     []any{"id", "locale", "default"},
			want:     "SELECT 1 FROM p JOIN m ON m.locale = ? AND m.d = ? WHERE p.id = ?",
			wantArgs: []any{"locale", "default", "id"},
		},
		{
			// content/post.go Posts uses $3 twice in one predicate.
			name:     "repeated placeholder",
			query:    "SELECT 1 FROM po WHERE ($3 = '' OR po.feed = $3)",
			args:     []any{"locale", "default", "blog"},
			want:     "SELECT 1 FROM po WHERE (? = '' OR po.feed = ?)",
			wantArgs: []any{"blog", "blog"},
		},
		{
			name:     "double digit",
			query:    "INSERT INTO t VALUES ($1, $10, $11)",
			args:     []any{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11},
			want:     "INSERT INTO t VALUES (?, ?, ?)",
			wantArgs: []any{1, 10, 11},
		},
		{
			name:     "no placeholders",
			query:    "SELECT count(*) FROM cms_users",
			args:     nil,
			want:     "SELECT count(*) FROM cms_users",
			wantArgs: []any{},
		},
		{
			name:     "dollar inside a string literal is left alone",
			query:    "SELECT '$1 costs $5' AS s WHERE a = $1",
			args:     []any{"x"},
			want:     "SELECT '$1 costs $5' AS s WHERE a = ?",
			wantArgs: []any{"x"},
		},
		{
			name:     "dollar inside a line comment is left alone",
			query:    "SELECT 1 -- not a $2 placeholder\nWHERE a = $1",
			args:     []any{"x"},
			want:     "SELECT 1 -- not a $2 placeholder\nWHERE a = ?",
			wantArgs: []any{"x"},
		},
		{
			name:     "dollar inside a block comment is left alone",
			query:    "SELECT 1 /* $2 $3 */ WHERE a = $1",
			args:     []any{"x"},
			want:     "SELECT 1 /* $2 $3 */ WHERE a = ?",
			wantArgs: []any{"x"},
		},
		{
			name:     "question mark inside a literal survives",
			query:    "SELECT 'why?' WHERE a = $1",
			args:     []any{"x"},
			want:     "SELECT 'why?' WHERE a = ?",
			wantArgs: []any{"x"},
		},
		{
			name:     "escaped quote inside a literal",
			query:    `SELECT 'it''s $9' WHERE a = $1`,
			args:     []any{"x"},
			want:     `SELECT 'it''s $9' WHERE a = ?`,
			wantArgs: []any{"x"},
		},
		{
			name:     "backslash-escaped quote inside a literal",
			query:    `SELECT 'a\' $9' WHERE a = $1`,
			args:     []any{"x"},
			want:     `SELECT 'a\' $9' WHERE a = ?`,
			wantArgs: []any{"x"},
		},
		{
			name:     "quoted identifier",
			query:    `SELECT "$weird" FROM t WHERE a = $1`,
			args:     []any{"x"},
			want:     `SELECT "$weird" FROM t WHERE a = ?`,
			wantArgs: []any{"x"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, gotArgs := rewrite(tc.query, tc.args)
			if got != tc.want {
				t.Errorf("query =\n  %q\nwant\n  %q", got, tc.want)
			}
			if len(gotArgs) != len(tc.wantArgs) {
				t.Fatalf("args = %v, want %v", gotArgs, tc.wantArgs)
			}
			for i := range tc.wantArgs {
				if gotArgs[i] != tc.wantArgs[i] {
					t.Errorf("arg %d = %v, want %v (full: %v)", i, gotArgs[i], tc.wantArgs[i], gotArgs)
				}
			}
		})
	}
}

func TestRewriteOutOfRangePlaceholderIsLeftIntact(t *testing.T) {
	// A $N with no matching argument is a bug in the CMS's SQL. It must not
	// be turned into a ? bound to NULL, which would run and quietly do the
	// wrong thing; leaving it makes the engine report the offending token.
	got, gotArgs := rewrite("SELECT 1 WHERE a = $1 AND b = $4", []any{"x"})
	want := "SELECT 1 WHERE a = ? AND b = $4"
	if got != want {
		t.Errorf("query = %q, want %q", got, want)
	}
	if len(gotArgs) != 1 {
		t.Errorf("args = %v, want just the one valid argument", gotArgs)
	}
}

func TestRewriteKeepsArgsForNativePlaceholders(t *testing.T) {
	// A statement already written with ? has no $N to rebuild the argument
	// list from. Returning the freshly built (empty) slice would drop the
	// arguments and the driver would report a syntax error at the ?s — which
	// is exactly how this surfaced, from the dialect's own GET_LOCK call.
	got, gotArgs := rewrite("SELECT GET_LOCK(?, ?)", []any{"cms_lock", 60})
	if got != "SELECT GET_LOCK(?, ?)" {
		t.Errorf("query = %q, want it unchanged", got)
	}
	if len(gotArgs) != 2 || gotArgs[0] != "cms_lock" || gotArgs[1] != 60 {
		t.Errorf("args = %v, want them passed through untouched", gotArgs)
	}
}

func TestRewriteUpserts(t *testing.T) {
	cases := []struct {
		name  string
		query string
		want  string
	}{
		{
			name:  "single column",
			query: "INSERT INTO t (a, b) VALUES ($1, $2) ON CONFLICT (a) DO UPDATE SET b = EXCLUDED.b",
			want:  "INSERT INTO t (a, b) VALUES (?, ?) ON DUPLICATE KEY UPDATE b = VALUES(b)",
		},
		{
			name:  "composite conflict target and several assignments",
			query: "INSERT INTO t VALUES ($1) ON CONFLICT (page_id, locale, status) DO UPDATE SET title = EXCLUDED.title, description = EXCLUDED.description",
			want:  "INSERT INTO t VALUES (?) ON DUPLICATE KEY UPDATE title = VALUES(title), description = VALUES(description)",
		},
		{
			name:  "assignment that is not from EXCLUDED is untouched",
			query: "INSERT INTO t VALUES ($1) ON CONFLICT (a) DO UPDATE SET b = EXCLUDED.b, updated_at = now()",
			want:  "INSERT INTO t VALUES (?) ON DUPLICATE KEY UPDATE b = VALUES(b), updated_at = now()",
		},
		{
			// Keywords are matched case-insensitively but always emitted in
			// upper case, so the output form does not depend on the input's.
			name:  "lower case keywords",
			query: "insert into t values ($1) on conflict (a) do update set b = excluded.b",
			want:  "insert into t values (?) ON DUPLICATE KEY UPDATE b = VALUES(b)",
		},
		{
			name:  "newlines between clauses",
			query: "INSERT INTO t VALUES ($1)\n\t\tON CONFLICT (a)\n\t\tDO UPDATE SET b = EXCLUDED.b",
			want:  "INSERT INTO t VALUES (?)\n\t\tON DUPLICATE KEY UPDATE b = VALUES(b)",
		},
		{
			name:  "no conflict target",
			query: "INSERT INTO t VALUES ($1) ON CONFLICT DO UPDATE SET b = EXCLUDED.b",
			want:  "INSERT INTO t VALUES (?) ON DUPLICATE KEY UPDATE b = VALUES(b)",
		},
		{
			// EXCLUDED must not match when it is only part of a longer name.
			name:  "similarly named identifier is untouched",
			query: "SELECT my_excluded.b, excludedness FROM t",
			want:  "SELECT my_excluded.b, excludedness FROM t",
		},
		{
			name:  "the word EXCLUDED inside a literal is untouched",
			query: "SELECT 'EXCLUDED.b' FROM t",
			want:  "SELECT 'EXCLUDED.b' FROM t",
		},
		{
			// DO NOTHING has no MySQL equivalent, so it must be left alone
			// to fail loudly rather than be mistranslated.
			name:  "DO NOTHING is deliberately not translated",
			query: "INSERT INTO t VALUES ($1) ON CONFLICT (a) DO NOTHING",
			want:  "INSERT INTO t VALUES (?) ON CONFLICT (a) DO NOTHING",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := rewrite(tc.query, []any{1, 2})
			if got != tc.want {
				t.Errorf("query =\n  %q\nwant\n  %q", got, tc.want)
			}
		})
	}
}

func TestSplitStatements(t *testing.T) {
	cases := []struct {
		name   string
		script string
		want   []string
	}{
		{
			name:   "single statement without a trailing semicolon",
			script: "CREATE TABLE t (id INT)",
			want:   []string{"CREATE TABLE t (id INT)"},
		},
		{
			name:   "several statements",
			script: "CREATE TABLE a (id INT);\nCREATE TABLE b (id INT);\n",
			want:   []string{"CREATE TABLE a (id INT)", "CREATE TABLE b (id INT)"},
		},
		{
			name:   "semicolon inside a string literal does not split",
			script: "INSERT INTO t VALUES ('a;b');\nSELECT 1;",
			want:   []string{"INSERT INTO t VALUES ('a;b')", "SELECT 1"},
		},
		{
			name:   "semicolon inside a comment does not split",
			script: "-- one; two\nSELECT 1;\nSELECT 2;",
			want:   []string{"-- one; two\nSELECT 1", "SELECT 2"},
		},
		{
			name:   "block comment with a semicolon",
			script: "/* a; b */ SELECT 1;",
			want:   []string{"/* a; b */ SELECT 1"},
		},
		{
			name:   "empty statements are dropped",
			script: ";;\nSELECT 1;;\n",
			want:   []string{"SELECT 1"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := splitStatements(tc.script)
			if len(got) != len(tc.want) {
				t.Fatalf("got %d statements %q, want %d %q", len(got), got, len(tc.want), tc.want)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Errorf("statement %d = %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

// TestRewriteRealStatements runs the upsert statements the CMS actually
// issues through the rewriter, so a change to any of them is caught here
// rather than against a live MySQL.
func TestRewriteRealStatements(t *testing.T) {
	cases := []struct{ name, query, want string }{
		{
			name: "sessionstore.Commit",
			query: `INSERT INTO cms_sessions (token, data, expiry) VALUES ($1, $2, $3)
		ON CONFLICT (token) DO UPDATE SET data = EXCLUDED.data, expiry = EXCLUDED.expiry`,
			want: `INSERT INTO cms_sessions (token, data, expiry) VALUES (?, ?, ?)
		ON DUPLICATE KEY UPDATE data = VALUES(data), expiry = VALUES(expiry)`,
		},
		{
			name: "content.UpsertDraftBlock",
			query: `INSERT INTO cms_blocks (page_id, region, locale, status, sort, kind, content)
		VALUES ($1, $2, $3, 'draft', 0, $4, $5)
		ON CONFLICT (page_id, region, locale, status, sort)
		DO UPDATE SET content = EXCLUDED.content, kind = EXCLUDED.kind, updated_at = now()`,
			want: `INSERT INTO cms_blocks (page_id, region, locale, status, sort, kind, content)
		VALUES (?, ?, ?, 'draft', 0, ?, ?)
		ON DUPLICATE KEY UPDATE content = VALUES(content), kind = VALUES(kind), updated_at = now()`,
		},
		{
			name: "content.SaveSiteSettings",
			query: `INSERT INTO cms_settings (key, value) VALUES ($1, $2)
				ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`,
			want: `INSERT INTO cms_settings (key, value) VALUES (?, ?)
				ON DUPLICATE KEY UPDATE value = VALUES(value)`,
		},
		{
			name: "cms.storeContentCSS",
			query: `INSERT INTO cms_content_css (singleton, class_hash, css)
		VALUES (TRUE, $1, $2)
		ON CONFLICT (singleton) DO UPDATE
		SET class_hash = EXCLUDED.class_hash, css = EXCLUDED.css, updated_at = now()`,
			want: `INSERT INTO cms_content_css (singleton, class_hash, css)
		VALUES (TRUE, ?, ?)
		ON DUPLICATE KEY UPDATE class_hash = VALUES(class_hash), css = VALUES(css), updated_at = now()`,
		},
		{
			name: "media.UpdateAlt",
			query: `INSERT INTO cms_media_meta (media_id, locale, alt_text)
		VALUES ($1, $2, $3)
		ON CONFLICT (media_id, locale) DO UPDATE SET alt_text = EXCLUDED.alt_text`,
			want: `INSERT INTO cms_media_meta (media_id, locale, alt_text)
		VALUES (?, ?, ?)
		ON DUPLICATE KEY UPDATE alt_text = VALUES(alt_text)`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := rewrite(tc.query, []any{1, 2, 3, 4, 5})
			if got != tc.want {
				t.Errorf("query =\n%s\nwant\n%s", got, tc.want)
			}
		})
	}
}
