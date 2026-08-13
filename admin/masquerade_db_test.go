package admin

// Masquerade, end to end against a real database: a superadmin becomes
// another user and works the admin with that user's powers, then switches
// back to their own account — and the doors that must stay shut do: no
// masquerading for lesser roles, none into inactive accounts, and an exit
// without a masquerade is a harmless no-op.

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/tsawler/cms/auth"
	"github.com/tsawler/cms/internal/dbtest"
	"github.com/tsawler/cms/internal/sqldb"
)

func masqueradePath(id int64) string {
	return "/admin/users/" + strconv.FormatInt(id, 10) + "/masquerade"
}

// getPage GETs a path and returns the response and its body.
func getPage(t *testing.T, srv *httptest.Server, client *http.Client, path string) (*http.Response, string) {
	t.Helper()
	resp, err := client.Get(srv.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, string(body)
}

func TestMasqueradeRoundTrip(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		srv, users := settingsTestServer(t, db)
		seedPermUser(t, users, "super@example.com", auth.RoleSuperadmin)
		editor := seedPermUser(t, users, "ed@example.com", auth.RoleEditor)

		client := newClient(t)
		logIn(t, srv, client, "super@example.com", "password123")
		csrf := csrfFrom(t, srv, client, "/admin/users")

		// The superadmin's users list offers Become on the editor's row
		// and not on their own.
		resp, page := getPage(t, srv, client, "/admin/users")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("users list: status = %d, want 200", resp.StatusCode)
		}
		if got := strings.Count(page, "/masquerade"); got != 1 {
			t.Fatalf("users list has %d Become forms, want 1 (editor's row only):\n%s", got, page)
		}

		// Become the editor: the dashboard now belongs to them, bannered
		// with the way back.
		resp, page = postForm(t, srv, client, masqueradePath(editor.ID), url.Values{"csrf_token": {csrf}})
		if resp.StatusCode != http.StatusOK || !strings.HasSuffix(resp.Request.URL.Path, "/admin/") {
			t.Fatalf("masquerade: status = %d at %s, want the dashboard", resp.StatusCode, resp.Request.URL.Path)
		}
		if !strings.Contains(page, "You are working as") || !strings.Contains(page, "Test ed@example.com") {
			t.Fatalf("dashboard missing the masquerade banner for the editor:\n%s", page)
		}
		if !strings.Contains(page, "/masquerade/exit") {
			t.Fatalf("masquerade banner has no exit form:\n%s", page)
		}

		// The session now carries the editor's powers, nothing more: the
		// users area is closed to it.
		resp, _ = getPage(t, srv, client, "/admin/users")
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("users list while masquerading as editor: status = %d, want 403", resp.StatusCode)
		}
		// And so is starting another masquerade from it.
		resp, _ = postForm(t, srv, client, masqueradePath(editor.ID), url.Values{"csrf_token": {csrf}})
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("masquerade as editor: status = %d, want 403", resp.StatusCode)
		}

		// Switch back: the superadmin lands on the users list, themselves
		// again, with no banner left.
		resp, page = postForm(t, srv, client, "/admin/masquerade/exit", url.Values{"csrf_token": {csrf}})
		if resp.StatusCode != http.StatusOK || !strings.HasSuffix(resp.Request.URL.Path, "/admin/users") {
			t.Fatalf("exit: status = %d at %s, want the users list", resp.StatusCode, resp.Request.URL.Path)
		}
		if !strings.Contains(page, "You are back in your own account.") {
			t.Fatalf("exit landed without its flash:\n%s", page)
		}
		if strings.Contains(page, "You are working as") {
			t.Fatalf("banner still up after exit:\n%s", page)
		}
	})
}

func TestMasqueradeClosedDoors(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		srv, users := settingsTestServer(t, db)
		super := seedPermUser(t, users, "super@example.com", auth.RoleSuperadmin)
		seedPermUser(t, users, "admin@example.com", auth.RoleAdmin)
		manager := seedPermUser(t, users, "manager@example.com", auth.RoleEditor, auth.PermUsers)
		dormant := seedPermUser(t, users, "dormant@example.com", auth.RoleEditor)
		dormant.Active = false
		if err := users.Update(context.Background(), dormant); err != nil {
			t.Fatal(err)
		}

		// An admin holds the users permission but not the top role: the
		// row shows no Become button and the route answers 403.
		client := newClient(t)
		logIn(t, srv, client, "admin@example.com", "password123")
		csrf := csrfFrom(t, srv, client, "/admin/users")
		if _, page := getPage(t, srv, client, "/admin/users"); strings.Contains(page, "/masquerade") {
			t.Fatalf("admin's users list offers Become:\n%s", page)
		}
		resp, _ := postForm(t, srv, client, masqueradePath(manager.ID), url.Values{"csrf_token": {csrf}})
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("masquerade as admin: status = %d, want 403", resp.StatusCode)
		}

		// A users-permission editor: same answer.
		client = newClient(t)
		logIn(t, srv, client, "manager@example.com", "password123")
		csrf = csrfFrom(t, srv, client, "/admin/users")
		resp, _ = postForm(t, srv, client, masqueradePath(super.ID), url.Values{"csrf_token": {csrf}})
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("masquerade as users-permission editor: status = %d, want 403", resp.StatusCode)
		}

		// A superadmin cannot become an inactive account, and an exit
		// without a masquerade just goes home.
		client = newClient(t)
		logIn(t, srv, client, "super@example.com", "password123")
		csrf = csrfFrom(t, srv, client, "/admin/users")
		resp, page := postForm(t, srv, client, masqueradePath(dormant.ID), url.Values{"csrf_token": {csrf}})
		if resp.StatusCode != http.StatusOK || !strings.HasSuffix(resp.Request.URL.Path, "/admin/users") {
			t.Fatalf("masquerade as inactive: status = %d at %s, want back on the users list", resp.StatusCode, resp.Request.URL.Path)
		}
		if !strings.Contains(page, "You cannot become an inactive user.") {
			t.Fatalf("no inactive-user message:\n%s", page)
		}
		if strings.Contains(page, "You are working as") {
			t.Fatalf("masquerading despite the inactive target:\n%s", page)
		}
		resp, _ = postForm(t, srv, client, "/admin/masquerade/exit", url.Values{"csrf_token": {csrf}})
		if resp.StatusCode != http.StatusOK || !strings.HasSuffix(resp.Request.URL.Path, "/admin/") {
			t.Fatalf("exit without masquerade: status = %d at %s, want the dashboard", resp.StatusCode, resp.Request.URL.Path)
		}
	})
}
