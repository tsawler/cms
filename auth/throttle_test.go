package auth

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

// The throttle is the only thing standing between a login form and an
// online password guess, so its edges matter more than its middle: the
// attempt that first crosses the limit, the window that lets a locked-out
// key back in, and the reset a successful login performs. All of it is
// in-memory, so the tests drive the clock through the window rather than
// waiting on one.

func TestThrottleBlocksAtTheLimit(t *testing.T) {
	tr := NewThrottle(3, time.Hour)
	key := "user@example.com|10.0.0.1"

	if tr.Blocked(key) {
		t.Fatal("a key with no failures is blocked")
	}
	// The limit is the number of failures allowed, so the block lands on
	// the third — not the fourth.
	for i := 1; i <= 2; i++ {
		tr.Fail(key)
		if tr.Blocked(key) {
			t.Fatalf("blocked after %d of 3 failures", i)
		}
	}
	tr.Fail(key)
	if !tr.Blocked(key) {
		t.Error("not blocked after reaching the limit")
	}
	// Further failures keep it blocked rather than wrapping around.
	tr.Fail(key)
	if !tr.Blocked(key) {
		t.Error("not blocked after exceeding the limit")
	}
}

func TestThrottleIsPerKey(t *testing.T) {
	tr := NewThrottle(1, time.Hour)
	tr.Fail("a@example.com|10.0.0.1")

	if !tr.Blocked("a@example.com|10.0.0.1") {
		t.Error("the failing key is not blocked")
	}
	// Same address, different account, and the same account from
	// somewhere else: neither inherits the block.
	if tr.Blocked("b@example.com|10.0.0.1") {
		t.Error("a second account behind one address is blocked")
	}
	if tr.Blocked("a@example.com|10.0.0.2") {
		t.Error("the same account from another address is blocked")
	}
}

func TestThrottleWindowExpires(t *testing.T) {
	tr := NewThrottle(2, 50*time.Millisecond)
	key := "user@example.com|10.0.0.1"
	tr.Fail(key)
	tr.Fail(key)
	if !tr.Blocked(key) {
		t.Fatal("not blocked after reaching the limit")
	}

	// The window runs from the first failure, so once it lapses the whole
	// entry goes and the key starts over — a block is a delay, not a ban.
	time.Sleep(60 * time.Millisecond)
	if tr.Blocked(key) {
		t.Error("still blocked after the window lapsed")
	}
	tr.Fail(key)
	if tr.Blocked(key) {
		t.Error("the post-window failure counted against the old total instead of starting a new one")
	}
}

// A slow guesser must not accumulate across windows: failures spread wider
// apart than the window can never add up to a block, because each one
// restarts the count.
func TestThrottleDoesNotAccumulateAcrossWindows(t *testing.T) {
	tr := NewThrottle(2, 30*time.Millisecond)
	key := "user@example.com|10.0.0.1"
	for range 4 {
		tr.Fail(key)
		time.Sleep(40 * time.Millisecond)
		if tr.Blocked(key) {
			t.Fatal("blocked by failures spread wider than the window")
		}
	}
}

func TestThrottleReset(t *testing.T) {
	tr := NewThrottle(2, time.Hour)
	key := "user@example.com|10.0.0.1"
	tr.Fail(key)
	tr.Fail(key)
	if !tr.Blocked(key) {
		t.Fatal("not blocked after reaching the limit")
	}

	// A password that finally works clears the slate, so the next typo
	// does not lock an account the user just proved they own.
	tr.Reset(key)
	if tr.Blocked(key) {
		t.Error("still blocked after Reset")
	}
	tr.Fail(key)
	if tr.Blocked(key) {
		t.Error("Reset left the old failure count behind")
	}
}

// Resetting a key that was never seen is what a first successful login
// does, and must not panic or resurrect anything.
func TestThrottleResetUnknownKey(t *testing.T) {
	tr := NewThrottle(1, time.Hour)
	tr.Reset("never-seen")
	if tr.Blocked("never-seen") {
		t.Error("an unknown key is blocked after Reset")
	}
}

// Fail sweeps stale entries once the map passes 10,000, which is what
// stops a spray of distinct keys — a botnet trying one password against
// many addresses — from growing it without bound. The sweep only drops
// entries past their window, so live blocks survive it.
func TestThrottleSweepsStaleEntriesButKeepsLiveOnes(t *testing.T) {
	tr := NewThrottle(1, 40*time.Millisecond)

	for i := range 10_001 {
		tr.Fail(fmt.Sprintf("spray-%d|10.0.0.1", i))
	}
	// Everything so far is stale once the window lapses; one fresh key
	// then trips the sweep.
	time.Sleep(50 * time.Millisecond)
	live := "live@example.com|10.0.0.2"
	tr.Fail(live)
	tr.Fail("trigger@example.com|10.0.0.3")

	tr.mu.Lock()
	n := len(tr.entries)
	tr.mu.Unlock()
	if n > 100 {
		t.Errorf("after the sweep %d entries remain, want the stale ones gone", n)
	}
	if !tr.Blocked(live) {
		t.Error("the sweep dropped a key still inside its window")
	}
}

// The limiter is shared by every in-flight login, so concurrent use has to
// be safe. Run with -race this is the test that says so.
func TestThrottleConcurrentUse(t *testing.T) {
	tr := NewThrottle(5, time.Hour)
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Go(func() {
			key := fmt.Sprintf("user-%d|10.0.0.1", i%3)
			for range 50 {
				tr.Fail(key)
				tr.Blocked(key)
				tr.Reset(key)
			}
		})
	}
	wg.Wait()
}
