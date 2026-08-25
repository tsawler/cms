package admin

import (
	"errors"
	"net"
	"net/http"
	"strconv"
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
		fail(http.StatusTooManyRequests, s.tr(r, "Too many failed attempts. Please wait a few minutes and try again."))
		return
	}

	// Honeypot: the field is visually hidden, so a value means a bot
	// filled the form. Answer exactly like a wrong password would.
	if r.PostFormValue("website") != "" {
		s.throttle.Fail(throttleKey)
		fail(http.StatusUnprocessableEntity, s.tr(r, "That email and password combination didn't work."))
		return
	}

	if s.deps.Captcha != nil {
		token := r.PostFormValue(captcha.FieldName)
		if token == "" {
			fail(http.StatusUnprocessableEntity, s.tr(r, "Please complete the verification challenge."))
			return
		}
		ok, err := s.deps.Captcha.Verify(r.Context(), token)
		if err != nil {
			// No verdict (Cap server unreachable): fail open so a Cap
			// outage cannot lock admins out — the login throttle still
			// blunts password guessing.
			s.deps.Logger.Warn("cms admin: captcha unavailable, skipping check", "err", err)
		} else if !ok {
			fail(http.StatusUnprocessableEntity, s.tr(r, "Verification failed. Please try again."))
			return
		}
	}

	u, err := s.deps.Users.Authenticate(r.Context(), email, password)
	if errors.Is(err, auth.ErrInvalidCredentials) {
		s.throttle.Fail(throttleKey)
		fail(http.StatusUnprocessableEntity, s.tr(r, "That email and password combination didn't work."))
		return
	}
	if err != nil {
		s.serverError(w, err)
		return
	}

	s.throttle.Reset(throttleKey)

	// The password was right and the site is shut. Refused here, before
	// the two-factor branch below: an account that cannot sign in should
	// not be walked through a code challenge first, only to be turned
	// away once it has been redeemed. completeLogin checks again, for
	// the challenge that was already pending when the lock was thrown.
	if !u.Role.IsSuperadmin() && s.siteLocked(r.Context()) {
		s.renderLocked(w, r)
		return
	}

	// A fresh session token on privilege change prevents session fixation.
	if err := s.deps.Sessions.RenewToken(r.Context()); err != nil {
		s.serverError(w, err)
		return
	}

	// Two-factor accounts are not logged in yet: the password only parks
	// them in a pending state the code challenge can redeem. The session
	// holds who passed and until when, never sessionKeyUserID.
	if u.TwoFactorEnabled() {
		s.deps.Sessions.Put(r.Context(), sessionKey2FAUserID, u.ID)
		s.deps.Sessions.Put(r.Context(), sessionKey2FARemember, r.PostFormValue("remember") == "1")
		s.deps.Sessions.Put(r.Context(), sessionKey2FAExpires, time.Now().Add(twoFactorGrace).Unix())
		http.Redirect(w, r, s.deps.AdminPath+"/login/2fa", http.StatusSeeOther)
		return
	}

	s.completeLogin(w, r, u, r.PostFormValue("remember") == "1")
}

// completeLogin grants the session: the shared tail of the plain login
// and the two-factor challenge.
func (s *server) completeLogin(w http.ResponseWriter, r *http.Request, u *auth.User, remember bool) {
	// The backstop for the check in login above: this is where both
	// login paths meet, so a two-factor challenge that was pending when
	// the site closed is refused here even though nothing refused it on
	// the way in. The password was right; the session is still not
	// granted.
	if !u.Role.IsSuperadmin() && s.siteLocked(r.Context()) {
		s.renderLocked(w, r)
		return
	}
	s.deps.Sessions.Put(r.Context(), sessionKeyUserID, u.ID)
	if remember {
		// Persistent cookie (survives browser restarts) with the
		// remember duration as both cookie and server-side deadline.
		s.deps.Sessions.RememberMe(r.Context(), true)
		s.deps.Sessions.SetDeadline(r.Context(), time.Now().Add(s.deps.RememberFor))
	}
	s.deps.Logger.Info("cms admin: login", "user", u.Email)
	http.Redirect(w, r, s.deps.AdminPath+"/", http.StatusSeeOther)
}

// twoFactorGrace is how long the code challenge stays open after a
// correct password. Enough to fetch a phone from another room; short
// enough that an abandoned half-login goes stale before the machine
// changes hands.
const twoFactorGrace = 5 * time.Minute

// twoFactorPending returns the user who has passed the password half of a
// two-factor login and may still redeem the code half, or nil.
func (s *server) twoFactorPending(r *http.Request) *auth.User {
	ctx := r.Context()
	id := s.deps.Sessions.GetInt64(ctx, sessionKey2FAUserID)
	if id == 0 {
		return nil
	}
	if time.Now().Unix() > s.deps.Sessions.GetInt64(ctx, sessionKey2FAExpires) {
		s.clearTwoFactorPending(r)
		return nil
	}
	u, err := s.deps.Users.GetByID(ctx, id)
	if err != nil {
		if !errors.Is(err, auth.ErrNotFound) {
			s.deps.Logger.Error("cms admin: loading pending 2fa user", "err", err)
		}
		return nil
	}
	// Re-checked here, not just at the password step: an account
	// deactivated — or whose two-factor was reset — mid-challenge should
	// not sail through on stale state.
	if !u.Active || !u.TwoFactorEnabled() {
		s.clearTwoFactorPending(r)
		return nil
	}
	return u
}

func (s *server) clearTwoFactorPending(r *http.Request) {
	s.deps.Sessions.Remove(r.Context(), sessionKey2FAUserID)
	s.deps.Sessions.Remove(r.Context(), sessionKey2FARemember)
	s.deps.Sessions.Remove(r.Context(), sessionKey2FAExpires)
}

func (s *server) twoFactorForm(w http.ResponseWriter, r *http.Request) {
	if s.currentUser(r) != nil {
		http.Redirect(w, r, s.deps.AdminPath+"/", http.StatusSeeOther)
		return
	}
	if s.twoFactorPending(r) == nil {
		http.Redirect(w, r, s.deps.AdminPath+"/login", http.StatusSeeOther)
		return
	}
	data := s.newTemplateData(r)
	data.PageScript = "validate.js"
	s.render(w, http.StatusOK, "login_2fa", data)
}

func (s *server) twoFactorSubmit(w http.ResponseWriter, r *http.Request) {
	u := s.twoFactorPending(r)
	if u == nil {
		http.Redirect(w, r, s.deps.AdminPath+"/login", http.StatusSeeOther)
		return
	}

	fail := func(status int, msg string) {
		data := s.newTemplateData(r)
		data.Error = msg
		data.PageScript = "validate.js"
		s.render(w, status, "login_2fa", data)
	}

	// Its own throttle namespace, keyed by the account under challenge:
	// a six-digit code has a million values, so five tries per fifteen
	// minutes is what makes guessing not a strategy.
	throttleKey := "2fa|" + strconv.FormatInt(u.ID, 10) + "|" + remoteIP(r)
	if s.throttle.Blocked(throttleKey) {
		fail(http.StatusTooManyRequests, s.tr(r, "Too many failed attempts. Please wait a few minutes and try again."))
		return
	}

	step, ok := auth.VerifyTOTP(u.TOTPSecret, r.PostFormValue("code"), time.Now())
	if ok {
		// The code is right; now claim its time step. A claim that fails
		// means this code was already accepted once — a replay — and gets
		// the same answer a wrong code does.
		claimed, err := s.deps.Users.ConsumeTOTPStep(r.Context(), u.ID, step)
		if err != nil {
			s.serverError(w, err)
			return
		}
		ok = claimed
	}
	if !ok {
		s.throttle.Fail(throttleKey)
		fail(http.StatusUnprocessableEntity, s.tr(r, "That code didn't work. Enter the current code from your authenticator app."))
		return
	}

	s.throttle.Reset(throttleKey)
	remember := s.deps.Sessions.GetBool(r.Context(), sessionKey2FARemember)
	s.clearTwoFactorPending(r)
	// A fresh token again on the pending → logged-in promotion, same as
	// the password step: each privilege change gets its own session id.
	if err := s.deps.Sessions.RenewToken(r.Context()); err != nil {
		s.serverError(w, err)
		return
	}
	s.completeLogin(w, r, u, remember)
}

func (s *server) logout(w http.ResponseWriter, r *http.Request) {
	if err := s.deps.Sessions.Destroy(r.Context()); err != nil {
		s.serverError(w, err)
		return
	}
	http.Redirect(w, r, s.deps.AdminPath+"/login", http.StatusSeeOther)
}

func remoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
