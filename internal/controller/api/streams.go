package api

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

const (
	// relayBuffer is how many module stream events wait for a slow client
	// (spec 1.6); one more ends the stream with overflow.
	relayBuffer = 256
	// maxRelayLine bounds one line of an agent's stream body: twice the
	// agent's 64 KiB event limit leaves room for the line's framing.
	maxRelayLine = 128 << 10
)

// relayEnd is the data of the one end frame of a relayed stream (W4).
type relayEnd struct {
	Reason string          `json:"reason"`
	Error  json.RawMessage `json:"error,omitempty"`
}

// handleStream relays a module stream of a host as server-sent events
// (spec 1.6): one "data" frame per event and exactly one "end" frame. The
// dispatcher checks the stream's schema hash and the stream limits before
// any SSE header is written, so a refusal is ordinary problem details. When
// sseStream itself ends the stream, the end frame is written here.
func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	c, _ := callerFrom(r.Context())
	if err := authorize(c.principal, core.ScopeRead); err != nil {
		writeProblem(w, s.log, err)
		return
	}
	body, err := s.cfg.Dispatcher.OpenStream(r.Context(), c.principal, r.PathValue("host"),
		r.PathValue("module"), r.PathValue("name"), r.URL.Query())
	if err != nil {
		writeProblem(w, s.log, err)
		return
	}
	defer func() { _ = body.Close() }()
	frames := make(chan sseFrame, relayBuffer+1)
	go relay(body, frames, s.log.With("host", r.PathValue("host"), "module", r.PathValue("module"), "stream", r.PathValue("name")))
	rw := &relayWriter{ResponseWriter: w}
	reason := s.sseStream(rw, r, frames)
	// sseStream may pick an expiry, a revocation or CloseStreams over the
	// closed source right after it wrote the relay's end frame; the stream
	// has one.
	if rw.ended {
		return
	}
	switch {
	case reason == "expired" || reason == "revoked":
	case reason == "error" && s.streamsClosed():
	default:
		return
	}
	if b, err := sseEncode(endFrame(reason, nil)); err == nil {
		_ = sseWrite(w, b)
	}
}

// streamsClosed reports whether CloseStreams ran.
func (s *Server) streamsClosed() bool {
	select {
	case <-s.streamsDone:
		return true
	default:
		return false
	}
}

// relayWriter records whether an end frame was written through it.
type relayWriter struct {
	http.ResponseWriter
	ended bool
}

func (w *relayWriter) Write(b []byte) (int, error) {
	if bytes.HasPrefix(b, []byte("event: end\n")) {
		w.ended = true
	}
	return w.ResponseWriter.Write(b)
}

// Unwrap lets sseWrite's ResponseController reach the connection's
// deadlines and flusher.
func (w *relayWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// relay turns the agent's JSON lines on body into frames on out and closes
// out after the one end frame. A data frame is sent only while fewer than
// relayBuffer frames wait, so the end frame always fits in out's last
// slot: when the client falls that far behind, relay closes body, which
// ends the agent's stream, and ends with overflow. A line too long or not
// JSON is logged on log, since the client sees only internal.
func relay(body io.ReadCloser, out chan<- sseFrame, log *slog.Logger) {
	defer close(out)
	sc := bufio.NewScanner(body)
	sc.Buffer(make([]byte, 0, 4096), maxRelayLine)
	for sc.Scan() {
		var line struct {
			Data json.RawMessage `json:"data"`
			End  *relayEnd       `json:"end"`
		}
		if err := json.Unmarshal(sc.Bytes(), &line); err != nil {
			log.Warn("api: a module stream line does not decode", "err", err)
			out <- endFrame("error", nil)
			return
		}
		switch {
		case line.End != nil && line.End.Reason == "done":
			out <- endFrame("done", nil)
			return
		case line.End != nil:
			out <- endFrame("error", agentProblem(line.End.Error))
			return
		case line.Data == nil:
			out <- endFrame("error", nil)
			return
		case len(out) >= relayBuffer:
			_ = body.Close()
			out <- endFrame("overflow", nil)
			return
		}
		out <- sseFrame{Event: "data", Data: line.Data}
	}
	// The body broke, or ended without an end line. Other read errors are
	// not logged: the handler's own close of body on a client's departure
	// is one.
	if errors.Is(sc.Err(), bufio.ErrTooLong) {
		log.Warn("api: a module stream line is too long", "limit", maxRelayLine)
	}
	out <- endFrame("error", nil)
}

// agentProblem keeps the code and detail of the problem an agent ended its
// stream with, the only members its framing has; anything else, such as a
// null or a string, is nil.
func agentProblem(raw json.RawMessage) json.RawMessage {
	var p struct {
		Code   string `json:"code"`
		Detail string `json:"detail"`
	}
	if json.Unmarshal(raw, &p) != nil || p.Code == "" {
		return nil
	}
	b, err := json.Marshal(p)
	if err != nil {
		return nil
	}
	return b
}

// endFrame is the end frame of reason. An error without the agent's
// problem gets internal's, as a failed request would.
func endFrame(reason string, problem json.RawMessage) sseFrame {
	end := relayEnd{Reason: reason}
	if reason == "error" {
		end.Error = problem
		if end.Error == nil {
			end.Error, _ = json.Marshal(map[string]string{"code": string(errcode.Internal), "detail": errcode.Internal.Title()})
		}
	}
	b, err := json.Marshal(end)
	if err != nil {
		panic("api: marshal a stream end: " + err.Error()) // a string and checked JSON always marshal
	}
	return sseFrame{Event: "end", Data: b}
}
