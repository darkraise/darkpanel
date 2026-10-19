package transport

import (
	"math/rand/v2"
	"time"
)

// Bounds of the agent's redial delay.
const (
	BackoffMin = time.Second
	BackoffMax = time.Minute
)

// Backoff spaces the agent's redials: exponential from BackoffMin to
// BackoffMax with full jitter, so agents that lost a controller together do
// not return together. It is not safe for concurrent use.
type Backoff struct {
	attempt int
	rand    func(n int64) int64 // a uniform value in [0, n)
}

// NewBackoff returns a backoff at its first delay.
func NewBackoff() *Backoff {
	return &Backoff{rand: rand.Int64N} //nolint:gosec // G404: jitter needs no unpredictability
}

// Next returns a delay drawn uniformly from [0, min(BackoffMax,
// BackoffMin*2^n)] for the n-th call since the last Reset.
func (b *Backoff) Next() time.Duration {
	ceiling := BackoffMin
	for i := 0; i < b.attempt && ceiling < BackoffMax; i++ {
		ceiling *= 2
	}
	if ceiling < BackoffMax {
		b.attempt++
	}
	ceiling = min(ceiling, BackoffMax)
	return time.Duration(b.rand(int64(ceiling) + 1))
}

// Reset returns the backoff to its first delay, after a session that came up.
func (b *Backoff) Reset() { b.attempt = 0 }
