package admin

// Who may reach the media library.
//
// It used to be anyone signed in: the routes sat in the requireUser group
// with no permission of their own, and the handlers checked nothing. An
// editor with no grants at all could upload, rename, move, and bulk-delete
// the whole library — including, in a deployment that shares one bucket,
// pictures belonging to sections they cannot open.
//
// The rule now is that the library belongs to people who edit something
// that carries media: the CMS's own pages, blogs, and news, plus whichever
// host permissions declared GrantsMedia. Managing users is not editing
// content, and a read-only inbox has no pictures in it.

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alexedwards/scs/v2"
	"github.com/tsawler/cms/auth"
	"github.com/tsawler/cms/internal/dbtest"
	"github.com/tsawler/cms/internal/sqldb"
)

// hostPerms mirrors a deployment like the dealership's: two sections whose
// records carry pictures, one read-only inbox that does not.
var hostPerms = []PermissionDef{
	{Key: "vehicles", Label: "Inventory", GrantsMedia: true},
	{Key: "team", Label: "Sales people & staff", AdminsNeedGrant: true, GrantsMedia: true},
	{Key: "leads", Label: "Customer leads", AdminsNeedGrant: true},
}

func mediaTestServer() *server {
	return &server{deps: Deps{
		Sessions:    scs.New(),
		Permissions: hostPerms,
	}}
}

// mediaPermissions is the set the gate consults; pinning it catches a
// host permission quietly gaining or losing library access.
func TestMediaPermissionSet(t *testing.T) {
	s := mediaTestServer()
	got := map[auth.Permission]bool{}
	for _, p := range s.mediaPermissions() {
		got[p] = true
	}

	for _, want := range []auth.Permission{auth.PermPages, auth.PermBlogs, auth.PermNews, "vehicles", "team"} {
		if !got[want] {
			t.Errorf("%q does not open the media library, and should", want)
		}
	}
	for _, unwanted := range []auth.Permission{auth.PermUsers, "leads"} {
		if got[unwanted] {
			t.Errorf("%q opens the media library, and should not", unwanted)
		}
	}
}

// The whole matrix of who gets in. canUseMedia takes a request, so these
// go through a session with a real user behind it.
func TestCanUseMedia(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		users := auth.NewStore(db)
		s := &server{deps: Deps{
			Sessions:    scs.New(),
			Users:       users,
			Permissions: hostPerms,
		}}

		cases := []struct {
			name  string
			role  auth.Role
			perms []auth.Permission
			want  bool
		}{
			{"superadmin", auth.RoleSuperadmin, nil, true},
			{"admin", auth.RoleAdmin, nil, true},
			{"page editor", auth.RoleEditor, []auth.Permission{auth.PermPages}, true},
			{"blog editor", auth.RoleEditor, []auth.Permission{auth.PermBlogs}, true},
			{"news editor", auth.RoleEditor, []auth.Permission{auth.PermNews}, true},
			{"inventory editor", auth.RoleEditor, []auth.Permission{"vehicles"}, true},
			{"team editor", auth.RoleEditor, []auth.Permission{"team"}, true},
			{"inventory and leads", auth.RoleEditor, []auth.Permission{"vehicles", "leads"}, true},

			{"leads only", auth.RoleEditor, []auth.Permission{"leads"}, false},
			{"user manager only", auth.RoleEditor, []auth.Permission{auth.PermUsers}, false},
			{"no grants at all", auth.RoleEditor, nil, false},
		}

		for i, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				email := "m" + string(rune('a'+i)) + "@example.com"
				u := seedPermUser(t, users, email, c.role, c.perms...)

				r := httptest.NewRequest("GET", "/admin/media", nil)
				var got bool
				s.deps.Sessions.LoadAndSave(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
					s.deps.Sessions.Put(r.Context(), sessionKeyUserID, u.ID)
					got = s.canUseMedia(r)
				})).ServeHTTP(httptest.NewRecorder(), r)

				if got != c.want {
					t.Errorf("canUseMedia = %v, want %v", got, c.want)
				}
			})
		}
	})
}

// A signed-out request reaches nothing: currentUser is nil and CanAny is
// nil-safe, so the gate refuses rather than panicking.
func TestCanUseMediaRefusesSignedOut(t *testing.T) {
	s := mediaTestServer()
	r := httptest.NewRequest("GET", "/admin/media", nil)
	var got bool
	s.deps.Sessions.LoadAndSave(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = s.canUseMedia(r)
	})).ServeHTTP(httptest.NewRecorder(), r)
	if got {
		t.Error("a signed-out request may use the media library")
	}
}

// A host that declares no media-bearing permission still has its own
// content grants, so the library does not become unreachable.
func TestMediaPermissionsWithoutHostGrants(t *testing.T) {
	s := &server{deps: Deps{Sessions: scs.New()}}
	if len(s.mediaPermissions()) != 3 {
		t.Errorf("mediaPermissions = %v, want the three built-in content grants",
			s.mediaPermissions())
	}
}
