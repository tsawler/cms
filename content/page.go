// Package content stores the CMS's pages and their editable content
// (blocks) in Postgres. Pages carry per-locale metadata; blocks carry the
// text and HTML that fill the editable regions declared in the host
// application's templates.
package content

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tsawler/cms/internal/pgutil"
	"golang.org/x/text/unicode/norm"
)

// Status is a page's or block set's publication state.
type Status string

const (
	StatusDraft     Status = "draft"
	StatusPublished Status = "published"
)

// Page is a site page. Title and Description are the metadata for the
// locale the page was loaded with.
type Page struct {
	ID           int64
	Slug         string // "" is the homepage; otherwise e.g. "about" or "about/team"
	TemplateName string // template file within the host's TemplateFS
	Status       Status
	HeadCSS      string // extra per-page CSS, injected by cmsHead
	BodyJS       string // extra per-page JS, injected by cmsScripts
	Title        string
	Description  string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

var (
	// ErrNotFound is returned when no page matches the query.
	ErrNotFound = errors.New("content: page not found")
	// ErrDuplicateSlug is returned by Insert/Update when the slug is taken.
	ErrDuplicateSlug = errors.New("content: slug already in use")
)

var slugRe = regexp.MustCompile(`^[a-z0-9-]+(?:/[a-z0-9-]+)*$`)

var slugCleanRe = regexp.MustCompile(`[^a-z0-9]+`)

// Slugify derives a URL slug from a human title: lowercased, diacritics
// stripped, everything else hyphenated — "Café & Bar!" becomes "cafe-bar".
// Returns "" when nothing usable remains.
func Slugify(title string) string {
	var b strings.Builder
	for _, r := range norm.NFD.String(strings.ToLower(title)) {
		if unicode.Is(unicode.Mn, r) {
			continue // combining marks left over from decomposed accents
		}
		b.WriteRune(r)
	}
	out := strings.Trim(slugCleanRe.ReplaceAllString(b.String(), "-"), "-")
	if len(out) > 80 {
		out = strings.Trim(out[:80], "-")
	}
	return out
}

// NormalizeSlug lowercases s and trims whitespace and surrounding slashes,
// so "/About-Us/" becomes "about-us".
func NormalizeSlug(s string) string {
	return strings.Trim(strings.ToLower(strings.TrimSpace(s)), "/")
}

// ValidSlug reports whether s is empty (the homepage) or made of
// slash-separated segments of lowercase letters, digits, and hyphens.
func ValidSlug(s string) bool {
	return s == "" || slugRe.MatchString(s)
}

// Store reads and writes pages and blocks in Postgres.
type Store struct {
	db *pgxpool.Pool
}

// NewStore returns a Store backed by db.
func NewStore(db *pgxpool.Pool) *Store {
	return &Store{db: db}
}

const pageColumns = `p.id, p.slug, p.template_name, p.status, p.head_css, p.body_js,
	COALESCE(m.title, ''), COALESCE(m.description, ''), p.created_at, p.updated_at`

func scanPage(row pgx.Row) (*Page, error) {
	var p Page
	err := row.Scan(&p.ID, &p.Slug, &p.TemplateName, &p.Status, &p.HeadCSS, &p.BodyJS,
		&p.Title, &p.Description, &p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// GetByID returns the page with the given id, with metadata for locale.
func (s *Store) GetByID(ctx context.Context, id int64, locale string) (*Page, error) {
	row := s.db.QueryRow(ctx, `
		SELECT `+pageColumns+`
		FROM cms_pages p
		LEFT JOIN cms_page_meta m ON m.page_id = p.id AND m.locale = $2
		WHERE p.id = $1`, id, locale)
	return scanPage(row)
}

// GetBySlug returns the page with the given slug, with metadata for locale.
// With publishedOnly, draft pages are treated as not found.
func (s *Store) GetBySlug(ctx context.Context, slug, locale string, publishedOnly bool) (*Page, error) {
	q := `
		SELECT ` + pageColumns + `
		FROM cms_pages p
		LEFT JOIN cms_page_meta m ON m.page_id = p.id AND m.locale = $2
		WHERE p.slug = $1`
	if publishedOnly {
		q += ` AND p.status = 'published'`
	}
	return scanPage(s.db.QueryRow(ctx, q, slug, locale))
}

// All returns every page with metadata for locale, ordered by slug.
func (s *Store) All(ctx context.Context, locale string) ([]Page, error) {
	rows, err := s.db.Query(ctx, `
		SELECT `+pageColumns+`
		FROM cms_pages p
		LEFT JOIN cms_page_meta m ON m.page_id = p.id AND m.locale = $1
		ORDER BY p.slug`, locale)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Page, error) {
		p, err := scanPage(row)
		if err != nil {
			return Page{}, err
		}
		return *p, nil
	})
}

// Insert stores a new page and its metadata for locale, returning its id.
// New pages always start as drafts.
func (s *Store) Insert(ctx context.Context, p *Page, locale string) (int64, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	err = tx.QueryRow(ctx, `
		INSERT INTO cms_pages (slug, template_name, status, head_css, body_js)
		VALUES ($1, $2, 'draft', $3, $4)
		RETURNING id`,
		p.Slug, p.TemplateName, p.HeadCSS, p.BodyJS,
	).Scan(&p.ID)
	if pgutil.IsUniqueViolation(err) {
		return 0, ErrDuplicateSlug
	}
	if err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO cms_page_meta (page_id, locale, title, description)
		VALUES ($1, $2, $3, $4)`,
		p.ID, locale, p.Title, p.Description); err != nil {
		return 0, err
	}
	return p.ID, tx.Commit(ctx)
}

// Update saves a page's fields and its metadata for locale. It does not
// change publication status; use Publish and Unpublish for that.
func (s *Store) Update(ctx context.Context, p *Page, locale string) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tag, err := tx.Exec(ctx, `
		UPDATE cms_pages
		SET slug = $1, template_name = $2, head_css = $3, body_js = $4, updated_at = now()
		WHERE id = $5`,
		p.Slug, p.TemplateName, p.HeadCSS, p.BodyJS, p.ID)
	if pgutil.IsUniqueViolation(err) {
		return ErrDuplicateSlug
	}
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO cms_page_meta (page_id, locale, title, description)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (page_id, locale)
		DO UPDATE SET title = EXCLUDED.title, description = EXCLUDED.description`,
		p.ID, locale, p.Title, p.Description); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Delete removes a page and (via cascade) its metadata and blocks.
func (s *Store) Delete(ctx context.Context, id int64) error {
	tag, err := s.db.Exec(ctx, "DELETE FROM cms_pages WHERE id = $1", id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Publish makes the page's draft content live: the published block set is
// replaced by a copy of the draft set and the page is marked published.
func (s *Store) Publish(ctx context.Context, pageID int64) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx,
		"DELETE FROM cms_blocks WHERE page_id = $1 AND status = 'published'", pageID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO cms_blocks (page_id, region, locale, status, sort, kind, snippet_key, content, settings)
		SELECT page_id, region, locale, 'published', sort, kind, snippet_key, content, settings
		FROM cms_blocks WHERE page_id = $1 AND status = 'draft'`, pageID); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx,
		"UPDATE cms_pages SET status = 'published', updated_at = now() WHERE id = $1", pageID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return tx.Commit(ctx)
}

// DiscardDraft throws away a page's unpublished edits: the draft block set
// is replaced by a copy of the currently published set, so the editor
// returns to exactly what is live. The page's publication status is left
// unchanged. It is the inverse of Publish.
func (s *Store) DiscardDraft(ctx context.Context, pageID int64) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx,
		"DELETE FROM cms_blocks WHERE page_id = $1 AND status = 'draft'", pageID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO cms_blocks (page_id, region, locale, status, sort, kind, snippet_key, content, settings)
		SELECT page_id, region, locale, 'draft', sort, kind, snippet_key, content, settings
		FROM cms_blocks WHERE page_id = $1 AND status = 'published'`, pageID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Unpublish takes a page off the public site. Draft and published content
// are left as they are.
func (s *Store) Unpublish(ctx context.Context, pageID int64) error {
	tag, err := s.db.Exec(ctx,
		"UPDATE cms_pages SET status = 'draft', updated_at = now() WHERE id = $1", pageID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
