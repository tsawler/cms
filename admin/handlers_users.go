package admin

import (
	"errors"
	"net/http"
	"net/mail"
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
	data := s.newTemplateData(r)
	data.Users = users
	s.render(w, http.StatusOK, "users", data)
}

func (s *server) userNew(w http.ResponseWriter, r *http.Request) {
	data := s.newTemplateData(r)
	data.IsNew = true
	data.FormUser = &auth.User{Role: auth.RoleEditor, Active: true}
	s.render(w, http.StatusOK, "user_form", data)
}

func (s *server) userCreate(w http.ResponseWriter, r *http.Request) {
	form, password, errs := s.parseUserForm(r, true)

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
			s.renderUserForm(w, r, form, true, map[string]string{"email": "That email address is already in use."})
			return
		}
		s.serverError(w, err)
		return
	}

	s.flash(r, "User created.")
	http.Redirect(w, r, s.deps.AdminPath+"/users", http.StatusSeeOther)
}

func (s *server) userEdit(w http.ResponseWriter, r *http.Request) {
	u, ok := s.userFromURL(w, r)
	if !ok {
		return
	}
	s.renderUserForm(w, r, u, false, nil)
}

func (s *server) userUpdate(w http.ResponseWriter, r *http.Request) {
	existing, ok := s.userFromURL(w, r)
	if !ok {
		return
	}

	form, password, errs := s.parseUserForm(r, false)
	form.ID = existing.ID

	// Guard rails: an admin editing their own account cannot lock
	// themselves out by deactivating it or dropping the admin role.
	if self := s.currentUser(r); self != nil && self.ID == existing.ID {
		if !form.Active {
			errs["active"] = "You cannot deactivate your own account."
		}
		if form.Role != auth.RoleAdmin {
			errs["role"] = "You cannot remove your own admin role."
		}
	}

	if len(errs) > 0 {
		s.renderUserForm(w, r, form, false, errs)
		return
	}

	if err := s.deps.Users.Update(r.Context(), form); err != nil {
		if errors.Is(err, auth.ErrDuplicateEmail) {
			s.renderUserForm(w, r, form, false, map[string]string{"email": "That email address is already in use."})
			return
		}
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

	s.flash(r, "User updated.")
	http.Redirect(w, r, s.deps.AdminPath+"/users", http.StatusSeeOther)
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

	if u.Name == "" {
		errs["name"] = "Name is required."
	}
	if u.Email == "" {
		errs["email"] = "Email is required."
	} else if _, err := mail.ParseAddress(u.Email); err != nil {
		errs["email"] = "That doesn't look like a valid email address."
	}
	if !u.Role.Valid() {
		errs["role"] = "Choose a role."
	}
	if isNew && password == "" {
		errs["password"] = "Password is required."
	}
	if password != "" && len(password) < minPasswordLength {
		errs["password"] = "Password must be at least 8 characters."
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
