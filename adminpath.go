package cms

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/tsawler/cms/editor"
	"github.com/tsawler/cms/media"
)

// adminPathSegmentRE is one segment of an admin path: RFC 3986 unreserved
// characters, the same alphabet admin section paths use. Nothing here
// needs escaping, so the path the admin links to is byte-for-byte the
// path a browser requests.
var adminPathSegmentRE = regexp.MustCompile(`^[A-Za-z0-9._~-]+$`)

// cmsNamespace is the first path segment the public side keeps for its
// own addresses — proxied media, the editor bundle, the generated content
// CSS, and whatever joins them later. An admin mounted there would be
// handed those requests first.
const cmsNamespace = "cms"

// normalizeAdminPath applies the AdminPath default and canonical form:
// a leading slash, no trailing slash. "admin", "/admin/", and "" all
// become "/admin". A bare "/" stays "/" for validateAdminPath to refuse,
// rather than quietly meaning the default.
func normalizeAdminPath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return "/admin"
	}
	return "/" + strings.Trim(p, "/")
}

// validateAdminPath checks a normalized AdminPath against the addresses
// the public side answers. Handler hands everything under AdminPath to
// the admin before Pages sees it, so an admin path that overlaps one of
// those does not fail loudly — it quietly takes the site's media, feeds,
// or translated pages with it. Better to refuse at startup.
//
// Only addresses the CMS itself owns are checked. Pages editors create
// are caught when they are saved (see admin's slug checks), and a host's
// own routes are the host's to keep apart.
func validateAdminPath(cfg Config) error {
	p := cfg.AdminPath
	if p == "/" {
		return fmt.Errorf("cms: AdminPath %q would put the admin over the whole site", p)
	}
	segs := strings.Split(strings.TrimPrefix(p, "/"), "/")
	for _, seg := range segs {
		if seg == "." || seg == ".." || !adminPathSegmentRE.MatchString(seg) {
			return fmt.Errorf("cms: AdminPath %q is not a usable URL path "+
				"(want segments of letters, digits, \"-\", \".\", \"_\", or \"~\", e.g. \"/cms-admin\")", p)
		}
	}

	first := segs[0]
	switch {
	case first == cmsNamespace:
		return fmt.Errorf("cms: AdminPath %q is inside %s, which the site uses for media and editor assets (%s, %s); "+
			"pick another, e.g. \"/cms-admin\"", p, "/"+cmsNamespace+"/", media.ProxyPathPrefix, editor.PathPrefix)
	case p == robotsPath || p == sitemapPath:
		return fmt.Errorf("cms: AdminPath %q is the site's %s", p, p)
	case cfg.PostTemplate.File != "" && (first == "blog" || first == "news"):
		return fmt.Errorf("cms: AdminPath %q is inside /%s/, where the site serves its posts", p, first)
	}
	for _, code := range cfg.Locales[1:] {
		if first == code {
			return fmt.Errorf("cms: AdminPath %q starts with the locale code %q, which prefixes the site's translated pages", p, code)
		}
	}
	if cfg.SearchTemplate.File != "" {
		adminSlug := strings.TrimPrefix(p, "/")
		if cfg.SearchPath == adminSlug || strings.HasPrefix(cfg.SearchPath, adminSlug+"/") {
			return fmt.Errorf("cms: AdminPath %q would take over the search page at /%s", p, cfg.SearchPath)
		}
	}
	return nil
}
