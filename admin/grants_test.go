package admin

import (
	"slices"
	"testing"

	"github.com/tsawler/cms/auth"
)

// mergeGrants is the one place a grant change is bounded to what the
// actor may give or take. The gated ("admins too") rules matter most:
// an admin switched out of a section must not be able to switch
// themselves — or anyone — back in from the users page.
func TestMergeGrantsAdminsNeedGrant(t *testing.T) {
	gated := map[auth.Permission]bool{"team": true}
	perms := func(ps ...auth.Permission) []auth.Permission { return ps }

	cases := []struct {
		name      string
		actor     *auth.User
		submitted []auth.Permission
		existing  []auth.Permission
		want      []auth.Permission
	}{
		{"admin cannot self-serve a gated grant",
			&auth.User{Role: auth.RoleAdmin},
			perms("team"), nil, nil},
		{"admin cannot revoke a gated grant they don't hold",
			&auth.User{Role: auth.RoleAdmin},
			nil, perms("team"), perms("team")},
		{"admin holding the gated grant may give it",
			&auth.User{Role: auth.RoleAdmin, Permissions: perms("team")},
			perms("team"), nil, perms("team")},
		{"admin holding the gated grant may take it",
			&auth.User{Role: auth.RoleAdmin, Permissions: perms("team")},
			nil, perms("team"), nil},
		{"admin stays unrestricted on ordinary permissions",
			&auth.User{Role: auth.RoleAdmin},
			perms("vehicles", auth.PermBlogs), perms(auth.PermNews), perms("vehicles", auth.PermBlogs)},
		{"superadmin changes anything",
			&auth.User{Role: auth.RoleSuperadmin},
			perms("team"), nil, perms("team")},
		{"editor manager still bounded by what they hold",
			&auth.User{Role: auth.RoleEditor, Permissions: perms(auth.PermUsers, auth.PermBlogs)},
			perms(auth.PermBlogs, auth.PermNews), perms(auth.PermPages), perms(auth.PermBlogs, auth.PermPages)},
	}
	for _, c := range cases {
		got := mergeGrants(c.actor, c.submitted, c.existing, gated)
		same := len(got) == len(c.want)
		for _, p := range c.want {
			if !slices.Contains(got, p) {
				same = false
			}
		}
		if !same {
			t.Errorf("%s: mergeGrants = %v, want %v", c.name, got, c.want)
		}
	}
}
