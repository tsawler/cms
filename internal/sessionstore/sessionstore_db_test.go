package sessionstore_test

import (
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
