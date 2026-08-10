package auth

import (
	"context"
	"regexp"
	"slices"
	"strings"

	"github.com/tsawler/cms/internal/sqldb"
)

// Permission names one grantable capability. The built-in permissions
// cover the CMS's own admin areas; a deployment may declare more (via
// cms.Config.Permissions or an admin section's Permission field) and
// check them in its own handlers with User.Can.
//
// Permissions gate editor-role accounts only: the admin and superadmin
// roles implicitly hold every permission, built-in or custom.
type Permission string

const (
	// PermBlogs grants the blog feed: creating, editing, and publishing
	// blog posts, in the admin and the in-place editor.
	PermBlogs Permission = "blogs"
	// PermNews grants the news feed, the same way PermBlogs grants blog.
	PermNews Permission = "news"
	// PermPages grants site pages and everything that shapes them:
	// creating and editing pages, the navigation menus, and the
	// non-code site settings (name, logo, menu alignment).
	PermPages Permission = "pages"
	// PermUsers grants user management. A non-admin holder manages
	// editor accounts only: they cannot touch admin accounts, assign
	// admin roles, or grant permissions they do not hold themselves.
	PermUsers Permission = "users"
)

// BuiltinPermissions returns the permissions the CMS itself defines, in
// the order the user form lists them.
func BuiltinPermissions() []Permission {
	return []Permission{PermBlogs, PermNews, PermPages, PermUsers}
}

// permKeyRe is deliberately narrow: keys appear in form values and DB
// rows, and a lowercase identifier never needs escaping in either.
var permKeyRe = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)

// ValidPermissionKey reports whether k is acceptable as a permission
// name: a lowercase letter followed by up to 63 lowercase letters,
// digits, hyphens, or underscores.
func ValidPermissionKey(k string) bool { return permKeyRe.MatchString(k) }

// PermissionForSlug returns the permission that governs the page at
// slug. Post slugs always live under their feed ("blog/…", "news/…" —
// see content.Post), so the slug alone decides: those prefixes map to
// the feed permissions and every other slug is a site page.
func PermissionForSlug(slug string) Permission {
	switch {
	case strings.HasPrefix(slug, "blog/"):
		return PermBlogs
	case strings.HasPrefix(slug, "news/"):
		return PermNews
	default:
		return PermPages
	}
}

// PermissionForFeed returns the permission governing a post feed name.
func PermissionForFeed(feed string) Permission {
	if feed == "news" {
		return PermNews
	}
	return PermBlogs
}

// Can reports whether the user holds the permission. Admin and
// superadmin roles hold every permission; editors hold what has been
// granted to them. Safe to call on a nil user (false).
func (u *User) Can(p Permission) bool {
	if u == nil {
		return false
	}
	if u.Role.IsAdmin() {
		return true
	}
	return slices.Contains(u.Permissions, p)
}

// CanAny reports whether the user holds at least one of the permissions.
func (u *User) CanAny(perms ...Permission) bool {
	for _, p := range perms {
		if u.Can(p) {
			return true
		}
	}
	return false
}

// loadPermissions fills u.Permissions from the grants table.
func (s *Store) loadPermissions(ctx context.Context, u *User) error {
	rows, err := s.db.Query(ctx,
		"SELECT permission FROM cms_user_permissions WHERE user_id = $1 ORDER BY permission", u.ID)
	if err != nil {
		return err
	}
	perms, err := sqldb.CollectRows(rows, func(row sqldb.Scanner) (Permission, error) {
		var p Permission
		err := row.Scan(&p)
		return p, err
	})
	if err != nil {
		return err
	}
	u.Permissions = perms
	return nil
}

// ReplacePermissions makes perms the user's exact set of grants,
// removing any not listed. Duplicates in perms are collapsed. The
// change is atomic: readers see the old set or the new one, never a
// half-written mix.
func (s *Store) ReplacePermissions(ctx context.Context, userID int64, perms []Permission) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, "DELETE FROM cms_user_permissions WHERE user_id = $1", userID); err != nil {
		return err
	}
	seen := make(map[Permission]bool, len(perms))
	for _, p := range perms {
		if seen[p] {
			continue
		}
		seen[p] = true
		if _, err := tx.Exec(ctx,
			"INSERT INTO cms_user_permissions (user_id, permission) VALUES ($1, $2)", userID, p); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
