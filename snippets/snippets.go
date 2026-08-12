// Package snippets manages the pre-written HTML blocks editors can insert
// into rich regions from the in-place editor's palette. Snippets come from
// two places: the host application's config (per-customer components,
// versioned with the code) and the database (created by admins in the
// admin UI). Once inserted, a snippet is ordinary region content — edited
// in place, sanitized on save, and styled entirely by the host site's CSS.
package snippets

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/tsawler/cms/internal/sqldb"
)

// Snippet is one insertable block. ID is set only for database-stored
// snippets; config-registered ones have ID zero.
//
// A snippet with a non-nil Settings map is a *section preset*: instead of
// a block dropped into existing content, it is a starting point for a
// whole section — the editor offers it only in the "Add a section"
// chooser, and applies the settings (background, width, height, vertical
// alignment) to the new section along with the HTML. Keys and values are
// the section-settings vocabulary: "bg" and "width" name curated
// SectionStyles option keys, "height" is "50"/"75"/"100", "valign" is
// "center"/"bottom", "bgcolor" is #rrggbb, "bgimage" is a URL, and
// "bgposition" anchors that image as a pair of percentages across and
// down, e.g. "50% 20%" ("50% 50%" is centered, and is the default).
// Unknown keys or invalid values fall back to the defaults,
// same as the section settings dialog. Presets come from config or from
// the admin snippets UI (which offers the curated settings; the
// free-form bgcolor/bgimage/bgposition are config-only).
// Group names the category the editor's drawer files the snippet under:
// the drawer offers a category dropdown when any loaded snippet carries
// one. Grouping is config-only (like render.EditorStyle.Group) — admin-
// created snippets and ungrouped config snippets appear under "Custom".
type Snippet struct {
	ID        int64
	Name      string
	Group     string
	HTML      string
	Settings  map[string]string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// ErrNotFound is returned when no snippet matches the query.
var ErrNotFound = errors.New("snippets: not found")

// videoSlotHTML is the shared "Click to add a video" placeholder used by
// the video snippet and section presets. While editing, the editor script
// turns a click on it into a chooser (media library or YouTube/Vimeo
// link) and replaces the slot with a <video> player or a bounded <iframe>
// embed — both shapes the editor sanitizer keeps.
const videoSlotHTML = `<div class="cms-video-slot not-prose flex aspect-video w-full items-center justify-center rounded-lg border-2 border-dashed border-slate-300 bg-slate-50" data-cms-video-slot=""><p class="font-semibold text-slate-500">&#127916; Click to add a video</p></div>`

// DefaultSnippets is the Tailwind-first default library, used when the
// host does not configure its own. The markup avoids elements the editor
// sanitizer strips (no SVG, no scripts) and uses `not-prose` so Tailwind
// Typography doesn't restyle component internals. Like the editor styles,
// every class here must be safelisted in the site's Tailwind build (see
// the README).
// The cms-snippet marker class on each snippet's root makes the block
// manageable in the in-place editor: a dotted outline while editing, and
// a floating drag-handle/trash chrome when clicked. Like cms-btn (which
// marks a link as an editable button), it carries no CSS of its own.
func DefaultSnippets() []Snippet {
	return []Snippet{
		// A plain paragraph: the workhorse block. Deliberately unstyled,
		// so it picks up the section's own prose styles.
		{Name: "Text", Group: "Basic", HTML: `<p class="cms-snippet">Write your text here.</p>`},
		{Name: "Callout", Group: "Basic", HTML: `<div class="cms-snippet not-prose my-6 rounded-lg border border-blue-200 bg-blue-50 p-4 text-blue-900">
<p class="font-semibold mb-1">Heads up</p>
<p>Something worth knowing goes here.</p>
</div>`},
		{Name: "Call to action", Group: "Basic", HTML: `<div class="cms-snippet not-prose my-6 rounded-xl bg-slate-900 p-6 text-center">
<p class="text-lg font-semibold text-white mb-3">Ready to get started?</p>
<a href="/" class="cms-btn inline-block rounded-lg bg-blue-600 px-5 py-2.5 font-semibold text-white">Get in touch</a>
</div>`},
		{Name: "Two columns", Group: "Basic", HTML: `<div class="cms-snippet not-prose my-6 grid gap-6 sm:grid-cols-2">
<div><h3 class="font-semibold mb-1">First column</h3><p class="text-slate-600">Write something here.</p></div>
<div><h3 class="font-semibold mb-1">Second column</h3><p class="text-slate-600">And something here.</p></div>
</div>`},
		{Name: "Quote", Group: "Quotes", HTML: `<figure class="cms-snippet not-prose my-6 rounded-xl border border-slate-200 bg-slate-50 p-6">
<blockquote class="text-lg text-slate-700">&ldquo;A quote worth repeating.&rdquo;</blockquote>
<figcaption class="mt-3 text-sm font-semibold text-slate-500">&mdash; Name, Title</figcaption>
</figure>`},
		{Name: "Button link", Group: "Buttons", HTML: `<p class="cms-snippet not-prose my-4">
<a href="/" class="cms-btn inline-block rounded-lg bg-blue-600 px-5 py-2.5 font-semibold text-white">Button text</a>
</p>`},
		// A movie, dropped inline: the slot offers the media library or a
		// YouTube/Vimeo link when clicked while editing.
		{Name: "Video", Group: "Media", HTML: `<div class="cms-snippet not-prose my-6">
` + videoSlotHTML + `
</div>`},
		// Invisible on the live site; in edit mode the editor script makes
		// it visible and click-to-adjust (see editor.js and the height
		// allowance in the sanitizer policy).
		{Name: "Flexible space", Group: "Basic", HTML: `<div class="cms-spacer" data-height="48px" style="height: 48px"></div>`},
	}
}

// DefaultSectionPresets is the Tailwind-first default library of section
// presets — snippets with Settings, offered as one-click starting points
// in the "Add a section" chooser. Two markup strategies, chosen per
// preset: markup that should adapt when the editor later changes the
// section background skips not-prose and leans on prose/prose-invert for
// its colors (Hero, CTA, FAQ); grid layouts that Typography would restyle
// use not-prose with explicit slate colors and suit light backgrounds
// (Feature grid, Stats, Testimonials). Like DefaultSnippets, every class
// must be safelisted in the site's Tailwind build (see the README).
func DefaultSectionPresets() []Snippet {
	return []Snippet{
		// A tall dark band, content centered both ways. The heading and
		// paragraph take their color from prose-invert, so the hero stays
		// readable if the background is later switched.
		{Name: "Hero", Group: "Headlines", Settings: map[string]string{"bg": "dark", "width": "wide", "height": "75", "valign": "center"},
			HTML: `<div class="cms-snippet text-center">
<h1 class="text-4xl sm:text-5xl font-bold tracking-tight">A headline that lands</h1>
<p class="text-xl">One sentence on why visitors should care.</p>
<p><a href="/" class="cms-btn inline-block rounded-lg bg-blue-600 px-6 py-3 font-semibold text-white">Get started</a></p>
</div>`},
		{Name: "Feature grid", Group: "Features", Settings: map[string]string{"width": "wide"},
			HTML: `<div class="cms-snippet not-prose my-8 grid gap-8 text-center sm:grid-cols-3">
<div><h3 class="text-lg font-semibold mb-2">Fast</h3><p class="text-slate-600">Explain the first thing you do well.</p></div>
<div><h3 class="text-lg font-semibold mb-2">Simple</h3><p class="text-slate-600">Explain the second thing you do well.</p></div>
<div><h3 class="text-lg font-semibold mb-2">Reliable</h3><p class="text-slate-600">Explain the third thing you do well.</p></div>
</div>`},
		{Name: "Stats", Group: "Stats", Settings: map[string]string{"bg": "light", "width": "wide"},
			HTML: `<div class="cms-snippet not-prose my-8 grid gap-8 text-center sm:grid-cols-3">
<div><p class="text-4xl font-bold">10k+</p><p class="text-slate-600 mt-1">Happy customers</p></div>
<div><p class="text-4xl font-bold">99.9%</p><p class="text-slate-600 mt-1">Uptime</p></div>
<div><p class="text-4xl font-bold">24/7</p><p class="text-slate-600 mt-1">Support</p></div>
</div>`},
		{Name: "Testimonials", Group: "Quotes", Settings: map[string]string{"width": "wide"},
			HTML: `<div class="cms-snippet not-prose my-8 grid gap-6 sm:grid-cols-2">
<figure class="rounded-xl border border-slate-200 bg-slate-50 p-6">
<blockquote class="text-slate-700">&ldquo;Exactly what we needed &mdash; it just works.&rdquo;</blockquote>
<figcaption class="mt-3 text-sm font-semibold text-slate-500">&mdash; A happy customer</figcaption>
</figure>
<figure class="rounded-xl border border-slate-200 bg-slate-50 p-6">
<blockquote class="text-slate-700">&ldquo;The best decision we made this year.&rdquo;</blockquote>
<figcaption class="mt-3 text-sm font-semibold text-slate-500">&mdash; Another happy customer</figcaption>
</figure>
</div>`},
		// Plain prose headings and paragraphs — Typography styles them,
		// so the FAQ adapts to any background. The width key is the
		// default anyway, but a preset needs at least one setting or the
		// API's omitempty would demote it to a plain snippet.
		{Name: "FAQ", Group: "Basic", Settings: map[string]string{"width": "normal"},
			HTML: `<div class="cms-snippet">
<h2>Frequently asked questions</h2>
<h3>The first question people ask?</h3>
<p>A short, direct answer.</p>
<h3>The second question people ask?</h3>
<p>Another short, direct answer.</p>
<h3>The third question people ask?</h3>
<p>One more short, direct answer.</p>
</div>`},
		// Movie sections: a full-width player, and both split layouts.
		// The slot picks up a library video or a YouTube/Vimeo embed when
		// clicked while editing (see videoSlotHTML).
		{Name: "Full-width video", Group: "Media", Settings: map[string]string{"width": "wide"},
			HTML: `<div class="cms-snippet not-prose my-8">
` + videoSlotHTML + `
</div>`},
		{Name: "Text + video", Group: "Media", Settings: map[string]string{"width": "wide"},
			HTML: `<div class="cms-snippet not-prose my-8 grid gap-8 items-center sm:grid-cols-2">
<div><h2 class="text-2xl font-bold mb-2">A headline for the video</h2><p class="text-slate-600">Set up what viewers will see and why it&rsquo;s worth watching.</p></div>
` + videoSlotHTML + `
</div>`},
		{Name: "Video + text", Group: "Media", Settings: map[string]string{"width": "wide"},
			HTML: `<div class="cms-snippet not-prose my-8 grid gap-8 items-center sm:grid-cols-2">
` + videoSlotHTML + `
<div><h2 class="text-2xl font-bold mb-2">A headline for the video</h2><p class="text-slate-600">Set up what viewers will see and why it&rsquo;s worth watching.</p></div>
</div>`},
		{Name: "Call-to-action banner", Group: "Basic", Settings: map[string]string{"bg": "accent", "valign": "center"},
			HTML: `<div class="cms-snippet text-center">
<h2 class="text-3xl font-bold">Ready when you are</h2>
<p>Tell visitors the one thing to do next.</p>
<p><a href="/" class="cms-btn inline-block rounded-lg bg-white px-6 py-3 font-semibold text-blue-700">Get in touch</a></p>
</div>`},
	}
}

// Store reads and writes admin-created snippets in Postgres.
type Store struct {
	db *sqldb.DB
}

// NewStore returns a Store backed by db.
func NewStore(db *sqldb.DB) *Store {
	return &Store{db: db}
}

const snippetColumns = "id, name, html, settings, created_at, updated_at"

func scanSnippet(row sqldb.Scanner) (*Snippet, error) {
	var s Snippet
	err := row.Scan(&s.ID, &s.Name, &s.HTML, sqldb.JSONInto(&s.Settings), &s.CreatedAt, &s.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
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
	return sqldb.CollectRows(rows, func(row sqldb.Scanner) (Snippet, error) {
		sn, err := scanSnippet(row)
		if err != nil {
			return Snippet{}, err
		}
		return *sn, nil
	})
}

// Count returns how many stored snippets exist — the admin adds the
// host-registered ones and shows the total beside its Snippets nav entry.
func (s *Store) Count(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRow(ctx, "SELECT count(*) FROM cms_snippets").Scan(&n)
	return n, err
}

// GetByID returns one stored snippet, or ErrNotFound.
func (s *Store) GetByID(ctx context.Context, id int64) (*Snippet, error) {
	return scanSnippet(s.db.QueryRow(ctx,
		"SELECT "+snippetColumns+" FROM cms_snippets WHERE id = $1", id))
}

// Insert stores a new snippet and returns its id. A nil Settings map is
// stored as NULL (plain block); a non-nil one makes it a section preset.
func (s *Store) Insert(ctx context.Context, sn *Snippet) (int64, error) {
	id, err := s.db.InsertID(ctx,
		"INSERT INTO cms_snippets (name, html, settings) VALUES ($1, $2, $3)",
		sn.Name, sn.HTML, sqldb.JSON(sn.Settings))
	if err != nil {
		return 0, err
	}
	sn.ID = id
	return sn.ID, nil
}

// Update saves a stored snippet's name, HTML, and settings.
func (s *Store) Update(ctx context.Context, sn *Snippet) error {
	tag, err := s.db.Exec(ctx,
		"UPDATE cms_snippets SET name = $1, html = $2, settings = $3, updated_at = now() WHERE id = $4",
		sn.Name, sn.HTML, sqldb.JSON(sn.Settings), sn.ID)
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
