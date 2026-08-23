package cms

import (
	"reflect"
	"testing"
	"testing/fstest"

	"github.com/tsawler/cms/render"
)

func TestClassTokens(t *testing.T) {
	seen := map[string]bool{}
	var out []string
	classTokens(`<div class="a b  c"><p class='d a'>x</p><span class = "e">y</span></div>`, seen, &out)
	want := []string{"a", "b", "c", "d", "e"}
	if !reflect.DeepEqual(out, want) {
		t.Errorf("classTokens = %v, want %v", out, want)
	}
}

func TestBuildHash(t *testing.T) {
	tc := &TailwindConfig{Command: []string{"tw", "{content}", "{output}"}}
	a := buildHash(tc, []string{"a", "b", "c"}, "src1")
	if b := buildHash(tc, []string{"a", "b", "c"}, "src1"); b != a {
		t.Errorf("same build hashed differently: %q vs %q", a, b)
	}
	if len(a) != 16 {
		t.Errorf("hash length = %d, want 16", len(a))
	}
	if c := buildHash(tc, []string{"a", "b"}, "src1"); c == a {
		t.Errorf("different class sets hashed identically: %q", c)
	}
	// A different command must invalidate the artifact — a new Tailwind
	// version or config produces different CSS from the same classes.
	tc2 := &TailwindConfig{Command: []string{"tw2", "{content}", "{output}"}}
	if c := buildHash(tc2, []string{"a", "b", "c"}, "src1"); c == a {
		t.Errorf("different commands hashed identically: %q", c)
	}
	// And so must an edited template. This is the case that used to slip
	// through: the class set is unchanged, so the rebuild was skipped and
	// the stale stylesheet went on overriding the site's own.
	if c := buildHash(tc, []string{"a", "b", "c"}, "src2"); c == a {
		t.Errorf("different sources hashed identically: %q", c)
	}
}

// TestSourcesDigest covers the fingerprint that makes a template edit
// invalidate the artifact.
func TestSourcesDigest(t *testing.T) {
	base := fstest.MapFS{
		"pages/home.gohtml": &fstest.MapFile{Data: []byte(`<div class="sm:grid-cols-2">`)},
		"pages/list.gohtml": &fstest.MapFile{Data: []byte(`<p class="p-5">`)},
	}
	a, err := sourcesDigest(base)
	if err != nil {
		t.Fatalf("sourcesDigest: %v", err)
	}
	if len(a) != 16 {
		t.Errorf("digest length = %d, want 16", len(a))
	}

	same, err := sourcesDigest(fstest.MapFS{
		"pages/home.gohtml": &fstest.MapFile{Data: []byte(`<div class="sm:grid-cols-2">`)},
		"pages/list.gohtml": &fstest.MapFile{Data: []byte(`<p class="p-5">`)},
	})
	if err != nil {
		t.Fatalf("sourcesDigest: %v", err)
	}
	if same != a {
		t.Errorf("identical trees digested differently: %q vs %q", a, same)
	}

	// The real-world change: a class added to a template.
	edited, err := sourcesDigest(fstest.MapFS{
		"pages/home.gohtml": &fstest.MapFile{Data: []byte(`<div class="sm:grid-cols-2">`)},
		"pages/list.gohtml": &fstest.MapFile{Data: []byte(`<p class="p-5 lg:grid-cols-6">`)},
	})
	if err != nil {
		t.Fatalf("sourcesDigest: %v", err)
	}
	if edited == a {
		t.Errorf("an edited template digested identically: %q", edited)
	}

	// A rename is a change too: a moved file can change what the
	// scanner's globs pick up.
	renamed, err := sourcesDigest(fstest.MapFS{
		"pages/home.gohtml":  &fstest.MapFile{Data: []byte(`<div class="sm:grid-cols-2">`)},
		"pages/other.gohtml": &fstest.MapFile{Data: []byte(`<p class="p-5">`)},
	})
	if err != nil {
		t.Fatalf("sourcesDigest: %v", err)
	}
	if renamed == a {
		t.Errorf("a renamed template digested identically: %q", renamed)
	}

	// Content is not concatenated blindly: "ab"+"c" and "a"+"bc" across
	// two files must not collide.
	x, _ := sourcesDigest(fstest.MapFS{
		"a": &fstest.MapFile{Data: []byte("ab")},
		"b": &fstest.MapFile{Data: []byte("c")},
	})
	y, _ := sourcesDigest(fstest.MapFS{
		"a": &fstest.MapFile{Data: []byte("a")},
		"b": &fstest.MapFile{Data: []byte("bc")},
	})
	if x == y {
		t.Errorf("split contents collided: %q", x)
	}

	if got, err := sourcesDigest(nil); err != nil || got != "" {
		t.Errorf("sourcesDigest(nil) = %q, %v; want \"\", nil", got, err)
	}
}

// TestCSSHash covers the other half: the URL must follow the bytes, not
// the build key. A Tailwind upgrade recompiles identical inputs into
// different CSS, and a URL that did not move would leave every browser
// holding an immutable copy of the old file.
func TestCSSHash(t *testing.T) {
	a := cssHash(".p-5{padding:1.25rem}")
	if len(a) != 16 {
		t.Errorf("hash length = %d, want 16", len(a))
	}
	if b := cssHash(".p-5{padding:1.25rem}"); b != a {
		t.Errorf("identical css hashed differently: %q vs %q", a, b)
	}
	if b := cssHash(".p-5{padding:20px}"); b == a {
		t.Errorf("different css hashed identically: %q", b)
	}
	if got := cssHash(""); got != "" {
		t.Errorf("cssHash(\"\") = %q, want empty", got)
	}
	if !contentCSSHashRe.MatchString(a) {
		t.Errorf("hash %q is not a servable URL segment", a)
	}
}

func TestTailwindArgv(t *testing.T) {
	got := tailwindArgv(
		[]string{"tailwindcss", "-i", "in.css", "-o", "{output}", "--content", "{content}"},
		"/tmp/content.html", "/tmp/out.css")
	want := []string{"tailwindcss", "-i", "in.css", "-o", "/tmp/out.css", "--content", "/tmp/content.html"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("tailwindArgv = %v, want %v", got, want)
	}
}

func TestValidateTailwindConfig(t *testing.T) {
	if err := validateTailwindConfig(nil); err != nil {
		t.Errorf("nil config should be valid, got %v", err)
	}
	if err := validateTailwindConfig(&TailwindConfig{}); err == nil {
		t.Error("empty command should be rejected")
	}
	if err := validateTailwindConfig(&TailwindConfig{Command: []string{"tw", "-o", "{output}"}}); err == nil {
		t.Error("missing {content} placeholder should be rejected")
	}
	if err := validateTailwindConfig(&TailwindConfig{
		Command: []string{"tw", "-o", "{output}", "--content", "{content}"}}); err != nil {
		t.Errorf("valid config rejected: %v", err)
	}
}

// TestSectionStyleAxesCoversEveryAxis is a drift guard on the generated
// stylesheet's corpus. Section settings are stored as keys and resolved to
// classes at render time, so no scan of stored content can ever see them —
// the corpus is the only thing that compiles them. An axis added to
// SectionStyles but left out of sectionStyleAxes produces a settings
// dropdown whose every choice applies a class nothing has styled, which is
// invisible until someone picks one on a real page.
func TestSectionStyleAxesCoversEveryAxis(t *testing.T) {
	// Fill every []SectionOption field with a marker class, so a
	// forgotten axis is a marker that never comes back.
	ss := &render.SectionStyles{}
	v := reflect.ValueOf(ss).Elem()
	typ := v.Type()
	want := map[string]string{}
	optSlice := reflect.TypeOf([]render.SectionOption{})
	for i := 0; i < typ.NumField(); i++ {
		if typ.Field(i).Type != optSlice {
			continue
		}
		marker := "axis-" + typ.Field(i).Name
		want[marker] = typ.Field(i).Name
		v.Field(i).Set(reflect.ValueOf([]render.SectionOption{{Key: "k", Class: marker}}))
	}
	if len(want) == 0 {
		t.Fatal("no []SectionOption fields found on SectionStyles — this test is not testing anything")
	}

	got := map[string]bool{}
	for _, list := range sectionStyleAxes(ss) {
		for _, o := range list {
			got[o.Class] = true
		}
	}
	for marker, field := range want {
		if !got[marker] {
			t.Errorf("SectionStyles.%s is not folded into the content stylesheet's corpus — "+
				"add it to sectionStyleAxes, or its classes compile nowhere", field)
		}
	}
}
