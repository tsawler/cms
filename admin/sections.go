// Host-registered admin sections: custom pages a deployment mounts inside
// the admin area. Sections run behind the admin's session, CSRF, and
// login middleware, and their handlers use the package-level helpers
// (UserFrom, CSRFToken, SetFlash, RenderPage) to integrate with the
// admin chrome.

package admin

import (
	"context"
	"fmt"
	"html/template"
	"net/http"
	"regexp"

	"github.com/tsawler/cms/auth"
)

// SectionPathPrefix is the URL segment custom sections are mounted under,
// between the admin path and the section's own path: a section with Path
// "reports" serves {AdminPath}/x/reports. The namespace keeps host
// sections from ever colliding with built-in admin routes, present or
// future.
const SectionPathPrefix = "/x"

// Section is one host-registered admin extension: an http.Handler mounted
// inside the admin's middleware chain (session, CSRF validation, security
// headers, and login requirement), with an optional link in the admin's
// top navigation bar.
type Section struct {
	// Path is the URL segment the section is mounted under: the section
	// root is served at {AdminPath}/x/{Path}/ (the bare URL without the
	// trailing slash redirects there, so relative links inside the
	// section resolve under it). One path segment of RFC 3986 unreserved
	// characters (letters, digits, "-", ".", "_", "~").
	Path string

	// NavLabel is the section's link text in the admin top bar. Empty
	// means no nav link; the section is still routable.
	NavLabel string

	// NavAfter names the built-in sidebar entry this section's link
	// follows: "dashboard", "pages", "posts", "media", "snippets", or
	// "users". Empty keeps the default placement, after the built-in
	// entries. Sections naming the same anchor keep their registration
	// order, and the link holds its position even when the anchor itself
	// is hidden from the user. Ignored when NavLabel is empty.
	NavAfter string

	// NavCount, when non-nil, supplies the number shown beside the nav
	// link, with the same leader line and count style as the built-in
	// entries ("Inventory ····· 42"). Called with the request context on
	// every admin page render whose sidebar shows the link, so it should
	// be a cheap query — the built-in counts run the same way. An error
	// is logged and the count renders as zero, exactly as a failed
	// built-in count does. Ignored when NavLabel is empty.
	NavCount func(ctx context.Context) (int, error)

	// Confirm, when non-empty, makes the nav link ask before it
	// navigates: clicking it opens the admin's shared confirmation
	// dialog with this message ("Question? Detail." — the question
	// becomes the heading), and the browser only follows the link on a
	// yes. For nav entries that do something — generate a file, run a
	// job — rather than open a page. Ignored when NavLabel is empty.
	Confirm string

	// AdminOnly restricts the section to users with the admin role.
	// Editors receive 403 and don't see the nav link.
	AdminOnly bool

	// SuperadminOnly restricts the section to superadmins. Everyone
	// else — admins included — receives 403 and doesn't see the nav
	// link. Subsumes AdminOnly; setting both is allowed and redundant.
	SuperadminOnly bool

	// Permission, when non-empty, restricts the section to users holding
	// the named permission (admin roles hold every permission). Naming a
	// built-in permission reuses it; any other key declares a custom
	// permission that appears as a grant checkbox on the user form,
	// labelled with NavLabel. Composes with AdminOnly: both must pass.
	Permission string

	// AdminsNeedGrant makes Permission an explicit grant for the admin
	// role too: admins see and open the section only when the permission
	// is ticked on their user page, exactly as editors do. Superadmins
	// hold everything, as always. Without it, Permission keeps its usual
	// meaning — admins hold every permission implicitly. Requires
	// Permission to be set.
	AdminsNeedGrant bool

	// Dashboard, when non-nil, puts a card for this section on the admin
	// dashboard, linking to the section root. Host cards render ahead of
	// the built-in cards (which are superadmin-only), in registration
	// order, and a card is shown exactly when the section's nav link
	// would be: SuperadminOnly, AdminOnly, Permission, and
	// AdminsNeedGrant all apply.
	Dashboard *DashboardCard

	// Handler serves the section's requests. The mount prefix is
	// stripped: it sees "/" at the section root and may serve its own
	// sub-routes and static assets beneath it. Requests only reach the
	// handler with a logged-in user, and unsafe methods (POST, PUT, ...)
	// have already passed CSRF validation — forms need only include
	// CSRFToken(r) as the csrf_token field.
	Handler http.Handler
}

// DashboardCard is a section's card on the admin dashboard: a heading, a
// one-line description, and a number. The number is the card's point — a
// dashboard answers "what needs my attention?", so give it the count that
// asks for attention (items pending, unread submissions), which is not
// always the nav link's "how many are there".
type DashboardCard struct {
	// Title is the card's heading. Empty uses the section's NavLabel;
	// one of the two must be set.
	Title string

	// Description is the card's one-line explanation, shown under the
	// title. Host text, rendered as-is.
	Description string

	// Count supplies the card's number. Called once per dashboard render
	// with the request context; an error is logged and the count renders
	// as zero, exactly as a failed NavCount does. Nil falls back to the
	// section's NavCount, and a card with neither shows no number.
	Count func(ctx context.Context) (int, error)

	// Note, when non-nil, supplies a short dynamic line rendered under
	// the description — the card's freshness or urgency in the host's
	// words ("Oldest unhandled: 2 days"). Called once per dashboard
	// render; returning "" shows nothing, and an error is logged and
	// shows nothing, so a note never blocks the page.
	Note func(ctx context.Context) (string, error)
}

// PermissionDef declares a custom permission to the admin so the user
// form can offer it as a grant checkbox: Key is what handlers check
// with auth.User.Can, Label is the checkbox text. Sections that name a
// Permission are declared automatically; cms.Config.Permissions is for
// permissions not tied to a section.
//
// AdminsNeedGrant marks a permission that gates the admin role too (see
// Section.AdminsNeedGrant); the user form annotates its checkbox so
// whoever is granting knows it binds admins as well as editors. For a
// permission carried by a section, it must match the section's own flag
// — two answers to "does this bind admins?" would be a configuration
// error, and cms.New refuses it.
type PermissionDef struct {
	Key             auth.Permission
	Label           string
	AdminsNeedGrant bool

	// GrantsMedia opens the media library to holders of this permission.
	//
	// The library is shared: one bucket behind every section that puts a
	// picture on the site, and a bulk delete inside it reaches all of
	// them. So it is not open to anyone merely signed in — it is open to
	// people who edit something that carries media. The CMS's own pages,
	// blogs, and news imply that on their own; a host section has to say
	// so, because only the host knows whether its records have pictures in
	// them.
	GrantsMedia bool
}

var sectionPathRE = regexp.MustCompile(`^[A-Za-z0-9._~-]+$`)

// navAnchors are the built-in sidebar entries a Section.NavAfter may name.
var navAnchors = map[string]bool{
	"dashboard": true,
	"pages":     true,
	"posts":     true,
	"media":     true,
	"snippets":  true,
	"users":     true,
}

// sectionHandler wraps a host-registered section for mounting: it enforces
// the role gates when asked, exposes the server to the package helpers,
// strips the mount prefix so the handler sees section-relative paths, and
// canonicalizes the bare section URL to its trailing-slash form so the
// handler's relative links resolve under the section.
func (s *server) sectionHandler(sec Section) http.Handler {
	prefix := SectionPathPrefix + "/" + sec.Path
	base := s.deps.AdminPath + prefix + "/"
	inner := http.StripPrefix(prefix, sec.Handler)

	var h http.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == prefix {
			url := base
			if r.URL.RawQuery != "" {
				url += "?" + r.URL.RawQuery
			}
			http.Redirect(w, r, url, http.StatusMovedPermanently)
			return
		}
		inner.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), sectionBaseCtxKey{}, base)))
	})

	h = s.withServer(h)
	if sec.Permission != "" {
		if sec.AdminsNeedGrant {
			h = s.requireGrant(auth.Permission(sec.Permission))(h)
		} else {
			h = s.requirePerm(auth.Permission(sec.Permission))(h)
		}
	}
	if sec.AdminOnly {
		h = s.requireAdmin(h)
	}
	if sec.SuperadminOnly {
		h = s.requireSuperadmin(h)
	}
	return h
}

// ValidateSections checks host-registered sections for empty, malformed,
// or duplicate paths and nil handlers. cms.New calls it; it is exported
// for hosts that want to fail earlier.
func ValidateSections(sections []Section) error {
	seen := make(map[string]bool, len(sections))
	for _, sec := range sections {
		if !sectionPathRE.MatchString(sec.Path) {
			return fmt.Errorf("cms: admin section path %q must be one path segment of letters, digits, or - . _ ~", sec.Path)
		}
		if seen[sec.Path] {
			return fmt.Errorf("cms: duplicate admin section path %q", sec.Path)
		}
		seen[sec.Path] = true
		if sec.Handler == nil {
			return fmt.Errorf("cms: admin section %q has a nil Handler", sec.Path)
		}
		if sec.Permission != "" && !auth.ValidPermissionKey(sec.Permission) {
			return fmt.Errorf("cms: admin section %q permission %q must be a lowercase letter followed by lowercase letters, digits, hyphens, or underscores", sec.Path, sec.Permission)
		}
		if sec.AdminsNeedGrant && sec.Permission == "" {
			return fmt.Errorf("cms: admin section %q sets AdminsNeedGrant without a Permission to grant", sec.Path)
		}
		if sec.NavAfter != "" && !navAnchors[sec.NavAfter] {
			return fmt.Errorf("cms: admin section %q NavAfter %q is not a built-in nav entry (dashboard, pages, posts, media, snippets, users)", sec.Path, sec.NavAfter)
		}
		if sec.Dashboard != nil && sec.Dashboard.Title == "" && sec.NavLabel == "" {
			return fmt.Errorf("cms: admin section %q has a Dashboard card with no Title and no NavLabel to fall back on", sec.Path)
		}
	}
	return nil
}

// serverCtxKey carries the admin server into section handlers so the
// package-level helpers below can reach sessions and templates.
type serverCtxKey struct{}

// sectionBaseCtxKey carries the current section's browser-facing base URL
// into its handler, for SectionPath.
type sectionBaseCtxKey struct{}

// SectionPath returns the browser-facing base URL of the custom admin
// section serving this request, with a trailing slash — e.g.
// "/admin/x/reports/". Section handlers see mount-stripped paths, so a
// redirect after a POST must target this absolute URL, not a relative one:
//
//	http.Redirect(w, r, admin.SectionPath(r), http.StatusSeeOther)
//
// Append a segment for sub-routes: SectionPath(r) + "settings". Outside a
// section handler it returns "".
func SectionPath(r *http.Request) string {
	base, _ := r.Context().Value(sectionBaseCtxKey{}).(string)
	return base
}

// withServer makes the admin server available to the helpers from inside
// a section handler.
func (s *server) withServer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), serverCtxKey{}, s)))
	})
}

func serverFrom(r *http.Request) *server {
	s, _ := r.Context().Value(serverCtxKey{}).(*server)
	return s
}

// UserFrom returns the logged-in CMS user for a request served by a
// custom admin section. Inside a section handler it is never nil — the
// admin middleware has already required a login. Outside one it is nil.
func UserFrom(r *http.Request) *auth.User {
	if s := serverFrom(r); s != nil {
		return s.currentUser(r)
	}
	return nil
}

// CSRFToken returns the session's CSRF token for a request served by a
// custom admin section. Forms that POST back to the section must send it
// in a hidden csrf_token field (or an X-CSRF-Token header); the admin
// middleware rejects unsafe requests without it.
func CSRFToken(r *http.Request) string {
	if s := serverFrom(r); s != nil {
		return s.deps.Sessions.GetString(r.Context(), sessionKeyCSRF)
	}
	return ""
}

// SetFlash queues a one-time message shown at the top of the next admin
// page the user loads — the usual post/redirect/get confirmation. It is a
// no-op outside a section handler.
func SetFlash(r *http.Request, msg string) {
	if s := serverFrom(r); s != nil {
		s.flash(r, msg)
	}
}

// RenderPage writes a 200 response wrapping body in the standard admin
// chrome (top bar, navigation, flash messages, admin stylesheet). Body is
// trusted host HTML, inserted unescaped. For any other status code or a
// fully custom look, write the response directly instead.
//
// The admin serves a strict Content-Security-Policy with no unsafe-inline,
// so inline <script> and <style> in body are blocked by browsers; serve
// scripts and stylesheets as files from the section's own handler.
func RenderPage(w http.ResponseWriter, r *http.Request, title string, body template.HTML) {
	s := serverFrom(r)
	if s == nil {
		http.Error(w, "admin.RenderPage: not inside an admin section handler", http.StatusInternalServerError)
		return
	}
	data := s.newTemplateData(r)
	data.Title = title
	data.Body = body
	s.render(w, http.StatusOK, "custom", data)
}
