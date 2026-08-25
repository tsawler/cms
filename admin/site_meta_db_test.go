package admin

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tsawler/cms/auth"
	"github.com/tsawler/cms/internal/dbtest"
	"github.com/tsawler/cms/internal/sqldb"
)

// The site's head markup is written into every public page untouched, so
// it is admin-only for the same reason the site-wide CSS and JS are. And
// like them, a save from anyone else carries the stored value through
// rather than being refused — the settings dialog sends the whole object
// every time.

const metaTag = `<meta name="google-site-verification" content="abc123">`

func TestAPISettingsSiteMeta(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := settingsServer(db)

		rec := httptest.NewRecorder()
		s.apiSaveSettings(rec, settingsRequest(t, s, auth.RoleAdmin,
			`{"siteName":"Acme","siteMeta":`+quote(metaTag)+`}`))
		if rec.Code != http.StatusOK {
			t.Fatalf("admin saving head markup: status %d, want 200: %s", rec.Code, rec.Body)
		}
		if got := storedSettings(t, s).SiteMeta; got != metaTag {
			t.Fatalf("stored head markup = %q, want the saved tag", got)
		}

		// The dialog reads it back so a save can carry it through.
		rec = httptest.NewRecorder()
		s.apiGetSettings(rec, httptest.NewRequest(http.MethodGet, "/api/settings", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("get: status %d, want 200: %s", rec.Code, rec.Body)
		}
		if got := decodeJSON(t, rec)["siteMeta"]; got != metaTag {
			t.Errorf("siteMeta = %v, want the stored tag", got)
		}
	})
}

// The site settings dialog PUTs a body that echoes the CSS and JS back
// but has never heard of the head markup. An absent field has to carry
// the stored value through, or renaming the site would drop the
// verification tag a search console is still checking for.
func TestAPISettingsAbsentSiteMetaCarriesThrough(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := settingsServer(db)

		rec := httptest.NewRecorder()
		s.apiSaveSettings(rec, settingsRequest(t, s, auth.RoleAdmin,
			`{"siteName":"Acme","siteMeta":`+quote(metaTag)+`}`))
		if rec.Code != http.StatusOK {
			t.Fatalf("save: status %d, want 200: %s", rec.Code, rec.Body)
		}

		rec = httptest.NewRecorder()
		s.apiSaveSettings(rec, settingsRequest(t, s, auth.RoleAdmin,
			`{"siteName":"Renamed","siteCss":"body{margin:0}"}`))
		if rec.Code != http.StatusOK {
			t.Fatalf("unrelated save: status %d, want 200: %s", rec.Code, rec.Body)
		}
		if got := storedSettings(t, s).SiteMeta; got != metaTag {
			t.Errorf("an unrelated save left the head markup as %q", got)
		}

		// Sent and empty is a real edit, though: clearing the field is
		// how an admin removes a tag they no longer need.
		rec = httptest.NewRecorder()
		s.apiSaveSettings(rec, settingsRequest(t, s, auth.RoleAdmin,
			`{"siteName":"Renamed","siteMeta":""}`))
		if rec.Code != http.StatusOK {
			t.Fatalf("clearing save: status %d, want 200: %s", rec.Code, rec.Body)
		}
		if got := storedSettings(t, s).SiteMeta; got != "" {
			t.Errorf("the head markup survived being cleared: %q", got)
		}
	})
}

func TestAPISettingsSiteMetaIsAdminOnly(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := settingsServer(db)

		rec := httptest.NewRecorder()
		s.apiSaveSettings(rec, settingsRequest(t, s, auth.RoleAdmin,
			`{"siteName":"Acme","siteMeta":`+quote(metaTag)+`}`))
		if rec.Code != http.StatusOK {
			t.Fatalf("save: status %d, want 200: %s", rec.Code, rec.Body)
		}

		// An editor's save goes through — the rest of the object is
		// theirs to change — but the head markup is not.
		rec = httptest.NewRecorder()
		s.apiSaveSettings(rec, settingsRequest(t, s, auth.RoleEditor,
			`{"siteName":"Acme","siteMeta":"<meta name=\"x\" content=\"mine\">"}`))
		if rec.Code != http.StatusOK {
			t.Fatalf("editor save: status %d, want 200: %s", rec.Code, rec.Body)
		}
		if got := storedSettings(t, s).SiteMeta; got != metaTag {
			t.Errorf("an editor rewrote the head markup to %q", got)
		}
	})
}

func TestAPISettingsSiteMetaTooLong(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := settingsServer(db)
		rec := httptest.NewRecorder()
		s.apiSaveSettings(rec, settingsRequest(t, s, auth.RoleAdmin,
			`{"siteName":"Acme","siteMeta":"`+strings.Repeat("x", maxSiteCodeLen+1)+`"}`))
		if rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("status %d, want 422", rec.Code)
		}
		if got := storedSettings(t, s).SiteMeta; got != "" {
			t.Errorf("a rejected save stored %q", got)
		}
	})
}

// quote renders s as a JSON string, so a test body can carry markup
// without hand-escaping every quote in it.
func quote(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
}
