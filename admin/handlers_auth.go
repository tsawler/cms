package admin

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/tsawler/cms/auth"
	"github.com/tsawler/cms/captcha"
)

func (s *server) loginForm(w http.ResponseWriter, r *http.Request) {
	if s.currentUser(r) != nil {
		http.Redirect(w, r, s.deps.AdminPath+"/", http.StatusSeeOther)
		return
	}
	s.render(w, http.StatusOK, "login", s.newTemplateData(r))
}

func (s *server) login(w http.ResponseWriter, r *http.Request) {
	email := strings.TrimSpace(r.PostFormValue("email"))
	password := r.PostFormValue("password")

	fail := func(status int, msg string) {
		data := s.newTemplateData(r)
		data.Error = msg
		s.render(w, status, "login", data)
	}

	throttleKey := strings.ToLower(email) + "|" + remoteIP(r)
	if s.throttle.Blocked(throttleKey) {
		fail(http.StatusTooManyRequests, "Too many failed attempts. Please wait a few minutes and try again.")
		return
	}

	// Honeypot: the field is visually hidden, so a value means a bot
	// filled the form. Answer exactly like a wrong password would.
	if r.PostFormValue("website") != "" {
		s.throttle.Fail(throttleKey)
		fail(http.StatusUnprocessableEntity, "That email and password combination didn't work.")
		return
	}

	if s.deps.Captcha != nil {
		token := r.PostFormValue(captcha.FieldName)
		if token == "" {
			fail(http.StatusUnprocessableEntity, "Please complete the verification challenge.")
			return
		}
		ok, err := s.deps.Captcha.Verify(r.Context(), token)
		if err != nil {
			// No verdict (Cap server unreachable): fail open so a Cap
			// outage cannot lock admins out — the login throttle still
			// blunts password guessing.
			s.deps.Logger.Warn("cms admin: captcha unavailable, skipping check", "err", err)
		} else if !ok {
			fail(http.StatusUnprocessableEntity, "Verification failed. Please try again.")
			return
		}
	}

	u, err := s.deps.Users.Authenticate(r.Context(), email, password)
	if errors.Is(err, auth.ErrInvalidCredentials) {
		s.throttle.Fail(throttleKey)
		fail(http.StatusUnprocessableEntity, "That email and password combination didn't work.")
		return
	}
	if err != nil {
		s.serverError(w, err)
		return
	}

	s.throttle.Reset(throttleKey)
	// A fresh session token on privilege change prevents session fixation.
	if err := s.deps.Sessions.RenewToken(r.Context()); err != nil {
		s.serverError(w, err)
		return
	}
	s.deps.Sessions.Put(r.Context(), sessionKeyUserID, u.ID)
	if r.PostFormValue("remember") == "1" {
		// Persistent cookie (survives browser restarts) with the
		// remember duration as both cookie and server-side deadline.
		s.deps.Sessions.RememberMe(r.Context(), true)
		s.deps.Sessions.SetDeadline(r.Context(), time.Now().Add(s.deps.RememberFor))
	}
	s.deps.Logger.Info("cms admin: login", "user", u.Email)
	http.Redirect(w, r, s.deps.AdminPath+"/", http.StatusSeeOther)
}

// captchaConfigJS sets the widget's globals before the widget module runs.
// It exists because the admin CSP forbids inline scripts, so the login page
// can't set window.CAP_CUSTOM_WASM_URL directly.
func (s *server) captchaConfigJS(w http.ResponseWriter, r *http.Request) {
	if s.deps.Captcha == nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	fmt.Fprintf(w, "window.CAP_CUSTOM_WASM_URL = %q;\n", s.deps.Captcha.WasmURL())
}

func (s *server) logout(w http.ResponseWriter, r *http.Request) {
	if err := s.deps.Sessions.Destroy(r.Context()); err != nil {
		s.serverError(w, err)
		return
	}
	http.Redirect(w, r, s.deps.AdminPath+"/login", http.StatusSeeOther)
}

func (s *server) dashboard(w http.ResponseWriter, r *http.Request) {
	s.render(w, http.StatusOK, "dashboard", s.newTemplateData(r))
}

func remoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
