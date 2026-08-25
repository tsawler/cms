package content

import (
	"context"
	"fmt"
)

// SiteSettings are the site-wide presentation settings the in-place
// editor's "Site settings" dialog manages. Zero values mean "not set" —
// templates fall back to their own defaults.
type SiteSettings struct {
	MenuAlign string // "left", "center", "right", or "" (host default)
	SiteName  string
	LogoURL   string // "" = no logo
	// FaviconURL is the site's browser-tab icon, emitted by cmsHead as
	// <link rel="icon">. "" leaves the host template's own icon (or the
	// browser's /favicon.ico guess) alone.
	FaviconURL string
	// LoginInNav adds a "Log in" link to {{cmsNav}} for visitors who
	// aren't logged in, pointing at the admin login page.
	LoginInNav bool
	// SiteCSS and SiteJS are injected raw into every public page (via
	// cmsHead/cmsScripts). Each holds plain code or full markup — <style>,
	// <link>, and <script> tags pass through as-is. Editing them is
	// admin-only, like per-page code.
	SiteCSS string
	SiteJS  string
	// SiteMeta is written raw into the <head> of every public page,
	// ahead of everything else the CMS emits: the verification tags a
	// search console or an analytics service asks to be pasted in, and
	// any other site-wide <meta> or <link> the host template does not
	// already carry. Admin-only for the same reason SiteCSS is — it is
	// markup nobody sanitizes.
	SiteMeta string
	// Mode is ModeDevelopment or ModeProduction (or "", read as
	// production). Development asks search engines to leave the site
	// alone; see Development. Changing it is superadmin-only.
	Mode string
	// RobotsTxt is the site's own /robots.txt, served verbatim once the
	// site is in production. "" — the default — leaves the path to the
	// host, which is what a site that predates this setting keeps
	// getting. Development ignores it and serves its own Disallow; see
	// Development. Editing it is superadmin-only, like Mode.
	RobotsTxt string
	// Sitemap makes the CMS serve a sitemap of every published, public
	// page at /sitemap.xml. Off — the default — leaves that address to
	// the host app, so an upgrade never shadows a sitemap it already
	// serves; SeedAdmin turns it on for brand-new sites. A site in
	// development serves none regardless: it has nothing it wants found.
	// Switching it is superadmin-only, like Mode.
	Sitemap bool
	// NoticeBar shows the site-wide notice bar — a thin strip above
	// everything else on every page, for the message the whole site has
	// to carry at once: a holiday closure, a delivery delay, a service
	// interruption. Its words are not here: they live in the shared
	// region render.NoticeRegion, so they translate, sanitize, and
	// publish exactly like a footer does. These three settings are the
	// bar itself.
	NoticeBar bool
	// NoticeStyle names the bar's colour scheme, one of the curated keys
	// in render.NoticeStyles. "" is the first of them.
	NoticeStyle string
	// NoticeDismissible gives the bar a close button, and remembers the
	// dismissal in the visitor's browser until the notice's words
	// change. Off, the bar stays until it is switched off here.
	NoticeDismissible bool
	// EditorTheme is the colour scheme of the in-place editor's own
	// chrome — the edit bar, the tool rail, the floating block and
	// section toolbars, and TinyMCE's formatting toolbar. "" and
	// EditorThemeDark are the dark chrome the editor has always worn;
	// EditorThemeLight swaps it for a pale one, which is what a site
	// with a dark design of its own needs: dark chrome on a dark page
	// stops reading as chrome at all.
	EditorTheme string
}

// The two site modes. A site under construction sits in development,
// where the CMS asks search engines to keep it out of their indexes; the
// switch to production is what makes it findable.
//
// The empty value reads as production, so a site that predates this
// setting — or one whose settings were never saved — keeps behaving
// exactly as it did. New installs are seeded into development instead,
// which is the safe end to start from.
const (
	ModeProduction  = "production"
	ModeDevelopment = "development"
)

// Development reports whether the site is in development mode, and so
// should be kept out of search results.
//
// This is a request to well-behaved crawlers, not access control: the
// site is still served to anyone who asks for it. Keeping an unfinished
// site genuinely private is the host's job — HTTP auth, an IP allowlist,
// or simply not pointing a public name at it.
func (s SiteSettings) Development() bool { return s.Mode == ModeDevelopment }

// ValidMode reports whether m is a mode that may be stored: one of the
// two named modes, or "" for the production default.
func ValidMode(m string) bool {
	return m == "" || m == ModeProduction || m == ModeDevelopment
}

// The in-place editor's two colour schemes. The empty value reads as
// dark, so a site that predates this setting keeps the chrome it has
// always had.
const (
	EditorThemeDark  = "dark"
	EditorThemeLight = "light"
)

// ValidEditorTheme reports whether t is a theme that may be stored: one
// of the two named schemes, or "" for the dark default.
func ValidEditorTheme(t string) bool {
	return t == "" || t == EditorThemeDark || t == EditorThemeLight
}

// Keys the settings are stored under in cms_settings.
const (
	settingMenuAlign  = "menu_align"
	settingSiteName   = "site_name"
	settingLogoURL    = "logo_url"
	settingFaviconURL = "favicon_url"
	settingLoginInNav = "login_in_nav"
	settingSiteCSS    = "site_css"
	settingSiteJS     = "site_js"
	settingSiteMeta   = "site_meta"
	settingSiteMode   = "site_mode"
	settingRobotsTxt  = "robots_txt"
	settingSitemap    = "sitemap"

	settingNoticeBar         = "notice_bar"
	settingNoticeStyle       = "notice_style"
	settingNoticeDismissible = "notice_dismissible"

	settingEditorTheme = "editor_theme"
)

// SiteSettings returns the stored site settings. Keys never saved come
// back as zero values, so a fresh install reads as "all defaults".
func (s *Store) SiteSettings(ctx context.Context) (SiteSettings, error) {
	// "key" is a reserved word in MySQL, so the identifier needs quoting —
	// and the two engines quote differently.
	keyCol := s.db.Dialect().Quote("key")
	rows, err := s.db.Query(ctx, "SELECT "+keyCol+", value FROM cms_settings")
	if err != nil {
		return SiteSettings{}, err
	}
	defer rows.Close()
	var out SiteSettings
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return SiteSettings{}, err
		}
		switch k {
		case settingMenuAlign:
			out.MenuAlign = v
		case settingSiteName:
			out.SiteName = v
		case settingLogoURL:
			out.LogoURL = v
		case settingFaviconURL:
			out.FaviconURL = v
		case settingLoginInNav:
			out.LoginInNav = v == "1"
		case settingSiteCSS:
			out.SiteCSS = v
		case settingSiteJS:
			out.SiteJS = v
		case settingSiteMeta:
			out.SiteMeta = v
		case settingSiteMode:
			out.Mode = v
		case settingRobotsTxt:
			out.RobotsTxt = v
		case settingSitemap:
			out.Sitemap = v == "1"
		case settingNoticeBar:
			out.NoticeBar = v == "1"
		case settingNoticeStyle:
			out.NoticeStyle = v
		case settingNoticeDismissible:
			out.NoticeDismissible = v == "1"
		case settingEditorTheme:
			out.EditorTheme = v
		}
	}
	return out, rows.Err()
}

// SaveSiteSettings stores the settings, atomically. Like menus they have
// no draft state — a save is live on commit.
func (s *Store) SaveSiteSettings(ctx context.Context, in SiteSettings) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	loginInNav, sitemap := "", ""
	if in.LoginInNav {
		loginInNav = "1"
	}
	if in.Sitemap {
		sitemap = "1"
	}
	noticeBar, noticeDismissible := "", ""
	if in.NoticeBar {
		noticeBar = "1"
	}
	if in.NoticeDismissible {
		noticeDismissible = "1"
	}
	for k, v := range map[string]string{
		settingMenuAlign:  in.MenuAlign,
		settingSiteName:   in.SiteName,
		settingLogoURL:    in.LogoURL,
		settingFaviconURL: in.FaviconURL,
		settingLoginInNav: loginInNav,
		settingSiteCSS:    in.SiteCSS,
		settingSiteJS:     in.SiteJS,
		settingSiteMeta:   in.SiteMeta,
		settingSiteMode:   in.Mode,
		settingRobotsTxt:  in.RobotsTxt,
		settingSitemap:    sitemap,

		settingNoticeBar:         noticeBar,
		settingNoticeStyle:       in.NoticeStyle,
		settingNoticeDismissible: noticeDismissible,

		settingEditorTheme: in.EditorTheme,
	} {
		keyCol := tx.Dialect().Quote("key")
		if _, err := tx.Exec(ctx, `
			INSERT INTO cms_settings (`+keyCol+`, value) VALUES ($1, $2)
			ON CONFLICT (`+keyCol+`) DO UPDATE SET value = EXCLUDED.value`, k, v); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// SetSiteMode stores the site mode on its own, leaving every other
// setting alone — what a fresh install's seeding wants, where writing a
// whole SiteSettings would mean inventing values for keys nobody has set
// yet.
func (s *Store) SetSiteMode(ctx context.Context, mode string) error {
	if !ValidMode(mode) {
		return fmt.Errorf("content: %q is not a site mode", mode)
	}
	return s.setSetting(ctx, settingSiteMode, mode)
}

// SetSitemap turns the generated sitemap on or off on its own, for the
// same reason SetSiteMode exists: seeding a new site sets this one key
// and has no opinion about the others.
func (s *Store) SetSitemap(ctx context.Context, on bool) error {
	v := ""
	if on {
		v = "1"
	}
	return s.setSetting(ctx, settingSitemap, v)
}

// setSetting upserts one key, leaving every other setting alone.
func (s *Store) setSetting(ctx context.Context, key, value string) error {
	keyCol := s.db.Dialect().Quote("key")
	_, err := s.db.Exec(ctx, `
		INSERT INTO cms_settings (`+keyCol+`, value) VALUES ($1, $2)
		ON CONFLICT (`+keyCol+`) DO UPDATE SET value = EXCLUDED.value`, key, value)
	return err
}
