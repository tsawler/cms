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

// What the site lock does at the door: everything answers 503 except the
// admin, the addresses the host named exempt, and whatever a superadmin
// asks for.

// setLocked closes or opens the site and defeats the settings cache,
// which would otherwise hold the previous answer for a few seconds.
func setLocked(t *testing.T, c *CMS, on bool) {
	t.Helper()
	if err := c.content.SetSiteLocked(context.Background(), on); err != nil {
		t.Fatalf("SetSiteLocked(%v): %v", on, err)
	}
	expireSiteCache(c)
}

// sessionFor mints a live session for the user and returns the cookie a
// request carries it in — the same thing a browser would hold after
// logging in, without going through the login form.
func sessionFor(t *testing.T, c *CMS, userID int64) *http.Cookie {
	t.Helper()
	ctx, err := c.sessions.Load(context.Background(), "")
	if err != nil {
		t.Fatalf("session Load: %v", err)
	}
	c.sessions.Put(ctx, sessiondata.KeyUserID, userID)
	token, _, err := c.sessions.Commit(ctx)
	if err != nil {
		t.Fatalf("session Commit: %v", err)
	}
	return &http.Cookie{Name: c.sessions.Cookie.Name, Value: token}
}

// insertUser adds an account with the given role, active and with no
// usable password — nothing here logs in through the form.
func insertUser(t *testing.T, c *CMS, email string, role auth.Role) int64 {
	t.Helper()
	u := &auth.User{Email: email, Name: email, PasswordHash: "x", Role: role, Active: true}
	id, err := c.users.Insert(context.Background(), u)
	if err != nil {
		t.Fatalf("Insert(%s): %v", email, err)
	}
	return id
}

func TestLockdown(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		c := newSeedTestCMS(t, db)
		if err := c.Migrate(ctx); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		superID := insertUser(t, c, "super@example.com", auth.RoleSuperadmin)
		editorID := insertUser(t, c, "editor@example.com", auth.RoleEditor)
		superCookie := sessionFor(t, c, superID)
		editorCookie := sessionFor(t, c, editorID)

		// The handler underneath says so, so "served" and "refused" are
		// told apart by the body rather than by the status alone.
		const served = "the site"
		next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(served))
		})
		h := c.Lockdown(next, "/healthz", "/api/feed.xml", "/vehicle-media/")

		get := func(path string, cookie *http.Cookie) *httptest.ResponseRecorder {
			r := httptest.NewRequest(http.MethodGet, path, nil)
			if cookie != nil {
				r.AddCookie(cookie)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, r)
			return rec
		}
		reached := func(rec *httptest.ResponseRecorder) bool { return rec.Body.String() == served }

		// Open: everything through, and nothing added to the response.
		setLocked(t, c, false)
		for _, path := range []string{"/", "/inventory", "/admin/", "/healthz"} {
			rec := get(path, nil)
			if !reached(rec) {
				t.Errorf("open: GET %s did not reach the site (status %d)", path, rec.Code)
			}
			if got := rec.Header().Get("Retry-After"); got != "" {
				t.Errorf("open: GET %s set Retry-After %q on an open site", path, got)
			}
		}

		setLocked(t, c, true)

		// Closed, and nobody signed in: refused, and told it is
		// temporary rather than gone.
		rec := get("/inventory", nil)
		if reached(rec) {
			t.Error("locked: an anonymous visitor reached the site")
		}
		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("locked: status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
		}
		if got := rec.Header().Get("Retry-After"); got != lockedRetryAfter {
			t.Errorf("locked: Retry-After = %q, want %q", got, lockedRetryAfter)
		}
		if got := rec.Header().Get("X-Robots-Tag"); got != robotsDirective {
			t.Errorf("locked: X-Robots-Tag = %q, want %q", got, robotsDirective)
		}
		if got := rec.Header().Get("Cache-Control"); got != "no-store" {
			t.Errorf("locked: Cache-Control = %q, want %q", got, "no-store")
		}

		// The admin stays reachable whoever is asking — it is where the
		// lock is lifted, and its own middleware decides who gets in.
		for _, path := range []string{"/admin", "/admin/", "/admin/login", "/admin/static/admin.css"} {
			if !reached(get(path, nil)) {
				t.Errorf("locked: GET %s did not reach the admin", path)
			}
		}
		// A path that merely starts with the admin's name is not the
		// admin: /adminish is a page like any other.
		if reached(get("/adminish", nil)) {
			t.Error("locked: /adminish was let through as the admin")
		}

		// The exempt list: an exact address matches exactly, and a
		// prefix entry (trailing slash) matches what is under it —
		// while a sibling that merely starts with the same letters does
		// not.
		for _, path := range []string{"/healthz", "/api/feed.xml", "/vehicle-media/1/front.webp"} {
			if !reached(get(path, nil)) {
				t.Errorf("locked: exempt GET %s was refused", path)
			}
		}
		for _, path := range []string{"/healthz-internal", "/api/feed.xml.bak", "/vehicle-media"} {
			if reached(get(path, nil)) {
				t.Errorf("locked: GET %s was let through by an exempt entry it only resembles", path)
			}
		}

		// Who the session belongs to is the whole question.
		if !reached(get("/inventory", superCookie)) {
			t.Error("locked: a superadmin was refused their own site")
		}
		if reached(get("/inventory", editorCookie)) {
			t.Error("locked: an editor reached a closed site")
		}
		// A cookie carrying a token no store has ever seen is nobody.
		if reached(get("/inventory", &http.Cookie{Name: c.sessions.Cookie.Name, Value: "not-a-token"})) {
			t.Error("locked: a forged session cookie reached the site")
		}
		// Nor does a deactivated superadmin keep the keys.
		u, err := c.users.GetByID(ctx, superID)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		u.Active = false
		if err := c.users.Update(ctx, u); err != nil {
			t.Fatalf("Update: %v", err)
		}
		if reached(get("/inventory", superCookie)) {
			t.Error("locked: a deactivated superadmin reached the site")
		}
	})
}

// The override wins over the stored switch in both directions — the way
// back into a site locked by somebody who then lost the password, and
// the way to bring one up closed.
func TestLockdownOverride(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		c := newSeedTestCMS(t, db)
		if err := c.Migrate(ctx); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		setLocked(t, c, true)
		if !c.SiteLocked(ctx) {
			t.Fatal("stored lock: SiteLocked = false, want true")
		}
		open := false
		c.cfg.LockOverride = &open
		if c.SiteLocked(ctx) {
			t.Error("override false over a stored lock: SiteLocked = true, want false")
		}
		setLocked(t, c, false)
		shut := true
		c.cfg.LockOverride = &shut
		if !c.SiteLocked(ctx) {
			t.Error("override true over an open site: SiteLocked = false, want true")
		}
	})
}
