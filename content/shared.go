package content

import (
	"context"
	"database/sql"
	"errors"

	"github.com/tsawler/cms/internal/sqldb"
)

// SiteSlug is the reserved slug of the site page — the system row that owns
// shared-region content ({{cmsShared "footer"}}). It holds an underscore,
// so ValidSlug rejects it and no editor-created page can ever take it.
const SiteSlug = "__site"

// sitePageID is the subquery every read uses to reach the site page
// without a round trip of its own. It yields NULL when the row is missing,
// which matches no block and so degrades to "the site has no shared
// content" rather than to an error.
const sitePageID = `(SELECT id FROM cms_pages WHERE slug = '` + SiteSlug + `' AND is_system)`

// SitePageID returns the id of the site page, the row shared blocks hang
// off, creating it if it has gone missing. The migration writes it, so the
// insert is only reached by a database that has been emptied — a test
// harness truncating between cases, or a hand-cleaned install — and
// recreating it there is better than leaving shared content unsavable.
//
// Only write paths need the id: reads reach the site page through a
// subquery, so rendering never pays for this.
func (s *Store) SitePageID(ctx context.Context) (int64, error) {
	id, err := s.lookupSitePageID(ctx)
	if !errors.Is(err, ErrNotFound) {
		return id, err
	}
	// ON CONFLICT rather than a plain insert: two requests may race here,
	// and the slug is unique. DO UPDATE (not DO NOTHING) because that is
	// the form the MySQL rewriter understands.
	if _, err := s.db.Exec(ctx, `
		INSERT INTO cms_pages (slug, template_name, status, visibility, is_system)
		VALUES ($1, '', 'published', 'public', true)
		ON CONFLICT (slug) DO UPDATE SET is_system = EXCLUDED.is_system`, SiteSlug); err != nil {
		return 0, err
	}
	return s.lookupSitePageID(ctx)
}

func (s *Store) lookupSitePageID(ctx context.Context) (int64, error) {
	var id int64
	err := s.db.QueryRow(ctx,
		"SELECT id FROM cms_pages WHERE slug = $1 AND is_system", SiteSlug).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	return id, err
}

// EffectiveBlocksWithShared returns a page's blocks and the site's shared
// blocks for one locale and publication state, in a single query. Both
// sets get the same region-level locale fallback EffectiveBlocks applies.
//
// The two travel together because every page render needs both: shared
// regions are the site's chrome, so a separate round trip would be one
// more query on every request, for content that is the same on all of them.
func (s *Store) EffectiveBlocksWithShared(ctx context.Context, pageID int64, locale string, status Status) (page, shared []Block, err error) {
	all, err := s.effectiveBlocks(ctx, pageID, true, locale, status)
	if err != nil {
		return nil, nil, err
	}
	for _, b := range all {
		if b.PageID == pageID {
			page = append(page, b)
		} else {
			shared = append(shared, b)
		}
	}
	return page, shared, nil
}

// SharedBlocks returns just the site's shared blocks, with the same
// locale fallback.
func (s *Store) SharedBlocks(ctx context.Context, locale string, status Status) ([]Block, error) {
	// 0 is no page's id, so the page half of the query matches nothing.
	return s.effectiveBlocks(ctx, 0, true, locale, status)
}

// UpsertSharedBlock stores one shared region's draft content. It is
// UpsertDraftBlock aimed at the site page, so shared edits ride the same
// draft/publish workflow as page content.
func (s *Store) UpsertSharedBlock(ctx context.Context, region, locale string, kind Kind, content string) error {
	siteID, err := s.SitePageID(ctx)
	if err != nil {
		return err
	}
	return s.UpsertDraftBlock(ctx, siteID, region, locale, kind, content)
}

// PublishShared makes the shared regions' draft content live. Every page
// shows shared content, so there is no page to publish it "on": it goes
// live alongside whichever page the editor published from.
func (s *Store) PublishShared(ctx context.Context) error {
	return s.PublishSharedAs(ctx, nil)
}

// PublishSharedAs is PublishShared, attributing the edition to the user
// with id by.
//
// The site page keeps its own history, like any page — which is what makes
// a footer recoverable at all, since shared content belongs to no page that
// could hold a version of it. It is also why an unchanged snapshot is never
// stored: this runs on every publish anywhere on the site, and almost none
// of those touched the footer.
func (s *Store) PublishSharedAs(ctx context.Context, by *int64) error {
	siteID, err := s.SitePageID(ctx)
	if err != nil {
		return err
	}
	return s.PublishAs(ctx, siteID, by)
}

// HasSharedUnpublishedChanges reports whether shared regions hold saved
// edits the site is not showing yet — the same probe HasUnpublishedChanges
// runs for a page, so the editor's status chip can count shared content as
// what it is: an unpublished edit visible on the page in front of you.
//
// A site page that does not exist has no edits, rather than being an
// error: this runs on every editor render and must not take pages down.
func (s *Store) HasSharedUnpublishedChanges(ctx context.Context) (bool, error) {
	siteID, err := s.lookupSitePageID(ctx)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return s.HasUnpublishedChanges(ctx, siteID)
}

// effectiveBlocks reads a page's blocks — and, with shared, the site
// page's too — applying region-level fallback to the default locale.
// Fallback is decided per (page, region): a page region and a shared
// region may share a name, and one of them being translated must not stop
// the other from falling back.
func (s *Store) effectiveBlocks(ctx context.Context, pageID int64, shared bool, locale string, status Status) ([]Block, error) {
	// The site page is reached by subquery rather than by an id resolved
	// beforehand, so the whole read stays one round trip.
	where := "page_id = $1"
	if shared {
		where = "(page_id = $1 OR page_id = " + sitePageID + ")"
	}
	rows, err := s.db.Query(ctx, `
		SELECT id, page_id, region, locale, status, sort, kind, snippet_key, content, settings
		FROM cms_blocks
		WHERE `+where+` AND locale IN ($2, $3) AND status = $4
		ORDER BY page_id, region, sort`, pageID, locale, s.defaultLocale, status)
	if err != nil {
		return nil, err
	}
	all, err := sqldb.CollectRows(rows, scanBlock)
	if err != nil || locale == s.defaultLocale {
		return all, err
	}
	type key struct {
		page   int64
		region string
	}
	localized := map[key]bool{}
	for _, b := range all {
		if b.Locale == locale {
			localized[key{b.PageID, b.Region}] = true
		}
	}
	out := all[:0]
	for _, b := range all {
		if b.Locale == locale || !localized[key{b.PageID, b.Region}] {
			out = append(out, b)
		}
	}
	return out, nil
}
