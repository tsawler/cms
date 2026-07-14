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

// Block is one unit of editable content inside a page region. Phase 2 uses
// a single block per region; snippets (phase 5) introduce multiple ordered
// blocks and SnippetKey.
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
}

// BlocksFor returns a page's blocks for one locale and publication state,
// ordered by region and sort.
func (s *Store) BlocksFor(ctx context.Context, pageID int64, locale string, status Status) ([]Block, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, page_id, region, locale, status, sort, kind, snippet_key, content
		FROM cms_blocks
		WHERE page_id = $1 AND locale = $2 AND status = $3
		ORDER BY region, sort`, pageID, locale, status)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Block, error) {
		var b Block
		err := row.Scan(&b.ID, &b.PageID, &b.Region, &b.Locale, &b.Status, &b.Sort,
			&b.Kind, &b.SnippetKey, &b.Content)
		return b, err
	})
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
