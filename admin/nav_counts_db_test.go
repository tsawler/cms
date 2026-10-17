package admin

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tsawler/cms/auth"
	"github.com/tsawler/cms/internal/dbtest"
	"github.com/tsawler/cms/internal/shortcache"
	"github.com/tsawler/cms/internal/sqldb"
)

// The sidebar's counts are one cached set shared by every admin page,
// masked per user, and dropped by any admin write so the person who made
// a change sees the count move.
func TestNavCountsAreCachedUntilAWrite(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := formServer(t, db)
		s.counts = shortcache.New[navCounts](time.Hour)
		admin := formAdmin(t, s)
		r := httptest.NewRequest(http.MethodGet, "/admin/", nil)

		before := s.navCounts(r, admin).Users
		formUser(t, s, "another@example.com", auth.RoleEditor) // behind the cache's back
		if got := s.navCounts(r, admin).Users; got != before {
			t.Errorf("users count = %d inside the TTL, want the cached %d", got, before)
		}

		// Any state-changing admin request clears it on the way out.
		var changed int
		s.deps.Changed = func() { changed++ }
		noop := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
		s.afterWrites(noop).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/admin/users", nil))
		if changed != 0 {
			t.Error("a GET counted as a write")
		}
		s.afterWrites(noop).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/admin/users/new", nil))
		if changed != 1 {
			t.Errorf("Deps.Changed called %d times for one POST, want 1", changed)
		}
		if got := s.navCounts(r, admin).Users; got != before+1 {
			t.Errorf("users count = %d after a write, want %d", got, before+1)
		}

		// The shared totals are masked for whoever is looking.
		editor := formEditor(t, s)
		if c := s.navCounts(r, editor); c.Users != 0 || c.Snippets != 0 {
			t.Errorf("an editor without the users grant sees users %d, snippets %d; want 0 and 0", c.Users, c.Snippets)
		}
	})
}
