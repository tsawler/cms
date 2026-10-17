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

// The public side reads the admin's session too — sessionUser for the
// in-place editor, superadminContext for the site lock's bypass — and
// both must drop a session a credential change has ended, as the admin
// does.
func TestPublicSessionChecksFollowTheEpoch(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		c := newTestCMS(t, db)
		defer c.Close()
		ctx := context.Background()
		users := auth.NewStore(db)
		u := &auth.User{Email: "super@example.com", Name: "Sue", PasswordHash: "x", Role: auth.RoleSuperadmin, Active: true}
		if _, err := users.Insert(ctx, u); err != nil {
			t.Fatal(err)
		}

		sctx, err := c.sessions.Load(ctx, "")
		if err != nil {
			t.Fatal(err)
		}
		c.sessions.Put(sctx, sessiondata.KeyUserID, u.ID)
		c.sessions.Put(sctx, sessiondata.KeyEpoch, u.SessionEpoch)
		token, _, err := c.sessions.Commit(sctx)
		if err != nil {
			t.Fatal(err)
		}
		request := func() *http.Request {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r.AddCookie(&http.Cookie{Name: c.sessions.Cookie.Name, Value: token})
			return r
		}

		if c.sessionUser(request().WithContext(sctx)) == nil {
			t.Fatal("sessionUser refused a current session")
		}
		if _, ok := c.superadminContext(request()); !ok {
			t.Fatal("superadminContext refused a current session")
		}

		if err := users.UpdatePassword(ctx, u.ID, "new-hash"); err != nil {
			t.Fatal(err)
		}
		if c.sessionUser(request().WithContext(sctx)) != nil {
			t.Error("sessionUser honoured a session the password change ended")
		}
		if _, ok := c.superadminContext(request()); ok {
			t.Error("superadminContext honoured a session the password change ended")
		}
	})
}
