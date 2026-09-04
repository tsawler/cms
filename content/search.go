package content

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/tsawler/cms/internal/sqldb"
)

// SearchResult is one hit from the site search: enough to draw a result
// without going back for the page.
type SearchResult struct {
	PageID int64
	Locale string
	// Kind is "page", "blog" or "news" — what the result is, so a results
	// template can badge or group them.
	Kind string
	Slug string
	// Title and Summary are the page's published metadata for this locale.
	// Summary is a post's summary or an ordinary page's description, and
	// is empty on a page that has neither.
	Title   string
	Summary string
	// Body is the page's whole indexed text, which the caller cuts a
	// snippet out of (see Snippet). It is carried rather than snipped in
	// SQL because the only portable way to snip around a match is to have
	// the text — Postgres has ts_headline for it and MySQL has nothing.
	Body string
	// PublishedAt is a post's display date, nil on an ordinary page.
	PublishedAt *time.Time
	// Rank is the engine's score for this hit. Comparable within one set
	// of results and meaningless between two — it exists for ordering and
	// for looking at when the ordering seems wrong.
	Rank float64
}

// SetLocales tells the store which locales the site serves, so that
// publishing a page indexes it once per locale. Empty or nil leaves the
// store indexing the default locale alone, which is what a single-language
// site wants and what a Store built without this keeps doing.
//
// It mirrors the site's Config.Locales rather than being read from the
// database because it is a fact about the host application, not about the
// content: a locale the site does not serve has no URL to put in a result.
func (s *Store) SetLocales(locales []string) {
	s.locales = slices.Clone(locales)
}

// indexLocales is the locale set a page is indexed for.
func (s *Store) indexLocales() []string {
	if len(s.locales) == 0 {
		return []string{s.defaultLocale}
	}
	return s.locales
}

// ReindexPage rebuilds one page's search documents on its own transaction.
// It is what every change outside the publish flow calls — a rename, a
// visibility switch, an unpublish, a post's date being edited — since all
// of those change what a result would say without touching a block.
func (s *Store) ReindexPage(ctx context.Context, pageID int64) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := s.reindexPage(ctx, tx, pageID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// reindexPage replaces a page's rows in cms_search_docs with what the site
// currently serves for it, or with nothing at all when the site serves it
// to nobody.
//
// The delete-then-insert is unconditional, and that is the point: this one
// function is the only thing that decides whether a page is searchable, so
// every caller can be "reindex this page" without knowing why. A page that
// has just been unpublished, made private, or turned into the system page
// takes the same path as one that was published — the rows come out, and
// nothing goes back in.
//
// It takes a handle rather than starting its own transaction so that
// Publish can call it inside the transaction that publishes: the index and
// the content it describes then become true at the same instant, and a
// publish that fails leaves no rows behind describing a page the site
// never served.
func (s *Store) reindexPage(ctx context.Context, h handle, pageID int64) error {
	if _, err := h.Exec(ctx, "DELETE FROM cms_search_docs WHERE page_id = $1", pageID); err != nil {
		return err
	}

	var (
		slug       string
		status     Status
		visibility Visibility
		isSystem   bool
		feed       string
		publishAt  *time.Time
	)
	err := h.QueryRow(ctx, `
		SELECT p.slug, p.status, p.visibility, p.is_system,
		       COALESCE(po.feed, ''), po.published_at
		FROM cms_pages p
		LEFT JOIN cms_posts po ON po.page_id = p.id
		WHERE p.id = $1`, pageID).
		Scan(&slug, &status, &visibility, &isSystem, &feed, &publishAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil // deleted under us; the cascade has already emptied the rows
	}
	if err != nil {
		return err
	}
	// The index's whole security story, in one condition. A search never
	// re-checks any of this, because anything it could find has already
	// passed here.
	if isSystem || status != StatusPublished || visibility != VisibilityPublic {
		return nil
	}

	meta, err := publishedMeta(ctx, h, pageID)
	if err != nil {
		return err
	}
	blocks, err := publishedBlockText(ctx, h, pageID)
	if err != nil {
		return err
	}

	kind := "page"
	if feed != "" {
		kind = feed
	}
	d := s.db.Dialect()
	cols := "page_id, locale, kind, slug, title, summary, body, published_at"
	vals := "$1, $2, $3, $4, $5, $6, $7, $8"
	if col, expr := d.SearchIndexWrite("$9", "$5", "$6", "$7"); col != "" {
		cols += ", " + col
		vals += ", " + expr
	}
	stmt := "INSERT INTO cms_search_docs (" + cols + ") VALUES (" + vals + ")"

	for _, locale := range s.indexLocales() {
		title, summary := meta.for_(locale, s.defaultLocale)
		body := blocks.for_(locale, s.defaultLocale)
		// A page with no words at all is left out rather than stored
		// empty: it can never match, and an empty row is one more thing
		// for a query to walk past.
		if title == "" && summary == "" && body == "" {
			continue
		}
		if _, err := h.Exec(ctx, stmt,
			pageID, locale, kind, slug, title, summary, body, publishAt,
			d.SearchConfig(locale)); err != nil {
			return err
		}
	}
	return nil
}

// ReindexAll rebuilds the entire search index from what is published, and
// reports how many pages it visited.
//
// It exists for the two moments the incremental path cannot cover: an
// install that had content before it had a search index, and a change to
// how text is extracted, which makes every stored document a little wrong
// in the same way. Ordinary operation never needs it — Publish keeps the
// index current on its own.
//
// Each page is its own transaction rather than the whole rebuild being
// one. A site's entire published content in a single transaction is a long
// lock over a table the public is reading from, and the alternative failure
// mode — an interrupted rebuild leaving some pages reindexed and some not
// — costs nothing, since running it again is the fix and running it twice
// is harmless.
func (s *Store) ReindexAll(ctx context.Context) (int, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id FROM cms_pages
		WHERE NOT is_system AND status = 'published' AND visibility = 'public'
		ORDER BY id`)
	if err != nil {
		return 0, err
	}
	ids, err := sqldb.CollectRows(rows, func(row sqldb.Scanner) (int64, error) {
		var id int64
		err := row.Scan(&id)
		return id, err
	})
	if err != nil {
		return 0, err
	}
	// Documents for pages that are no longer eligible — unpublished while
	// the index was not being maintained — are not reached by the loop
	// below, which only visits pages that qualify. Clearing first is what
	// makes this a rebuild rather than a refresh.
	if _, err := s.db.Exec(ctx, "DELETE FROM cms_search_docs"); err != nil {
		return 0, err
	}
	for i, id := range ids {
		if err := s.ReindexPage(ctx, id); err != nil {
			return i, err
		}
	}
	return len(ids), nil
}

// SearchIndexEmpty reports whether the index holds no documents at all. It
// is how a host decides to run ReindexAll once at startup: an install that
// predates the search table has content and no index, and nothing else
// distinguishes it from a site with nothing published yet — for which the
// rebuild is instant anyway.
func (s *Store) SearchIndexEmpty(ctx context.Context) (bool, error) {
	var any int
	err := s.db.QueryRow(ctx, "SELECT COUNT(*) FROM (SELECT 1 FROM cms_search_docs LIMIT 1) t").Scan(&any)
	return any == 0, err
}

// publishedMeta reads a page's published per-locale title and description.
type localeMeta map[string][2]string

func publishedMeta(ctx context.Context, h handle, pageID int64) (localeMeta, error) {
	rows, err := h.Query(ctx, `
		SELECT locale, title, description
		FROM cms_page_meta WHERE page_id = $1 AND status = 'published'`, pageID)
	if err != nil {
		return nil, err
	}
	out := localeMeta{}
	_, err = sqldb.CollectRows(rows, func(row sqldb.Scanner) (struct{}, error) {
		var l, t, d string
		err := row.Scan(&l, &t, &d)
		out[l] = [2]string{t, d}
		return struct{}{}, err
	})
	return out, err
}

// for_ resolves one locale's title and summary with the same field-level
// fallback pageColumns applies, so what the index holds is what the page
// shows: a French page with only a title still carries the English
// description.
func (m localeMeta) for_(locale, defaultLocale string) (title, summary string) {
	got, def := m[locale], m[defaultLocale]
	title, summary = got[0], got[1]
	if title == "" {
		title = def[0]
	}
	if summary == "" {
		summary = def[1]
	}
	return title, summary
}

// localeBlocks holds a page's published block text grouped by locale and
// region, which is the granularity the fallback works at.
type localeBlocks map[string]map[string][]string

// publishedBlockText reads a page's published blocks and renders each to
// plain text, in the order a reader meets them.
//
// Image blocks are skipped: their content is a URL, and a search that
// matched it would be matching a file name nobody typed. Custom-code
// blocks need no special handling — a page stores only the inert
// placeholder for those (see 0032), and the placeholder has no text in it.
func publishedBlockText(ctx context.Context, h handle, pageID int64) (localeBlocks, error) {
	rows, err := h.Query(ctx, `
		SELECT locale, region, kind, content
		FROM cms_blocks
		WHERE page_id = $1 AND status = 'published'
		ORDER BY region, sort`, pageID)
	if err != nil {
		return nil, err
	}
	out := localeBlocks{}
	_, err = sqldb.CollectRows(rows, func(row sqldb.Scanner) (struct{}, error) {
		var locale, region string
		var kind Kind
		var content string
		if err := row.Scan(&locale, &region, &kind, &content); err != nil {
			return struct{}{}, err
		}
		if kind == KindImage {
			return struct{}{}, nil
		}
		text := SearchText(content)
		if text == "" {
			return struct{}{}, nil
		}
		if out[locale] == nil {
			out[locale] = map[string][]string{}
		}
		out[locale][region] = append(out[locale][region], text)
		return struct{}{}, nil
	})
	return out, err
}

// for_ joins one locale's text with region-level fallback to the default
// locale — the same rule EffectiveBlocks renders by, so the index holds
// the words the visitor is shown and not some other set.
func (b localeBlocks) for_(locale, defaultLocale string) string {
	got, def := b[locale], b[defaultLocale]
	regions := map[string]bool{}
	for r := range got {
		regions[r] = true
	}
	for r := range def {
		regions[r] = true
	}
	// Sorted, so reindexing an unchanged page writes an unchanged
	// document — which is what makes a rebuild a no-op instead of
	// silently reordering every row's text.
	names := make([]string, 0, len(regions))
	for r := range regions {
		names = append(names, r)
	}
	slices.Sort(names)
	var parts []string
	for _, r := range names {
		if texts, ok := got[r]; ok {
			parts = append(parts, texts...)
			continue
		}
		parts = append(parts, def[r]...)
	}
	return strings.Join(parts, " ")
}

// searchWhere builds the filter and the ranking expression one query
// shares with its count, along with the arguments they take.
//
// Terms shorter than the engine's index floor are matched with LIKE rather
// than through the index (see dialect.SearchMinWordLen). On Postgres there
// are none — it indexes every word — so this is in practice the MySQL path,
// where the alternative is that a site writing about AI or 3D printing
// cannot be searched for either. A substring match is a blunter instrument
// than the index: "ai" finds "said". It is still a better answer than an
// empty results page for a word that is on the site.
func (s *Store) searchWhere(terms []SearchTerm, locale string) (where, rank string, args []any) {
	d := s.db.Dialect()
	minLen := d.SearchMinWordLen()
	var indexed []SearchTerm
	var short []SearchTerm
	for _, t := range terms {
		// A phrase is measured by its longest word: "in AI" is a phrase
		// the index can find on the strength of the word it does hold.
		if longestWord(t.Text) >= minLen {
			indexed = append(indexed, t)
			continue
		}
		short = append(short, t)
	}

	args = []any{locale}
	clauses := []string{"locale = $1"}
	next := func(v any) string {
		args = append(args, v)
		return "$" + strconv.Itoa(len(args))
	}
	if len(indexed) > 0 {
		cfg := next(d.SearchConfig(locale))
		q := next(d.SearchQuery(indexed))
		w, r := d.SearchMatch(cfg, q)
		clauses = append(clauses, w)
		rank = r
	}
	for _, t := range short {
		ph := next("%" + escapeLike(t.Text) + "%")
		c := "(" + d.CaseInsensitiveLike("title", ph) +
			" OR " + d.CaseInsensitiveLike("summary", ph) +
			" OR " + d.CaseInsensitiveLike("body", ph) + ")"
		if t.Exclude {
			c = "NOT " + c
		}
		clauses = append(clauses, c)
	}
	return strings.Join(clauses, " AND "), rank, args
}

// longestWord is the length in runes of the longest run of non-space
// characters in s.
func longestWord(s string) int {
	best, n := 0, 0
	for _, r := range s {
		if r == ' ' {
			n = 0
			continue
		}
		n++
		best = max(best, n)
	}
	return best
}

// escapeLike neutralizes the LIKE wildcards in a literal string, so a
// visitor searching for "100%" is not handed every page on the site.
// Backslash is the default escape character on both engines.
func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

// CountSearch is how many documents match, for sizing a results pager. It
// runs the same filter as Search, so the page count always describes the
// results being shown.
func (s *Store) CountSearch(ctx context.Context, terms []SearchTerm, locale string) (int, error) {
	if len(terms) == 0 {
		return 0, nil
	}
	where, _, args := s.searchWhere(terms, locale)
	var n int
	err := s.db.QueryRow(ctx, "SELECT COUNT(*) FROM cms_search_docs WHERE "+where, args...).Scan(&n)
	return n, err
}

// Search returns one window of results for a parsed query, best match
// first. An empty term list matches nothing — there is no query to run.
//
// The ordering is total: page_id breaks a tie in rank, and it is unique
// within a locale, so no result can straddle two pages of a paginated
// listing or be skipped between them. That is the same property the post
// listings are careful to have, and for the same reason.
func (s *Store) Search(ctx context.Context, terms []SearchTerm, locale string, limit, offset int) ([]SearchResult, error) {
	if len(terms) == 0 {
		return nil, nil
	}
	where, rank, args := s.searchWhere(terms, locale)
	scored := rank
	order := "page_id DESC"
	if scored == "" {
		// Nothing was matched through the index, so there is no score to
		// sort by. A bare "0" would be read as an ordinal column
		// reference in ORDER BY, so it appears only in the select list.
		scored = "0"
	} else {
		order = scored + " DESC, page_id DESC"
	}
	q := `
		SELECT page_id, locale, kind, slug, title, summary, body, published_at, ` + scored + `
		FROM cms_search_docs
		WHERE ` + where + `
		ORDER BY ` + order
	if limit > 0 {
		args = append(args, limit)
		q += " LIMIT $" + strconv.Itoa(len(args))
		if offset > 0 {
			args = append(args, offset)
			q += " OFFSET $" + strconv.Itoa(len(args))
		}
	}
	rows, err := s.db.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	return sqldb.CollectRows(rows, func(row sqldb.Scanner) (SearchResult, error) {
		var r SearchResult
		err := row.Scan(&r.PageID, &r.Locale, &r.Kind, &r.Slug, &r.Title,
			&r.Summary, &r.Body, &r.PublishedAt, &r.Rank)
		return r, err
	})
}
