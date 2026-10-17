package shortcache

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// A fresh value is served without loading; an expired one is loaded once
// however many readers arrive together.
func TestGetSharesOneLoad(t *testing.T) {
	c := New[int](time.Hour)
	var loads atomic.Int32
	release := make(chan struct{})
	load := func() (int, error) {
		loads.Add(1)
		<-release
		return 42, nil
	}
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if v, _, err := c.Get(load); err != nil || v != 42 {
				t.Errorf("Get = %d, %v", v, err)
			}
		}()
	}
	time.Sleep(50 * time.Millisecond) // let them all reach the flight
	close(release)
	wg.Wait()
	if n := loads.Load(); n != 1 {
		t.Errorf("20 concurrent readers ran %d loads, want 1", n)
	}
	if v, _, _ := c.Get(func() (int, error) { t.Error("a fresh value was loaded again"); return 0, nil }); v != 42 {
		t.Errorf("cached value = %d, want 42", v)
	}
}

// Clear makes the next read load, and a load that began before the Clear
// neither stores its answer nor hands it to readers who came after.
func TestClearOutranksALoadInFlight(t *testing.T) {
	c := New[string](time.Hour)
	started, finish := make(chan struct{}), make(chan struct{})
	done := make(chan string)
	go func() {
		v, _, _ := c.Get(func() (string, error) {
			close(started)
			<-finish
			return "before the write", nil
		})
		done <- v
	}()
	<-started
	c.Clear()
	if v, _, _ := c.Get(func() (string, error) { return "after the write", nil }); v != "after the write" {
		t.Errorf("a read after Clear got %q, want a load of its own", v)
	}
	close(finish)
	<-done
	if v, _, _ := c.Get(func() (string, error) { return "unexpected load", nil }); v != "after the write" {
		t.Errorf("cached value = %q: the load from before Clear was stored", v)
	}
}

// A failed load falls back to the last value, marked stale; with nothing
// to fall back on, it is the zero value.
func TestFailedLoadServesTheLastValue(t *testing.T) {
	c := New[int](time.Nanosecond)
	if _, stale, err := c.Get(func() (int, error) { return 0, errors.New("down") }); err == nil || stale {
		t.Errorf("first load failing: stale %v, err %v; want an error and nothing stale", stale, err)
	}
	c.Get(func() (int, error) { return 7, nil })
	time.Sleep(time.Millisecond)
	v, stale, err := c.Get(func() (int, error) { return 0, errors.New("down") })
	if err == nil || !stale || v != 7 {
		t.Errorf("got %d, stale %v, err %v; want 7, stale, with the error", v, stale, err)
	}
}
