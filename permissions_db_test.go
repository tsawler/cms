package cms

// The public site under per-user permissions: edit mode is decided per
// page by the slug's permission, so a blogs-only editor gets the
// in-place editor (and drafts) on blog posts and a plain public render
// everywhere else.

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/tsawler/cms/auth"
	"github.com/tsawler/cms/content"
	"github.com/tsawler/cms/internal/dbtest"
	"github.com/tsawler/cms/internal/sqldb"
)

var permTestFS = fstest.MapFS{
	"templates/base.gohtml": &fstest.MapFile{Data: []byte(
		`{{define "base"}}<html><body>{{block "content" .}}{{end}}</body></html>{{end}}`)},
	"templates/pages/standard.gohtml": &fstest.MapFile{Data: []byte(
		`{{template "base" .}}{{define "content"}}<main>{{cmsRegion "main"}}</main>{{end}}`)},
	"templates/pages/post.gohtml": &fstest.MapFile{Data: []byte(
		`{{template "base" .}}{{define "content"}}<article>{{cmsRegion "main"}}</article>{{end}}`)},
}

func newPermTestCMS(t *testing.T, db *sqldb.DB) *CMS {
	t.Helper()
	c, err := New(Config{
		DB:              db.SQL(),
		Dialect:         db.Dialect().Name(),
		Logger:          slog.New(slog.NewTextHandler(io.Discard, nil)),
		TemplateFS:      permTestFS,
		SharedTemplates: []string{"templates/base.gohtml"},
		PageTemplates: []PageTemplate{
			{File: "templates/pages/standard.gohtml", Label: "Standard"},
		},
		PostTemplate: PageTemplate{File: "templates/pages/post.gohtml", Label: "Post"},
	})
	if err != nil {
		t.Fatalf("cms.New: %v", err)
	}
	return c
}

var loginCSRFRe = regexp.MustCompile(`name="csrf_token" value="([^"]+)"`)

// logInSite drives the admin login through the CMS's own Handler, so the
// session cookie is good for public pages too.
func logInSite(t *testing.T, srv *httptest.Server, client *http.Client, email string) {
	t.Helper()
	resp, err := client.Get(srv.URL + "/admin/login")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	m := loginCSRFRe.FindSubmatch(body)
	if m == nil {
		t.Fatalf("no csrf token on the login page:\n%s", body)
	}
	resp, err = client.PostForm(srv.URL+"/admin/login", url.Values{
		"csrf_token": {string(m[1])}, "email": {email}, "password": {"password123"},
	})
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
}

func fetch(t *testing.T, srv *httptest.Server, client *http.Client, path string) (int, string) {
	t.Helper()
	resp, err := client.Get(srv.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp.StatusCode, string(body)
}

func TestServePageEditModePerPermission(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		c := newPermTestCMS(t, db)

		home := &content.Page{Slug: "", Title: "Home", TemplateName: "templates/pages/standard.gohtml"}
		if _, err := c.content.Insert(ctx, home, "en"); err != nil {
			t.Fatal(err)
		}
		if err := c.content.Publish(ctx, home.ID); err != nil {
			t.Fatal(err)
		}
		// The blog post stays a draft: only someone in edit mode sees it.
		post := &content.Post{Feed: content.FeedBlog, PublishedAt: time.Now()}
		post.Title, post.Slug, post.TemplateName = "B", "blog/b", "templates/pages/post.gohtml"
		if _, err := c.content.InsertPost(ctx, post, "en"); err != nil {
			t.Fatal(err)
		}

		hash, err := auth.HashPassword("password123")
		if err != nil {
			t.Fatal(err)
		}
		blogsOnly := &auth.User{Email: "blogs@example.com", Name: "B", PasswordHash: hash, Role: auth.RoleEditor, Active: true}
		if _, err := c.users.Insert(ctx, blogsOnly); err != nil {
			t.Fatal(err)
		}
		if err := c.users.ReplacePermissions(ctx, blogsOnly.ID, []auth.Permission{auth.PermBlogs}); err != nil {
			t.Fatal(err)
		}
		adminUser := &auth.User{Email: "admin@example.com", Name: "A", PasswordHash: hash, Role: auth.RoleAdmin, Active: true}
		if _, err := c.users.Insert(ctx, adminUser); err != nil {
			t.Fatal(err)
		}

		srv := httptest.NewServer(c.Handler())
		t.Cleanup(srv.Close)

		// Anonymous: home renders plain, the draft post is a 404.
		client := &http.Client{}
		if code, body := fetch(t, srv, client, "/"); code != 200 || strings.Contains(body, "data-page-id") {
			t.Errorf("anonymous home: code %d, editor injected: %v", code, strings.Contains(body, "data-page-id"))
		}
		if code, _ := fetch(t, srv, client, "/blog/b"); code != 404 {
			t.Errorf("anonymous draft post: code %d, want 404", code)
		}

		// A blogs-only editor edits blog posts (drafts included) and gets
		// the plain public render on the home page.
		jar, _ := cookiejar.New(nil)
		blogsClient := &http.Client{Jar: jar}
		logInSite(t, srv, blogsClient, "blogs@example.com")

		code, body := fetch(t, srv, blogsClient, "/blog/b")
		if code != 200 {
			t.Fatalf("blogs editor on draft post: code %d, want 200", code)
		}
		for _, want := range []string{`data-page-id`, `data-can-blogs="1"`, `data-can-news="0"`, `data-can-pages="0"`} {
			if !strings.Contains(body, want) {
				t.Errorf("blogs editor on post: missing %s", want)
			}
		}
		if code, body := fetch(t, srv, blogsClient, "/"); code != 200 || strings.Contains(body, "data-page-id") {
			t.Errorf("blogs editor on home: code %d, editor injected: %v — want a plain public render", code, strings.Contains(body, "data-page-id"))
		}

		// An admin edits everywhere, with every flag up.
		jar2, _ := cookiejar.New(nil)
		adminClient := &http.Client{Jar: jar2}
		logInSite(t, srv, adminClient, "admin@example.com")
		code, body = fetch(t, srv, adminClient, "/")
		if code != 200 || !strings.Contains(body, "data-page-id") {
			t.Fatalf("admin on home: code %d, editor injected: %v", code, strings.Contains(body, "data-page-id"))
		}
		for _, want := range []string{`data-can-blogs="1"`, `data-can-news="1"`, `data-can-pages="1"`} {
			if !strings.Contains(body, want) {
				t.Errorf("admin editor flags: missing %s", want)
			}
		}
	})
}

func TestCollectPermissions(t *testing.T) {
	section := func(perm string) AdminSection {
		return AdminSection{Path: "inv", NavLabel: "Inventory", Permission: perm,
			Handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})}
	}

	// Sections declare their permission automatically, labelled with the
	// nav label; an explicit declaration wins; built-ins are never listed.
	got, err := collectPermissions(nil, []AdminSection{section("vehicles")})
	if err != nil || len(got) != 1 || got[0].Key != "vehicles" || got[0].Label != "Inventory" {
		t.Errorf("section-derived = %+v (err %v), want vehicles/Inventory", got, err)
	}
	got, err = collectPermissions([]PermissionDef{{Key: "vehicles", Label: "Manage vehicles"}},
		[]AdminSection{section("vehicles")})
	if err != nil || len(got) != 1 || got[0].Label != "Manage vehicles" {
		t.Errorf("explicit override = %+v (err %v), want the explicit label", got, err)
	}
	got, err = collectPermissions(nil, []AdminSection{section("pages")})
	if err != nil || len(got) != 0 {
		t.Errorf("builtin-reusing section = %+v (err %v), want no declaration", got, err)
	}
	if got, err = collectPermissions([]PermissionDef{{Key: "reports"}}, nil); err != nil || len(got) != 1 || got[0].Label != "reports" {
		t.Errorf("label defaulting = %+v (err %v), want key as label", got, err)
	}

	for name, defs := range map[string][]PermissionDef{
		"invalid key":        {{Key: "Not Valid", Label: "X"}},
		"builtin redeclared": {{Key: "users", Label: "X"}},
		"duplicate key":      {{Key: "reports", Label: "A"}, {Key: "reports", Label: "B"}},
	} {
		if _, err := collectPermissions(defs, nil); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}

	// AdminsNeedGrant rides through from the section, and every
	// declaration of one key must agree on it — the flag decides who the
	// checkbox binds, and two answers would be a configuration error.
	gatedSection := func(path, perm string, gated bool) AdminSection {
		return AdminSection{Path: path, NavLabel: path, Permission: perm, AdminsNeedGrant: gated,
			Handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})}
	}
	got, err = collectPermissions(nil, []AdminSection{
		gatedSection("sales", "team", true), gatedSection("staff", "team", true)})
	if err != nil || len(got) != 1 || !got[0].AdminsNeedGrant {
		t.Errorf("shared gated key = %+v (err %v), want one AdminsNeedGrant declaration", got, err)
	}
	if _, err := collectPermissions(nil, []AdminSection{
		gatedSection("sales", "team", true), gatedSection("staff", "team", false)}); err == nil {
		t.Error("sections disagreeing on AdminsNeedGrant: expected an error")
	}
	if _, err := collectPermissions([]PermissionDef{{Key: "team", Label: "Team"}},
		[]AdminSection{gatedSection("staff", "team", true)}); err == nil {
		t.Error("explicit def disagreeing with the section on AdminsNeedGrant: expected an error")
	}
	got, err = collectPermissions([]PermissionDef{{Key: "team", Label: "Sales people & staff", AdminsNeedGrant: true}},
		[]AdminSection{gatedSection("staff", "team", true)})
	if err != nil || len(got) != 1 || got[0].Label != "Sales people & staff" || !got[0].AdminsNeedGrant {
		t.Errorf("explicit gated override = %+v (err %v), want the explicit label, still gated", got, err)
	}
}
