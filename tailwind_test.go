package cms

import (
	"reflect"
	"testing"
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
	a := buildHash(tc, []string{"a", "b", "c"})
	if b := buildHash(tc, []string{"a", "b", "c"}); b != a {
		t.Errorf("same build hashed differently: %q vs %q", a, b)
	}
	if len(a) != 16 {
		t.Errorf("hash length = %d, want 16", len(a))
	}
	if c := buildHash(tc, []string{"a", "b"}); c == a {
		t.Errorf("different class sets hashed identically: %q", c)
	}
	// A different command must invalidate the artifact — a new Tailwind
	// version or config produces different CSS from the same classes.
	tc2 := &TailwindConfig{Command: []string{"tw2", "{content}", "{output}"}}
	if c := buildHash(tc2, []string{"a", "b", "c"}); c == a {
		t.Errorf("different commands hashed identically: %q", c)
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
