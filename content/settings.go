package content

import "context"

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
