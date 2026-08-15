package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tsawler/cms/auth"
	"github.com/tsawler/cms/content"
	"github.com/tsawler/cms/internal/dbtest"
	"github.com/tsawler/cms/internal/sqldb"
)

// The site's robots.txt is what the live site tells crawlers, so the
// settings API holds it to the superadmin exactly as it holds the mode.

// storedSettings is what the public site would read.
func storedSettings(t *testing.T, s *server) content.SiteSettings {
	t.Helper()
	site, err := s.deps.Content.SiteSettings(context.Background())
	if err != nil {
		t.Fatalf("SiteSettings: %v", err)
	}
	return site
}

func TestAPISettingsRobotsTxtIsSuperadminOnly(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := settingsServer(db)

		rec := httptest.NewRecorder()
		s.apiSaveSettings(rec, settingsRequest(t, s, auth.RoleSuperadmin,
			`{"siteName":"Acme","robotsTxt":"User-agent: *\nDisallow: /admin\n"}`))
		if rec.Code != http.StatusOK {
			t.Fatalf("superadmin save: status %d, want 200: %s", rec.Code, rec.Body)
		}
		const want = "User-agent: *\nDisallow: /admin\n"
		if got := storedSettings(t, s).RobotsTxt; got != want {
			t.Fatalf("after the superadmin's save, robots.txt = %q, want %q", got, want)
		}

		// Nobody else may rewrite it, and everyone may still save the
		// rest of the dialog.
		for _, role := range []auth.Role{auth.RoleAdmin, auth.RoleEditor} {
			rec := httptest.NewRecorder()
			s.apiSaveSettings(rec, settingsRequest(t, s, role,
				`{"siteName":"By `+string(role)+`","robotsTxt":"User-agent: *\nAllow: /\n"}`))
			if rec.Code != http.StatusOK {
				t.Fatalf("%s save: status %d, want 200: %s", role, rec.Code, rec.Body)
			}
			site := storedSettings(t, s)
			if site.RobotsTxt != want {
				t.Errorf("%s rewrote robots.txt to %q — it is superadmin-only", role, site.RobotsTxt)
			}
			if got := "By " + string(role); site.SiteName != got {
				t.Errorf("%s: site name = %q, want %q — the rest of the dialog still saves",
					role, site.SiteName, got)
			}
		}

		// Emptying it is a superadmin's call too, and hands /robots.txt
		// back to the host.
		rec = httptest.NewRecorder()
		s.apiSaveSettings(rec, settingsRequest(t, s, auth.RoleSuperadmin,
			`{"siteName":"Acme","robotsTxt":""}`))
		if rec.Code != http.StatusOK {
			t.Fatalf("superadmin clear: status %d, want 200: %s", rec.Code, rec.Body)
		}
		if got := storedSettings(t, s).RobotsTxt; got != "" {
			t.Errorf("after clearing, robots.txt = %q, want empty", got)
		}
	})
}

// A browser textarea submits CRLF; the file is served byte-for-byte, so
// the line endings are normalized on the way in.
func TestAPISettingsRobotsTxtNormalizesLineEndings(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := settingsServer(db)
		rec := httptest.NewRecorder()
		s.apiSaveSettings(rec, settingsRequest(t, s, auth.RoleSuperadmin,
			`{"siteName":"Acme","robotsTxt":"User-agent: *\r\nDisallow: /x\r\n"}`))
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d, want 200: %s", rec.Code, rec.Body)
		}
		if got := storedSettings(t, s).RobotsTxt; got != "User-agent: *\nDisallow: /x\n" {
			t.Errorf("robots.txt = %q, want LF line endings", got)
		}
	})
}

func TestAPISettingsRobotsTxtTooLong(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := settingsServer(db)
		rec := httptest.NewRecorder()
		s.apiSaveSettings(rec, settingsRequest(t, s, auth.RoleSuperadmin,
			`{"siteName":"Acme","robotsTxt":"`+strings.Repeat("x", maxRobotsLen+1)+`"}`))
		if rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("status %d, want 422", rec.Code)
		}
		if got := storedSettings(t, s).RobotsTxt; got != "" {
			t.Errorf("a rejected save stored %q", got)
		}
	})
}

// The Site CSS & JS panel PUTs the settings without the superadmin
// fields. An absent field carries the stored value through — a missing
// mode read as "" would mean production, quietly making a site that is
// still being built findable.
func TestAPISettingsAbsentSuperadminFieldsCarryThrough(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := settingsServer(db)

		rec := httptest.NewRecorder()
		s.apiSaveSettings(rec, settingsRequest(t, s, auth.RoleSuperadmin,
			`{"siteName":"Acme","mode":"development","robotsTxt":"Disallow: /\n"}`))
		if rec.Code != http.StatusOK {
			t.Fatalf("superadmin save: status %d, want 200: %s", rec.Code, rec.Body)
		}

		// The same superadmin saves site-wide CSS, sending neither field.
		rec = httptest.NewRecorder()
		s.apiSaveSettings(rec, settingsRequest(t, s, auth.RoleSuperadmin,
			`{"siteName":"Acme","siteCss":"body{color:red}"}`))
		if rec.Code != http.StatusOK {
			t.Fatalf("code-panel save: status %d, want 200: %s", rec.Code, rec.Body)
		}

		site := storedSettings(t, s)
		if site.Mode != content.ModeDevelopment {
			t.Errorf("a CSS save moved the site to mode %q — it sent no mode at all", site.Mode)
		}
		if site.RobotsTxt != "Disallow: /\n" {
			t.Errorf("a CSS save changed robots.txt to %q", site.RobotsTxt)
		}
		if site.SiteCSS != "body{color:red}" {
			t.Errorf("site CSS = %q, want the saved value", site.SiteCSS)
		}
	})
}

// The dialog reads the stored file back so a save can carry it through.
func TestAPIGetSettingsReturnsRobotsTxt(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := settingsServer(db)
		rec := httptest.NewRecorder()
		s.apiSaveSettings(rec, settingsRequest(t, s, auth.RoleSuperadmin,
			`{"siteName":"Acme","robotsTxt":"Disallow: /x\n"}`))
		if rec.Code != http.StatusOK {
			t.Fatalf("save: status %d, want 200: %s", rec.Code, rec.Body)
		}
		rec = httptest.NewRecorder()
		s.apiGetSettings(rec, httptest.NewRequest(http.MethodGet, "/api/settings", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d, want 200: %s", rec.Code, rec.Body)
		}
		if got := decodeJSON(t, rec)["robotsTxt"]; got != "Disallow: /x\n" {
			t.Errorf("robotsTxt = %v, want the stored file", got)
		}
	})
}
