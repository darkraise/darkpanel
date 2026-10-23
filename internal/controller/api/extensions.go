package api

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/ext"
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
// route.
func (s *Server) extensionStreamRoute(r *http.Request) bool {
	if s.streamMux == nil {
		return false
	}
	_, pattern := s.streamMux.Handler(r)
	return pattern != ""
}

// extensionStream is rt's handler. A stream route instead gets the
// built-in streams' lifecycle: a write deadline of SSEWriteTimeout renewed
// on every write in place of the listeners' 60 s total, and a context that
// ends at SSEHardLimit, at CloseStreams, when the runtime listener it came
// through is revoked, and on the web when the SSERecheck re-authentication
// fails; through a runtime listener it also gets X-Accel-Buffering: no
// (spec 1.6, 1.10).
func (s *Server) extensionStream(rt ext.Route) http.Handler {
	if !rt.Stream {
		return rt.Handler
	}
	recheck := rt.Surface == ext.SurfaceWeb
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()
		var revoked <-chan struct{}
		if p, ok := core.ProxiedFrom(ctx); ok {
			revoked = p.Revoked
			w.Header().Set("X-Accel-Buffering", "no")
		}
		r = r.WithContext(ctx)
		// The watcher re-authenticates a copy, so it never reads the
		// request the handler may still be using.
		go s.watchStream(ctx, cancel, r.Clone(ctx), recheck, revoked)
		dw := &deadlineWriter{ResponseWriter: w}
		dw.renew()
		rt.Handler.ServeHTTP(dw, r)
	})
}

// watchStream cancels a stream route's context when the stream must end,
// and returns once it is done. It ends the stream on a failed
// re-authentication of r only with recheck: the socket's peer check holds
// for the connection's life.
func (s *Server) watchStream(ctx context.Context, cancel context.CancelFunc, r *http.Request, recheck bool, revoked <-chan struct{}) {
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
			return
		case <-hard:
			return
		case <-s.streamsDone:
			return
		case <-revoked:
			return
		case <-check:
			if s.sseRecheck(r, &expires) != "" {
				return
			}
			checkT = clock.AfterFunc(SSERecheck, sseSignal(check))
		}
	}
}

// deadlineWriter renews the connection's write deadline before every
// header and body write.
type deadlineWriter struct {
	http.ResponseWriter
}

// renew moves the write deadline SSEWriteTimeout ahead. Its error is
// dropped: a writer without deadlines is one only tests use, and a broken
// connection fails the write that follows.
func (w *deadlineWriter) renew() {
	_ = http.NewResponseController(w.ResponseWriter).SetWriteDeadline(time.Now().Add(SSEWriteTimeout))
}

func (w *deadlineWriter) WriteHeader(status int) {
	w.renew()
	w.ResponseWriter.WriteHeader(status)
}

func (w *deadlineWriter) Write(b []byte) (int, error) {
	w.renew()
	return w.ResponseWriter.Write(b)
}

// Unwrap lets a handler's ResponseController reach the connection's
// flusher and deadlines.
func (w *deadlineWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
