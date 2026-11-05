package cloud

import (
	"math/big"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/cloud/cloudfake"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// TestRaRetryAfterOverflow pins that a Retry-After over the 60 second bound ends the retries at once, however
// large the number: one whose seconds overflow time.Duration, or int, must not become a wait of a nanosecond or
// the default backoff.
func TestRaRetryAfterOverflow(t *testing.T) {
	start := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name, retryAfter string
		refused          bool
	}{
		{"at the bound", "60", false},
		{"one second over", "61", true},
		{"overflows Duration", "9223372037", true},
		{"largest int64", "9223372036854775807", true},
		{"beyond int64", "99999999999999999999", true},
		{"padded with zeros", "0000000000000000000000000000000061", true},
		{"far future date", "Fri, 31 Dec 9999 23:59:59 GMT", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := cloudfake.New(t)
			fake.Handle(http.MethodGet, "/api/x", clOK)
			fake.Fail(http.MethodGet, "/api/x", cloudfake.Fault{Status: 429, RetryAfter: tc.retryAfter})
			c, sleeps := clClient(t, fake, func(cfg *Config) { cfg.Clock = coretest.NewFakeClock(start) })
			_, err := c.Do(t.Context(), Request{Method: http.MethodGet, Path: "/x"})
			if !tc.refused {
				if err != nil {
					t.Fatal(err)
				}
				if got := sleeps.got(); !slices.Equal(got, []time.Duration{60 * time.Second}) {
					t.Fatalf("delays = %v, want one of 60s", got)
				}
				return
			}
			if errcode.From(err).Code != errcode.CloudRateLimited {
				t.Fatalf("err = %v, want cloud.rate_limited without waiting", err)
			}
			if got := sleeps.got(); len(got) != 0 {
				t.Fatalf("delays = %v, want none", got)
			}
			if n := len(fake.Requests()); n != 1 {
				t.Fatalf("sent %d requests, want 1", n)
			}
		})
	}
}

// TestRaRetryAfterNegative pins that a negative Retry-After, even one beyond int64, reads as none given, so the
// default backoff applies rather than the refusal a number over the bound gets.
func TestRaRetryAfterNegative(t *testing.T) {
	c := &Client{cfg: Config{Clock: coretest.NewFakeClock(time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC))}}
	for _, v := range []string{"-1", "-9223372036854775808", "-99999999999999999999"} {
		if got := c.retryAfter(v); got != 0 {
			t.Errorf("retryAfter(%q) = %v, want 0: none given", v, got)
		}
	}
}

func fz10Head(s string) string {
	if len(s) > 64 {
		return s[:64]
	}
	return s
}

// FuzzRetryAfter checks that retryAfter never returns a negative wait, and that a Retry-After of more than 60
// whole seconds, however many digits it has, gives a wait above maxRetryAfter.
func FuzzRetryAfter(f *testing.F) {
	start := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	for _, v := range []string{
		"", "0", "7", "60", "61", "3600", "+61", "0061", "-1", "soon",
		"9223372036", "9223372037", "9223372036854775807", "9223372036854775808", "99999999999999999999", "-99999999999999999999",
		start.Add(30 * time.Second).Format(http.TimeFormat), start.Add(-time.Hour).Format(http.TimeFormat),
		"Fri, 31 Dec 9999 23:59:59 GMT",
	} {
		f.Add(v)
	}
	c := &Client{cfg: Config{Clock: coretest.NewFakeClock(start)}}
	sixty := big.NewInt(60)
	f.Fuzz(func(t *testing.T, v string) {
		got := c.retryAfter(v)
		if got < 0 {
			t.Fatalf("retryAfter(%q) = %v, want a wait of zero or more", fz10Head(v), got)
		}
		if n, ok := new(big.Int).SetString(v, 10); ok && n.Cmp(sixty) > 0 && got <= maxRetryAfter {
			t.Fatalf("retryAfter(%q) = %v, want more than %v", fz10Head(v), got, maxRetryAfter)
		}
	})
}
