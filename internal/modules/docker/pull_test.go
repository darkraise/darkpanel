package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

func plClock() *coretest.FakeClock {
	return coretest.NewFakeClock(time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC))
}

func plEvent(n int) PullEvent { return PullEvent{Status: fmt.Sprintf("e%d", n)} }

func plStatuses(events []PullEvent) []string {
	out := make([]string, len(events))
	for i, e := range events {
		out[i] = e.Status
	}
	return out
}

// plRecv takes one event from ch and fails the test when none arrives.
func plRecv(t *testing.T, ch <-chan PullEvent) PullEvent {
	t.Helper()
	select {
	case e, ok := <-ch:
		if !ok {
			t.Fatal("channel closed, want an event")
		}
		return e
	case <-time.After(5 * time.Second):
		t.Fatal("no event within 5 seconds")
	}
	return PullEvent{}
}

// plDrain reads ch until it closes and fails the test when it stays open.
func plDrain(t *testing.T, ch <-chan PullEvent) []PullEvent {
	t.Helper()
	var got []PullEvent
	timeout := time.After(5 * time.Second)
	for {
		select {
		case e, ok := <-ch:
			if !ok {
				return got
			}
			got = append(got, e)
		case <-timeout:
			t.Fatalf("channel still open after %d events", len(got))
		}
	}
}

func plSame(t *testing.T, got []PullEvent, want ...string) {
	t.Helper()
	g := plStatuses(got)
	if len(g) != len(want) {
		t.Fatalf("statuses %v, want %v", g, want)
	}
	for i := range want {
		if g[i] != want[i] {
			t.Fatalf("statuses %v, want %v", g, want)
		}
	}
}

func TestPLHubReplaysTheBacklogOfAFinishedChange(t *testing.T) {
	h := newPullHub(plClock())
	for i := 1; i <= 3; i++ {
		h.publish("c1", plEvent(i))
	}
	h.publish("c2", plEvent(99))
	h.finish("c1")
	h.publish("c1", plEvent(4))

	ch, stop := h.subscribe(context.Background(), "c1")
	defer stop()
	plSame(t, plDrain(t, ch), "e1", "e2", "e3")
}

func TestPLHubDeliversLiveEventsToAnEarlySubscriber(t *testing.T) {
	h := newPullHub(plClock())
	ch, stop := h.subscribe(context.Background(), "c1")
	defer stop()
	go func() {
		for i := 1; i <= 200; i++ {
			h.publish("c1", plEvent(i))
		}
		h.finish("c1")
	}()
	got := plDrain(t, ch)
	if len(got) != 200 {
		t.Fatalf("%d events, want 200", len(got))
	}
	for i, e := range got {
		if e.Status != fmt.Sprintf("e%d", i+1) {
			t.Fatalf("event %d is %s, want e%d", i, e.Status, i+1)
		}
	}
}

func TestPLHubBacklogHoldsTheLast256ThenGoesLive(t *testing.T) {
	h := newPullHub(plClock())
	for i := 1; i <= 300; i++ {
		h.publish("c1", plEvent(i))
	}
	ch, stop := h.subscribe(context.Background(), "c1")
	defer stop()
	for i := 45; i <= 300; i++ {
		if got := plRecv(t, ch).Status; got != fmt.Sprintf("e%d", i) {
			t.Fatalf("backlog event is %s, want e%d", got, i)
		}
	}
	h.publish("c1", plEvent(301))
	h.finish("c1")
	plSame(t, plDrain(t, ch), "e301")
}

func TestPLHubDropsAFinishedBacklogAfterTenMinutes(t *testing.T) {
	clock := plClock()
	h := newPullHub(clock)
	h.publish("c1", plEvent(1))
	h.finish("c1")

	clock.Advance(10*time.Minute - time.Second)
	ch, stop := h.subscribe(context.Background(), "c1")
	plSame(t, plDrain(t, ch), "e1")
	stop()

	clock.Advance(time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	late, stopLate := h.subscribe(ctx, "c1")
	defer stopLate()
	cancel()
	if got := plDrain(t, late); len(got) != 0 {
		t.Errorf("a dropped backlog replayed %v", plStatuses(got))
	}
}

func TestPLHubKeepsChangesApart(t *testing.T) {
	h := newPullHub(plClock())
	h.publish("c1", plEvent(1))
	h.publish("c2", plEvent(2))
	h.finish("c1")
	h.finish("c2")
	ch, stop := h.subscribe(context.Background(), "c2")
	defer stop()
	plSame(t, plDrain(t, ch), "e2")
}

func TestPLHubNeverBlocksOnASubscriberThatDoesNotRead(t *testing.T) {
	h := newPullHub(plClock())
	slow, stop := h.subscribe(context.Background(), "c1")
	defer stop()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 5000; i++ {
			h.publish("c1", plEvent(i))
		}
		h.finish("c1")
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("publish blocked on a subscriber that does not read")
	}

	got := plDrain(t, slow)
	if len(got) == 0 || len(got) >= 5000 {
		t.Fatalf("the slow subscriber kept %d of 5000 events, want some but not all", len(got))
	}
	for i, e := range got {
		if e.Status != fmt.Sprintf("e%d", i) {
			t.Fatalf("kept event %d is %s, want e%d", i, e.Status, i)
		}
	}

	late, stopLate := h.subscribe(context.Background(), "c1")
	defer stopLate()
	replay := plStatuses(plDrain(t, late))
	if len(replay) != 256 || replay[0] != "e4744" || replay[255] != "e4999" {
		t.Errorf("a late subscriber got %d events %v, want the last 256, e4744 to e4999", len(replay), replay)
	}
}

func TestPLHubStopAndContextEndCloseTheChannel(t *testing.T) {
	h := newPullHub(plClock())

	ch, stop := h.subscribe(context.Background(), "c1")
	stop()
	if got := plDrain(t, ch); len(got) != 0 {
		t.Errorf("a stopped subscription delivered %v", plStatuses(got))
	}
	h.publish("c1", plEvent(1))
	stop()

	ctx, cancel := context.WithCancel(context.Background())
	byCtx, stopCtx := h.subscribe(ctx, "c2")
	defer stopCtx()
	cancel()
	if got := plDrain(t, byCtx); len(got) != 0 {
		t.Errorf("a subscription whose context ended delivered %v", plStatuses(got))
	}
}

func TestPLHubMarksADroppedSubscriberLagged(t *testing.T) {
	h := newPullHub(plClock())
	slow, stop := h.subscribe(context.Background(), "c1")
	for i := 0; i < 5000; i++ {
		h.publish("c1", plEvent(i))
	}
	if !h.lagged(slow) {
		t.Error("a subscriber dropped for falling behind is not lagged")
	}
	stop()
	if h.lagged(slow) {
		t.Error("a stopped subscriber is still lagged")
	}

	done, stopDone := h.subscribe(context.Background(), "c2")
	defer stopDone()
	h.publish("c2", plEvent(1))
	h.finish("c2")
	plDrain(t, done)
	if h.lagged(done) {
		t.Error("a subscriber closed by finish is lagged")
	}
}

func TestPLStreamEndsWithUnavailableWhenTheReaderFallsBehind(t *testing.T) {
	h := newDKHost(t)
	hub := newPullHub(h.clock)
	def := h.dk().pullStreamDef(hub)
	hub.publish("c1", plEvent(0))

	first := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	done := make(chan error, 1)
	go func() {
		done <- def.Run(context.Background(), h.env(""), json.RawMessage(`{"changeId":"c1"}`), func(any) error {
			once.Do(func() {
				close(first)
				<-release
			})
			return nil
		})
	}()
	select {
	case <-first:
	case <-time.After(5 * time.Second):
		t.Fatal("the stream emitted nothing within 5 seconds")
	}
	for i := 1; i <= 2000; i++ {
		hub.publish("c1", plEvent(i))
	}
	close(release)

	select {
	case err := <-done:
		if got := plCode(t, err); got != errcode.Unavailable {
			t.Errorf("Run = %v (code %s), want %s", err, got, errcode.Unavailable)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return within 5 seconds of a dropped reader")
	}
}
