// Package content stores the CMS's pages and their editable content
// (blocks) in Postgres. Pages carry per-locale metadata; blocks carry the
// text and HTML that fill the editable regions declared in the host
// application's templates.
package content

import (
	"context"
	"errors"
	"regexp"
	"strconv"
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

// Visibility is who may view a page on the public site, independent of its
// publication status: a private page goes through the same draft/publish
// workflow but is only served to logged-in users once published.
type Visibility string

const (
	VisibilityPublic  Visibility = "public"
	VisibilityPrivate Visibility = "private"
)

// ValidVisibility reports whether s names a known visibility.
func ValidVisibility(s string) bool {
	return s == string(VisibilityPublic) || s == string(VisibilityPrivate)
}

// orPublic maps a zero Visibility to the public default, so callers that
// never set the field write a valid value.
func (v Visibility) orPublic() Visibility {
	if v == "" {
		return VisibilityPublic
	}
	return v
}

// Page is a site page. Title and Description are the metadata for the
// locale the page was loaded with.
type Page struct {
	ID           int64
	Slug         string // "" is the homepage; otherwise e.g. "about" or "about/team"
	TemplateName string // template file within the host's TemplateFS
	Status       Status
	Visibility   Visibility
	HeadCSS      string // extra per-page CSS, injected by cmsHead
	BodyJS       string // extra per-page JS, injected by cmsScripts
	CSSLinks     string // external stylesheet URLs, one per line
	JSLinks      string // external script URLs, one per line
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

// Store reads and writes pages and blocks in Postgres. Reads for a
// non-default locale fall back to the default locale's values where the
// requested locale has none.
type Store struct {
	db            *pgxpool.Pool
	defaultLocale string
}

// NewStore returns a Store backed by db. defaultLocale is the fallback
// for per-locale reads (page metadata, blocks); pass the site's first
// configured locale. Empty defaults to "en".
func NewStore(db *pgxpool.Pool, defaultLocale string) *Store {
	if defaultLocale == "" {
		defaultLocale = "en"
	}
	return &Store{db: db, defaultLocale: defaultLocale}
}

// pageColumns reads metadata from the requested-locale join (m) with
// field-level fallback to the default-locale join (md): an absent row or
// an empty field falls back, so a French page with only a title still
// gets the English description.
const pageColumns = `p.id, p.slug, p.template_name, p.status, p.visibility, p.head_css, p.body_js,
	p.css_links, p.js_links,
	COALESCE(NULLIF(m.title, ''), md.title, ''),
	COALESCE(NULLIF(m.description, ''), md.description, ''),
	p.created_at, p.updated_at`

func scanPage(row pgx.Row) (*Page, error) {
	var p Page
	err := row.Scan(&p.ID, &p.Slug, &p.TemplateName, &p.Status, &p.Visibility, &p.HeadCSS, &p.BodyJS,
		&p.CSSLinks, &p.JSLinks,
		&p.Title, &p.Description, &p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// pageMetaJoins joins page metadata twice: the requested locale ($n) and
// the default locale ($n+1), for pageColumns' fallback COALESCEs.
func pageMetaJoins(localeArg, defaultArg int) string {
	return `
		LEFT JOIN cms_page_meta m ON m.page_id = p.id AND m.locale = $` + strconv.Itoa(localeArg) + `
		LEFT JOIN cms_page_meta md ON md.page_id = p.id AND md.locale = $` + strconv.Itoa(defaultArg)
}

// GetByID returns the page with the given id, with metadata for locale.
func (s *Store) GetByID(ctx context.Context, id int64, locale string) (*Page, error) {
	row := s.db.QueryRow(ctx, `
		SELECT `+pageColumns+`
		FROM cms_pages p`+pageMetaJoins(2, 3)+`
		WHERE p.id = $1`, id, locale, s.defaultLocale)
	return scanPage(row)
}

// GetBySlug returns the page with the given slug, with metadata for locale.
// With publishedOnly, draft pages are treated as not found.
func (s *Store) GetBySlug(ctx context.Context, slug, locale string, publishedOnly bool) (*Page, error) {
	q := `
		SELECT ` + pageColumns + `
		FROM cms_pages p` + pageMetaJoins(2, 3) + `
		WHERE p.slug = $1`
	if publishedOnly {
		q += ` AND p.status = 'published'`
	}
	return scanPage(s.db.QueryRow(ctx, q, slug, locale, s.defaultLocale))
}

// All returns every page with metadata for locale, ordered by slug.
func (s *Store) All(ctx context.Context, locale string) ([]Page, error) {
	rows, err := s.db.Query(ctx, `
		SELECT `+pageColumns+`
		FROM cms_pages p`+pageMetaJoins(1, 2)+`
		ORDER BY p.slug`, locale, s.defaultLocale)
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

// Counts returns how many non-post pages and how many posts exist — the
// numbers the admin shows beside its Pages and Blog & News nav entries.
func (s *Store) Counts(ctx context.Context) (pages, posts int, err error) {
	err = s.db.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM cms_pages p
			 WHERE NOT EXISTS (SELECT 1 FROM cms_posts po WHERE po.page_id = p.id)),
			(SELECT count(*) FROM cms_posts)`).Scan(&pages, &posts)
	return pages, posts, err
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
		INSERT INTO cms_pages (slug, template_name, status, visibility, head_css, body_js, css_links, js_links)
		VALUES ($1, $2, 'draft', $3, $4, $5, $6, $7)
		RETURNING id`,
		p.Slug, p.TemplateName, p.Visibility.orPublic(), p.HeadCSS, p.BodyJS, p.CSSLinks, p.JSLinks,
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

// Duplicate copies the page srcID under a new slug: the page row itself
// (template, per-page CSS/JS), its metadata for every locale, and its
// draft blocks. title becomes the copy's title for locale; other locales
// keep the source's titles. The copy always starts as a draft, so the
// source's published blocks are not copied — the copy's first Publish
// snapshots the duplicated draft. Returns the new page's id,
// ErrDuplicateSlug when slug is taken, or ErrNotFound when the source
// page doesn't exist.
func (s *Store) Duplicate(ctx context.Context, srcID int64, slug, title, locale string) (int64, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var id int64
	err = tx.QueryRow(ctx, `
		INSERT INTO cms_pages (slug, template_name, status, visibility, head_css, body_js, css_links, js_links)
		SELECT $2, template_name, 'draft', visibility, head_css, body_js, css_links, js_links
		FROM cms_pages WHERE id = $1
		RETURNING id`, srcID, slug).Scan(&id)
	if pgutil.IsUniqueViolation(err) {
		return 0, ErrDuplicateSlug
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO cms_page_meta (page_id, locale, title, description)
		SELECT $1, locale, title, description
		FROM cms_page_meta WHERE page_id = $2`, id, srcID); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO cms_page_meta (page_id, locale, title, description)
		VALUES ($1, $2, $3, '')
		ON CONFLICT (page_id, locale)
		DO UPDATE SET title = EXCLUDED.title`, id, locale, title); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO cms_blocks (page_id, region, locale, status, sort, kind, snippet_key, content, settings)
		SELECT $1, region, locale, 'draft', sort, kind, snippet_key, content, settings
		FROM cms_blocks WHERE page_id = $2 AND status = 'draft'`, id, srcID); err != nil {
		return 0, err
	}
	return id, tx.Commit(ctx)
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
		SET slug = $1, template_name = $2, visibility = $3, head_css = $4, body_js = $5,
			css_links = $6, js_links = $7, updated_at = now()
		WHERE id = $8`,
		p.Slug, p.TemplateName, p.Visibility.orPublic(), p.HeadCSS, p.BodyJS, p.CSSLinks, p.JSLinks, p.ID)
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

// UpdateMeta saves only a page's per-locale metadata (title and
// description) — how non-default-locale admin tabs save, since every
// other page field is locale-independent.
func (s *Store) UpdateMeta(ctx context.Context, pageID int64, locale, title, description string) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO cms_page_meta (page_id, locale, title, description)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (page_id, locale)
		DO UPDATE SET title = EXCLUDED.title, description = EXCLUDED.description`,
		pageID, locale, title, description)
	return err
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

// SetVisibility changes who may view the page on the public site. It does
// not touch publication status or content.
func (s *Store) SetVisibility(ctx context.Context, pageID int64, v Visibility) error {
	tag, err := s.db.Exec(ctx,
		"UPDATE cms_pages SET visibility = $2, updated_at = now() WHERE id = $1",
		pageID, v.orPublic())
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
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
