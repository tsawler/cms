package auth

import "testing"

func TestCan(t *testing.T) {
	editor := &User{Role: RoleEditor, Permissions: []Permission{PermBlogs, "vehicles"}}
	cases := []struct {
		name string
		user *User
		perm Permission
		want bool
	}{
		{"nil user", nil, PermBlogs, false},
		{"editor with grant", editor, PermBlogs, true},
		{"editor with custom grant", editor, "vehicles", true},
		{"editor without grant", editor, PermPages, false},
		{"editor no grants at all", &User{Role: RoleEditor}, PermBlogs, false},
		{"admin, empty grants", &User{Role: RoleAdmin}, PermUsers, true},
		{"admin, custom permission", &User{Role: RoleAdmin}, "vehicles", true},
		{"superadmin, empty grants", &User{Role: RoleSuperadmin}, PermNews, true},
	}
	for _, c := range cases {
		if got := c.user.Can(c.perm); got != c.want {
			t.Errorf("%s: Can(%q) = %v, want %v", c.name, c.perm, got, c.want)
		}
	}
}

func TestCanAny(t *testing.T) {
	editor := &User{Role: RoleEditor, Permissions: []Permission{PermNews}}
	if !editor.CanAny(PermBlogs, PermNews) {
		t.Error("CanAny(blogs, news) with a news grant = false, want true")
	}
	if editor.CanAny(PermBlogs, PermPages) {
		t.Error("CanAny(blogs, pages) with only a news grant = true, want false")
	}
	if editor.CanAny() {
		t.Error("CanAny() with no arguments = true, want false")
	}
	var nobody *User
	if nobody.CanAny(PermBlogs) {
		t.Error("nil user CanAny = true, want false")
	}
}

func TestPermissionForSlug(t *testing.T) {
	cases := map[string]Permission{
		"blog/launch-day":  PermBlogs,
		"news/big-news":    PermNews,
		"news/a/b":         PermNews,
		"about-us":         PermPages,
		"":                 PermPages, // the home page
		"blogfoo":          PermPages, // prefix must be a whole segment
		"blog":             PermPages, // the blog listing page itself
		"news":             PermPages,
		"pricing/blog/sub": PermPages,
	}
	for slug, want := range cases {
		if got := PermissionForSlug(slug); got != want {
			t.Errorf("PermissionForSlug(%q) = %q, want %q", slug, got, want)
		}
	}
}

func TestPermissionForFeed(t *testing.T) {
	if got := PermissionForFeed("news"); got != PermNews {
		t.Errorf("PermissionForFeed(news) = %q, want %q", got, PermNews)
	}
	if got := PermissionForFeed("blog"); got != PermBlogs {
		t.Errorf("PermissionForFeed(blog) = %q, want %q", got, PermBlogs)
	}
}

func TestValidPermissionKey(t *testing.T) {
	valid := []string{"vehicles", "manage-vehicles", "v2", "a", "reports_beta"}
	for _, k := range valid {
		if !ValidPermissionKey(k) {
			t.Errorf("ValidPermissionKey(%q) = false, want true", k)
		}
	}
	invalid := []string{"", "Vehicles", "manage vehicles", "2fast", "-lead", "é", "a.b",
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"} // 65 chars
	for _, k := range invalid {
		if ValidPermissionKey(k) {
			t.Errorf("ValidPermissionKey(%q) = true, want false", k)
		}
	}
}
