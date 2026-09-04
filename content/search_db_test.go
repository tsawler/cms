package content_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/tsawler/cms/content"
	"github.com/tsawler/cms/internal/dbtest"
	"github.com/tsawler/cms/internal/sqldb"
)

// searchFor runs a query the way a request does — parse, then search — and
// returns the slugs that came back, in rank order.
func searchFor(t *testing.T, s *content.Store, q, locale string) []string {
	t.Helper()
	results, err := s.Search(context.Background(), content.ParseSearchQuery(q), locale, 0, 0)
	if err != nil {
		t.Fatalf("Search(%q): %v", q, err)
	}
	slugs := make([]string, 0, len(results))
	for _, r := range results {
		slugs = append(slugs, r.Slug)
	}
	return slugs
}

func contains(slugs []string, want string) bool {
	for _, s := range slugs {
		if s == want {
			return true
		}
	}
	return false
}

// publishWithBody gives a page a published body and title and indexes it,
// which is what a real publish does in one transaction.
func publishWithBody(t *testing.T, s *content.Store, p *content.Page, body string) {
	t.Helper()
	ctx := context.Background()
	if err := s.UpsertDraftBlock(ctx, p.ID, "main", defaultLocale, content.KindHTML, body); err != nil {
		t.Fatalf("UpsertDraftBlock: %v", err)
	}
	if err := s.Publish(ctx, p.ID); err != nil {
		t.Fatalf("Publish: %v", err)
	}
}

// The index holds what a visitor reads, with the markup taken off on the
// way in — so a search matches words on the page and never the HTML around
// them.
func TestSearchFindsPublishedWordsAndNotMarkup(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := content.NewStore(db, defaultLocale)
		p := seedPage(t, s, content.Page{Slug: "returns", Title: "Returns policy"}, defaultLocale)
		publishWithBody(t, s, p,
			`<div class="banner"><p>Post anything back within thirty days.</p></div>`)

		if got := searchFor(t, s, "thirty days", defaultLocale); !contains(got, "returns") {
			t.Errorf("searching the page's words found %v, want the returns page", got)
		}
		// The class name is markup, not content: it was never indexed, so
		// there is nothing for this to match.
		if got := searchFor(t, s, "banner", defaultLocale); contains(got, "returns") {
			t.Errorf("a search for a CSS class matched the page: %v", got)
		}
		if got := searchFor(t, s, "Returns policy", defaultLocale); !contains(got, "returns") {
			t.Errorf("searching the title found %v, want the returns page", got)
		}
	})
}

// The index's whole security story: a document exists only for a page the
// site serves to everyone. Nothing is re-checked at query time, so each of
// these has to keep the page out of the table itself.
func TestSearchNeverReturnsWhatTheSiteDoesNotServe(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := content.NewStore(db, defaultLocale)

		draft := seedPage(t, s, content.Page{Slug: "unfinished", Title: "Unfinished"}, defaultLocale)
		if err := s.UpsertDraftBlock(ctx, draft.ID, "main", defaultLocale,
			content.KindHTML, "<p>zebracorn</p>"); err != nil {
			t.Fatalf("UpsertDraftBlock: %v", err)
		}
		if got := searchFor(t, s, "zebracorn", defaultLocale); len(got) != 0 {
			t.Errorf("a never-published page is searchable: %v", got)
		}

		// Published, then hidden three different ways, each of which must
		// take it back out.
		p := seedPage(t, s, content.Page{Slug: "secrets", Title: "Secrets"}, defaultLocale)
		publishWithBody(t, s, p, "<p>zebracorn</p>")
		if got := searchFor(t, s, "zebracorn", defaultLocale); !contains(got, "secrets") {
			t.Fatalf("a published page is not searchable: %v", got)
		}

		if err := s.SetVisibility(ctx, p.ID, content.VisibilityPrivate); err != nil {
			t.Fatalf("SetVisibility: %v", err)
		}
		if got := searchFor(t, s, "zebracorn", defaultLocale); contains(got, "secrets") {
			t.Errorf("a private page is still searchable: %v", got)
		}
		if err := s.SetVisibility(ctx, p.ID, content.VisibilityPublic); err != nil {
			t.Fatalf("SetVisibility(public): %v", err)
		}
		if got := searchFor(t, s, "zebracorn", defaultLocale); !contains(got, "secrets") {
			t.Errorf("making a page public again did not restore it: %v", got)
		}

		if err := s.Unpublish(ctx, p.ID); err != nil {
			t.Fatalf("Unpublish: %v", err)
		}
		if got := searchFor(t, s, "zebracorn", defaultLocale); contains(got, "secrets") {
			t.Errorf("an unpublished page is still searchable: %v", got)
		}

		// And deleting the page takes its documents with it, by cascade.
		if err := s.Publish(ctx, p.ID); err != nil {
			t.Fatalf("Publish: %v", err)
		}
		if err := s.Delete(ctx, p.ID); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if got := searchFor(t, s, "zebracorn", defaultLocale); len(got) != 0 {
			t.Errorf("a deleted page is still searchable: %v", got)
		}
	})
}

// Shared regions are on every page, so indexing them would make the footer
// match every search. The system page they hang off is never indexed.
func TestSearchIgnoresSharedContent(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := content.NewStore(db, defaultLocale)
		p := seedPage(t, s, content.Page{Slug: "about", Title: "About"}, defaultLocale)
		publishWithBody(t, s, p, "<p>ordinary words</p>")

		if err := s.UpsertSharedBlock(ctx, "footer", defaultLocale,
			content.KindHTML, "<p>zebracorn holdings ltd</p>"); err != nil {
			t.Fatalf("UpsertSharedBlock: %v", err)
		}
		if err := s.PublishShared(ctx); err != nil {
			t.Fatalf("PublishShared: %v", err)
		}
		if got := searchFor(t, s, "zebracorn", defaultLocale); len(got) != 0 {
			t.Errorf("shared footer content is searchable: %v", got)
		}
	})
}

// A publish is what puts words into the index, and only a publish: an edit
// sitting in the draft is not on the site and must not be findable.
func TestSearchFollowsThePublishFlow(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := content.NewStore(db, defaultLocale)
		p := seedPage(t, s, content.Page{Slug: "menu", Title: "Menu"}, defaultLocale)
		publishWithBody(t, s, p, "<p>we serve zebracorn on toast</p>")

		if err := s.UpsertDraftBlock(ctx, p.ID, "main", defaultLocale,
			content.KindHTML, "<p>we serve quinoa on toast</p>"); err != nil {
			t.Fatalf("UpsertDraftBlock: %v", err)
		}
		if got := searchFor(t, s, "quinoa", defaultLocale); len(got) != 0 {
			t.Errorf("an unpublished edit is searchable: %v", got)
		}
		if got := searchFor(t, s, "zebracorn", defaultLocale); !contains(got, "menu") {
			t.Errorf("the published words stopped being findable: %v", got)
		}

		if err := s.Publish(ctx, p.ID); err != nil {
			t.Fatalf("Publish: %v", err)
		}
		if got := searchFor(t, s, "quinoa", defaultLocale); !contains(got, "menu") {
			t.Errorf("publishing did not index the new words: %v", got)
		}
		if got := searchFor(t, s, "zebracorn", defaultLocale); len(got) != 0 {
			t.Errorf("the replaced words are still in the index: %v", got)
		}
	})
}

// A rename is not staged — it takes effect at once — so the address in the
// index has to move with it, or every result would point at a 404.
func TestSearchFollowsARename(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := content.NewStore(db, defaultLocale)
		p := seedPage(t, s, content.Page{Slug: "old-address", Title: "Findable"}, defaultLocale)
		publishWithBody(t, s, p, "<p>zebracorn</p>")

		p.Slug = "new-address"
		if err := s.Update(ctx, p, defaultLocale); err != nil {
			t.Fatalf("Update: %v", err)
		}
		got := searchFor(t, s, "zebracorn", defaultLocale)
		if !contains(got, "new-address") {
			t.Errorf("search returned %v, want the page's new address", got)
		}
	})
}

// Posts are pages, so they are indexed like pages — but a result has to
// know it is a post, and carry the date a listing would show.
func TestSearchCarriesPostFeedAndDate(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := content.NewStore(db, defaultLocale)
		when := time.Date(2026, 3, 14, 15, 9, 0, 0, time.UTC)
		post := &content.Post{
			Page:        content.Page{Slug: "news/launch", Title: "Launch day", TemplateName: "post.gohtml"},
			Feed:        content.FeedNews,
			PublishedAt: when,
		}
		if _, err := s.InsertPost(ctx, post, defaultLocale); err != nil {
			t.Fatalf("InsertPost: %v", err)
		}
		publishWithBody(t, s, &post.Page, "<p>the zebracorn has landed</p>")

		results, err := s.Search(ctx, content.ParseSearchQuery("zebracorn"), defaultLocale, 0, 0)
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		if len(results) != 1 {
			t.Fatalf("Search returned %d results, want 1", len(results))
		}
		r := results[0]
		if r.Kind != "news" {
			t.Errorf("result kind = %q, want %q", r.Kind, "news")
		}
		if r.PublishedAt == nil || !r.PublishedAt.Equal(when) {
			t.Errorf("result date = %v, want %v", r.PublishedAt, when)
		}
	})
}

// A hit in the title has to beat a hit buried in the body, or the page
// about a thing loses to every page that mentions it.
func TestSearchRanksTitleMatchesFirst(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := content.NewStore(db, defaultLocale)

		mention := seedPage(t, s, content.Page{Slug: "mention", Title: "Something else"}, defaultLocale)
		publishWithBody(t, s, mention,
			"<p>"+strings.Repeat("filler words here. ", 40)+" zebracorn "+
				strings.Repeat("more filler. ", 40)+"</p>")

		about := seedPage(t, s, content.Page{Slug: "about-it", Title: "Zebracorn"}, defaultLocale)
		publishWithBody(t, s, about, "<p>All about it.</p>")

		got := searchFor(t, s, "zebracorn", defaultLocale)
		if len(got) != 2 {
			t.Fatalf("search returned %v, want both pages", got)
		}
		if got[0] != "about-it" {
			t.Errorf("search returned %v, want the titled page first", got)
		}
	})
}

// Both engines have to agree about what a query means, which is the whole
// reason queries are parsed rather than passed through.
func TestSearchQueryGrammarMeansTheSameOnEveryEngine(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := content.NewStore(db, defaultLocale)

		both := seedPage(t, s, content.Page{Slug: "both", Title: "Both"}, defaultLocale)
		publishWithBody(t, s, both, "<p>zebracorn and quinoa together</p>")
		one := seedPage(t, s, content.Page{Slug: "one", Title: "One"}, defaultLocale)
		publishWithBody(t, s, one, "<p>zebracorn alone</p>")

		// Words are AND-ed.
		if got := searchFor(t, s, "zebracorn quinoa", defaultLocale); len(got) != 1 || got[0] != "both" {
			t.Errorf("two words returned %v, want only the page holding both", got)
		}
		// A minus excludes.
		if got := searchFor(t, s, "zebracorn -quinoa", defaultLocale); len(got) != 1 || got[0] != "one" {
			t.Errorf("an exclusion returned %v, want only the page without it", got)
		}
		// A phrase must appear intact.
		if got := searchFor(t, s, `"zebracorn and quinoa"`, defaultLocale); len(got) != 1 || got[0] != "both" {
			t.Errorf("a phrase returned %v, want only the page holding it", got)
		}
		// Junk is not a syntax error, it is a search that finds nothing.
		for _, junk := range []string{"((( ***", `"unclosed`, "+++", "~@#"} {
			if _, err := s.Search(context.Background(),
				content.ParseSearchQuery(junk), defaultLocale, 10, 0); err != nil {
				t.Errorf("Search(%q) returned an error: %v", junk, err)
			}
		}
	})
}

// A term too short for the engine's index has to be found anyway. On
// Postgres it is in the index; on MySQL nothing under
// innodb_ft_min_token_size ever is, and the store matches it with LIKE
// instead. Neither is allowed to come back empty.
func TestSearchFindsShortWords(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := content.NewStore(db, defaultLocale)
		p := seedPage(t, s, content.Page{Slug: "ai", Title: "Our AI work"}, defaultLocale)
		publishWithBody(t, s, p, "<p>We do 3D printing too.</p>")
		other := seedPage(t, s, content.Page{Slug: "other", Title: "Something else"}, defaultLocale)
		publishWithBody(t, s, other, "<p>Nothing relevant on this page.</p>")

		if got := searchFor(t, s, "AI", defaultLocale); !contains(got, "ai") {
			t.Errorf("searching a two-letter word returned %v, want the AI page", got)
		}
		if got := searchFor(t, s, "3D printing", defaultLocale); !contains(got, "ai") {
			t.Errorf("a short word beside a long one returned %v, want the AI page", got)
		}
	})
}

// The pager needs a count that describes the results it is paging, and a
// window that never repeats or skips a result between pages.
func TestSearchPaginatesWithoutRepeatingOrSkipping(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := content.NewStore(db, defaultLocale)
		const n = 7
		for i := range n {
			p := seedPage(t, s, content.Page{
				Slug:  "p" + string(rune('a'+i)),
				Title: "Zebracorn " + string(rune('a'+i)),
			}, defaultLocale)
			publishWithBody(t, s, p, "<p>zebracorn</p>")
		}

		terms := content.ParseSearchQuery("zebracorn")
		total, err := s.CountSearch(ctx, terms, defaultLocale)
		if err != nil {
			t.Fatalf("CountSearch: %v", err)
		}
		if total != n {
			t.Fatalf("CountSearch = %d, want %d", total, n)
		}

		seen := map[string]bool{}
		for offset := 0; offset < total; offset += 3 {
			page, err := s.Search(ctx, terms, defaultLocale, 3, offset)
			if err != nil {
				t.Fatalf("Search(offset %d): %v", offset, err)
			}
			for _, r := range page {
				if seen[r.Slug] {
					t.Errorf("%q appeared on two pages", r.Slug)
				}
				seen[r.Slug] = true
			}
		}
		if len(seen) != n {
			t.Errorf("paging saw %d of %d results", len(seen), n)
		}
	})
}

// A multilingual site indexes each locale, and a locale with no
// translation falls back to the default one's words — which is what the
// visitor is actually shown.
func TestSearchIsPerLocaleWithFallback(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := content.NewStore(db, defaultLocale)
		s.SetLocales([]string{defaultLocale, "fr"})

		p := seedPage(t, s, content.Page{Slug: "about", Title: "About"}, defaultLocale)
		if err := s.UpsertDraftBlock(ctx, p.ID, "main", defaultLocale,
			content.KindHTML, "<p>zebracorn</p>"); err != nil {
			t.Fatalf("UpsertDraftBlock(en): %v", err)
		}
		if err := s.UpsertDraftBlock(ctx, p.ID, "intro", "fr",
			content.KindHTML, "<p>licorne</p>"); err != nil {
			t.Fatalf("UpsertDraftBlock(fr): %v", err)
		}
		if err := s.Publish(ctx, p.ID); err != nil {
			t.Fatalf("Publish: %v", err)
		}

		// The French translation is findable in French.
		if got := searchFor(t, s, "licorne", "fr"); !contains(got, "about") {
			t.Errorf("the French words were not indexed for fr: %v", got)
		}
		// And so is the region that was never translated: fallback means
		// the French visitor sees those English words on the page.
		if got := searchFor(t, s, "zebracorn", "fr"); !contains(got, "about") {
			t.Errorf("untranslated content is not findable in fr: %v", got)
		}
		// The French words are not in the English index — an English
		// visitor never sees them.
		if got := searchFor(t, s, "licorne", defaultLocale); len(got) != 0 {
			t.Errorf("French-only content is findable in English: %v", got)
		}
	})
}

// ReindexAll has to arrive at exactly the state the incremental path
// maintains — otherwise a rebuild is a change, and running it would be
// something to be careful about rather than something safe.
func TestReindexAllRebuildsWhatPublishingBuilt(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := content.NewStore(db, defaultLocale)

		live := seedPage(t, s, content.Page{Slug: "live", Title: "Live"}, defaultLocale)
		publishWithBody(t, s, live, "<p>zebracorn</p>")
		hidden := seedPage(t, s, content.Page{Slug: "hidden", Title: "Hidden"}, defaultLocale)
		publishWithBody(t, s, hidden, "<p>zebracorn</p>")
		if err := s.Unpublish(ctx, hidden.ID); err != nil {
			t.Fatalf("Unpublish: %v", err)
		}

		before := searchFor(t, s, "zebracorn", defaultLocale)
		n, err := s.ReindexAll(ctx)
		if err != nil {
			t.Fatalf("ReindexAll: %v", err)
		}
		if n == 0 {
			t.Error("ReindexAll visited no pages")
		}
		after := searchFor(t, s, "zebracorn", defaultLocale)
		if len(before) != len(after) {
			t.Fatalf("rebuild changed the results: %v -> %v", before, after)
		}
		for i := range before {
			if before[i] != after[i] {
				t.Errorf("rebuild changed the results: %v -> %v", before, after)
				break
			}
		}
		if contains(after, "hidden") {
			t.Errorf("a rebuild put an unpublished page back into the index: %v", after)
		}

		// And it is idempotent: running it twice is running it once.
		if _, err := s.ReindexAll(ctx); err != nil {
			t.Fatalf("ReindexAll (second run): %v", err)
		}
		if again := searchFor(t, s, "zebracorn", defaultLocale); len(again) != len(after) {
			t.Errorf("a second rebuild changed the results: %v -> %v", after, again)
		}
	})
}

func TestSearchIndexEmptyReportsAnUnbuiltIndex(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := content.NewStore(db, defaultLocale)
		empty, err := s.SearchIndexEmpty(ctx)
		if err != nil {
			t.Fatalf("SearchIndexEmpty: %v", err)
		}
		if !empty {
			t.Error("a fresh database reports a built search index")
		}
		p := seedPage(t, s, content.Page{Slug: "about", Title: "About"}, defaultLocale)
		publishWithBody(t, s, p, "<p>zebracorn</p>")
		if empty, err = s.SearchIndexEmpty(ctx); err != nil || empty {
			t.Errorf("SearchIndexEmpty = %v, %v after a publish; want false, nil", empty, err)
		}
	})
}
