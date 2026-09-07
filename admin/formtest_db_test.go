package admin

// The harness the content-form tests share. The write handlers — the
// page and post forms, and the in-place editor's API — are the half of
// the admin that changes stored content, and until now nothing drove
// them: the tests around them checked the markup a form renders and the
// routes it posts to, not what the handler on the other end did with the
// submission.
//
// Handlers are called directly rather than through the router, the way
// the metadata API's tests already do, so a test names the handler it is
// about and does not restate the CSRF and permission middleware that
// media_permission_test.go and csrf_bodylimit_test.go already cover.

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/alexedwards/scs/v2"
	"github.com/go-chi/chi/v5"

	"github.com/tsawler/cms/auth"
	"github.com/tsawler/cms/content"
	"github.com/tsawler/cms/internal/sqldb"
	"github.com/tsawler/cms/media"
	"github.com/tsawler/cms/render"
)

// formTemplates is a template set shaped like the ones the write handlers
// have opinions about: the page template's only editable region is a
// sections region (the "blank canvas" shape seedStarterSections seeds),
// and the post template adds a header region above it, which is where a
// post's banner goes.
func formTemplates(t *testing.T) *render.Renderer {
	t.Helper()
	fsys := fstest.MapFS{
		"base.gohtml": &fstest.MapFile{Data: []byte(
			`{{define "base"}}<html><body><h1>{{.Title}}</h1>` +
				`{{block "content" .}}{{end}}</body></html>{{end}}`)},
		"page.gohtml": &fstest.MapFile{Data: []byte(
			`{{template "base" .}}{{define "content"}}{{cmsSections "main"}}{{end}}`)},
		"post.gohtml": &fstest.MapFile{Data: []byte(
			`{{template "base" .}}{{define "content"}}` +
				`{{cmsSections "header"}}{{cmsSections "main"}}{{end}}`)},
		// A template whose regions are the two kinds the page form
		// itself saves — plain text and rich HTML. Sections regions are
		// not among them on purpose: they save through the editor's own
		// endpoint, so the form has to leave them alone.
		"plain.gohtml": &fstest.MapFile{Data: []byte(
			`{{template "base" .}}{{define "content"}}` +
				`{{cmsText "tagline"}}{{cmsRegion "body"}}{{end}}`)},
	}
	r, err := render.New(fsys, []string{"base.gohtml"},
		[]render.PageTemplate{
			{File: "page.gohtml", Label: "Page"},
			{File: "plain.gohtml", Label: "Plain"},
		},
		render.DefaultSectionStyles(),
		// The post template is registered hidden, exactly as cms.New
		// registers it: posts render with it, but it is not offered in
		// the page form's template picker.
		render.PageTemplate{File: "post.gohtml", Label: "Post"})
	if err != nil {
		t.Fatalf("render.New: %v", err)
	}
	return r
}

// formServer wires a server to a real store, the template set above, and
// the admin's own templates — everything a write handler touches on both
// its success path and the form re-render it falls back to.
func formServer(t *testing.T, db *sqldb.DB) *server {
	t.Helper()
	return &server{
		templates: parseTemplates(),
		deps: Deps{
			Sessions:      scs.New(),
			Users:         auth.NewStore(db),
			Content:       content.NewStore(db, "en"),
			Renderer:      formTemplates(t),
			SectionStyles: render.DefaultSectionStyles(),
			PostTemplate:  render.PageTemplate{File: "post.gohtml", Label: "Post"},
			Logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
			AdminPath:     "/admin",
			DefaultLocale: "en",
			Locales:       []string{"en", "fr"},
		},
	}
}

// withMedia gives a server a media manager backed by memory, for the
// handlers that only do half their job without one — the post form's
// image pickers, and the media library itself.
func withMedia(s *server, db *sqldb.DB) *downloadStore {
	store := &downloadStore{objects: map[string][]byte{}}
	s.deps.Media = media.NewManager(db, store, s.deps.Logger)
	return store
}

// formUser returns a user with the given role and grants, inserting it the
// first time it is asked for. Tests share a database across subtests, so
// the lookup comes first: a second call with the same email is the same
// person, not a duplicate-email failure.
func formUser(t *testing.T, s *server, email string, role auth.Role, perms ...auth.Permission) *auth.User {
	t.Helper()
	ctx := context.Background()
	u, err := s.deps.Users.GetByEmail(ctx, email)
	if err == nil {
		return u
	}
	if !errors.Is(err, auth.ErrNotFound) {
		t.Fatalf("looking up %s: %v", email, err)
	}
	u = &auth.User{Email: email, Name: email, PasswordHash: "x", Role: role, Active: true}
	if _, err := s.deps.Users.Insert(ctx, u); err != nil {
		t.Fatalf("seeding %s: %v", email, err)
	}
	if len(perms) > 0 {
		if err := s.deps.Users.ReplacePermissions(ctx, u.ID, perms); err != nil {
			t.Fatalf("granting %s: %v", email, err)
		}
		u, err = s.deps.Users.GetByID(ctx, u.ID)
		if err != nil {
			t.Fatalf("reloading %s: %v", email, err)
		}
	}
	return u
}

// formAdmin and formEditor are the two users nearly every test wants: one
// who may do anything, and one who holds only the pages permission and so
// stands on the far side of every admin-only branch.
func formAdmin(t *testing.T, s *server) *auth.User {
	return formUser(t, s, "form-admin@example.com", auth.RoleAdmin)
}

func formEditor(t *testing.T, s *server, perms ...auth.Permission) *auth.User {
	if len(perms) == 0 {
		perms = []auth.Permission{auth.PermPages}
	}
	return formUser(t, s, "form-editor@example.com", auth.RoleEditor, perms...)
}

// formReq builds a form POST carrying the chi URL parameters the handler
// reads and the session state the middleware would have left behind for
// the signed-in user. The returned request's context holds the session,
// so a test reads the flash back off it after the handler runs.
func formReq(t *testing.T, s *server, u *auth.User, form url.Values, params map[string]string) *http.Request {
	t.Helper()
	return formReqTo(t, s, u, http.MethodPost, "/admin/x", form, params)
}

// formReqTo is formReq for the handlers that care about the method or the
// query string — a locale tab arrives as "?locale=fr", and the previews
// are GETs.
func formReqTo(t *testing.T, s *server, u *auth.User, method, target string,
	form url.Values, params map[string]string) *http.Request {
	t.Helper()

	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	r := httptest.NewRequest(method, target, body)
	if form != nil {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	return withRouteAndSession(t, s, u, r, params)
}

// withRouteAndSession attaches the chi route parameters and a loaded
// session to a request built by any means — formReqTo, or a test that
// assembled a multipart body of its own.
func withRouteAndSession(t *testing.T, s *server, u *auth.User, r *http.Request, params map[string]string) *http.Request {
	t.Helper()

	rctx := chi.NewRouteContext()
	for k, v := range params {
		rctx.URLParams.Add(k, v)
	}
	r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))

	sctx, err := s.deps.Sessions.Load(r.Context(), "")
	if err != nil {
		t.Fatalf("loading a session: %v", err)
	}
	if u != nil {
		s.deps.Sessions.Put(sctx, sessionKeyUserID, u.ID)
	}
	return r.WithContext(sctx)
}

// idParams is the {id} route parameter on its own, which is what most of
// these handlers take.
func idParams(id int64) map[string]string {
	return map[string]string{"id": strconv.FormatInt(id, 10)}
}

// flashOf reads the flash a handler left for the next page. It pops, like
// the real read does, so a test asserting twice would see the second read
// come back empty — which is the behaviour, not a quirk of the harness.
func flashOf(s *server, r *http.Request) string {
	return s.deps.Sessions.PopString(r.Context(), sessionKeyFlash)
}

// seedPage inserts a page directly, for the tests that are about editing
// one rather than about creating it.
func seedPage(t *testing.T, s *server, p *content.Page) *content.Page {
	t.Helper()
	if p.TemplateName == "" {
		p.TemplateName = "page.gohtml"
	}
	if p.Title == "" {
		p.Title = "Seeded"
	}
	id, err := s.deps.Content.Insert(context.Background(), p, s.deps.DefaultLocale)
	if err != nil {
		t.Fatalf("seeding page %q: %v", p.Slug, err)
	}
	stored, err := s.deps.Content.GetByID(context.Background(), id, s.deps.DefaultLocale)
	if err != nil {
		t.Fatalf("reloading page %q: %v", p.Slug, err)
	}
	return stored
}

// seedPost inserts a post directly, on the post template, for the tests
// that are about editing one rather than creating it. The slug tail is
// what goes after the feed prefix, which is the only part a post's
// address lets anyone choose.
func seedPost(t *testing.T, s *server, feed content.Feed, title, slugTail string) *content.Post {
	t.Helper()
	p := &content.Post{Feed: feed}
	p.Title = title
	p.TemplateName = s.deps.PostTemplate.File
	p.Slug = string(feed) + "/" + slugTail
	if _, err := s.deps.Content.InsertPost(context.Background(), p, s.deps.DefaultLocale); err != nil {
		t.Fatalf("seeding post %q: %v", p.Slug, err)
	}
	stored, err := s.deps.Content.PostByID(context.Background(), p.PostID, s.deps.DefaultLocale)
	if err != nil {
		t.Fatalf("reloading post %q: %v", p.Slug, err)
	}
	return stored
}

// draftHTML is the concatenated draft content of one region, which is what
// most of these assertions are really about: did the submission land in
// stored content, and in the right shape.
func draftHTML(t *testing.T, s *server, pageID int64, region, locale string) string {
	t.Helper()
	blocks, err := s.deps.Content.BlocksFor(context.Background(), pageID, locale, content.StatusDraft)
	if err != nil {
		t.Fatalf("reading draft blocks: %v", err)
	}
	var sb strings.Builder
	for _, b := range blocks {
		if b.Region == region {
			sb.WriteString(b.Content)
		}
	}
	return sb.String()
}

// reloadPage re-reads a page from the store, for asserting on what a
// handler actually wrote rather than on what it was handed.
func reloadPage(t *testing.T, s *server, id int64) *content.Page {
	t.Helper()
	p, err := s.deps.Content.GetByID(context.Background(), id, s.deps.DefaultLocale)
	if err != nil {
		t.Fatalf("reloading page %d: %v", id, err)
	}
	return p
}

func reloadPost(t *testing.T, s *server, postID int64) *content.Post {
	t.Helper()
	p, err := s.deps.Content.PostByID(context.Background(), postID, s.deps.DefaultLocale)
	if err != nil {
		t.Fatalf("reloading post %d: %v", postID, err)
	}
	return p
}

// wantRedirect asserts a 303 to the given location, which is how every
// successful write handler ends: see-other, so a reload does not re-post.
func wantRedirect(t *testing.T, rec *httptest.ResponseRecorder, want string) {
	t.Helper()
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303 (body %q)", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Location"); got != want {
		t.Errorf("Location = %q, want %q", got, want)
	}
}

// itoa keeps the redirect assertions readable — every one of them ends in
// a page or post id.
func itoa(n int64) string { return strconv.FormatInt(n, 10) }
