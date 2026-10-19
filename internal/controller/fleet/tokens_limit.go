// Package fleet manages the controller's agents: the agent certificate
// authority, enrollment and the agents' sessions with the controller.
package fleet

import (
	"sync"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// Enrollment rate limits (spec section 3), counted over a sliding minute.
const (
	EnrollPerSource = 10 // requests per source address per minute
	EnrollGlobal    = 60 // requests from all sources per minute
)

const enrollWindow = time.Minute

// EnrollLimiter admits enrollment requests in memory. Only admitted requests
// are kept, so it never holds more than EnrollGlobal times however many
// sources a flood uses, and a client that keeps retrying while refused does
// not push its own limit further out.
type EnrollLimiter struct {
	mu      sync.Mutex
	clock   core.Clock
	all     []time.Time            // admitted request times, oldest first
	sources map[string][]time.Time // the same times by source, oldest first
}

// NewEnrollLimiter returns a limiter that reads time from clock.
func NewEnrollLimiter(clock core.Clock) *EnrollLimiter {
	return &EnrollLimiter{clock: clock, sources: map[string][]time.Time{}}
}

// Allow admits one enrollment request from source (an IP address without a
// port) or fails with enroll.rate_limited when the last minute already holds
// EnrollPerSource admitted requests from source or EnrollGlobal from all
// sources. A refused request is not counted.
func (l *EnrollLimiter) Allow(source string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.clock.Now()
	l.prune(now)
	if len(l.sources[source]) >= EnrollPerSource || len(l.all) >= EnrollGlobal {
		return errcode.New(errcode.EnrollRateLimited, "too many enrollment requests; wait a minute and try again")
	}
	l.all = append(l.all, now)
	l.sources[source] = append(l.sources[source], now)
	return nil
}

// prune drops every time that left the window and every source left with
// none.
func (l *EnrollLimiter) prune(now time.Time) {
	cutoff := now.Add(-enrollWindow)
	l.all = dropBefore(l.all, cutoff)
	for s, times := range l.sources {
		if times = dropBefore(times, cutoff); len(times) == 0 {
			delete(l.sources, s)
		} else {
			l.sources[s] = times
		}
	}
}

// dropBefore removes the leading times at or before cutoff: a request
// admitted exactly one minute ago has left the window.
func dropBefore(times []time.Time, cutoff time.Time) []time.Time {
	i := 0
	for i < len(times) && !times[i].After(cutoff) {
		i++
	}
	return times[i:]
}
