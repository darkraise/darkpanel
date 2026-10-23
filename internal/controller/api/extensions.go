package api

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/ext"
	"github.com/darkraise/darkpanel/internal/core/schema"
)

// extensionPrefix is where every contributed route lives: under the API,
// routeAccess authenticates it, and no contribution can claim a UI path.
const extensionPrefix = "/api/v1/"

var extensionMethods = []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete}

// checkExtension refuses a contributed route that New could not mount
// safely. A catch-all wildcard ({x...}) serves a subtree as a trailing slash
// does, and an end anchor ({$}) serves the trailing-slash path itself.
func checkExtension(rt ext.Route) error {
	pattern := rt.Method + " " + rt.Path
	switch {
	case rt.Surface != ext.SurfaceWeb && rt.Surface != ext.SurfaceSocket:
		return fmt.Errorf("api: extension route %s: unknown surface %d", pattern, rt.Surface)
	case rt.Handler == nil:
		return fmt.Errorf("api: extension route %s: no handler", pattern)
	case !slices.Contains(extensionMethods, rt.Method):
		return fmt.Errorf("api: extension route %s: method %q is not one of %s", pattern, rt.Method, strings.Join(extensionMethods, ", "))
	case !strings.HasPrefix(rt.Path, extensionPrefix) || strings.HasSuffix(rt.Path, "/") || strings.HasSuffix(rt.Path, "...}") ||
		strings.HasSuffix(rt.Path, "{$}"):
		return fmt.Errorf("api: extension route %s: the path must lie under %s and name one route, not a subtree", pattern, extensionPrefix)
	case rt.Surface == ext.SurfaceWeb && !slices.Contains([]core.Scope{core.ScopeRead, core.ScopeOperate, core.ScopeAdmin}, rt.Scope):
		return fmt.Errorf("api: extension route %s: scope %q is not read, operate or admin", pattern, rt.Scope)
	}
	return nil
}

// mount adds h at pattern to mux. ServeMux panics on a pattern that is
// invalid or conflicts with one it holds; for a contributed route that is
// a mistake that must fail startup, so the panic becomes an error.
func mount(mux *http.ServeMux, pattern string, h http.Handler) (err error) {
	defer func() {
		if v := recover(); v != nil {
			err = fmt.Errorf("api: extension route %s: %v", pattern, v)
		}
	}()
	mux.Handle(pattern, h)
	return nil
}

// mountWebExtensions mounts the web surface's contributed routes on the
// network mux, behind the authentication and CSRF middleware every network
// route has, each with its scope check. Stream routes are also recorded on
// streamMux, so their requests authenticate passively like the built-in
// streams.
func (s *Server) mountWebExtensions() error {
	s.streamMux = http.NewServeMux()
	for _, rt := range s.cfg.Extensions {
		if rt.Surface != ext.SurfaceWeb {
			continue
		}
		next := s.extensionStream(rt)
		need := rt.Scope
		h := requireCaller(s.log, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			c, _ := callerFrom(r.Context())
			if err := authorize(c.principal, need); err != nil {
				writeProblem(w, s.log, err)
				return
			}
			next.ServeHTTP(w, r)
		}))
		pattern := rt.Method + " " + rt.Path
		if err := mount(s.mux, pattern, h); err != nil {
			return err
		}
		if rt.Stream {
			// The network mux accepted the pattern, so this one cannot clash.
			s.streamMux.Handle(pattern, http.NotFoundHandler())
		}
	}
	return nil
}

// extensionStreamRoute reports whether r asks for a contributed web stream
// route: the network mux must route r to a pattern streamMux holds. Asking
// streamMux alone would also count a request that a more specific ordinary
// route serves, which would then never extend its session.
func (s *Server) extensionStreamRoute(r *http.Request) bool {
	if s.streamMux == nil {
		return false
	}
	_, pattern := s.mux.Handler(r)
	_, stream := s.streamMux.Handler(r)
	return pattern != "" && pattern == stream
}

// mountSocketExtensions mounts the socket surface's contributed routes on
// mux, where the socket's peer check is their authentication.
func (s *Server) mountSocketExtensions(mux *http.ServeMux) error {
	for _, rt := range s.cfg.Extensions {
		if rt.Surface != ext.SurfaceSocket {
			continue
		}
		if err := mount(mux, rt.Method+" "+rt.Path, s.extensionStream(rt)); err != nil {
			return err
		}
	}
	return nil
}

// extensionStream is rt's handler. A stream route instead gets the
// built-in streams' lifecycle: a write deadline of SSEWriteTimeout renewed
// on every write in place of the listeners' 60 s total, and a context that
// ends at SSEHardLimit, at CloseStreams, when the runtime listener it came
// through is revoked, and on the web when the SSERecheck re-authentication
// fails; through a runtime listener it also gets X-Accel-Buffering: no
// (spec 1.6, 1.10). Once that context ends, the handler's writes fail, and
// a server-sent event stream gets the end frame naming why it ended.
func (s *Server) extensionStream(rt ext.Route) http.Handler {
	if !rt.Stream {
		return rt.Handler
	}
	recheck := rt.Surface == ext.SurfaceWeb
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		client := r.Context()
		ctx, cancel := context.WithCancel(client)
		defer cancel()
		var revoked <-chan struct{}
		if p, ok := core.ProxiedFrom(ctx); ok {
			revoked = p.Revoked
			w.Header().Set("X-Accel-Buffering", "no")
		}
		r = r.WithContext(ctx)
		// The watcher re-authenticates a copy, so it never reads the
		// request the handler may still be using.
		watched := make(chan string, 1)
		watcher := r.Clone(ctx)
		go func() { watched <- s.watchStream(ctx, cancel, watcher, recheck, revoked) }()
		dw := &deadlineWriter{ResponseWriter: w, ctx: ctx}
		dw.renew()
		rt.Handler.ServeHTTP(dw, r)
		cancel()
		reason := <-watched
		// The handler has returned, so the end frame cannot interleave with
		// its writes. A stream that already ended itself, broke, or whose
		// client left takes none.
		if reason == "" || !dw.events || dw.ended || dw.broken || client.Err() != nil {
			return
		}
		if b, err := sseEncode(endFrame(reason, nil)); err == nil {
			_ = sseWrite(w, b)
		}
	})
}

// watchStream cancels a stream route's context when the stream must end,
// and returns why, as an end frame's reason, once it is done: "" when the
// context ended first. It ends the stream on a failed re-authentication of
// r only with recheck: the socket's peer check holds for the connection's
// life.
func (s *Server) watchStream(ctx context.Context, cancel context.CancelFunc, r *http.Request, recheck bool, revoked <-chan struct{}) string {
	defer cancel()
	c, _ := callerFrom(r.Context())
	expires := c.identity.ExpiresAt
	clock := s.cfg.Clock
	hard, check := make(chan struct{}, 1), make(chan struct{}, 1)
	hardT := clock.AfterFunc(SSEHardLimit, sseSignal(hard))
	defer hardT.Stop()
	var checkT core.Timer
	if recheck {
		checkT = clock.AfterFunc(SSERecheck, sseSignal(check))
	}
	defer func() {
		if checkT != nil {
			checkT.Stop()
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return ""
		case <-hard:
			return "expired"
		case <-s.streamsDone:
			return "error"
		case <-revoked:
			return "revoked"
		case <-check:
			if reason := s.sseRecheck(r, &expires); reason != "" {
				return reason
			}
			checkT = clock.AfterFunc(SSERecheck, sseSignal(check))
		}
	}
}

// deadlineWriter renews the connection's write deadline before every
// header and body write, and refuses both once ctx has ended, so a handler
// that does not watch its context still stops writing to a stream that was
// ended. It records what extensionStream needs to decide on an end frame.
type deadlineWriter struct {
	http.ResponseWriter
	ctx     context.Context
	started bool // the status line was written
	events  bool // the response is a successful text/event-stream
	ended   bool // an end frame went through
	broken  bool // a write or flush failed
}

func (w *deadlineWriter) start(status int) {
	if w.started {
		return
	}
	w.started = true
	w.events = status >= 200 && status < 300 && strings.HasPrefix(w.Header().Get("Content-Type"), "text/event-stream")
}

// renew moves the write deadline SSEWriteTimeout ahead. Its error is
// dropped: a writer without deadlines is one only tests use, and a broken
// connection fails the write that follows.
func (w *deadlineWriter) renew() {
	_ = http.NewResponseController(w.ResponseWriter).SetWriteDeadline(time.Now().Add(SSEWriteTimeout))
}

func (w *deadlineWriter) WriteHeader(status int) {
	if w.ctx.Err() != nil {
		return
	}
	w.renew()
	w.start(status)
	w.ResponseWriter.WriteHeader(status)
}

func (w *deadlineWriter) Write(b []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	w.renew()
	w.start(http.StatusOK)
	if bytes.HasPrefix(b, []byte("event: end\n")) {
		w.ended = true
	}
	n, err := w.ResponseWriter.Write(b)
	if err != nil {
		w.broken = true
	}
	return n, err
}

// FlushError renews the deadline and flushes; a handler's
// ResponseController calls it, so it sees the flush's error.
func (w *deadlineWriter) FlushError() error {
	if err := w.ctx.Err(); err != nil {
		return err
	}
	w.renew()
	err := http.NewResponseController(w.ResponseWriter).Flush()
	if err != nil && !errors.Is(err, http.ErrNotSupported) {
		w.broken = true
	}
	return err
}

// Flush is FlushError for a handler that asserts http.Flusher rather than
// using a ResponseController; http.Flusher has no way to return the error.
func (w *deadlineWriter) Flush() { _ = w.FlushError() }

// Unwrap lets a handler's ResponseController reach the connection's
// deadlines.
func (w *deadlineWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// checkWebDocument builds the OpenAPI document once, so a web route at a
// path the registry also generates (a resource read or an action operation)
// fails New: ServeMux accepts it as more specific than the built-in route,
// and the document would otherwise fail every request.
func (s *Server) checkWebDocument() error {
	if !slices.ContainsFunc(s.cfg.Extensions, func(rt ext.Route) bool { return rt.Surface == ext.SurfaceWeb }) {
		return nil
	}
	if _, err := schema.OpenAPI(s.cfg.Registry, s.OpenAPIRoutes()); err != nil {
		return fmt.Errorf("api: extension routes: %w", err)
	}
	return nil
}

// OpenAPIRoutes returns Routes followed by the web surface's contributed
// routes, which the served OpenAPI document lists. The socket's routes are
// not part of the network API the document describes.
func (s *Server) OpenAPIRoutes() []schema.Route {
	out := Routes()
	for _, rt := range s.cfg.Extensions {
		if rt.Surface == ext.SurfaceWeb {
			out = append(out, schema.Route{Method: rt.Method, Path: rt.Path, Summary: rt.Summary})
		}
	}
	return out
}
