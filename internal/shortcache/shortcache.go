// Package shortcache holds one value read from the database for a short
// while, so the requests in between share it rather than each asking.
package shortcache

import (
	"strconv"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

// Cache keeps one value for TTL. Concurrent readers of an expired value
// share one load rather than each running it, and Clear makes the next
// read load afresh. The zero value is not usable; build one with New.
type Cache[T any] struct {
	ttl    time.Duration
	flight singleflight.Group

	mu  sync.Mutex
	val T
	at  time.Time
	ok  bool   // whether val is a reading rather than a zero value
	gen uint64 // bumped by Clear
}

// New returns an empty Cache holding values for ttl.
func New[T any](ttl time.Duration) *Cache[T] {
	return &Cache[T]{ttl: ttl}
}

// Get returns the cached value while it is fresh, and otherwise the
// result of load — run once however many callers are waiting for it.
//
// When load fails, Get returns its error along with the last value it
// held, if any: stale is usually better than nothing for a caller that
// can say so, and stale reports which it got.
//
// A load is tied to the generation it began in. One still running when
// Clear is called neither stores its result nor serves callers that
// arrive after the Clear: those start a load of their own, so a request
// made after a write never gets an answer read before it.
func (c *Cache[T]) Get(load func() (T, error)) (val T, stale bool, err error) {
	c.mu.Lock()
	if c.ok && time.Since(c.at) < c.ttl {
		val = c.val
		c.mu.Unlock()
		return val, false, nil
	}
	gen, last, had := c.gen, c.val, c.ok
	c.mu.Unlock()

	v, err, _ := c.flight.Do(strconv.FormatUint(gen, 10), func() (any, error) {
		v, err := load()
		if err != nil {
			return nil, err
		}
		c.mu.Lock()
		if c.gen == gen {
			c.val, c.at, c.ok = v, time.Now(), true
		}
		c.mu.Unlock()
		return v, nil
	})
	if err != nil {
		if had {
			return last, true, err
		}
		var zero T
		return zero, false, err
	}
	return v.(T), false, nil
}

// Clear forgets the value, so the next Get loads it again.
func (c *Cache[T]) Clear() {
	c.mu.Lock()
	c.ok = false
	c.gen++
	c.mu.Unlock()
}
