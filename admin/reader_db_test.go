package admin

// The reader role and the login's return address, end to end against a
// real database: what a reader can reach, where a sign-in lands, and how
// user managers may hand out and take away the role.

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tsawler/cms/auth"
	"github.com/tsawler/cms/content"
	"github.com/tsawler/cms/internal/dbtest"
	"github.com/tsawler/cms/internal/sqldb"
)

// noFollow is newClient that hands back redirects instead of following
// them, so a test can read where a response points.
func noFollow(t *testing.T) *http.Client {
	c := newClient(t)
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return c
}

// signIn posts the login form, with next when it is not empty, and
// returns the response without following it.
func signIn(t *testing.T, srv *httptest.Server, client *http.Client, email, next string) *http.Response {
	t.Helper()
	form := url.Values{
		"csrf_token": {csrfFrom(t, srv, client, "/admin/login")},
		"email":      {email},
		"password":   {"password123"},
	}
	if next != "" {
		form.Set("next", next)
	}
	resp, err := client.PostForm(srv.URL+"/admin/login", form)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return resp
}

func get(t *testing.T, srv *httptest.Server, client *http.Client, path string) (*http.Response, string) {
	t.Helper()
	resp, err := client.Get(srv.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, string(body)
}

func TestReaderReachesOnlyTheirAccount(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		srv, users, store := permTestServer(t, db)
		ctx := context.Background()
		page := &content.Page{Title: "About", Slug: "about", TemplateName: "page.gohtml"}
		if _, err := store.Insert(ctx, page, "en"); err != nil {
			t.Fatal(err)
		}
		pageID := strconv.FormatInt(page.ID, 10)

		// Grants written straight to the table, around Save: a reader
		// must get nothing from them.
		seedPermUser(t, users, "reader@example.com", auth.RoleReader, auth.PermPages, auth.PermUsers)

		client := noFollow(t)
		resp := signIn(t, srv, client, "reader@example.com", "")
		if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/" {
			t.Fatalf("reader login: %d to %q, want 303 to the site", resp.StatusCode, resp.Header.Get("Location"))
		}

		// Their own account page works, and draws no staff nav.
		resp, body := get(t, srv, client, "/admin/settings")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("reader settings: %d, want 200", resp.StatusCode)
		}
		for _, staff := range []string{`href="/admin/"`, `href="/admin/users"`, `href="/admin/media"`} {
			if strings.Contains(body, staff) {
				t.Errorf("reader's account page links %s", staff)
			}
		}
		if !strings.Contains(body, "Log out") {
			t.Error("reader's account page has no way to log out")
		}
		csrf := csrfFrom(t, srv, client, "/admin/settings")

		// Every staff page sends them to the site; every write is refused.
		for _, path := range []string{"/admin/", "/admin/users", "/admin/posts", "/admin/api/pages", "/admin/api/menu?menu=main"} {
			resp, _ := get(t, srv, client, path)
			if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/" {
				t.Errorf("reader GET %s: %d to %q, want 303 to /", path, resp.StatusCode, resp.Header.Get("Location"))
			}
		}
		for _, c := range []struct{ method, path, body string }{
			{"POST", "/admin/api/pages/" + pageID + "/regions", `{"locale":"en","regions":{"main":"<p>x</p>"}}`},
			{"POST", "/admin/api/pages/" + pageID + "/publish", ""},
			{"PUT", "/admin/api/menu", `{"menu":"main","items":[]}`},
			{"POST", "/admin/api/pages", `{"title":"T","template":"page.gohtml"}`},
			{"POST", "/admin/users/new", ""},
		} {
			if got := do(t, srv, client, csrf, c.method, c.path, c.body); got != http.StatusForbidden {
				t.Errorf("reader %s %s = %d, want 403", c.method, c.path, got)
			}
		}

		// A role an account already holds stops working the moment it
		// changes: an editor made a reader is out on their next request.
		ed := seedPermUser(t, users, "ed@example.com", auth.RoleEditor, auth.PermPages)
		edClient := noFollow(t)
		signIn(t, srv, edClient, "ed@example.com", "")
		edCSRF := csrfFrom(t, srv, edClient, "/admin/settings")
		if got := do(t, srv, edClient, edCSRF, "PUT", "/admin/api/menu", `{"menu":"main","items":[]}`); got != http.StatusOK {
			t.Fatalf("editor menu save = %d, want 200", got)
		}
		ed.Role = auth.RoleReader
		if err := users.Save(ctx, auth.UserSave{User: ed}); err != nil {
			t.Fatal(err)
		}
		if got := do(t, srv, edClient, edCSRF, "PUT", "/admin/api/menu", `{"menu":"main","items":[]}`); got != http.StatusForbidden {
			t.Errorf("editor-turned-reader menu save = %d, want 403", got)
		}
	})
}

func TestLoginReturnsToNext(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		srv, users, _ := permTestServer(t, db)
		seedPermUser(t, users, "reader@example.com", auth.RoleReader)
		seedPermUser(t, users, "editor@example.com", auth.RoleEditor, auth.PermPages)

		lands := func(email, next, want string) {
			t.Helper()
			resp := signIn(t, srv, noFollow(t), email, next)
			if got := resp.Header.Get("Location"); resp.StatusCode != http.StatusSeeOther || got != want {
				t.Errorf("%s with next=%q: %d to %q, want 303 to %q", email, next, resp.StatusCode, got, want)
			}
		}
		lands("reader@example.com", "/health-and-safety?tab=forms", "/health-and-safety?tab=forms")
		lands("editor@example.com", "/health-and-safety", "/health-and-safety")
		lands("editor@example.com", "", "/admin/")
		for _, bad := range []string{"https://evil.example/", "//evil.example", "/\\evil.example", "/\t/evil.example", "javascript:alert(1)", "/admin/login"} {
			lands("reader@example.com", bad, "/")
			lands("editor@example.com", bad, "/admin/")
		}

		// The form carries a good next and drops a bad one.
		_, body := get(t, srv, newClient(t), "/admin/login?next=%2Fhealth-and-safety")
		if !strings.Contains(body, `name="next" value="/health-and-safety"`) {
			t.Error("login form does not carry next")
		}
		_, body = get(t, srv, newClient(t), "/admin/login?next=%2F%2Fevil.example")
		if strings.Contains(body, `name="next"`) {
			t.Error("login form carries an off-site next")
		}

		// A failed attempt keeps it for the retry.
		client := newClient(t)
		resp, err := client.PostForm(srv.URL+"/admin/login", url.Values{
			"csrf_token": {csrfFrom(t, srv, client, "/admin/login")},
			"email":      {"reader@example.com"}, "password": {"wrong"}, "next": {"/forms"},
		})
		if err != nil {
			t.Fatal(err)
		}
		retry, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if !strings.Contains(string(retry), `name="next" value="/forms"`) {
			t.Error("a wrong password loses next")
		}

		// Already signed in: the login page forwards to next.
		signedIn := noFollow(t)
		signIn(t, srv, signedIn, "editor@example.com", "")
		resp, _ = get(t, srv, signedIn, "/admin/login?next=%2Fforms")
		if resp.Header.Get("Location") != "/forms" {
			t.Errorf("signed-in login?next=/forms went to %q", resp.Header.Get("Location"))
		}
	})
}

// next survives the two-factor step, held in the session rather than on
// the challenge's URL.
func TestLoginReturnsToNextThroughTwoFactor(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		srv, users, _ := permTestServer(t, db)
		u := seedPermUser(t, users, "reader@example.com", auth.RoleReader)
		const secret = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"
		if err := users.EnableTOTP(context.Background(), u.ID, secret, 0); err != nil {
			t.Fatal(err)
		}
		client := noFollow(t)
		resp := signIn(t, srv, client, "reader@example.com", "/forms")
		if resp.Header.Get("Location") != "/admin/login/2fa" {
			t.Fatalf("password step went to %q, want the code challenge", resp.Header.Get("Location"))
		}
		code, err := auth.TOTPCode(secret, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		resp, err = client.PostForm(srv.URL+"/admin/login/2fa", url.Values{
			"csrf_token": {csrfFrom(t, srv, client, "/admin/login/2fa")}, "code": {code},
			"next": {"https://evil.example/"}, // ignored: the session's copy wins
		})
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.Header.Get("Location") != "/forms" {
			t.Errorf("code step went to %q, want /forms", resp.Header.Get("Location"))
		}
	})
}

func TestUserManagersAndReaders(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		srv, users, _ := permTestServer(t, db)
		ctx := context.Background()
		seedPermUser(t, users, "admin@example.com", auth.RoleAdmin)
		seedPermUser(t, users, "manager@example.com", auth.RoleEditor, auth.PermUsers)
		pages := seedPermUser(t, users, "pages@example.com", auth.RoleEditor, auth.PermPages)

		manager := newClient(t)
		logIn(t, srv, manager, "manager@example.com", "password123")
		csrf := csrfFrom(t, srv, manager, "/admin/users/new")

		// A user manager without the admin role can create a reader; any
		// grants ticked on the form are not stored.
		resp, _ := postForm(t, srv, manager, "/admin/users/new", url.Values{
			"csrf_token": {csrf}, "name": {"Rae"}, "email": {"rae@example.com"},
			"password": {"password123"}, "role": {"reader"}, "active": {"on"}, "perm": {"users"},
		})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("creating a reader: %d", resp.StatusCode)
		}
		rae, err := users.GetByEmail(ctx, "rae@example.com")
		if err != nil {
			t.Fatalf("reader not created: %v", err)
		}
		if rae.Role != auth.RoleReader || len(rae.Permissions) != 0 {
			t.Errorf("created reader: role %q, grants %v; want reader with none", rae.Role, rae.Permissions)
		}
		if resp, _ := get(t, srv, manager, "/admin/users/"+strconv.FormatInt(rae.ID, 10)); resp.StatusCode != http.StatusOK {
			t.Errorf("manager opening a reader: %d, want 200", resp.StatusCode)
		}

		// But cannot make a pages editor a reader: that would revoke a
		// grant they could never revoke directly.
		_, body := postForm(t, srv, manager, "/admin/users/"+strconv.FormatInt(pages.ID, 10), url.Values{
			"csrf_token": {csrf}, "name": {pages.Name}, "email": {pages.Email}, "role": {"reader"}, "active": {"on"},
		})
		if !strings.Contains(body, "cannot make it a reader") {
			t.Error("demoting a pages editor gave no reason")
		}
		// The form comes back showing the grants the account still holds.
		if !regexp.MustCompile(`value="pages"\s+checked`).MatchString(body) {
			t.Error("the refused form lost the account's pages grant")
		}
		if got, _ := users.GetByEmail(ctx, "pages@example.com"); got.Role != auth.RoleEditor || len(got.Permissions) != 1 {
			t.Errorf("pages editor after refused demotion: %q %v", got.Role, got.Permissions)
		}

		// An admin can, and the grants go with the role.
		admin := newClient(t)
		logIn(t, srv, admin, "admin@example.com", "password123")
		postForm(t, srv, admin, "/admin/users/"+strconv.FormatInt(pages.ID, 10), url.Values{
			"csrf_token": {csrfFrom(t, srv, admin, "/admin/users")}, "name": {pages.Name}, "email": {pages.Email},
			"role": {"reader"}, "active": {"on"}, "perm": {"pages"},
		})
		if got, _ := users.GetByEmail(ctx, "pages@example.com"); got.Role != auth.RoleReader || len(got.Permissions) != 0 {
			t.Errorf("after an admin's demotion: %q %v, want reader with no grants", got.Role, got.Permissions)
		}
	})
}
