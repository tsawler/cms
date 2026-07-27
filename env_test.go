package cms

import (
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
	"CMS_REMEMBER_DAYS", "CMS_MEDIA_WEBP_QUALITY", "CMS_MEDIA_MAX_VIDEO_MB",
	"CMS_TAILWIND_COMMAND", "CMS_TAILWIND_DIR",
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
	if cfg.S3 != nil || cfg.Captcha != nil || cfg.Tailwind != nil {
		t.Errorf("empty environment enabled features: S3=%v Captcha=%v Tailwind=%v",
			cfg.S3, cfg.Captcha, cfg.Tailwind)
	}
	if cfg.RememberFor != 0 || cfg.MediaWebPQuality != 0 || cfg.MediaMaxVideoMB != 0 {
		t.Errorf("empty environment set values: %+v", cfg)
	}
}

func TestConfigFromEnvFull(t *testing.T) {
	clearEnv(t)
	for k, v := range map[string]string{
		"S3_ENDPOINT":            "s3.example.com",
		"S3_REGION":              "us-east-1",
		"S3_BUCKET":              "my-site",
		"S3_ACCESS_KEY":          "key",
		"S3_SECRET":              "secret",
		"S3_KEY_PREFIX":          "prod",
		"S3_APPLY_PUBLIC_POLICY": "1",
		"CAP_URL":                "http://localhost:3300",
		"CAP_INTERNAL_URL":       "http://cap:3000",
		"CAP_SITE_KEY":           "site",
		"CAP_SECRET":             "sk-x",
		"CAP_WIDGET":             "visible",
		"CMS_REMEMBER_DAYS":      "14",
		"CMS_MEDIA_WEBP_QUALITY": "0.5",
		"CMS_MEDIA_MAX_VIDEO_MB": "128",
		"CMS_TAILWIND_COMMAND":   "./tw.sh {content} {output}",
		"CMS_TAILWIND_DIR":       "assets",
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
		!cfg.S3.ApplyPublicReadPolicy {
		t.Errorf("S3 = %+v", cfg.S3)
	}
	if cfg.Captcha == nil {
		t.Fatal("Captcha not configured")
	}
	if cfg.Captcha.URL != "http://localhost:3300" || cfg.Captcha.InternalURL != "http://cap:3000" ||
		cfg.Captcha.SiteKey != "site" || cfg.Captcha.Secret != "sk-x" || !cfg.Captcha.Visible {
		t.Errorf("Captcha = %+v", cfg.Captcha)
	}
	if cfg.RememberFor != 14*24*time.Hour {
		t.Errorf("RememberFor = %v, want 336h", cfg.RememberFor)
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
		"CMS_MEDIA_WEBP_QUALITY": "high",
		"CMS_MEDIA_MAX_VIDEO_MB": "big",
	} {
		clearEnv(t)
		t.Setenv(envVar, bad)
		if _, err := ConfigFromEnv(); err == nil {
			t.Errorf("%s=%q: expected an error, got nil", envVar, bad)
		}
	}

	// Zero and negative "remember me" days are as wrong as non-numeric.
	for _, bad := range []string{"0", "-3"} {
		clearEnv(t)
		t.Setenv("CMS_REMEMBER_DAYS", bad)
		if _, err := ConfigFromEnv(); err == nil {
			t.Errorf("CMS_REMEMBER_DAYS=%q: expected an error, got nil", bad)
		}
	}
}
