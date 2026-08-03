package editor

import (
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func TestVersion(t *testing.T) {
	v := Version()
	if !regexp.MustCompile(`^[0-9a-f]{16}$`).MatchString(v) {
		t.Fatalf("Version() = %q, want 16 hex characters", v)
	}
	if v2 := Version(); v2 != v {
		t.Errorf("Version() is not stable: %q then %q", v, v2)
	}
	if !looksLikeVersion(v) {
		t.Errorf("Version() %q is not recognised by looksLikeVersion", v)
	}
}

func TestScriptPathCarriesTheVersion(t *testing.T) {
	p := ScriptPath()
	if !strings.HasPrefix(p, PathPrefix) {
		t.Errorf("ScriptPath() = %q, want it under %q", p, PathPrefix)
	}
	if !strings.Contains(p, Version()) {
		t.Errorf("ScriptPath() = %q, want it to carry version %q", p, Version())
	}
	if !strings.HasSuffix(p, "/editor.js") {
		t.Errorf("ScriptPath() = %q, want it to end in /editor.js", p)
	}
}

func get(t *testing.T, path string) *http.Response {
	t.Helper()
	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec.Result()
}

// TestHandlerCaching is the point of the whole change: the versioned URL
// may be cached forever, and every other spelling may not.
func TestHandlerCaching(t *testing.T) {
	tests := []struct {
		name, path, wantCache string
		wantStatus            int
	}{
		{
			name: "current version is immutable",
			path: ScriptPath(), wantStatus: http.StatusOK,
			wantCache: "public, max-age=31536000, immutable",
		},
		{
			// HTML minted by an older build. Serving the current script
			// beats 404ing an editor who did nothing wrong.
			name: "a superseded version still works, briefly cached",
			path: PathPrefix + "0123456789abcdef/editor.js", wantStatus: http.StatusOK,
			wantCache: "public, max-age=60",
		},
		{
			name: "the unversioned path still works, briefly cached",
			path: PathPrefix + "editor.js", wantStatus: http.StatusOK,
			wantCache: "public, max-age=60",
		},
		{
			name: "tinymce under the version is immutable too",
			path: PathPrefix + Version() + "/tinymce/tinymce.min.js", wantStatus: http.StatusOK,
			wantCache: "public, max-age=31536000, immutable",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res := get(t, tc.path)
			if res.StatusCode != tc.wantStatus {
				t.Fatalf("GET %s = %d, want %d", tc.path, res.StatusCode, tc.wantStatus)
			}
			if got := res.Header.Get("Cache-Control"); got != tc.wantCache {
				t.Errorf("Cache-Control = %q, want %q", got, tc.wantCache)
			}
		})
	}
}

func TestHandlerRejectsDirectoriesAndUnknownFiles(t *testing.T) {
	if res := get(t, PathPrefix); res.StatusCode != http.StatusNotFound {
		t.Errorf("directory listing = %d, want 404", res.StatusCode)
	}
	if res := get(t, PathPrefix+Version()+"/nope.js"); res.StatusCode != http.StatusNotFound {
		t.Errorf("unknown file = %d, want 404", res.StatusCode)
	}
}

// TestHandlerServesTheSameBytesEitherWay guards the forgiving path: an
// outdated URL must return the *current* asset, not a stale one and not
// an error.
func TestHandlerServesTheSameBytesEitherWay(t *testing.T) {
	want, err := assets.ReadFile("editor.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{ScriptPath(), PathPrefix + "editor.js",
		PathPrefix + "0123456789abcdef/editor.js"} {
		res := get(t, p)
		if res.StatusCode != http.StatusOK {
			t.Fatalf("GET %s = %d", p, res.StatusCode)
		}
		got, err := io.ReadAll(res.Body)
		res.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(want) {
			t.Errorf("GET %s served %d bytes, want the current %d-byte bundle",
				p, len(got), len(want))
		}
	}
}

func TestLooksLikeVersion(t *testing.T) {
	for _, s := range []string{"0123456789abcdef", Version()} {
		if !looksLikeVersion(s) {
			t.Errorf("looksLikeVersion(%q) = false, want true", s)
		}
	}
	// "tinymce" must not be mistaken for a version, or the unversioned
	// asset paths would have their first segment eaten.
	for _, s := range []string{"tinymce", "editor.js", "", "0123456789ABCDEF",
		"0123456789abcde", "0123456789abcdefg", "0123456789abcdeg"} {
		if looksLikeVersion(s) {
			t.Errorf("looksLikeVersion(%q) = true, want false", s)
		}
	}
}
