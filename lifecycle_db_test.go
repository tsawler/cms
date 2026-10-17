package cms

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"runtime"
	"testing"
	"testing/fstest"
	"time"

	"github.com/tsawler/cms/internal/dbtest"
	"github.com/tsawler/cms/internal/sqldb"
)

// goroutinesIn counts the live goroutines whose stack passes through fn.
func goroutinesIn(fn string) int {
	buf := make([]byte, 1<<20)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			buf = buf[:n]
			break
		}
		buf = make([]byte, 2*len(buf))
	}
	return bytes.Count(buf, []byte(fn))
}

// settle waits for the count of goroutines in fn to reach want, and
// returns where it stood when it gave up.
func settle(fn string, want int) int {
	deadline := time.Now().Add(2 * time.Second)
	for {
		got := goroutinesIn(fn)
		if got <= want || time.Now().After(deadline) {
			return got
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A New that fails after starting the session store's cleanup goroutine
// stops it again: a host that retries New, or a test that expects it to
// fail, would otherwise leave one ticking against the database per try.
func TestFailedNewStopsWhatItStarted(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		const loop = "sessionstore.(*Store).cleanupLoop"
		before := goroutinesIn(loop)
		for range 5 {
			_, err := New(Config{
				DB:      db.SQL(),
				Dialect: db.Dialect().Name(),
				Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
				// A template that does not parse, which New finds out
				// after the session store is up.
				TemplateFS: fstest.MapFS{"page.gohtml": &fstest.MapFile{
					Data: []byte(`{{define "page.gohtml"}}{{end`)}},
				PageTemplates: []PageTemplate{{File: "page.gohtml", Label: "Page"}},
			})
			if err == nil {
				t.Fatal("New accepted a template that does not parse")
			}
		}
		if got := settle(loop, before); got > before {
			t.Errorf("%d session cleanup goroutines left running by failed News, want %d", got, before)
		}
	})
}

// Close stops the work the CMS started on its own and waits for it: a
// stylesheet rebuild in the middle of a slow Tailwind run ends with it,
// rather than running on against a pool the host closes next. Nothing
// starts after Close.
func TestCloseStopsBackgroundWork(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		c, err := New(Config{
			DB:      db.SQL(),
			Dialect: db.Dialect().Name(),
			Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
			Tailwind: &TailwindConfig{
				// A build that would run well past the test, with a
				// timeout longer still: only Close can end it.
				Command: []string{"sh", "-c", "sleep 30; :", "{content}", "{output}"},
				Timeout: time.Minute,
			},
		})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		const rebuild = "(*cssRebuilder).run"
		before := goroutinesIn(rebuild)
		c.cssBuilder.schedule()
		// Under way — past collecting classes and into the slow command —
		// before Close is asked to stop it.
		deadline := time.Now().Add(5 * time.Second)
		for goroutinesIn(rebuild) <= before || goroutinesIn("os/exec.(*Cmd).Wait") == 0 {
			if time.Now().After(deadline) {
				t.Fatal("the rebuild never reached the Tailwind command")
			}
			time.Sleep(10 * time.Millisecond)
		}

		closed := make(chan error, 1)
		go func() { closed <- c.Close() }()
		select {
		case err := <-closed:
			if err != nil {
				t.Errorf("Close: %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("Close did not return while a rebuild was running")
		}
		if got := goroutinesIn(rebuild); got > before {
			t.Errorf("%d rebuild goroutines still running after Close returned", got-before)
		}
		if c.background(func(context.Context) {}) {
			t.Error("background work started after Close")
		}
	})
}
