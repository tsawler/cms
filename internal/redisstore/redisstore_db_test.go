package redisstore_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"

	"github.com/tsawler/cms/internal/dbtest"
	"github.com/tsawler/cms/internal/redisstore"
)

// The container is started lazily, once per test process, and shared by
// every test in the package, matching how dbtest shares its databases.
var (
	redisOnce   sync.Once
	redisClient *redis.Client
	redisErr    error
)

// newStore hands the test a Store on a fresh (flushed) Redis database,
// skipping when Docker is unavailable so `go test ./...` still passes.
func newStore(t *testing.T) *redisstore.Store {
	t.Helper()
	if testing.Short() {
		t.Skip("redisstore: skipping container test in -short mode")
	}
	dbtest.SkipWithoutDocker(t)

	redisOnce.Do(func() {
		ctx := context.Background()
		container, err := tcredis.Run(ctx, "redis:7-alpine")
		if err != nil {
			redisErr = fmt.Errorf("starting container: %w", err)
			return
		}
		uri, err := container.ConnectionString(ctx)
		if err != nil {
			redisErr = fmt.Errorf("connection string: %w", err)
			return
		}
		opts, err := redis.ParseURL(uri)
		if err != nil {
			redisErr = fmt.Errorf("parsing %q: %w", uri, err)
			return
		}
		redisClient = redis.NewClient(opts)
	})
	if redisErr != nil {
		t.Fatalf("redisstore: starting redis: %v", redisErr)
	}
	if err := redisClient.FlushDB(context.Background()).Err(); err != nil {
		t.Fatalf("redisstore: flushing: %v", err)
	}
	return redisstore.New(redisClient)
}

func TestSessionCommitAndFind(t *testing.T) {
	s := newStore(t)

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
}

func TestSessionFindMissing(t *testing.T) {
	s := newStore(t)

	// A missing token is not an error, just absent.
	got, found, err := s.Find("nope")
	if err != nil {
		t.Fatalf("Find(missing): %v", err)
	}
	if found {
		t.Errorf("Find(missing) returned %q, want not found", got)
	}
}

func TestSessionCommitOverwrites(t *testing.T) {
	s := newStore(t)

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
}

func TestSessionExpiredIsInvisible(t *testing.T) {
	s := newStore(t)

	// A commit whose expiry is already past must not resurrect the token —
	// Redis has no "negative TTL", so the store deletes instead.
	if err := s.Commit("stale", []byte("live"), time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if err := s.Commit("stale", []byte("old"), time.Now().Add(-time.Minute)); err != nil {
		t.Fatalf("Commit(expired): %v", err)
	}
	if _, found, err := s.Find("stale"); err != nil || found {
		t.Errorf("Find(expired) = found %v, err %v, want not found", found, err)
	}
}

func TestSessionDelete(t *testing.T) {
	s := newStore(t)

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
}

func TestSessionBinaryDataRoundTrip(t *testing.T) {
	s := newStore(t)

	// scs stores gob-encoded bytes, so the value must be binary-safe:
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
}
