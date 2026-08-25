// Environment-driven configuration. ConfigFromEnv fills in every Config
// field that has a documented environment variable, so hosts configured
// by environment don't re-type the same parsing boilerplate.

package cms

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// ConfigFromEnv returns a Config populated from the environment:
//
//   - CMS_SITE_URL → SiteURL, the site's canonical public address
//     ("https://example.com"). Leave it unset in development, where each
//     request's own host is right.
//   - S3_ENDPOINT, S3_REGION, S3_BUCKET, S3_ACCESS_KEY, S3_SECRET,
//     S3_KEY_PREFIX, S3_APPLY_PUBLIC_POLICY (=1) → S3. Setting
//     S3_ENDPOINT enables the media library.
//   - CAP_URL, CAP_INTERNAL_URL, CAP_SITE_KEY, CAP_SECRET,
//     CAP_WIDGET (=visible) → Captcha. Setting CAP_URL enables the
//     login CAPTCHA.
//   - CMS_SESSION_REDIS_ADDR, CMS_SESSION_REDIS_PASSWORD,
//     CMS_SESSION_REDIS_DB → Redis. Setting CMS_SESSION_REDIS_ADDR moves
//     session storage from the cms_sessions table to Redis.
//   - CMS_REMEMBER_DAYS → RememberFor, in days.
//   - CMS_POSTS_PER_PAGE → PostsPerPage, how many posts a paginated blog
//     or news listing shows on one page.
//   - CMS_ADMIN_PER_PAGE → AdminPerPage, how many rows a paginated admin
//     list shows on one page. Separate from CMS_POSTS_PER_PAGE, which
//     sizes the public listing.
//   - CMS_SITE_LOCKED (true/false) → LockOverride, forcing the site lock
//     on or off whatever the admin has stored. Unset leaves the stored
//     switch alone; CMS_SITE_LOCKED=false is the way to reopen a site
//     without a working admin to click through.
//   - CMS_MEDIA_WEBP_QUALITY → MediaWebPQuality.
//   - CMS_MEDIA_MAX_VIDEO_MB → MediaMaxVideoMB.
//   - CMS_MEDIA_ADOPT (when-empty | off | reconcile) → MediaAdopt, which
//     decides whether a bucket that already holds media is adopted into an
//     empty database. Setting S3_KEY_PREFIX is what makes that safe on a
//     bucket shared with other sites.
//   - CMS_TAILWIND_COMMAND (a space-separated argv with {content} and
//     {output} placeholders), CMS_TAILWIND_DIR → Tailwind.
//
// An unset variable leaves its field zero, so New applies the usual
// defaults; a set-but-malformed value is a configuration mistake and
// returns an error. Everything without an environment variable — DB,
// TemplateFS, PageTemplates, AdminSections, ... — is left for the host
// to fill in:
//
//	cfg, err := cms.ConfigFromEnv()
//	if err != nil { ... }
//	cfg.DB = pool
//	cfg.TemplateFS = templateFS
//	c, err := cms.New(cfg)
func ConfigFromEnv() (Config, error) {
	var cfg Config

	// The site's public address, for links that have to work off the page.
	// Leave it unset in development, where the request's host is right.
	cfg.SiteURL = os.Getenv("CMS_SITE_URL")

	if endpoint := os.Getenv("S3_ENDPOINT"); endpoint != "" {
		cfg.S3 = &S3Config{
			Endpoint:              endpoint,
			Region:                os.Getenv("S3_REGION"),
			Bucket:                os.Getenv("S3_BUCKET"),
			AccessKey:             os.Getenv("S3_ACCESS_KEY"),
			Secret:                os.Getenv("S3_SECRET"),
			KeyPrefix:             os.Getenv("S3_KEY_PREFIX"),
			ApplyPublicReadPolicy: os.Getenv("S3_APPLY_PUBLIC_POLICY") == "1",
		}
	}

	if url := os.Getenv("CAP_URL"); url != "" {
		cfg.Captcha = &CaptchaConfig{
			URL:         url,
			InternalURL: os.Getenv("CAP_INTERNAL_URL"),
			SiteKey:     os.Getenv("CAP_SITE_KEY"),
			Secret:      os.Getenv("CAP_SECRET"),
			Visible:     os.Getenv("CAP_WIDGET") == "visible",
		}
	}

	if addr := os.Getenv("CMS_SESSION_REDIS_ADDR"); addr != "" {
		cfg.Redis = &RedisConfig{
			Addr:     addr,
			Password: os.Getenv("CMS_SESSION_REDIS_PASSWORD"),
		}
		if v := os.Getenv("CMS_SESSION_REDIS_DB"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				return Config{}, fmt.Errorf("cms: CMS_SESSION_REDIS_DB %q is not a database number", v)
			}
			cfg.Redis.DB = n
		}
	}

	if v := os.Getenv("CMS_REMEMBER_DAYS"); v != "" {
		d, err := strconv.Atoi(v)
		if err != nil || d <= 0 {
			return Config{}, fmt.Errorf("cms: CMS_REMEMBER_DAYS %q is not a positive number of days", v)
		}
		cfg.RememberFor = time.Duration(d) * 24 * time.Hour
	}

	// The site lock, forced from configuration. Unset is the normal
	// case and leaves the switch to the admin; set, it wins over what is
	// stored, in both directions — which is what makes CMS_SITE_LOCKED=0
	// the way back into a site somebody locked and then lost the
	// password for.
	if v := os.Getenv("CMS_SITE_LOCKED"); v != "" {
		locked, err := strconv.ParseBool(v)
		if err != nil {
			return Config{}, fmt.Errorf("cms: CMS_SITE_LOCKED %q is not a true/false value", v)
		}
		cfg.LockOverride = &locked
	}

	if v := os.Getenv("CMS_POSTS_PER_PAGE"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return Config{}, fmt.Errorf("cms: CMS_POSTS_PER_PAGE %q is not a positive number of posts", v)
		}
		cfg.PostsPerPage = n
	}

	if v := os.Getenv("CMS_ADMIN_PER_PAGE"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return Config{}, fmt.Errorf("cms: CMS_ADMIN_PER_PAGE %q is not a positive number of rows", v)
		}
		cfg.AdminPerPage = n
	}

	if v := os.Getenv("CMS_MEDIA_WEBP_QUALITY"); v != "" {
		q, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return Config{}, fmt.Errorf("cms: CMS_MEDIA_WEBP_QUALITY %q is not a number: %w", v, err)
		}
		cfg.MediaWebPQuality = q
	}

	if v := os.Getenv("CMS_MEDIA_MAX_VIDEO_MB"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return Config{}, fmt.Errorf("cms: CMS_MEDIA_MAX_VIDEO_MB %q is not a number: %w", v, err)
		}
		cfg.MediaMaxVideoMB = n
	}

	if v := os.Getenv("CMS_MEDIA_ADOPT"); v != "" {
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "when-empty":
			cfg.MediaAdopt = MediaAdoptWhenEmpty
		case "off":
			cfg.MediaAdopt = MediaAdoptOff
		case "reconcile":
			cfg.MediaAdopt = MediaAdoptReconcile
		default:
			return Config{}, fmt.Errorf("cms: CMS_MEDIA_ADOPT %q is not one of when-empty, off, reconcile", v)
		}
	}

	if cmd := os.Getenv("CMS_TAILWIND_COMMAND"); cmd != "" {
		cfg.Tailwind = &TailwindConfig{
			Command: strings.Fields(cmd),
			Dir:     os.Getenv("CMS_TAILWIND_DIR"),
		}
	}

	return cfg, nil
}
