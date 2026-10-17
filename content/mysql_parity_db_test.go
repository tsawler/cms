package content_test

import (
	"context"
	"errors"
	"testing"

	"github.com/tsawler/cms/content"
	"github.com/tsawler/cms/internal/dbtest"
	"github.com/tsawler/cms/internal/sqldb"
)

// Where MySQL's defaults differ from Postgres's, the CMS has to make them
// agree: these are the differences that once showed. Each runs on every
// engine and expects the Postgres answer.

// A page is found at its slug exactly: /About is not /about.
func TestSlugLookupIsExact(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := content.NewStore(db, defaultLocale)
		seedPage(t, s, content.Page{Slug: "about", Title: "About"}, defaultLocale)

		if _, err := s.GetBySlug(ctx, "about", defaultLocale, false); err != nil {
			t.Fatalf("GetBySlug(about): %v", err)
		}
		for _, slug := range []string{"About", "ABOUT", "abóut"} {
			if _, err := s.GetBySlug(ctx, slug, defaultLocale, false); !errors.Is(err, content.ErrNotFound) {
				t.Errorf("GetBySlug(%q) = %v, want ErrNotFound", slug, err)
			}
		}
		// And the unique index still refuses the same slug twice.
		dup := content.Page{Slug: "about", Title: "Again", TemplateName: "page.gohtml"}
		if _, err := s.Insert(ctx, &dup, defaultLocale); !errors.Is(err, content.ErrDuplicateSlug) {
			t.Errorf("a second page at the same slug: err = %v, want ErrDuplicateSlug", err)
		}
	})
}

// An edit that only changes case or accents is an edit: the page shows
// unpublished changes, in content, metadata and page code alike.
func TestCaseAndAccentEditsAreUnpublishedChanges(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := content.NewStore(db, defaultLocale)

		edits := map[string]func(p *content.Page) error{
			"content case": func(p *content.Page) error {
				return s.UpsertDraftBlock(ctx, p.ID, "main", defaultLocale, content.KindHTML, "<p>Buy an iPhone</p>")
			},
			"content accent": func(p *content.Page) error {
				return s.UpsertDraftBlock(ctx, p.ID, "main", defaultLocale, content.KindHTML, "<p>Buy an Iphoné</p>")
			},
			"title case": func(p *content.Page) error {
				p.Title = "PHONES"
				return s.Update(ctx, p, defaultLocale)
			},
			"page code case": func(p *content.Page) error {
				p.BodyJS = "Track()"
				return s.Update(ctx, p, defaultLocale)
			},
		}
		for name, edit := range edits {
			p := seedPage(t, s, content.Page{Slug: "phones-" + slugOf(name), Title: "Phones", BodyJS: "track()"}, defaultLocale)
			if err := s.UpsertDraftBlock(ctx, p.ID, "main", defaultLocale, content.KindHTML, "<p>Buy an Iphone</p>"); err != nil {
				t.Fatal(err)
			}
			if err := s.Publish(ctx, p.ID); err != nil {
				t.Fatal(err)
			}
			if changed, err := s.HasUnpublishedChanges(ctx, p.ID); err != nil || changed {
				t.Fatalf("%s: freshly published page reports changes (%v, %v)", name, changed, err)
			}
			got, err := s.GetByID(ctx, p.ID, defaultLocale)
			if err != nil {
				t.Fatal(err)
			}
			if err := edit(got); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			if changed, err := s.HasUnpublishedChanges(ctx, p.ID); err != nil || !changed {
				t.Errorf("%s: HasUnpublishedChanges = %v (err %v), want true", name, changed, err)
			}
		}
	})
}

func slugOf(s string) string {
	out := []byte(s)
	for i, c := range out {
		if c == ' ' {
			out[i] = '-'
		}
	}
	return string(out)
}

// A hyphenated word is one search term: "well-known" finds the page that
// says it, and does not exclude a page for saying "known".
func TestSearchForAHyphenatedWord(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := content.NewStore(db, defaultLocale)
		fact := seedPage(t, s, content.Page{Slug: "fact", Title: "Fact"}, defaultLocale)
		publishWithBody(t, s, fact, "<p>It is a well-known remedy for hiccups.</p>")
		other := seedPage(t, s, content.Page{Slug: "other", Title: "Other"}, defaultLocale)
		publishWithBody(t, s, other, "<p>These are the remedies we have.</p>")

		if got := searchFor(t, s, "well-known", defaultLocale); !contains(got, "fact") {
			t.Errorf("searching well-known found %v, want the page that says it", got)
		}
	})
}
