package admin

import "testing"

const bootstrapJS = "https://cdn.jsdelivr.net/npm/bootstrap@5.3.8/dist/js/bootstrap.min.js"
const bootstrapCSS = "https://cdn.jsdelivr.net/npm/bootstrap@5.3.8/dist/css/bootstrap.min.css"

func TestExtractEmbedLinesScript(t *testing.T) {
	tests := []struct {
		name  string
		code  string
		want  string
		wants []string
	}{
		{"bare CDN snippet",
			`<script src="` + bootstrapJS + `"></script>`,
			"", []string{bootstrapJS}},
		{"single quotes and attributes",
			`<script defer src='/js/app.js' crossorigin="anonymous"></script>`,
			"", []string{"/js/app.js"}},
		{"tag line among code",
			"console.log(1)\n<script src=\"/a.js\"></script>\nconsole.log(2)",
			"console.log(1)\nconsole.log(2)", []string{"/a.js"}},
		{"tag inside a string stays code",
			`document.write('<script src="/a.js"></script>')`,
			`document.write('<script src="/a.js"></script>')`, nil},
		{"inline script stays code",
			"<script>\nconsole.log(1)\n</script>",
			"<script>\nconsole.log(1)\n</script>", nil},
		{"plain code untouched",
			"var a = 1 < 2;", "var a = 1 < 2;", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, urls := extractEmbedLines(tt.code, scriptSrcURL)
			if code != tt.want {
				t.Errorf("code = %q, want %q", code, tt.want)
			}
			if len(urls) != len(tt.wants) {
				t.Fatalf("urls = %v, want %v", urls, tt.wants)
			}
			for i := range urls {
				if urls[i] != tt.wants[i] {
					t.Errorf("urls[%d] = %q, want %q", i, urls[i], tt.wants[i])
				}
			}
		})
	}
}

func TestExtractEmbedLinesStylesheet(t *testing.T) {
	code, urls := extractEmbedLines(
		`<link href="`+bootstrapCSS+`" rel="stylesheet" integrity="sha384-x" crossorigin="anonymous">`,
		stylesheetURL)
	if code != "" || len(urls) != 1 || urls[0] != bootstrapCSS {
		t.Errorf("got code %q urls %v", code, urls)
	}

	// Non-stylesheet link tags are not resource links.
	pre := `<link rel="preconnect" href="https://fonts.googleapis.com">`
	code, urls = extractEmbedLines(pre, stylesheetURL)
	if code != pre || urls != nil {
		t.Errorf("preconnect: got code %q urls %v", code, urls)
	}
}

func TestCleanResourceLinksAcceptsEmbedTags(t *testing.T) {
	raw := `<script src="` + bootstrapJS + `"></script>` + "\n" +
		`<link rel="stylesheet" href="/site.css">` + "\n" +
		"/plain.js\nnot a url\njavascript:alert(1)"
	want := bootstrapJS + "\n/site.css\n/plain.js"
	if got := cleanResourceLinks(raw); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestCleanResourceLinksDedupes(t *testing.T) {
	raw := "/a.js\n" + `<script src="/a.js"></script>` + "\n/b.js\n/a.js"
	if got := cleanResourceLinks(raw); got != "/a.js\n/b.js" {
		t.Errorf("got %q", got)
	}
}

func TestJoinLinkLines(t *testing.T) {
	if got := joinLinkLines("/a.js", []string{"/b.js"}); got != "/a.js\n/b.js" {
		t.Errorf("append: got %q", got)
	}
	if got := joinLinkLines("", []string{"/b.js"}); got != "/b.js" {
		t.Errorf("empty raw: got %q", got)
	}
	if got := joinLinkLines("/a.js", nil); got != "/a.js" {
		t.Errorf("no extra: got %q", got)
	}
}
