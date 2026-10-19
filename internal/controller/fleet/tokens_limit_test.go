package fleet

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// limiterStart is the fake clock's start for the limiter tests.
var limiterStart = time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)

func wantRateLimited(t *testing.T, err error, what string) {
	t.Helper()
	var coded *errcode.Error
	if !errors.As(err, &coded) || coded.Code != errcode.EnrollRateLimited {
		t.Fatalf("%s: error = %v, want %s", what, err, errcode.EnrollRateLimited)
	}
}

func TestEnrollLimiterPerSource(t *testing.T) {
	clock := coretest.NewFakeClock(limiterStart)
	l := NewEnrollLimiter(clock)
	for i := range EnrollPerSource {
		if err := l.Allow("192.0.2.1"); err != nil {
			t.Fatalf("request %d: %v", i+1, err)
		}
	}
	wantRateLimited(t, l.Allow("192.0.2.1"), "request above the per-source limit")
	if err := l.Allow("192.0.2.2"); err != nil {
		t.Fatalf("another source was refused: %v", err)
	}
}

func TestEnrollLimiterGlobal(t *testing.T) {
	clock := coretest.NewFakeClock(limiterStart)
	l := NewEnrollLimiter(clock)
	for i := range EnrollGlobal {
		source := fmt.Sprintf("198.51.100.%d", i/EnrollPerSource)
		if err := l.Allow(source); err != nil {
			t.Fatalf("request %d from %s: %v", i+1, source, err)
		}
	}
	wantRateLimited(t, l.Allow("203.0.113.9"), "request above the global limit")
	clock.Advance(time.Minute)
	if err := l.Allow("203.0.113.9"); err != nil {
		t.Fatalf("request a minute later: %v", err)
	}
}

func TestEnrollLimiterWindowSlides(t *testing.T) {
	clock := coretest.NewFakeClock(limiterStart)
	l := NewEnrollLimiter(clock)
	for range EnrollPerSource / 2 {
		if err := l.Allow("192.0.2.1"); err != nil {
			t.Fatal(err)
		}
	}
	clock.Advance(30 * time.Second)
	for range EnrollPerSource - EnrollPerSource/2 {
		if err := l.Allow("192.0.2.1"); err != nil {
			t.Fatal(err)
		}
	}
	wantRateLimited(t, l.Allow("192.0.2.1"), "full window")
	// The first half leaves the window exactly one minute after it was admitted.
	clock.Advance(30 * time.Second)
	for i := range EnrollPerSource / 2 {
		if err := l.Allow("192.0.2.1"); err != nil {
			t.Fatalf("request %d after the first half expired: %v", i+1, err)
		}
	}
	wantRateLimited(t, l.Allow("192.0.2.1"), "refilled window")
}

func TestEnrollLimiterDoesNotCountRefusals(t *testing.T) {
	clock := coretest.NewFakeClock(limiterStart)
	l := NewEnrollLimiter(clock)
	for range EnrollPerSource {
		if err := l.Allow("192.0.2.1"); err != nil {
			t.Fatal(err)
		}
	}
	clock.Advance(59 * time.Second)
	for range 100 {
		wantRateLimited(t, l.Allow("192.0.2.1"), "refused request")
	}
	clock.Advance(time.Second)
	if err := l.Allow("192.0.2.1"); err != nil {
		t.Fatalf("refused requests extended the limit: %v", err)
	}
}

func TestEnrollLimiterForgetsQuietSources(t *testing.T) {
	clock := coretest.NewFakeClock(limiterStart)
	l := NewEnrollLimiter(clock)
	for _, s := range []string{"192.0.2.1", "192.0.2.2", "192.0.2.3"} {
		if err := l.Allow(s); err != nil {
			t.Fatal(err)
		}
	}
	clock.Advance(time.Minute)
	if err := l.Allow("192.0.2.4"); err != nil {
		t.Fatal(err)
	}
	if n := len(l.sources); n != 1 {
		t.Fatalf("tracked sources = %d after a quiet minute, want 1", n)
	}
}
