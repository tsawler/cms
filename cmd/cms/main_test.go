package main

// The cms command. It is the first thing anyone runs — `cms init mysite`
// is step one of the README — so a break here is a break in the only
// experience a new user has had so far, and nothing downstream would
// catch it.
//
// What is tested here is the command's own work: reading the flags,
// creating and wiring the go.mod, and reporting what it did. The files
// themselves belong to the scaffold package, which tests every
// combination of them (including that the generated project compiles);
// these check that a flag reaches it, not what it renders.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsawler/cms/scaffold"
)

// capture runs fn with stdout and stderr redirected to a file and returns
// everything written. The command prints its report straight to the
// process's streams — which is right for a CLI — so reading it back means
// taking them over for the duration.
func capture(t *testing.T, fn func() error) (string, error) {
	t.Helper()

	f, err := os.CreateTemp(t.TempDir(), "cms-cli-output")
	if err != nil {
		t.Fatalf("creating the capture file: %v", err)
	}
	defer f.Close()

	stdout, stderr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = f, f
	runErr := fn()
	os.Stdout, os.Stderr = stdout, stderr

	out, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatalf("reading the captured output: %v", err)
	}
	return string(out), runErr
}

// initIn runs `cms init` against a fresh directory and returns the
// directory, the output, and whatever the command reported.
func initIn(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "mysite")
	out, err := capture(t, func() error {
		return run(append([]string{"init"}, append(args, dir)...))
	})
	return dir, out, err
}

// exists is the question most of these tests ask.
func exists(t *testing.T, path string) bool {
	t.Helper()
	_, err := os.Stat(path)
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("stat %s: %v", path, err)
	}
	return err == nil
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(body)
}

// No command at all is the bare `cms`, and it has to say what the command
// does rather than fail silently.
func TestRunWithNoCommand(t *testing.T) {
	out, err := capture(t, func() error { return run(nil) })
	if err == nil {
		t.Error("running with no command succeeded")
	}
	if !strings.Contains(out, "cms init") {
		t.Errorf("output does not show the usage: %q", out)
	}
}

func TestRunUnknownCommand(t *testing.T) {
	out, err := capture(t, func() error { return run([]string{"sprout"}) })
	if err == nil {
		t.Fatal("an unknown command succeeded")
	}
	if !strings.Contains(err.Error(), `"sprout"`) {
		t.Errorf("error = %v, want it to name the command", err)
	}
	if !strings.Contains(out, "cms init") {
		t.Errorf("output does not show the usage: %q", out)
	}
}

func TestRunHelpAndVersion(t *testing.T) {
	for _, arg := range []string{"help", "-h", "--help"} {
		out, err := capture(t, func() error { return run([]string{arg}) })
		if err != nil {
			t.Errorf("%s: %v", arg, err)
		}
		if !strings.Contains(out, "cms init") || !strings.Contains(out, "cms version") {
			t.Errorf("%s: output does not list the commands: %q", arg, out)
		}
	}

	out, err := capture(t, func() error { return run([]string{"version"}) })
	if err != nil {
		t.Fatalf("version: %v", err)
	}
	// Built from a source checkout there is no released version to
	// report, and the fallback is what says so rather than an empty line.
	if !strings.HasPrefix(out, "cms ") || strings.TrimSpace(out) == "cms" {
		t.Errorf("version printed %q, want a version or the fallback", out)
	}
}

// The happy path: an empty directory becomes a project that has
// everything the printed next steps assume.
func TestInitWritesARunnableProject(t *testing.T) {
	dir, out, err := initIn(t)
	if err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}

	for _, name := range []string{"main.go", "go.mod", ".env", "docker-compose.yml"} {
		if !exists(t, filepath.Join(dir, name)) {
			t.Errorf("%s was not written", name)
		}
	}
	// The module defaults to the directory name, which is what makes
	// `cms init mysite` work with no further arguments.
	if got := readFile(t, filepath.Join(dir, "go.mod")); !strings.Contains(got, "module mysite") {
		t.Errorf("go.mod = %q, want the module named after the directory", got)
	}

	// The report names what it wrote, and the next steps name the
	// commands that make it run.
	if !strings.Contains(out, "main.go") {
		t.Errorf("the report does not mention main.go: %q", out)
	}
	for _, step := range []string{"docker compose up", "go run ."} {
		if !strings.Contains(out, step) {
			t.Errorf("the next steps do not mention %q: %q", step, out)
		}
	}
}

// A dry run is what someone runs to see what they are about to get. It
// must not create the directory, never mind the files in it.
func TestInitDryRunWritesNothing(t *testing.T) {
	dir, out, err := initIn(t, "-n")
	if err != nil {
		t.Fatalf("init -n: %v\n%s", err, out)
	}

	if exists(t, dir) {
		t.Error("a dry run created the target directory")
	}
	if !strings.Contains(out, "nothing written") {
		t.Errorf("the report does not say nothing was written: %q", out)
	}
	// It still describes the whole plan, go.mod included.
	if !strings.Contains(out, "main.go") || !strings.Contains(out, "go.mod") {
		t.Errorf("the plan does not list the files: %q", out)
	}
}

// init is documented as safe to re-run: a second run picks up starter
// files the first one did not write, and leaves everything else alone.
// That is what makes it usable on a project that has moved on.
func TestInitLeavesExistingFilesAlone(t *testing.T) {
	dir, out, err := initIn(t)
	if err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	mainGo := filepath.Join(dir, "main.go")
	if err := os.WriteFile(mainGo, []byte("// my own work\n"), 0o644); err != nil {
		t.Fatalf("editing main.go: %v", err)
	}

	out, err = capture(t, func() error { return run([]string{"init", dir}) })
	if err != nil {
		t.Fatalf("second init: %v\n%s", err, out)
	}
	if got := readFile(t, mainGo); got != "// my own work\n" {
		t.Errorf("main.go = %q, want the edit kept", got)
	}
	// And it says so, rather than reporting a write that did not happen.
	if !strings.Contains(out, string(scaffold.Skipped)) || !strings.Contains(out, "(exists)") {
		t.Errorf("the report does not mark the skipped file: %q", out)
	}

	// -force is the way to take the starter file back.
	out, err = capture(t, func() error { return run([]string{"init", "-force", dir}) })
	if err != nil {
		t.Fatalf("forced init: %v\n%s", err, out)
	}
	if got := readFile(t, mainGo); got == "// my own work\n" {
		t.Error("-force did not overwrite main.go")
	}
}

// A go.mod already there belongs to the project, not to this command: its
// module path is the one the user chose, and re-running init must not
// reach for it.
func TestInitKeepsAnExistingGoMod(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "mysite")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	const existing = "module example.com/already/here\n\ngo 1.26.0\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(existing), 0o644); err != nil {
		t.Fatalf("writing go.mod: %v", err)
	}

	out, err := capture(t, func() error { return run([]string{"init", dir}) })
	if err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	if got := readFile(t, filepath.Join(dir, "go.mod")); got != existing {
		t.Errorf("go.mod = %q, want it untouched", got)
	}
}

func TestInitModuleFlag(t *testing.T) {
	dir, out, err := initIn(t, "-module", "example.com/kraken")
	if err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	if got := readFile(t, filepath.Join(dir, "go.mod")); !strings.Contains(got, "module example.com/kraken") {
		t.Errorf("go.mod = %q, want the configured module path", got)
	}
}

// -replace is how you work against an unpublished module, and it needs
// both halves: a require for the version and the replace that satisfies
// it. Either alone leaves a project that will not build.
func TestInitReplacePointsAtALocalCheckout(t *testing.T) {
	local, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("resolving the checkout: %v", err)
	}
	dir, out, err := initIn(t, "-replace", local)
	if err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}

	gomod := readFile(t, filepath.Join(dir, "go.mod"))
	if !strings.Contains(gomod, "require "+modulePath) && !strings.Contains(gomod, modulePath+" v0.0.0") {
		t.Errorf("go.mod = %q, want a require for the cms module", gomod)
	}
	if !strings.Contains(gomod, "replace "+modulePath+" => "+local) {
		t.Errorf("go.mod = %q, want a replace pointing at %s", gomod, local)
	}
	// The report names the edits, so a wrong path is visible immediately
	// rather than at the first build.
	if !strings.Contains(out, "replace=") {
		t.Errorf("the report does not mention the go.mod edits: %q", out)
	}
}

// A -replace path that is not a module is a typo. Saying so beats a
// go.mod that points somewhere useless.
func TestInitReplaceNeedsAGoMod(t *testing.T) {
	empty := t.TempDir()
	dir := filepath.Join(t.TempDir(), "mysite")
	out, err := capture(t, func() error {
		return run([]string{"init", "-replace", empty, dir})
	})
	if err == nil {
		t.Fatalf("init accepted a -replace with no go.mod\n%s", out)
	}
	if !strings.Contains(err.Error(), "no go.mod") {
		t.Errorf("error = %v, want it to say what is missing", err)
	}
}

// The flags this command owns are checked before anything is written, so
// a typo costs nothing.
func TestInitRejectsBadArguments(t *testing.T) {
	t.Run("unknown engine", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "mysite")
		out, err := capture(t, func() error {
			return run([]string{"init", "-db", "oracle", dir})
		})
		if err == nil {
			t.Fatalf("init accepted -db oracle\n%s", out)
		}
		// The message lists what would have worked.
		for _, engine := range scaffold.Engines() {
			if !strings.Contains(err.Error(), string(engine)) {
				t.Errorf("error = %v, want it to list %s", err, engine)
			}
		}
		if exists(t, dir) {
			t.Error("a rejected engine still created the directory")
		}
	})

	t.Run("two directories", func(t *testing.T) {
		out, err := capture(t, func() error {
			return run([]string{"init", "one", "two"})
		})
		if err == nil {
			t.Fatalf("init accepted two directories\n%s", out)
		}
	})

	t.Run("unknown flag", func(t *testing.T) {
		out, err := capture(t, func() error {
			return run([]string{"init", "-nope"})
		})
		if err == nil {
			t.Fatalf("init accepted an unknown flag\n%s", out)
		}
		if !strings.Contains(out, "Usage: cms init") {
			t.Errorf("output does not show the init usage: %q", out)
		}
	})
}

// The flags that shape the project are the command's to pass along; that
// they arrive is this test's business, and what they produce is the
// scaffold package's.
func TestInitFlagsReachTheScaffold(t *testing.T) {
	t.Run("engine", func(t *testing.T) {
		dir, out, err := initIn(t, "-db", string(scaffold.MariaDB))
		if err != nil {
			t.Fatalf("init: %v\n%s", err, out)
		}
		compose := readFile(t, filepath.Join(dir, "docker-compose.yml"))
		if !strings.Contains(compose, "mariadb") {
			t.Errorf("docker-compose.yml does not use mariadb: %q", compose)
		}
	})

	t.Run("site name", func(t *testing.T) {
		dir, out, err := initIn(t, "-name", "The Kraken Chronicle")
		if err != nil {
			t.Fatalf("init: %v\n%s", err, out)
		}
		if got := readFile(t, filepath.Join(dir, "main.go")); !strings.Contains(got, "The Kraken Chronicle") {
			t.Errorf("main.go does not carry the site name: %q", got)
		}
	})

	// The two flags that drop whole groups of files. Each names one file
	// only that group brings, so the check cannot pass by accident.
	for _, c := range []struct {
		flag string
		file string
	}{
		{"-blog=false", filepath.Join("templates", "pages", "post.gohtml")},
		{"-tailwind=false", "gen.go"},
	} {
		t.Run(c.flag, func(t *testing.T) {
			on, out, err := initIn(t)
			if err != nil {
				t.Fatalf("init: %v\n%s", err, out)
			}
			if !exists(t, filepath.Join(on, c.file)) {
				t.Fatalf("%s is not written by default — the check below would pass either way", c.file)
			}

			off, out, err := initIn(t, c.flag)
			if err != nil {
				t.Fatalf("init %s: %v\n%s", c.flag, err, out)
			}
			if exists(t, filepath.Join(off, c.file)) {
				t.Errorf("%s wrote %s anyway", c.flag, c.file)
			}
		})
	}

	// The Tailwind step is offered in the next steps only when there is
	// something to generate.
	t.Run("tailwind changes the next steps", func(t *testing.T) {
		_, on, err := initIn(t)
		if err != nil {
			t.Fatalf("init: %v\n%s", err, on)
		}
		if !strings.Contains(on, "go generate") {
			t.Errorf("the next steps do not offer go generate with Tailwind on: %q", on)
		}

		_, off, err := initIn(t, "-tailwind=false")
		if err != nil {
			t.Fatalf("init -tailwind=false: %v\n%s", err, off)
		}
		if strings.Contains(off, "go generate") {
			t.Errorf("the next steps offer go generate with Tailwind off: %q", off)
		}
	})
}

// "." is the documented default, and it is the form the README's second
// example uses — running init inside a directory you are already in.
func TestInitDefaultsToTheCurrentDirectory(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	out, err := capture(t, func() error { return run([]string{"init"}) })
	if err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	if !exists(t, filepath.Join(dir, "main.go")) {
		t.Error("init . wrote nothing into the working directory")
	}
	// No "cd" step, because there is nowhere to go.
	if strings.Contains(out, "cd .") {
		t.Errorf("the next steps tell the reader to cd into \".\": %q", out)
	}
}

func TestEngineHelpers(t *testing.T) {
	for _, e := range scaffold.Engines() {
		if !validEngine(e) {
			t.Errorf("validEngine(%q) = false, want true", e)
		}
		if !strings.Contains(engineList(), string(e)) {
			t.Errorf("engineList() = %q, want it to list %q", engineList(), e)
		}
	}
	for _, e := range []scaffold.Engine{"", "oracle", "POSTGRES"} {
		if validEngine(e) {
			t.Errorf("validEngine(%q) = true, want false", e)
		}
	}
}

// version() is empty for a binary built from a source checkout, and the
// fallback is what keeps `cms version` from printing a blank line.
func TestCmpFallback(t *testing.T) {
	if got := cmp("", "(devel)"); got != "(devel)" {
		t.Errorf("cmp(\"\", fallback) = %q, want the fallback", got)
	}
	if got := cmp("v1.2.3", "(devel)"); got != "v1.2.3" {
		t.Errorf("cmp(value, fallback) = %q, want the value", got)
	}
}
