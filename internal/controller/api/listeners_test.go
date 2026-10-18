package api

import (
	"sync"
	"testing"
)

func TestListenerState(t *testing.T) {
	h := newHarness(t)
	notStarted := ListenerState{Message: "The listeners are not started yet."}
	if got := h.srv.listenerState(); got != notStarted {
		t.Errorf("before a source is set: %+v, want %+v", got, notStarted)
	}
	bound := ListenerState{TailscaleBound: true, Message: "Serving on Tailscale: https://100.64.0.1:8443."}
	h.srv.SetListenerState(func() ListenerState { return bound })
	if got := h.srv.listenerState(); got != bound {
		t.Errorf("after SetListenerState: %+v, want %+v", got, bound)
	}
	h.srv.SetListenerState(nil)
	if got := h.srv.listenerState(); got != notStarted {
		t.Errorf("after SetListenerState(nil): %+v, want %+v", got, notStarted)
	}
}

// TestListenerStateConcurrent sets the source while requests read it; the
// race detector checks the hand-over.
func TestListenerStateConcurrent(t *testing.T) {
	h := newHarness(t)
	loopback := ListenerState{Message: "No Tailscale address found; serving loopback only."}
	var wg sync.WaitGroup
	wg.Go(func() {
		for range 100 {
			h.srv.SetListenerState(func() ListenerState { return loopback })
		}
	})
	for range 4 {
		wg.Go(func() {
			for range 100 {
				if got := h.srv.listenerState(); got.TailscaleBound || got.Message == "" {
					t.Errorf("read %+v during SetListenerState", got)
					return
				}
			}
		})
	}
	wg.Wait()
	if got := h.srv.listenerState(); got != loopback {
		t.Errorf("after the writes: %+v, want %+v", got, loopback)
	}
}
