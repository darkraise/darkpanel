// Package coretest holds fakes of the core interfaces for tests in every
// part: a manual clock, a sealer, a validator, journal and audit stores, an
// agent client, a dispatcher and a host directory.
package coretest

import (
	"sync"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
)

// FakeClock is a core.Clock whose time moves only through Advance and
// StepBack.
type FakeClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*fakeTimer
}

type fakeTimer struct {
	clock *FakeClock
	at    time.Time
	f     func()
	done  bool // fired or stopped
}

// NewFakeClock returns a clock reading start.
func NewFakeClock(start time.Time) *FakeClock {
	return &FakeClock{now: start}
}

// Now returns the clock's current time.
func (c *FakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// AfterFunc arms f to run when Advance reaches now+d.
func (c *FakeClock) AfterFunc(d time.Duration, f func()) core.Timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &fakeTimer{clock: c, at: c.now.Add(d), f: f}
	c.timers = append(c.timers, t)
	return t
}

// Advance moves the clock forward by d and runs every timer that falls due,
// earliest first (ties in arming order), on the calling goroutine. While a
// timer runs, Now reads its deadline; timers it arms that fall due within the
// advance run too.
func (c *FakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	target := c.now.Add(d)
	c.mu.Unlock()
	for {
		c.mu.Lock()
		var next *fakeTimer
		for _, t := range c.timers {
			if !t.done && !t.at.After(target) && (next == nil || t.at.Before(next.at)) {
				next = t
			}
		}
		if next == nil {
			c.now = target
			c.timers = pendingTimers(c.timers)
			c.mu.Unlock()
			return
		}
		next.done = true
		if next.at.After(c.now) {
			c.now = next.at
		}
		c.mu.Unlock()
		next.f()
	}
}

// StepBack moves the clock back by d, as a wall clock set back does. It runs
// no timer; armed timers keep their deadlines and fire when Advance reaches
// them.
func (c *FakeClock) StepBack(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(-d)
}

// Pending is the number of armed timers that have neither fired nor been
// stopped.
func (c *FakeClock) Pending() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(pendingTimers(c.timers))
}

func pendingTimers(ts []*fakeTimer) []*fakeTimer {
	var out []*fakeTimer
	for _, t := range ts {
		if !t.done {
			out = append(out, t)
		}
	}
	return out
}

// Stop disarms the timer; it reports false when the timer already fired or
// was stopped.
func (t *fakeTimer) Stop() bool {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	if t.done {
		return false
	}
	t.done = true
	return true
}
