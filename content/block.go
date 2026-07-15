package content

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// Kind distinguishes short plain text ({{cmsText}}) from rich HTML
// ({{cmsRegion}}) block content.
type Kind string

const (
	KindText  Kind = "text"
	KindHTML  Kind = "html"
	KindImage Kind = "image" // content holds the image's public URL
)

// Block is one unit of editable content inside a page region. Simple
// regions (cmsText/cmsRegion/cmsImage) use a single block at sort 0;
// sections regions (cmsSections) hold an ordered list of blocks, one per
// section, with presentation settings.
type Block struct {
	ID         int64
	PageID     int64
	Region     string
	Locale     string
	Status     Status
	Sort       int
	Kind       Kind
	SnippetKey *string
	Content    string
	Settings   map[string]string // section presentation settings (e.g. bg, width)
}

// BlocksFor returns a page's blocks for one locale and publication state,
// ordered by region and sort.
func (s *Store) BlocksFor(ctx context.Context, pageID int64, locale string, status Status) ([]Block, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, page_id, region, locale, status, sort, kind, snippet_key, content, settings
		FROM cms_blocks
		WHERE page_id = $1 AND locale = $2 AND status = $3
		ORDER BY region, sort`, pageID, locale, status)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Block, error) {
		var b Block
		err := row.Scan(&b.ID, &b.PageID, &b.Region, &b.Locale, &b.Status, &b.Sort,
			&b.Kind, &b.SnippetKey, &b.Content, &b.Settings)
		return b, err
	})
}

// HasUnpublishedChanges reports whether a page's draft blocks differ from
// its published blocks in any way (content, order, settings, or blocks
// added/removed) for the locale.
func (s *Store) HasUnpublishedChanges(ctx context.Context, pageID int64, locale string) (bool, error) {
	const blockSet = `SELECT region, sort, kind, coalesce(snippet_key, ''), content, settings::text
		FROM cms_blocks WHERE page_id = $1 AND locale = $2 AND status = `
	var changed bool
	err := s.db.QueryRow(ctx, `
		SELECT EXISTS ((`+blockSet+`'draft') EXCEPT (`+blockSet+`'published'))
		    OR EXISTS ((`+blockSet+`'published') EXCEPT (`+blockSet+`'draft'))`,
		pageID, locale).Scan(&changed)
	return changed, err
}

// SectionInput is one section supplied to ReplaceDraftSections.
type SectionInput struct {
	Settings map[string]string
	Content  string
}

// ReplaceDraftSections replaces a sections region's draft blocks with the
// given ordered list, atomically. An empty list clears the region.
func (s *Store) ReplaceDraftSections(ctx context.Context, pageID int64, region, locale string, sections []SectionInput) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `
		DELETE FROM cms_blocks
		WHERE page_id = $1 AND region = $2 AND locale = $3 AND status = 'draft'`,
		pageID, region, locale); err != nil {
		return err
	}
	for i, sec := range sections {
		if sec.Settings == nil {
			sec.Settings = map[string]string{}
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO cms_blocks (page_id, region, locale, status, sort, kind, content, settings)
			VALUES ($1, $2, $3, 'draft', $4, 'html', $5, $6)`,
			pageID, region, locale, i, sec.Content, sec.Settings); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// UpsertDraftBlock creates or updates the draft block at sort position 0 of
// a region — the single-block-per-region model used until snippets arrive.
func (s *Store) UpsertDraftBlock(ctx context.Context, pageID int64, region, locale string, kind Kind, content string) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO cms_blocks (page_id, region, locale, status, sort, kind, content)
		VALUES ($1, $2, $3, 'draft', 0, $4, $5)
		ON CONFLICT (page_id, region, locale, status, sort)
		DO UPDATE SET content = EXCLUDED.content, kind = EXCLUDED.kind, updated_at = now()`,
		pageID, region, locale, kind, content)
	return err
}
