package auth

import (
	"sync"
	"time"
)

// Throttle is a small in-memory failed-login limiter. Keys are typically
// "email|remote-ip". It is per-process; that is sufficient to blunt online
// password guessing, which is all it aims to do.
type Throttle struct {
	limit  int
	window time.Duration

	mu      sync.Mutex
	entries map[string]*throttleEntry
}

type throttleEntry struct {
	fails int
	first time.Time
}

// NewThrottle returns a Throttle allowing limit failures per key per window.
func NewThrottle(limit int, window time.Duration) *Throttle {
	return &Throttle{
		limit:   limit,
		window:  window,
		entries: make(map[string]*throttleEntry),
	}
}

// Blocked reports whether key has exceeded its failure allowance.
func (t *Throttle) Blocked(key string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	e, ok := t.entries[key]
	if !ok {
		return false
	}
	if time.Since(e.first) > t.window {
		delete(t.entries, key)
		return false
	}
	return e.fails >= t.limit
}

// Fail records a failed attempt for key.
func (t *Throttle) Fail(key string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	// Opportunistically drop stale entries so the map cannot grow without
	// bound under a spray of distinct keys.
	if len(t.entries) > 10000 {
		for k, e := range t.entries {
			if time.Since(e.first) > t.window {
				delete(t.entries, k)
			}
		}
	}
	e, ok := t.entries[key]
	if !ok || time.Since(e.first) > t.window {
		t.entries[key] = &throttleEntry{fails: 1, first: time.Now()}
		return
	}
	e.fails++
}

// Reset clears the failure count for key, e.g. after a successful login.
func (t *Throttle) Reset(key string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.entries, key)
}
