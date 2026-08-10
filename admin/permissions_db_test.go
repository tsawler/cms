package admin

// Route access under per-user permissions, end to end against a real
// database: which admin areas each grant opens, that the feed split
// holds inside Blog & News, that posts' backing pages are unreachable
// from the Pages section, and that a users-permission editor cannot
// escalate — not to the admin role, not into admin accounts, and not by
// granting permissions they don't hold.

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/tsawler/cms/auth"
	"github.com/tsawler/cms/content"
	"github.com/tsawler/cms/internal/dbtest"
	"github.com/tsawler/cms/internal/sqldb"
	"github.com/tsawler/cms/render"
	"github.com/tsawler/cms/snippets"
)

// permTestServer is settingsTestServer plus a renderer and post
// template, so the pages, posts, and editor-API routes all register.
func permTestServer(t *testing.T, db *sqldb.DB) (*httptest.Server, *auth.Store, *content.Store) {
	t.Helper()
	fsys := fstest.MapFS{
		"base.gohtml": &fstest.MapFile{Data: []byte(
			`{{define "base"}}<html><body>{{block "content" .}}{{end}}` +
				`<footer>{{cmsShared "footer"}}</footer></body></html>{{end}}`)},
		"page.gohtml": &fstest.MapFile{Data: []byte(
			`{{template "base" .}}{{define "content"}}{{cmsRegion "main"}}{{end}}`)},
		"post.gohtml": &fstest.MapFile{Data: []byte(
			`{{template "base" .}}{{define "content"}}{{cmsRegion "main"}}{{end}}`)},
		"oneoff.gohtml": &fstest.MapFile{Data: []byte(
			`{{template "base" .}}{{define "content"}}{{cmsRegion "main"}}{{end}}`)},
	}
	postTemplate := render.PageTemplate{File: "post.gohtml", Label: "Post"}
	r, err := render.New(fsys, []string{"base.gohtml"},
		[]render.PageTemplate{
			{File: "page.gohtml", Label: "Page"},
			{File: "oneoff.gohtml", Label: "One-off", Unlisted: true},
		}, nil, postTemplate)
	if err != nil {
		t.Fatalf("render.New: %v", err)
	}
	users := auth.NewStore(db)
	h := New(Deps{
		Sessions:      scs.New(),
		Users:         users,
		Content:       content.NewStore(db, "en"),
		Renderer:      r,
		Snippets:      snippets.NewStore(db),
		PostTemplate:  postTemplate,
		Logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		AdminPath:     "/admin",
		DefaultLocale: "en",
		Locales:       []string{"en"},
	})
	mux := http.NewServeMux()
	mux.Handle("/admin/", http.StripPrefix("/admin", h))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, users, content.NewStore(db, "en")
}

// seedPermUser inserts an active account with the given role and grants.
// The password is always "password123".
func seedPermUser(t *testing.T, users *auth.Store, email string, role auth.Role, perms ...auth.Permission) *auth.User {
	t.Helper()
	ctx := context.Background()
	hash, err := auth.HashPassword("password123")
	if err != nil {
		t.Fatal(err)
	}
	u := &auth.User{Email: email, Name: "Test " + email, PasswordHash: hash, Role: role, Active: true}
	if _, err := users.Insert(ctx, u); err != nil {
		t.Fatalf("seeding %s: %v", email, err)
	}
	if len(perms) > 0 {
		if err := users.ReplacePermissions(ctx, u.ID, perms); err != nil {
			t.Fatalf("granting %s: %v", email, err)
		}
	}
	return u
}

// do sends a request with the session's CSRF token on the header, the
// way the editor's JS does, and returns the status code.
func do(t *testing.T, srv *httptest.Server, client *http.Client, csrf, method, path, body string) int {
	t.Helper()
	req, err := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("X-CSRF-Token", csrf)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return resp.StatusCode
}

func TestRouteAccessByPermission(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		srv, users, store := permTestServer(t, db)
		ctx := context.Background()

		page := &content.Page{Title: "About", Slug: "about", TemplateName: "page.gohtml"}
		if _, err := store.Insert(ctx, page, "en"); err != nil {
			t.Fatal(err)
		}
		blogPost := &content.Post{Feed: content.FeedBlog, PublishedAt: time.Now()}
		blogPost.Title, blogPost.Slug, blogPost.TemplateName = "B", "blog/b", "post.gohtml"
		if _, err := store.InsertPost(ctx, blogPost, "en"); err != nil {
			t.Fatal(err)
		}
		newsPost := &content.Post{Feed: content.FeedNews, PublishedAt: time.Now()}
		newsPost.Title, newsPost.Slug, newsPost.TemplateName = "N", "news/n", "post.gohtml"
		if _, err := store.InsertPost(ctx, newsPost, "en"); err != nil {
			t.Fatal(err)
		}
		pageID := strconv.FormatInt(page.ID, 10)
		blogPageID := strconv.FormatInt(blogPost.ID, 10) // the backing page's id
		blogPostID := strconv.FormatInt(blogPost.PostID, 10)
		newsPostID := strconv.FormatInt(newsPost.PostID, 10)

		seedPermUser(t, users, "super@example.com", auth.RoleSuperadmin)
		seedPermUser(t, users, "admin@example.com", auth.RoleAdmin)
		seedPermUser(t, users, "blogs@example.com", auth.RoleEditor, auth.PermBlogs)
		seedPermUser(t, users, "pages@example.com", auth.RoleEditor, auth.PermPages)
		seedPermUser(t, users, "userperm@example.com", auth.RoleEditor, auth.PermUsers)
		seedPermUser(t, users, "none@example.com", auth.RoleEditor)

		regionsBody := `{"locale":"en","regions":{"main":"<p>hi</p>"}}`
		sharedBody := `{"locale":"en","regions":{"site:footer":"<p>changed</p>"}}`
		menuBody := `{"menu":"main","items":[]}`

		checks := []struct {
			actor        string
			method, path string
			body         string
			want         int
		}{
			// The Pages section is superadmin-only — even the pages
			// permission and the admin role don't open it — and posts'
			// backing pages are not reachable through it for anyone.
			{"super@example.com", "GET", "/admin/pages", "", 200},
			{"super@example.com", "GET", "/admin/pages/" + pageID, "", 200},
			{"admin@example.com", "GET", "/admin/pages", "", 403},
			{"pages@example.com", "GET", "/admin/pages", "", 403},
			{"pages@example.com", "GET", "/admin/pages/" + pageID, "", 403},
			{"blogs@example.com", "GET", "/admin/pages", "", 403},
			{"none@example.com", "GET", "/admin/pages", "", 403},
			{"super@example.com", "GET", "/admin/pages/" + blogPageID, "", 404},

			// Blog & News admits either feed permission; the other feed's
			// posts stay out of reach.
			{"blogs@example.com", "GET", "/admin/posts", "", 200},
			{"blogs@example.com", "GET", "/admin/posts?feed=news", "", 403},
			{"blogs@example.com", "GET", "/admin/posts/" + blogPostID, "", 200},
			{"blogs@example.com", "GET", "/admin/posts/" + newsPostID, "", 403},
			{"pages@example.com", "GET", "/admin/posts", "", 403},
			{"none@example.com", "GET", "/admin/posts", "", 403},
			{"admin@example.com", "GET", "/admin/posts/" + newsPostID, "", 200},

			// The editor API's per-page mutations answer to the page's
			// slug: a blogs editor saves blog posts, not site pages — and
			// not the shared footer, even from a blog post's page.
			{"blogs@example.com", "POST", "/admin/api/pages/" + blogPageID + "/regions", regionsBody, 200},
			{"blogs@example.com", "POST", "/admin/api/pages/" + pageID + "/regions", regionsBody, 403},
			{"pages@example.com", "POST", "/admin/api/pages/" + blogPageID + "/regions", regionsBody, 403},
			{"blogs@example.com", "POST", "/admin/api/pages/" + blogPageID + "/regions", sharedBody, 403},
			{"pages@example.com", "POST", "/admin/api/pages/" + pageID + "/regions", sharedBody, 200},
			{"blogs@example.com", "POST", "/admin/api/pages/" + blogPageID + "/publish", "", 200},
			{"blogs@example.com", "POST", "/admin/api/pages/" + pageID + "/publish", "", 403},

			// Menus and site settings belong to pages.
			{"pages@example.com", "PUT", "/admin/api/menu", menuBody, 200},
			{"blogs@example.com", "PUT", "/admin/api/menu", menuBody, 403},
			{"blogs@example.com", "PUT", "/admin/api/settings", `{"siteName":"X"}`, 403},
			{"pages@example.com", "PUT", "/admin/api/settings", `{"siteName":"X"}`, 200},

			// Reads the editor needs stay open to every logged-in user.
			{"none@example.com", "GET", "/admin/api/pages", "", 200},
			{"none@example.com", "GET", "/admin/api/menu?menu=main", "", 200},
			{"none@example.com", "GET", "/admin/api/settings", "", 200},
			{"none@example.com", "GET", "/admin/api/snippets", "", 200},
			{"none@example.com", "GET", "/admin/", "", 200},

			// Post creation checks the feed being posted into.
			{"blogs@example.com", "POST", "/admin/api/posts", `{"title":"T","feed":"blog"}`, 200},
			{"blogs@example.com", "POST", "/admin/api/posts", `{"title":"T","feed":"news"}`, 403},

			// Page creation offers listed templates to any pages-permission
			// holder; unlisted templates (and the hidden post template) are
			// a superadmin's alone.
			{"pages@example.com", "POST", "/admin/api/pages", `{"title":"T","template":"page.gohtml"}`, 200},
			{"pages@example.com", "POST", "/admin/api/pages", `{"title":"T","template":"oneoff.gohtml"}`, 400},
			{"admin@example.com", "POST", "/admin/api/pages", `{"title":"T","template":"oneoff.gohtml"}`, 400},
			{"pages@example.com", "POST", "/admin/api/pages", `{"title":"T","template":"post.gohtml"}`, 400},
			{"super@example.com", "POST", "/admin/api/pages", `{"title":"T","template":"oneoff.gohtml"}`, 200},

			// Snippets are superadmin's; users follows its permission.
			{"super@example.com", "GET", "/admin/snippets", "", 200},
			{"admin@example.com", "GET", "/admin/snippets", "", 403},
			{"userperm@example.com", "GET", "/admin/users", "", 200},
			{"userperm@example.com", "GET", "/admin/pages", "", 403},
			{"none@example.com", "GET", "/admin/users", "", 403},
			{"admin@example.com", "GET", "/admin/users", "", 200},
		}

		clients := map[string]*http.Client{}
		csrfs := map[string]string{}
		for _, c := range checks {
			client, ok := clients[c.actor]
			if !ok {
				client = newClient(t)
				logIn(t, srv, client, c.actor, "password123")
				clients[c.actor] = client
				csrfs[c.actor] = csrfFrom(t, srv, client, "/admin/")
			}
			if got := do(t, srv, client, csrfs[c.actor], c.method, c.path, c.body); got != c.want {
				t.Errorf("%s: %s %s = %d, want %d", c.actor, c.method, c.path, got, c.want)
			}
		}
	})
}

// A non-admin holder of the users permission manages editor accounts
// only, and can neither assign roles nor move grants they don't hold.
func TestUserManagementEscalationGuards(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		srv, users, _ := permTestServer(t, db)
		ctx := context.Background()

		manager := seedPermUser(t, users, "manager@example.com", auth.RoleEditor, auth.PermUsers, auth.PermBlogs)
		target := seedPermUser(t, users, "target@example.com", auth.RoleEditor, auth.PermPages)
		boss := seedPermUser(t, users, "boss@example.com", auth.RoleAdmin)

		client := newClient(t)
		logIn(t, srv, client, "manager@example.com", "password123")
		csrf := csrfFrom(t, srv, client, "/admin/")

		bossPath := "/admin/users/" + strconv.FormatInt(boss.ID, 10)
		targetPath := "/admin/users/" + strconv.FormatInt(target.ID, 10)
		selfPath := "/admin/users/" + strconv.FormatInt(manager.ID, 10)

		// Admin accounts are entirely out of reach — the form (with its
		// password and two-factor resets) never even opens.
		if got := do(t, srv, client, csrf, "GET", bossPath, ""); got != 403 {
			t.Errorf("GET admin target = %d, want 403", got)
		}
		resp, _ := postForm(t, srv, client, bossPath, url.Values{
			"csrf_token": {csrf}, "name": {"Evil"}, "email": {"boss@example.com"},
			"role": {"editor"}, "active": {"on"}, "password": {"hijacked-pass"},
		})
		if resp.StatusCode != 403 {
			t.Errorf("POST admin target = %d, want 403", resp.StatusCode)
		}
		if _, err := users.Authenticate(ctx, "boss@example.com", "password123"); err != nil {
			t.Error("admin's password was changed by a non-admin user manager")
		}

		// Assigning the admin role is refused even for an editor target.
		resp, page := postForm(t, srv, client, targetPath, url.Values{
			"csrf_token": {csrf}, "name": {"Target"}, "email": {"target@example.com"},
			"role": {"admin"}, "active": {"on"},
		})
		if resp.StatusCode != 422 {
			t.Errorf("assigning admin role = %d, want 422", resp.StatusCode)
		}
		if !strings.Contains(page, "Only administrators") {
			t.Error("no role error message on the re-rendered form")
		}
		if got, _ := users.GetByID(ctx, target.ID); got.Role != auth.RoleEditor {
			t.Errorf("target role = %q after refused save, want editor", got.Role)
		}

		// Grants move only within what the manager holds: news (not held)
		// is not granted, pages (not held, target had it) is not revoked,
		// blogs (held) is granted.
		resp, _ = postForm(t, srv, client, targetPath, url.Values{
			"csrf_token": {csrf}, "name": {"Target"}, "email": {"target@example.com"},
			"role": {"editor"}, "active": {"on"},
			"perm": {string(auth.PermNews), string(auth.PermBlogs)},
		})
		if resp.StatusCode != 200 {
			t.Fatalf("editing an editor target = %d, want 200 after redirect", resp.StatusCode)
		}
		got, _ := users.GetByID(ctx, target.ID)
		if slices.Contains(got.Permissions, auth.PermNews) {
			t.Error("manager granted news without holding it")
		}
		if !slices.Contains(got.Permissions, auth.PermPages) {
			t.Error("manager revoked pages without holding it")
		}
		if !slices.Contains(got.Permissions, auth.PermBlogs) {
			t.Error("manager could not grant blogs, which they hold")
		}

		// Dropping one's own users grant is refused — it's the permission
		// this whole page runs on.
		resp, page = postForm(t, srv, client, selfPath, url.Values{
			"csrf_token": {csrf}, "name": {"Manager"}, "email": {"manager@example.com"},
			"role": {"editor"}, "active": {"on"},
			"perm": {string(auth.PermBlogs)},
		})
		if resp.StatusCode != 422 {
			t.Errorf("dropping own users grant = %d, want 422", resp.StatusCode)
		}
		if !strings.Contains(page, "user-management permission") {
			t.Error("no self-guard message on the re-rendered form")
		}
		if got, _ := users.GetByID(ctx, manager.ID); !got.Can(auth.PermUsers) {
			t.Error("manager lost the users grant despite the guard")
		}

		// Creating a user: the role is forced to editor and grants are
		// bounded the same way as an edit.
		resp, _ = postForm(t, srv, client, "/admin/users/new", url.Values{
			"csrf_token": {csrf}, "name": {"Newbie"}, "email": {"new@example.com"},
			"role": {"editor"}, "active": {"on"}, "password": {"password123"},
			"perm": {string(auth.PermBlogs), string(auth.PermPages)},
		})
		if resp.StatusCode != 200 {
			t.Fatalf("creating an editor = %d, want 200 after redirect", resp.StatusCode)
		}
		newbie, err := users.GetByEmail(ctx, "new@example.com")
		if err != nil {
			t.Fatal(err)
		}
		if newbie.Role != auth.RoleEditor {
			t.Errorf("created role = %q, want editor", newbie.Role)
		}
		if slices.Contains(newbie.Permissions, auth.PermPages) {
			t.Error("manager granted pages on create without holding it")
		}
		if !slices.Contains(newbie.Permissions, auth.PermBlogs) {
			t.Error("blogs grant missing on the created user")
		}

		// An admin remains free to hand out any role and any grant.
		adminClient := newClient(t)
		logIn(t, srv, adminClient, "boss@example.com", "password123")
		adminCSRF := csrfFrom(t, srv, adminClient, "/admin/")
		resp, _ = postForm(t, srv, adminClient, targetPath, url.Values{
			"csrf_token": {adminCSRF}, "name": {"Target"}, "email": {"target@example.com"},
			"role": {"admin"}, "active": {"on"},
			"perm": {string(auth.PermNews)},
		})
		if resp.StatusCode != 200 {
			t.Fatalf("admin promoting target = %d, want 200 after redirect", resp.StatusCode)
		}
		if got, _ := users.GetByID(ctx, target.ID); got.Role != auth.RoleAdmin {
			t.Errorf("target role after admin save = %q, want admin", got.Role)
		}
	})
}
