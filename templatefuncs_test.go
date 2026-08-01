package cms

import (
	"database/sql"
	"database/sql/driver"
	"errors"
	"html/template"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

// New validates config without touching the database — it only wraps the
// pool — so a driver that never connects is enough to reach the checks
// these tests are about.
type stubDriver struct{}

func (stubDriver) Open(string) (driver.Conn, error) { return nil, errors.New("stub: no connections") }

func init() { sql.Register("cms-config-test", stubDriver{}) }

func stubDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("cms-config-test", "")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

var funcTestFS = fstest.MapFS{
	"templates/base.gohtml": &fstest.MapFile{Data: []byte(
		`{{define "base"}}<html><head>{{cmsHead}}</head><body>` +
			`{{block "content" .}}{{end}}{{cmsScripts}}</body></html>{{end}}`)},
	"templates/pages/home.gohtml": &fstest.MapFile{Data: []byte(
		`{{template "base" .}}{{define "content"}}<p>{{stockCount}}</p>{{end}}`)},
}

func funcTestConfig(t *testing.T) Config {
	t.Helper()
	return Config{
		DB:              stubDB(t),
		TemplateFS:      funcTestFS,
		SharedTemplates: []string{"templates/base.gohtml"},
		PageTemplates:   []PageTemplate{{File: "templates/pages/home.gohtml", Label: "Home"}},
	}
}

// New must reject a host func inside the reserved cms* namespace, so a
// later release can add funcs without silently losing to a host's.
func TestNewRejectsReservedTemplateFuncName(t *testing.T) {
	cfg := funcTestConfig(t)
	cfg.TemplateFuncs = template.FuncMap{"cmsStock": func() int { return 0 }}

	_, err := New(cfg)
	if err == nil {
		t.Fatal("New accepted a TemplateFuncs entry in the cms* namespace")
	}
	if !strings.Contains(err.Error(), "reserved") {
		t.Errorf("error should name the reserved prefix, got: %v", err)
	}
}

// RequestFuncs alone cannot work: templates parse against TemplateFuncs,
// so a name declared only per request is one no template could call.
func TestNewRejectsRequestFuncsWithoutTemplateFuncs(t *testing.T) {
	cfg := funcTestConfig(t)
	cfg.RequestFuncs = func(*http.Request) template.FuncMap { return nil }

	_, err := New(cfg)
	if err == nil {
		t.Fatal("New accepted RequestFuncs with no TemplateFuncs")
	}
	if !strings.Contains(err.Error(), "TemplateFuncs") {
		t.Errorf("error should point at TemplateFuncs, got: %v", err)
	}
}

func TestNewAcceptsHostTemplateFuncs(t *testing.T) {
	cfg := funcTestConfig(t)
	cfg.TemplateFuncs = template.FuncMap{"stockCount": func() int { return 0 }}
	cfg.RequestFuncs = func(*http.Request) template.FuncMap { return nil }

	if _, err := New(cfg); err != nil {
		t.Fatalf("New with host template funcs: %v", err)
	}
}

// requestFuncs is what binds a host func to the request; it must tolerate
// an unset RequestFuncs rather than panicking on every page render.
func TestRequestFuncsBindsPerRequest(t *testing.T) {
	c := &CMS{cfg: Config{}}
	if got := c.requestFuncs(httptest.NewRequest(http.MethodGet, "/", nil)); got != nil {
		t.Errorf("requestFuncs with no RequestFuncs = %v, want nil", got)
	}

	c = &CMS{cfg: Config{
		RequestFuncs: func(r *http.Request) template.FuncMap {
			return template.FuncMap{"path": func() string { return r.URL.Path }}
		},
	}}
	got := c.requestFuncs(httptest.NewRequest(http.MethodGet, "/inventory", nil))
	fn, ok := got["path"].(func() string)
	if !ok {
		t.Fatalf("requestFuncs did not return the host's func, got %v", got)
	}
	if fn() != "/inventory" {
		t.Errorf("host func saw path %q, want /inventory", fn())
	}
}
