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
