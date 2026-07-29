package content

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/tsawler/cms/internal/dberr"
	"github.com/tsawler/cms/internal/sqldb"
	"github.com/tsawler/cms/media"
)

// Feed is which of the two post feeds a post belongs to. Blog and news are
// the same engine — a post's feed decides which listings and RSS feed it
// appears in, and the slug prefix its page lives under.
type Feed string

const (
	FeedBlog Feed = "blog"
	FeedNews Feed = "news"
)

// ValidFeed reports whether s names a known feed.
func ValidFeed(s string) bool {
	return s == string(FeedBlog) || s == string(FeedNews)
}

// Post is one blog or news entry. The embedded Page is its backing page —
// the post's body lives in that page's blocks and is edited exactly like
// any page (in place, with sections and snippets); the page's Title and
// Description double as the post's title and summary. Post slugs are
// always prefixed with the feed name, e.g. "blog/launch-day".
type Post struct {
	Page
	PostID      int64
	Feed        Feed
	PublishedAt time.Time // display and ordering date, not a schedule
	AuthorID    *int64
	AuthorName  string // resolved from cms_users; "" when the author is gone

	// A post's two images are each either a library image or a bare URL.
	// The library is the normal case: the id is stored and the *MediaID
	// and *Media fields are set, which lets the renderer pick the
	// rendition that fits the slot and build a srcset from the rest.
	// The URL fields carry images the library does not hold — an absolute
	// URL elsewhere, or a path the host site serves itself — and are empty
	// whenever an id is set.
	ThumbnailMediaID *int64
	ThumbnailURL     string       // optional listing thumbnail, when not from the library
	Thumbnail        *media.Media // joined library record, nil when there is none
	HeaderMediaID    *int64
	HeaderURL        string // optional header/banner image, when not from the library
	Header           *media.Media
}

// ThumbnailMediaIDValue returns the thumbnail's media id, or 0 when the
// post has no library thumbnail — convenient in templates, where pointers
// are awkward to compare.
func (p *Post) ThumbnailMediaIDValue() int64 { return idValue(p.ThumbnailMediaID) }

// HeaderMediaIDValue is ThumbnailMediaIDValue for the header image.
func (p *Post) HeaderMediaIDValue() int64 { return idValue(p.HeaderMediaID) }

func idValue(id *int64) int64 {
	if id == nil {
		return 0
	}
	return *id
}

// postColumns and postJoins take draft for the same reason pageColumns
// does: admin screens and the in-place editor read the working copy, the
// public listings and feeds read what is published.
func postColumns(draft bool) string {
	return pageColumns(draft) + `,
	po.id, po.feed, po.published_at, po.author_id, COALESCE(u.name, ''),
	po.thumbnail_url, po.header_url,` +
		postMediaColumns("tm", "tma") + "," + postMediaColumns("hm", "hma")
}

// postMediaColumns lists one joined image's columns: everything
// media.Manager needs to build its renditions, plus the alt text for the
// render's locale. They are all NULL when the post has no such image.
func postMediaColumns(item, meta string) string {
	return item + `.id, ` + item + `.kind, ` + item + `.store_key, ` + item + `.ext, ` +
		item + `.variant_ext, ` + item + `.width, ` + item + `.height, ` + meta + `.alt_text`
}

func postJoins(draft bool) string {
	status := "published"
	if draft {
		status = "draft"
	}
	j := `
	FROM cms_posts po
	JOIN cms_pages p ON p.id = po.page_id
	LEFT JOIN cms_page_meta m ON m.page_id = p.id AND m.locale = $1 AND m.status = '` + status + `'
	LEFT JOIN cms_page_meta md ON md.page_id = p.id AND md.locale = $2 AND md.status = '` + status + `'
	LEFT JOIN cms_users u ON u.id = po.author_id
	LEFT JOIN cms_media tm ON tm.id = po.thumbnail_media_id
	LEFT JOIN cms_media_meta tma ON tma.media_id = tm.id AND tma.locale = $1
	LEFT JOIN cms_media hm ON hm.id = po.header_media_id
	LEFT JOIN cms_media_meta hma ON hma.media_id = hm.id AND hma.locale = $1`
	if draft {
		j += `
	LEFT JOIN cms_page_drafts d ON d.page_id = p.id`
	}
	return j
}

// joinedMedia receives the LEFT JOINed cms_media columns of one post
// image. Every field is nullable because the join misses whenever the post
// has no library image there.
type joinedMedia struct {
	id         sql.NullInt64
	kind       sql.NullString
	storeKey   sql.NullString
	ext        sql.NullString
	variantExt sql.NullString
	width      sql.NullInt64
	height     sql.NullInt64
	alt        sql.NullString
}

func (j *joinedMedia) dest() []any {
	return []any{&j.id, &j.kind, &j.storeKey, &j.ext, &j.variantExt, &j.width, &j.height, &j.alt}
}

// record rebuilds the media record the columns describe, and the id to
// write back on the next save. Both are nil when the join missed.
func (j *joinedMedia) record() (*int64, *media.Media) {
	if !j.id.Valid {
		return nil, nil
	}
	id := j.id.Int64
	return &id, &media.Media{
		ID:         id,
		Kind:       media.Kind(j.kind.String),
		StoreKey:   j.storeKey.String,
		Ext:        j.ext.String,
		VariantExt: j.variantExt.String,
		Width:      int(j.width.Int64),
		Height:     int(j.height.Int64),
		Alt:        j.alt.String,
	}
}

func scanPost(row sqldb.Scanner) (*Post, error) {
	var p Post
	var thumb, header joinedMedia
	dest := []any{&p.ID, &p.Slug, &p.TemplateName, &p.Status, &p.Visibility, &p.HeadCSS, &p.BodyJS,
		&p.Title, &p.Description, &p.CreatedAt, &p.UpdatedAt,
		&p.PostID, &p.Feed, &p.PublishedAt, &p.AuthorID, &p.AuthorName,
		&p.ThumbnailURL, &p.HeaderURL}
	dest = append(dest, thumb.dest()...)
	dest = append(dest, header.dest()...)
	err := row.Scan(dest...)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	p.ThumbnailMediaID, p.Thumbnail = thumb.record()
	p.HeaderMediaID, p.Header = header.record()
	return &p, nil
}

// InsertPost stores a new post and its backing page (always a draft) in one
// transaction, returning the post's id. The caller sets the page fields
// (Slug already feed-prefixed, TemplateName, Title, Description) and the
// post fields; a zero PublishedAt becomes now.
func (s *Store) InsertPost(ctx context.Context, p *Post, locale string) (int64, error) {
	if p.PublishedAt.IsZero() {
		p.PublishedAt = time.Now()
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	p.ID, err = tx.InsertID(ctx, `
		INSERT INTO cms_pages (slug, template_name, status, head_css, body_js)
		VALUES ($1, $2, 'draft', $3, $4)`,
		p.Slug, p.TemplateName, p.HeadCSS, p.BodyJS)
	if dberr.IsUniqueViolation(err) {
		return 0, ErrDuplicateSlug
	}
	if err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO cms_page_drafts (page_id, template_name, head_css, body_js)
		VALUES ($1, $2, $3, $4)`,
		p.ID, p.TemplateName, p.HeadCSS, p.BodyJS); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO cms_page_meta (page_id, locale, title, description, status)
		VALUES ($1, $2, $3, $4, 'draft')`,
		p.ID, locale, p.Title, p.Description); err != nil {
		return 0, err
	}
	p.PostID, err = tx.InsertID(ctx, `
		INSERT INTO cms_posts (page_id, feed, published_at, author_id,
			thumbnail_media_id, thumbnail_url, header_media_id, header_url)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		p.ID, p.Feed, p.PublishedAt, p.AuthorID,
		p.ThumbnailMediaID, p.ThumbnailURL, p.HeaderMediaID, p.HeaderURL)
	if err != nil {
		return 0, err
	}
	return p.PostID, tx.Commit(ctx)
}

// UpdatePost saves a post's fields and its backing page's fields and
// metadata for locale, in one transaction. Like Page updates it does not
// change publication status, and the author is fixed at creation.
//
// The backing page's staged fields (title, description, template, per-page
// code) go to the working copy and reach the site on the next Publish; the
// slug and the cms_posts fields — feed, date, images — apply immediately.
func (s *Store) UpdatePost(ctx context.Context, p *Post, locale string) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tag, err := tx.Exec(ctx, `
		UPDATE cms_pages SET slug = $1, updated_at = now() WHERE id = $2`,
		p.Slug, p.ID)
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
		INSERT INTO cms_page_meta (page_id, locale, title, description, status)
		VALUES ($1, $2, $3, $4, 'draft')
		ON CONFLICT (page_id, locale, status)
		DO UPDATE SET title = EXCLUDED.title, description = EXCLUDED.description`,
		p.ID, locale, p.Title, p.Description); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE cms_posts
		SET feed = $1, published_at = $2,
			thumbnail_media_id = $3, thumbnail_url = $4,
			header_media_id = $5, header_url = $6
		WHERE id = $7`,
		p.Feed, p.PublishedAt, p.ThumbnailMediaID, p.ThumbnailURL,
		p.HeaderMediaID, p.HeaderURL, p.PostID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// PostByID returns the post with the given post id, with page metadata for
// locale. It reads the working copy: every caller is an admin screen.
func (s *Store) PostByID(ctx context.Context, id int64, locale string) (*Post, error) {
	row := s.db.QueryRow(ctx, `SELECT `+postColumns(true)+postJoins(true)+` WHERE po.id = $3`,
		locale, s.defaultLocale, id)
	return scanPost(row)
}

// PostByPageID returns the post backed by the given page, or ErrNotFound
// when the page is not a post. With draft it reads the working copy, which
// is what the in-place editor shows; without, what the site serves.
func (s *Store) PostByPageID(ctx context.Context, pageID int64, locale string, draft bool) (*Post, error) {
	row := s.db.QueryRow(ctx, `SELECT `+postColumns(draft)+postJoins(draft)+` WHERE po.page_id = $3`,
		locale, s.defaultLocale, pageID)
	return scanPost(row)
}

// Posts returns a feed's posts newest first, with page metadata for locale.
// An empty feed returns both feeds (the admin's combined list). With
// publishedOnly, draft and private posts are omitted (the public view);
// without, editors see everything. A non-positive limit returns everything.
func (s *Store) Posts(ctx context.Context, feed Feed, locale string, publishedOnly bool, limit int) ([]Post, error) {
	q := `SELECT ` + postColumns(!publishedOnly) + postJoins(!publishedOnly) + ` WHERE ($3 = '' OR po.feed = $3)`
	if publishedOnly {
		q += ` AND p.status = 'published' AND p.visibility = 'public'`
	}
	q += ` ORDER BY po.published_at DESC, po.id DESC`
	args := []any{locale, s.defaultLocale, feed}
	if limit > 0 {
		q += ` LIMIT $4`
		args = append(args, limit)
	}
	rows, err := s.db.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	return sqldb.CollectRows(rows, func(row sqldb.Scanner) (Post, error) {
		p, err := scanPost(row)
		if err != nil {
			return Post{}, err
		}
		return *p, nil
	})
}

// AllNonPost returns every page that is not a post's backing page, ordered
// by slug — the admin Pages list, where posts appear under Blog & News
// instead.
func (s *Store) AllNonPost(ctx context.Context, locale string) ([]Page, error) {
	rows, err := s.db.Query(ctx, `
		SELECT `+pageColumns(true)+`
		FROM cms_pages p`+pageMetaJoins(1, 2, true)+`
		WHERE NOT EXISTS (SELECT 1 FROM cms_posts po WHERE po.page_id = p.id)
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
