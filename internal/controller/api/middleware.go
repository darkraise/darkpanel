package api

import (
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"runtime"
	"runtime/debug"
	"strings"

	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// ContentSecurityPolicy is sent on every response (spec section 12).
// style-src allows inline styles because the UI kit sets style attributes;
// scripts stay strict.
const ContentSecurityPolicy = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data:; font-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; " +
	"form-action 'self'"

const healthzPath = "/healthz"

// isAPIPath reports whether path belongs to the API rather than the UI.
func isAPIPath(path string) bool {
	return path == "/api" || strings.HasPrefix(path, "/api/")
}

// securityHeaders sets the response headers of spec section 12. HSTS is sent
// only over HTTPS, and API and health responses are never cached.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", ContentSecurityPolicy)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		if r.TLS != nil {
			h.Set("Strict-Transport-Security", "max-age=31536000")
		}
		if isAPIPath(r.URL.Path) || r.URL.Path == healthzPath {
			h.Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}

// trackingWriter records whether the response has started.
type trackingWriter struct {
	http.ResponseWriter
	wrote bool
}

func (t *trackingWriter) WriteHeader(status int) {
	t.wrote = true
	t.ResponseWriter.WriteHeader(status)
}

func (t *trackingWriter) Write(b []byte) (int, error) {
	t.wrote = true
	return t.ResponseWriter.Write(b)
}

func (t *trackingWriter) Unwrap() http.ResponseWriter { return t.ResponseWriter }

// recoverPanics logs a handler panic with its stack and answers internal. A
// panic inside the lifecycle engine can leave a change applying until
// recovery, so it must reach the log. When the response has already started
// it aborts the connection instead, so the client never takes a truncated
// answer for a complete one. The internal answer carries the headers as they
// were before the handler ran, so a handler that opened a session and then
// panicked hands out no session, and no header it set, changed or removed
// reaches the client. As in lifecycle.Protect, the panic value is logged only
// when it is a runtime error; any other value is logged by its type alone,
// because a handler may have put a credential in it.
func recoverPanics(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tw := &trackingWriter{ResponseWriter: w}
		before := w.Header().Clone()
		defer func() {
			v := recover()
			if v == nil {
				return
			}
			if err, ok := v.(error); ok && errors.Is(err, http.ErrAbortHandler) {
				panic(v)
			}
			value := fmt.Sprintf("%T", v)
			if re, ok := v.(runtime.Error); ok {
				value = re.Error()
			}
			log.Error("api: handler panic", "method", r.Method, "path", r.URL.Path, "panic", value, "stack", string(debug.Stack()))
			if tw.wrote {
				panic(http.ErrAbortHandler)
			}
			h := w.Header()
			clear(h)
			maps.Copy(h, before)
			writeProblem(w, log, errcode.New(errcode.Internal, ""))
		}()
		next.ServeHTTP(tw, r)
	})
}
