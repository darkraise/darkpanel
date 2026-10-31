package docker

import (
	"context"
	"sync"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
)

const (
	// pullBacklog is the number of a change's latest events a new
	// subscriber receives first.
	pullBacklog = 256
	// pullBuffer is the capacity of one subscriber's channel; it holds a
	// whole backlog, so a new subscriber never waits for it.
	pullBuffer = 1024
	// pullKeep is how long a finished change's backlog stays readable.
	pullKeep = 10 * time.Minute
)

// pullHub carries the progress events of image pulls from the apply that
// publishes them to the streams that read them, keyed by change id.
type pullHub struct {
	clock core.Clock
	mu    sync.Mutex
	feeds map[string]*pullFeed
}

// pullFeed is one change's events.
type pullFeed struct {
	backlog  []PullEvent // the latest pullBacklog events, oldest first
	subs     map[*pullSub]struct{}
	finished bool
}

// pullSub is one subscription. closed is guarded by the hub's mutex.
type pullSub struct {
	ch         chan PullEvent
	closed     bool
	unregister func() bool
}

// newPullHub returns an empty hub whose finished backlogs expire on clock.
func newPullHub(clock core.Clock) *pullHub {
	return &pullHub{clock: clock, feeds: map[string]*pullFeed{}}
}

// feed returns the change's feed, creating it when absent. The caller holds
// h.mu.
func (h *pullHub) feed(changeID string) *pullFeed {
	f, ok := h.feeds[changeID]
	if !ok {
		f = &pullFeed{subs: map[*pullSub]struct{}{}}
		h.feeds[changeID] = f
	}
	return f
}

// publish records e as the change's latest event and hands it to every
// subscriber. It does nothing once the change finished.
func (h *pullHub) publish(changeID string, e PullEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()
	f := h.feed(changeID)
	if f.finished {
		return
	}
	f.backlog = append(f.backlog, e)
	if len(f.backlog) > pullBacklog {
		f.backlog = f.backlog[len(f.backlog)-pullBacklog:]
	}
	for s := range f.subs {
		select {
		case s.ch <- e:
		default:
			// A full channel means the reader fell behind. Dropping it keeps
			// the pull from waiting on a reader; it has missed events, and
			// a new subscription starts again from the backlog.
			s.closed = true
			close(s.ch)
			delete(f.subs, s)
		}
	}
}

// finish ends the change: its subscribers' channels close, later events are
// ignored, and its backlog is dropped after pullKeep.
func (h *pullHub) finish(changeID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	// The feed is created when absent so that a change which fails before
	// its first event still ends the subscribers waiting for it.
	f := h.feed(changeID)
	if f.finished {
		return
	}
	f.finished = true
	for s := range f.subs {
		s.closed = true
		close(s.ch)
	}
	f.subs = nil
	h.clock.AfterFunc(pullKeep, func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		if h.feeds[changeID] == f {
			delete(h.feeds, changeID)
		}
	})
}

// subscribe returns a channel that receives the change's backlog and then
// its live events, and closes when the change finishes, when ctx ends or
// when the returned stop function runs. stop is idempotent.
func (h *pullHub) subscribe(ctx context.Context, changeID string) (<-chan PullEvent, func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	f := h.feed(changeID)
	s := &pullSub{ch: make(chan PullEvent, pullBuffer)}
	for _, e := range f.backlog {
		s.ch <- e
	}
	if f.finished {
		s.closed = true
		close(s.ch)
	} else {
		f.subs[s] = struct{}{}
	}
	stop := func() { h.unsubscribe(changeID, f, s) }
	// Registered under h.mu, so a stop that ctx runs at once still sees it.
	s.unregister = context.AfterFunc(ctx, stop)
	return s.ch, stop
}

// unsubscribe ends s, a subscription to f. It deletes f when f is still
// unfinished with no events and no subscribers, so change ids nobody
// published do not accumulate.
func (h *pullHub) unsubscribe(changeID string, f *pullFeed, s *pullSub) {
	h.mu.Lock()
	defer h.mu.Unlock()
	s.unregister()
	if s.closed {
		return
	}
	s.closed = true
	close(s.ch)
	delete(f.subs, s)
	if h.feeds[changeID] == f && !f.finished && len(f.backlog) == 0 && len(f.subs) == 0 {
		delete(h.feeds, changeID)
	}
}
