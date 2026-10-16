package admin

import (
	"testing"

	"github.com/tsawler/cms/auth"
)

// canManageUser answers two questions that used to be answered separately
// and disagreed: may this request edit that account (the route), and should
// this row offer an Edit link (the users table). The full matrix is cheap
// to state, so state it.
func TestCanManageUser(t *testing.T) {
	super := &auth.User{ID: 1, Role: auth.RoleSuperadmin}
	admin := &auth.User{ID: 2, Role: auth.RoleAdmin}
	// An editor who holds PermUsers — the only non-admin who reaches the
	// users section at all.
	manager := &auth.User{ID: 3, Role: auth.RoleEditor, Permissions: []auth.Permission{auth.PermUsers}}

	targetSuper := &auth.User{ID: 4, Role: auth.RoleSuperadmin}
	targetAdmin := &auth.User{ID: 5, Role: auth.RoleAdmin}
	targetEditor := &auth.User{ID: 6, Role: auth.RoleEditor}

	cases := []struct {
		name   string
		actor  *auth.User
		target *auth.User
		want   bool
	}{
		{"superadmin manages a superadmin", super, targetSuper, true},
		{"superadmin manages an admin", super, targetAdmin, true},
		{"superadmin manages an editor", super, targetEditor, true},

		{"admin cannot manage a superadmin", admin, targetSuper, false},
		{"admin manages a fellow admin", admin, targetAdmin, true},
		{"admin manages an editor", admin, targetEditor, true},

		{"user manager cannot manage a superadmin", manager, targetSuper, false},
		{"user manager cannot manage an admin", manager, targetAdmin, false},
		{"user manager manages an editor", manager, targetEditor, true},

		{"superadmin manages themselves", super, super, true},
		{"admin manages themselves", admin, admin, true},

		{"a signed-out request manages nobody", nil, targetEditor, false},
		{"a missing target is refused", admin, nil, false},
	}
	for _, c := range cases {
		if got := canManageUser(c.actor, c.target); got != c.want {
			t.Errorf("%s: canManageUser = %v, want %v", c.name, got, c.want)
		}
	}
}

// Changing an account's email, password or two-factor enrollment is a way
// to sign in as it, so it takes covering every grant the account holds —
// managing the account is not enough.
func TestMayResetCredentials(t *testing.T) {
	const vehicles auth.Permission = "vehicles" // binds admins too
	const parts auth.Permission = "parts"       // an ordinary host grant
	known := map[auth.Permission]bool{
		auth.PermBlogs: true, auth.PermNews: true, auth.PermPages: true, auth.PermUsers: true,
		vehicles: true, parts: true,
	}
	gated := map[auth.Permission]bool{vehicles: true}

	editor := func(id int64, perms ...auth.Permission) *auth.User {
		return &auth.User{ID: id, Role: auth.RoleEditor, Permissions: perms}
	}
	super := &auth.User{ID: 1, Role: auth.RoleSuperadmin}
	admin := &auth.User{ID: 2, Role: auth.RoleAdmin}
	vehicleAdmin := &auth.User{ID: 3, Role: auth.RoleAdmin, Permissions: []auth.Permission{vehicles}}
	manager := editor(4, auth.PermUsers, auth.PermBlogs)

	cases := []struct {
		name   string
		actor  *auth.User
		target *auth.User
		want   bool
	}{
		{"manager may reset an editor holding nothing", manager, editor(10), true},
		{"manager may reset an editor whose grants they hold", manager, editor(11, auth.PermBlogs, auth.PermUsers), true},
		{"manager may not reset an editor holding pages", manager, editor(12, auth.PermBlogs, auth.PermPages), false},
		{"manager may not reset an editor holding a host grant", manager, editor(13, parts), false},
		{"a grant the deployment no longer declares locks nothing", manager, editor(14, "retired"), true},
		{"manager may reset themselves", manager, manager, true},
		{"manager may not reset an admin at all", manager, &auth.User{ID: 15, Role: auth.RoleAdmin}, false},

		{"admin may reset an editor holding any ordinary grant", admin, editor(20, auth.PermPages, parts), true},
		{"admin may not reset an account holding an admin-binding grant they lack", admin, editor(21, vehicles), false},
		{"admin may not reset a fellow admin holding one", admin, vehicleAdmin, false},
		{"admin holding the grant may reset that admin", vehicleAdmin, &auth.User{ID: 22, Role: auth.RoleAdmin, Permissions: []auth.Permission{vehicles}}, true},
		{"admin may not reset a superadmin", admin, &auth.User{ID: 23, Role: auth.RoleSuperadmin}, false},

		{"superadmin may reset anyone", super, vehicleAdmin, true},
		{"a signed-out request resets nobody", nil, editor(30), false},
	}
	for _, c := range cases {
		if got := mayResetCredentials(c.actor, c.target, known, gated); got != c.want {
			t.Errorf("%s: mayResetCredentials = %v, want %v", c.name, got, c.want)
		}
	}
}
