package admin

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/tsawler/cms/auth"
	"github.com/tsawler/cms/internal/sessiondata"
)

const (
	sessionKeyUserID = sessiondata.KeyUserID
	sessionKeyCSRF   = sessiondata.KeyCSRF
	sessionKeyFlash  = sessiondata.KeyFlash

	// The two-factor login's pending state: who passed the password step
	// (never sessionKeyUserID — pending is not logged in), whether they
	// ticked "remember me", and when the challenge lapses. Admin-only, so
	// not in sessiondata: the public handler has no business reading them.
	sessionKey2FAUserID   = "cms2FAUserID"
	sessionKey2FARemember = "cms2FARemember"
	sessionKey2FAExpires  = "cms2FAExpires"

	// A generated-but-unconfirmed enrollment secret on the settings page.
	// It only moves to the database once a live code has confirmed the
	// authenticator app holds it too.
	sessionKeyTOTPSetup = "cmsTOTPSetupSecret"

	// The masquerade trail: while a superadmin works as another user,
	// sessionKeyUserID carries the target and this key parks the owner's
	// real ID for masqueradeExit to restore. Admin-only for the same
	// reason as the 2FA keys — the public handler follows
	// sessionKeyUserID and needs no say in who the session really is.
	sessionKeyMasqueradeFrom = "cmsMasqueradeFrom"
)

// throttleLimits: what the three counters allow, and why.
//
// The per-source counter is the tight one, and on its own it is not a cap
// on anything. Its key names an account *and* where the attempt came
// from, so an attacker with a range of addresses to spend — a /64 of IPv6
// is free — gets the whole allowance again from each one. That is fine
// as far as it goes: most guessing comes from somewhere, and five tries
// is a short leash. It is just not a limit on how often an account can be
// guessed at, which is the thing the two account counters supply.
//
// perAccountPasswordLimit is deliberately loose. Any per-account limit is
// also a way to lock somebody out — an attacker who knows an admin's
// address can hold the account shut by failing on purpose — so this is
// set where no human lands on it (nobody mistypes a password 25 times in
// a quarter of an hour) and an attacker has to keep up 100 failures an
// hour, against one named account, to hold the lock. That is a trade, not
// a free win: it buys a brake on distributed guessing and it sells a
// nuisance. The CAPTCHA, when configured, is the better answer to the
// same attack and does not sell anything.
//
// perAccountCodeLimit is the strict one, and it is the reason this whole
// change exists. A six-digit code has a million values and three of them
// are live at any moment, so a guess lands with probability 3e-6 and it
// takes about 231,000 of them to reach even odds. Unmetered — which is
// what a per-source counter is, to anyone with addresses to spend — that
// is a few days of traffic. At ten an hour... at ten per window, which is
// 960 a day, it is about eight months of sustained, continuously logged
// failure against an account whose password the attacker must already
// know. The lockout objection barely applies here for that same reason:
// anyone in a position to trip this limit has the password already, and
// shutting the account is the right answer rather than the harm.
const (
	throttleWindow = 15 * time.Minute

	perSourceLimit          = 5
	perAccountPasswordLimit = 25
	perAccountCodeLimit     = 10
)

// clientIP is where the request came from, for the throttle counters that
// key on a source.
//
// Deps.ClientIPHeader names the header a trusted proxy sets; without it
// the address comes off the connection, which behind a proxy is the
// proxy. That case is not merely imprecise, it inverts the counter: every
// visitor shares one key, so five failures against a known address lock
// that account out for everybody, and meanwhile a real attacker gets no
// separation at all. Naming the header is how a deployment says which
// answer to believe.
//
// An absent or empty header falls back to the connection rather than to a
// blank key, so a proxy that is misconfigured (or a health check that
// bypasses it) degrades to the old behaviour instead of collapsing every
// source into one bucket.
func (s *server) clientIP(r *http.Request) string {
	if h := s.deps.ClientIPHeader; h != "" {
		if v := r.Header.Get(h); v != "" {
			// X-Forwarded-For is a list, appended to hop by hop. The
			// rightmost entry is the one the nearest proxy wrote and is
			// the only one it vouches for; everything left of it is
			// whatever the client claimed. Single-value headers
			// (CF-Connecting-IP and friends) have no comma and fall
			// through unchanged.
			if i := strings.LastIndexByte(v, ','); i >= 0 {
				v = v[i+1:]
			}
			if ip := strings.TrimSpace(v); ip != "" {
				return ip
			}
		}
	}
	return remoteIP(r)
}

// userCacheKey types the request-context slot holding the memo below.
type userCacheKey struct{}

// userCache is one request's answer to "who is this?", so that asking
// repeatedly costs one lookup rather than one each.
//
// loaded is separate from user because nil is a real answer — a session
// naming an account that has been deleted or deactivated — and re-asking
// the database to be told nil again is the case worth avoiding most.
//
// id records which session user the answer belongs to, so a request that
// changes who it is signed in as does not keep the old one. Nothing does
// that and then renders today (masquerade, login and logout all redirect),
// but the memo should not be the reason that has to stay true.
//
// One request is one goroutine, so there is no lock here. A handler that
// wants the user from a goroutine of its own should read it before
// starting one.
type userCache struct {
	id     int64
	user   *auth.User
	loaded bool
}

// withUserCache installs the memo. It sits at the top of the admin chain,
// so every route — including the ones outside requireUser, like the login
// and password-reset forms — gets one.
func (s *server) withUserCache(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userCacheKey{}, &userCache{})))
	})
}

// currentUser returns the logged-in, active user for the request, or nil.
//
// It is asked several times over one request — by the middleware that
// gates the route, by the handler, by the template data, by whatever
// checks a permission along the way — and each answer used to be a pair
// of queries: the row, then its grants. A page behind two middleware
// layers spent ten queries establishing one fact. The answer is now
// remembered for the request that asked for it.
func (s *server) currentUser(r *http.Request) *auth.User {
	id := s.deps.Sessions.GetInt64(r.Context(), sessionKeyUserID)
	if id == 0 {
		return nil
	}
	// The session read above is in memory and has to happen anyway, so
	// checking it against the memo costs nothing and keeps the memo from
	// outliving the session state it describes.
	cache, _ := r.Context().Value(userCacheKey{}).(*userCache)
	if cache != nil && cache.loaded && cache.id == id {
		return cache.user
	}
	u := s.loadSessionUser(r, id)
	if cache != nil {
		cache.id, cache.user, cache.loaded = id, u, true
	}
	return u
}

// loadSessionUser reads the account a session names, or nil when there is
// no usable one: no such row, or an account that has been switched off.
func (s *server) loadSessionUser(r *http.Request, id int64) *auth.User {
	u, err := s.deps.Users.GetByID(r.Context(), id)
	if err != nil {
		if !errors.Is(err, auth.ErrNotFound) {
			s.deps.Logger.Error("cms admin: loading session user", "err", err)
		}
		return nil
	}
	if !u.Active {
		return nil
	}
	return u
}

// siteLocked reports whether the site is closed to everyone but
// superadmins. Nil Deps.SiteLocked reads as open, so a host that never
// set it — and every test that builds a server by hand — behaves exactly
// as it did before the switch existed.
func (s *server) siteLocked(ctx context.Context) bool {
	return s.deps.SiteLocked != nil && s.deps.SiteLocked(ctx)
}

// lockedOut reports whether the site lock bars this user from the admin.
// Superadmins are who the lock is for, so they pass; everyone else is
// turned away, the same answer the public site gives them.
//
// The exception is a masquerading session. Only a superadmin can start
// one, and while it runs the session reads as the target user — an
// editor, usually — so without this a superadmin who locked the site
// mid-masquerade could not reach the button that ends it. They keep the
// admin until they exit; the public site refuses them like the user they
// are wearing (see cms.Lockdown).
func (s *server) lockedOut(r *http.Request, u *auth.User) bool {
	if u == nil || u.Role.IsSuperadmin() || !s.siteLocked(r.Context()) {
		return false
	}
	return s.deps.Sessions.GetInt64(r.Context(), sessionKeyMasqueradeFrom) == 0
}

// requireUser redirects to the login page when no active user is logged in,
// and turns away everyone but superadmins while the site is locked.
func (s *server) requireUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := s.currentUser(r)
		if u == nil {
			http.Redirect(w, r, s.deps.AdminPath+"/login", http.StatusSeeOther)
			return
		}
		// A session that predates the lock — an editor who was already
		// working when it was thrown — stops here, at the first request
		// after it. The session itself is left alone: the site opening
		// again should not mean everybody logging back in.
		if s.lockedOut(r, u) {
			s.renderLocked(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// renderLocked answers a signed-in non-superadmin while the site is
// locked. 503 rather than 403: nothing is wrong with their account and
// nothing about their permissions has changed — the site is shut, and it
// will open again.
func (s *server) renderLocked(w http.ResponseWriter, r *http.Request) {
	data := s.newTemplateData(r)
	// No nav: the session is real, but every link it would draw leads
	// somewhere this user is about to be refused again.
	data.User = nil
	data.Error = s.tr(r, "This account cannot be used while the site is closed.")
	s.render(w, http.StatusServiceUnavailable, "login", data)
}

// requireAdmin responds 403 unless the logged-in user has the admin role.
// It must be nested inside requireUser.
func (s *server) requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := s.currentUser(r)
		if u == nil || !u.Role.IsAdmin() {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// requireSuperadmin responds 403 unless the logged-in user has the
// superadmin role. It must be nested inside requireUser.
func (s *server) requireSuperadmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := s.currentUser(r)
		if u == nil || !u.Role.IsSuperadmin() {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// requireGrant is requireAnyPerm without the admin shortcut: the permission
// must be granted explicitly whatever the role, and only superadmin
// passes for free. It gates the sections that declare AdminsNeedGrant.
func (s *server) requireGrant(p auth.Permission) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !s.currentUser(r).HasGrant(p) {
				http.Error(w, "Forbidden", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// mediaPermissions is every permission that opens the media library: the
// CMS's own content grants, plus whichever host permissions declared
// GrantsMedia.
//
// PermUsers is deliberately absent. Managing accounts is not editing
// content, and a user manager who holds nothing else has no page, post, or
// record to put a picture on.
func (s *server) mediaPermissions() []auth.Permission {
	perms := []auth.Permission{auth.PermPages, auth.PermBlogs, auth.PermNews}
	for _, d := range s.deps.Permissions {
		if d.GrantsMedia {
			perms = append(perms, d.Key)
		}
	}
	return perms
}

// canUseMedia reports whether this request may reach the media library.
// Admin roles pass on Can alone, as they do everywhere — an admin already
// holds the content grants implicitly, so gating them here would be a
// distinction the rest of the product does not draw.
func (s *server) canUseMedia(r *http.Request) bool {
	return s.currentUser(r).CanAny(s.mediaPermissions()...)
}

// requireMedia gates the media library on any of mediaPermissions.
func (s *server) requireMedia(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.canUseMedia(r) {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// requireAnyPerm builds middleware that responds 403 unless the logged-in
// user holds at least one of the permissions (admin roles hold every
// permission). It must be nested inside requireUser.
//
// Variadic because some areas are unlocked by more than one grant — the
// shared Blog & News section needs either feed, not both — and naming one
// permission is the ordinary case rather than a different function.
func (s *server) requireAnyPerm(perms ...auth.Permission) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !s.currentUser(r).CanAny(perms...) {
				http.Error(w, "Forbidden", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// Request body ceilings, applied by the csrf middleware — see readToken
// for why they have to live there rather than in the handlers.
const (
	// DefaultMaxRequestBytes bounds an authenticated unsafe request when
	// the host sets no Deps.MaxRequestBytes and there is no media manager
	// to size one from.
	DefaultMaxRequestBytes = 64 << 20

	// maxAnonRequestBytes bounds one that arrives without a session user.
	// Every form a signed-out visitor can legitimately post — login, the
	// two-factor code, forgot-password, reset-password — is a handful of
	// short fields, so this is generous by three orders of magnitude and
	// still refuses the interesting case: a multipart body aimed at
	// /admin/login by someone with no account at all.
	maxAnonRequestBytes = 1 << 20

	// csrfParseMemory is what the middleware keeps in memory while
	// parsing; anything beyond it goes to a temp file, bounded in total by
	// the ceilings above. Matches the net/http default that
	// PostFormValue used to apply implicitly, so upload behaviour is
	// unchanged in every respect except being bounded.
	csrfParseMemory = 32 << 20
)

// maxRequestBytes is the ceiling for this request's body.
//
// Signed-in staff may post an inventory upload of many photos at once;
// nobody signed out has any business sending more than a form's worth of
// fields. Reading the session directly rather than calling currentUser
// keeps this off the database — the answer only has to be "is anyone
// logged in", and requireUser does the real check downstream.
func (s *server) maxRequestBytes(r *http.Request) int64 {
	if s.deps.Sessions.GetInt64(r.Context(), sessionKeyUserID) == 0 {
		return maxAnonRequestBytes
	}
	if s.deps.MaxRequestBytes > 0 {
		return s.deps.MaxRequestBytes
	}
	// Sized from the media limits when the host configured them, so
	// raising MaxVideoBytes does not silently need this raised too.
	if s.deps.Media != nil {
		if limit := s.uploadLimit(); limit > DefaultMaxRequestBytes {
			return limit + (1 << 20) // multipart framing and sibling fields
		}
	}
	return DefaultMaxRequestBytes
}

// readToken pulls the CSRF token from the header, or failing that from the
// form body — and is where the request body's ceiling is established,
// because this is the first thing in the whole chain to touch it.
//
// That ordering is the reason it matters. PostFormValue on a multipart
// request calls ParseMultipartForm, which streams the *entire* body,
// spilling past its memory limit into temp files with no cap on the
// total. Worse, ParseMultipartForm returns nil immediately once
// r.MultipartForm is set, so every limit a handler sets afterwards —
// http.MaxBytesReader on a body already consumed, ParseMultipartForm with
// a smaller memory bound — is dead code that reads as if it works.
// Bounding the body has to happen here or it does not happen at all.
//
// Reports false when the body exceeded its ceiling, having already
// answered 413: an oversized upload deserves a better answer than the
// "invalid or missing CSRF token" it would otherwise collect, since the
// token was neither.
func (s *server) readToken(w http.ResponseWriter, r *http.Request) (string, bool) {
	// Capped first, and on every path. The header path does not read the
	// body here — that is left to the handler — but it still leaves with
	// a bounded one, so the ceiling is a property of the request rather
	// than of which way the token happened to arrive. Handlers used to
	// set their own, which worked for the header path and was inert for
	// the form path, since by then the body had already been read.
	r.Body = http.MaxBytesReader(w, r.Body, s.maxRequestBytes(r))

	if sent := r.Header.Get("X-CSRF-Token"); sent != "" {
		return sent, true
	}

	var err error
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		err = r.ParseMultipartForm(csrfParseMemory)
	} else {
		err = r.ParseForm()
	}
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			http.Error(w, "Request body too large", http.StatusRequestEntityTooLarge)
			return "", false
		}
		// Any other parse failure is a malformed body, which the token
		// comparison below refuses on its own.
	}
	return r.PostFormValue("csrf_token"), true
}

// csrf implements a session-bound synchronizer token. Safe methods ensure a
// token exists; unsafe methods must echo it back in the csrf_token form
// field or the X-CSRF-Token header.
func (s *server) csrf(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, err := sessiondata.EnsureCSRF(r.Context(), s.deps.Sessions)
		if err != nil {
			s.serverError(w, err)
			return
		}

		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
		default:
			sent, ok := s.readToken(w, r)
			if !ok {
				return
			}
			if subtle.ConstantTimeCompare([]byte(sent), []byte(token)) != 1 {
				http.Error(w, "Invalid or missing CSRF token", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// secureHeaders sets conservative security headers on every admin response.
// Page previews are exempt from the CSP: they render the host site's own
// templates, which may legitimately load framework CSS/JS from CDNs.
// capOrigin, when non-empty, is the Cap CAPTCHA server's origin; the login
// page loads the widget script from it, the widget calls its challenge API
// and runs a WASM solver in blob workers, so the CSP must admit all of that.
func secureHeaders(capOrigin string) func(http.Handler) http.Handler {
	// img-src allows https so the media library can show images served
	// from the site's bucket/CDN; media-src the same, so its inspector can
	// play videos, plus blob: for the local files the uploader reads a
	// poster frame out of before they are anywhere a URL can reach.
	const plain = "default-src 'self'; img-src 'self' https: data:; " +
		"media-src 'self' https: blob:; frame-ancestors 'none'"

	// loginCSP is the policy for the login page, and only that page. The
	// CAPTCHA widget needs concessions the rest of the admin does not, so
	// they are confined to the one page that loads it — which renders no
	// user-supplied content, only a form and translated labels.
	//
	//   nonce         lets the page carry an inline <script>, and is handed
	//                 to the widget to stamp on the inline script it runs
	//                 inside its about:srcdoc instrumentation frame. That
	//                 frame inherits this policy, so without a nonce the
	//                 script is blocked and the solver never finishes.
	//   unsafe-eval   the instrumentation script's anti-tamper checks call
	//                 eval() and new Function(). 'wasm-unsafe-eval' covers
	//                 WebAssembly only and a nonce does not grant eval, so
	//                 there is no narrower directive that admits it. Turn
	//                 instrumentation off for the site key in the Cap
	//                 dashboard and this is no longer needed.
	//   unsafe-inline the widget injects an inline <style> block; styles
	//                 can't run code, so this is a far smaller concession
	//                 than it would be for scripts.
	loginCSP := func(nonce string) string {
		return "default-src 'self'; " +
			"script-src 'self' 'nonce-" + nonce + "' 'wasm-unsafe-eval' 'unsafe-eval' " + capOrigin + "; " +
			"style-src 'self' 'unsafe-inline'; " +
			"connect-src 'self' " + capOrigin + "; " +
			"worker-src 'self' blob:; " +
			"img-src 'self' https: data:; frame-ancestors 'none'"
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("X-Frame-Options", "DENY")
			h.Set("Referrer-Policy", "same-origin")
			if strings.HasSuffix(r.URL.Path, "/preview") {
				// Previews render the host site's own templates, which may
				// legitimately load framework CSS/JS from CDNs.
				next.ServeHTTP(w, r)
				return
			}
			// Every page except the login form gets the strict policy —
			// the same one a deployment without CAPTCHA serves everywhere.
			if capOrigin == "" || !isLoginPath(r.URL.Path) {
				h.Set("Content-Security-Policy", plain)
				next.ServeHTTP(w, r)
				return
			}
			nonce, err := newScriptNonce()
			if err != nil {
				// Failing closed is right: serving the page with no nonce
				// would silently drop the CAPTCHA's script.
				http.Error(w, "Something went wrong.", http.StatusInternalServerError)
				return
			}
			h.Set("Content-Security-Policy", loginCSP(nonce))
			next.ServeHTTP(w, r.WithContext(withScriptNonce(r.Context(), nonce)))
		})
	}
}

// isLoginPath reports whether p addresses the login form — the only page
// that loads the CAPTCHA widget, and so the only one whose policy is
// relaxed for it. Matched by suffix because the admin router may be mounted
// with or without its prefix stripped.
// isLoginPath matches the pages that embed the CAPTCHA widget: the login
// form and the forgot-password form. The second is there because it is an
// email-sending endpoint — exactly where a CAPTCHA earns its keep — and it
// needs the same CSP concessions to load the same widget.
func isLoginPath(p string) bool {
	return p == "/login" || strings.HasSuffix(p, "/login") ||
		p == "/forgot-password" || strings.HasSuffix(p, "/forgot-password")
}

// scriptNonceKey types the request-context slot holding the CSP nonce.
type scriptNonceKey struct{}

// newScriptNonce returns a fresh nonce. A nonce is only worth anything if it
// is unguessable and never reused, so it is generated per response rather
// than per session.
//
// URL-safe base64 keeps the value to characters that need no HTML escaping:
// standard base64's "+" would render as "&#43;" in the script tag's nonce
// attribute, which browsers decode correctly but which makes the header and
// the markup look like they disagree.
func newScriptNonce() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

func withScriptNonce(ctx context.Context, nonce string) context.Context {
	return context.WithValue(ctx, scriptNonceKey{}, nonce)
}

// scriptNonce returns the CSP nonce for this response, or "" when the
// policy carries none (no CAPTCHA configured, or a preview).
func scriptNonce(r *http.Request) string {
	nonce, _ := r.Context().Value(scriptNonceKey{}).(string)
	return nonce
}
