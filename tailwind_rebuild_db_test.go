package cms

// The rebuild pipeline: schedule → run → buildOnce → setCache. A build
// shells out to the host's Tailwind CLI, so these stand a shell script in
// for it — which is a shape the config documents anyway, since a setup
// whose CLI can't take an ad-hoc content file is told to point at a
// wrapper script.
//
// What the stub lets these check is the part that is the CMS's own: when
// a build is skipped, what a failed build leaves behind, and that a burst
// of saves cannot stack compilers.

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/tsawler/cms/content"
	"github.com/tsawler/cms/internal/dbtest"
	"github.com/tsawler/cms/internal/sqldb"
)

// tailwindStub is a script standing in for the Tailwind CLI. Every run
// appends a line to a log file, so a test can count builds, and writes
// whatever CSS it was given to {output}.
type tailwindStub struct {
	dir     string
	logPath string
	script  string
}

// newTailwindStub writes the script and returns it. body is the shell that
// stands in for compiling, with $CONTENT and $OUTPUT bound to the two paths
// the CMS passes; the prologue logs the run and keeps a copy of the
// synthetic content file, which the CMS deletes as soon as the build ends.
func newTailwindStub(t *testing.T, body string) *tailwindStub {
	t.Helper()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("tailwind build tests need a shell to stand in for the CLI")
	}
	dir := t.TempDir()
	stub := &tailwindStub{
		dir:     dir,
		logPath: filepath.Join(dir, "builds.log"),
		script:  filepath.Join(dir, "tailwind-stub.sh"),
	}
	script := "#!/bin/sh\n" +
		"CONTENT=\"$1\"\n" +
		"OUTPUT=\"$2\"\n" +
		"echo \"$CONTENT\" >> " + stub.logPath + "\n" +
		"cp \"$CONTENT\" " + filepath.Join(dir, "content-seen.html") + "\n" +
		body + "\n"
	if err := os.WriteFile(stub.script, []byte(script), 0o755); err != nil {
		t.Fatalf("writing the stub: %v", err)
	}
	return stub
}

// writes is the ordinary stub body: emit a stylesheet.
func writes(css string) string { return `printf '%s' '` + css + `' > "$OUTPUT"` }

// command is the argv a TailwindConfig points at the stub with.
func (s *tailwindStub) command() []string {
	return []string{"sh", s.script, "{content}", "{output}"}
}

// builds is how many times the stub has been invoked.
func (s *tailwindStub) builds(t *testing.T) int {
	t.Helper()
	body, err := os.ReadFile(s.logPath)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatalf("reading the stub log: %v", err)
	}
	return len(strings.Fields(strings.TrimSpace(string(body))))
}

// contentSeen returns the synthetic HTML the CMS handed the stub on its
// last run — the class corpus, as the compiler sees it.
func (s *tailwindStub) contentSeen(t *testing.T) string {
	t.Helper()
	body, err := os.ReadFile(s.logPath)
	if err != nil {
		t.Fatalf("reading the stub log: %v", err)
	}
	paths := strings.Fields(strings.TrimSpace(string(body)))
	if len(paths) == 0 {
		t.Fatal("the stub has never run")
	}
	seen, err := os.ReadFile(filepath.Join(s.dir, "content-seen.html"))
	if err != nil {
		t.Fatalf("reading the captured content file: %v", err)
	}
	return string(seen)
}

// tailwindCMS wires a CMS to db with the stub as its Tailwind command.
func tailwindCMS(t *testing.T, db *sqldb.DB, stub *tailwindStub, sources fs.FS) *CMS {
	t.Helper()
	c, err := New(Config{
		DB:      db.SQL(),
		Dialect: db.Dialect().Name(),
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		Tailwind: &TailwindConfig{
			Command: stub.command(),
			Sources: sources,
			Timeout: 30 * time.Second,
		},
	})
	if err != nil {
		t.Fatalf("cms.New: %v", err)
	}
	if c.cssBuilder == nil {
		t.Fatal("a configured Tailwind left no rebuilder")
	}
	return c
}

func TestBuildOnceCompilesAndStores(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		stub := newTailwindStub(t, writes(".generated{color:red}"))
		c := tailwindCMS(t, db, stub, nil)
		ctx := context.Background()

		c.cssBuilder.buildOnce()

		if n := stub.builds(t); n != 1 {
			t.Fatalf("the compiler ran %d times, want 1", n)
		}
		hash, css, err := c.loadContentCSS(ctx)
		if err != nil {
			t.Fatalf("loadContentCSS: %v", err)
		}
		if css != ".generated{color:red}" {
			t.Errorf("stored css = %q, want the compiler's output", css)
		}
		if hash == "" {
			t.Error("the stylesheet was stored without a build key")
		}
		// The cache and the {{cmsHead}} link follow the build, so the very
		// next render links the stylesheet that was just made.
		cachedHash, cachedCSS := c.cssBuilder.current(ctx)
		if cachedCSS != css {
			t.Errorf("cached css = %q, want the stored stylesheet", cachedCSS)
		}
		if cachedHash != cssHash(css) {
			t.Errorf("cached hash = %q, want the hash of the bytes", cachedHash)
		}
		// The compiler is handed the class corpus, not the pages.
		if seen := stub.contentSeen(t); !strings.Contains(seen, "class=") {
			t.Errorf("the compiler was given %q, want a synthetic class file", seen)
		}
	})
}

// A rebuild is skipped when nothing that feeds it has changed — that is
// the whole reason the build hash exists. The stored stylesheet is still
// republished into the cache, so an instance that skipped a build still
// serves the right link.
func TestBuildOnceSkipsWhenNothingChanged(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		stub := newTailwindStub(t, writes(".generated{color:red}"))
		c := tailwindCMS(t, db, stub, nil)

		c.cssBuilder.buildOnce()
		c.cssBuilder.buildOnce()
		c.cssBuilder.buildOnce()

		if n := stub.builds(t); n != 1 {
			t.Errorf("the compiler ran %d times, want 1 — the rest had nothing to do", n)
		}
		if _, css := c.cssBuilder.current(context.Background()); css != ".generated{color:red}" {
			t.Errorf("cached css = %q, want the stored stylesheet republished", css)
		}
	})
}

// The bug this guards is the one the config's Sources field exists for. A
// template edit that adds a class changes no stored content, so the class
// set is identical and the build would be skipped — leaving a stylesheet
// that lacks the new class. Because it is linked after the site's own,
// the utilities it does carry outrank the ones it doesn't, and the
// symptom is a class that silently stops applying.
func TestBuildOnceRebuildsWhenTheScannedSourcesChange(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		stub := newTailwindStub(t, writes(".generated{color:red}"))

		sources := fstest.MapFS{"page.gohtml": &fstest.MapFile{Data: []byte(`<div class="p-4">`)}}
		c := tailwindCMS(t, db, stub, sources)

		c.cssBuilder.buildOnce()
		if n := stub.builds(t); n != 1 {
			t.Fatalf("the compiler ran %d times, want 1", n)
		}

		// Nothing in the database moved; only a scanned template did.
		c.cfg.Tailwind.Sources = fstest.MapFS{
			"page.gohtml": &fstest.MapFile{Data: []byte(`<div class="p-4 lg:p-8">`)},
		}
		c.cssBuilder.buildOnce()
		if n := stub.builds(t); n != 2 {
			t.Errorf("the compiler ran %d times, want 2 — a template edit is a build input", n)
		}
	})
}

// A rebuild whose fingerprint cannot be taken must not build at all: the
// hash it stored would be wrong, and a wrong hash is exactly what makes a
// stale stylesheet look current forever.
func TestBuildOnceRefusesWhenTheSourcesCannotBeRead(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		stub := newTailwindStub(t, writes(".generated{color:red}"))
		c := tailwindCMS(t, db, stub, nil)

		c.cfg.Tailwind.Sources = unreadableFS{}
		c.cssBuilder.buildOnce()

		if n := stub.builds(t); n != 0 {
			t.Errorf("the compiler ran %d times, want 0", n)
		}
		if _, css, _ := c.loadContentCSS(context.Background()); css != "" {
			t.Errorf("stored css = %q, want nothing written on an unfingerprintable build", css)
		}
	})
}

// unreadableFS walks cleanly and then fails the read, which is the shape
// of a source tree that moved underneath the build.
type unreadableFS struct{}

func (unreadableFS) Open(name string) (fs.File, error) {
	if name == "." {
		return fstest.MapFS{"gone.gohtml": &fstest.MapFile{Data: []byte("x")}}.Open(name)
	}
	return nil, fmt.Errorf("cms test: %s went away mid-build", name)
}

// A build failure must never take a save down with it, and it must never
// leave the site with no stylesheet: the previous one stays exactly where
// it was until a build succeeds.
func TestBuildFailureKeepsThePreviousStylesheet(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		good := newTailwindStub(t, writes(".generated{color:red}"))
		c := tailwindCMS(t, db, good, nil)
		ctx := context.Background()

		c.cssBuilder.buildOnce()
		hash, css, err := c.loadContentCSS(ctx)
		if err != nil {
			t.Fatalf("loadContentCSS: %v", err)
		}
		if css == "" {
			t.Fatal("the first build stored nothing")
		}

		cases := map[string]string{
			// The compiler itself failed.
			"a non-zero exit": `echo "some tailwind error" >&2; exit 1`,
			// It succeeded but wrote nothing, which is not a stylesheet.
			"an empty output": `: > "$OUTPUT"`,
		}
		for name, body := range cases {
			t.Run(name, func(t *testing.T) {
				broken := newTailwindStub(t, body)
				c.cfg.Tailwind.Command = broken.command()
				// A different command is a different build key, so this is
				// a build that genuinely tries rather than one that skips.
				c.cssBuilder.buildOnce()

				if n := broken.builds(t); n != 1 {
					t.Fatalf("the failing compiler ran %d times, want 1", n)
				}
				gotHash, gotCSS, err := c.loadContentCSS(ctx)
				if err != nil {
					t.Fatalf("loadContentCSS: %v", err)
				}
				if gotCSS != css || gotHash != hash {
					t.Errorf("stored = %q/%q after a failed build, want the previous %q/%q",
						gotHash, gotCSS, hash, css)
				}
			})
		}
	})
}

// alwaysChangingFS fingerprints differently on every read. It is the
// instrument the two tests below need: an unchanged build hash makes
// buildOnce return before it reaches the compiler, so without this a
// coalescing test would count one compiler run whether or not the
// coalescing worked, and pass either way.
type alwaysChangingFS struct{ n atomic.Int64 }

func (f *alwaysChangingFS) Open(name string) (fs.File, error) {
	if name == "." {
		return fstest.MapFS{"v.txt": &fstest.MapFile{}}.Open(".")
	}
	body := fmt.Sprintf("v%d", f.n.Add(1))
	return fstest.MapFS{"v.txt": &fstest.MapFile{Data: []byte(body)}}.Open(name)
}

// Builds are serialized and coalesced: a request landing mid-build queues
// exactly one follow-up. Without that, a burst of saves — an import, a
// bulk edit, an editor typing — would stack a compiler per save, doing
// work every run but the last of which is thrown away.
func TestScheduleCoalescesABurstOfSaves(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		// Slow enough that the whole burst lands during the first build,
		// and with sources that never fingerprint the same twice so no
		// build short-circuits for having nothing to do.
		stub := newTailwindStub(t, `sleep 0.3; date +%s%N > "$OUTPUT"`)
		c := tailwindCMS(t, db, stub, &alwaysChangingFS{})

		var wg sync.WaitGroup
		for range 20 {
			wg.Go(c.cssBuilder.schedule)
		}
		wg.Wait()
		waitIdle(t, c.cssBuilder)

		// One build in flight plus at most one queued follow-up. Two is
		// the expected answer; one is legal if the whole burst arrived
		// before the first build started.
		if n := stub.builds(t); n < 1 || n > 2 {
			t.Errorf("20 saves ran %d builds, want at most 2", n)
		}
	})
}

// The coalescing is about builds in flight, not a lock on ever building
// again: a save arriving after one finishes starts another.
func TestScheduleBuildsAgainAfterGoingIdle(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		stub := newTailwindStub(t, `date +%s%N > "$OUTPUT"`)
		c := tailwindCMS(t, db, stub, &alwaysChangingFS{})

		c.cssBuilder.schedule()
		waitIdle(t, c.cssBuilder)
		first := stub.builds(t)
		if first != 1 {
			t.Fatalf("the first save ran %d builds, want 1", first)
		}

		c.cssBuilder.schedule()
		waitIdle(t, c.cssBuilder)

		if second := stub.builds(t); second != 2 {
			t.Errorf("builds went %d -> %d, want the second save to have built", first, second)
		}
	})
}

// The same thing through the door the product actually uses: a content
// change is what calls schedule, and a class that was not in the corpus
// before is what makes the build real work rather than a skip.
func TestScheduleAfterAContentChangeCompilesTheNewClass(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		stub := newTailwindStub(t, writes(".generated{}"))
		c := tailwindCMS(t, db, stub, nil)
		ctx := context.Background()

		c.cssBuilder.schedule()
		waitIdle(t, c.cssBuilder)
		if n := stub.builds(t); n != 1 {
			t.Fatalf("the first build ran %d times, want 1", n)
		}

		page := &content.Page{Slug: "newly-styled", TemplateName: "page.gohtml", Title: "Styled"}
		if _, err := c.content.Insert(ctx, page, "en"); err != nil {
			t.Fatalf("Insert: %v", err)
		}
		if err := c.content.UpsertDraftBlock(ctx, page.ID, "main", "en",
			content.KindHTML, `<p class="text-freshly-added">hello</p>`); err != nil {
			t.Fatalf("UpsertDraftBlock: %v", err)
		}

		c.cssBuilder.schedule()
		waitIdle(t, c.cssBuilder)

		if n := stub.builds(t); n != 2 {
			t.Errorf("the compiler ran %d times, want 2 — new content is new work", n)
		}
		if seen := stub.contentSeen(t); !strings.Contains(seen, "text-freshly-added") {
			t.Errorf("the compiler was given %q, want the newly saved class", seen)
		}
	})
}

// waitIdle blocks until the rebuilder has no build in flight and none
// queued.
func waitIdle(t *testing.T, b *cssRebuilder) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		b.mu.Lock()
		idle := !b.running && !b.rerun
		b.mu.Unlock()
		if idle {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the rebuilder never went idle")
}

// The in-memory copy is trusted for a few seconds and then re-read, which
// is how an instance that ran no build of its own picks up one done
// elsewhere. Past the TTL the database wins.
func TestCurrentRereadsTheDatabaseOnceTheCacheIsStale(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		stub := newTailwindStub(t, `printf '.mine{}' > "$OUTPUT"`)
		c := tailwindCMS(t, db, stub, nil)
		ctx := context.Background()

		c.cssBuilder.buildOnce()
		if _, css := c.cssBuilder.current(ctx); css != ".mine{}" {
			t.Fatalf("cached css = %q, want this instance's build", css)
		}

		// Another instance rebuilds and stores its result.
		if err := c.storeContentCSS(ctx, "elsewhere", ".theirs{}"); err != nil {
			t.Fatalf("storeContentCSS: %v", err)
		}
		// Inside the TTL this instance keeps serving what it has, which is
		// the point of the cache — one database read per request would be
		// the alternative.
		if _, css := c.cssBuilder.current(ctx); css != ".mine{}" {
			t.Errorf("cached css = %q, want the fresh cache still trusted", css)
		}

		// Age the cache past the TTL.
		c.cssBuilder.cacheMu.Lock()
		c.cssBuilder.cachedAt = time.Now().Add(-contentCSSCacheTTL - time.Second)
		c.cssBuilder.cacheMu.Unlock()

		hash, css := c.cssBuilder.current(ctx)
		if css != ".theirs{}" {
			t.Errorf("css = %q, want the other instance's build", css)
		}
		// And the URL is re-derived from the new bytes, not from the
		// build key the other instance stored.
		if hash != cssHash(".theirs{}") {
			t.Errorf("hash = %q, want the hash of the bytes", hash)
		}
		if hash == "elsewhere" {
			t.Error("the URL carried the stored build key rather than the content hash")
		}
	})
}

// The classes a build compiles come from stored content, which is the
// gap the whole feature exists to close: Tailwind's own scanner cannot
// see the database.
func TestBuildOnceCompilesClassesFromStoredContent(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		stub := newTailwindStub(t, writes(".generated{}"))
		c := tailwindCMS(t, db, stub, nil)
		ctx := context.Background()

		page := &content.Page{Slug: "styled", TemplateName: "page.gohtml", Title: "Styled"}
		if _, err := c.content.Insert(ctx, page, "en"); err != nil {
			t.Fatalf("Insert: %v", err)
		}
		if err := c.content.UpsertDraftBlock(ctx, page.ID, "main", "en",
			content.KindHTML, `<p class="text-invented-token">hello</p>`); err != nil {
			t.Fatalf("UpsertDraftBlock: %v", err)
		}

		c.cssBuilder.buildOnce()

		if seen := stub.contentSeen(t); !strings.Contains(seen, "text-invented-token") {
			t.Errorf("the compiler was given %q, want the class from stored content", seen)
		}
	})
}
