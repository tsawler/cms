package admin

// A site must never run out of superadmins. Only a superadmin can grant
// the role, and SeedAdmin is a no-op the moment any account exists, so a
// site that reaches zero cannot get anybody back in without an edit
// straight to the database — and loses snippets, the Pages section,
// masquerade and the site lock on the way.
//
// The reachable way in was a superadmin demoting themselves. The
// self-edit guard asked whether the *admin* role was being dropped, and
// IsAdmin() is true for superadmin too, so superadmin-to-admin kept it
// and sailed through.

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/tsawler/cms/auth"
	"github.com/tsawler/cms/internal/dbtest"
	"github.com/tsawler/cms/internal/sqldb"
)

const lastSuperadminMsg = "only superadministrator left"

// editForm is the user form filled in as an edit of u, with the caller's
// changes applied on top.
func editForm(csrf string, u *auth.User, changes url.Values) url.Values {
	form := url.Values{
		"csrf_token": {csrf},
		"name":       {u.Name},
		"email":      {u.Email},
		"role":       {string(u.Role)},
		"active":     {"on"},
	}
	for k, v := range changes {
		form[k] = v
	}
	return form
}

func TestLastSuperadminCannotDemoteThemselves(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		srv, users := settingsTestServer(t, db)
		super := seedPermUser(t, users, "super@example.com", auth.RoleSuperadmin)
		seedPermUser(t, users, "admin@example.com", auth.RoleAdmin)
		seedPermUser(t, users, "editor@example.com", auth.RoleEditor)

		client := newClient(t)
		logIn(t, srv, client, "super@example.com", "password123")
		csrf := csrfFrom(t, srv, client, "/admin/users")

		resp, page := postForm(t, srv, client, userPath(super.ID),
			editForm(csrf, super, url.Values{"role": {"admin"}}))
		if resp.StatusCode != http.StatusUnprocessableEntity {
			t.Errorf("self-demotion: status = %d, want 422", resp.StatusCode)
		}
		if !strings.Contains(page, lastSuperadminMsg) {
			t.Errorf("self-demotion: missing the explanation:\n%s", page)
		}

		// And it did not happen: an admin-role account cannot be given
		// the superadmin role back by anyone, so a wrong answer here is
		// permanent.
		got, err := users.GetByID(ctx, super.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Role != auth.RoleSuperadmin {
			t.Fatalf("role is now %q — the site has no superadmin and no way to make one", got.Role)
		}
	})
}

// The rule is "leave one behind", not "superadmins are frozen". With a
// second superadmin in place the same edit goes through.
func TestSuperadminCanStepDownOnceAnotherExists(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		srv, users := settingsTestServer(t, db)
		super := seedPermUser(t, users, "super@example.com", auth.RoleSuperadmin)
		seedPermUser(t, users, "second@example.com", auth.RoleSuperadmin)

		client := newClient(t)
		logIn(t, srv, client, "super@example.com", "password123")
		csrf := csrfFrom(t, srv, client, "/admin/users")

		resp, page := postForm(t, srv, client, userPath(super.ID),
			editForm(csrf, super, url.Values{"role": {"admin"}}))
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("stepping down with a second superadmin present: status = %d:\n%s", resp.StatusCode, page)
		}
		got, err := users.GetByID(ctx, super.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Role != auth.RoleAdmin {
			t.Errorf("role = %q, want admin", got.Role)
		}
	})
}

// An inactive superadmin is not one who can log in, so it does not count
// as the somebody left behind.
func TestDeactivatedSuperadminDoesNotCountAsTheOneLeft(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		srv, users := settingsTestServer(t, db)
		super := seedPermUser(t, users, "super@example.com", auth.RoleSuperadmin)
		asleep := seedPermUser(t, users, "asleep@example.com", auth.RoleSuperadmin)
		asleep.Active = false
		if err := users.Update(ctx, asleep); err != nil {
			t.Fatal(err)
		}

		client := newClient(t)
		logIn(t, srv, client, "super@example.com", "password123")
		csrf := csrfFrom(t, srv, client, "/admin/users")

		resp, page := postForm(t, srv, client, userPath(super.ID),
			editForm(csrf, super, url.Values{"role": {"admin"}}))
		if resp.StatusCode != http.StatusUnprocessableEntity {
			t.Errorf("self-demotion with only a deactivated superadmin left: status = %d, want 422", resp.StatusCode)
		}
		if !strings.Contains(page, lastSuperadminMsg) {
			t.Errorf("missing the explanation:\n%s", page)
		}
	})
}

// The roundabout route: a superadmin masquerades as another superadmin
// and demotes the account they came from, leaving the session the only
// one — then demotes that too. Each step looks like one superadmin
// managing another, which every role rule allows.
func TestMasqueradeCannotDrainTheSuperadmins(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		srv, users := settingsTestServer(t, db)
		owner := seedPermUser(t, users, "owner@example.com", auth.RoleSuperadmin)
		other := seedPermUser(t, users, "other@example.com", auth.RoleSuperadmin)

		client := newClient(t)
		logIn(t, srv, client, "owner@example.com", "password123")
		csrf := csrfFrom(t, srv, client, "/admin/users")
		postForm(t, srv, client, userPath(other.ID)+"/masquerade", url.Values{"csrf_token": {csrf}})

		// Now signed in as "other". Demoting the owner is allowed —
		// there are still two superadmins at that moment.
		csrf = csrfFrom(t, srv, client, "/admin/users")
		resp, page := postForm(t, srv, client, userPath(owner.ID),
			editForm(csrf, owner, url.Values{"role": {"admin"}}))
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("demoting the other superadmin: status = %d:\n%s", resp.StatusCode, page)
		}

		// The session is the last superadmin now, and must not be able to
		// finish the job.
		csrf = csrfFrom(t, srv, client, "/admin/users")
		resp, page = postForm(t, srv, client, userPath(other.ID),
			editForm(csrf, other, url.Values{"role": {"admin"}}))
		if resp.StatusCode != http.StatusUnprocessableEntity {
			t.Errorf("demoting the last superadmin from a masquerade: status = %d, want 422", resp.StatusCode)
		}
		if !strings.Contains(page, lastSuperadminMsg) {
			t.Errorf("missing the explanation:\n%s", page)
		}

		got, err := users.GetByID(ctx, other.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Role != auth.RoleSuperadmin {
			t.Fatal("the site was left with no superadmin at all")
		}
	})
}

// CountOtherActiveSuperadmins is the query the rule rests on.
func TestCountOtherActiveSuperadmins(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		users := auth.NewStore(db)
		a := seedPermUser(t, users, "a@example.com", auth.RoleSuperadmin)
		b := seedPermUser(t, users, "b@example.com", auth.RoleSuperadmin)
		seedPermUser(t, users, "admin@example.com", auth.RoleAdmin)
		seedPermUser(t, users, "editor@example.com", auth.RoleEditor)

		if n, err := users.CountOtherActiveSuperadmins(ctx, a.ID); err != nil || n != 1 {
			t.Errorf("with two superadmins: n = %d, err = %v, want 1", n, err)
		}

		// Other roles are not superadmins, however many there are.
		b.Active = false
		if err := users.Update(ctx, b); err != nil {
			t.Fatal(err)
		}
		if n, err := users.CountOtherActiveSuperadmins(ctx, a.ID); err != nil || n != 0 {
			t.Errorf("with the second deactivated: n = %d, err = %v, want 0", n, err)
		}
		// And the excluded account is genuinely excluded, not merely
		// outnumbered.
		if n, err := users.CountOtherActiveSuperadmins(ctx, b.ID); err != nil || n != 1 {
			t.Errorf("counting from the deactivated one: n = %d, err = %v, want 1", n, err)
		}
	})
}
