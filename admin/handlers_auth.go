package admin

import (
	"errors"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/tsawler/cms/auth"
	"github.com/tsawler/cms/captcha"
)

// nextParam names where to go once signed in: a query parameter on the
// login page, carried through the form as a hidden field of the same
// name. A host that turns a signed-out visitor away from a private page
// sends them to AdminPath + "/login?next=" + the page's path; see
// LoginURL.
const nextParam = "next"

// maxNextLen bounds a return address. Any real page path fits many times
// over; the bound only keeps a hostile one from being carried around in
// the session.
const maxNextLen = 2048

// safeNext returns next when it is a path on this site worth returning
// to, and "" otherwise. "" means the ordinary landing page — see
// landingFor — so every refusal here is quiet and harmless.
//
// This is the whole defence against the login page becoming an open
// redirect: an attacker's link to /admin/login?next=https://evil.example
// signs the victim in for real and then forwards them, from a page they
// just trusted with a password, to a convincing copy asking for it
// again. So only a site-relative path is accepted, and the checks are
// written against how *browsers* read a Location header, which is more
// generously than url.Parse does:
//
//   - It must start with exactly one slash. "//evil.example" is a
//     scheme-relative URL — another host — and so is "/\evil.example",
//     because browsers treat a backslash in a URL as a slash.
//   - No backslashes or control characters anywhere. Browsers delete
//     tabs and newlines from URLs before parsing them, so "/	/evil" is
//     "//evil" by the time it is followed; and CR/LF have no business
//     anywhere near a response header.
//   - After all that, url.Parse must agree that it names no scheme, host
//     or user — belt and braces for whatever the list above missed.
//
// The login flow's own pages are refused too, not for safety but so a
// stale link cannot loop someone back to the form they just filled in.
func (s *server) safeNext(next string) string {
	if next == "" || len(next) > maxNextLen || next[0] != '/' {
		return ""
	}
	if len(next) > 1 && (next[1] == '/' || next[1] == '\\') {
		return ""
	}
	for i := 0; i < len(next); i++ {
		if c := next[i]; c < 0x21 || c == 0x7f || c == '\\' {
			return ""
		}
	}
	u, err := url.Parse(next)
	if err != nil || u.Scheme != "" || u.Host != "" || u.User != nil || u.Opaque != "" {
		return ""
	}
	for _, p := range []string{"/login", "/logout", "/forgot-password", "/reset-password"} {
		if u.Path == s.deps.AdminPath+p || strings.HasPrefix(u.Path, s.deps.AdminPath+p+"/") {
			return ""
		}
	}
	return next
}

// landingFor is where a sign-in ends up: the return address when there
// is a good one, and otherwise the front of the site for a reader (the
// admin holds nothing for them) and the dashboard for staff.
func (s *server) landingFor(u *auth.User, next string) string {
	if next != "" {
		return next
	}
	if u != nil && u.Role.IsReader() {
		return "/"
	}
	return s.deps.AdminPath + "/"
}

func (s *server) loginForm(w http.ResponseWriter, r *http.Request) {
	next := s.safeNext(r.URL.Query().Get(nextParam))
	if u := s.currentUser(r); u != nil {
		http.Redirect(w, r, s.landingFor(u, next), http.StatusSeeOther)
		return
	}
	data := s.newTemplateData(r)
	data.Next = next
	s.render(w, http.StatusOK, "login", data)
}

func (s *server) login(w http.ResponseWriter, r *http.Request) {
	email := strings.TrimSpace(r.PostFormValue("email"))
	password := r.PostFormValue("password")
	// Validated now and again at the redirect: the form field is as
	// attacker-controlled as the query string it came from.
	next := s.safeNext(r.PostFormValue(nextParam))

	fail := func(status int, msg string) {
		data := s.newTemplateData(r)
		data.Error = msg
		data.Next = next
		s.render(w, status, "login", data)
	}

	// Two counters, failed together and checked together: one for this
	// account from this source, one for this account from anywhere. The
	// second is what makes the limit a limit — see throttleLimits.
	account := strings.ToLower(email)
	throttleKey := account + "|" + s.clientIP(r)
	tooMany := func() bool {
		return s.throttle.Blocked(throttleKey) || s.acctAttempt.Blocked(account)
	}
	countFailure := func() {
		s.throttle.Fail(throttleKey)
		s.acctAttempt.Fail(account)
	}
	if tooMany() {
		fail(http.StatusTooManyRequests, s.tr(r, "Too many failed attempts. Please wait a few minutes and try again."))
		return
	}

	// Honeypot: the field is visually hidden, so a value means a bot
	// filled the form. Answer exactly like a wrong password would.
	if r.PostFormValue("website") != "" {
		countFailure()
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
			s.captchaNoVerdict(r, err)
		} else if !ok {
			fail(http.StatusUnprocessableEntity, s.tr(r, "Verification failed. Please try again."))
			return
		}
	}

	u, err := s.deps.Users.Authenticate(r.Context(), email, password)
	if errors.Is(err, auth.ErrInvalidCredentials) {
		countFailure()
		fail(http.StatusUnprocessableEntity, s.tr(r, "That email and password combination didn't work."))
		return
	}
	// The client went away while waiting for a password check (see
	// auth's argonSlots). Nobody is left to answer, and under a flood of
	// abandoned logins a server-error log line apiece is just noise.
	if r.Context().Err() != nil {
		return
	}
	if err != nil {
		s.serverError(w, err)
		return
	}

	// Both password counters clear on a correct password — but nothing
	// here touches the two-factor counters. They are what a caller who
	// already has the password is up against, so letting the password
	// step reset them would hand the attacker an unlimited supply of
	// code guesses for the price of re-submitting a password they know.
	s.throttle.Reset(throttleKey)
	s.acctAttempt.Reset(account)

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
		s.deps.Sessions.Put(r.Context(), sessionKey2FAEpoch, u.SessionEpoch)
		// In the session rather than on the challenge's URL, so nothing
		// between here and the code form can swap it.
		s.deps.Sessions.Put(r.Context(), sessionKey2FANext, next)
		http.Redirect(w, r, s.deps.AdminPath+"/login/2fa", http.StatusSeeOther)
		return
	}

	s.completeLogin(w, r, u, r.PostFormValue("remember") == "1", next)
}

// completeLogin grants the session: the shared tail of the plain login
// and the two-factor challenge.
func (s *server) completeLogin(w http.ResponseWriter, r *http.Request, u *auth.User, remember bool, next string) {
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
	s.deps.Sessions.Put(r.Context(), sessionKeyEpoch, u.SessionEpoch)
	if remember {
		// Persistent cookie (survives browser restarts) with the
		// remember duration as both cookie and server-side deadline.
		s.deps.Sessions.RememberMe(r.Context(), true)
		s.deps.Sessions.SetDeadline(r.Context(), time.Now().Add(s.deps.RememberFor))
	}
	s.deps.Logger.Info("cms admin: login", "user", u.Email)
	http.Redirect(w, r, s.landingFor(u, s.safeNext(next)), http.StatusSeeOther)
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
	// deactivated — or whose password or two-factor changed —
	// mid-challenge should not sail through on stale state.
	if !u.Active || !u.TwoFactorEnabled() || s.deps.Sessions.GetInt64(ctx, sessionKey2FAEpoch) != u.SessionEpoch {
		s.clearTwoFactorPending(r)
		return nil
	}
	return u
}

func (s *server) clearTwoFactorPending(r *http.Request) {
	s.deps.Sessions.Remove(r.Context(), sessionKey2FAUserID)
	s.deps.Sessions.Remove(r.Context(), sessionKey2FARemember)
	s.deps.Sessions.Remove(r.Context(), sessionKey2FAExpires)
	s.deps.Sessions.Remove(r.Context(), sessionKey2FAEpoch)
	s.deps.Sessions.Remove(r.Context(), sessionKey2FANext)
}

func (s *server) twoFactorForm(w http.ResponseWriter, r *http.Request) {
	if u := s.currentUser(r); u != nil {
		http.Redirect(w, r, s.landingFor(u, ""), http.StatusSeeOther)
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

	// Its own throttle namespace, keyed by the account under challenge —
	// and counted twice, per source and per account. The per-account half
	// is the one that matters here: a six-digit code is a small enough
	// space that an attacker who can spend addresses will simply walk it,
	// and only a counter that ignores where the attempt came from turns
	// that back into years. See throttleLimits.
	account := strconv.FormatInt(u.ID, 10)
	throttleKey := "2fa|" + account + "|" + s.clientIP(r)
	if s.throttle.Blocked(throttleKey) || s.acctCode.Blocked(account) {
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
		s.acctCode.Fail(account)
		fail(http.StatusUnprocessableEntity, s.tr(r, "That code didn't work. Enter the current code from your authenticator app."))
		return
	}

	s.throttle.Reset(throttleKey)
	s.acctCode.Reset(account)
	remember := s.deps.Sessions.GetBool(r.Context(), sessionKey2FARemember)
	next := s.deps.Sessions.GetString(r.Context(), sessionKey2FANext)
	s.clearTwoFactorPending(r)
	// A fresh token again on the pending → logged-in promotion, same as
	// the password step: each privilege change gets its own session id.
	if err := s.deps.Sessions.RenewToken(r.Context()); err != nil {
		s.serverError(w, err)
		return
	}
	s.completeLogin(w, r, u, remember, next)
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

// captchaNoVerdict handles a Verify that came back with neither a yes nor
// a no, and it is where the decision to fail open is actually made.
//
// Open, because the alternative locks every account out of a site over a
// dependency that is not the site: an admin who needs to get in and fix
// something should not be held out by the thing that was meant to keep
// other people out. What still stands in the way of guessing is the
// throttle — and since it gained per-account counters it is a real limit
// rather than a per-address one.
//
// The two ways to get here want different things from whoever reads the
// log, so they are logged differently. ErrUnavailable is an outage: wait,
// or look at the Cap server. ErrBadResponse means something answered and
// it was not Cap — a URL typo, a proxy's error page, another service on
// the port — which will never fix itself, and until it is fixed the
// challenge on this form is decorative. That is worth an error rather
// than the warning the whole thing used to share, because it is a
// standing hole rather than a passing one.
func (s *server) captchaNoVerdict(r *http.Request, err error) {
	if errors.Is(err, captcha.ErrBadResponse) {
		s.deps.Logger.Error("cms admin: the CAPTCHA server answered with something that is not a verdict, "+
			"so the challenge is not being enforced — check CAP_URL/CAP_INTERNAL_URL and the site key",
			"err", err, "path", r.URL.Path)
		return
	}
	s.deps.Logger.Warn("cms admin: no CAPTCHA verdict, allowing the request through",
		"err", err, "path", r.URL.Path)
}
