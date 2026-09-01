package admin

// The users list's filters and paging, end to end against a real database:
// active accounts by default, the inactive and all tabs, a name/email
// search, a server-side answer to junk in the query string, and a pager
// whose links keep the filters they were built under.

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alexedwards/scs/v2"
	"github.com/tsawler/cms/auth"
	"github.com/tsawler/cms/internal/dbtest"
	"github.com/tsawler/cms/internal/sqldb"
)

// usersListTestServer is settingsTestServer with a configurable page
// size, so the paging tests fill pages with a handful of rows instead of
// twenty-six.
func usersListTestServer(t *testing.T, db *sqldb.DB, perPage int) (*httptest.Server, *auth.Store) {
	t.Helper()
	users := auth.NewStore(db)
	h := New(Deps{
		Sessions:  scs.New(),
		Users:     users,
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		AdminPath: "/admin",
		PerPage:   perPage,
	})
	mux := http.NewServeMux()
	mux.Handle("/admin/", http.StripPrefix("/admin", h))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, users
}

// deactivate flips an account off, the state the inactive tab lists.
func deactivate(t *testing.T, users *auth.Store, u *auth.User) {
	t.Helper()
	u.Active = false
	if err := users.Update(context.Background(), u); err != nil {
		t.Fatalf("deactivating %s: %v", u.Email, err)
	}
}

func TestUsersListFilters(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		srv, users := usersListTestServer(t, db, 0)
		seedPermUser(t, users, "super@example.com", auth.RoleSuperadmin)
		seedPermUser(t, users, "amy@example.com", auth.RoleEditor)
		deactivate(t, users, seedPermUser(t, users, "gone@example.com", auth.RoleEditor))

		client := newClient(t)
		logIn(t, srv, client, "super@example.com", "password123")

		// The topbar names the logged-in superadmin on every page, so
		// presence and absence are only ever asserted on the two editors.
		cases := []struct {
			name     string
			path     string
			wantAmy  bool
			wantGone bool
		}{
			{"the default view is active accounts", "/admin/users", true, false},
			{"the inactive tab", "/admin/users?status=inactive", false, true},
			{"the all tab", "/admin/users?status=all", true, true},
			{"junk status is the default view", "/admin/users?status=banana", true, false},
			{"search matches a name fragment, whatever the case", "/admin/users?status=all&q=AMY", true, false},
			{"search matches email too", "/admin/users?status=all&q=gone%40example", false, true},
			{"search respects the status tab", "/admin/users?status=inactive&q=amy", false, false},
		}
		for _, c := range cases {
			resp, page := getPage(t, srv, client, c.path)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("%s: status = %d, want 200", c.name, resp.StatusCode)
			}
			if got := strings.Contains(page, "amy@example.com"); got != c.wantAmy {
				t.Errorf("%s: amy listed = %v, want %v", c.name, got, c.wantAmy)
			}
			if got := strings.Contains(page, "gone@example.com"); got != c.wantGone {
				t.Errorf("%s: gone listed = %v, want %v", c.name, got, c.wantGone)
			}
		}

		// A search nothing matches says so, in the filtered voice rather
		// than the empty-table one.
		if _, page := getPage(t, srv, client, "/admin/users?q=zebra"); !strings.Contains(page, "No users match this filter.") {
			t.Errorf("empty search result missing its message:\n%s", page)
		}
	})
}

func TestUsersListPaging(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		srv, users := usersListTestServer(t, db, 2)
		// Four active accounts at two per page. seedPermUser names each
		// "Test <email>", so name order is email order: amy, bea, cleo,
		// super — amy and bea on page one, cleo and super on page two.
		seedPermUser(t, users, "super@example.com", auth.RoleSuperadmin)
		seedPermUser(t, users, "amy@example.com", auth.RoleEditor)
		seedPermUser(t, users, "bea@example.com", auth.RoleEditor)
		seedPermUser(t, users, "cleo@example.com", auth.RoleEditor)

		client := newClient(t)
		logIn(t, srv, client, "super@example.com", "password123")

		resp, page := getPage(t, srv, client, "/admin/users")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("page one: status = %d, want 200", resp.StatusCode)
		}
		if !strings.Contains(page, "amy@example.com") || !strings.Contains(page, "bea@example.com") {
			t.Errorf("page one missing its rows:\n%s", page)
		}
		if strings.Contains(page, "cleo@example.com") {
			t.Errorf("page one shows a page-two row:\n%s", page)
		}
		if !strings.Contains(page, "page=2") {
			t.Errorf("page one offers no link to page two:\n%s", page)
		}

		_, page = getPage(t, srv, client, "/admin/users?page=2")
		if !strings.Contains(page, "cleo@example.com") || strings.Contains(page, "amy@example.com") {
			t.Errorf("page two shows the wrong window:\n%s", page)
		}

		// A page number past the end is clamped to the last real page,
		// not answered with an empty table.
		_, page = getPage(t, srv, client, "/admin/users?page=99")
		if !strings.Contains(page, "cleo@example.com") || strings.Contains(page, "amy@example.com") {
			t.Errorf("out-of-range page did not clamp to the last page:\n%s", page)
		}

		// The pager's links carry the active filters, so paging never
		// silently drops a tab or a search. Page one of a filtered list
		// keeps the filter and sheds only the page number.
		_, page = getPage(t, srv, client, "/admin/users?status=all&page=2")
		if !strings.Contains(page, `href="/admin/users?status=all"`) {
			t.Errorf("page two of the all tab links page one without its filter:\n%s", page)
		}
	})
}
