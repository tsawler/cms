package cms

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tsawler/cms/auth"
	"github.com/tsawler/cms/content"
	"github.com/tsawler/cms/internal/dbtest"
	"github.com/tsawler/cms/internal/sqldb"
)

// Page views are counted in memory and land in the database in batches,
// under the page's own address: serving a page writes nothing, /open and
// /open/ are one page, staff and announced crawlers are not traffic, a
// flush that fails keeps its counts for the next one, and Close flushes
// what is left.
func TestPageViewsAreBatchedUnderThePagesAddress(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		c := newSeedTestCMS(t, db)
		defer c.Close()
		if err := c.Migrate(ctx); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		p := &content.Page{Slug: "open", Title: "Open", TemplateName: "templates/pages/standard.gohtml"}
		if _, err := c.content.Insert(ctx, p, "en"); err != nil {
			t.Fatalf("Insert: %v", err)
		}
		if err := c.content.Publish(ctx, p.ID); err != nil {
			t.Fatalf("Publish: %v", err)
		}
		staff := sessionFor(t, c, insertUser(t, c, "staff@example.com", auth.RoleEditor))

		h := c.Handler()
		visit := func(path, ua string, cookie *http.Cookie) {
			t.Helper()
			r := httptest.NewRequest(http.MethodGet, path, nil)
			r.Header.Set("User-Agent", ua)
			if cookie != nil {
				r.AddCookie(cookie)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, r)
			if rec.Code != http.StatusOK {
				t.Fatalf("GET %s: status %d", path, rec.Code)
			}
		}
		const browser = "Mozilla/5.0 (Macintosh) Safari/605.1.15"
		visit("/open", browser, nil)
		visit("/open/", browser, nil)
		visit("/open", browser, nil)
		visit("/open", "Googlebot/2.1 (+http://www.google.com/bot.html)", nil)
		visit("/open", browser, staff)

		today := time.Now().UTC()
		views := func() map[string]int {
			t.Helper()
			top, err := c.content.TopPages(ctx, today, today, 10)
			if err != nil {
				t.Fatalf("TopPages: %v", err)
			}
			out := map[string]int{}
			for _, pv := range top {
				out[pv.Path] = pv.Views
			}
			return out
		}
		if got := views(); len(got) != 0 {
			t.Errorf("serving pages wrote counts straight to the database: %v", got)
		}

		// A flush that cannot reach the database keeps the batch.
		dead, cancel := context.WithCancel(ctx)
		cancel()
		c.flushPageViews(dead)
		if got := views(); len(got) != 0 {
			t.Errorf("a failed flush wrote something: %v", got)
		}

		c.flushPageViews(ctx)
		if got := views(); got["/open"] != 3 || len(got) != 1 {
			t.Errorf("after the flush: %v, want /open: 3 and nothing else", got)
		}

		visit("/open", browser, nil)
		if err := c.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
		if got := views(); got["/open"] != 4 {
			t.Errorf("after Close: %v, want the last view flushed (/open: 4)", got)
		}
	})
}
