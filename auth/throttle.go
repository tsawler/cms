package auth

import (
	"crypto/sha256"
	"sync"
	"time"
)

// Throttle is a small in-memory failed-login limiter. Keys are typically
// "email|remote-ip". It is per-process; that is sufficient to blunt online
// password guessing, which is all it aims to do.
//
// Keys are stored as their SHA-256, so an entry costs the same whatever
// the caller passes: the key is usually built from a submitted email,
// which a signed-out request can make as long as its body allows.
type Throttle struct {
	limit  int
	window time.Duration

	mu        sync.Mutex
	entries   map[[sha256.Size]byte]*throttleEntry
	lastSweep time.Time
}

type throttleEntry struct {
	fails int
	first time.Time
}

// sweepAbove is the map size past which Fail starts dropping stale
// entries, so the map cannot grow without bound under a spray of
// distinct keys.
const sweepAbove = 10000

// NewThrottle returns a Throttle allowing limit failures per key per window.
func NewThrottle(limit int, window time.Duration) *Throttle {
	return &Throttle{
		limit:   limit,
		window:  window,
		entries: make(map[[sha256.Size]byte]*throttleEntry),
	}
}

// Blocked reports whether key has exceeded its failure allowance.
func (t *Throttle) Blocked(key string) bool {
	k := sha256.Sum256([]byte(key))
	t.mu.Lock()
	defer t.mu.Unlock()
	e, ok := t.entries[k]
	if !ok {
		return false
	}
	if time.Since(e.first) > t.window {
		delete(t.entries, k)
		return false
	}
	return e.fails >= t.limit
}

// Fail records a failed attempt for key.
func (t *Throttle) Fail(key string) {
	k := sha256.Sum256([]byte(key))
	t.mu.Lock()
	defer t.mu.Unlock()
	t.sweep()
	e, ok := t.entries[k]
	if !ok || time.Since(e.first) > t.window {
		t.entries[k] = &throttleEntry{fails: 1, first: time.Now()}
		return
	}
	e.fails++
}

// sweep drops stale entries once the map is past sweepAbove — at most
// once a minute. A sweep walks the whole map under the lock, and a spray
// of fresh keys leaves nothing stale to drop, so sweeping on every Fail
// would turn each failed attempt into a full scan. Entries live for at
// most a window either way.
func (t *Throttle) sweep() {
	if len(t.entries) <= sweepAbove || time.Since(t.lastSweep) < time.Minute {
		return
	}
	t.lastSweep = time.Now()
	for k, e := range t.entries {
		if time.Since(e.first) > t.window {
			delete(t.entries, k)
		}
	}
}

// Reset clears the failure count for key, e.g. after a successful login.
func (t *Throttle) Reset(key string) {
	k := sha256.Sum256([]byte(key))
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.entries, k)
}
