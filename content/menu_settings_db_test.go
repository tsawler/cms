package content_test

import (
	"context"
	"testing"

	"github.com/tsawler/cms/internal/sqldb"

	"github.com/tsawler/cms/content"
	"github.com/tsawler/cms/internal/dbtest"
)

func TestMenuReplaceAndRead(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := content.NewStore(db, defaultLocale)
		about := seedPage(t, s, content.Page{Slug: "about"}, defaultLocale)
		if err := s.Publish(ctx, about.ID); err != nil {
			t.Fatalf("Publish: %v", err)
		}

		items := []content.MenuItemInput{
			{Label: "Home", PageID: nil, URL: "/"},
			{
				Label:  "Company",
				Labels: map[string]string{"fr": "Société"},
				Children: []content.MenuItemInput{
					{Label: "About", PageID: &about.ID},
					{Label: "External", URL: "https://example.com", NewTab: true},
				},
			},
		}
		if err := s.ReplaceMenu(ctx, "main", items); err != nil {
			t.Fatalf("ReplaceMenu: %v", err)
		}

		got, err := s.MenuItems(ctx, "main")
		if err != nil {
			t.Fatalf("MenuItems: %v", err)
		}
		if len(got) != 4 {
			t.Fatalf("got %d menu items, want 4 (2 top level + 2 children)", len(got))
		}
		// Ordering is by sort, with children interleaved after their parent.
		wantLabels := []string{"Home", "Company", "About", "External"}
		for i, want := range wantLabels {
			if got[i].Label != want {
				t.Fatalf("item %d label = %q, want %q (order = %v)", i, got[i].Label, want, wantLabels)
			}
		}

		company := got[1]
		// The JSON labels column has to survive the round trip.
		if company.LabelFor("fr") != "Société" {
			t.Errorf("LabelFor(fr) = %q, want %q", company.LabelFor("fr"), "Société")
		}
		// An unknown locale falls back to the default-locale label.
		if company.LabelFor("de") != "Company" {
			t.Errorf("LabelFor(de) = %q, want the fallback %q", company.LabelFor("de"), "Company")
		}
		// A nil Labels map is documented to store as {}, not NULL.
		if got[0].Labels == nil {
			t.Error("Home labels = nil, want an empty map")
		}

		aboutItem := got[2]
		if aboutItem.ParentID == nil || *aboutItem.ParentID != company.ID {
			t.Errorf("About parent = %v, want Company's id %d", aboutItem.ParentID, company.ID)
		}
		// The linked page's slug and status are joined in for URL resolution.
		if aboutItem.PageSlug == nil || *aboutItem.PageSlug != "about" {
			t.Errorf("About page slug = %v, want %q", aboutItem.PageSlug, "about")
		}
		if aboutItem.PageStatus == nil || *aboutItem.PageStatus != content.StatusPublished {
			t.Errorf("About page status = %v, want published", aboutItem.PageStatus)
		}
		// A literal-URL item has no page joined.
		external := got[3]
		if external.PageSlug != nil {
			t.Errorf("External page slug = %v, want nil", external.PageSlug)
		}
		if !external.NewTab {
			t.Error("External new_tab = false, want true")
		}

		// Replacing is wholesale.
		if err := s.ReplaceMenu(ctx, "main", []content.MenuItemInput{{Label: "Only", URL: "/"}}); err != nil {
			t.Fatalf("ReplaceMenu(second): %v", err)
		}
		got, err = s.MenuItems(ctx, "main")
		if err != nil {
			t.Fatalf("MenuItems: %v", err)
		}
		if len(got) != 1 || got[0].Label != "Only" {
			t.Errorf("after replace got %d items, want just \"Only\"", len(got))
		}
	})
}

func TestMenuItemsScopedByMenu(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := content.NewStore(db, defaultLocale)

		if err := s.ReplaceMenu(ctx, "main", []content.MenuItemInput{{Label: "Main Item", URL: "/"}}); err != nil {
			t.Fatalf("ReplaceMenu(main): %v", err)
		}
		if err := s.ReplaceMenu(ctx, "footer", []content.MenuItemInput{{Label: "Footer Item", URL: "/legal"}}); err != nil {
			t.Fatalf("ReplaceMenu(footer): %v", err)
		}

		main, err := s.MenuItems(ctx, "main")
		if err != nil {
			t.Fatalf("MenuItems(main): %v", err)
		}
		if len(main) != 1 || main[0].Label != "Main Item" {
			t.Errorf("MenuItems(main) = %+v, want just the main item", main)
		}

		// Replacing one menu must not disturb the other.
		footer, err := s.MenuItems(ctx, "footer")
		if err != nil {
			t.Fatalf("MenuItems(footer): %v", err)
		}
		if len(footer) != 1 || footer[0].Label != "Footer Item" {
			t.Errorf("MenuItems(footer) = %+v, want just the footer item", footer)
		}

		// An empty menu name returns every menu's items.
		all, err := s.MenuItems(ctx, "")
		if err != nil {
			t.Fatalf("MenuItems(all): %v", err)
		}
		if len(all) != 2 {
			t.Errorf("MenuItems(\"\") returned %d items, want 2", len(all))
		}
	})
}

func TestSiteSettingsRoundTrip(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := content.NewStore(db, defaultLocale)

		// A fresh install reads as all defaults rather than erroring.
		got, err := s.SiteSettings(ctx)
		if err != nil {
			t.Fatalf("SiteSettings(empty): %v", err)
		}
		if got != (content.SiteSettings{}) {
			t.Errorf("SiteSettings(empty) = %+v, want the zero value", got)
		}

		want := content.SiteSettings{
			MenuAlign:  "center",
			SiteName:   "Example Co",
			LogoURL:    "/img/logo.svg",
			FaviconURL: "/cms/media/abc123/original.png",
			LoginInNav: true,
			SiteCSS:    "body{margin:0}",
			SiteJS:     "console.log('hi')",
			// The notice bar's switch and look. Its words are not here:
			// they are a shared region, stored with the rest of the
			// site's content.
			NoticeBar:         true,
			NoticeStyle:       "warning",
			NoticeDismissible: true,
			// The in-place editor's own chrome, which is a property of
			// the site the way its design is: dark tools vanish into a
			// dark page.
			EditorTheme: content.EditorThemeLight,
		}
		if err := s.SaveSiteSettings(ctx, want); err != nil {
			t.Fatalf("SaveSiteSettings: %v", err)
		}
		got, err = s.SiteSettings(ctx)
		if err != nil {
			t.Fatalf("SiteSettings: %v", err)
		}
		if got != want {
			t.Errorf("SiteSettings = %+v, want %+v", got, want)
		}

		// Saving again must update in place, not accumulate rows — this is
		// the key/value upsert path.
		want.SiteName = "Renamed Co"
		want.LoginInNav = false
		want.NoticeBar = false
		if err := s.SaveSiteSettings(ctx, want); err != nil {
			t.Fatalf("SaveSiteSettings(second): %v", err)
		}
		got, err = s.SiteSettings(ctx)
		if err != nil {
			t.Fatalf("SiteSettings: %v", err)
		}
		if got != want {
			t.Errorf("SiteSettings after re-save = %+v, want %+v", got, want)
		}
	})
}
