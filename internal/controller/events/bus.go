// Package events is the controller's in-process event bus (spec 1.6). It
// numbers events within a random epoch, keeps the last RingSize of them for
// replay, and hands each one to every subscriber without ever blocking the
// publisher.
package events

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Bus limits (spec 1.6).
const (
	RingSize         = 1024
	SubscriberBuffer = 256
)

// The event types of spec 1.6.
const (
	TypeChange        = "change"
	TypeCompositeStep = "composite.step"
	TypeHost          = "host"
	TypeModule        = "module"
	TypeReset         = "reset"
)

// Event is one published event; Seq increases from 1 within the bus's epoch.
type Event struct {
	Seq  uint64
	Type string
	Data json.RawMessage
}

// ChangeEvent is the data of a change event.
type ChangeEvent struct {
	HostID    string    `json:"hostId"`
	ChangeID  string    `json:"changeId"`
	Action    string    `json:"action"`
	State     string    `json:"state"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// HostEvent is the data of a host event.
type HostEvent struct {
	HostID string `json:"hostId"`
	Online bool   `json:"online"`
}

// ModuleEvent is the data of a module event.
type ModuleEvent struct {
	HostID    string `json:"hostId"`
	ModuleID  string `json:"moduleId"`
	Enabled   bool   `json:"enabled"`
	Available bool   `json:"available"`
}

var resetData = json.RawMessage(`{}`)

// Bus fans events out to subscribers. Its methods are safe for concurrent
// use.
type Bus struct {
	log   *slog.Logger
	epoch string

	mu   sync.Mutex
	seq  uint64
	ring [RingSize]Event // the event of seq s is at ring[(s-1)%RingSize]
	subs map[chan Event]struct{}
}

// NewBus returns a bus with a new random epoch; a nil log discards.
func NewBus(log *slog.Logger) *Bus {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	var b [8]byte
	_, _ = rand.Read(b[:])
	return &Bus{log: log, epoch: hex.EncodeToString(b[:]), subs: map[chan Event]struct{}{}}
}

// Epoch is the bus's epoch, 16 lower-case hex digits.
func (b *Bus) Epoch() string { return b.epoch }

// Publish marshals data and hands the event, with the next sequence number,
// to every subscriber. It never blocks: a subscriber whose buffer is full
// loses what it holds and gets one reset event instead, so it refetches
// everything. A marshal error is logged and the event dropped; a reset takes
// its place, so subscribers refetch what it would have named.
func (b *Bus) Publish(eventType string, data any) {
	raw, err := json.Marshal(data)
	if err != nil {
		b.log.Error("events: marshal event; it is dropped", "type", eventType, "err", err)
		eventType, raw = TypeReset, resetData
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.seq++
	ev := Event{Seq: b.seq, Type: eventType, Data: raw}
	b.ring[(ev.Seq-1)%RingSize] = ev
	for ch := range b.subs {
		select {
		case ch <- ev:
			continue
		default:
		}
		// Only Publish sends, under b.mu, so once the buffer is drained the
		// reset always fits.
		for drained := false; !drained; {
			select {
			case <-ch:
			default:
				drained = true
			}
		}
		ch <- Event{Seq: ev.Seq, Type: TypeReset, Data: resetData}
	}
}

// Subscribe returns a channel of the events published after the returned
// sequence number. The channel is closed once ctx ends.
func (b *Bus) Subscribe(ctx context.Context) (<-chan Event, uint64) {
	ch := make(chan Event, SubscriberBuffer)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	last := b.seq
	b.mu.Unlock()
	go func() {
		<-ctx.Done()
		b.mu.Lock()
		delete(b.subs, ch)
		close(ch)
		b.mu.Unlock()
	}()
	return ch, last
}

// Since returns the ring's events whose Seq is above after, oldest first.
// It reports false when one of those events has already left the ring.
func (b *Bus) Since(after uint64) ([]Event, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if after >= b.seq {
		return nil, true
	}
	if b.seq-after > RingSize {
		return nil, false
	}
	out := make([]Event, 0, b.seq-after)
	for s := after + 1; s <= b.seq; s++ {
		out = append(out, b.ring[(s-1)%RingSize])
	}
	return out, true
}

// FormatID is the SSE id of the event seq of epoch.
func FormatID(epoch string, seq uint64) string {
	return epoch + "-" + strconv.FormatUint(seq, 10)
}

// ParseID splits an SSE id that FormatID made; ok is false for anything
// else.
func ParseID(id string) (epoch string, seq uint64, ok bool) {
	epoch, digits, found := strings.Cut(id, "-")
	if !found || len(epoch) != 16 || digits == "" {
		return "", 0, false
	}
	for _, c := range epoch {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return "", 0, false
		}
	}
	for _, c := range digits {
		if c < '0' || c > '9' {
			return "", 0, false
		}
	}
	seq, err := strconv.ParseUint(digits, 10, 64)
	if err != nil {
		return "", 0, false
	}
	return epoch, seq, true
}
