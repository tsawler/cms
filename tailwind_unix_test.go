//go:build unix

package cms

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// runWithin runs runTailwind with a short build timeout and fails the
// test if it has not returned within limit — the old failure was a build
// that never returned at all.
func runWithin(t *testing.T, limit time.Duration, argv []string, dir string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	type result struct {
		out []byte
		err error
	}
	done := make(chan result, 1)
	go func() {
		out, err := runTailwind(ctx, argv, dir)
		done <- result{out, err}
	}()
	select {
	case r := <-done:
		return string(r.out), r.err
	case <-time.After(limit):
		t.Fatalf("the build was still running %v after its timeout", limit)
		return "", nil
	}
}

func shortWaitDelay(t *testing.T) {
	was := tailwindWaitDelay
	tailwindWaitDelay = 300 * time.Millisecond
	t.Cleanup(func() { tailwindWaitDelay = was })
}

// A wrapper script whose CLI child hangs: the timeout has to stop the
// child too, or the child goes on holding the output pipe and the build
// waits on it for as long as it runs.
func TestTailwindTimeoutStopsTheWholeCommand(t *testing.T) {
	shortWaitDelay(t)
	_, err := runWithin(t, 5*time.Second, []string{"sh", "-c", "sleep 30; :"}, "")
	if err == nil {
		t.Error("a build stopped by its timeout reported success")
	}
}

// A child that ignores TERM is not waited for past WaitDelay, and does
// not outlive the build.
func TestTailwindTimeoutKillsWhatIgnoresTERM(t *testing.T) {
	shortWaitDelay(t)
	out, _ := runWithin(t, 5*time.Second, []string{"sh", "-c", `echo $$; trap "" TERM; sleep 30; :`}, "")
	pgid, err := strconv.Atoi(strings.TrimSpace(strings.SplitN(out, "\n", 2)[0]))
	if err != nil {
		t.Fatalf("no process group id in the output %q", out)
	}
	deadline := time.Now().Add(2 * time.Second)
	for !errors.Is(syscall.Kill(-pgid, 0), syscall.ESRCH) {
		if time.Now().After(deadline) {
			_ = syscall.Kill(-pgid, syscall.SIGKILL)
			t.Fatal("a process from the build's group outlived it")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// The shipped wrapper script, stopped mid-build, still removes its
// scratch directory: the TERM reaches it as well as the CLI, and its
// trap turns that into an exit that runs the cleanup.
func TestTailwindWrapperCleansUpWhenStopped(t *testing.T) {
	shortWaitDelay(t)
	work := t.TempDir()
	if err := os.MkdirAll(filepath.Join(work, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "assets", "theme.css"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "tailwindcss"), []byte("#!/bin/sh\nsleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	scratch := t.TempDir()
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TMPDIR", scratch)
	script, err := filepath.Abs("examples/basic/tailwind-content.sh")
	if err != nil {
		t.Fatal(err)
	}
	content := filepath.Join(work, "content.html")
	if err := os.WriteFile(content, []byte(`<div class="p-4"></div>`), 0o644); err != nil {
		t.Fatal(err)
	}

	runWithin(t, 5*time.Second, []string{"sh", script, content, filepath.Join(work, "out.css")}, work)

	deadline := time.Now().Add(2 * time.Second)
	for {
		left, err := os.ReadDir(scratch)
		if err != nil {
			t.Fatal(err)
		}
		if len(left) == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the stopped wrapper left its scratch directory behind: %v", left)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
