package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// Server-sent event limits (spec 1.6). A stream response sets its own
// deadlines instead of the listeners' write timeout.
const (
	SSEWriteTimeout = 30 * time.Second
	SSEIdleTimeout  = 10 * time.Minute
	SSEHardLimit    = 2 * time.Hour
	SSEHeartbeat    = 20 * time.Second
	SSERecheck      = 30 * time.Second
	MaxEventStreams = 4 // GET /api/v1/events per principal
)

// sseFrame is one SSE message; an empty ID writes no id line.
type sseFrame struct {
	ID, Event string
	Data      json.RawMessage
}

// sseEncode renders f as id, event and data lines and a blank line. The
// data is compacted, so it is one line whatever its source wrote.
func sseEncode(f sseFrame) ([]byte, error) {
	var b bytes.Buffer
	if f.ID != "" {
		b.WriteString("id: " + f.ID + "\n")
	}
	b.WriteString("event: " + f.Event + "\ndata: ")
	if err := json.Compact(&b, f.Data); err != nil {
		return nil, err
	}
	b.WriteString("\n\n")
	return b.Bytes(), nil
}

// sseWrite writes b under a fresh write deadline and flushes it. A writer
// without deadlines or without a flusher is accepted, which only tests use.
func sseWrite(w http.ResponseWriter, b []byte) error {
	rc := http.NewResponseController(w)
	if err := rc.SetWriteDeadline(time.Now().Add(SSEWriteTimeout)); err != nil && !errors.Is(err, http.ErrNotSupported) {
		return err
	}
	if len(b) > 0 {
		if _, err := w.Write(b); err != nil {
			return err
		}
	}
	if err := rc.Flush(); err != nil && !errors.Is(err, http.ErrNotSupported) {
		return err
	}
	return nil
}

// sseSignal returns a timer callback that marks ch; a mark already pending
// absorbs the new one.
func sseSignal(ch chan struct{}) func() {
	return func() {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// CloseStreams ends every stream response, open or later, with "error". A
// graceful shutdown of the listeners waits for running requests, which a
// stream would otherwise hold until the shutdown's deadline.
func (s *Server) CloseStreams() {
	s.closeStreams.Do(func() { close(s.streamsDone) })
}

// sseStream writes the response headers, then frames from src until src
// closes or the stream ends, renewing the write deadline before each write,
// sending ": ping" every SSEHeartbeat, re-identifying the request every
// SSERecheck; it returns the end reason: "done" (src closed), "expired"
// (idle, hard limit, or session or token expired), "revoked" (session or
// token revoked) or "error" (a write failed, the client went away or
// CloseStreams ran). A HEAD request gets the headers alone and ends at once
// with done.
func (s *Server) sseStream(w http.ResponseWriter, r *http.Request, src <-chan sseFrame) string {
	c, _ := callerFrom(r.Context())
	expires := c.identity.ExpiresAt
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	if sseWrite(w, nil) != nil {
		return "error"
	}
	if r.Method == http.MethodHead {
		return "done"
	}

	clock := s.cfg.Clock
	beat, check, idle, hard := make(chan struct{}, 1), make(chan struct{}, 1), make(chan struct{}, 1), make(chan struct{}, 1)
	lastFrame := clock.Now()
	hardT := clock.AfterFunc(SSEHardLimit, sseSignal(hard))
	beatT := clock.AfterFunc(SSEHeartbeat, sseSignal(beat))
	checkT := clock.AfterFunc(SSERecheck, sseSignal(check))
	idleT := clock.AfterFunc(SSEIdleTimeout, sseSignal(idle))
	defer func() {
		hardT.Stop()
		beatT.Stop()
		checkT.Stop()
		idleT.Stop()
	}()
	for {
		select {
		case f, ok := <-src:
			if !ok {
				return "done"
			}
			b, err := sseEncode(f)
			if err != nil {
				s.log.Error("api: encode a stream event", "event", f.Event, "err", err)
				return "error"
			}
			if sseWrite(w, b) != nil {
				return "error"
			}
			lastFrame = clock.Now()
		case <-beat:
			if sseWrite(w, []byte(": ping\n\n")) != nil {
				return "error"
			}
			beatT = clock.AfterFunc(SSEHeartbeat, sseSignal(beat))
		case <-check:
			if reason := s.sseRecheck(r, &expires); reason != "" {
				return reason
			}
			checkT = clock.AfterFunc(SSERecheck, sseSignal(check))
		case <-idle:
			left := SSEIdleTimeout - clock.Now().Sub(lastFrame)
			if left <= 0 {
				return "expired"
			}
			idleT = clock.AfterFunc(left, sseSignal(idle))
		case <-hard:
			return "expired"
		case <-r.Context().Done():
			return "error"
		case <-s.streamsDone:
			return "error"
		}
	}
}

// sseRecheck authenticates r again; it returns "" while the credentials
// hold and moves *expires to their new end. The identity service answers
// auth.required for every refusal, so a refusal before *expires is a
// revocation (logout, a revoked token, a disabled user) and one after it an
// expiry. The check is passive (streamRoute), so an open stream does not
// keep its session alive: past the idle limit the next check ends it.
func (s *Server) sseRecheck(r *http.Request, expires *time.Time) string {
	c, err := s.identify(r)
	switch {
	case err == nil:
		*expires = c.identity.ExpiresAt
		return ""
	case errcode.From(err).Code != errcode.AuthRequired:
		s.log.Error("api: re-authenticate a stream", "err", err)
		return "error"
	case s.cfg.Clock.Now().Before(*expires):
		return "revoked"
	default:
		return "expired"
	}
}

// eventSlot takes one of actor's MaxEventStreams event streams; release
// gives it back.
func (s *Server) eventSlot(actor string) (release func(), err error) {
	s.eventsMu.Lock()
	defer s.eventsMu.Unlock()
	if s.eventStreams[actor] >= MaxEventStreams {
		return nil, errcode.New(errcode.StreamLimit, fmt.Sprintf("at most %d event streams per principal", MaxEventStreams))
	}
	s.eventStreams[actor]++
	return func() {
		s.eventsMu.Lock()
		defer s.eventsMu.Unlock()
		if s.eventStreams[actor]--; s.eventStreams[actor] == 0 {
			delete(s.eventStreams, actor)
		}
	}, nil
}
