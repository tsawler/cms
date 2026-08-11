package admin

import (
	"context"
	"errors"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tsawler/cms/auth"
)

var noopHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})

func TestValidateSections(t *testing.T) {
	valid := []Section{
		{Path: "reports", Handler: noopHandler},
		{Path: "site-stats_v1.2~x", Handler: noopHandler},
		{Path: "inventory", NavLabel: "Inventory", NavAfter: "dashboard", Handler: noopHandler},
		{Path: "carded", NavLabel: "Carded", Dashboard: &DashboardCard{Description: "d"}, Handler: noopHandler},
		{Path: "titled", Dashboard: &DashboardCard{Title: "Titled"}, Handler: noopHandler},
	}
	if err := ValidateSections(valid); err != nil {
		t.Errorf("valid sections rejected: %v", err)
	}
	if err := ValidateSections(nil); err != nil {
		t.Errorf("nil sections rejected: %v", err)
	}

	bad := map[string][]Section{
		"empty path":                     {{Path: "", Handler: noopHandler}},
		"slash in path":                  {{Path: "a/b", Handler: noopHandler}},
		"space in path":                  {{Path: "a b", Handler: noopHandler}},
		"escaping path":                  {{Path: "../users", Handler: noopHandler}},
		"duplicate paths":                {{Path: "x", Handler: noopHandler}, {Path: "x", Handler: noopHandler}},
		"nil handler":                    {{Path: "x"}},
		"uppercase permission":           {{Path: "x", Permission: "Vehicles", Handler: noopHandler}},
		"spaced permission":              {{Path: "x", Permission: "manage vehicles", Handler: noopHandler}},
		"grant-gated without permission": {{Path: "x", AdminsNeedGrant: true, Handler: noopHandler}},
		"unknown NavAfter anchor":        {{Path: "x", NavAfter: "reports", Handler: noopHandler}},
		"section path as NavAfter":       {{Path: "x", NavAfter: "x", Handler: noopHandler}},
		"untitled dashboard card":        {{Path: "x", Dashboard: &DashboardCard{Description: "d"}, Handler: noopHandler}},
	}
	for name, sections := range bad {
		if err := ValidateSections(sections); err == nil {
			t.Errorf("%s: expected an error, got nil", name)
		}
	}
}

func TestNavSectionsFor(t *testing.T) {
	sections := []Section{
		{Path: "reports", NavLabel: "Reports", NavAfter: "dashboard", Confirm: "Run the report?",
			NavCount: func(context.Context) (int, error) { return 7, nil }, Handler: noopHandler},
		{Path: "hidden", Handler: noopHandler}, // no NavLabel: never in the nav
		{Path: "billing", NavLabel: "Billing", AdminOnly: true, Handler: noopHandler},
	}
	editorUser := &auth.User{Role: auth.RoleEditor}
	adminUser := &auth.User{Role: auth.RoleAdmin}

	editor := navSectionsFor(sections, "/admin", editorUser, "/")
	if len(editor) != 1 || editor[0].Label != "Reports" || editor[0].URL != "/admin/x/reports/" {
		t.Errorf("editor nav = %+v, want only Reports at /admin/x/reports/", editor)
	}
	if editor[0].After != "dashboard" {
		t.Errorf("After = %q, want the section's NavAfter carried through", editor[0].After)
	}
	if !editor[0].HasCount || editor[0].count == nil {
		t.Errorf("HasCount = %v, count nil = %v; want the section's NavCount carried through", editor[0].HasCount, editor[0].count == nil)
	}
	if editor[0].Confirm != "Run the report?" {
		t.Errorf("Confirm = %q, want the section's message carried through", editor[0].Confirm)
	}

	admin := navSectionsFor(sections, "/admin", adminUser, "/")
	if len(admin) != 2 || admin[1].Label != "Billing" || admin[1].URL != "/admin/x/billing/" {
		t.Errorf("admin nav = %+v, want Reports then Billing", admin)
	}

	// Viewing a section marks its own link, and only its own, active.
	viewing := navSectionsFor(sections, "/admin", adminUser, "/x/billing/subpage")
	if viewing[0].Active || !viewing[1].Active {
		t.Errorf("active flags = %+v, want only Billing active", viewing)
	}

	// The login page renders the nav data with no user at all.
	if got := navSectionsFor(sections, "/admin", nil, "/"); len(got) != 1 {
		t.Errorf("nil-user nav = %+v, want only Reports", got)
	}
}

// A section's Permission hides its nav link from editors without the
// grant, admits editors with it, and never bars admin roles.
func TestNavSectionsForPermission(t *testing.T) {
	sections := []Section{
		{Path: "inventory", NavLabel: "Inventory", Permission: "vehicles", Handler: noopHandler},
	}

	for name, tc := range map[string]struct {
		user *auth.User
		want int
	}{
		"nil user":              {nil, 0},
		"editor without grant":  {&auth.User{Role: auth.RoleEditor}, 0},
		"editor with grant":     {&auth.User{Role: auth.RoleEditor, Permissions: []auth.Permission{"vehicles"}}, 1},
		"editor with other":     {&auth.User{Role: auth.RoleEditor, Permissions: []auth.Permission{auth.PermPages}}, 0},
		"admin without grant":   {&auth.User{Role: auth.RoleAdmin}, 1},
		"superadmin, no grants": {&auth.User{Role: auth.RoleSuperadmin}, 1},
	} {
		if got := navSectionsFor(sections, "/admin", tc.user, "/"); len(got) != tc.want {
			t.Errorf("%s: nav has %d links, want %d", name, len(got), tc.want)
		}
	}
}

// AdminsNeedGrant makes the permission bind the admin role too: the nav
// link appears only with the explicit grant, and only superadmin passes
// for free.
func TestNavSectionsForAdminsNeedGrant(t *testing.T) {
	sections := []Section{
		{Path: "staff", NavLabel: "Staff", Permission: "team",
			AdminsNeedGrant: true, Handler: noopHandler},
	}

	for name, tc := range map[string]struct {
		user *auth.User
		want int
	}{
		"nil user":              {nil, 0},
		"editor without grant":  {&auth.User{Role: auth.RoleEditor}, 0},
		"editor with grant":     {&auth.User{Role: auth.RoleEditor, Permissions: []auth.Permission{"team"}}, 1},
		"admin without grant":   {&auth.User{Role: auth.RoleAdmin}, 0},
		"admin with grant":      {&auth.User{Role: auth.RoleAdmin, Permissions: []auth.Permission{"team"}}, 1},
		"superadmin, no grants": {&auth.User{Role: auth.RoleSuperadmin}, 1},
	} {
		if got := navSectionsFor(sections, "/admin", tc.user, "/"); len(got) != tc.want {
			t.Errorf("%s: nav has %d links, want %d", name, len(got), tc.want)
		}
	}
}

// A mounted section handler must see section-relative paths and its own
// browser-facing base via SectionPath, and the bare section URL must
// canonicalize to the trailing-slash form.
func TestSectionHandlerPaths(t *testing.T) {
	var gotPath, gotBase string
	s := &server{deps: Deps{AdminPath: "/admin"}}
	h := s.sectionHandler(Section{
		Path: "reports",
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			gotBase = SectionPath(r)
		}),
	})

	for reqPath, want := range map[string]string{
		"/x/reports/":            "/",
		"/x/reports/refresh":     "/refresh",
		"/x/reports/assets/a.js": "/assets/a.js",
	} {
		gotPath, gotBase = "", ""
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, reqPath, nil))
		if gotPath != want {
			t.Errorf("request %s: handler saw path %q, want %q", reqPath, gotPath, want)
		}
		if gotBase != "/admin/x/reports/" {
			t.Errorf("request %s: SectionPath = %q, want /admin/x/reports/", reqPath, gotBase)
		}
	}

	if got := SectionPath(httptest.NewRequest(http.MethodGet, "/", nil)); got != "" {
		t.Errorf("SectionPath outside a section = %q, want empty", got)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x/reports?tab=2", nil))
	if rec.Code != http.StatusMovedPermanently {
		t.Fatalf("bare section URL: got status %d, want 301", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/admin/x/reports/?tab=2" {
		t.Errorf("bare section URL redirected to %q, want /admin/x/reports/?tab=2", loc)
	}
}

// fillNavCounts must store each counting link's number and, on a failed
// count, log and leave zero — the built-in counts' contract.
func TestFillNavCounts(t *testing.T) {
	s := &server{deps: Deps{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}}
	links := []navLink{
		{Label: "Inventory", HasCount: true, count: func(context.Context) (int, error) { return 42, nil }},
		{Label: "Stickers"}, // no NavCount: untouched
		{Label: "Broken", HasCount: true, count: func(context.Context) (int, error) { return 9, errors.New("boom") }},
	}
	s.fillNavCounts(context.Background(), links)

	if links[0].Count != 42 {
		t.Errorf("Inventory count = %d, want 42", links[0].Count)
	}
	if links[1].HasCount || links[1].Count != 0 {
		t.Errorf("countless link = %+v, want untouched", links[1])
	}
	if !links[2].HasCount || links[2].Count != 0 {
		t.Errorf("failed count link = %+v, want HasCount with zero", links[2])
	}
}

// A section's NavAfter must place its link directly under the named
// built-in sidebar entry, in registration order; sections without one
// keep the trailing position, after the built-in entries.
func TestNavAfterPlacement(t *testing.T) {
	templates := parseTemplates()
	tmpl, ok := templates["custom"]
	if !ok {
		t.Fatal("no custom template in the parsed set")
	}

	data := templateData{
		AdminPath:    "/admin",
		User:         &auth.User{Name: "Pat", Role: auth.RoleSuperadmin},
		PagesEnabled: true,
		NavSections: []navLink{
			{URL: "/admin/x/inventory/", Label: "Inventory", After: "dashboard", HasCount: true, Count: 42},
			{URL: "/admin/x/stickers/", Label: "Stickers", After: "dashboard"},
			{URL: "/admin/x/exports/", Label: "Exports", After: "media"},
			{URL: "/admin/x/reports/", Label: "Reports"},
		},
	}

	var out strings.Builder
	if err := tmpl.ExecuteTemplate(&out, "layout", data); err != nil {
		t.Fatalf("rendering layout: %v", err)
	}
	html := out.String()

	// The sidebar's labels, in the order they must appear. Media itself is
	// disabled (MediaEnabled false), so Exports also proves an anchored
	// link holds its position when the anchor entry is hidden.
	order := []string{"Dashboard", "Inventory", "Stickers", "Pages", "Exports", "Snippets", "Users", "Reports", "Public Site"}
	last := -1
	for _, label := range order {
		i := strings.Index(html, ">"+label+"</span>")
		if i < 0 {
			t.Fatalf("rendered sidebar missing %q:\n%s", label, html)
		}
		if i < last {
			t.Errorf("sidebar order wrong: %q appears before the entry preceding it in %v", label, order)
		}
		last = i
	}

	// A counting link gets the built-in entries' leader and number; a
	// countless one stays a bare label.
	if !strings.Contains(html, `>Inventory</span><span class="cms-nav-leader"></span><span class="cms-nav-count">42</span>`) {
		t.Errorf("Inventory link missing its leader and count:\n%s", html)
	}
	if !strings.Contains(html, `>Stickers</span></a>`) {
		t.Errorf("Stickers link should have no leader or count:\n%s", html)
	}
}

// The custom template must wrap host HTML unescaped in the admin layout,
// and the layout must render the registered nav links.
func TestCustomTemplateRendersHostHTML(t *testing.T) {
	templates := parseTemplates()
	tmpl, ok := templates["custom"]
	if !ok {
		t.Fatal("no custom template in the parsed set")
	}

	data := templateData{
		AdminPath: "/admin",
		User:      &auth.User{Name: "Pat", Role: auth.RoleEditor},
		CSRFToken: "tok",
		Title:     "Reports",
		Body:      template.HTML(`<h1>Reports</h1><p class="host">42 visits</p>`),
		NavSections: []navLink{
			{URL: "/admin/x/reports/", Label: "Reports"},
			{URL: "/admin/x/stickers/", Label: "Stickers", Confirm: `Print them all? A "big" PDF.`},
		},
	}

	var out strings.Builder
	if err := tmpl.ExecuteTemplate(&out, "layout", data); err != nil {
		t.Fatalf("rendering custom page: %v", err)
	}
	html := out.String()

	for _, want := range []string{
		`<p class="host">42 visits</p>`,              // body inserted unescaped
		`<title>Reports — CMS</title>`,               // title block
		`href="/admin/x/reports/"`,                   // nav link target
		`<span class="cms-nav-label">Reports</span>`, // nav link label
		`href="/admin/static/admin.css"`,             // standard chrome
		// A Confirm section's link asks first, its message escaped into
		// the attribute admin.js reads.
		` data-confirm="Print them all? A &#34;big&#34; PDF." href="/admin/x/stickers/"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("rendered page missing %q:\n%s", want, html)
		}
	}
	if strings.Contains(html, "&lt;h1&gt;") {
		t.Error("host body was HTML-escaped")
	}
}
