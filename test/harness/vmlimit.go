//go:build harness

package harness

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"sync"
)

var vmsFlag = flag.Int("vms", 3, "most VMs the harness runs at once; a test that boots more fails")

// vmLimiter bounds how many VMs run at once. A boot takes all its slots at
// once, so two tests that each wait for a part of their VMs cannot hold
// each other's slots forever.
type vmLimiter struct {
	mu    sync.Mutex
	limit int
	used  int
	wake  chan struct{} // closed and replaced whenever slots are released
}

func newVMLimiter(limit int) *vmLimiter {
	return &vmLimiter{limit: limit, wake: make(chan struct{})}
}

// acquire takes n slots, waiting until they are free or ctx ends. More
// slots than the limit can never be free, so that fails at once.
func (l *vmLimiter) acquire(ctx context.Context, n int) error {
	if n > l.limit {
		return fmt.Errorf("harness: %d VMs at once, but -vms allows %d", n, l.limit)
	}
	for {
		l.mu.Lock()
		if l.used+n <= l.limit {
			l.used += n
			l.mu.Unlock()
			return nil
		}
		wake := l.wake
		l.mu.Unlock()
		select {
		case <-ctx.Done():
			return fmt.Errorf("harness: waiting for %d VM slots: %w", n, ctx.Err())
		case <-wake:
		}
	}
}

// release returns n slots.
func (l *vmLimiter) release(n int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.used -= n
	if l.used < 0 {
		panic(errors.New("harness: more VM slots released than acquired"))
	}
	close(l.wake)
	l.wake = make(chan struct{})
}

var (
	vmSlotsOnce sync.Once
	vmSlotsLim  *vmLimiter
)

// vmSlots is the process's VM limiter, sized by -vms, which is parsed by
// the time a test runs.
func vmSlots() *vmLimiter {
	vmSlotsOnce.Do(func() { vmSlotsLim = newVMLimiter(max(*vmsFlag, 1)) })
	return vmSlotsLim
}
