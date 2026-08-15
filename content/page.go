// Package content stores the CMS's pages and their editable content
// (blocks) in Postgres. Pages carry per-locale metadata; blocks carry the
// text and HTML that fill the editable regions declared in the host
// application's templates.
package content

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/tsawler/cms/internal/dberr"
	"github.com/tsawler/cms/internal/sqldb"
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
	HeadCSS      string // extra per-page CSS (or raw head markup), injected by cmsHead
	BodyJS       string // extra per-page JS (or raw markup), injected by cmsScripts
	Title        string
	Description  string
	// MetaDescription is what search engines are told the page is about,
	// when that should differ from Description. It exists for posts,
	// whose Description is the summary shown in listings and feeds — a
	// blurb written for readers browsing a list, which is not always the
	// line worth showing under a search result. Empty means "use
	// Description", so an ordinary page, whose Description is already its
	// meta description, never sets it. Read MetaTag rather than this
	// field to get the words a page actually publishes.
	MetaDescription string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// MetaTag is the description the page publishes to search engines: its
// own meta description, or the summary/description it falls back to.
func (p *Page) MetaTag() string {
	if p.MetaDescription != "" {
		return p.MetaDescription
	}
	return p.Description
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
	db            *sqldb.DB
	defaultLocale string
}

// NewStore returns a Store backed by db. defaultLocale is the fallback
// for per-locale reads (page metadata, blocks); pass the site's first
// configured locale. Empty defaults to "en".
func NewStore(db *sqldb.DB, defaultLocale string) *Store {
	if defaultLocale == "" {
		defaultLocale = "en"
	}
	return &Store{db: db, defaultLocale: defaultLocale}
}

// pageColumns reads metadata from the requested-locale join (m) with
// field-level fallback to the default-locale join (md): an absent row or
// an empty field falls back, so a French page with only a title still
// gets the English description.
//
// With draft, the fields that take part in the draft/publish workflow are
// read from the working copy — cms_page_drafts (d) for the page-level
// fields, and the draft-status metadata rows the joins select — instead of
// the published values on cms_pages. Slug and visibility are not staged,
// so they always read from cms_pages. The COALESCE back to cms_pages keeps
// a page whose draft row somehow went missing readable instead of failing
// the scan.
func pageColumns(draft bool) string {
	tmpl, css, js := "p.template_name", "p.head_css", "p.body_js"
	if draft {
		tmpl = "COALESCE(d.template_name, p.template_name)"
		css = "COALESCE(d.head_css, p.head_css)"
		js = "COALESCE(d.body_js, p.body_js)"
	}
	return `p.id, p.slug, ` + tmpl + `, p.status, p.visibility, ` + css + `, ` + js + `,
		COALESCE(NULLIF(m.title, ''), md.title, ''),
		COALESCE(NULLIF(m.description, ''), md.description, ''),
		COALESCE(NULLIF(m.meta_description, ''), md.meta_description, ''),
		p.created_at, p.updated_at`
}

func scanPage(row sqldb.Scanner) (*Page, error) {
	var p Page
	err := row.Scan(&p.ID, &p.Slug, &p.TemplateName, &p.Status, &p.Visibility, &p.HeadCSS, &p.BodyJS,
		&p.Title, &p.Description, &p.MetaDescription, &p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// pageMetaJoins joins page metadata twice: the requested locale ($n) and
// the default locale ($n+1), for pageColumns' fallback COALESCEs. Both
// joins take the draft or published side of the metadata, matching the
// pageColumns call they accompany; the draft side also brings in the
// page-level working copy the draft columns read from.
func pageMetaJoins(localeArg, defaultArg int, draft bool) string {
	status := "published"
	if draft {
		status = "draft"
	}
	j := `
		LEFT JOIN cms_page_meta m ON m.page_id = p.id AND m.locale = $` + strconv.Itoa(localeArg) + ` AND m.status = '` + status + `'
		LEFT JOIN cms_page_meta md ON md.page_id = p.id AND md.locale = $` + strconv.Itoa(defaultArg) + ` AND md.status = '` + status + `'`
	if draft {
		j += `
		LEFT JOIN cms_page_drafts d ON d.page_id = p.id`
	}
	return j
}

// GetByID returns the page with the given id, with metadata for locale.
// It reads the draft working copy: every caller is an admin screen.
func (s *Store) GetByID(ctx context.Context, id int64, locale string) (*Page, error) {
	row := s.db.QueryRow(ctx, `
		SELECT `+pageColumns(true)+`
		FROM cms_pages p`+pageMetaJoins(2, 3, true)+`
		WHERE p.id = $1`, id, locale, s.defaultLocale)
	return scanPage(row)
}

// GetBySlug returns the page with the given slug, with metadata for locale.
// With publishedOnly, draft pages are treated as not found and the page
// reads as the site serves it; without, it reads as the working copy, which
// is what the editor and preview want.
func (s *Store) GetBySlug(ctx context.Context, slug, locale string, publishedOnly bool) (*Page, error) {
	// The site page is excluded here rather than only at the router: it is
	// not a page, and nothing that resolves a URL should ever land on it.
	q := `
		SELECT ` + pageColumns(!publishedOnly) + `
		FROM cms_pages p` + pageMetaJoins(2, 3, !publishedOnly) + `
		WHERE p.slug = $1 AND NOT p.is_system`
	if publishedOnly {
		q += ` AND p.status = 'published'`
	}
	return scanPage(s.db.QueryRow(ctx, q, slug, locale, s.defaultLocale))
}

// All returns every page with metadata for locale, ordered by slug, as the
// working copy — it backs admin listings.
func (s *Store) All(ctx context.Context, locale string) ([]Page, error) {
	rows, err := s.db.Query(ctx, `
		SELECT `+pageColumns(true)+`
		FROM cms_pages p`+pageMetaJoins(1, 2, true)+`
		WHERE NOT p.is_system
		ORDER BY p.slug`, locale, s.defaultLocale)
	if err != nil {
		return nil, err
	}
	return sqldb.CollectRows(rows, func(row sqldb.Scanner) (Page, error) {
		p, err := scanPage(row)
		if err != nil {
			return Page{}, err
		}
		return *p, nil
	})
}

// SitemapEntry is one page as a sitemap sees it: where it lives and when
// it last changed. No metadata — a sitemap lists addresses, and the
// titles and descriptions belong to the pages themselves.
type SitemapEntry struct {
	Slug      string
	UpdatedAt time.Time
}

// SitemapPages returns every page a search engine may be pointed at:
// published, publicly visible, and not a system page. Posts come back
// alongside ordinary pages — a post is a page, so one pass covers both —
// ordered by slug.
//
// UpdatedAt is the page row's, which moves when a page is published,
// unpublished, renamed, or has its visibility changed, and stays put
// while a draft is edited (those writes land on cms_blocks). That makes
// it the date the live page last changed, which is what a sitemap's
// lastmod means.
//
// A non-positive limit returns everything; callers that serve the result
// in one document pass the protocol's ceiling.
func (s *Store) SitemapPages(ctx context.Context, limit int) ([]SitemapEntry, error) {
	q := `
		SELECT p.slug, p.updated_at
		FROM cms_pages p
		WHERE NOT p.is_system
		  AND p.status = 'published'
		  AND p.visibility = 'public'
		ORDER BY p.slug`
	var args []any
	if limit > 0 {
		q += ` LIMIT $1`
		args = append(args, limit)
	}
	rows, err := s.db.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	return sqldb.CollectRows(rows, func(row sqldb.Scanner) (SitemapEntry, error) {
		var e SitemapEntry
		err := row.Scan(&e.Slug, &e.UpdatedAt)
		return e, err
	})
}

// Counts returns how many non-post pages and how many posts exist — the
// numbers the admin shows beside its Pages and Blog & News nav entries.
func (s *Store) Counts(ctx context.Context) (pages, posts int, err error) {
	err = s.db.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM cms_pages p
			 WHERE NOT p.is_system
			   AND NOT EXISTS (SELECT 1 FROM cms_posts po WHERE po.page_id = p.id)),
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

	p.ID, err = tx.InsertID(ctx, `
		INSERT INTO cms_pages (slug, template_name, status, visibility, head_css, body_js)
		VALUES ($1, $2, 'draft', $3, $4, $5)`,
		p.Slug, p.TemplateName, p.Visibility.orPublic(), p.HeadCSS, p.BodyJS)
	if dberr.IsUniqueViolation(err) {
		return 0, ErrDuplicateSlug
	}
	if err != nil {
		return 0, err
	}
	// The working copy starts out matching the row just written; edits to
	// the staged fields land here and only reach cms_pages on Publish.
	if _, err := tx.Exec(ctx, `
		INSERT INTO cms_page_drafts (page_id, template_name, head_css, body_js)
		VALUES ($1, $2, $3, $4)`,
		p.ID, p.TemplateName, p.HeadCSS, p.BodyJS); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO cms_page_meta (page_id, locale, title, description, meta_description, status)
		VALUES ($1, $2, $3, $4, $5, 'draft')`,
		p.ID, locale, p.Title, p.Description, p.MetaDescription); err != nil {
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

	// Everything is copied from the source's working copy, so a duplicate
	// reproduces what the editor shows rather than what is live.
	//
	// Read first, then insert a plain VALUES row. An INSERT ... SELECT with
	// a RETURNING clause is Postgres-only — MySQL has no RETURNING at all
	// and MariaDB supports it only on the VALUES form — and splitting the
	// two also makes the copied fields explicit.
	var src Page
	err = tx.QueryRow(ctx, `
		SELECT COALESCE(d.template_name, p.template_name), p.visibility,
			COALESCE(d.head_css, p.head_css), COALESCE(d.body_js, p.body_js)
		FROM cms_pages p
		LEFT JOIN cms_page_drafts d ON d.page_id = p.id
		WHERE p.id = $1`, srcID,
	).Scan(&src.TemplateName, &src.Visibility, &src.HeadCSS, &src.BodyJS)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, err
	}

	id, err := tx.InsertID(ctx, `
		INSERT INTO cms_pages (slug, template_name, status, visibility, head_css, body_js)
		VALUES ($1, $2, 'draft', $3, $4, $5)`,
		slug, src.TemplateName, src.Visibility, src.HeadCSS, src.BodyJS)
	if dberr.IsUniqueViolation(err) {
		return 0, ErrDuplicateSlug
	}
	if err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO cms_page_drafts (page_id, template_name, head_css, body_js)
		SELECT $1, template_name, head_css, body_js
		FROM cms_pages WHERE id = $1`, id); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO cms_page_meta (page_id, locale, title, description, meta_description, status)
		SELECT $1, locale, title, description, meta_description, 'draft'
		FROM cms_page_meta WHERE page_id = $2 AND status = 'draft'`, id, srcID); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO cms_page_meta (page_id, locale, title, description, meta_description, status)
		VALUES ($1, $2, $3, '', '', 'draft')
		ON CONFLICT (page_id, locale, status)
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
//
// Title, description, template and per-page code are staged: they land in
// the working copy and reach the site on the next Publish. Slug and
// visibility are not staged and take effect immediately.
func (s *Store) Update(ctx context.Context, p *Page, locale string) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tag, err := tx.Exec(ctx, `
		UPDATE cms_pages
		SET slug = $1, visibility = $2, updated_at = now()
		WHERE id = $3`,
		p.Slug, p.Visibility.orPublic(), p.ID)
	if dberr.IsUniqueViolation(err) {
		return ErrDuplicateSlug
	}
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO cms_page_drafts (page_id, template_name, head_css, body_js)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (page_id)
		DO UPDATE SET template_name = EXCLUDED.template_name,
			head_css = EXCLUDED.head_css, body_js = EXCLUDED.body_js`,
		p.ID, p.TemplateName, p.HeadCSS, p.BodyJS); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO cms_page_meta (page_id, locale, title, description, meta_description, status)
		VALUES ($1, $2, $3, $4, $5, 'draft')
		ON CONFLICT (page_id, locale, status)
		DO UPDATE SET title = EXCLUDED.title, description = EXCLUDED.description,
			meta_description = EXCLUDED.meta_description`,
		p.ID, locale, p.Title, p.Description, p.MetaDescription); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// UpdateMeta saves only a page's per-locale metadata — how non-default-
// locale admin tabs save, since every other page field is locale-
// independent, and how the in-place editor's settings dialogs save. Like
// Update it writes the working copy, so the change reaches the site on
// the next Publish.
func (s *Store) UpdateMeta(ctx context.Context, pageID int64, locale string, m PageMeta) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO cms_page_meta (page_id, locale, title, description, meta_description, status)
		VALUES ($1, $2, $3, $4, $5, 'draft')
		ON CONFLICT (page_id, locale, status)
		DO UPDATE SET title = EXCLUDED.title, description = EXCLUDED.description,
			meta_description = EXCLUDED.meta_description`,
		pageID, locale, m.Title, m.Description, m.MetaDescription)
	return err
}

// PageMeta is one locale's page metadata exactly as stored, without the
// default-locale fallback Page.Title and Page.Description carry.
// Inherited holds what that fallback would supply, so a caller editing a
// translation can offer the inherited words as a placeholder rather than
// prefilling the field with them — prefilling would copy the default
// language into the translation's own row on the next save, and the page
// would stop tracking the original for good.
//
// It is also what UpdateMeta writes, so a save states every stored field
// rather than a list of strings whose order is the only thing keeping
// the title out of the description.
type PageMeta struct {
	Title           string
	Description     string
	MetaDescription string
	// The default locale's stored values. All are empty when the
	// metadata read is the default locale's own.
	InheritedTitle           string
	InheritedDescription     string
	InheritedMetaDescription string
}

// MetaFor returns the page's draft metadata for locale as stored: no
// fallback applied, so an empty field means this locale has none of its
// own and reads as the default locale's. It is the read behind an
// editing form, where Page's already-resolved Title and Description
// cannot tell an inherited value from an authored one.
func (s *Store) MetaFor(ctx context.Context, pageID int64, locale string) (PageMeta, error) {
	var m PageMeta
	err := s.db.QueryRow(ctx, `
		SELECT COALESCE(m.title, ''), COALESCE(m.description, ''), COALESCE(m.meta_description, ''),
			COALESCE(md.title, ''), COALESCE(md.description, ''), COALESCE(md.meta_description, '')
		FROM cms_pages p
		LEFT JOIN cms_page_meta m ON m.page_id = p.id AND m.locale = $2 AND m.status = 'draft'
		LEFT JOIN cms_page_meta md ON md.page_id = p.id AND md.locale = $3 AND md.status = 'draft'
		WHERE p.id = $1`, pageID, locale, s.defaultLocale,
	).Scan(&m.Title, &m.Description, &m.MetaDescription,
		&m.InheritedTitle, &m.InheritedDescription, &m.InheritedMetaDescription)
	if errors.Is(err, sql.ErrNoRows) {
		return PageMeta{}, ErrNotFound
	}
	if err != nil {
		return PageMeta{}, err
	}
	// The default locale inherits from nothing; reporting its own values
	// as inherited would offer them as a placeholder for themselves.
	if locale == s.defaultLocale {
		m.InheritedTitle, m.InheritedDescription, m.InheritedMetaDescription = "", "", ""
	}
	return m, nil
}

// Delete removes a page and (via cascade) its metadata and blocks. The
// site page is not deletable — losing it would take every shared region
// with it — so it reads as not found.
func (s *Store) Delete(ctx context.Context, id int64) error {
	tag, err := s.db.Exec(ctx, "DELETE FROM cms_pages WHERE id = $1 AND NOT is_system", id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Publish makes the page's draft content live: the published block set and
// metadata are replaced by copies of the draft ones, the staged page-level
// fields are copied onto the page row, and the page is marked published.
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
	if _, err := tx.Exec(ctx,
		"DELETE FROM cms_page_meta WHERE page_id = $1 AND status = 'published'", pageID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO cms_page_meta (page_id, locale, title, description, meta_description, status)
		SELECT page_id, locale, title, description, meta_description, 'published'
		FROM cms_page_meta WHERE page_id = $1 AND status = 'draft'`, pageID); err != nil {
		return err
	}
	// Correlated subqueries rather than a join, so a page with no working
	// copy row still publishes (keeping its current values) instead of
	// silently matching nothing and reporting ErrNotFound.
	tag, err := tx.Exec(ctx, `
		UPDATE cms_pages SET
			template_name = COALESCE((SELECT template_name FROM cms_page_drafts WHERE page_id = $1), template_name),
			head_css      = COALESCE((SELECT head_css FROM cms_page_drafts WHERE page_id = $1), head_css),
			body_js       = COALESCE((SELECT body_js FROM cms_page_drafts WHERE page_id = $1), body_js),
			status = 'published', updated_at = now()
		WHERE id = $1`, pageID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return tx.Commit(ctx)
}

// DiscardDraft throws away a page's unpublished edits: the draft blocks
// and metadata are replaced by copies of the currently published ones and
// the staged page-level fields revert to the page row, so the editor
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
	if _, err := tx.Exec(ctx,
		"DELETE FROM cms_page_meta WHERE page_id = $1 AND status = 'draft'", pageID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO cms_page_meta (page_id, locale, title, description, meta_description, status)
		SELECT page_id, locale, title, description, meta_description, 'draft'
		FROM cms_page_meta WHERE page_id = $1 AND status = 'published'`, pageID); err != nil {
		return err
	}
	// Correlated subqueries rather than UPDATE ... FROM, which is
	// Postgres-only syntax; this matches the shape Publish uses above and
	// runs unchanged on both engines.
	if _, err := tx.Exec(ctx, `
		UPDATE cms_page_drafts SET
			template_name = COALESCE((SELECT template_name FROM cms_pages WHERE id = $1), template_name),
			head_css      = COALESCE((SELECT head_css FROM cms_pages WHERE id = $1), head_css),
			body_js       = COALESCE((SELECT body_js FROM cms_pages WHERE id = $1), body_js)
		WHERE page_id = $1`, pageID); err != nil {
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
