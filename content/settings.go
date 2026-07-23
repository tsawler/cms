package content

import "context"

// SiteSettings are the site-wide presentation settings the in-place
// editor's "Site settings" dialog manages. Zero values mean "not set" —
// templates fall back to their own defaults.
type SiteSettings struct {
	MenuAlign string // "left", "center", "right", or "" (host default)
	SiteName  string
	LogoURL   string // "" = no logo
}

// Keys the settings are stored under in cms_settings.
const (
	settingMenuAlign = "menu_align"
	settingSiteName  = "site_name"
	settingLogoURL   = "logo_url"
)

// SiteSettings returns the stored site settings. Keys never saved come
// back as zero values, so a fresh install reads as "all defaults".
func (s *Store) SiteSettings(ctx context.Context) (SiteSettings, error) {
	rows, err := s.db.Query(ctx, "SELECT key, value FROM cms_settings")
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
	for k, v := range map[string]string{
		settingMenuAlign: in.MenuAlign,
		settingSiteName:  in.SiteName,
		settingLogoURL:   in.LogoURL,
	} {
		if _, err := tx.Exec(ctx, `
			INSERT INTO cms_settings (key, value) VALUES ($1, $2)
			ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`, k, v); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
