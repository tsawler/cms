package admin

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"

	"github.com/tsawler/cms/auth"
	"github.com/tsawler/cms/internal/sessiondata"
)

const (
	sessionKeyUserID = sessiondata.KeyUserID
	sessionKeyCSRF   = sessiondata.KeyCSRF
	sessionKeyFlash  = sessiondata.KeyFlash
)

// currentUser returns the logged-in, active user for the request, or nil.
func (s *server) currentUser(r *http.Request) *auth.User {
	id := s.deps.Sessions.GetInt64(r.Context(), sessionKeyUserID)
	if id == 0 {
		return nil
	}
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

// requireUser redirects to the login page when no active user is logged in.
func (s *server) requireUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.currentUser(r) == nil {
			http.Redirect(w, r, s.deps.AdminPath+"/login", http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r)
	})
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
			sent := r.Header.Get("X-CSRF-Token")
			if sent == "" {
				sent = r.PostFormValue("csrf_token")
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
	// from the site's bucket/CDN.
	const plain = "default-src 'self'; img-src 'self' https: data:; frame-ancestors 'none'"

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
func isLoginPath(p string) bool {
	return p == "/login" || strings.HasSuffix(p, "/login")
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
