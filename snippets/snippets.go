// Package snippets manages the pre-written HTML blocks editors can insert
// into rich regions from the in-place editor's palette. Snippets come from
// two places: the host application's config (per-customer components,
// versioned with the code) and the database (created by admins in the
// admin UI). Once inserted, a snippet is ordinary region content — edited
// in place, sanitized on save, and styled entirely by the host site's CSS.
package snippets

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Snippet is one insertable block. ID is set only for database-stored
// snippets; config-registered ones have ID zero.
type Snippet struct {
	ID        int64
	Name      string
	HTML      string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// ErrNotFound is returned when no snippet matches the query.
var ErrNotFound = errors.New("snippets: not found")

// DefaultSnippets is the Tailwind-first default library, used when the
// host does not configure its own. The markup avoids elements the editor
// sanitizer strips (no SVG, no scripts) and uses `not-prose` so Tailwind
// Typography doesn't restyle component internals. Like the editor styles,
// every class here must be safelisted in the site's Tailwind build (see
// the README).
func DefaultSnippets() []Snippet {
	return []Snippet{
		{Name: "Callout", HTML: `<div class="not-prose my-6 rounded-lg border border-blue-200 bg-blue-50 p-4 text-blue-900">
<p class="font-semibold mb-1">Heads up</p>
<p>Something worth knowing goes here.</p>
</div>`},
		{Name: "Call to action", HTML: `<div class="not-prose my-6 rounded-xl bg-slate-900 p-6 text-center">
<p class="text-lg font-semibold text-white mb-3">Ready to get started?</p>
<a href="#" class="cms-btn inline-block rounded-lg bg-blue-600 px-5 py-2.5 font-semibold text-white">Get in touch</a>
</div>`},
		{Name: "Two columns", HTML: `<div class="not-prose my-6 grid gap-6 sm:grid-cols-2">
<div><h3 class="font-semibold mb-1">First column</h3><p class="text-slate-600">Write something here.</p></div>
<div><h3 class="font-semibold mb-1">Second column</h3><p class="text-slate-600">And something here.</p></div>
</div>`},
		{Name: "Quote", HTML: `<figure class="not-prose my-6 rounded-xl border border-slate-200 bg-slate-50 p-6">
<blockquote class="text-lg text-slate-700">&ldquo;A quote worth repeating.&rdquo;</blockquote>
<figcaption class="mt-3 text-sm font-semibold text-slate-500">&mdash; Name, Title</figcaption>
</figure>`},
		// The cms-btn marker class makes the link a "button" to the
		// in-place editor: clicking it while editing shows gear/trash
		// controls (colors, roundness, size, delete). It has no CSS of
		// its own.
		{Name: "Button link", HTML: `<p class="not-prose my-4">
<a href="#" class="cms-btn inline-block rounded-lg bg-blue-600 px-5 py-2.5 font-semibold text-white">Button text</a>
</p>`},
		// Invisible on the live site; in edit mode the editor script makes
		// it visible and click-to-adjust (see editor.js and the height
		// allowance in the sanitizer policy).
		{Name: "Flexible space", HTML: `<div class="cms-spacer" data-height="48px" style="height:48px"></div>`},
	}
}

// Store reads and writes admin-created snippets in Postgres.
type Store struct {
	db *pgxpool.Pool
}

// NewStore returns a Store backed by db.
func NewStore(db *pgxpool.Pool) *Store {
	return &Store{db: db}
}

const snippetColumns = "id, name, html, created_at, updated_at"

func scanSnippet(row pgx.Row) (*Snippet, error) {
	var s Snippet
	err := row.Scan(&s.ID, &s.Name, &s.HTML, &s.CreatedAt, &s.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// All returns every stored snippet, ordered by name.
func (s *Store) All(ctx context.Context) ([]Snippet, error) {
	rows, err := s.db.Query(ctx, "SELECT "+snippetColumns+" FROM cms_snippets ORDER BY name")
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Snippet, error) {
		sn, err := scanSnippet(row)
		if err != nil {
			return Snippet{}, err
		}
		return *sn, nil
	})
}

// GetByID returns one stored snippet, or ErrNotFound.
func (s *Store) GetByID(ctx context.Context, id int64) (*Snippet, error) {
	return scanSnippet(s.db.QueryRow(ctx,
		"SELECT "+snippetColumns+" FROM cms_snippets WHERE id = $1", id))
}

// Insert stores a new snippet and returns its id.
func (s *Store) Insert(ctx context.Context, sn *Snippet) (int64, error) {
	err := s.db.QueryRow(ctx,
		"INSERT INTO cms_snippets (name, html) VALUES ($1, $2) RETURNING id",
		sn.Name, sn.HTML).Scan(&sn.ID)
	return sn.ID, err
}

// Update saves a stored snippet's name and HTML.
func (s *Store) Update(ctx context.Context, sn *Snippet) error {
	tag, err := s.db.Exec(ctx,
		"UPDATE cms_snippets SET name = $1, html = $2, updated_at = now() WHERE id = $3",
		sn.Name, sn.HTML, sn.ID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Delete removes a stored snippet. Content already inserted into pages is
// unaffected — inserted snippets are plain region HTML.
func (s *Store) Delete(ctx context.Context, id int64) error {
	tag, err := s.db.Exec(ctx, "DELETE FROM cms_snippets WHERE id = $1", id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
