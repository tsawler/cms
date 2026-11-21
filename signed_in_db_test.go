package cms

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tsawler/cms/auth"
	"github.com/tsawler/cms/internal/dbtest"
	"github.com/tsawler/cms/internal/sessiondata"
	"github.com/tsawler/cms/internal/sqldb"
)

// SignedIn is what a host shows a sign-in link or a staff menu by, so the
// case that matters most is the browser that has just logged out: it
// still sends a session cookie — the login page it was sent to started a
// new session for its CSRF token — and must read as signed out.
func TestSignedIn(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		c := newSeedTestCMS(t, db)
		defer c.Close()
		if err := c.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
		readerID := insertUser(t, c, "reader@example.com", auth.RoleReader)

		// A session with no user in it: what logging out leaves behind.
		anon, err := c.sessions.Load(ctx, "")
		if err != nil {
			t.Fatal(err)
		}
		c.sessions.Put(anon, sessiondata.KeyCSRF, "token")
		anonToken, _, err := c.sessions.Commit(anon)
		if err != nil {
			t.Fatal(err)
		}

		signedIn := func(cookie *http.Cookie) bool {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			if cookie != nil {
				r.AddCookie(cookie)
			}
			return c.SignedIn(r)
		}
		name := c.sessions.Cookie.Name
		reader := sessionFor(t, c, readerID)

		if signedIn(nil) {
			t.Error("no cookie reads as signed in")
		}
		if signedIn(&http.Cookie{Name: name, Value: "not-a-session"}) {
			t.Error("an unknown session token reads as signed in")
		}
		if signedIn(&http.Cookie{Name: name, Value: anonToken}) {
			t.Error("a session with no user — a logged-out browser — reads as signed in")
		}
		if !signedIn(reader) {
			t.Error("a reader's session reads as signed out")
		}

		if _, err := db.SQL().ExecContext(ctx,
			"UPDATE cms_users SET active = false WHERE email = 'reader@example.com'"); err != nil {
			t.Fatal(err)
		}
		if signedIn(reader) {
			t.Error("a deactivated account's session reads as signed in")
		}
	})
}
