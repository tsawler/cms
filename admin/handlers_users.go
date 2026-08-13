package admin

import (
	"errors"
	"net/http"
	"net/mail"
	"slices"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/tsawler/cms/auth"
)

const minPasswordLength = 8

func (s *server) usersList(w http.ResponseWriter, r *http.Request) {
	users, err := s.deps.Users.All(r.Context())
	if err != nil {
		s.serverError(w, err)
		return
	}
	actor := s.currentUser(r)
	rows := make([]userRow, len(users))
	for i := range users {
		rows[i] = userRow{User: users[i], CanManage: canManageUser(actor, &users[i])}
	}
	data := s.newTemplateData(r)
	data.Users = rows
	s.render(w, http.StatusOK, "users", data)
}

func (s *server) userNew(w http.ResponseWriter, r *http.Request) {
	// A fresh editor starts with the content permissions ticked — the
	// pre-permissions status quo — and user management off. A non-admin
	// creator can only pass on what they hold, so the defaults are cut
	// down the same way the save will be.
	actor := s.currentUser(r)
	var perms []auth.Permission
	for _, p := range []auth.Permission{auth.PermBlogs, auth.PermNews, auth.PermPages} {
		if actor.Can(p) {
			perms = append(perms, p)
		}
	}
	data := s.newTemplateData(r)
	data.IsNew = true
	data.FormUser = &auth.User{Role: auth.RoleEditor, Active: true, Permissions: perms}
	data.Permissions = s.deps.Permissions
	s.render(w, http.StatusOK, "user_form", data)
}

func (s *server) userCreate(w http.ResponseWriter, r *http.Request) {
	form, password, errs := s.parseUserForm(r, true)
	form.Permissions = mergeGrants(s.currentUser(r), form.Permissions, nil, s.gatedPermissions())

	if len(errs) > 0 {
		s.renderUserForm(w, r, form, true, errs)
		return
	}

	hash, err := auth.HashPassword(password)
	if err != nil {
		s.serverError(w, err)
		return
	}
	form.PasswordHash = hash

	if _, err := s.deps.Users.Insert(r.Context(), form); err != nil {
		if errors.Is(err, auth.ErrDuplicateEmail) {
			s.renderUserForm(w, r, form, true, map[string]string{"email": s.tr(r, "That email address is already in use.")})
			return
		}
		s.serverError(w, err)
		return
	}
	if err := s.deps.Users.ReplacePermissions(r.Context(), form.ID, form.Permissions); err != nil {
		s.serverError(w, err)
		return
	}

	s.flash(r, s.tr(r, "User created."))
	http.Redirect(w, r, s.deps.AdminPath+"/users", http.StatusSeeOther)
}

func (s *server) userEdit(w http.ResponseWriter, r *http.Request) {
	u, ok := s.userFromURL(w, r)
	if !ok {
		return
	}
	if !s.canManage(r, u) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	s.renderUserForm(w, r, u, false, nil)
}

func (s *server) userUpdate(w http.ResponseWriter, r *http.Request) {
	existing, ok := s.userFromURL(w, r)
	if !ok {
		return
	}
	if !s.canManage(r, existing) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	form, password, errs := s.parseUserForm(r, false)
	form.ID = existing.ID
	form.Permissions = mergeGrants(s.currentUser(r), form.Permissions, existing.Permissions, s.gatedPermissions())
	// Carried over so an error re-render still shows the two-factor
	// reset checkbox; Update never writes this field.
	form.TOTPSecret = existing.TOTPSecret

	// Guard rails: nobody may deactivate their own account, an admin
	// editing their own account cannot drop the admin role, and a
	// non-admin user manager cannot drop the very permission that lets
	// them manage users.
	if self := s.currentUser(r); self != nil && self.ID == existing.ID {
		if !form.Active {
			errs["active"] = s.tr(r, "You cannot deactivate your own account.")
		}
		if self.Role.IsAdmin() && !form.Role.IsAdmin() {
			errs["role"] = s.tr(r, "You cannot remove your own admin role.")
		}
		if !self.Role.IsAdmin() && !slices.Contains(form.Permissions, auth.PermUsers) {
			errs["perm"] = s.tr(r, "You cannot remove your own user-management permission.")
		}
	}

	if len(errs) > 0 {
		s.renderUserForm(w, r, form, false, errs)
		return
	}

	if err := s.deps.Users.Update(r.Context(), form); err != nil {
		if errors.Is(err, auth.ErrDuplicateEmail) {
			s.renderUserForm(w, r, form, false, map[string]string{"email": s.tr(r, "That email address is already in use.")})
			return
		}
		s.serverError(w, err)
		return
	}
	if err := s.deps.Users.ReplacePermissions(r.Context(), form.ID, form.Permissions); err != nil {
		s.serverError(w, err)
		return
	}

	if password != "" {
		hash, err := auth.HashPassword(password)
		if err != nil {
			s.serverError(w, err)
			return
		}
		if err := s.deps.Users.UpdatePassword(r.Context(), form.ID, hash); err != nil {
			s.serverError(w, err)
			return
		}
	}

	// The rescue for a lost phone: an admin clears the user's two-factor
	// enrollment so their password alone logs them in again.
	if r.PostFormValue("reset_totp") == "on" && existing.TwoFactorEnabled() {
		if err := s.deps.Users.DisableTOTP(r.Context(), existing.ID); err != nil {
			s.serverError(w, err)
			return
		}
		s.deps.Logger.Info("cms admin: two-factor reset", "user", existing.Email, "by", s.currentUser(r).Email)
	}

	s.flash(r, s.tr(r, "User updated."))
	http.Redirect(w, r, s.deps.AdminPath+"/users", http.StatusSeeOther)
}

// userDelete removes an account outright. Deletion is for admin roles
// only: a non-admin holder of the users permission can deactivate the
// editors they manage, but erasing an account is beyond their reach.
// Nobody deletes their own account — the same rule as deactivation, and
// it guarantees whoever is deleting still exists afterwards. A superadmin
// account is deletable only by another superadmin, matching canManage:
// an admin who cannot edit one has no business erasing it either.
func (s *server) userDelete(w http.ResponseWriter, r *http.Request) {
	actor := s.currentUser(r)
	if actor == nil || !actor.Role.IsAdmin() {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	target, ok := s.userFromURL(w, r)
	if !ok {
		return
	}
	if target.Role == auth.RoleSuperadmin && !actor.Role.IsSuperadmin() {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if actor.ID == target.ID {
		s.flash(r, s.tr(r, "You cannot delete your own account."))
		http.Redirect(w, r, s.deps.AdminPath+"/users", http.StatusSeeOther)
		return
	}
	if err := s.deps.Users.Delete(r.Context(), target.ID); err != nil {
		s.serverError(w, err)
		return
	}
	s.deps.Logger.Info("cms admin: user deleted", "user", target.Email, "by", actor.Email)
	s.flash(r, s.tr(r, "User deleted."))
	http.Redirect(w, r, s.deps.AdminPath+"/users", http.StatusSeeOther)
}

// userMasquerade signs the session in as the target user, so a superadmin
// can see the admin — and the public site's in-place editor — exactly as
// that user does. The superadmin's own ID is parked in the session and
// masqueradeExit restores it; nothing about the target account changes.
// Superadmin-only, enforced by the route's middleware: the swap grants the
// target's entire session, so nothing less than the top role may start one.
func (s *server) userMasquerade(w http.ResponseWriter, r *http.Request) {
	actor := s.currentUser(r)
	target, ok := s.userFromURL(w, r)
	if !ok {
		return
	}
	if target.ID == actor.ID {
		http.Redirect(w, r, s.deps.AdminPath+"/users", http.StatusSeeOther)
		return
	}
	// An inactive account can't log in, and a masqueraded session would
	// hit the same wall at its very next request.
	if !target.Active {
		s.flash(r, s.tr(r, "You cannot become an inactive user."))
		http.Redirect(w, r, s.deps.AdminPath+"/users", http.StatusSeeOther)
		return
	}

	ctx := r.Context()
	// Masquerading from inside a masquerade (the first target was another
	// superadmin) keeps the original owner: exit always returns to the
	// account that actually logged in.
	owner := s.deps.Sessions.GetInt64(ctx, sessionKeyMasqueradeFrom)
	if owner == 0 {
		owner = actor.ID
	}
	// A fresh session token on privilege change, same as login. RenewToken
	// keeps the session's data, so the parked owner survives it.
	if err := s.deps.Sessions.RenewToken(ctx); err != nil {
		s.serverError(w, err)
		return
	}
	s.deps.Sessions.Put(ctx, sessionKeyMasqueradeFrom, owner)
	s.deps.Sessions.Put(ctx, sessionKeyUserID, target.ID)
	s.deps.Logger.Info("cms admin: masquerade started", "as", target.Email, "by", actor.Email)
	http.Redirect(w, r, s.deps.AdminPath+"/", http.StatusSeeOther)
}

// masqueradeExit returns a masquerading session to its owner. It sits
// behind requireUser alone — whoever the session is currently signed in
// as may end the masquerade; the target is usually not a superadmin.
func (s *server) masqueradeExit(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	ownerID := s.deps.Sessions.GetInt64(ctx, sessionKeyMasqueradeFrom)
	if ownerID == 0 {
		http.Redirect(w, r, s.deps.AdminPath+"/", http.StatusSeeOther)
		return
	}
	owner, err := s.deps.Users.GetByID(ctx, ownerID)
	if err != nil && !errors.Is(err, auth.ErrNotFound) {
		s.serverError(w, err)
		return
	}
	if err != nil || !owner.Active {
		// The owner was deleted or deactivated mid-masquerade: there is
		// nobody to return to, so the session ends like a logout.
		if err := s.deps.Sessions.Destroy(ctx); err != nil {
			s.serverError(w, err)
			return
		}
		http.Redirect(w, r, s.deps.AdminPath+"/login", http.StatusSeeOther)
		return
	}
	masked := s.currentUser(r)
	if err := s.deps.Sessions.RenewToken(ctx); err != nil {
		s.serverError(w, err)
		return
	}
	s.deps.Sessions.Remove(ctx, sessionKeyMasqueradeFrom)
	s.deps.Sessions.Put(ctx, sessionKeyUserID, owner.ID)
	if masked != nil {
		s.deps.Logger.Info("cms admin: masquerade ended", "user", owner.Email, "was", masked.Email)
	}
	s.flash(r, s.tr(r, "You are back in your own account."))
	http.Redirect(w, r, s.deps.AdminPath+"/users", http.StatusSeeOther)
}

// canManage reports whether the acting user may open or edit the target
// account. Superadmins manage everyone; an admin manages admins and
// editors but not superadmins; a non-admin holder of the users permission
// manages editor accounts only — an admin's password, role, or two-factor
// reset is never within an editor's reach.
//
// The superadmin exclusion is what makes the role assignment check in
// parseUserForm mean anything. Barring an admin from *assigning* superadmin
// while still letting them edit an existing one would leave the escalation
// wide open: set the superadmin's password, then log in as them.
func (s *server) canManage(r *http.Request, target *auth.User) bool {
	return canManageUser(s.currentUser(r), target)
}

// canManageUser is the rule canManage applies, written against the two
// accounts alone so the users list can ask the same question once per row.
// The list used to decide for itself in template syntax, which drifts: it
// offered an Edit link to viewers the route then answered with a 403.
func canManageUser(actor, target *auth.User) bool {
	if actor == nil || target == nil {
		return false
	}
	if actor.Role.IsSuperadmin() {
		return true
	}
	if target.Role == auth.RoleSuperadmin {
		return false
	}
	return actor.Role.IsAdmin() || target.Role == auth.RoleEditor
}

// userRow is one line of the users table: the account, plus whether the
// viewer may open it. Computing this in the handler keeps the table and
// the route reading from the same rule.
type userRow struct {
	auth.User
	CanManage bool
}

// mergeGrants bounds a grant change to what the actor may give or take:
// what they cannot touch, the target keeps. A non-admin user manager can
// neither grant nor revoke a permission they don't hold themselves. An
// admin may change anything except grants that bind admins too
// (AdminsNeedGrant), which they likewise may only change while holding —
// otherwise an admin switched out of a section could switch themselves
// back in from the users page. Superadmins change everything.
func mergeGrants(actor *auth.User, submitted, existing []auth.Permission, gated map[auth.Permission]bool) []auth.Permission {
	if actor != nil && actor.Role.IsSuperadmin() {
		return submitted
	}
	mayChange := func(p auth.Permission) bool {
		if gated[p] {
			return actor.HasGrant(p)
		}
		return actor.Can(p)
	}
	var out []auth.Permission
	for _, p := range submitted {
		if mayChange(p) {
			out = append(out, p)
		}
	}
	for _, p := range existing {
		if !mayChange(p) {
			out = append(out, p)
		}
	}
	return out
}

// gatedPermissions is the set of declared permissions that bind admins
// too — the ones mergeGrants holds admin actors to.
func (s *server) gatedPermissions() map[auth.Permission]bool {
	gated := make(map[auth.Permission]bool)
	for _, d := range s.deps.Permissions {
		if d.AdminsNeedGrant {
			gated[d.Key] = true
		}
	}
	return gated
}

// knownPermissions is the set the user form's checkboxes may grant: the
// built-ins plus whatever the host declared.
func (s *server) knownPermissions() map[auth.Permission]bool {
	known := make(map[auth.Permission]bool, 4+len(s.deps.Permissions))
	for _, p := range auth.BuiltinPermissions() {
		known[p] = true
	}
	for _, d := range s.deps.Permissions {
		known[d.Key] = true
	}
	return known
}

// parseUserForm reads and validates the shared user form. When isNew is
// true a password is required; otherwise a blank password means "keep".
func (s *server) parseUserForm(r *http.Request, isNew bool) (*auth.User, string, map[string]string) {
	errs := map[string]string{}

	u := &auth.User{
		Email:  strings.TrimSpace(r.PostFormValue("email")),
		Name:   strings.TrimSpace(r.PostFormValue("name")),
		Role:   auth.Role(r.PostFormValue("role")),
		Active: r.PostFormValue("active") == "on",
	}
	password := r.PostFormValue("password")

	// Unknown keys can only come from a tampered form; dropping them
	// silently matches how the image picker treats a bogus media id.
	known := s.knownPermissions()
	for _, v := range r.PostForm["perm"] {
		p := auth.Permission(v)
		if known[p] && !slices.Contains(u.Permissions, p) {
			u.Permissions = append(u.Permissions, p)
		}
	}

	if u.Name == "" {
		errs["name"] = s.tr(r, "Name is required.")
	}
	if u.Email == "" {
		errs["email"] = s.tr(r, "Email is required.")
	} else if _, err := mail.ParseAddress(u.Email); err != nil {
		errs["email"] = s.tr(r, "That doesn't look like a valid email address.")
	}
	// Role assignment is bounded by the actor's own role, and superadmin is
	// bounded separately from admin: IsAdmin is true for both, so checking
	// only that would let an admin mint a superadmin and log in as it,
	// walking around every SuperadminOnly gate in the product.
	actor := s.currentUser(r)
	switch {
	case !u.Role.Valid():
		errs["role"] = s.tr(r, "Choose a role.")
	case actor == nil:
		errs["role"] = s.tr(r, "Only administrators can assign admin roles.")
	case u.Role == auth.RoleSuperadmin && !actor.Role.IsSuperadmin():
		errs["role"] = s.tr(r, "Only superadministrators can assign the superadmin role.")
	case u.Role != auth.RoleEditor && !actor.Role.IsAdmin():
		errs["role"] = s.tr(r, "Only administrators can assign admin roles.")
	}
	if isNew && password == "" {
		errs["password"] = s.tr(r, "Password is required.")
	}
	if password != "" && len(password) < minPasswordLength {
		errs["password"] = s.tr(r, "Password must be at least 8 characters.")
	}

	return u, password, errs
}

func (s *server) renderUserForm(w http.ResponseWriter, r *http.Request, u *auth.User, isNew bool, errs map[string]string) {
	status := http.StatusOK
	if len(errs) > 0 {
		status = http.StatusUnprocessableEntity
	}
	data := s.newTemplateData(r)
	data.FormUser = u
	data.IsNew = isNew
	data.FormErrors = errs
	data.Permissions = s.deps.Permissions
	s.render(w, status, "user_form", data)
}

// userFromURL loads the user identified by the {id} URL parameter, writing
// a 404 and returning ok=false when it is missing or malformed.
func (s *server) userFromURL(w http.ResponseWriter, r *http.Request) (*auth.User, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return nil, false
	}
	u, err := s.deps.Users.GetByID(r.Context(), id)
	if errors.Is(err, auth.ErrNotFound) {
		http.NotFound(w, r)
		return nil, false
	}
	if err != nil {
		s.serverError(w, err)
		return nil, false
	}
	return u, true
}
