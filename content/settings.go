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

// Keys the settings are stored under in cms_settings.
const (
	settingMenuAlign  = "menu_align"
	settingSiteName   = "site_name"
	settingLogoURL    = "logo_url"
	settingFaviconURL = "favicon_url"
	settingLoginInNav = "login_in_nav"
	settingSiteCSS    = "site_css"
	settingSiteJS     = "site_js"
	settingSiteMode   = "site_mode"
	settingRobotsTxt  = "robots_txt"
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
		case settingSiteMode:
			out.Mode = v
		case settingRobotsTxt:
			out.RobotsTxt = v
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
	loginInNav := ""
	if in.LoginInNav {
		loginInNav = "1"
	}
	for k, v := range map[string]string{
		settingMenuAlign:  in.MenuAlign,
		settingSiteName:   in.SiteName,
		settingLogoURL:    in.LogoURL,
		settingFaviconURL: in.FaviconURL,
		settingLoginInNav: loginInNav,
		settingSiteCSS:    in.SiteCSS,
		settingSiteJS:     in.SiteJS,
		settingSiteMode:   in.Mode,
		settingRobotsTxt:  in.RobotsTxt,
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
	keyCol := s.db.Dialect().Quote("key")
	_, err := s.db.Exec(ctx, `
		INSERT INTO cms_settings (`+keyCol+`, value) VALUES ($1, $2)
		ON CONFLICT (`+keyCol+`) DO UPDATE SET value = EXCLUDED.value`, settingSiteMode, mode)
	return err
}
