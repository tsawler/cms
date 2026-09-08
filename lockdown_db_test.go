package cms

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tsawler/cms/auth"
	"github.com/tsawler/cms/content"
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

// The switch is in the product's own UI, so it cannot depend on the host
// having remembered to mount Lockdown in main(). Everything the CMS
// serves itself — pages, the sitemap, robots.txt, proxied media, the
// editor bundle — has to close on its own account.
//
// This is #4: before it, a superadmin could throw the switch, watch the
// admin lock down around them, and the public site would carry on serving
// with nothing anywhere to say why.
func TestPagesEnforceTheLockWithoutLockdown(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		c := newSeedTestCMS(t, db)
		if err := c.Migrate(ctx); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		if _, err := c.SeedHomePage(ctx, "templates/pages/canvas.gohtml", "Welcome"); err != nil {
			t.Fatalf("SeedHomePage: %v", err)
		}
		superID := insertUser(t, c, "super@example.com", auth.RoleSuperadmin)
		editorID := insertUser(t, c, "editor@example.com", auth.RoleEditor)
		superCookie := sessionFor(t, c, superID)
		editorCookie := sessionFor(t, c, editorID)

		// No Lockdown anywhere: this is Pages exactly as a host that
		// never read docs/production.md would mount it.
		h := c.Pages()
		get := func(path string, cookie *http.Cookie) *httptest.ResponseRecorder {
			r := httptest.NewRequest(http.MethodGet, path, nil)
			if cookie != nil {
				r.AddCookie(cookie)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, r)
			return rec
		}

		setLocked(t, c, false)
		if rec := get("/", nil); rec.Code != http.StatusOK {
			t.Fatalf("open: GET / status = %d, want 200", rec.Code)
		}

		setLocked(t, c, true)

		// Every address the CMS answers at, closed to the public, with
		// the same 503 the outer door gives.
		for _, path := range []string{"/", "/robots.txt", "/sitemap.xml", "/cms/media/abc/web.webp"} {
			rec := get(path, nil)
			if rec.Code != http.StatusServiceUnavailable {
				t.Errorf("locked: GET %s status = %d, want 503", path, rec.Code)
			}
			if got := rec.Header().Get("Retry-After"); got != lockedRetryAfter {
				t.Errorf("locked: GET %s Retry-After = %q, want %q", path, got, lockedRetryAfter)
			}
			if got := rec.Header().Get("Cache-Control"); got != "no-store" {
				t.Errorf("locked: GET %s Cache-Control = %q, want no-store", path, got)
			}
		}

		// The people it is not for.
		if rec := get("/", editorCookie); rec.Code != http.StatusServiceUnavailable {
			t.Errorf("locked: an editor got status %d from the public site, want 503", rec.Code)
		}
		// And the one it is: a superadmin still sees the site, which is
		// how the lock gets lifted from the editor's own settings dialog.
		if rec := get("/", superCookie); rec.Code != http.StatusOK {
			t.Errorf("locked: a superadmin got status %d from their own site, want 200", rec.Code)
		}
	})
}

// Handler is Pages plus the admin, so it inherits the lock — and the
// admin has to stay reachable through it, or there is no way back in.
func TestHandlerEnforcesTheLockAndKeepsTheAdminOpen(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		c := newSeedTestCMS(t, db)
		if err := c.Migrate(ctx); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		if _, err := c.SeedHomePage(ctx, "templates/pages/canvas.gohtml", "Welcome"); err != nil {
			t.Fatalf("SeedHomePage: %v", err)
		}
		setLocked(t, c, true)

		h := c.Handler()
		get := func(path string) *httptest.ResponseRecorder {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
			return rec
		}

		if rec := get("/"); rec.Code != http.StatusServiceUnavailable {
			t.Errorf("locked: GET / status = %d, want 503", rec.Code)
		}
		// The login page answers — refusing everyone but superadmins is
		// the admin's own job, and it does it with a page, not a 503.
		if rec := get("/admin/login"); rec.Code == http.StatusServiceUnavailable {
			t.Error("locked: the admin login page was refused by the site lock")
		}
	})
}

// Lockdown's exempt list has to survive the trip. Pages enforces the lock
// too, so without the mark Lockdown puts on a request it passes, an
// address the host exempted — a health check, a partner's feed — would
// sail through the outer door and be refused by the inner one.
func TestLockdownExemptSurvivesPagesEnforcement(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		c := newSeedTestCMS(t, db)
		if err := c.Migrate(ctx); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		if _, err := c.SeedHomePage(ctx, "templates/pages/canvas.gohtml", "Welcome"); err != nil {
			t.Fatalf("SeedHomePage: %v", err)
		}
		// A second published page, so the exempt entry can name one
		// address without naming everything. ("/" would not do: the
		// exempt rule treats a trailing slash as a prefix, so "/" matches
		// the whole site.)
		id, err := c.content.Insert(ctx, &content.Page{
			Slug: "status", Title: "Status", TemplateName: "templates/pages/canvas.gohtml",
		}, "en")
		if err != nil {
			t.Fatalf("Insert: %v", err)
		}
		if err := c.content.Publish(ctx, id); err != nil {
			t.Fatalf("Publish: %v", err)
		}
		setLocked(t, c, true)

		h := c.Lockdown(c.Pages(), "/status")
		get := func(path string) int {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
			return rec.Code
		}

		if code := get("/status"); code != http.StatusOK {
			t.Errorf("an exempt CMS address got status %d, want 200 — the inner "+
				"check overruled the exempt list", code)
		}
		// And the mark is not simply switching the lock off for
		// everything behind the wrapper.
		if code := get("/"); code != http.StatusServiceUnavailable {
			t.Errorf("a non-exempt address got status %d, want 503", code)
		}
	})
}
