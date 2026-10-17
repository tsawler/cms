package content_test

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/tsawler/cms/content"
	"github.com/tsawler/cms/internal/dbtest"
	"github.com/tsawler/cms/internal/sqldb"
)

// concurrently runs fn from n goroutines released together, and returns
// the errors they report.
func concurrently(n int, fn func(i int) error) []error {
	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make([]error, n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs[i] = fn(i)
		}()
	}
	close(start)
	wg.Wait()
	var out []error
	for _, err := range errs {
		if err != nil {
			out = append(out, err)
		}
	}
	return out
}

// Writes that delete a page's rows and insert them again take the page's
// lock first, so two at once queue rather than collide. Without it the
// second insert of a double-clicked Publish tripped a unique key — or
// deadlocked, on MySQL — and the editor got an error.
func TestConcurrentWritesToOnePageQueue(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := content.NewStore(db, defaultLocale)
		p := seedPage(t, s, content.Page{Slug: "busy", Title: "Busy"}, defaultLocale)
		sections := make([]content.SectionInput, 6)
		for i := range sections {
			sections[i] = content.SectionInput{Content: fmt.Sprintf("<p>section %d</p>", i)}
		}
		if err := s.ReplaceDraftSections(ctx, p.ID, "main", defaultLocale, sections); err != nil {
			t.Fatalf("ReplaceDraftSections: %v", err)
		}

		const writers, rounds = 6, 4
		for round := range rounds {
			if errs := concurrently(writers, func(int) error { return s.Publish(ctx, p.ID) }); len(errs) > 0 {
				t.Fatalf("round %d: %d of %d concurrent publishes failed, first: %v", round, len(errs), writers, errs[0])
			}
			if errs := concurrently(writers, func(i int) error {
				return s.ReplaceDraftSections(ctx, p.ID, "main", defaultLocale, sections[:3+i%3])
			}); len(errs) > 0 {
				t.Fatalf("round %d: %d of %d concurrent section saves failed, first: %v", round, len(errs), writers, errs[0])
			}
		}

		// Whatever order they ran in, each left a whole region behind:
		// one block per position, never two.
		for _, status := range []content.Status{content.StatusDraft, content.StatusPublished} {
			blocks, err := s.BlocksFor(ctx, p.ID, defaultLocale, status)
			if err != nil {
				t.Fatalf("BlocksFor(%s): %v", status, err)
			}
			seen := map[int]bool{}
			for _, b := range blocks {
				if seen[b.Sort] {
					t.Errorf("%s: two blocks at position %d", status, b.Sort)
				}
				seen[b.Sort] = true
			}
		}
	})
}
