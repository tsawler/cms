package cms

// Content-driven Tailwind builds. Production Tailwind only generates CSS
// for classes its scanner sees in files at build time — and CMS content
// lives in the database, invisible to it. When Config.Tailwind is set,
// the CMS closes that gap itself: after every content change it collects
// the class tokens from stored content, and — when the set actually
// changed — writes them to a synthetic HTML file, runs the host's
// Tailwind command over it, and stores the resulting stylesheet in the
// database. Pages link it as /cms/content-<hash>.css, injected via
// {{cmsHead}}.
//
// Two hashes appear here and they answer different questions. The *build*
// hash (buildHash) is the cache key for compiling: the class set, the
// command, and a digest of the sources the command scans for itself. It
// decides whether a rebuild can be skipped. The *content* hash (cssHash)
// is the address of the finished bytes, and it is what the URL carries,
// so the link doubles as a cache buster: identical CSS produces an
// identical URL on every instance (no clock skew), and any change to the
// bytes — from content, a template edit, or a CLI upgrade — produces a
// fresh URL no cache has seen. Using the build hash for the URL would let
// the file change underneath a URL browsers were told to keep forever.
//
// The stylesheet itself lives in the database, so multi-instance
// deployments serve one consistent artifact no matter which instance
// built it.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tsawler/cms/render"
)

// TailwindConfig makes the CMS rebuild a supplemental stylesheet for
// classes that appear in stored content, using the host's own Tailwind
// setup. Optional: when nil, the CMS never runs a compiler and serves no
// generated CSS (the safelist documented in the README remains the way
// to cover the default editor vocabulary).
type TailwindConfig struct {
	// Command is the argv that runs the Tailwind CLI. Two placeholders
	// are replaced before running: {content} becomes the path of a
	// synthetic HTML file holding every class token found in stored
	// content, and {output} the path the command must write CSS to.
	// Both must appear somewhere in the argv. Example, using the
	// standalone binary with a v3 config:
	//
	//	Command: []string{"tailwindcss", "-i", "assets/input.css",
	//	    "-o", "{output}", "--content", "{content}"}
	//
	// Setups whose CLI can't point at an ad-hoc content file (e.g.
	// Tailwind v4 auto-detection) can target a wrapper script that
	// copies {content} where their build expects it.
	Command []string

	// Dir is the working directory for Command — typically where the
	// site's Tailwind config and input stylesheet live. Empty means the
	// process's working directory.
	Dir string

	// Sources fingerprints the files the Tailwind build reads on its own
	// account — page templates, the input stylesheet, a theme file —
	// rather than through {content}.
	//
	// It exists because those files are build inputs the CMS cannot
	// otherwise see. A rebuild is skipped when the class set is
	// unchanged, so without this a template edit that adds a class
	// produces no rebuild at all: the generated stylesheet keeps
	// whatever it had, and because it is linked *after* the site's own
	// stylesheet, the utilities it does carry outrank the ones it does
	// not. The symptom is a responsive class that silently stops
	// applying — a lg: rule losing to the sm: rule the stale artifact
	// still holds — which looks like a CSS bug and is really a cache
	// bug.
	//
	// Nil defaults to Config.TemplateFS, which is right whenever the
	// build scans the same templates the CMS renders — the usual
	// arrangement. Set it explicitly to cover more (an input.css that
	// changes, a theme file) or to opt out with an empty FS.
	//
	// Only the file contents matter, not their paths on disk, so an
	// embed.FS and an os.DirFS both work.
	Sources fs.FS

	// Timeout bounds one rebuild. Zero means 60 seconds.
	Timeout time.Duration
}

// contentCSSPrefix is the public route generated stylesheets are served
// under: /cms/content-<hash>.css.
const contentCSSPrefix = "/cms/content-"

// contentCSSCurrentPath answers "what is the current stylesheet URL?" —
// the editor polls it after a save to hot-swap the page's <link> once an
// asynchronous rebuild lands, instead of waiting for the next reload.
const contentCSSCurrentPath = "/cms/content-css"

// contentCSSCacheTTL is how long an instance trusts its in-memory copy of
// the generated CSS before re-reading the database — the staleness bound
// for renders on instances that didn't run the rebuild themselves.
const contentCSSCacheTTL = 5 * time.Second

var (
	tailwindClassAttrRe = regexp.MustCompile(`class\s*=\s*(?:"([^"]*)"|'([^']*)')`)
	contentCSSHashRe    = regexp.MustCompile(`^[0-9a-f]{16}$`)
)

func validateTailwindConfig(tc *TailwindConfig) error {
	if tc == nil {
		return nil
	}
	if len(tc.Command) == 0 {
		return errors.New("cms: Tailwind.Command is required when Tailwind is configured")
	}
	joined := strings.Join(tc.Command, " ")
	if !strings.Contains(joined, "{content}") || !strings.Contains(joined, "{output}") {
		return errors.New("cms: Tailwind.Command must use the {content} and {output} placeholders")
	}
	return nil
}

// tailwindArgv resolves the {content} and {output} placeholders.
func tailwindArgv(command []string, contentPath, outputPath string) []string {
	argv := make([]string, len(command))
	for i, a := range command {
		a = strings.ReplaceAll(a, "{content}", contentPath)
		a = strings.ReplaceAll(a, "{output}", outputPath)
		argv[i] = a
	}
	return argv
}

// classTokens extracts the class attribute tokens from an HTML fragment.
func classTokens(html string, seen map[string]bool, out *[]string) {
	for _, m := range tailwindClassAttrRe.FindAllStringSubmatch(html, -1) {
		list := m[1]
		if list == "" {
			list = m[2]
		}
		for _, tok := range strings.Fields(list) {
			if !seen[tok] {
				seen[tok] = true
				*out = append(*out, tok)
			}
		}
	}
}

// collectClassTokens gathers every class token the site's pages can carry:
// stored content (all pages, draft and published — editors see drafts, so
// draft classes need CSS too), admin-created snippets, and the configured
// snippet/style libraries (so a freshly inserted snippet is styled before
// its first save).
func (c *CMS) collectClassTokens(ctx context.Context) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	addList := func(list string) {
		for _, tok := range strings.Fields(list) {
			if !seen[tok] {
				seen[tok] = true
				out = append(out, tok)
			}
		}
	}

	rows, err := c.db.Query(ctx, `SELECT content FROM cms_blocks WHERE kind = 'html'`)
	if err != nil {
		return nil, fmt.Errorf("cms: reading blocks for tailwind: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var html string
		if err := rows.Scan(&html); err != nil {
			return nil, err
		}
		classTokens(html, seen, &out)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	snips, err := c.db.Query(ctx, `SELECT html FROM cms_snippets`)
	if err != nil {
		return nil, fmt.Errorf("cms: reading snippets for tailwind: %w", err)
	}
	defer snips.Close()
	for snips.Next() {
		var html string
		if err := snips.Scan(&html); err != nil {
			return nil, err
		}
		classTokens(html, seen, &out)
	}
	if err := snips.Err(); err != nil {
		return nil, err
	}

	for _, sn := range c.cfg.Snippets {
		classTokens(sn.HTML, seen, &out)
	}
	for _, st := range c.cfg.EditorStyles {
		addList(st.Class)
	}
	if ss := c.cfg.SectionStyles; ss != nil {
		// Every curated axis, Paddings included: a host that configures
		// vertical spacing must not have to hand-safelist the classes
		// its own settings dialog offers.
		for _, list := range [][]render.SectionOption{ss.Backgrounds, ss.Widths, ss.Corners, ss.Paddings} {
			for _, o := range list {
				addList(o.Class)
				addList(o.ContentClass)
			}
		}
	}

	sort.Strings(out)
	return out, nil
}

// buildHash is the content address of a build: the class set, the command
// that compiles it, and a digest of the sources the command reads for
// itself. Changing the Tailwind command, version pin, config directory,
// or a scanned template regenerates the artifact even when stored content
// hasn't changed. Stable across instances and orderings, unlike a
// timestamp.
func buildHash(tc *TailwindConfig, tokens []string, sourcesDigest string) string {
	input := strings.Join(tc.Command, "\x00") + "\x00" + tc.Dir +
		"\x01" + strings.Join(tokens, "\n") + "\x02" + sourcesDigest
	sum := sha256.Sum256([]byte(input))
	return hex.EncodeToString(sum[:])[:16]
}

// sourcesDigest fingerprints every file in fsys, so an edit to a scanned
// template changes the build hash.
//
// Paths are folded in alongside contents, so a rename counts as a change;
// fs.WalkDir visits in lexical order, which makes the result stable
// across instances without sorting. A nil FS digests to the empty string,
// which is simply a build whose sources are not tracked.
//
// Errors are reported rather than swallowed: a digest that silently
// degrades to "" on a read error would reintroduce the stale-artifact bug
// in the one case nobody would think to check.
func sourcesDigest(fsys fs.FS) (string, error) {
	if fsys == nil {
		return "", nil
	}
	h := sha256.New()
	err := fs.WalkDir(fsys, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		body, err := fs.ReadFile(fsys, path)
		if err != nil {
			return fmt.Errorf("reading %s: %w", path, err)
		}
		fmt.Fprintf(h, "%s\x00%d\x00", path, len(body))
		h.Write(body)
		return nil
	})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil))[:16], nil
}

// tailwindSources is the FS whose contents feed the build hash:
// Tailwind.Sources when set, otherwise the templates the CMS renders.
func (c *CMS) tailwindSources() fs.FS {
	if c.cfg.Tailwind != nil && c.cfg.Tailwind.Sources != nil {
		return c.cfg.Tailwind.Sources
	}
	return c.cfg.TemplateFS
}

func (c *CMS) loadContentCSS(ctx context.Context) (hash, css string, err error) {
	row := c.db.QueryRow(ctx,
		`SELECT class_hash, css FROM cms_content_css WHERE singleton`)
	if err := row.Scan(&hash, &css); err != nil {
		if strings.Contains(err.Error(), "no rows") {
			return "", "", nil
		}
		return "", "", err
	}
	return hash, css, nil
}

func (c *CMS) storeContentCSS(ctx context.Context, hash, css string) error {
	_, err := c.db.Exec(ctx, `
		INSERT INTO cms_content_css (singleton, class_hash, css)
		VALUES (TRUE, $1, $2)
		ON CONFLICT (singleton) DO UPDATE
		SET class_hash = EXCLUDED.class_hash, css = EXCLUDED.css, updated_at = now()`,
		hash, css)
	return err
}

/* ------------------------------------------------------------------ *
 * The rebuilder: serialized, latest-wins background builds.
 * ------------------------------------------------------------------ */

type cssRebuilder struct {
	c *CMS

	mu      sync.Mutex // guards running/rerun
	running bool
	rerun   bool

	cacheMu sync.Mutex // guards the served-copy cache below
	// cachedHash addresses the *bytes*, not the build. See cssHash.
	cachedHash string
	cachedCSS  string
	cachedAt   time.Time
}

// cssHash is the content address of the stylesheet itself, and it is what
// appears in the served URL.
//
// Deliberately not the build hash. The build hash answers "do I need to
// compile again?", and it covers inputs — the class set, the command, the
// scanned sources. Those can change without changing a byte of the
// output, and, more damagingly, the output can change without them: any
// upgrade of the Tailwind CLI recompiles the same inputs into different
// CSS. A URL carrying the build hash then stays fixed while its contents
// move, and every browser holding the immutable copy keeps it — the file
// updates and no visitor sees it until a hard reload.
//
// Hashing the bytes makes the URL change exactly when the bytes do, which
// is the property "immutable" claims. Derived on load rather than stored,
// so it cannot disagree with the CSS it names.
func cssHash(css string) string {
	if css == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(css))
	return hex.EncodeToString(sum[:])[:16]
}

// schedule requests a rebuild. Builds are serialized; a request landing
// mid-build queues exactly one follow-up ("latest wins"), so a burst of
// saves cannot stack compilers. Safe on a nil receiver (feature off).
func (b *cssRebuilder) schedule() {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.running {
		b.rerun = true
		return
	}
	b.running = true
	go b.run()
}

func (b *cssRebuilder) run() {
	for {
		b.buildOnce()
		b.mu.Lock()
		if !b.rerun {
			b.running = false
			b.mu.Unlock()
			return
		}
		b.rerun = false
		b.mu.Unlock()
	}
}

// buildOnce collects the class set and, when its hash differs from the
// stored artifact's, runs the host's Tailwind command and stores the
// result. A build failure keeps the previous stylesheet — saves must
// never be held hostage by the CSS toolchain.
func (b *cssRebuilder) buildOnce() {
	tc := b.c.cfg.Tailwind
	log := b.c.cfg.Logger
	timeout := tc.Timeout
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	started := time.Now()

	tokens, err := b.c.collectClassTokens(ctx)
	if err != nil {
		log.Error("cms tailwind: collecting classes", "err", err)
		return
	}
	digest, err := sourcesDigest(b.c.tailwindSources())
	if err != nil {
		// Building anyway would be worse than not building: the hash
		// would be wrong, and a wrong hash is what makes a stale
		// stylesheet look current forever.
		log.Error("cms tailwind: fingerprinting build sources", "err", err)
		return
	}
	hash := buildHash(tc, tokens, digest)
	storedHash, storedCSS, err := b.c.loadContentCSS(ctx)
	if err != nil {
		log.Error("cms tailwind: loading stored css", "err", err)
		return
	}
	if storedHash == hash {
		b.setCache(storedCSS)
		return
	}

	contentFile, err := os.CreateTemp("", "cms-tailwind-*.html")
	if err != nil {
		log.Error("cms tailwind: temp content file", "err", err)
		return
	}
	defer os.Remove(contentFile.Name())
	_, werr := contentFile.WriteString(`<div class="` + strings.Join(tokens, " ") + `"></div>`)
	if cerr := contentFile.Close(); werr != nil || cerr != nil {
		log.Error("cms tailwind: writing content file", "err", errors.Join(werr, cerr))
		return
	}

	outputFile, err := os.CreateTemp("", "cms-tailwind-*.css")
	if err != nil {
		log.Error("cms tailwind: temp output file", "err", err)
		return
	}
	outputFile.Close()
	defer os.Remove(outputFile.Name())

	argv := tailwindArgv(tc.Command, contentFile.Name(), outputFile.Name())
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = tc.Dir
	if out, err := cmd.CombinedOutput(); err != nil {
		log.Error("cms tailwind: build failed — keeping the previous stylesheet",
			"err", err, "output", tail(string(out), 2000))
		return
	}
	css, err := os.ReadFile(outputFile.Name())
	if err != nil || len(css) == 0 {
		log.Error("cms tailwind: build produced no css", "err", err)
		return
	}

	if err := b.c.storeContentCSS(ctx, hash, string(css)); err != nil {
		log.Error("cms tailwind: storing css", "err", err)
		return
	}
	b.setCache(string(css))
	log.Info("cms tailwind: rebuilt content css",
		"classes", len(tokens), "bytes", len(css), "took", time.Since(started).Round(time.Millisecond))
}

// setCache publishes a stylesheet as the one to serve, addressing it by
// its own bytes.
func (b *cssRebuilder) setCache(css string) {
	hash := cssHash(css)
	b.cacheMu.Lock()
	b.cachedHash = hash
	b.cachedCSS = css
	b.cachedAt = time.Now()
	b.cacheMu.Unlock()
	// The renderer's {{cmsHead}} link follows the cache.
	if r := b.c.renderer; r != nil {
		href := ""
		if css != "" {
			href = contentCSSPrefix + hash + ".css"
		}
		r.SetContentCSSHref(href)
	}
}

// current returns the newest stored stylesheet and the hash of its bytes,
// trusting the in-memory copy for a few seconds so multi-instance
// deployments converge on a rebuild done elsewhere without a database
// read per request.
func (b *cssRebuilder) current(ctx context.Context) (hash, css string) {
	b.cacheMu.Lock()
	fresh := time.Since(b.cachedAt) < contentCSSCacheTTL && b.cachedHash != ""
	hash, css = b.cachedHash, b.cachedCSS
	b.cacheMu.Unlock()
	if fresh {
		return hash, css
	}
	// The stored class_hash is the build key and is deliberately not what
	// the URL carries; setCache re-derives the address from the bytes.
	_, dbCSS, err := b.c.loadContentCSS(ctx)
	if err != nil {
		// Serve the stale copy rather than nothing.
		b.c.cfg.Logger.Error("cms tailwind: reading css for serving", "err", err)
		return hash, css
	}
	b.setCache(dbCSS)
	return cssHash(dbCSS), dbCSS
}

// serveContentCSS handles GET /cms/content-<hash>.css. The current
// stylesheet is served whatever hash the URL names; only a request for
// the current hash is marked immutable, so stale HTML (cached pages
// naming an old hash) still gets working styles on a short cache.
func (c *CMS) serveContentCSS(w http.ResponseWriter, r *http.Request) {
	reqHash := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, contentCSSPrefix), ".css")
	if !contentCSSHashRe.MatchString(reqHash) {
		c.notFound(w)
		return
	}
	hash, css := c.cssBuilder.current(r.Context())
	if css == "" {
		c.notFound(w)
		return
	}
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	if reqHash == hash {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "public, max-age=60")
	}
	fmt.Fprint(w, css)
}

// serveContentCSSCurrent reports the current generated stylesheet's URL
// as JSON ({"href": ""} when no artifact exists yet). Never cached: its
// whole purpose is telling an already-loaded page that the URL changed.
func (c *CMS) serveContentCSSCurrent(w http.ResponseWriter, r *http.Request) {
	hash, css := c.cssBuilder.current(r.Context())
	href := ""
	if css != "" {
		href = contentCSSPrefix + hash + ".css"
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	fmt.Fprintf(w, `{"href":%q}`, href)
}

func tail(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}
