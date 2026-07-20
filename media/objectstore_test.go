package media

import (
	"context"
	"io"
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
