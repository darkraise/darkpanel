package api

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/darkraise/darkpanel/internal/controller/events"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// handleEvents serves GET /api/v1/events (spec 1.6): the controller's
// events as server-sent events, after the events a Last-Event-ID missed or
// a reset. The stream ends without an end frame, the browser reconnecting
// with the last id it saw, unless the runtime listener it came through was
// revoked: then it ends with one of reason revoked.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	c, _ := callerFrom(r.Context())
	if err := authorize(c.principal, core.ScopeRead); err != nil {
		writeProblem(w, s.log, err)
		return
	}
	bus := s.cfg.Events
	if bus == nil {
		writeProblem(w, s.log, errcode.New(errcode.NotFound, "this controller publishes no events"))
		return
	}
	release, err := s.eventSlot(c.principal.Actor)
	if err != nil {
		writeProblem(w, s.log, err)
		return
	}
	defer release()
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	live, last := bus.Subscribe(ctx)
	backlog := eventBacklog(bus, r.Header.Values("Last-Event-ID"), last)
	frames := make(chan sseFrame)
	go func() {
		defer close(frames)
		send := func(ev events.Event) bool {
			select {
			case frames <- sseFrame{ID: events.FormatID(bus.Epoch(), ev.Seq), Event: ev.Type, Data: ev.Data}:
				return true
			case <-ctx.Done():
				return false
			}
		}
		for _, ev := range backlog {
			if !send(ev) {
				return
			}
		}
		for ev := range live {
			if !send(ev) {
				return
			}
		}
	}()
	s.sseStream(w, r, frames)
}

// eventBacklog is what a stream sends before the events published after
// last: nothing without a Last-Event-ID; the events after it when it names
// this epoch and none of them has left the ring; otherwise one reset.
func eventBacklog(bus *events.Bus, lastIDs []string, last uint64) []events.Event {
	if len(lastIDs) == 0 {
		return nil
	}
	if epoch, seq, ok := events.ParseID(lastIDs[0]); ok && len(lastIDs) == 1 && epoch == bus.Epoch() && seq <= last {
		if missed, ok := bus.Since(seq); ok {
			n := 0
			for n < len(missed) && missed[n].Seq <= last {
				n++
			}
			return missed[:n]
		}
	}
	return []events.Event{{Seq: last, Type: events.TypeReset, Data: json.RawMessage(`{}`)}}
}
