package admin

import "testing"

// dropArbitraryClasses runs over editorHTMLPolicy's output and touches
// nothing but arbitrary-value class tokens the save has not vouched for.
func TestDropArbitraryClasses(t *testing.T) {
	vouched := map[string]bool{
		"shadow-[0_6px_22px_rgba(51,48,46,0.07)]": true,
		"[&_p]:mb-2": true, // compared unescaped: the policy writes & as &amp;
	}
	cases := []struct{ name, in, want string }{
		{"ordinary classes pass",
			`<p class="font-bold text-lg sm:grid-cols-2 bg-red-500/50">x</p>`,
			`<p class="font-bold text-lg sm:grid-cols-2 bg-red-500/50">x</p>`},
		{"an arbitrary property is dropped, the rest kept",
			`<p class="[background:url(//evil/x)] font-bold">x</p>`,
			`<p class="font-bold">x</p>`},
		{"arbitrary values and variants are dropped",
			`<div class="bg-[url(//evil/y)] fixed [body:has(&amp;)_*]:hidden bg-(--brand)">x</div>`,
			`<div class="fixed">x</div>`},
		{"a class left empty goes",
			`<p class="[mask:none]" style="text-align: center">x</p>`,
			`<p style="text-align: center">x</p>`},
		{"vouched tokens stay, escaped or not",
			`<div class="shadow-[0_6px_22px_rgba(51,48,46,0.07)] [&amp;_p]:mb-2">x</div>`,
			`<div class="shadow-[0_6px_22px_rgba(51,48,46,0.07)] [&amp;_p]:mb-2">x</div>`},
		{"text that only looks like a class attribute is left alone",
			`<p>write class=&#34;[x]&#34; to style it</p>`,
			`<p>write class=&#34;[x]&#34; to style it</p>`},
	}
	for _, c := range cases {
		if got := dropArbitraryClasses(c.in, vouched); got != c.want {
			t.Errorf("%s:\n got %s\nwant %s", c.name, got, c.want)
		}
	}
}

// The policy's own output is what dropArbitraryClasses is written
// against: a hostile class survives the policy, and does not survive the
// two together.
func TestEditorPolicyThenClassFilter(t *testing.T) {
	in := `<p class='[background:url(//evil/x)]  font-bold' onclick="x()">hi</p>`
	got := dropArbitraryClasses(editorHTMLPolicy.Sanitize(in), nil)
	if want := `<p class="font-bold">hi</p>`; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}
