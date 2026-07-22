package auth

import "testing"

func TestRoles(t *testing.T) {
	cases := []struct {
		role       Role
		valid      bool
		admin      bool
		superadmin bool
	}{
		{RoleSuperadmin, true, true, true},
		{RoleAdmin, true, true, false},
		{RoleEditor, true, false, false},
		{Role("owner"), false, false, false},
		{Role(""), false, false, false},
	}
	for _, c := range cases {
		if got := c.role.Valid(); got != c.valid {
			t.Errorf("Role(%q).Valid() = %v, want %v", c.role, got, c.valid)
		}
		if got := c.role.IsAdmin(); got != c.admin {
			t.Errorf("Role(%q).IsAdmin() = %v, want %v", c.role, got, c.admin)
		}
		if got := c.role.IsSuperadmin(); got != c.superadmin {
			t.Errorf("Role(%q).IsSuperadmin() = %v, want %v", c.role, got, c.superadmin)
		}
	}
}
