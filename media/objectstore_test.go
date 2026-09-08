package media

import (
	"context"
	"io"
	"strings"
	"testing"
)

func TestValidKeyPrefix(t *testing.T) {
	cases := map[string]bool{
		"":           true, // prefixing disabled
		"acme-hotel": true,
		"sawler.ca":  true,
		"Site_2":     true,
		"a/b":        false,
		"../escape":  false,
		"dots..":     false,
		"spa ce":     false,
		"media/":     false,
		"étage":      false,
	}
	for prefix, want := range cases {
		if got := validKeyPrefix(prefix); got != want {
			t.Errorf("validKeyPrefix(%q) = %v, want %v", prefix, got, want)
		}
	}
}

func TestKeyRoot(t *testing.T) {
	if got := keyRoot(""); got != "media/" {
		t.Errorf("keyRoot(\"\") = %q, want \"media/\"", got)
	}
	if got := keyRoot("acme"); got != "acme/media/" {
		t.Errorf("keyRoot(\"acme\") = %q, want \"acme/media/\"", got)
	}
}

func TestNewS3StoreRejectsBadKeyPrefix(t *testing.T) {
	_, err := NewS3Store(S3Config{
		Endpoint: "example.com", Bucket: "b", AccessKey: "k", Secret: "s",
		KeyPrefix: "a/b",
	})
	if err == nil {
		t.Fatal("NewS3Store accepted KeyPrefix with a slash")
	}
}

func TestPublicURL(t *testing.T) {
	cases := []struct {
		name string
		cfg  S3Config
		key  string
		want string
	}{
		{"proxy", S3Config{}, "media/abc/web.jpg", "/cms/media/abc/web.jpg"},
		{"proxy prefixed", S3Config{KeyPrefix: "acme"}, "acme/media/abc/web.jpg", "/cms/media/abc/web.jpg"},
		{"cdn", S3Config{PublicBaseURL: "https://cdn.example.com"}, "acme/media/abc/web.jpg", "https://cdn.example.com/acme/media/abc/web.jpg"},
		{"path style", S3Config{PublicRead: true, UsePathStyle: true, Endpoint: "s3.example.com", Bucket: "b"}, "acme/media/abc/web.jpg", "https://s3.example.com/b/acme/media/abc/web.jpg"},
		{"virtual host", S3Config{PublicRead: true, Endpoint: "s3.example.com", Bucket: "b"}, "acme/media/abc/web.jpg", "https://b.s3.example.com/acme/media/abc/web.jpg"},
	}
	for _, c := range cases {
		s := &S3Store{cfg: c.cfg}
		if got := s.PublicURL(c.key); got != c.want {
			t.Errorf("%s: PublicURL(%q) = %q, want %q", c.name, c.key, got, c.want)
		}
	}
}

// prefixedStore is a stub ObjectStore implementing KeyPrefixer.
type prefixedStore struct{ prefix string }

func (p prefixedStore) Put(context.Context, string, string, io.Reader) error { return nil }
func (p prefixedStore) Get(context.Context, string) (io.ReadCloser, string, error) {
	return nil, "", ErrObjectNotFound
}
func (p prefixedStore) Delete(context.Context, string) error { return nil }
func (p prefixedStore) PublicURL(key string) string          { return "/" + key }
func (p prefixedStore) KeyPrefix() string                    { return p.prefix }

func TestNewManagerKeyRoot(t *testing.T) {
	if got := NewManager(nil, prefixedStore{"acme"}, nil).KeyRoot(); got != "acme/media/" {
		t.Errorf("KeyRoot() = %q, want \"acme/media/\"", got)
	}
	// A store without KeyPrefixer keeps the bare root.
	if got := NewManager(nil, dumbStore{}, nil).KeyRoot(); got != "media/" {
		t.Errorf("KeyRoot() = %q, want \"media/\"", got)
	}
}

// dumbStore is a stub ObjectStore without KeyPrefixer.
type dumbStore struct{}

func (dumbStore) Put(context.Context, string, string, io.Reader) error { return nil }
func (dumbStore) Get(context.Context, string) (io.ReadCloser, string, error) {
	return nil, "", ErrObjectNotFound
}
func (dumbStore) Delete(context.Context, string) error { return nil }
func (dumbStore) PublicURL(key string) string          { return "/" + key }

// NewS3Store validates the config and builds a client without touching
// the network, so all of this runs in the fast lane. The S3 calls
// themselves are exercised against a real server in objectstore_s3_test.go.

func TestNewS3StoreRequiresItsCredentials(t *testing.T) {
	full := S3Config{Endpoint: "s3.example.com", Bucket: "b", AccessKey: "k", Secret: "s"}
	if _, err := NewS3Store(full); err != nil {
		t.Fatalf("a complete config was rejected: %v", err)
	}

	// Each of the four is required; leaving one out is a misconfiguration
	// that should be reported at startup rather than on the first upload.
	cases := map[string]func(*S3Config){
		"no endpoint":   func(c *S3Config) { c.Endpoint = "" },
		"no bucket":     func(c *S3Config) { c.Bucket = "" },
		"no access key": func(c *S3Config) { c.AccessKey = "" },
		"no secret":     func(c *S3Config) { c.Secret = "" },
	}
	for name, drop := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := full
			drop(&cfg)
			if _, err := NewS3Store(cfg); err == nil {
				t.Error("NewS3Store accepted an incomplete config")
			}
		})
	}
}

// The signing region is guessed from the endpoint, which is right for the
// cluster-labelled endpoints S3-compatible providers use. A host that
// names one explicitly keeps it — AWS is the case the guess gets wrong.
func TestNewS3StoreRegion(t *testing.T) {
	s, err := NewS3Store(S3Config{
		Endpoint: "us-ord-10.linodeobjects.com", Bucket: "b", AccessKey: "k", Secret: "s",
	})
	if err != nil {
		t.Fatalf("NewS3Store: %v", err)
	}
	if s.cfg.Region != "us-ord-10" {
		t.Errorf("region = %q, want it taken from the endpoint", s.cfg.Region)
	}

	s, err = NewS3Store(S3Config{
		Endpoint: "s3.us-east-2.amazonaws.com", Region: "us-east-2",
		Bucket: "b", AccessKey: "k", Secret: "s",
	})
	if err != nil {
		t.Fatalf("NewS3Store: %v", err)
	}
	if s.cfg.Region != "us-east-2" {
		t.Errorf("region = %q, want the configured one", s.cfg.Region)
	}
}

func TestRegionFromEndpoint(t *testing.T) {
	cases := map[string]string{
		"us-ord-10.linodeobjects.com": "us-ord-10",
		"nyc3.digitaloceanspaces.com": "nyc3",
		"s3.us-east-1.amazonaws.com":  "s3",
		// Nothing to split on: fall back to the region every S3 client
		// understands rather than to an empty one, which signs nothing.
		"localhost": "us-east-1",
		"":          "us-east-1",
	}
	for endpoint, want := range cases {
		if got := regionFromEndpoint(endpoint); got != want {
			t.Errorf("regionFromEndpoint(%q) = %q, want %q", endpoint, got, want)
		}
	}
}

// KeyPrefix is how the Manager learns to namespace a shared bucket, so a
// store reports exactly what it was configured with — including nothing.
func TestS3StoreKeyPrefixAccessor(t *testing.T) {
	for _, want := range []string{"", "acme", "acme-hotel_2"} {
		s, err := NewS3Store(S3Config{
			Endpoint: "s3.example.com", Bucket: "b", AccessKey: "k", Secret: "s",
			KeyPrefix: want,
		})
		if err != nil {
			t.Fatalf("NewS3Store(%q): %v", want, err)
		}
		if got := s.KeyPrefix(); got != want {
			t.Errorf("KeyPrefix() = %q, want %q", got, want)
		}
		// And the Manager picks it up through the KeyPrefixer interface.
		var kp KeyPrefixer = s
		if got := kp.KeyPrefix(); got != want {
			t.Errorf("as a KeyPrefixer, KeyPrefix() = %q, want %q", got, want)
		}
	}
}

// An endpoint may carry a scheme, and http is the reason it may: a store
// on the same machine or private network with no certificate — a MinIO in
// Docker, most often — is unreachable over https and was unreachable
// through this config at all until the scheme was allowed.
func TestSplitEndpoint(t *testing.T) {
	cases := map[string]struct {
		scheme, host string
		wantErr      bool
	}{
		// A bare host is https, which is what every hosted provider wants.
		"s3.example.com":              {"https", "s3.example.com", false},
		"https://s3.example.com":      {"https", "s3.example.com", false},
		"http://localhost:9000":       {"http", "localhost:9000", false},
		"https://s3.example.com:8443": {"https", "s3.example.com:8443", false},
		// A trailing slash is what a pasted URL carries, and it would
		// otherwise double up in every object URL.
		"https://s3.example.com/": {"https", "s3.example.com", false},
		// Anything past the host is not an endpoint. Silently keeping it
		// would produce URLs with a path buried in the hostname.
		"https://s3.example.com/media": {"", "", true},
		"s3.example.com/media":         {"", "", true},
		// A scheme that is neither is a typo, not a hostname.
		"ftp://s3.example.com": {"", "", true},
		"https://":             {"", "", true},
	}
	for endpoint, want := range cases {
		scheme, host, err := splitEndpoint(endpoint)
		switch {
		case want.wantErr && err == nil:
			t.Errorf("splitEndpoint(%q) = %q, %q; want an error", endpoint, scheme, host)
		case !want.wantErr && err != nil:
			t.Errorf("splitEndpoint(%q): %v", endpoint, err)
		case !want.wantErr && (scheme != want.scheme || host != want.host):
			t.Errorf("splitEndpoint(%q) = %q, %q; want %q, %q",
				endpoint, scheme, host, want.scheme, want.host)
		}
	}
}

// The scheme has to reach both places an endpoint is used, or a store
// configured for http would sign requests to one address and hand pages
// links to another.
func TestNewS3StoreCarriesTheEndpointScheme(t *testing.T) {
	cases := map[string]struct {
		endpoint, wantURL string
	}{
		"bare host":      {"s3.example.com", "https://s3.example.com/b/media/abc/web.jpg"},
		"explicit https": {"https://s3.example.com", "https://s3.example.com/b/media/abc/web.jpg"},
		"http":           {"http://localhost:9000", "http://localhost:9000/b/media/abc/web.jpg"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			s, err := NewS3Store(S3Config{
				Endpoint: c.endpoint, Bucket: "b", AccessKey: "k", Secret: "s",
				PublicRead: true, UsePathStyle: true,
			})
			if err != nil {
				t.Fatalf("NewS3Store: %v", err)
			}
			// Endpoint is normalized to a bare host, which is what the
			// signing region and the URL builders both read.
			if strings.Contains(s.cfg.Endpoint, "://") {
				t.Errorf("stored endpoint = %q, want the scheme taken off", s.cfg.Endpoint)
			}
			if got := s.PublicURL("media/abc/web.jpg"); got != c.wantURL {
				t.Errorf("PublicURL = %q, want %q", got, c.wantURL)
			}
		})
	}
}

// The region is guessed from the host, so a scheme must not become part
// of the guess — "https:" would be signed as the region and every request
// rejected.
func TestNewS3StoreRegionIgnoresTheScheme(t *testing.T) {
	s, err := NewS3Store(S3Config{
		Endpoint: "https://us-ord-10.linodeobjects.com",
		Bucket:   "b", AccessKey: "k", Secret: "s",
	})
	if err != nil {
		t.Fatalf("NewS3Store: %v", err)
	}
	if s.cfg.Region != "us-ord-10" {
		t.Errorf("region = %q, want it taken from the host alone", s.cfg.Region)
	}
}

func TestNewS3StoreRejectsABadEndpoint(t *testing.T) {
	for _, endpoint := range []string{"ftp://s3.example.com", "https://s3.example.com/media", "https://"} {
		if _, err := NewS3Store(S3Config{
			Endpoint: endpoint, Bucket: "b", AccessKey: "k", Secret: "s",
		}); err == nil {
			t.Errorf("NewS3Store accepted endpoint %q", endpoint)
		}
	}
}

// publicStore is a stub ObjectStore that hands out direct bucket URLs,
// the way a PublicRead or PublicBaseURL deployment does.
type publicStore struct{}

func (publicStore) Put(context.Context, string, string, io.Reader) error { return nil }
func (publicStore) Get(context.Context, string) (io.ReadCloser, string, error) {
	return nil, "", ErrObjectNotFound
}
func (publicStore) Delete(context.Context, string) error { return nil }
func (publicStore) PublicURL(key string) string          { return "https://cdn.example.com/" + key }

// TestURLKeepsSVGOnTheProxy: the script-blocking CSP that backs up the
// upload scan is a header the CMS writes, so it only exists on responses
// the CMS writes. An SVG addressed at a bucket or CDN is served by
// somebody else, under no policy — so it stays on the proxy whatever the
// store would say. Everything else takes the direct URL as before.
func TestURLKeepsSVGOnTheProxy(t *testing.T) {
	m := NewManager(nil, publicStore{}, nil)

	svg := &Media{Kind: KindImage, StoreKey: "abc", Ext: ".svg", VariantExt: ".svg"}
	for _, rendition := range []string{"original", "web", "card", "thumb"} {
		got := m.URL(svg, rendition)
		if !strings.HasPrefix(got, ProxyPathPrefix) {
			t.Errorf("URL(svg, %q) = %q, want a %s… proxy path", rendition, got, ProxyPathPrefix)
		}
		if strings.Contains(got, "cdn.example.com") {
			t.Errorf("URL(svg, %q) = %q: an SVG must not be addressed at the bucket", rendition, got)
		}
	}
	// The exact address, so the proxy can actually resolve it.
	if got, want := m.URL(svg, "web"), "/cms/media/abc/web.svg"; got != want {
		t.Errorf("URL(svg, \"web\") = %q, want %q", got, want)
	}

	// Not a blanket move to the proxy: raster images, videos and
	// documents keep their direct URLs, which is the point of a CDN.
	raster := &Media{Kind: KindImage, StoreKey: "def", Ext: ".jpg", VariantExt: ".webp"}
	if got, want := m.URL(raster, "web"), "https://cdn.example.com/media/def/web.webp"; got != want {
		t.Errorf("URL(jpeg, \"web\") = %q, want %q", got, want)
	}
	if got, want := m.URL(raster, "original"), "https://cdn.example.com/media/def/original.jpg"; got != want {
		t.Errorf("URL(jpeg, \"original\") = %q, want %q", got, want)
	}
	video := &Media{Kind: KindVideo, StoreKey: "ghi", Ext: ".mp4", VariantExt: ".webp"}
	if got, want := m.URL(video, "original"), "https://cdn.example.com/media/ghi/original.mp4"; got != want {
		t.Errorf("URL(video, \"original\") = %q, want %q", got, want)
	}
	if got, want := m.URL(video, "poster"), "https://cdn.example.com/media/ghi/web.webp"; got != want {
		t.Errorf("URL(video, \"poster\") = %q, want %q", got, want)
	}
	doc := &Media{Kind: KindFile, StoreKey: "jkl/report.pdf", Ext: ".pdf"}
	if got, want := m.URL(doc, "original"), "https://cdn.example.com/media/jkl/report.pdf"; got != want {
		t.Errorf("URL(pdf, \"original\") = %q, want %q", got, want)
	}
}

// The proxy re-adds the key root, so a deployment prefix must not appear
// in the path the page links — the same contract S3Store.PublicURL keeps
// for a private bucket.
func TestURLKeepsSVGOnTheProxyUnderAKeyPrefix(t *testing.T) {
	m := NewManager(nil, prefixedPublicStore{}, nil)
	svg := &Media{Kind: KindImage, StoreKey: "abc", Ext: ".svg", VariantExt: ".svg"}
	if got, want := m.URL(svg, "web"), "/cms/media/abc/web.svg"; got != want {
		t.Errorf("URL(svg, \"web\") = %q, want %q", got, want)
	}
}

type prefixedPublicStore struct{ publicStore }

func (prefixedPublicStore) KeyPrefix() string { return "acme" }
