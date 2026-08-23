package admin

import (
	"net/http/httptest"
	"net/url"
	"reflect"
	"regexp"
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
		"set_height": {"100"}, "set_valign": {"center"}, "set_corners": {"medium"},
		"set_padding": {"tight"}, "set_size": {"large"},
	})
	if len(errs) != 0 {
		t.Fatalf("preset: unexpected errors %v", errs)
	}
	want := map[string]string{"bg": "dark", "width": "wide", "height": "100", "valign": "center",
		"corners": "medium", "padding": "tight", "size": "large"}
	for k, v := range want {
		if set[k] != v {
			t.Errorf("preset settings[%q] = %q, want %q", k, set[k], v)
		}
	}

	// Preset with junk values: bg/width fall back to defaults (never
	// empty — a preset must stay recognizable), junk height/valign drop,
	// junk corners resolve to the default and stay unstored.
	set, errs, _, _ = do(url.Values{
		"name": {"P"}, "html": {"<p>x</p>"}, "kind": {"preset"},
		"set_bg": {"nope"}, "set_width": {"nope"},
		"set_height": {"9000"}, "set_valign": {"sideways"}, "set_corners": {"nope"},
		"set_padding": {"nope"}, "set_size": {"nope"},
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
	if _, ok := set["corners"]; ok {
		t.Error("junk corners should resolve to the default and not be stored")
	}
	if _, ok := set["padding"]; ok {
		t.Error("junk padding should resolve to the default and not be stored")
	}
	if _, ok := set["size"]; ok {
		t.Error("junk size should resolve to the default and not be stored")
	}

	// Validation errors still fire regardless of kind.
	_, errs, _, _ = do(url.Values{"kind": {"preset"}})
	if errs["name"] == "" || errs["html"] == "" {
		t.Errorf("missing name/html not reported: %v", errs)
	}
}

// TestSnippetFormOffersEverySectionAxis is the guard on the half of this
// feature that lives in markup. parseSnippetForm reads a set_* value per
// curated axis, but a value the form never posts silently resolves to the
// default — so an axis added to SectionStyles and to the handler, and
// forgotten in the template, is not an error anywhere: the field simply
// isn't there, and every preset an admin creates is stuck at the first
// option. (That is exactly what happened to vertical spacing.)
//
// Comparing the axes the template iterates against the fields the struct
// declares makes the omission fail here instead.
func TestSnippetFormOffersEverySectionAxis(t *testing.T) {
	src, err := templateFS.ReadFile("templates/snippet_form.gohtml")
	if err != nil {
		t.Fatalf("reading the snippet form template: %v", err)
	}
	offered := map[string]bool{}
	for _, m := range regexp.MustCompile(`range \.SectionStyles\.(\w+)`).FindAllSubmatch(src, -1) {
		offered[string(m[1])] = true
	}

	typ := reflect.TypeOf(render.SectionStyles{})
	optSlice := reflect.TypeOf([]render.SectionOption{})
	var axes int
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		if f.Type != optSlice {
			continue
		}
		axes++
		if !offered[f.Name] {
			t.Errorf("the section-preset form has no control for SectionStyles.%s — "+
				"presets can never carry anything but its first option", f.Name)
		}
	}
	if axes == 0 {
		t.Fatal("no []SectionOption fields found on SectionStyles — this test is not testing anything")
	}
	for name := range offered {
		if _, ok := typ.FieldByName(name); !ok {
			t.Errorf("the form iterates .SectionStyles.%s, which is not a field", name)
		}
	}
}
