package admin

import (
	"errors"
	"net"
	"net/http"
	"strings"

	"github.com/tsawler/cms/auth"
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
	s.deps.Logger.Info("cms admin: login", "user", u.Email)
	http.Redirect(w, r, s.deps.AdminPath+"/", http.StatusSeeOther)
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
