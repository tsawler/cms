package admin

// The "forgot password" flow: ask for a link, get an email, set a new
// password. Mounted only when Deps.Mailer is set — see the Mailer
// interface for why nil means off rather than degraded.
//
// The flow never says whether an account exists. The confirmation page
// after asking is the same for every address, the send happens in the
// background so a known address costs no more response time than an
// unknown one, and a dead token gets one undifferentiated answer whether
// it was guessed, expired, or already spent.

import (
	"context"
	"errors"
	"html"
	"net/http"
	"strings"
	"time"

	"github.com/tsawler/cms/auth"
	"github.com/tsawler/cms/captcha"
)

// esc is html.EscapeString, named for reading an email template inline.
func esc(s string) string { return html.EscapeString(s) }

// resetSendTimeout bounds the background delivery of one reset email.
// Generous, because nothing waits on it: the visitor already has their
// answer, and the goroutine holding this deadline is the only thing the
// timeout can cancel.
const resetSendTimeout = 30 * time.Second

func (s *server) forgotForm(w http.ResponseWriter, r *http.Request) {
	if s.currentUser(r) != nil {
		http.Redirect(w, r, s.deps.AdminPath+"/", http.StatusSeeOther)
		return
	}
	s.render(w, http.StatusOK, "forgot_password", s.newTemplateData(r))
}

// forgotRequest takes the address and answers identically whether or not
// it names an account. The same defenses as the login form, because this
// endpoint does something login does not: it makes the server send email,
// which is worth as much to a spammer as a password guess is to a thief.
func (s *server) forgotRequest(w http.ResponseWriter, r *http.Request) {
	email := strings.TrimSpace(r.PostFormValue("email"))

	fail := func(status int, msg string) {
		data := s.newTemplateData(r)
		data.Error = msg
		s.render(w, status, "forgot_password", data)
	}

	// Its own throttle namespace: a locked-out password guesser should
	// not arrive here pre-blocked, nor spend reset attempts to block
	// somebody's login.
	throttleKey := "reset|" + strings.ToLower(email) + "|" + remoteIP(r)
	if s.throttle.Blocked(throttleKey) {
		fail(http.StatusTooManyRequests, s.tr(r, "Too many requests. Please wait a few minutes and try again."))
		return
	}

	if email == "" {
		fail(http.StatusUnprocessableEntity, s.tr(r, "Enter your email address."))
		return
	}

	// Honeypot: answer exactly like success would, so a bot learns
	// nothing — but no email moves.
	if r.PostFormValue("website") != "" {
		s.throttle.Fail(throttleKey)
		s.renderForgotSent(w, r)
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
			// Fail open, as login does: a Cap outage should not strand
			// somebody who has genuinely lost their password. The
			// throttle still bounds abuse.
			s.deps.Logger.Warn("cms admin: captcha unavailable, skipping check", "err", err)
		} else if !ok {
			fail(http.StatusUnprocessableEntity, s.tr(r, "Verification failed. Please try again."))
			return
		}
	}

	// Every attempt counts against the throttle, hit or miss — this is
	// an email-sending endpoint, and five sends in fifteen minutes is
	// already generous for a human who cannot find their inbox.
	s.throttle.Fail(throttleKey)

	// From here the answer is already decided: the confirmation page,
	// whatever we find. The lookup and send happen after the decision so
	// they cannot change it — only whether an email actually goes.
	u, err := s.deps.Users.GetByEmail(r.Context(), email)
	switch {
	case errors.Is(err, auth.ErrNotFound) || (err == nil && !u.Active):
		// No account, or one that could not log in anyway. Say nothing:
		// the reset form must not become an oracle for which addresses
		// have admin accounts.
	case err != nil:
		s.serverError(w, err)
		return
	default:
		token, err := s.deps.Users.MintReset(r.Context(), u.ID)
		if err != nil {
			s.serverError(w, err)
			return
		}
		link := s.absoluteAdminURL(r, "/reset-password?token="+token)
		subject, text, html := s.resetEmail(r, u.Name, link)

		// Delivered off the request, for two reasons. The honest one is
		// timing: a send that ran inline would make known addresses
		// measurably slower than unknown ones, and the whole page is
		// built not to make that distinction. The practical one is that
		// SMTP on a bad day should not hold this response hostage.
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), resetSendTimeout)
			defer cancel()
			if err := s.deps.Mailer.Send(ctx, u.Email, subject, text, html); err != nil {
				s.deps.Logger.Error("cms admin: sending password reset", "err", err, "user", u.Email)
				return
			}
			s.deps.Logger.Info("cms admin: password reset link sent", "user", u.Email)
		}()
	}

	s.renderForgotSent(w, r)
}

func (s *server) renderForgotSent(w http.ResponseWriter, r *http.Request) {
	data := s.newTemplateData(r)
	data.ResetSent = true
	s.render(w, http.StatusOK, "forgot_password", data)
}

// resetForm shows the new-password form behind a live token — without
// spending it, because rendering a form is not using it. A dead token
// gets the invalid page and deliberately no detail about how it died.
func (s *server) resetForm(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if _, err := s.deps.Users.ResetUser(r.Context(), token); err != nil {
		if errors.Is(err, auth.ErrResetInvalid) {
			s.renderResetInvalid(w, r)
			return
		}
		s.serverError(w, err)
		return
	}
	data := s.newTemplateData(r)
	data.ResetToken = token
	s.render(w, http.StatusOK, "reset_password", data)
}

func (s *server) resetSubmit(w http.ResponseWriter, r *http.Request) {
	token := r.PostFormValue("token")
	password := r.PostFormValue("password")
	confirm := r.PostFormValue("confirm")

	// Password problems re-render the form with the token intact: a typo
	// must not burn the link. The token is only consumed once there is a
	// valid password to install.
	if len(password) < minPasswordLength || password != confirm {
		if _, err := s.deps.Users.ResetUser(r.Context(), token); err != nil {
			s.renderResetInvalid(w, r)
			return
		}
		data := s.newTemplateData(r)
		data.ResetToken = token
		if len(password) < minPasswordLength {
			data.Error = s.tr(r, "Password must be at least 8 characters.")
		} else {
			data.Error = s.tr(r, "Those passwords don't match.")
		}
		s.render(w, http.StatusUnprocessableEntity, "reset_password", data)
		return
	}

	u, err := s.deps.Users.ConsumeReset(r.Context(), token)
	if errors.Is(err, auth.ErrResetInvalid) {
		s.renderResetInvalid(w, r)
		return
	}
	if err != nil {
		s.serverError(w, err)
		return
	}

	hash, err := auth.HashPassword(password)
	if err != nil {
		s.serverError(w, err)
		return
	}
	if err := s.deps.Users.UpdatePassword(r.Context(), u.ID, hash); err != nil {
		s.serverError(w, err)
		return
	}

	s.deps.Logger.Info("cms admin: password reset completed", "user", u.Email)
	s.flash(r, s.tr(r, "Your password has been changed. Log in with it now."))
	http.Redirect(w, r, s.deps.AdminPath+"/login", http.StatusSeeOther)
}

func (s *server) renderResetInvalid(w http.ResponseWriter, r *http.Request) {
	data := s.newTemplateData(r)
	data.ResetInvalid = true
	s.render(w, http.StatusUnprocessableEntity, "reset_password", data)
}

// absoluteAdminURL builds a link into the admin that works away from this
// request — in an email. Deps.SiteBaseURL when the host provided one;
// otherwise the request's own scheme and host, honouring a proxy's
// X-Forwarded-Proto, which is right for a site served under one name.
func (s *server) absoluteAdminURL(r *http.Request, path string) string {
	base := ""
	if s.deps.SiteBaseURL != nil {
		base = s.deps.SiteBaseURL(r)
	}
	if base == "" {
		scheme := "http"
		if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
			scheme = "https"
		}
		base = scheme + "://" + r.Host
	}
	return base + s.deps.AdminPath + path
}

// resetEmail is the message itself: authored here, in the module, so
// every host sends the same carefully-worded thing — in particular the
// part that does not confirm the account to whoever asked. The host's
// Mailer supplies only delivery.
//
// Plain and unbranded on purpose: this is an operational credential
// email from a piece of software, and dressing it up as anything else
// makes it look more like phishing, not less.
func (s *server) resetEmail(r *http.Request, name, link string) (subject, text, html string) {
	greet := s.tr(r, "Hi")
	if name != "" {
		greet += " " + name
	}
	asked := s.tr(r, "Somebody asked to reset the password for the CMS account at this address. If it was you, follow this link — it works once, for the next hour:")
	ignore := s.tr(r, "If it wasn't you, ignore this message. Nothing changes unless the link is used.")

	subject = s.tr(r, "Reset your CMS password")
	text = greet + ",\n\n" + asked + "\n\n" + link + "\n\n" + ignore + "\n"

	// The one HTML flourish is making the link a link; some clients do
	// not autolink long URLs with query strings. Everything is escaped
	// through the template so a user's name cannot inject markup.
	html = `<p>` + esc(greet) + `,</p>` +
		`<p>` + esc(asked) + `</p>` +
		`<p><a href="` + esc(link) + `">` + esc(link) + `</a></p>` +
		`<p>` + esc(ignore) + `</p>`
	return subject, text, html
}
