package sessionstore_test

import (
	"context"
	"github.com/alexedwards/scs/v2"
	"testing"
	"time"

	"github.com/tsawler/cms/internal/sqldb"

	"github.com/tsawler/cms/internal/dbtest"
	"github.com/tsawler/cms/internal/sessionstore"
)

func TestSessionCommitAndFind(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		// Cleanup off: these tests drive expiry explicitly and a background
		// sweep would race them.
		s := sessionstore.NewWithCleanupInterval(db, 0)

		want := []byte("session payload")
		if err := s.Commit("tok-1", want, time.Now().Add(time.Hour)); err != nil {
			t.Fatalf("Commit: %v", err)
		}

		got, found, err := s.Find("tok-1")
		if err != nil {
			t.Fatalf("Find: %v", err)
		}
		if !found {
			t.Fatal("Find(committed token) reported not found")
		}
		if string(got) != string(want) {
			t.Errorf("data = %q, want %q", got, want)
		}
	})
}

func TestSessionFindMissing(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := sessionstore.NewWithCleanupInterval(db, 0)

		// A missing token is not an error, just absent.
		got, found, err := s.Find("nope")
		if err != nil {
			t.Fatalf("Find(missing): %v", err)
		}
		if found {
			t.Errorf("Find(missing) returned %q, want not found", got)
		}
	})
}

func TestSessionCommitOverwrites(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := sessionstore.NewWithCleanupInterval(db, 0)

		if err := s.Commit("tok-1", []byte("first"), time.Now().Add(time.Hour)); err != nil {
			t.Fatalf("Commit(first): %v", err)
		}
		// Committing the same token again must update in place — this is the
		// upsert path scs relies on for every request that touches a session.
		if err := s.Commit("tok-1", []byte("second"), time.Now().Add(2*time.Hour)); err != nil {
			t.Fatalf("Commit(second): %v", err)
		}

		got, found, err := s.Find("tok-1")
		if err != nil || !found {
			t.Fatalf("Find = %v, %v, want the updated session", found, err)
		}
		if string(got) != "second" {
			t.Errorf("data = %q, want %q", got, "second")
		}
	})
}

func TestSessionExpiredIsInvisible(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := sessionstore.NewWithCleanupInterval(db, 0)

		if err := s.Commit("stale", []byte("old"), time.Now().Add(-time.Minute)); err != nil {
			t.Fatalf("Commit: %v", err)
		}
		// Find filters on expiry, so an expired row is invisible even before
		// the background cleanup removes it.
		if _, found, err := s.Find("stale"); err != nil || found {
			t.Errorf("Find(expired) = found %v, err %v, want not found", found, err)
		}
	})
}

func TestSessionDelete(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := sessionstore.NewWithCleanupInterval(db, 0)

		if err := s.Commit("tok-1", []byte("data"), time.Now().Add(time.Hour)); err != nil {
			t.Fatalf("Commit: %v", err)
		}
		if err := s.Delete("tok-1"); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if _, found, err := s.Find("tok-1"); err != nil || found {
			t.Errorf("Find after Delete = found %v, err %v, want not found", found, err)
		}
		// Deleting a token that is already gone is not an error.
		if err := s.Delete("tok-1"); err != nil {
			t.Errorf("Delete(already gone) = %v, want nil", err)
		}
	})
}

func TestSessionBinaryDataRoundTrip(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := sessionstore.NewWithCleanupInterval(db, 0)

		// scs stores gob-encoded bytes, so the column must be binary-safe:
		// NUL bytes and high bytes have to survive intact.
		want := []byte{0x00, 0x01, 0xff, 0xfe, 0x00, 'a', 'b', 0x7f}
		if err := s.Commit("binary", want, time.Now().Add(time.Hour)); err != nil {
			t.Fatalf("Commit: %v", err)
		}
		got, found, err := s.Find("binary")
		if err != nil || !found {
			t.Fatalf("Find = %v, %v, want the session", found, err)
		}
		if string(got) != string(want) {
			t.Errorf("data = %v, want %v", got, want)
		}
	})
}

// The cleanup goroutine has to actually stop when asked, or every CMS a
// process builds leaves one ticking against a database it no longer
// serves. StopCleanup returns once it has, so this needs no sleeping.
func TestStopCleanupEndsTheGoroutine(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := sessionstore.NewWithCleanupInterval(db, time.Millisecond)
		done := make(chan struct{})
		go func() {
			s.StopCleanup()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("StopCleanup did not return: the cleanup goroutine is still running")
		}

		// And again — a shutdown path that can be reached twice must not
		// panic on a closed channel.
		s.StopCleanup()
		s.StopCleanup()
	})
}

// A store built with cleanup disabled has no goroutine to stop, and
// stopping it must not block or panic either.
func TestStopCleanupWithoutACleanupLoop(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := sessionstore.NewWithCleanupInterval(db, 0)
		s.StopCleanup()
		s.StopCleanup()
	})
}

// Every session read and write used to run on context.Background(): no
// deadline, and no notice when the client went away, so a slow database
// turned each abandoned request into a query nothing would stop. The
// store now implements scs.CtxStore, and these are the two halves of what
// that has to mean.

// A read carries the caller's cancellation: a lookup for a visitor who is
// no longer there stops with them.
func TestFindCtxHonoursCancellation(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := sessionstore.NewWithCleanupInterval(db, 0)
		if err := s.Commit("tok", []byte("data"), time.Now().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}

		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, _, err := s.FindCtx(ctx, "tok"); err == nil {
			t.Error("FindCtx on a cancelled context succeeded — the read is not cancellable")
		}

		// And an uncancelled one still works, so the plumbing did not
		// simply break reads.
		data, found, err := s.FindCtx(context.Background(), "tok")
		if err != nil || !found || string(data) != "data" {
			t.Errorf("FindCtx = %q, %v, %v; want the stored data", data, found, err)
		}
	})
}

// A write does not. It happens because something has already been decided
// — a login granted, a session destroyed on logout — and a client that
// hangs up in the moment between the decision and the write must not undo
// it. A logout that did not land because the browser went away would
// leave a session that still works.
func TestWritesOutliveACancelledRequest(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := sessionstore.NewWithCleanupInterval(db, 0)

		cancelled, cancel := context.WithCancel(context.Background())
		cancel()

		if err := s.CommitCtx(cancelled, "tok", []byte("data"), time.Now().Add(time.Hour)); err != nil {
			t.Fatalf("CommitCtx on a cancelled context: %v", err)
		}
		if _, found, err := s.FindCtx(context.Background(), "tok"); err != nil || !found {
			t.Fatalf("the session was not committed: found = %v, err = %v", found, err)
		}

		if err := s.DeleteCtx(cancelled, "tok"); err != nil {
			t.Fatalf("DeleteCtx on a cancelled context: %v", err)
		}
		if _, found, err := s.FindCtx(context.Background(), "tok"); err != nil || found {
			t.Errorf("the session survived a logout whose request was cancelled: found = %v, err = %v", found, err)
		}
	})
}

// The context-less three are the same three queries, for anyone holding
// the store through the narrower scs.Store interface.
func TestPlainMethodsStillWork(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		var s scs.Store = sessionstore.NewWithCleanupInterval(db, 0)
		if err := s.Commit("plain", []byte("v"), time.Now().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		data, found, err := s.Find("plain")
		if err != nil || !found || string(data) != "v" {
			t.Fatalf("Find = %q, %v, %v", data, found, err)
		}
		if err := s.Delete("plain"); err != nil {
			t.Fatal(err)
		}
		if _, found, _ := s.Find("plain"); found {
			t.Error("Delete did not remove the session")
		}
	})
}
