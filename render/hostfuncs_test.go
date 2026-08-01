package render

import (
	"bytes"
	"html/template"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/tsawler/cms/content"
)

// hostFuncFS exercises the two things a host func has to do: return data a
// template ranges over, and be callable more than once per page — the whole
// point of a FuncMap rather than a single hook.
var hostFuncFS = fstest.MapFS{
	"base.gohtml": &fstest.MapFile{Data: []byte(
		`{{define "base"}}<html><head>{{cmsHead}}</head><body>` +
			`{{block "content" .}}{{end}}{{cmsScripts}}</body></html>{{end}}`)},
	"pages/home.gohtml": &fstest.MapFile{Data: []byte(
		`{{template "base" .}}{{define "content"}}` +
			`<h1>{{cmsText "heading"}}</h1>` +
			`<p>{{vehicleCount}} in stock</p>` +
			`<ul>{{range featuredVehicles 2}}<li>{{.Name}} {{.Price}}</li>{{end}}</ul>` +
			`{{end}}`)},
}

type testVehicle struct {
	Name  string
	Price string
}

func hostFuncRenderer(t *testing.T, host template.FuncMap) *Renderer {
	t.Helper()
	r, err := NewWithFuncs(hostFuncFS, []string{"base.gohtml"},
		[]PageTemplate{{File: "pages/home.gohtml", Label: "Home"}}, nil, host)
	if err != nil {
		t.Fatalf("NewWithFuncs: %v", err)
	}
	return r
}

func renderHome(t *testing.T, r *Renderer, in Input) string {
	t.Helper()
	in.Page = &content.Page{Slug: "", TemplateName: "pages/home.gohtml", Title: "Home"}
	in.Locale = "en"
	var buf bytes.Buffer
	if err := r.Render(&buf, in); err != nil {
		t.Fatalf("Render: %v", err)
	}
	return buf.String()
}

func TestHostFuncsRenderMultipleFunctions(t *testing.T) {
	r := hostFuncRenderer(t, template.FuncMap{
		"featuredVehicles": func(n int) []testVehicle {
			all := []testVehicle{{"Bronco Sport", "$31,977"}, {"Equinox", "$37,977"}, {"K4", "$27,977"}}
			return all[:min(n, len(all))]
		},
		"vehicleCount": func() int { return 3 },
	})

	got := renderHome(t, r, Input{})
	for _, want := range []string{"3 in stock", "Bronco Sport $31,977", "Equinox $37,977"} {
		if !strings.Contains(got, want) {
			t.Errorf("rendered page missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "K4") {
		t.Errorf("featuredVehicles 2 returned a third vehicle:\n%s", got)
	}
}

func TestHostFuncsInputOverridesPerRender(t *testing.T) {
	r := hostFuncRenderer(t, template.FuncMap{
		"featuredVehicles": func(int) []testVehicle { return []testVehicle{{"Declared", "$0"}} },
		"vehicleCount":     func() int { return 0 },
	})

	// Only one name is replaced; the other must keep its declared
	// implementation rather than disappearing.
	got := renderHome(t, r, Input{Funcs: template.FuncMap{
		"featuredVehicles": func(int) []testVehicle { return []testVehicle{{"PerRequest", "$1"}} },
	}})

	if !strings.Contains(got, "PerRequest $1") {
		t.Errorf("Input.Funcs did not override the declared func:\n%s", got)
	}
	if strings.Contains(got, "Declared") {
		t.Errorf("declared func still ran after being overridden:\n%s", got)
	}
	if !strings.Contains(got, "0 in stock") {
		t.Errorf("un-overridden func lost its declared implementation:\n%s", got)
	}
}

func TestHostFuncsCannotShadowCMSFuncs(t *testing.T) {
	// Rejected at construction...
	_, err := NewWithFuncs(hostFuncFS, []string{"base.gohtml"},
		[]PageTemplate{{File: "pages/home.gohtml", Label: "Home"}}, nil,
		template.FuncMap{"cmsHead": func() template.HTML { return "<!-- hijacked -->" }})
	if err == nil {
		t.Fatal("NewWithFuncs accepted a func in the reserved cms* namespace")
	}
	if !strings.Contains(err.Error(), "reserved") {
		t.Errorf("error should explain the reserved prefix, got: %v", err)
	}

	// ...and defensively dropped if one reaches a render anyway, so a
	// hand-assembled FuncMap cannot cost the page {{cmsHead}}.
	r := hostFuncRenderer(t, template.FuncMap{
		"featuredVehicles": func(int) []testVehicle { return nil },
		"vehicleCount":     func() int { return 0 },
	})
	got := renderHome(t, r, Input{Funcs: template.FuncMap{
		"cmsScripts": func() template.HTML { return "<!-- hijacked -->" },
	}})
	if strings.Contains(got, "hijacked") {
		t.Errorf("Input.Funcs shadowed a reserved cms func:\n%s", got)
	}
}

func TestValidateFuncNames(t *testing.T) {
	cases := []struct {
		name    string
		funcs   template.FuncMap
		wantErr bool
	}{
		{"nil is fine", nil, false},
		{"ordinary names", template.FuncMap{"vehicles": func() int { return 0 }}, false},
		{"underscore and digits", template.FuncMap{"top_10": func() int { return 0 }}, false},
		{"reserved prefix", template.FuncMap{"cmsThing": func() int { return 0 }}, true},
		{"reserved exact", template.FuncMap{"cms": func() int { return 0 }}, true},
		{"empty name", template.FuncMap{"": func() int { return 0 }}, true},
		{"leading digit", template.FuncMap{"3vehicles": func() int { return 0 }}, true},
		{"dot in name", template.FuncMap{"a.b": func() int { return 0 }}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateFuncNames(tc.funcs)
			if (err != nil) != tc.wantErr {
				t.Errorf("ValidateFuncNames(%v) error = %v, wantErr %v", tc.funcs, err, tc.wantErr)
			}
		})
	}
}

func TestCheckTemplateFuncsAcceptsHostNames(t *testing.T) {
	src := `{{define "content"}}{{range featuredVehicles 3}}{{.Name}}{{end}}{{end}}`

	if err := CheckTemplate("t", src); err == nil {
		t.Error("CheckTemplate should not know a host func name")
	}
	host := template.FuncMap{"featuredVehicles": func(int) []testVehicle { return nil }}
	if err := CheckTemplateFuncs("t", src, host); err != nil {
		t.Errorf("CheckTemplateFuncs with the host map: %v", err)
	}
}
