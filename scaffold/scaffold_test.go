package scaffold

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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
