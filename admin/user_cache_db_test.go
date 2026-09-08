package admin

// currentUser is asked several times over one request — by the middleware
// gating the route, by the handler, by the template data, by whatever
// checks a permission on the way — and each answer used to cost two
// queries: the row, then its grants. The answer is now remembered for the
// request that asked for it.
//
// Counting queries is not possible from here (Deps.Users is a concrete
// *auth.Store, so nothing can be wrapped around it), so the memo is
// tested by what it means instead: change the stored row mid-request and
// see whether the same request notices.

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alexedwards/scs/v2"
	"github.com/tsawler/cms/auth"
	"github.com/tsawler/cms/internal/dbtest"
	"github.com/tsawler/cms/internal/sqldb"
)

// cacheTestServer is a bare server — no router, no templates — for
// calling currentUser directly.
func cacheTestServer(db *sqldb.DB) (*server, *scs.SessionManager, *auth.Store) {
	sessions := scs.New()
	users := auth.NewStore(db)
	return &server{deps: Deps{
		Sessions:  sessions,
		Users:     users,
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		AdminPath: "/admin",
	}}, sessions, users
}

// requestAs builds a request carrying a live session for id, with the
// memo installed the way withUserCache installs it.
func requestAs(t *testing.T, sessions *scs.SessionManager, id int64) *http.Request {
	t.Helper()
	ctx, err := sessions.Load(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	sessions.Put(ctx, sessionKeyUserID, id)
	ctx = context.WithValue(ctx, userCacheKey{}, &userCache{})
	return httptest.NewRequest(http.MethodGet, "/admin/", nil).WithContext(ctx)
}

func TestCurrentUserIsAnsweredOncePerRequest(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s, sessions, users := cacheTestServer(db)
		u := seedPermUser(t, users, "pat@example.com", auth.RoleEditor, auth.PermPages)

		r := requestAs(t, sessions, u.ID)
		first := s.currentUser(r)
		if first == nil || first.Email != "pat@example.com" {
			t.Fatalf("currentUser = %v, want pat", first)
		}
		if len(first.Permissions) != 1 {
			t.Fatalf("grants = %v, want the pages permission", first.Permissions)
		}

		// Change the row underneath. A second read would see this; a
		// remembered answer will not.
		u.Active = false
		if err := users.Update(ctx, u); err != nil {
			t.Fatal(err)
		}

		if again := s.currentUser(r); again != first {
			t.Errorf("currentUser asked the database again within one request: %v then %v", first, again)
		}

		// The memo is per request, though — it must not turn into a
		// process-wide cache that keeps a deactivated account signed in.
		if next := s.currentUser(requestAs(t, sessions, u.ID)); next != nil {
			t.Errorf("a later request still saw the deactivated account as %v", next)
		}
	})
}

// A nil answer is remembered too. A session naming an account that has
// been deleted is the case where re-asking is most wasteful, because
// every check in the chain asks and every one is told nothing.
func TestCurrentUserRemembersNobody(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s, sessions, users := cacheTestServer(db)
		u := seedPermUser(t, users, "gone@example.com", auth.RoleEditor)

		r := requestAs(t, sessions, u.ID)
		if err := users.Delete(ctx, u.ID); err != nil {
			t.Fatal(err)
		}
		if got := s.currentUser(r); got != nil {
			t.Fatalf("currentUser = %v, want nil for a deleted account", got)
		}

		// The nil was recorded, not merely returned. Read straight from
		// the memo, because "it answered nil again" would be true either
		// way and would prove nothing — the question is whether it went
		// back to the database to find that out.
		cache := r.Context().Value(userCacheKey{}).(*userCache)
		if !cache.loaded {
			t.Error("a nil answer was not remembered, so every later check in the chain asks again")
		}
		if cache.user != nil || cache.id != u.ID {
			t.Errorf("memo = (%d, %v), want (%d, nil)", cache.id, cache.user, u.ID)
		}
		if got := s.currentUser(r); got != nil {
			t.Errorf("currentUser = %v, want nil", got)
		}
	})
}

// The memo belongs to a session, not to a request: masquerade swaps who
// the session is signed in as mid-request, and the answer has to follow.
// Nothing renders after that swap today — masquerade, login and logout
// all redirect — but the memo should not be the reason that has to stay
// true.
func TestCurrentUserFollowsASessionSwap(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s, sessions, users := cacheTestServer(db)
		one := seedPermUser(t, users, "one@example.com", auth.RoleSuperadmin)
		two := seedPermUser(t, users, "two@example.com", auth.RoleEditor)

		r := requestAs(t, sessions, one.ID)
		if got := s.currentUser(r); got == nil || got.Email != "one@example.com" {
			t.Fatalf("currentUser = %v, want one", got)
		}

		sessions.Put(r.Context(), sessionKeyUserID, two.ID)
		got := s.currentUser(r)
		if got == nil || got.Email != "two@example.com" {
			t.Errorf("after the session changed hands, currentUser = %v, want two", got)
		}
	})
}

// And the memo is actually installed on real requests. A middleware
// nobody mounted would leave every property above true in a unit test and
// false in the product, so this drives the real chain: a host section
// (which runs inside it, behind requireUser) reads the user, the row
// changes underneath, and the second read must not notice.
func TestUserCacheIsMountedOnTheAdminChain(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		users := auth.NewStore(db)

		var first, second *auth.User
		h := New(Deps{
			Sessions:  scs.New(),
			Users:     users,
			Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
			AdminPath: "/admin",
			Sections: []Section{{
				Path: "probe",
				Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					first = UserFrom(r)
					if first != nil {
						u := *first
						u.Active = false
						if err := users.Update(r.Context(), &u); err != nil {
							t.Error(err)
						}
					}
					second = UserFrom(r)
				}),
			}},
		})
		mux := http.NewServeMux()
		mux.Handle("/admin/", http.StripPrefix("/admin", h))
		srv := httptest.NewServer(mux)
		defer srv.Close()

		u := seedPermUser(t, users, "pat@example.com", auth.RoleAdmin)
		client := newClient(t)
		logIn(t, srv, client, "pat@example.com", "password123")

		resp, err := client.Get(srv.URL + "/admin/x/probe/")
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()

		if first == nil {
			t.Fatal("the section did not see a signed-in user")
		}
		if second == nil {
			t.Fatal("the second read went back to the database and found the row deactivated — " +
				"the memo is not mounted on the admin chain")
		}
		if second != first {
			t.Errorf("two reads in one request returned different values: %v then %v", first, second)
		}
		_ = ctx
		_ = u
	})
}
