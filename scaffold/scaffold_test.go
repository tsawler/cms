package scaffold

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/tsawler/cms/render"
)

// TestManifestCoversEmbeddedFiles pins the two halves together: a starter
// file added under files/ but not listed in the manifest would be embedded
// into the binary and never written, and a manifest entry naming a file
// that does not exist would fail only when someone picked the option that
// selects it.
func TestManifestCoversEmbeddedFiles(t *testing.T) {
	embedded, err := embeddedFiles()
	if err != nil {
		t.Fatal(err)
	}

	inManifest := make(map[string]int, len(manifest))
	for _, f := range manifest {
		inManifest[f.src]++
	}

	for _, src := range embedded {
		switch inManifest[src] {
		case 1:
		case 0:
			t.Errorf("files/%s is embedded but not in the manifest, so it is never written", src)
		default:
			t.Errorf("files/%s appears %d times in the manifest", src, inManifest[src])
		}
	}
	for src := range inManifest {
		if _, err := starter.ReadFile("files/" + src); err != nil {
			t.Errorf("manifest names %s, which is not embedded: %v", src, err)
		}
	}
}

// TestWriteRendersEveryCombination executes every starter file under every
// combination of options and engines. Rendering a .go file also parses it
// through go/format, so a template that produces invalid Go fails here.
func TestWriteRendersEveryCombination(t *testing.T) {
	for _, engine := range Engines() {
		for _, blog := range []bool{false, true} {
			for _, tailwind := range []bool{false, true} {
				for _, captcha := range []bool{false, true} {
					opts := Options{
						Engine:   engine,
						Blog:     blog,
						Tailwind: tailwind,
						Captcha:  captcha,
					}
					name := string(engine)
					for _, f := range []struct {
						on bool
						s  string
					}{{blog, "blog"}, {tailwind, "tailwind"}, {captcha, "captcha"}} {
						if f.on {
							name += "+" + f.s
						}
					}

					t.Run(name, func(t *testing.T) {
						dir := t.TempDir()
						results, err := Write(dir, opts)
						if err != nil {
							t.Fatal(err)
						}
						for _, r := range results {
							if r.Status != Created {
								t.Errorf("%s: got %s, want create in an empty directory", r.Path, r.Status)
							}
							if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(r.Path))); err != nil {
								t.Errorf("%s reported as %s but is not on disk: %v", r.Path, r.Status, err)
							}
						}

						// Options must actually select files.
						has := func(p string) bool {
							_, err := os.Stat(filepath.Join(dir, filepath.FromSlash(p)))
							return err == nil
						}
						if got := has("templates/pages/post.gohtml"); got != blog {
							t.Errorf("post.gohtml present = %v, want %v", got, blog)
						}
						if got := has("assets/input.css"); got != tailwind {
							t.Errorf("input.css present = %v, want %v", got, tailwind)
						}

						main := read(t, dir, "main.go")
						if blog != strings.Contains(main, "cfg.PostTemplate") {
							t.Errorf("main.go sets PostTemplate = %v, want %v", !blog, blog)
						}
						wantDialect := engines[engine].dialect != "postgres"
						if got := strings.Contains(main, "cfg.Dialect"); got != wantDialect {
							t.Errorf("main.go sets Dialect = %v, want %v", got, wantDialect)
						}
						if want := engines[engine].driverImport; !strings.Contains(main, want) {
							t.Errorf("main.go does not import the %s driver %q", engine, want)
						}

						compose := read(t, dir, "docker-compose.yml")
						if captcha != strings.Contains(compose, "valkey:") {
							t.Errorf("docker-compose.yml has valkey = %v, want %v", !captcha, captcha)
						}
						if !strings.Contains(compose, string(engine)+":") {
							t.Errorf("docker-compose.yml declares no %s service", engine)
						}
					})
				}
			}
		}
	}
}

// TestWriteLeavesExistingFilesAlone covers the case that matters most for
// re-running init inside a project that has already been edited.
func TestWriteLeavesExistingFilesAlone(t *testing.T) {
	dir := t.TempDir()
	opts := Options{Blog: true, Tailwind: true}
	if _, err := Write(dir, opts); err != nil {
		t.Fatal(err)
	}

	edited := "// hand-edited\npackage main\n"
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}

	results, err := Write(dir, opts)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range results {
		if r.Status != Skipped {
			t.Errorf("%s: got %s on a second run, want skip", r.Path, r.Status)
		}
	}
	if got := read(t, dir, "main.go"); got != edited {
		t.Error("main.go was overwritten by a second run without -force")
	}

	results, err = Write(dir, Options{Blog: true, Tailwind: true, Force: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range results {
		if r.Status != Replaced {
			t.Errorf("%s: got %s with Force, want replace", r.Path, r.Status)
		}
	}
	if got := read(t, dir, "main.go"); got == edited {
		t.Error("main.go survived a run with Force")
	}
}

func TestWriteDryRunTouchesNothing(t *testing.T) {
	dir := t.TempDir()
	results, err := Write(dir, Options{Blog: true, Tailwind: true, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Fatal("dry run reported no files")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("dry run wrote %d entries", len(entries))
	}
}

func TestWriteRejectsUnknownEngine(t *testing.T) {
	if _, err := Write(t.TempDir(), Options{Engine: "sqlite"}); err == nil {
		t.Fatal("want an error for an unknown engine")
	}
}

func TestScriptIsExecutable(t *testing.T) {
	dir := t.TempDir()
	if _, err := Write(dir, Options{Tailwind: true}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, "tailwind-content.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&0o111 == 0 {
		t.Errorf("tailwind-content.sh mode is %v, want the execute bit set", info.Mode())
	}
}

// TestDSNPortMatchesComposePort pins the generated DSN to the port the
// generated compose file publishes. They live in different files — the
// engines table here, the service definition in the template — so moving
// one and not the other produces a project that starts its database
// happily and then fails to connect to it, which reads as a broken
// scaffold rather than a one-character mistake.
func TestDSNPortMatchesComposePort(t *testing.T) {
	// hostPort pulls the host side out of a "HOST:CONTAINER" mapping.
	publishedRe := regexp.MustCompile(`-\s*"(\d+):\d+"`)

	for _, engine := range Engines() {
		t.Run(string(engine), func(t *testing.T) {
			dir := t.TempDir()
			if _, err := Write(dir, Options{Engine: engine}); err != nil {
				t.Fatal(err)
			}

			// The DSN reaches .env and main.go from the same field, so
			// checking the rendered .env covers both.
			env := read(t, dir, ".env")
			// postgres://…@localhost:5432/… and …@tcp(localhost:3307)/…
			// both put the port right after "localhost:".
			dsnPort := regexp.MustCompile(`localhost:(\d+)`).FindStringSubmatch(env)
			if dsnPort == nil {
				t.Fatalf(".env carries no recognizable DSN port:\n%s", env)
			}

			var ports []string
			for _, m := range publishedRe.FindAllStringSubmatch(read(t, dir, "docker-compose.yml"), -1) {
				ports = append(ports, m[1])
			}
			if !slices.Contains(ports, dsnPort[1]) {
				t.Errorf("DSN connects to port %s, but docker-compose.yml publishes %v", dsnPort[1], ports)
			}
		})
	}
}

// TestBothBuildsShareTheme guards the one thing about the generated
// styling setup that fails silently. The site build and the CMS's
// content build are separate Tailwind runs, and {{cmsHead}} links the
// content stylesheet last — so if only one of them imports theme.css,
// the other's stock theme wins and a customized token (a redefined
// --font-sans, say) is reset site-wide with nothing logged and no build
// error. The symptom is "my font works locally and not in production",
// which is a miserable thing to debug, so pin it here instead.
func TestBothBuildsShareTheme(t *testing.T) {
	dir := t.TempDir()
	if _, err := Write(dir, Options{Tailwind: true}); err != nil {
		t.Fatal(err)
	}

	theme := read(t, dir, "assets/theme.css")
	if !strings.Contains(theme, "@theme") {
		t.Error("assets/theme.css declares no @theme block")
	}
	for _, token := range []string{"--font-body", "--font-display"} {
		if !strings.Contains(theme, token) {
			t.Errorf("assets/theme.css does not define %s", token)
		}
	}

	// Both builds must pull the same file in.
	for _, f := range []string{"assets/input.css", "tailwind-content.sh"} {
		if got := read(t, dir, f); !strings.Contains(got, `@import "./theme.css"`) {
			t.Errorf(`%s does not @import "./theme.css", so the two Tailwind builds can disagree on the theme`, f)
		}
	}

	// The content build runs from a scratch directory, so importing the
	// theme is only half of it — the file has to be copied in beside the
	// input, or the import resolves to nothing.
	if got := read(t, dir, "tailwind-content.sh"); !strings.Contains(got, "cp assets/theme.css") {
		t.Error("tailwind-content.sh imports theme.css but never copies it into the scratch directory")
	}

	// The tokens are useless if nothing consumes them.
	css := read(t, dir, "assets/input.css")
	for _, use := range []string{"var(--font-body)", "var(--font-display)"} {
		if !strings.Contains(css, use) {
			t.Errorf("assets/input.css defines no rule using %s", use)
		}
	}
}

func TestSiteNameDefaultsToDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "my-cool_site")
	if _, err := Write(dir, Options{}); err != nil {
		t.Fatal(err)
	}
	if got := read(t, dir, "templates/base.gohtml"); !strings.Contains(got, "My Cool Site") {
		t.Error(`base.gohtml does not carry the derived site name "My Cool Site"`)
	}
	if got := read(t, dir, ".env"); !strings.Contains(got, "S3_KEY_PREFIX=my-cool-site") {
		t.Error(".env does not carry the derived slug")
	}
}

// TestGeneratedProjectBuilds is the test that keeps the starter main.go
// honest: it compiles the generated project against this very checkout of
// the cms module, so renaming a Config field or changing a method
// signature fails here rather than in a user's first `go run .`.
//
// It shells out to the go tool and resolves dependencies, so it is skipped
// under -short.
func TestGeneratedProjectBuilds(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the generated project with the go tool")
	}

	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}

	// One case per driver import, since that is what varies at compile
	// time; MariaDB shares MySQL's driver, so it adds no coverage here.
	cases := []struct {
		name string
		opts Options
	}{
		{"postgres-full", Options{Engine: Postgres, Blog: true, Tailwind: true, Captcha: true}},
		{"mysql-minimal", Options{Engine: MySQL}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			if _, err := Write(dir, tc.opts); err != nil {
				t.Fatal(err)
			}

			goMod := "module scaffoldtest\n\ngo 1.25.0\n\n" +
				"require github.com/tsawler/cms v0.0.0\n\n" +
				"replace github.com/tsawler/cms => " + root + "\n"
			if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goMod), 0o644); err != nil {
				t.Fatal(err)
			}

			for _, args := range [][]string{{"mod", "tidy"}, {"build", "./..."}} {
				cmd := exec.Command("go", args...)
				cmd.Dir = dir
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("go %s failed: %v\n%s", strings.Join(args, " "), err, out)
				}
			}
		})
	}
}

func read(t *testing.T, dir, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestGeneratedTemplatesParse is the runtime half of what
// TestGeneratedProjectBuilds cannot see. Page templates are parsed when
// the server first renders them, so a generated template with a broken
// action still compiles and still passes the build test — it fails in
// front of the site's first visitor instead.
//
// The site name is the thing most likely to break one, because it is
// interpolated into quoted template arguments ({{cmsBrand "…"}}, the
// {{cmsShared}} fallback). A name carrying a quote closed the argument and
// left an unparseable template; one carrying < put markup in the <title>.
// Hence a deliberately hostile name here rather than a tidy one.
func TestGeneratedTemplatesParse(t *testing.T) {
	dir := t.TempDir()
	opts := Options{
		SiteName: `6" Nails \q Co & <b>`,
		Engine:   Postgres,
		Blog:     true, // writes the most template files
		Tailwind: true,
	}
	if _, err := Write(dir, opts); err != nil {
		t.Fatal(err)
	}

	checked := 0
	root := filepath.Join(dir, "templates")
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".gohtml") {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		checked++
		rel, _ := filepath.Rel(dir, p)
		if err := render.CheckTemplate(d.Name(), string(b)); err != nil {
			t.Errorf("%s does not parse, so the generated site fails on its first request: %v", rel, err)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
	if checked == 0 {
		t.Fatal("no .gohtml templates were generated, so this test proved nothing")
	}
	t.Logf("parsed %d generated templates", checked)

	// Parsing is necessary but not sufficient: the escaping has to suit
	// each context, and the wrong one still parses. {{cmsBrand}} escapes
	// its own output, so it takes the name plain — handed an HTML-escaped
	// one it renders the entities at the top of every page.
	base := read(t, dir, "templates/base.gohtml")
	if want := `{{cmsBrand "6\" Nails \\q Co & <b>"}}`; !strings.Contains(base, want) {
		t.Errorf("cmsBrand is not passed the plain name.\n got: %s\nwant to contain: %s",
			firstLineWith(base, "cmsBrand"), want)
	}
	// The {{cmsShared}} fallback is the opposite: the renderer emits it as
	// raw HTML, so it has to arrive escaped.
	if want := "&#92;q Co &amp; &lt;b&gt;"; !strings.Contains(base, want) {
		t.Errorf("the footer fallback is not HTML-escaped; want it to contain %s", want)
	}
}

// firstLineWith returns the first line of s containing sub, for error
// messages that would otherwise print a whole template.
func firstLineWith(s, sub string) string {
	for _, line := range strings.Split(s, "\n") {
		if strings.Contains(line, sub) {
			return strings.TrimSpace(line)
		}
	}
	return "(no matching line)"
}
