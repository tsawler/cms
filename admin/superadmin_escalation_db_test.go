package admin

// Privilege escalation, end to end against a real database: the admin role
// must not be able to reach the superadmin role, by any of the four routes
// that once led there.
//
// IsAdmin() is true for admin *and* superadmin, so a guard written as
// "!IsAdmin()" reads like a superadmin check and is not one. Every case
// below failed before the role gates were split apart, and each fails
// independently — an admin who cannot mint a superadmin but can still edit
// one just sets its password and logs in instead, so the create, edit,
// update, and delete paths all have to hold at once.

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/tsawler/cms/auth"
	"github.com/tsawler/cms/internal/dbtest"
	"github.com/tsawler/cms/internal/sqldb"
)

func userPath(id int64) string {
	return "/admin/users/" + strconv.FormatInt(id, 10)
}

// newUserForm is the user form's fields with a role swapped in.
func newUserForm(csrf, email, role string) url.Values {
	return url.Values{
		"csrf_token": {csrf},
		"name":       {"Escalated"},
		"email":      {email},
		"password":   {"password123"},
		"role":       {role},
		"active":     {"on"},
	}
}

func TestAdminCannotEscalateToSuperadmin(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		srv, users := settingsTestServer(t, db)
		seedPermUser(t, users, "admin@example.com", auth.RoleAdmin)
		super := seedPermUser(t, users, "super@example.com", auth.RoleSuperadmin)

		client := newClient(t)
		logIn(t, srv, client, "admin@example.com", "password123")
		csrf := csrfFrom(t, srv, client, "/admin/users")

		// 1. Creating a superadmin outright.
		resp, page := postForm(t, srv, client, "/admin/users/new",
			newUserForm(csrf, "new-super@example.com", "superadmin"))
		if resp.StatusCode != http.StatusUnprocessableEntity {
			t.Errorf("create superadmin: status = %d, want 422", resp.StatusCode)
		}
		if !strings.Contains(page, "Only superadministrators can assign the superadmin role.") {
			t.Errorf("create superadmin: missing the role error:\n%s", page)
		}
		if _, err := users.GetByEmail(context.Background(), "new-super@example.com"); err == nil {
			t.Error("create superadmin: the account was created anyway")
		}

		// 2. Opening an existing superadmin's edit form.
		resp, _ = getPage(t, srv, client, userPath(super.ID))
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("edit superadmin: status = %d, want 403", resp.StatusCode)
		}

		// 3. Posting an update to one anyway — the edit form being closed
		// is no defence if the save it posts to is open.
		resp, _ = postForm(t, srv, client, userPath(super.ID),
			newUserForm(csrf, "super@example.com", "superadmin"))
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("update superadmin: status = %d, want 403", resp.StatusCode)
		}

		// 4. Deleting one.
		resp, _ = postForm(t, srv, client, userPath(super.ID)+"/delete",
			url.Values{"csrf_token": {csrf}})
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("delete superadmin: status = %d, want 403", resp.StatusCode)
		}
		if _, err := users.GetByID(context.Background(), super.ID); err != nil {
			t.Errorf("delete superadmin: the account is gone: %v", err)
		}

		// The form must not offer what the save would refuse.
		_, page = getPage(t, srv, client, "/admin/users/new")
		if strings.Contains(page, `value="superadmin"`) {
			t.Errorf("new user form offers the superadmin role to an admin:\n%s", page)
		}
	})
}

// The gate bounds the admin role specifically; a superadmin still runs the
// section. Without this, "fixing" the escalation by refusing everyone would
// pass the test above and quietly break user management.
func TestSuperadminStillManagesSuperadmins(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		srv, users := settingsTestServer(t, db)
		seedPermUser(t, users, "super@example.com", auth.RoleSuperadmin)
		other := seedPermUser(t, users, "other-super@example.com", auth.RoleSuperadmin)

		client := newClient(t)
		logIn(t, srv, client, "super@example.com", "password123")
		csrf := csrfFrom(t, srv, client, "/admin/users")

		resp, page := postForm(t, srv, client, "/admin/users/new",
			newUserForm(csrf, "made-super@example.com", "superadmin"))
		if resp.StatusCode != http.StatusOK {
			t.Errorf("superadmin creating a superadmin: status = %d, want 200:\n%s", resp.StatusCode, page)
		}
		made, err := users.GetByEmail(context.Background(), "made-super@example.com")
		if err != nil {
			t.Fatalf("superadmin creating a superadmin: not created: %v", err)
		}
		if made.Role != auth.RoleSuperadmin {
			t.Errorf("created role = %q, want superadmin", made.Role)
		}

		resp, _ = getPage(t, srv, client, userPath(other.ID))
		if resp.StatusCode != http.StatusOK {
			t.Errorf("superadmin editing a superadmin: status = %d, want 200", resp.StatusCode)
		}

		_, page = getPage(t, srv, client, "/admin/users/new")
		if !strings.Contains(page, `value="superadmin"`) {
			t.Errorf("new user form withholds the superadmin role from a superadmin:\n%s", page)
		}
	})
}

// The users table offers Edit exactly where the route allows it. A link
// that 403s is only cosmetic, but the two deciding the same question in
// two places is how they drift apart — the table now reads the rule the
// route enforces rather than restating it.
func TestUsersListOffersEditOnlyWhereAllowed(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		srv, users := settingsTestServer(t, db)
		seedPermUser(t, users, "manager@example.com", auth.RoleEditor, auth.PermUsers)
		editor := seedPermUser(t, users, "ed@example.com", auth.RoleEditor)
		admin := seedPermUser(t, users, "admin@example.com", auth.RoleAdmin)
		super := seedPermUser(t, users, "super@example.com", auth.RoleSuperadmin)

		editLink := func(id int64) string {
			return `href="` + userPath(id) + `"`
		}

		// A non-admin user manager: editors only.
		client := newClient(t)
		logIn(t, srv, client, "manager@example.com", "password123")
		_, page := getPage(t, srv, client, "/admin/users")
		for _, tc := range []struct {
			name string
			id   int64
			want bool
		}{
			{"editor", editor.ID, true},
			{"admin", admin.ID, false},
			{"superadmin", super.ID, false},
		} {
			if got := strings.Contains(page, editLink(tc.id)); got != tc.want {
				t.Errorf("user manager's list: Edit link for %s = %v, want %v", tc.name, got, tc.want)
			}
		}

		// An admin: everyone but the superadmin.
		adminClient := newClient(t)
		logIn(t, srv, adminClient, "admin@example.com", "password123")
		_, page = getPage(t, srv, adminClient, "/admin/users")
		if !strings.Contains(page, editLink(editor.ID)) {
			t.Error("admin's list: no Edit link for the editor")
		}
		if strings.Contains(page, editLink(super.ID)) {
			t.Error("admin's list: Edit link offered for the superadmin")
		}

		// A superadmin: everyone.
		superClient := newClient(t)
		logIn(t, srv, superClient, "super@example.com", "password123")
		_, page = getPage(t, srv, superClient, "/admin/users")
		for _, id := range []int64{editor.ID, admin.ID, super.ID} {
			if !strings.Contains(page, editLink(id)) {
				t.Errorf("superadmin's list: no Edit link for user %d", id)
			}
		}
	})
}

// An admin keeps the powers the gate was not aimed at: editors and fellow
// admins are still theirs to manage.
func TestAdminStillManagesNonSuperadmins(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		srv, users := settingsTestServer(t, db)
		seedPermUser(t, users, "admin@example.com", auth.RoleAdmin)
		editor := seedPermUser(t, users, "ed@example.com", auth.RoleEditor)
		peer := seedPermUser(t, users, "peer@example.com", auth.RoleAdmin)

		client := newClient(t)
		logIn(t, srv, client, "admin@example.com", "password123")
		csrf := csrfFrom(t, srv, client, "/admin/users")

		for _, tc := range []struct {
			name string
			id   int64
		}{{"editor", editor.ID}, {"fellow admin", peer.ID}} {
			resp, _ := getPage(t, srv, client, userPath(tc.id))
			if resp.StatusCode != http.StatusOK {
				t.Errorf("admin editing %s: status = %d, want 200", tc.name, resp.StatusCode)
			}
		}

		resp, page := postForm(t, srv, client, "/admin/users/new",
			newUserForm(csrf, "new-admin@example.com", "admin"))
		if resp.StatusCode != http.StatusOK {
			t.Errorf("admin creating an admin: status = %d, want 200:\n%s", resp.StatusCode, page)
		}
	})
}
