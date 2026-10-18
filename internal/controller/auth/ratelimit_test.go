package auth

import (
	"fmt"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core/coretest"
)

var t0 = time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC)

func TestUsernameLock(t *testing.T) {
	clock := coretest.NewFakeClock(t0)
	l := newLimiter(clock)
	for i := range UsernameFailureLimit - 1 {
		l.fail("alice", "10.0.0."+string(rune('1'+i)))
	}
	if d := l.blocked("alice", "10.0.0.9"); d != 0 {
		t.Fatalf("blocked after %d failures for %s, want allowed", UsernameFailureLimit-1, d)
	}
	clock.Advance(time.Minute)
	l.fail("alice", "10.0.0.9")
	if d := l.blocked("alice", "10.0.0.200"); d != LockDuration {
		t.Fatalf("after %d failures blocked for %s, want %s from any source", UsernameFailureLimit, d, LockDuration)
	}
	if d := l.blocked("bob", "10.0.0.9"); d != 0 {
		t.Errorf("another username is blocked for %s", d)
	}
	clock.Advance(LockDuration - time.Second)
	if d := l.blocked("alice", "10.0.0.9"); d != time.Second {
		t.Errorf("one second before the lock ends: blocked for %s, want 1s", d)
	}
	clock.Advance(time.Second)
	if d := l.blocked("alice", "10.0.0.9"); d != 0 {
		t.Errorf("lock outlived %s: blocked for %s", LockDuration, d)
	}
}

func TestFailuresOutsideTheWindowDoNotCount(t *testing.T) {
	clock := coretest.NewFakeClock(t0)
	l := newLimiter(clock)
	for range UsernameFailureLimit - 1 {
		l.fail("alice", "10.0.0.1")
	}
	clock.Advance(FailureWindow)
	l.fail("alice", "10.0.0.1")
	if d := l.blocked("alice", "10.0.0.1"); d != 0 {
		t.Errorf("failures older than %s counted: blocked for %s", FailureWindow, d)
	}
}

func TestSourceBlock(t *testing.T) {
	clock := coretest.NewFakeClock(t0)
	l := newLimiter(clock)
	for i := range SourceFailureLimit {
		l.fail("user"+string(rune('a'+i)), "10.0.0.1")
	}
	if d := l.blocked("fresh", "10.0.0.1"); d != LockDuration {
		t.Errorf("source after %d failures blocked for %s, want %s", SourceFailureLimit, d, LockDuration)
	}
	if d := l.blocked("fresh", "10.0.0.2"); d != 0 {
		t.Errorf("another source is blocked for %s", d)
	}
}

func TestBlockedReturnsTheLongerWait(t *testing.T) {
	clock := coretest.NewFakeClock(t0)
	l := newLimiter(clock)
	for i := range SourceFailureLimit {
		l.fail("user"+string(rune('a'+i)), "10.0.0.1")
	}
	clock.Advance(5 * time.Minute)
	for range UsernameFailureLimit {
		l.fail("alice", "10.0.0.2")
	}
	if d := l.blocked("alice", "10.0.0.1"); d != LockDuration {
		t.Errorf("blocked for %s, want the username's %s", d, LockDuration)
	}
	if d := l.blocked("bob", "10.0.0.1"); d != LockDuration-5*time.Minute {
		t.Errorf("blocked for %s, want the source's remaining %s", d, LockDuration-5*time.Minute)
	}
}

func TestStaleKeysArePruned(t *testing.T) {
	clock := coretest.NewFakeClock(t0)
	l := newLimiter(clock)
	for range UsernameFailureLimit {
		l.fail("locked", "10.0.0.1")
	}
	l.fail("stale", "10.0.0.2")
	clock.Advance(FailureWindow - time.Minute)
	l.fail("other", "10.0.0.3")
	clock.Advance(time.Minute)
	l.fail("new", "10.0.0.4")
	for _, k := range []string{"u:stale", "s:10.0.0.2"} {
		if _, ok := l.keys[k]; ok {
			t.Errorf("key %s survived its window", k)
		}
	}
	for _, k := range []string{"u:other", "s:10.0.0.3", "u:new", "s:10.0.0.4"} {
		if _, ok := l.keys[k]; !ok {
			t.Errorf("key %s was pruned inside its window", k)
		}
	}
	if _, ok := l.keys["u:locked"]; ok {
		t.Error("a key whose lock ended was kept")
	}
}

func TestLockedKeyIsKeptAfterItsFailuresAge(t *testing.T) {
	clock := coretest.NewFakeClock(t0)
	l := newLimiter(clock)
	for range UsernameFailureLimit {
		l.fail("alice", "10.0.0.1")
	}
	clock.Advance(LockDuration - time.Minute)
	l.fail("bob", "10.0.0.2")
	if d := l.blocked("alice", "10.0.0.3"); d != time.Minute {
		t.Errorf("lock dropped by pruning: blocked for %s, want 1m", d)
	}
}

func TestKeyCapDropsTheOldest(t *testing.T) {
	clock := coretest.NewFakeClock(t0)
	l := newLimiter(clock)
	l.max = 3
	l.fail("a", "x") // u:a, s:x at t0
	clock.Advance(time.Second)
	l.fail("a", "y") // u:a and s:y at t0+1s
	clock.Advance(time.Second)
	l.fail("b", "y") // u:b is new: s:x, the oldest, is dropped
	if len(l.keys) != 3 {
		t.Fatalf("%d keys kept, want the cap of 3", len(l.keys))
	}
	if _, ok := l.keys["s:x"]; ok {
		t.Error("the oldest key was kept")
	}
	for _, k := range []string{"u:a", "s:y", "u:b"} {
		if _, ok := l.keys[k]; !ok {
			t.Errorf("key %s was dropped", k)
		}
	}
	if MaxLimiterKeys != 10000 || newLimiter(clock).max != MaxLimiterKeys {
		t.Errorf("default cap is not %d", 10000)
	}
}

func TestLockSurvivesAFloodOfNewKeys(t *testing.T) {
	clock := coretest.NewFakeClock(t0)
	l := newLimiter(clock)
	for range UsernameFailureLimit {
		l.fail("alice", "10.0.0.1")
	}
	clock.Advance(time.Minute)
	for i := range MaxLimiterKeys {
		l.fail(fmt.Sprintf("user%d", i), fmt.Sprintf("10.%d.%d.%d", 1+i>>16, i>>8&0xff, i&0xff))
	}
	if len(l.keys) > MaxLimiterKeys {
		t.Fatalf("%d keys kept, above the cap of %d", len(l.keys), MaxLimiterKeys)
	}
	if d := l.blocked("alice", "192.0.2.1"); d != LockDuration-time.Minute {
		t.Errorf("after %d other failing usernames alice is blocked for %s, want %s", MaxLimiterKeys, d, LockDuration-time.Minute)
	}
}

func TestFullOfLockedKeysFailsClosed(t *testing.T) {
	clock := coretest.NewFakeClock(t0)
	l := newLimiter(clock)
	for range UsernameFailureLimit {
		l.fail("alice", "10.0.0.1") // u:alice locked until t0+15m
	}
	clock.Advance(5 * time.Minute)
	for range SourceFailureLimit - UsernameFailureLimit {
		l.fail("bob", "10.0.0.1") // u:bob and s:10.0.0.1 locked until t0+20m
	}
	l.max = 3
	if len(l.keys) != 3 {
		t.Fatalf("%d keys, want 3 locked keys", len(l.keys))
	}
	if d := l.blocked("carol", "10.0.0.9"); d != 10*time.Minute {
		t.Errorf("new keys while every key is locked: blocked for %s, want the shortest lock, 10m", d)
	}
	if d := l.blocked("bob", "10.0.0.9"); d != LockDuration {
		t.Errorf("known locked username with a new source: blocked for %s, want %s", d, LockDuration)
	}
	l.fail("carol", "10.0.0.9")
	if len(l.keys) != 3 || l.keys["u:carol"] != nil {
		t.Errorf("a failure displaced a locked key: %d keys, carol recorded %v", len(l.keys), l.keys["u:carol"] != nil)
	}
	clock.Advance(10 * time.Minute)
	if d := l.blocked("carol", "10.0.0.9"); d != 0 {
		t.Errorf("after the first lock ended: blocked for %s, want allowed", d)
	}
}

func TestSourceKey(t *testing.T) {
	for _, tt := range []struct{ in, want string }{
		{"10.0.0.1", "s:10.0.0.1"},
		{"10.0.0.1:52000", "s:10.0.0.1"},
		{"::ffff:10.0.0.1", "s:10.0.0.1"},
		{"2001:db8:1:2::1", "s:2001:db8:1:2::/64"},
		{"[2001:db8:1:2:ffff::9]:443", "s:2001:db8:1:2::/64"},
		{"fe80::1%eth0", "s:fe80::/64"},
		{"socket", "s:socket"},
	} {
		if got := sourceKey(tt.in); got != tt.want {
			t.Errorf("sourceKey(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestOneIPv6PrefixSharesACounter(t *testing.T) {
	clock := coretest.NewFakeClock(t0)
	l := newLimiter(clock)
	for i := range SourceFailureLimit {
		l.fail(fmt.Sprintf("user%d", i), fmt.Sprintf("2001:db8:1:2::%x", i+1))
	}
	if d := l.blocked("fresh", "2001:db8:1:2:ffff::9"); d != LockDuration {
		t.Errorf("another address of the /64 is blocked for %s, want %s", d, LockDuration)
	}
	if d := l.blocked("fresh", "2001:db8:1:3::1"); d != 0 {
		t.Errorf("an address of another /64 is blocked for %s", d)
	}
}

func TestRetryAfterSeconds(t *testing.T) {
	for _, tt := range []struct {
		d    time.Duration
		want int
	}{
		{15 * time.Minute, 900},
		{1500 * time.Millisecond, 2},
		{time.Millisecond, 1},
		{0, 1},
	} {
		if got := (&RetryAfterError{After: tt.d}).Seconds(); got != tt.want {
			t.Errorf("Seconds(%s) = %d, want %d", tt.d, got, tt.want)
		}
	}
}
