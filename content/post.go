package content

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/tsawler/cms/internal/pgutil"
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
	PostID       int64
	Feed         Feed
	PublishedAt  time.Time // display and ordering date, not a schedule
	AuthorID     *int64
	AuthorName   string // resolved from cms_users; "" when the author is gone
	ThumbnailURL string // optional listing thumbnail
	HeaderURL    string // optional header/banner image
}

const postColumns = pageColumns + `,
	po.id, po.feed, po.published_at, po.author_id, COALESCE(u.name, ''),
	po.thumbnail_url, po.header_url`

const postJoins = `
	FROM cms_posts po
	JOIN cms_pages p ON p.id = po.page_id
	LEFT JOIN cms_page_meta m ON m.page_id = p.id AND m.locale = $1
	LEFT JOIN cms_page_meta md ON md.page_id = p.id AND md.locale = $2
	LEFT JOIN cms_users u ON u.id = po.author_id`

func scanPost(row pgx.Row) (*Post, error) {
	var p Post
	err := row.Scan(&p.ID, &p.Slug, &p.TemplateName, &p.Status, &p.HeadCSS, &p.BodyJS,
		&p.CSSLinks, &p.JSLinks,
		&p.Title, &p.Description, &p.CreatedAt, &p.UpdatedAt,
		&p.PostID, &p.Feed, &p.PublishedAt, &p.AuthorID, &p.AuthorName,
		&p.ThumbnailURL, &p.HeaderURL)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
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

	err = tx.QueryRow(ctx, `
		INSERT INTO cms_pages (slug, template_name, status, head_css, body_js, css_links, js_links)
		VALUES ($1, $2, 'draft', $3, $4, $5, $6)
		RETURNING id`,
		p.Slug, p.TemplateName, p.HeadCSS, p.BodyJS, p.CSSLinks, p.JSLinks,
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
	if err := tx.QueryRow(ctx, `
		INSERT INTO cms_posts (page_id, feed, published_at, author_id, thumbnail_url, header_url)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id`,
		p.ID, p.Feed, p.PublishedAt, p.AuthorID, p.ThumbnailURL, p.HeaderURL,
	).Scan(&p.PostID); err != nil {
		return 0, err
	}
	return p.PostID, tx.Commit(ctx)
}

// UpdatePost saves a post's fields and its backing page's fields and
// metadata for locale, in one transaction. Like Page updates it does not
// change publication status, and the author is fixed at creation.
func (s *Store) UpdatePost(ctx context.Context, p *Post, locale string) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tag, err := tx.Exec(ctx, `
		UPDATE cms_pages
		SET slug = $1, template_name = $2, head_css = $3, body_js = $4,
			css_links = $5, js_links = $6, updated_at = now()
		WHERE id = $7`,
		p.Slug, p.TemplateName, p.HeadCSS, p.BodyJS, p.CSSLinks, p.JSLinks, p.ID)
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
	if _, err := tx.Exec(ctx, `
		UPDATE cms_posts
		SET feed = $1, published_at = $2, thumbnail_url = $3, header_url = $4
		WHERE id = $5`,
		p.Feed, p.PublishedAt, p.ThumbnailURL, p.HeaderURL, p.PostID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// PostByID returns the post with the given post id, with page metadata for
// locale.
func (s *Store) PostByID(ctx context.Context, id int64, locale string) (*Post, error) {
	row := s.db.QueryRow(ctx, `SELECT `+postColumns+postJoins+` WHERE po.id = $3`,
		locale, s.defaultLocale, id)
	return scanPost(row)
}

// PostByPageID returns the post backed by the given page, or ErrNotFound
// when the page is not a post.
func (s *Store) PostByPageID(ctx context.Context, pageID int64, locale string) (*Post, error) {
	row := s.db.QueryRow(ctx, `SELECT `+postColumns+postJoins+` WHERE po.page_id = $3`,
		locale, s.defaultLocale, pageID)
	return scanPost(row)
}

// Posts returns a feed's posts newest first, with page metadata for locale.
// An empty feed returns both feeds (the admin's combined list). With
// publishedOnly, draft posts are omitted (the public view); without,
// editors see drafts too. A non-positive limit returns everything.
func (s *Store) Posts(ctx context.Context, feed Feed, locale string, publishedOnly bool, limit int) ([]Post, error) {
	q := `SELECT ` + postColumns + postJoins + ` WHERE ($3 = '' OR po.feed = $3)`
	if publishedOnly {
		q += ` AND p.status = 'published'`
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
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Post, error) {
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
		SELECT `+pageColumns+`
		FROM cms_pages p`+pageMetaJoins(1, 2)+`
		WHERE NOT EXISTS (SELECT 1 FROM cms_posts po WHERE po.page_id = p.id)
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
