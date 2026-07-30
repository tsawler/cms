package content

import (
	"context"

	"github.com/tsawler/cms/internal/sqldb"
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
	return sqldb.CollectRows(rows, func(row sqldb.Scanner) (Block, error) {
		var b Block
		err := row.Scan(&b.ID, &b.PageID, &b.Region, &b.Locale, &b.Status, &b.Sort,
			&b.Kind, &b.SnippetKey, &b.Content, sqldb.JSONInto(&b.Settings))
		return b, err
	})
}

// EffectiveBlocks returns a page's blocks for locale with region-level
// fallback to the store's default locale: regions with no rows in the
// requested locale use the default locale's rows wholesale. Region-level
// (not per-block) because a sections region is one ordered document —
// interleaving two locales' section lists would be nonsense. Callers can
// tell fallback content apart by the blocks' Locale field.
func (s *Store) EffectiveBlocks(ctx context.Context, pageID int64, locale string, status Status) ([]Block, error) {
	if locale == s.defaultLocale {
		return s.BlocksFor(ctx, pageID, locale, status)
	}
	rows, err := s.db.Query(ctx, `
		SELECT id, page_id, region, locale, status, sort, kind, snippet_key, content, settings
		FROM cms_blocks
		WHERE page_id = $1 AND locale IN ($2, $3) AND status = $4
		ORDER BY region, sort`, pageID, locale, s.defaultLocale, status)
	if err != nil {
		return nil, err
	}
	all, err := sqldb.CollectRows(rows, func(row sqldb.Scanner) (Block, error) {
		var b Block
		err := row.Scan(&b.ID, &b.PageID, &b.Region, &b.Locale, &b.Status, &b.Sort,
			&b.Kind, &b.SnippetKey, &b.Content, sqldb.JSONInto(&b.Settings))
		return b, err
	})
	if err != nil {
		return nil, err
	}
	localized := map[string]bool{}
	for _, b := range all {
		if b.Locale == locale {
			localized[b.Region] = true
		}
	}
	out := all[:0]
	for _, b := range all {
		if b.Locale == locale || !localized[b.Region] {
			out = append(out, b)
		}
	}
	return out, nil
}

// HasUnpublishedChanges reports whether a page has edits that the site is
// not yet showing: draft blocks or metadata differing from the published
// ones in any way (content, order, settings, or rows added/removed) in any
// locale, or staged page-level fields differing from the page row. Locale-
// blind because Publish snapshots every locale at once.
// The set-difference probes rely on EXCEPT, which MySQL only gained in
// 8.0.31 and MariaDB in 10.3 — that is where the CMS's MySQL floor comes
// from. The JSON cast and the NULL-safe comparison have no shared spelling,
// so both come from the dialect.
func (s *Store) HasUnpublishedChanges(ctx context.Context, pageID int64) (bool, error) {
	d := s.db.Dialect()
	blockSet := `SELECT region, locale, sort, kind, coalesce(snippet_key, ''), content, ` +
		d.JSONText("settings") + `
		FROM cms_blocks WHERE page_id = $1 AND status = `
	const metaSet = `SELECT locale, title, description, meta_description
		FROM cms_page_meta WHERE page_id = $1 AND status = `
	var changed bool
	err := s.db.QueryRow(ctx, `
		SELECT EXISTS ((`+blockSet+`'draft') EXCEPT (`+blockSet+`'published'))
		    OR EXISTS ((`+blockSet+`'published') EXCEPT (`+blockSet+`'draft'))
		    OR EXISTS ((`+metaSet+`'draft') EXCEPT (`+metaSet+`'published'))
		    OR EXISTS ((`+metaSet+`'published') EXCEPT (`+metaSet+`'draft'))
		    OR EXISTS (
				SELECT 1 FROM cms_page_drafts d
				JOIN cms_pages p ON p.id = d.page_id
				WHERE d.page_id = $1
				  AND (`+d.Distinct("d.template_name", "p.template_name")+`
				    OR `+d.Distinct("d.head_css", "p.head_css")+`
				    OR `+d.Distinct("d.body_js", "p.body_js")+`))`,
		pageID).Scan(&changed)
	return changed, err
}

// DeleteLocaleContent removes a page's draft blocks and draft metadata row
// for one (non-default) locale, so the page reverts to default-locale
// fallback. Draft-side only: like any edit it goes live on the next
// Publish.
func (s *Store) DeleteLocaleContent(ctx context.Context, pageID int64, locale string) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `
		DELETE FROM cms_blocks WHERE page_id = $1 AND locale = $2 AND status = 'draft'`,
		pageID, locale); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		DELETE FROM cms_page_meta WHERE page_id = $1 AND locale = $2 AND status = 'draft'`,
		pageID, locale); err != nil {
		return err
	}
	return tx.Commit(ctx)
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
			pageID, region, locale, i, sec.Content, sqldb.JSON(sec.Settings)); err != nil {
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
