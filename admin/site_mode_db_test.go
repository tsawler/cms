package admin

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alexedwards/scs/v2"

	"github.com/tsawler/cms/auth"
	"github.com/tsawler/cms/content"
	"github.com/tsawler/cms/internal/dbtest"
	"github.com/tsawler/cms/internal/sqldb"
)

// The site mode decides whether the public site may be indexed at all,
// so the settings API holds it to the superadmin the way it holds
// site-wide CSS and JS to admins: everyone else's save carries the
// stored value through rather than being refused, because the dialog
// sends the whole settings object every time.

func settingsServer(db *sqldb.DB) *server {
	return &server{deps: Deps{
		Sessions:      scs.New(),
		Users:         auth.NewStore(db),
		Content:       content.NewStore(db, "en"),
		Logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		DefaultLocale: "en",
		Locales:       []string{"en"},
	}}
}

// settingsRequest is a PUT /api/settings carrying the session state the
// middleware would have left for a logged-in user of the given role.
func settingsRequest(t *testing.T, s *server, role auth.Role, body string) *http.Request {
	t.Helper()
	r := httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")

	ctx := context.Background()
	email := string(role) + "-settings@example.com"
	u, err := s.deps.Users.GetByEmail(ctx, email)
	if errors.Is(err, auth.ErrNotFound) {
		u = &auth.User{Email: email, Name: string(role), PasswordHash: "x", Role: role, Active: true}
		_, err = s.deps.Users.Insert(ctx, u)
	}
	if err != nil {
		t.Fatalf("seeding the session %s: %v", role, err)
	}
	sctx, err := s.deps.Sessions.Load(r.Context(), "")
	if err != nil {
		t.Fatalf("loading a session: %v", err)
	}
	s.deps.Sessions.Put(sctx, sessionKeyUserID, u.ID)
	return r.WithContext(sctx)
}

// storedMode is the mode as the public site would read it.
func storedMode(t *testing.T, s *server) string {
	t.Helper()
	site, err := s.deps.Content.SiteSettings(context.Background())
	if err != nil {
		t.Fatalf("SiteSettings: %v", err)
	}
	return site.Mode
}

func TestAPISettingsModeIsSuperadminOnly(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := settingsServer(db)

		// A superadmin puts the site into development.
		rec := httptest.NewRecorder()
		s.apiSaveSettings(rec, settingsRequest(t, s, auth.RoleSuperadmin,
			`{"siteName":"Acme","mode":"development"}`))
		if rec.Code != http.StatusOK {
			t.Fatalf("superadmin save: status %d, want 200: %s", rec.Code, rec.Body)
		}
		if got := storedMode(t, s); got != content.ModeDevelopment {
			t.Fatalf("after the superadmin's save, mode = %q, want development", got)
		}

		// An admin and an editor both save the dialog with the mode set
		// to production. Neither may make the site findable, and both
		// must still be able to change what is theirs to change.
		for _, role := range []auth.Role{auth.RoleAdmin, auth.RoleEditor} {
			rec := httptest.NewRecorder()
			s.apiSaveSettings(rec, settingsRequest(t, s, role,
				`{"siteName":"Renamed by `+string(role)+`","mode":"production"}`))
			if rec.Code != http.StatusOK {
				t.Fatalf("%s save: status %d, want 200: %s", role, rec.Code, rec.Body)
			}
			if got := storedMode(t, s); got != content.ModeDevelopment {
				t.Errorf("%s switched the site to %q — the mode is superadmin-only", role, got)
			}
			site, err := s.deps.Content.SiteSettings(context.Background())
			if err != nil {
				t.Fatalf("SiteSettings: %v", err)
			}
			if want := "Renamed by " + string(role); site.SiteName != want {
				t.Errorf("%s: site name = %q, want %q — the rest of the dialog still saves",
					role, site.SiteName, want)
			}
		}

		// Back to production, by the one role that may.
		rec = httptest.NewRecorder()
		s.apiSaveSettings(rec, settingsRequest(t, s, auth.RoleSuperadmin,
			`{"siteName":"Acme","mode":"production"}`))
		if rec.Code != http.StatusOK {
			t.Fatalf("superadmin save: status %d, want 200: %s", rec.Code, rec.Body)
		}
		if got := storedMode(t, s); got != content.ModeProduction {
			t.Fatalf("after the switch to production, mode = %q", got)
		}

		// A mode nobody defined is a mistake, not a third state.
		rec = httptest.NewRecorder()
		s.apiSaveSettings(rec, settingsRequest(t, s, auth.RoleSuperadmin,
			`{"siteName":"Acme","mode":"staging"}`))
		if rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("unknown mode: status %d, want 422", rec.Code)
		}
		if got := storedMode(t, s); got != content.ModeProduction {
			t.Errorf("a rejected save changed the mode to %q", got)
		}
	})
}

// The dialog reads the mode back, and a site that has never saved one
// reads as production — the setting arrived after these sites did.
func TestAPIGetSettingsReportsProductionWhenUnset(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := settingsServer(db)
		rec := httptest.NewRecorder()
		s.apiGetSettings(rec, httptest.NewRequest(http.MethodGet, "/api/settings", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d, want 200: %s", rec.Code, rec.Body)
		}
		if got := decodeJSON(t, rec)["mode"]; got != content.ModeProduction {
			t.Errorf("mode = %v, want production", got)
		}
	})
}
