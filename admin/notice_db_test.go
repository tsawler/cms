package admin

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/tsawler/cms/auth"
	"github.com/tsawler/cms/internal/dbtest"
	"github.com/tsawler/cms/internal/sqldb"
)

// The notice bar is site furniture like the menu alignment: anyone who
// can open the settings dialog may switch it on, and its words go
// through the shared-region path like any other content. What needs
// guarding is the opposite of the mode's problem — not who may set it,
// but what an unrelated save does to it.

func storedNotice(t *testing.T, s *server) (on bool, style string, dismissible bool) {
	t.Helper()
	site, err := s.deps.Content.SiteSettings(context.Background())
	if err != nil {
		t.Fatalf("SiteSettings: %v", err)
	}
	return site.NoticeBar, site.NoticeStyle, site.NoticeDismissible
}

func TestAPISettingsNoticeBar(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := settingsServer(db)

		// An editor may switch the bar on: it is content furniture, not
		// a decision about how the site meets search engines.
		rec := httptest.NewRecorder()
		s.apiSaveSettings(rec, settingsRequest(t, s, auth.RoleEditor,
			`{"siteName":"Acme","noticeBar":true,"noticeStyle":"alert","noticeDismissible":true}`))
		if rec.Code != 200 {
			t.Fatalf("editor switching the bar on: got %d, want 200 (%s)", rec.Code, rec.Body)
		}
		on, style, dismiss := storedNotice(t, s)
		if !on || style != "alert" || !dismiss {
			t.Fatalf("stored notice = (%v, %q, %v), want (true, alert, true)", on, style, dismiss)
		}

		// The Site CSS & JS panel PUTs the settings it knows about and
		// nothing else. A save from there must leave the bar exactly as
		// it was: a missing boolean read as false would take a holiday
		// closure off every page of the site, silently, as a side effect
		// of saving a stylesheet.
		rec = httptest.NewRecorder()
		s.apiSaveSettings(rec, settingsRequest(t, s, auth.RoleAdmin,
			`{"siteName":"Acme","siteCss":"body{margin:0}"}`))
		if rec.Code != 200 {
			t.Fatalf("saving unrelated settings: got %d, want 200 (%s)", rec.Code, rec.Body)
		}
		on, style, dismiss = storedNotice(t, s)
		if !on || style != "alert" || !dismiss {
			t.Errorf("an unrelated save changed the bar: (%v, %q, %v), want (true, alert, true)",
				on, style, dismiss)
		}

		// Switching it off is an explicit false, not an omission.
		rec = httptest.NewRecorder()
		s.apiSaveSettings(rec, settingsRequest(t, s, auth.RoleEditor,
			`{"siteName":"Acme","noticeBar":false}`))
		if rec.Code != 200 {
			t.Fatalf("switching the bar off: got %d, want 200 (%s)", rec.Code, rec.Body)
		}
		if on, _, _ = storedNotice(t, s); on {
			t.Error("the bar is still on after an explicit false")
		}

		// A style nobody offers is refused rather than quietly stored,
		// so a broken client shows up as an error and not as a bar in
		// the wrong colour.
		rec = httptest.NewRecorder()
		s.apiSaveSettings(rec, settingsRequest(t, s, auth.RoleEditor,
			`{"siteName":"Acme","noticeStyle":"chartreuse"}`))
		if rec.Code != 422 {
			t.Errorf("unknown notice style: got %d, want 422", rec.Code)
		}
		if _, style, _ = storedNotice(t, s); style != "alert" {
			t.Errorf("a refused save changed the stored style to %q", style)
		}
	})
}
