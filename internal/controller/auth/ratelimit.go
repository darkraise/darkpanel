package auth

import (
	"container/list"
	"fmt"
	"net/netip"
	"sync"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
)

// Login rate limits (spec section 9).
const (
	UsernameFailureLimit = 5
	SourceFailureLimit   = 20
	FailureWindow        = 15 * time.Minute
	LockDuration         = 15 * time.Minute
	MaxLimiterKeys       = 10000
)

// RetryAfterError is the cause inside an auth.rate_limited error: how long
// the client must wait before its next login attempt is evaluated.
type RetryAfterError struct {
	After time.Duration
}

func (e *RetryAfterError) Error() string { return fmt.Sprintf("retry after %s", e.After) }

// Seconds is the Retry-After header value: whole seconds rounded up, at least
// 1.
func (e *RetryAfterError) Seconds() int {
	s := int((e.After + time.Second - 1) / time.Second)
	return max(s, 1)
}

type counter struct {
	key         string
	failures    []time.Time
	lockedUntil time.Time
	last        time.Time
	elem        *list.Element
}

func (c *counter) locked(now time.Time) bool { return now.Before(c.lockedUntil) }

// limiter counts failed logins per username ("u:" keys) and per source
// address ("s:" keys) in memory. At the key cap it evicts only unlocked keys,
// so flooding it with fresh keys cannot clear a running lock; when every key
// is locked, attempts that would need a new key are refused instead.
//
// order holds the counters least recently failed first. A lock starts at a
// failure and lasts as long as the window, so a locked counter's last failure
// is always inside the window: walking from the front, every counter before
// the first one inside the window is stale and unlocked.
type limiter struct {
	mu    sync.Mutex
	clock core.Clock
	keys  map[string]*counter
	order *list.List
	max   int
}

func newLimiter(clock core.Clock) *limiter {
	return &limiter{clock: clock, keys: map[string]*counter{}, order: list.New(), max: MaxLimiterKeys}
}

// sourceKey normalises a client address: an IPv4-mapped IPv6 address becomes
// its IPv4 form and any other IPv6 address its /64 prefix, because one host
// commonly holds a whole /64; IPv4 stays whole. A port is dropped, and a
// value that is not an address is used as it is.
func sourceKey(source string) string {
	addr, err := netip.ParseAddr(source)
	if err != nil {
		ap, perr := netip.ParseAddrPort(source)
		if perr != nil {
			return "s:" + source
		}
		addr = ap.Addr()
	}
	addr = addr.WithZone("").Unmap()
	if addr.Is6() {
		p, _ := addr.Prefix(64)
		return "s:" + p.String()
	}
	return "s:" + addr.String()
}

func usernameKey(username string) string { return "u:" + username }

// blocked returns how long username or source stays locked, the longer of
// the two, or 0 when the attempt may be evaluated. When the table is full of
// locked keys and either key is new, the attempt could not be counted, so it
// is refused until the first lock ends.
func (l *limiter) blocked(username, source string) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.clock.Now()
	if len(l.keys) >= l.max {
		l.prune(now)
	}
	var wait time.Duration
	unknown := false
	for _, k := range []string{usernameKey(username), sourceKey(source)} {
		c, ok := l.keys[k]
		switch {
		case !ok:
			unknown = true
		case c.locked(now):
			wait = max(wait, c.lockedUntil.Sub(now))
		}
	}
	if unknown && len(l.keys) >= l.max {
		if first, full := l.allLocked(now); full {
			wait = max(wait, first)
		}
	}
	return wait
}

// allLocked reports whether every key is locked and, if so, the shortest
// remaining lock.
func (l *limiter) allLocked(now time.Time) (time.Duration, bool) {
	var first time.Duration
	for e := l.order.Front(); e != nil; e = e.Next() {
		c := e.Value.(*counter)
		if !c.locked(now) {
			return 0, false
		}
		if d := c.lockedUntil.Sub(now); first == 0 || d < first {
			first = d
		}
	}
	return first, true
}

// fail records one failed login for username and source. Neither key is
// evicted to make room for the other: with one free slot they would displace
// each other on every attempt and neither would count past one failure.
func (l *limiter) fail(username, source string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.clock.Now()
	l.prune(now)
	u, s := usernameKey(username), sourceKey(source)
	l.record(u, UsernameFailureLimit, now, s)
	l.record(s, SourceFailureLimit, now, u)
}

// prune drops every key left with neither failures inside the window nor a
// running lock.
func (l *limiter) prune(now time.Time) {
	for e := l.order.Front(); e != nil; {
		c := e.Value.(*counter)
		if now.Sub(c.last) < FailureWindow {
			return
		}
		e = e.Next()
		l.remove(c)
	}
}

func (l *limiter) remove(c *counter) {
	l.order.Remove(c.elem)
	delete(l.keys, c.key)
}

func recent(failures []time.Time, now time.Time) []time.Time {
	kept := failures[:0]
	for _, f := range failures {
		if now.Sub(f) < FailureWindow {
			kept = append(kept, f)
		}
	}
	return kept
}

// record counts a failure for key. A new key that finds the table full takes
// the place of the oldest unlocked key other than keep; when there is none it
// is not recorded (when every key is locked, blocked refuses such attempts
// before they are evaluated).
func (l *limiter) record(key string, limit int, now time.Time, keep string) {
	c, ok := l.keys[key]
	if !ok {
		if len(l.keys) >= l.max && !l.dropOldestUnlocked(now, keep) {
			return
		}
		c = &counter{key: key}
		c.elem = l.order.PushBack(c)
		l.keys[key] = c
	}
	c.last = now
	l.order.MoveToBack(c.elem)
	c.failures = append(recent(c.failures, now), now)
	if len(c.failures) >= limit {
		c.lockedUntil = now.Add(LockDuration)
		c.failures = nil
	}
}

func (l *limiter) dropOldestUnlocked(now time.Time, keep string) bool {
	for e := l.order.Front(); e != nil; e = e.Next() {
		if c := e.Value.(*counter); !c.locked(now) && c.key != keep {
			l.remove(c)
			return true
		}
	}
	return false
}
