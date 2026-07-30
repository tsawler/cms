// Command cms generates a new website built on the cms module.
//
// From an empty directory:
//
//	go run github.com/tsawler/cms/cmd/cms@latest init mysite
//	cd mysite
//	docker compose up -d
//	go mod tidy
//	go generate .
//	go run .
//
// Or install it once and use it anywhere:
//
//	go install github.com/tsawler/cms/cmd/cms@latest
//	cms init mysite
//
// init never overwrites a file that already exists unless -force is given,
// so it is safe to re-run inside a project that has moved on — a second
// run picks up starter files the first one did not write.
//
// This command deliberately depends on nothing but the standard library
// and the scaffold package, so that fetching it with @latest does not pull
// down the database drivers, the AWS SDK, and testcontainers along the way.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"slices"
	"strings"

	"github.com/tsawler/cms/scaffold"
)

const modulePath = "github.com/tsawler/cms"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "cms:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		usage()
		return errors.New("no command given")
	}
	switch args[0] {
	case "init":
		return initCmd(args[1:])
	case "version":
		fmt.Println("cms", cmp(version(), "(devel)"))
		return nil
	case "help", "-h", "--help":
		usage()
		return nil
	default:
		usage()
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `cms generates a website built on `+modulePath+`.

Usage:
    cms init [flags] [directory]    write the starter files (default ".")
    cms version                     print the version of this tool
    cms help                        show this message

Run "cms init -h" for the init flags.
`)
}

func initCmd(args []string) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `Usage: cms init [flags] [directory]

Writes a runnable starter site — main.go, page templates, .env, and a
docker-compose.yml — into the given directory, creating it and its go.mod
if they do not exist. Existing files are left alone unless -force is given.

Flags:
`)
		fs.PrintDefaults()
	}

	var (
		name     = fs.String("name", "", "site name for the title and header (default: the directory name)")
		db       = fs.String("db", string(scaffold.Postgres), "database engine: "+engineList())
		module   = fs.String("module", "", "module path for a new go.mod (default: the directory name)")
		replace  = fs.String("replace", "", "add a replace directive pointing at a local checkout of the cms module")
		blog     = fs.Bool("blog", true, "include the blog, news, and post templates")
		tailwind = fs.Bool("tailwind", true, "include the Tailwind stylesheet build")
		captcha  = fs.Bool("captcha", false, "add the Cap CAPTCHA services to docker-compose.yml")
		force    = fs.Bool("force", false, "overwrite files that already exist")
		dryRun   = fs.Bool("n", false, "show what would be written without writing it")
		tidy     = fs.Bool("tidy", false, "run go mod tidy when finished")
	)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 1 {
		return fmt.Errorf("expected at most one directory, got %d", fs.NArg())
	}

	dir := "."
	if fs.NArg() == 1 {
		dir = fs.Arg(0)
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}

	engine := scaffold.Engine(*db)
	if !validEngine(engine) {
		return fmt.Errorf("unknown -db %q: want one of %s", *db, engineList())
	}

	// The go.mod has to exist before go mod edit can touch it, and the
	// directory before go mod init can run in it.
	if !*dryRun {
		if err := os.MkdirAll(abs, 0o755); err != nil {
			return err
		}
	}
	createdGoMod, err := ensureGoMod(abs, *module, *dryRun)
	if err != nil {
		return err
	}

	results, err := scaffold.Write(abs, scaffold.Options{
		SiteName: *name,
		Engine:   engine,
		Blog:     *blog,
		Tailwind: *tailwind,
		Captcha:  *captcha,
		Force:    *force,
		DryRun:   *dryRun,
	})
	for _, r := range results {
		switch r.Status {
		case scaffold.Skipped:
			fmt.Printf("  %-8s %s (exists)\n", r.Status, r.Path)
		default:
			fmt.Printf("  %-8s %s\n", r.Status, r.Path)
		}
	}
	if err != nil {
		return err
	}

	if !*dryRun {
		if err := wireModule(abs, *replace, createdGoMod); err != nil {
			return err
		}
		if *tidy {
			fmt.Println("\nrunning go mod tidy")
			if err := goCmd(abs, "mod", "tidy"); err != nil {
				return err
			}
		}
	}

	printNextSteps(dir, *tailwind, *tidy, *dryRun)
	return nil
}

// ensureGoMod creates a go.mod if the directory has none, defaulting the
// module path to the directory name. It reports whether it created one, so
// wireModule knows if it may set the cms requirement without stepping on a
// version the project already chose.
func ensureGoMod(dir, module string, dryRun bool) (bool, error) {
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
		return false, nil
	} else if !os.IsNotExist(err) {
		return false, err
	}

	if module == "" {
		module = filepath.Base(dir)
	}
	if dryRun {
		fmt.Printf("  %-8s go.mod (module %s)\n", scaffold.Created, module)
		return false, nil
	}
	if err := goCmd(dir, "mod", "init", module); err != nil {
		return false, fmt.Errorf("go mod init %s: %w", module, err)
	}
	fmt.Printf("  %-8s go.mod (module %s)\n", scaffold.Created, module)
	return true, nil
}

// wireModule points the new project's go.mod at the right copy of the cms
// module.
//
// With -replace it resolves to a local checkout, which is how you work
// against an unpublished or unreleased module: the require line needs a
// version even so, and the replace directive is what actually satisfies it.
//
// Without it, and only in a go.mod this command just created, the
// requirement is pinned to the version of the cms module this very binary
// was built from. That keeps the generated main.go and the library it
// compiles against in step — the alternative is generating code from
// @latest and letting go mod tidy resolve something else.
func wireModule(dir, replace string, createdGoMod bool) error {
	var edits []string

	if replace != "" {
		local, err := filepath.Abs(replace)
		if err != nil {
			return err
		}
		if _, err := os.Stat(filepath.Join(local, "go.mod")); err != nil {
			return fmt.Errorf("-replace %s: no go.mod there", replace)
		}
		edits = append(edits,
			"-require="+modulePath+"@v0.0.0",
			"-replace="+modulePath+"="+local,
		)
	} else if createdGoMod {
		v := version()
		if v == "" {
			// Built from a source checkout rather than fetched, so there is
			// no version to pin. go mod tidy will pick the latest.
			return nil
		}
		edits = append(edits, "-require="+modulePath+"@"+v)
	}

	if len(edits) == 0 {
		return nil
	}
	if err := goCmd(dir, append([]string{"mod", "edit"}, edits...)...); err != nil {
		return fmt.Errorf("go mod edit: %w", err)
	}
	for _, e := range edits {
		fmt.Printf("  %-8s go.mod (%s)\n", "edit", strings.TrimPrefix(e, "-"))
	}
	return nil
}

func printNextSteps(dir string, tailwind, tidied, dryRun bool) {
	if dryRun {
		fmt.Println("\nnothing written (-n)")
		return
	}

	fmt.Println("\nNext:")
	if dir != "." {
		fmt.Printf("    cd %s\n", dir)
	}
	fmt.Println("    docker compose up -d      # start the database")
	if !tidied {
		fmt.Println("    go mod tidy")
	}
	if tailwind {
		fmt.Println("    go generate .             # compile static/site.css")
	}
	fmt.Println("    go run .")
	fmt.Println("\nThen sign in at http://localhost:4000/admin/ with the credentials in .env.")
	if tailwind {
		fmt.Println("go generate needs the Tailwind CLI: brew install tailwindcss")
	}
}

// version reports the cms module version this binary was built from, or ""
// when that is not a released version — which is the case for `go run
// ./cmd/cms` inside a source checkout, where Main.Version is "(devel)".
func version() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	v := info.Main.Version
	if v == "" || v == "(devel)" {
		return ""
	}
	return v
}

func goCmd(dir string, args ...string) error {
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func validEngine(e scaffold.Engine) bool {
	return slices.Contains(scaffold.Engines(), e)
}

func engineList() string {
	names := make([]string, 0, len(scaffold.Engines()))
	for _, e := range scaffold.Engines() {
		names = append(names, string(e))
	}
	return strings.Join(names, ", ")
}

func cmp(v, fallback string) string {
	if v != "" {
		return v
	}
	return fallback
}
