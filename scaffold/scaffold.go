// Package scaffold writes the starter files for a website built on the cms
// module — main.go, page templates, .env, docker-compose.yml — into a
// directory. It is what "cms init" runs; importing it directly lets a host
// application build its own generator on top.
//
// Nothing here imports the cms module itself, and nothing here imports
// anything outside the standard library. That is deliberate: the command
// wrapping this package is fetched with
//
//	go run github.com/tsawler/cms/cmd/cms@latest
//
// which resolves the whole module graph. Keeping this package on the
// standard library alone keeps that a small download rather than one that
// drags in the database drivers, the AWS SDK, and testcontainers.
//
// The embedded starter files are Go templates using [[ and ]] as
// delimiters, because most of what they contain is itself Go template
// source written in {{ and }}.
package scaffold

import (
	"bytes"
	"embed"
	"fmt"
	"go/format"
	"html"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"
	"unicode"
)

//go:embed files
var starter embed.FS

// Engine is a database engine the generated site can target.
type Engine string

// The supported engines. All three are first-class — the same store tests
// run against each — so the choice is an operational one.
const (
	Postgres Engine = "postgres"
	MySQL    Engine = "mysql"
	MariaDB  Engine = "mariadb"
)

// engineInfo carries everything that differs between engines: what
// main.go imports, the DSN it falls back to, and the service
// docker-compose.yml declares.
type engineInfo struct {
	label        string
	dialect      string // cms.Config.Dialect
	driver       string // database/sql driver name
	driverImport string
	dsn          string
}

var engines = map[Engine]engineInfo{
	Postgres: {
		label:        "PostgreSQL",
		dialect:      "postgres",
		driver:       "pgx",
		driverImport: "github.com/jackc/pgx/v5/stdlib",
		dsn:          "postgres://cms:cms@localhost:5432/cms?sslmode=disable",
	},
	// parseTime and loc make timestamps scan into time.Time as UTC,
	// time_zone makes the server session agree with them, and
	// clientFoundRows makes an UPDATE report rows matched rather than rows
	// changed. The CMS needs all four; each fails subtly on its own.
	MySQL: {
		label:        "MySQL",
		dialect:      "mysql",
		driver:       "mysql",
		driverImport: "github.com/go-sql-driver/mysql",
		dsn: "cms:cms@tcp(localhost:3307)/cms" +
			"?parseTime=true&loc=UTC&time_zone=%27%2B00%3A00%27&clientFoundRows=true",
	},
	MariaDB: {
		label:        "MariaDB",
		dialect:      "mysql",
		driver:       "mysql",
		driverImport: "github.com/go-sql-driver/mysql",
		dsn: "cms:cms@tcp(localhost:3308)/cms" +
			"?parseTime=true&loc=UTC&time_zone=%27%2B00%3A00%27&clientFoundRows=true",
	},
}

// Engines lists the supported engines in a stable order, for help text.
func Engines() []Engine { return []Engine{Postgres, MySQL, MariaDB} }

// Options controls what Write generates. The zero value is valid and
// produces the smallest useful site: Postgres, no blog, no Tailwind, no
// CAPTCHA. The cms init command turns Blog and Tailwind on by default.
type Options struct {
	// SiteName is the human-readable name used in the page title, the
	// brand in the header, and the generated README. Defaults to a
	// title-cased form of the target directory's name.
	SiteName string

	// Engine is the database the generated main.go and docker-compose.yml
	// target. Defaults to Postgres.
	Engine Engine

	// Blog adds the blog listing, news listing, and post templates, and
	// sets Config.PostTemplate in the generated main.go. Without it, blog
	// and news are disabled and the admin area does not offer them.
	Blog bool

	// Tailwind adds assets/input.css, assets/theme.css, gen.go, and
	// tailwind-content.sh, and wires CMS_TAILWIND_COMMAND in .env so
	// classes typed into content get compiled CSS without a redeploy.
	// Both builds import theme.css, which is what keeps a customized theme
	// from being reset by the content stylesheet. Without it the generated
	// templates still carry their Tailwind classes — you supply
	// static/site.css however you like.
	Tailwind bool

	// Captcha adds the Cap and Valkey services to docker-compose.yml for
	// the login CAPTCHA. The CAP_* variables in .env stay commented out
	// either way: the CMS runs without a CAPTCHA until they are set.
	Captcha bool

	// Force overwrites files that already exist. Without it they are left
	// alone and reported as Skipped, which makes Write safe to re-run in a
	// project that has already diverged.
	Force bool

	// DryRun reports what would be written without touching the disk.
	DryRun bool
}

// Status is what Write did, or would have done, with one file.
type Status string

const (
	Created  Status = "create"  // the file did not exist
	Replaced Status = "replace" // it existed and Force was set
	Skipped  Status = "skip"    // it existed and Force was not set
)

// Result records the disposition of a single file. Path is relative to the
// directory passed to Write.
type Result struct {
	Path   string
	Status Status
}

// file is one entry in the manifest below.
type file struct {
	src  string      // path within the embedded FS, below "files/"
	dst  string      // path within the target directory
	mode os.FileMode // 0o755 for scripts, 0o600 for anything holding secrets
	when func(Options) bool
}

func always(Options) bool     { return true }
func ifBlog(o Options) bool   { return o.Blog }
func ifStyles(o Options) bool { return o.Tailwind }

// manifest is the complete list of generated files. Every file embedded
// under files/ must appear here exactly once; TestManifestCoversEmbeddedFiles
// enforces it, so a new starter file cannot be added and silently not
// generated.
var manifest = []file{
	{src: "main.go.tmpl", dst: "main.go", mode: 0o644, when: always},
	{src: "gitignore.tmpl", dst: ".gitignore", mode: 0o644, when: always},
	// .env holds the initial admin password and, once filled in, the S3
	// and CAPTCHA secrets.
	{src: "env.tmpl", dst: ".env", mode: 0o600, when: always},
	{src: "docker-compose.yml.tmpl", dst: "docker-compose.yml", mode: 0o644, when: always},
	{src: "README.md.tmpl", dst: "README.md", mode: 0o644, when: always},

	{src: "gen.go.tmpl", dst: "gen.go", mode: 0o644, when: ifStyles},
	{src: "tailwind-content.sh.tmpl", dst: "tailwind-content.sh", mode: 0o755, when: ifStyles},
	{src: "assets/input.css.tmpl", dst: "assets/input.css", mode: 0o644, when: ifStyles},
	// The theme both Tailwind builds import — see TestBothBuildsShareTheme.
	{src: "assets/theme.css.tmpl", dst: "assets/theme.css", mode: 0o644, when: ifStyles},
	// static/site.css is a build artifact and gitignored, so the directory
	// would not survive a clone without this.
	{src: "gitkeep.tmpl", dst: "static/.gitkeep", mode: 0o644, when: ifStyles},

	{src: "templates/base.gohtml.tmpl", dst: "templates/base.gohtml", mode: 0o644, when: always},
	{src: "templates/pages/home.gohtml.tmpl", dst: "templates/pages/home.gohtml", mode: 0o644, when: always},
	{src: "templates/pages/standard.gohtml.tmpl", dst: "templates/pages/standard.gohtml", mode: 0o644, when: always},
	{src: "templates/pages/canvas.gohtml.tmpl", dst: "templates/pages/canvas.gohtml", mode: 0o644, when: always},

	{src: "templates/pages/blog.gohtml.tmpl", dst: "templates/pages/blog.gohtml", mode: 0o644, when: ifBlog},
	{src: "templates/pages/news.gohtml.tmpl", dst: "templates/pages/news.gohtml", mode: 0o644, when: ifBlog},
	{src: "templates/pages/post.gohtml.tmpl", dst: "templates/pages/post.gohtml", mode: 0o644, when: ifBlog},
}

// templateData is the dot the starter files render against. Its fields are
// referenced by name in files/*.tmpl, so renaming one is a breaking change
// to those templates — TestGeneratedProjectMatchesExample catches it.
type templateData struct {
	SiteName string
	// SiteNameHTML is SiteName escaped as HTML, for the places a template
	// puts it into markup directly: the {{cmsShared}} footer fallback,
	// whose argument the renderer emits as raw HTML.
	// Escaping also removes the quote that would otherwise close the
	// template argument early, leaving a template that compiles but fails
	// to parse when the first request renders it.
	//
	// The backslash is escaped as an entity for the same reason, since
	// HTML escaping leaves it alone: inside the footer's quoted argument a
	// site named A \q B is an invalid Go escape sequence and fails the
	// same way. As an entity it survives both contexts and still renders
	// as a backslash.
	SiteNameHTML string
	// SiteNameArg is SiteName as a complete Go-quoted string literal —
	// quotes included, so the template writes {{cmsBrand [[.SiteNameArg]]}}
	// rather than wrapping it. It is for funcs whose output is escaped
	// on the way out — {{cmsBrand}} escapes its own, {{cmsSiteName}}
	// returns plain text for html/template to escape: handing either an
	// HTML-escaped name renders the entities instead of the name.
	SiteNameArg string
	Program     string
	Slug        string
	Engine      string
	EngineLabel string
	Dialect     string
	Driver      string

	DriverImport string
	DSN          string

	// DSNNeedsParams marks the engines whose DSN carries the four required
	// query parameters, so main.go can explain them.
	DSNNeedsParams bool
	// NeedsDialect is false for Postgres, which is Config.Dialect's default
	// and so needs no assignment at all in the generated main.go.
	NeedsDialect bool

	Blog     bool
	Tailwind bool
	Captcha  bool
}

// htmlLiteral escapes a value for HTML that is also sitting inside a Go
// template's quoted argument — the footer's {{cmsShared}} fallback is both
// at once. HTML escaping alone leaves the backslash, which the template
// parser then reads as the start of an escape sequence, so it becomes an
// entity too.
func htmlLiteral(s string) string {
	return strings.ReplaceAll(html.EscapeString(s), `\`, "&#92;")
}

// Write generates the starter files into dir, creating it if necessary,
// and returns what it did with each one in manifest order. Existing files
// are left alone unless Options.Force is set.
//
// On error the results so far are still returned, so a caller can report
// the files that were written before the failure.
func Write(dir string, opts Options) ([]Result, error) {
	if opts.Engine == "" {
		opts.Engine = Postgres
	}
	info, ok := engines[opts.Engine]
	if !ok {
		return nil, fmt.Errorf("scaffold: unknown engine %q", opts.Engine)
	}

	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	program := filepath.Base(abs)

	siteName := cmp(opts.SiteName, titleCase(program))
	data := templateData{
		SiteName:       siteName,
		SiteNameHTML:   htmlLiteral(siteName),
		SiteNameArg:    strconv.Quote(siteName),
		Program:        program,
		Slug:           slugify(program),
		Engine:         string(opts.Engine),
		EngineLabel:    info.label,
		Dialect:        info.dialect,
		Driver:         info.driver,
		DriverImport:   info.driverImport,
		DSN:            info.dsn,
		DSNNeedsParams: info.dialect != "postgres",
		NeedsDialect:   info.dialect != "postgres",
		Blog:           opts.Blog,
		Tailwind:       opts.Tailwind,
		Captcha:        opts.Captcha,
	}

	if !opts.DryRun {
		if err := os.MkdirAll(abs, 0o755); err != nil {
			return nil, err
		}
	}

	var results []Result
	for _, f := range manifest {
		if !f.when(opts) {
			continue
		}

		target := filepath.Join(abs, filepath.FromSlash(f.dst))
		status := Created
		if _, err := os.Stat(target); err == nil {
			if !opts.Force {
				results = append(results, Result{Path: f.dst, Status: Skipped})
				continue
			}
			status = Replaced
		}

		out, err := renderFile(f.src, data)
		if err != nil {
			return results, err
		}

		if !opts.DryRun {
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return results, err
			}
			if err := os.WriteFile(target, out, f.mode); err != nil {
				return results, err
			}
			// WriteFile only applies mode when it creates the file, so a
			// replaced script would keep the old bits.
			if status == Replaced {
				if err := os.Chmod(target, f.mode); err != nil {
					return results, err
				}
			}
		}
		results = append(results, Result{Path: f.dst, Status: status})
	}
	return results, nil
}

// renderFile executes one embedded starter file. Go output is run through
// go/format, so the templates can use readable [[if]] blocks without
// fighting over whitespace — a formatting error there becomes a parse
// error here rather than ugly generated source.
func renderFile(src string, data templateData) ([]byte, error) {
	raw, err := starter.ReadFile(path.Join("files", src))
	if err != nil {
		return nil, fmt.Errorf("scaffold: reading %s: %w", src, err)
	}

	t, err := template.New(path.Base(src)).
		Delims("[[", "]]").
		Option("missingkey=error").
		Parse(string(raw))
	if err != nil {
		return nil, fmt.Errorf("scaffold: parsing %s: %w", src, err)
	}

	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("scaffold: executing %s: %w", src, err)
	}

	if strings.HasSuffix(src, ".go.tmpl") {
		formatted, err := format.Source(buf.Bytes())
		if err != nil {
			return nil, fmt.Errorf("scaffold: %s did not render to valid Go: %w", src, err)
		}
		return formatted, nil
	}
	return buf.Bytes(), nil
}

// titleCase turns a directory name into something presentable as a site
// name: "my-cool-site" and "my_cool_site" both become "My Cool Site".
func titleCase(s string) string {
	words := strings.FieldsFunc(s, func(r rune) bool {
		return r == '-' || r == '_' || r == ' ' || r == '.'
	})
	for i, w := range words {
		r := []rune(w)
		r[0] = unicode.ToUpper(r[0])
		words[i] = string(r)
	}
	if len(words) == 0 {
		return "My Site"
	}
	return strings.Join(words, " ")
}

// slugify reduces a directory name to something safe as an S3 key prefix.
func slugify(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case b.Len() > 0 && !strings.HasSuffix(b.String(), "-"):
			b.WriteRune('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

func cmp(v, fallback string) string {
	if v != "" {
		return v
	}
	return fallback
}

// embeddedFiles lists every starter file, for the manifest coverage test.
func embeddedFiles() ([]string, error) {
	var out []string
	err := fs.WalkDir(starter, "files", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel("files", p)
		if err != nil {
			return err
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	return out, err
}
