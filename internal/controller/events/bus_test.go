package events

import (
	"bytes"
	"context"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// busRecv receives one event from ch within five seconds.
func busRecv(t *testing.T, ch <-chan Event) Event {
	t.Helper()
	select {
	case ev, ok := <-ch:
		if !ok {
			t.Fatal("the subscription closed")
		}
		return ev
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for an event")
	}
	return Event{}
}

// busEmpty fails unless ch holds no event right now.
func busEmpty(t *testing.T, ch <-chan Event) {
	t.Helper()
	select {
	case ev := <-ch:
		t.Fatalf("unexpected event %+v", ev)
	default:
	}
}

// busPublish publishes n change events whose data carries their index.
func busPublish(b *Bus, n int) {
	for i := range n {
		b.Publish(TypeChange, map[string]int{"i": i})
	}
}

func TestBusPublishSubscribe(t *testing.T) {
	b := NewBus(nil)
	b.Publish(TypeHost, HostEvent{HostID: "h0", Online: true})
	ctx, cancel := context.WithCancel(context.Background())
	ch, last := b.Subscribe(ctx)
	if last != 1 {
		t.Fatalf("Subscribe's last seq = %d, want 1", last)
	}
	for i := range 3 {
		b.Publish(TypeChange, ChangeEvent{HostID: "h1", ChangeID: "c" + string(rune('1'+i)), Action: "demo.item.add",
			State: "completed", UpdatedAt: time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)})
	}
	for i := range 3 {
		ev := busRecv(t, ch)
		want := `{"hostId":"h1","changeId":"c` + string(rune('1'+i)) +
			`","action":"demo.item.add","state":"completed","updatedAt":"2026-10-22T12:00:00Z"}`
		if ev.Seq != uint64(i+2) || ev.Type != TypeChange || string(ev.Data) != want {
			t.Errorf("event %d = {%d %s %s}, want {%d change %s}", i, ev.Seq, ev.Type, ev.Data, i+2, want)
		}
	}
	cancel()
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("an event arrived after the cancel, want the channel closed")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the channel stayed open after the cancel")
	}
	b.Publish(TypeChange, map[string]int{}) // no subscriber left to send to
}

func TestBusEpoch(t *testing.T) {
	b1, b2 := NewBus(nil), NewBus(nil)
	e := b1.Epoch()
	if !regexp.MustCompile(`^[0-9a-f]{16}$`).MatchString(e) {
		t.Fatalf("Epoch() = %q, want 16 lower-case hex digits", e)
	}
	if b2.Epoch() == e {
		t.Fatalf("two buses share the epoch %s", e)
	}
	if got := FormatID(e, 7); got != e+"-7" {
		t.Fatalf("FormatID = %q, want %s-7", got, e)
	}
	for _, seq := range []uint64{0, 1, 7, 1<<64 - 1} {
		if ep, s, ok := ParseID(FormatID(e, seq)); !ok || ep != e || s != seq {
			t.Errorf("ParseID(FormatID(e, %d)) = %q, %d, %v", seq, ep, s, ok)
		}
	}
	for _, id := range []string{"x", "", e, e + "-", e + "-0x1", e + "-+1", e + "--1", e + "-1-2", e + "-18446744073709551616",
		strings.ToUpper(e) + "-1", e + "a-1", e[:15] + "-1", "0123456789abcdeg-1"} {
		if ep, s, ok := ParseID(id); ok {
			t.Errorf("ParseID(%q) = %q, %d, true; want refused", id, ep, s)
		}
	}
}

func TestBusRing(t *testing.T) {
	b := NewBus(nil)
	if evs, ok := b.Since(0); !ok || len(evs) != 0 {
		t.Fatalf("Since(0) of an empty bus = %d events, %v; want none, true", len(evs), ok)
	}
	busPublish(b, RingSize+1)
	if evs, ok := b.Since(0); ok || evs != nil {
		t.Fatalf("Since(0) = %d events, %v; want none, false", len(evs), ok)
	}
	evs, ok := b.Since(1)
	if !ok || len(evs) != RingSize || evs[0].Seq != 2 || evs[RingSize-1].Seq != RingSize+1 {
		t.Fatalf("Since(1) = %d events, %v; want events 2 to %d", len(evs), ok, RingSize+1)
	}
	for i, ev := range evs {
		if ev.Seq != uint64(i+2) || string(ev.Data) != `{"i":`+strconv.Itoa(i+1)+`}` {
			t.Fatalf("Since(1)[%d] = {%d %s}, want seq %d", i, ev.Seq, ev.Data, i+2)
		}
	}
	if evs, ok := b.Since(RingSize + 1); !ok || len(evs) != 0 {
		t.Fatalf("Since(%d) = %d events, %v; want none, true", RingSize+1, len(evs), ok)
	}
}

func TestBusSlowSubscriber(t *testing.T) {
	b := NewBus(nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	slow, _ := b.Subscribe(ctx)
	fast, _ := b.Subscribe(ctx)
	got := make(chan []uint64, 1)
	go func() {
		var seqs []uint64
		for ev := range fast {
			seqs = append(seqs, ev.Seq)
			if len(seqs) == SubscriberBuffer+1 {
				got <- seqs
				return
			}
		}
	}()
	for i := range SubscriberBuffer + 1 {
		b.Publish(TypeChange, map[string]int{"i": i})
		// Waiting for the fast reader keeps its buffer from filling, which
		// would reset it too.
		deadline := time.Now().Add(5 * time.Second)
		for len(fast) > 0 && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
	}
	select {
	case seqs := <-got:
		for i, s := range seqs {
			if s != uint64(i+1) {
				t.Fatalf("the fast subscriber got %v, want 1 to %d", seqs, SubscriberBuffer+1)
			}
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the fast subscriber did not get every event")
	}
	if ev := busRecv(t, slow); ev.Type != TypeReset || ev.Seq != SubscriberBuffer+1 || string(ev.Data) != `{}` {
		t.Fatalf("the slow subscriber read {%d %s %s}, want {%d reset {}}", ev.Seq, ev.Type, ev.Data, SubscriberBuffer+1)
	}
	busEmpty(t, slow)
	b.Publish(TypeHost, HostEvent{HostID: "h1"})
	if ev := busRecv(t, slow); ev.Type != TypeHost || ev.Seq != SubscriberBuffer+2 {
		t.Fatalf("after the reset the slow subscriber read {%d %s}, want {%d host}", ev.Seq, ev.Type, SubscriberBuffer+2)
	}
}

func TestBusMarshalError(t *testing.T) {
	var buf bytes.Buffer
	b := NewBus(slog.New(slog.NewTextHandler(&buf, nil)))
	ch, _ := b.Subscribe(t.Context())
	b.Publish(TypeChange, make(chan int))
	if !strings.Contains(buf.String(), "events: marshal event") || !strings.Contains(buf.String(), "type=change") {
		t.Fatalf("log = %q, want the marshal error with the type", buf.String())
	}
	if ev := busRecv(t, ch); ev.Seq != 1 || ev.Type != TypeReset || string(ev.Data) != `{}` {
		t.Fatalf("in place of the dropped event: {%d %s %s}, want {1 reset {}}", ev.Seq, ev.Type, ev.Data)
	}
	busEmpty(t, ch)
	if evs, ok := b.Since(0); !ok || len(evs) != 1 || evs[0].Type != TypeReset {
		t.Fatalf("Since(0) = %+v, want the reset alone", evs)
	}
	b.Publish(TypeHost, HostEvent{HostID: "h1"})
	if ev := busRecv(t, ch); ev.Seq != 2 || ev.Type != TypeHost {
		t.Fatalf("the next event is {%d %s}, want {2 host}", ev.Seq, ev.Type)
	}
}

func TestBusPublishNeverBlocks(t *testing.T) {
	b := NewBus(nil)
	stalled, _ := b.Subscribe(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		busPublish(b, 10_000)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("10,000 publishes to a stalled subscriber did not return")
	}
	if ev := busRecv(t, stalled); ev.Type != TypeReset {
		t.Fatalf("the stalled subscriber read %s first, want reset", ev.Type)
	}
}
