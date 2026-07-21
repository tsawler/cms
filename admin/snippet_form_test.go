package admin

import (
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/tsawler/cms/render"
)

func TestParseSnippetForm(t *testing.T) {
	s := &server{deps: Deps{SectionStyles: render.DefaultSectionStyles()}}

	do := func(vals url.Values) (map[string]string, map[string]string, string, string) {
		r := httptest.NewRequest("POST", "/admin/snippets/new",
			strings.NewReader(vals.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		sn, errs := s.parseSnippetForm(r)
		return sn.Settings, errs, sn.Name, sn.HTML
	}

	// Plain block: no settings, even if stray set_* fields are posted.
	set, errs, name, html := do(url.Values{
		"name": {" Card "}, "html": {"<p>x</p>"},
		"kind": {"block"}, "set_bg": {"dark"},
	})
	if len(errs) != 0 || set != nil || name != "Card" || html != "<p>x</p>" {
		t.Errorf("plain block: settings=%v errs=%v name=%q", set, errs, name)
	}

	// Preset: curated keys resolve, height/valign validated.
	set, errs, _, _ = do(url.Values{
		"name": {"Big hero"}, "html": {"<h1>x</h1>"}, "kind": {"preset"},
		"set_bg": {"dark"}, "set_width": {"wide"},
		"set_height": {"100"}, "set_valign": {"center"},
	})
	if len(errs) != 0 {
		t.Fatalf("preset: unexpected errors %v", errs)
	}
	want := map[string]string{"bg": "dark", "width": "wide", "height": "100", "valign": "center"}
	for k, v := range want {
		if set[k] != v {
			t.Errorf("preset settings[%q] = %q, want %q", k, set[k], v)
		}
	}

	// Preset with junk values: bg/width fall back to defaults (never
	// empty — a preset must stay recognizable), junk height/valign drop.
	set, errs, _, _ = do(url.Values{
		"name": {"P"}, "html": {"<p>x</p>"}, "kind": {"preset"},
		"set_bg": {"nope"}, "set_width": {"nope"},
		"set_height": {"9000"}, "set_valign": {"sideways"},
	})
	if len(errs) != 0 {
		t.Fatalf("junk preset: unexpected errors %v", errs)
	}
	if set["bg"] != "default" || set["width"] != "normal" {
		t.Errorf("junk keys should resolve to defaults, got %v", set)
	}
	if _, ok := set["height"]; ok {
		t.Error("junk height should be dropped")
	}
	if _, ok := set["valign"]; ok {
		t.Error("junk valign should be dropped")
	}

	// Validation errors still fire regardless of kind.
	_, errs, _, _ = do(url.Values{"kind": {"preset"}})
	if errs["name"] == "" || errs["html"] == "" {
		t.Errorf("missing name/html not reported: %v", errs)
	}
}
