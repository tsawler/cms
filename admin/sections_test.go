package admin

import (
	"html/template"
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
	}
	if err := ValidateSections(valid); err != nil {
		t.Errorf("valid sections rejected: %v", err)
	}
	if err := ValidateSections(nil); err != nil {
		t.Errorf("nil sections rejected: %v", err)
	}

	bad := map[string][]Section{
		"empty path":      {{Path: "", Handler: noopHandler}},
		"slash in path":   {{Path: "a/b", Handler: noopHandler}},
		"space in path":   {{Path: "a b", Handler: noopHandler}},
		"escaping path":   {{Path: "../users", Handler: noopHandler}},
		"duplicate paths": {{Path: "x", Handler: noopHandler}, {Path: "x", Handler: noopHandler}},
		"nil handler":     {{Path: "x"}},
	}
	for name, sections := range bad {
		if err := ValidateSections(sections); err == nil {
			t.Errorf("%s: expected an error, got nil", name)
		}
	}
}

func TestNavSectionsFor(t *testing.T) {
	sections := []Section{
		{Path: "reports", NavLabel: "Reports", Handler: noopHandler},
		{Path: "hidden", Handler: noopHandler}, // no NavLabel: never in the nav
		{Path: "billing", NavLabel: "Billing", AdminOnly: true, Handler: noopHandler},
	}

	editor := navSectionsFor(sections, "/admin", false, "/")
	if len(editor) != 1 || editor[0].Label != "Reports" || editor[0].URL != "/admin/x/reports/" {
		t.Errorf("editor nav = %+v, want only Reports at /admin/x/reports/", editor)
	}

	admin := navSectionsFor(sections, "/admin", true, "/")
	if len(admin) != 2 || admin[1].Label != "Billing" || admin[1].URL != "/admin/x/billing/" {
		t.Errorf("admin nav = %+v, want Reports then Billing", admin)
	}

	// Viewing a section marks its own link, and only its own, active.
	viewing := navSectionsFor(sections, "/admin", true, "/x/billing/subpage")
	if viewing[0].Active || !viewing[1].Active {
		t.Errorf("active flags = %+v, want only Billing active", viewing)
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
	} {
		if !strings.Contains(html, want) {
			t.Errorf("rendered page missing %q:\n%s", want, html)
		}
	}
	if strings.Contains(html, "&lt;h1&gt;") {
		t.Error("host body was HTML-escaped")
	}
}
