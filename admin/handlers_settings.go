package admin

// The signed-in user's own settings page: change their password, and turn
// two-factor login (an authenticator app) on or off. Everything here acts
// on the current user only — user management for others lives behind the
// admin-only /users pages.
//
// Both sensitive actions re-prove the password: changing it requires the
// current one, and turning two-factor off requires it too. A walked-away
// or hijacked session must not be enough to quietly weaken the account.

import (
	"encoding/base64"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"time"

	qrcode "github.com/skip2/go-qrcode"
	"github.com/tsawler/cms/auth"
)

func (s *server) settingsForm(w http.ResponseWriter, r *http.Request) {
	s.renderSettings(w, r, http.StatusOK, nil)
}

// renderSettings draws the settings page, including the in-progress
// enrollment block (QR code and confirm field) when the session holds a
// pending secret.
func (s *server) renderSettings(w http.ResponseWriter, r *http.Request, status int, errs map[string]string) {
	data := s.newTemplateData(r)
	data.FormErrors = errs
	u := data.User // non-nil behind requireUser
	data.TwoFactorEnabled = u.TwoFactorEnabled()

	if secret := s.deps.Sessions.GetString(r.Context(), sessionKeyTOTPSetup); secret != "" && !data.TwoFactorEnabled {
		data.TOTPSecret = groupSecret(secret)
		uri := auth.TOTPProvisioningURI(s.totpIssuer(r), u.Email, secret)
		// Failing to draw the QR code is not failing the page: the
		// manual-entry key beside it enrolls the same secret.
		if png, err := qrcode.Encode(uri, qrcode.Medium, 208); err != nil {
			s.deps.Logger.Error("cms admin: rendering totp qr code", "err", err)
		} else {
			data.TOTPQR = template.URL("data:image/png;base64," + base64.StdEncoding.EncodeToString(png))
		}
	}
	s.render(w, status, "settings", data)
}

// settingsPassword changes the current user's password.
// POST /settings/password  current_password, password, confirm
func (s *server) settingsPassword(w http.ResponseWriter, r *http.Request) {
	u := s.currentUser(r)

	// Guessing the current password out of a stolen session gets the
	// same throttle a login gets; "pw|" keeps the namespaces apart.
	throttleKey := "pw|" + strconv.FormatInt(u.ID, 10) + "|" + remoteIP(r)
	if s.throttle.Blocked(throttleKey) {
		s.renderSettings(w, r, http.StatusTooManyRequests, map[string]string{
			"current_password": s.tr(r, "Too many failed attempts. Please wait a few minutes and try again."),
		})
		return
	}

	errs := map[string]string{}
	ok, err := auth.VerifyPassword(r.PostFormValue("current_password"), u.PasswordHash)
	if err != nil {
		s.serverError(w, err)
		return
	}
	if !ok {
		s.throttle.Fail(throttleKey)
		errs["current_password"] = s.tr(r, "That isn't your current password.")
	}
	password := r.PostFormValue("password")
	if len(password) < minPasswordLength {
		errs["password"] = s.tr(r, "Password must be at least 8 characters.")
	} else if password != r.PostFormValue("confirm") {
		errs["confirm"] = s.tr(r, "Those passwords don't match.")
	}
	if len(errs) > 0 {
		s.renderSettings(w, r, http.StatusUnprocessableEntity, errs)
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
	s.throttle.Reset(throttleKey)
	s.deps.Logger.Info("cms admin: password changed", "user", u.Email)
	s.flash(r, s.tr(r, "Your password has been changed."))
	http.Redirect(w, r, s.deps.AdminPath+"/settings", http.StatusSeeOther)
}

// totpSetup starts enrollment: a fresh secret, held only in the session
// until a live code confirms the authenticator app has it.
// POST /settings/2fa/setup
func (s *server) totpSetup(w http.ResponseWriter, r *http.Request) {
	if s.currentUser(r).TwoFactorEnabled() {
		http.Redirect(w, r, s.deps.AdminPath+"/settings", http.StatusSeeOther)
		return
	}
	secret, err := auth.GenerateTOTPSecret()
	if err != nil {
		s.serverError(w, err)
		return
	}
	s.deps.Sessions.Put(r.Context(), sessionKeyTOTPSetup, secret)
	http.Redirect(w, r, s.deps.AdminPath+"/settings", http.StatusSeeOther)
}

// totpCancel abandons an enrollment in progress.
// POST /settings/2fa/cancel
func (s *server) totpCancel(w http.ResponseWriter, r *http.Request) {
	s.deps.Sessions.Remove(r.Context(), sessionKeyTOTPSetup)
	http.Redirect(w, r, s.deps.AdminPath+"/settings", http.StatusSeeOther)
}

// totpConfirm finishes enrollment: only a code the app generated from the
// pending secret turns two-factor on, so nobody locks themselves out by
// enabling it with an app that never scanned the QR code.
// POST /settings/2fa/confirm  code
func (s *server) totpConfirm(w http.ResponseWriter, r *http.Request) {
	u := s.currentUser(r)
	if u.TwoFactorEnabled() {
		http.Redirect(w, r, s.deps.AdminPath+"/settings", http.StatusSeeOther)
		return
	}
	secret := s.deps.Sessions.GetString(r.Context(), sessionKeyTOTPSetup)
	if secret == "" {
		http.Redirect(w, r, s.deps.AdminPath+"/settings", http.StatusSeeOther)
		return
	}
	step, ok := auth.VerifyTOTP(secret, r.PostFormValue("code"), time.Now())
	if !ok {
		s.renderSettings(w, r, http.StatusUnprocessableEntity, map[string]string{
			"totp": s.tr(r, "That code didn't work. Enter the current code from your authenticator app."),
		})
		return
	}
	// Recording the confirming code's step spends it: the code that
	// proved the enrollment cannot also pass the next login challenge.
	if err := s.deps.Users.EnableTOTP(r.Context(), u.ID, secret, step); err != nil {
		s.serverError(w, err)
		return
	}
	s.deps.Sessions.Remove(r.Context(), sessionKeyTOTPSetup)
	s.deps.Logger.Info("cms admin: two-factor enabled", "user", u.Email)
	s.flash(r, s.tr(r, "Two-factor authentication is on. You'll be asked for a code when you log in."))
	http.Redirect(w, r, s.deps.AdminPath+"/settings", http.StatusSeeOther)
}

// totpDisable turns two-factor off, behind the password: a borrowed
// session alone must not be able to strip the account's second factor.
// POST /settings/2fa/disable  current_password
func (s *server) totpDisable(w http.ResponseWriter, r *http.Request) {
	u := s.currentUser(r)
	if !u.TwoFactorEnabled() {
		http.Redirect(w, r, s.deps.AdminPath+"/settings", http.StatusSeeOther)
		return
	}

	throttleKey := "pw|" + strconv.FormatInt(u.ID, 10) + "|" + remoteIP(r)
	if s.throttle.Blocked(throttleKey) {
		s.renderSettings(w, r, http.StatusTooManyRequests, map[string]string{
			"disable": s.tr(r, "Too many failed attempts. Please wait a few minutes and try again."),
		})
		return
	}
	ok, err := auth.VerifyPassword(r.PostFormValue("current_password"), u.PasswordHash)
	if err != nil {
		s.serverError(w, err)
		return
	}
	if !ok {
		s.throttle.Fail(throttleKey)
		s.renderSettings(w, r, http.StatusUnprocessableEntity, map[string]string{
			"disable": s.tr(r, "That isn't your current password."),
		})
		return
	}

	if err := s.deps.Users.DisableTOTP(r.Context(), u.ID); err != nil {
		s.serverError(w, err)
		return
	}
	s.throttle.Reset(throttleKey)
	s.deps.Logger.Info("cms admin: two-factor disabled", "user", u.Email)
	s.flash(r, s.tr(r, "Two-factor authentication is off."))
	http.Redirect(w, r, s.deps.AdminPath+"/settings", http.StatusSeeOther)
}

// totpIssuer names the site inside the authenticator app's entry. The
// public host when one is known, so an app holding several CMS logins
// shows which site each belongs to.
func (s *server) totpIssuer(r *http.Request) string {
	if s.deps.SiteBaseURL != nil {
		if u, err := url.Parse(s.deps.SiteBaseURL(r)); err == nil && u.Host != "" {
			return u.Host
		}
	}
	if r.Host != "" {
		return r.Host
	}
	return "CMS"
}

// groupSecret spaces a base32 secret into blocks of four for the
// manual-entry fallback, the way authenticator apps print their own keys.
// VerifyTOTP strips the spaces back out, and apps ignore them on entry.
func groupSecret(secret string) string {
	var b []byte
	for i := 0; i < len(secret); i += 4 {
		if i > 0 {
			b = append(b, ' ')
		}
		b = append(b, secret[i:min(i+4, len(secret))]...)
	}
	return string(b)
}
