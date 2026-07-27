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
// The hash in the URL is derived from the class set, so the link doubles
// as the cache buster: identical content produces an identical URL on
// every instance (no clock skew), and any change produces a fresh URL
// that no cache has seen. The stylesheet itself lives in the database,
// so multi-instance deployments serve one consistent artifact no matter
// which instance built it.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
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

	rows, err := c.cfg.DB.Query(ctx, `SELECT content FROM cms_blocks WHERE kind = 'html'`)
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

	snips, err := c.cfg.DB.Query(ctx, `SELECT html FROM cms_snippets`)
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
		for _, list := range [][]render.SectionOption{ss.Backgrounds, ss.Widths, ss.Corners} {
			for _, o := range list {
				addList(o.Class)
				addList(o.ContentClass)
			}
		}
	}

	sort.Strings(out)
	return out, nil
}

// buildHash is the content address of a build: the class set plus the
// command that compiles it, so changing the Tailwind command, version
// pin, or config directory regenerates the artifact even when content
// hasn't changed. Stable across instances and orderings, unlike a
// timestamp.
func buildHash(tc *TailwindConfig, tokens []string) string {
	input := strings.Join(tc.Command, "\x00") + "\x00" + tc.Dir + "\x01" + strings.Join(tokens, "\n")
	sum := sha256.Sum256([]byte(input))
	return hex.EncodeToString(sum[:])[:16]
}

func (c *CMS) loadContentCSS(ctx context.Context) (hash, css string, err error) {
	row := c.cfg.DB.QueryRow(ctx,
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
	_, err := c.cfg.DB.Exec(ctx, `
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

	cacheMu    sync.Mutex // guards the served-copy cache below
	cachedHash string
	cachedCSS  string
	cachedAt   time.Time
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
	hash := buildHash(tc, tokens)
	storedHash, storedCSS, err := b.c.loadContentCSS(ctx)
	if err != nil {
		log.Error("cms tailwind: loading stored css", "err", err)
		return
	}
	if storedHash == hash {
		b.setCache(hash, storedCSS)
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
	b.setCache(hash, string(css))
	log.Info("cms tailwind: rebuilt content css",
		"classes", len(tokens), "bytes", len(css), "took", time.Since(started).Round(time.Millisecond))
}

func (b *cssRebuilder) setCache(hash, css string) {
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

// current returns the newest stored stylesheet, trusting the in-memory
// copy for a few seconds so multi-instance deployments converge on a
// rebuild done elsewhere without a database read per request.
func (b *cssRebuilder) current(ctx context.Context) (hash, css string) {
	b.cacheMu.Lock()
	fresh := time.Since(b.cachedAt) < contentCSSCacheTTL && b.cachedHash != ""
	hash, css = b.cachedHash, b.cachedCSS
	b.cacheMu.Unlock()
	if fresh {
		return hash, css
	}
	dbHash, dbCSS, err := b.c.loadContentCSS(ctx)
	if err != nil {
		// Serve the stale copy rather than nothing.
		b.c.cfg.Logger.Error("cms tailwind: reading css for serving", "err", err)
		return hash, css
	}
	b.setCache(dbHash, dbCSS)
	return dbHash, dbCSS
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
