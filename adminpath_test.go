package cms

import (
	"strings"
	"testing"
)

func TestNormalizeAdminPath(t *testing.T) {
	for in, want := range map[string]string{
		"":              "/admin",
		"/":             "/",
		"//":            "/",
		"  ":            "/admin",
		"/admin":        "/admin",
		"admin/":        "/admin",
		"/cms-admin/":   "/cms-admin",
		" site/manage ": "/site/manage",
	} {
		if got := normalizeAdminPath(in); got != want {
			t.Errorf("normalizeAdminPath(%q) = %q, want %q", in, got, want)
		}
	}
}

// validateAdminPath refuses the admin paths that would swallow an address
// the public site answers, and nothing else.
func TestValidateAdminPath(t *testing.T) {
	base := Config{
		Locales:        []string{"en", "fr"},
		SearchPath:     "search",
		PostTemplate:   PageTemplate{File: "post.gohtml"},
		SearchTemplate: PageTemplate{File: "search.gohtml"},
	}
	for _, ok := range []string{
		"/admin", "/cms-admin", "/cms_admin", "/site/manage", "/Admin",
		"/cmsadmin", "/blogs", "/french", "/search-admin", "/en",
		"/backend/v1.2", "/~staff",
	} {
		cfg := base
		cfg.AdminPath = ok
		if err := validateAdminPath(cfg); err != nil {
			t.Errorf("AdminPath %q: unexpected error %v", ok, err)
		}
	}
	for bad, mention := range map[string]string{
		"/":            "whole site",
		"/cms":         "/cms/",
		"/cms/admin":   "/cms/",
		"/robots.txt":  "robots.txt",
		"/sitemap.xml": "sitemap.xml",
		"/blog":        "posts",
		"/news/admin":  "posts",
		"/fr":          "locale",
		"/fr/admin":    "locale",
		"/search":      "search",
		"/a b":         "usable",
		"/admin//x":    "usable",
		"/admin/../x":  "usable",
		"/./admin":     "usable",
		"/admin%2Fx":   "usable",
		"/admin?x=1":   "usable",
		"/café":        "usable",
	} {
		cfg := base
		cfg.AdminPath = bad
		err := validateAdminPath(cfg)
		if err == nil {
			t.Errorf("AdminPath %q: expected an error", bad)
			continue
		}
		if !strings.Contains(err.Error(), mention) {
			t.Errorf("AdminPath %q: error %q does not mention %q", bad, err, mention)
		}
	}
}

// The blog/news and search refusals exist to protect those features, so a
// site that has them switched off may still use the addresses.
func TestValidateAdminPathFeatureOff(t *testing.T) {
	for _, p := range []string{"/blog", "/news", "/search"} {
		cfg := Config{AdminPath: p, Locales: []string{"en"}, SearchPath: "search"}
		if err := validateAdminPath(cfg); err != nil {
			t.Errorf("AdminPath %q with posts and search off: unexpected error %v", p, err)
		}
	}
}
