package cms

import (
	tls13 "crypto/tls"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// every variable ConfigFromEnv reads, for blanking between subtests so a
// developer's real environment can't leak in.
var envVars = []string{
	"S3_ENDPOINT", "S3_REGION", "S3_BUCKET", "S3_ACCESS_KEY", "S3_SECRET",
	"S3_KEY_PREFIX", "S3_APPLY_PUBLIC_POLICY",
	"CAP_URL", "CAP_INTERNAL_URL", "CAP_SITE_KEY", "CAP_SECRET", "CAP_WIDGET",
	"CMS_SESSION_REDIS_ADDR", "CMS_SESSION_REDIS_PASSWORD", "CMS_SESSION_REDIS_DB",
	"CMS_REMEMBER_DAYS", "CMS_POSTS_PER_PAGE", "CMS_ADMIN_PER_PAGE",
	"CMS_PAGE_VERSIONS_KEPT",
	"CMS_MEDIA_WEBP_QUALITY", "CMS_MEDIA_MAX_VIDEO_MB",
	"CMS_MEDIA_ADOPT", "CMS_TAILWIND_COMMAND", "CMS_TAILWIND_DIR",
	"CMS_SITE_URL", "CMS_SECURE_COOKIES", "CMS_CLIENT_IP_HEADER",
	"S3_PUBLIC_READ", "S3_PUBLIC_BASE_URL", "S3_USE_PATH_STYLE",
}

func clearEnv(t *testing.T) {
	t.Helper()
	for _, k := range envVars {
		t.Setenv(k, "")
	}
}

func TestConfigFromEnvEmpty(t *testing.T) {
	clearEnv(t)
	cfg, err := ConfigFromEnv()
	if err != nil {
		t.Fatalf("empty environment: %v", err)
	}
	if cfg.S3 != nil || cfg.Captcha != nil || cfg.Tailwind != nil || cfg.Redis != nil {
		t.Errorf("empty environment enabled features: S3=%v Captcha=%v Tailwind=%v Redis=%v",
			cfg.S3, cfg.Captcha, cfg.Tailwind, cfg.Redis)
	}
	if cfg.RememberFor != 0 || cfg.MediaWebPQuality != 0 || cfg.MediaMaxVideoMB != 0 {
		t.Errorf("empty environment set values: %+v", cfg)
	}
	// Adoption defaults on: a fresh deployment pointed at a bucket that
	// already holds media should pick it up without being told to.
	if cfg.MediaAdopt != MediaAdoptWhenEmpty {
		t.Errorf("MediaAdopt = %v, want the when-empty default", cfg.MediaAdopt)
	}
}

func TestConfigFromEnvMediaAdopt(t *testing.T) {
	for value, want := range map[string]MediaAdoptMode{
		"when-empty": MediaAdoptWhenEmpty,
		"off":        MediaAdoptOff,
		"reconcile":  MediaAdoptReconcile,
		"Reconcile":  MediaAdoptReconcile, // case-insensitive
		" off ":      MediaAdoptOff,       // and trimmed
	} {
		clearEnv(t)
		t.Setenv("CMS_MEDIA_ADOPT", value)
		cfg, err := ConfigFromEnv()
		if err != nil {
			t.Errorf("CMS_MEDIA_ADOPT=%q: %v", value, err)
			continue
		}
		if cfg.MediaAdopt != want {
			t.Errorf("CMS_MEDIA_ADOPT=%q gave %v, want %v", value, cfg.MediaAdopt, want)
		}
	}
}

func TestConfigFromEnvFull(t *testing.T) {
	clearEnv(t)
	for k, v := range map[string]string{
		"S3_ENDPOINT":                "s3.example.com",
		"S3_REGION":                  "us-east-1",
		"S3_BUCKET":                  "my-site",
		"S3_ACCESS_KEY":              "key",
		"S3_SECRET":                  "secret",
		"S3_KEY_PREFIX":              "prod",
		"S3_PUBLIC_READ":             "true",
		"S3_PUBLIC_BASE_URL":         "https://cdn.example.com/",
		"S3_USE_PATH_STYLE":          "1",
		"S3_APPLY_PUBLIC_POLICY":     "1",
		"CAP_URL":                    "http://localhost:3300",
		"CAP_INTERNAL_URL":           "http://cap:3000",
		"CAP_SITE_KEY":               "site",
		"CAP_SECRET":                 "sk-x",
		"CAP_WIDGET":                 "visible",
		"CMS_SESSION_REDIS_ADDR":     "localhost:6379",
		"CMS_SESSION_REDIS_PASSWORD": "hunter2",
		"CMS_SESSION_REDIS_DB":       "2",
		"CMS_REMEMBER_DAYS":          "14",
		"CMS_POSTS_PER_PAGE":         "6",
		"CMS_ADMIN_PER_PAGE":         "40",
		"CMS_PAGE_VERSIONS_KEPT":     "12",
		"CMS_MEDIA_WEBP_QUALITY":     "0.5",
		"CMS_MEDIA_MAX_VIDEO_MB":     "128",
		"CMS_TAILWIND_COMMAND":       "./tw.sh {content} {output}",
		"CMS_TAILWIND_DIR":           "assets",
	} {
		t.Setenv(k, v)
	}

	cfg, err := ConfigFromEnv()
	if err != nil {
		t.Fatal(err)
	}

	if cfg.S3 == nil {
		t.Fatal("S3 not configured")
	}
	if cfg.S3.Endpoint != "s3.example.com" || cfg.S3.Region != "us-east-1" ||
		cfg.S3.Bucket != "my-site" || cfg.S3.AccessKey != "key" ||
		cfg.S3.Secret != "secret" || cfg.S3.KeyPrefix != "prod" ||
		!cfg.S3.ApplyPublicReadPolicy || !cfg.S3.PublicRead || !cfg.S3.UsePathStyle ||
		cfg.S3.PublicBaseURL != "https://cdn.example.com" {
		t.Errorf("S3 = %+v", cfg.S3)
	}
	if cfg.Captcha == nil {
		t.Fatal("Captcha not configured")
	}
	if cfg.Captcha.URL != "http://localhost:3300" || cfg.Captcha.InternalURL != "http://cap:3000" ||
		cfg.Captcha.SiteKey != "site" || cfg.Captcha.Secret != "sk-x" || !cfg.Captcha.Visible {
		t.Errorf("Captcha = %+v", cfg.Captcha)
	}
	if cfg.Redis == nil {
		t.Fatal("Redis not configured")
	}
	if cfg.Redis.Addr != "localhost:6379" || cfg.Redis.Password != "hunter2" || cfg.Redis.DB != 2 {
		t.Errorf("Redis = %+v", cfg.Redis)
	}
	if cfg.RememberFor != 14*24*time.Hour {
		t.Errorf("RememberFor = %v, want 336h", cfg.RememberFor)
	}
	if cfg.PostsPerPage != 6 {
		t.Errorf("PostsPerPage = %v, want 6", cfg.PostsPerPage)
	}
	if cfg.AdminPerPage != 40 {
		t.Errorf("AdminPerPage = %v, want 40", cfg.AdminPerPage)
	}
	if cfg.PageVersionsKept != 12 {
		t.Errorf("PageVersionsKept = %v, want 12", cfg.PageVersionsKept)
	}
	if cfg.MediaWebPQuality != 0.5 {
		t.Errorf("MediaWebPQuality = %v, want 0.5", cfg.MediaWebPQuality)
	}
	if cfg.MediaMaxVideoMB != 128 {
		t.Errorf("MediaMaxVideoMB = %v, want 128", cfg.MediaMaxVideoMB)
	}
	if cfg.Tailwind == nil {
		t.Fatal("Tailwind not configured")
	}
	if strings.Join(cfg.Tailwind.Command, " ") != "./tw.sh {content} {output}" ||
		cfg.Tailwind.Dir != "assets" {
		t.Errorf("Tailwind = %+v", cfg.Tailwind)
	}
}

// A set-but-malformed value is a config mistake and must error, never be
// silently ignored.
func TestConfigFromEnvMalformed(t *testing.T) {
	for envVar, bad := range map[string]string{
		"CMS_REMEMBER_DAYS":      "soon",
		"CMS_POSTS_PER_PAGE":     "lots",
		"CMS_ADMIN_PER_PAGE":     "many",
		"CMS_PAGE_VERSIONS_KEPT": "all of them",
		"CMS_MEDIA_WEBP_QUALITY": "high",
		"CMS_MEDIA_MAX_VIDEO_MB": "big",
		"CMS_MEDIA_ADOPT":        "sometimes",
	} {
		clearEnv(t)
		t.Setenv(envVar, bad)
		if _, err := ConfigFromEnv(); err == nil {
			t.Errorf("%s=%q: expected an error, got nil", envVar, bad)
		}
	}

	// Zero and negative "remember me" days are as wrong as non-numeric,
	// and so is a page that holds no posts.
	for _, k := range []string{"CMS_REMEMBER_DAYS", "CMS_POSTS_PER_PAGE", "CMS_ADMIN_PER_PAGE",
		"CMS_PAGE_VERSIONS_KEPT"} {
		for _, bad := range []string{"0", "-3"} {
			clearEnv(t)
			t.Setenv(k, bad)
			if _, err := ConfigFromEnv(); err == nil {
				t.Errorf("%s=%q: expected an error, got nil", k, bad)
			}
		}
	}

	// The Redis database number is only read once an address enables Redis
	// sessions at all, matching how the S3_ vars ride on S3_ENDPOINT.
	for _, bad := range []string{"primary", "-1"} {
		clearEnv(t)
		t.Setenv("CMS_SESSION_REDIS_ADDR", "localhost:6379")
		t.Setenv("CMS_SESSION_REDIS_DB", bad)
		if _, err := ConfigFromEnv(); err == nil {
			t.Errorf("CMS_SESSION_REDIS_DB=%q: expected an error, got nil", bad)
		}
	}
}

func TestConfigFromEnvSiteURL(t *testing.T) {
	clearEnv(t)
	t.Setenv("CMS_SITE_URL", "https://example.com")
	cfg, err := ConfigFromEnv()
	if err != nil {
		t.Fatalf("ConfigFromEnv: %v", err)
	}
	if cfg.SiteURL != "https://example.com" {
		t.Errorf("SiteURL = %q, want https://example.com", cfg.SiteURL)
	}

	clearEnv(t)
	cfg, err = ConfigFromEnv()
	if err != nil {
		t.Fatalf("ConfigFromEnv: %v", err)
	}
	if cfg.SiteURL != "" {
		t.Errorf("unset CMS_SITE_URL: SiteURL = %q, want empty", cfg.SiteURL)
	}
}

// CMS_SECURE_COOKIES is the escape hatch for HTTPS in front of an install
// that leaves CMS_SITE_URL empty. It is a true/false value, and a
// misspelled one is a configuration mistake rather than a silent false —
// "CMS_SECURE_COOKIES=yes" quietly meaning "no" is exactly the failure
// this whole setting is prone to.
func TestConfigFromEnvSecureCookies(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  bool
	}{
		{"true", true}, {"1", true}, {"TRUE", true},
		{"false", false}, {"0", false},
	} {
		t.Run(tc.value, func(t *testing.T) {
			clearEnv(t)
			t.Setenv("CMS_SECURE_COOKIES", tc.value)
			cfg, err := ConfigFromEnv()
			if err != nil {
				t.Fatalf("CMS_SECURE_COOKIES=%s: %v", tc.value, err)
			}
			if cfg.SecureCookies != tc.want {
				t.Errorf("CMS_SECURE_COOKIES=%s gave SecureCookies=%v, want %v", tc.value, cfg.SecureCookies, tc.want)
			}
		})
	}

	t.Run("unset leaves it alone", func(t *testing.T) {
		clearEnv(t)
		cfg, err := ConfigFromEnv()
		if err != nil {
			t.Fatal(err)
		}
		if cfg.SecureCookies {
			t.Error("an unset CMS_SECURE_COOKIES forced the flag on")
		}
	})

	t.Run("malformed is an error", func(t *testing.T) {
		clearEnv(t)
		t.Setenv("CMS_SECURE_COOKIES", "yes-please")
		if _, err := ConfigFromEnv(); err == nil {
			t.Error("a malformed CMS_SECURE_COOKIES was accepted")
		}
	})
}

func TestNormalizeSiteURL(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"", ""},
		{"  ", ""},
		{"https://example.com", "https://example.com"},
		{"https://example.com/", "https://example.com"},
		{"https://example.com///", "https://example.com"},
		{"  https://example.com  ", "https://example.com"},
		{"http://localhost:4000", "http://localhost:4000"},
		// A bare host is what people reach for when asked for a server
		// name, and a canonical public URL is essentially always https.
		{"example.com", "https://example.com"},
		{"example.com/", "https://example.com"},
	} {
		if got := normalizeSiteURL(c.in); got != c.want {
			t.Errorf("normalizeSiteURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSiteBaseURL(t *testing.T) {
	req := func(tls bool, proto string) *http.Request {
		r := httptest.NewRequest("GET", "http://site.test/x", nil)
		r.Host = "site.test"
		if tls {
			r.TLS = &tls13.ConnectionState{}
		}
		if proto != "" {
			r.Header.Set("X-Forwarded-Proto", proto)
		}
		return r
	}

	configured := &CMS{cfg: Config{SiteURL: "https://canonical.example"}}
	if got := configured.siteBaseURL(req(false, "")); got != "https://canonical.example" {
		t.Errorf("configured SiteURL should win, got %q", got)
	}
	// Even when the request looks nothing like it — that is the point of
	// configuring one.
	if got := configured.siteBaseURL(req(true, "https")); got != "https://canonical.example" {
		t.Errorf("configured SiteURL should win over the request, got %q", got)
	}

	derived := &CMS{cfg: Config{}}
	for _, c := range []struct {
		name  string
		tls   bool
		proto string
		want  string
	}{
		{"plain", false, "", "http://site.test"},
		{"tls", true, "", "https://site.test"},
		{"behind a proxy", false, "https", "https://site.test"},
	} {
		if got := derived.siteBaseURL(req(c.tls, c.proto)); got != c.want {
			t.Errorf("%s: siteBaseURL = %q, want %q", c.name, got, c.want)
		}
	}
}

// The S3 variables that decide how media is addressed. Before these
// existed, S3_APPLY_PUBLIC_POLICY was the only one of the group reachable
// from the environment — so an env-configured deployment could open its
// bucket to the world and get nothing for it, because PublicURL went on
// handing out proxy paths.
func TestConfigFromEnvS3PublicSettings(t *testing.T) {
	t.Run("defaults are private", func(t *testing.T) {
		clearEnv(t)
		t.Setenv("S3_ENDPOINT", "s3.example.com")
		cfg, err := ConfigFromEnv()
		if err != nil {
			t.Fatal(err)
		}
		if cfg.S3.PublicRead || cfg.S3.UsePathStyle || cfg.S3.ApplyPublicReadPolicy ||
			cfg.S3.PublicBaseURL != "" {
			t.Errorf("a bare S3_ENDPOINT opened something up: %+v", cfg.S3)
		}
	})

	// A trailing slash on the base URL would double up with the "/" the
	// key is joined with.
	t.Run("public base url is trimmed", func(t *testing.T) {
		clearEnv(t)
		t.Setenv("S3_ENDPOINT", "s3.example.com")
		t.Setenv("S3_PUBLIC_BASE_URL", "  https://cdn.example.com/  ")
		cfg, err := ConfigFromEnv()
		if err != nil {
			t.Fatal(err)
		}
		if cfg.S3.PublicBaseURL != "https://cdn.example.com" {
			t.Errorf("PublicBaseURL = %q, want it trimmed", cfg.S3.PublicBaseURL)
		}
	})

	// Every one of these is true/false, and a value that is neither is a
	// configuration mistake rather than a silent false. S3_APPLY_PUBLIC_POLICY
	// used to be compared against "1", so "true" meant false — on a
	// variable whose whole job is opening a bucket.
	for _, key := range []string{"S3_PUBLIC_READ", "S3_USE_PATH_STYLE", "S3_APPLY_PUBLIC_POLICY"} {
		t.Run(key+" accepts true", func(t *testing.T) {
			clearEnv(t)
			t.Setenv("S3_ENDPOINT", "s3.example.com")
			t.Setenv("S3_PUBLIC_READ", "true") // so the policy has something to pair with
			t.Setenv(key, "true")
			cfg, err := ConfigFromEnv()
			if err != nil {
				t.Fatalf("%s=true: %v", key, err)
			}
			got := map[string]bool{
				"S3_PUBLIC_READ":         cfg.S3.PublicRead,
				"S3_USE_PATH_STYLE":      cfg.S3.UsePathStyle,
				"S3_APPLY_PUBLIC_POLICY": cfg.S3.ApplyPublicReadPolicy,
			}[key]
			if !got {
				t.Errorf("%s=true was not read as true", key)
			}
		})
		t.Run(key+" rejects nonsense", func(t *testing.T) {
			clearEnv(t)
			t.Setenv("S3_ENDPOINT", "s3.example.com")
			t.Setenv(key, "yes-please")
			if _, err := ConfigFromEnv(); err == nil {
				t.Errorf("%s=yes-please was accepted", key)
			}
		})
	}
}
