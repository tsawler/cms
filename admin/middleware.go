package admin

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"net/http"

	"github.com/tsawler/cms/auth"
)

const (
	sessionKeyUserID = "cmsUserID"
	sessionKeyCSRF   = "cmsCSRFToken"
	sessionKeyFlash  = "cmsFlash"
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
		if u == nil || u.Role != auth.RoleAdmin {
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
		ctx := r.Context()
		token := s.deps.Sessions.GetString(ctx, sessionKeyCSRF)
		if token == "" {
			buf := make([]byte, 32)
			if _, err := rand.Read(buf); err != nil {
				s.serverError(w, err)
				return
			}
			token = hex.EncodeToString(buf)
			s.deps.Sessions.Put(ctx, sessionKeyCSRF, token)
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
func secureHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Content-Security-Policy", "default-src 'self'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}
