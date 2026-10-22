package api

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/darkraise/darkpanel/internal/controller/auth"
	"github.com/darkraise/darkpanel/internal/controller/events"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// Session cookie names and the CSRF header (spec section 9). HTTPS
// listeners use the __Host- cookie with Secure; the plain-HTTP loopback
// listener uses the other name without Secure.
const (
	SessionCookieSecure = "__Host-darkpanel_session"
	SessionCookiePlain  = "darkpanel_session"
	CSRFHeader          = "X-CSRF-Token"
)

const loginPath = "/api/v1/auth/login"

// Authenticator is the part of *auth.Service the API calls.
type Authenticator interface {
	Login(ctx context.Context, r auth.LoginRequest) (auth.LoginResult, error)
	Logout(ctx context.Context, sessionID, source string) error
	Authenticate(ctx context.Context, c auth.Credentials) (auth.Identity, error)
	CSRFToken(sessionID string) string
	VerifyCSRF(sessionID, token string) bool
}

var _ Authenticator = (*auth.Service)(nil)

// WebCA is the web certificate root that the controller socket serves. Its
// method set is listen.WebCA's.
type WebCA interface {
	// RootPEM is the root certificate as one PEM CERTIFICATE block.
	RootPEM() []byte
	// Fingerprint is the lowercase hex SHA-256 of the root's DER encoding.
	Fingerprint() string
}

// Config configures New. Every field but UI and Log is required.
type Config struct {
	Dispatcher core.Dispatcher
	Auth       Authenticator
	Registry   *core.Registry
	WebCA      WebCA
	// SchemaVersion returns the highest applied core migration of the
	// controller database, which /healthz reports.
	SchemaVersion func(ctx context.Context) (int, error)
	// HostID is the controller host's id, part of the socket actors.
	HostID string
	// Dev accepts the service's own non-root uid on the controller socket.
	Dev bool
	// UI is the web UI served at /; nil serves none.
	UI fs.FS
	// HealthStatus is the status GET /healthz reports, always with HTTP 200;
	// empty means "ok".
	HealthStatus string
	// Log receives internal errors and panics; nil discards.
	Log *slog.Logger
	// Events is the bus GET /api/v1/events streams; nil answers that route
	// with not_found.
	Events *events.Bus
	// Clock times the event streams; nil means core.SystemClock{}.
	Clock core.Clock
}

// Server serves the API. Its handlers are safe for concurrent use.
type Server struct {
	cfg       Config
	log       *slog.Logger
	mux       *http.ServeMux
	handler   http.Handler
	listeners atomic.Pointer[func() ListenerState]

	eventsMu     sync.Mutex
	eventStreams map[string]int // open GET /api/v1/events streams per Principal.Actor
	streamsDone  chan struct{}  // closed by CloseStreams
	closeStreams sync.Once
}

// ListenerState is the listener state the UI footer shows (spec section 13):
// whether a Tailscale listener is bound, and the listener service's message.
type ListenerState struct {
	TailscaleBound bool   `json:"tailscaleBound"`
	Message        string `json:"message"`
}

const listenersNotStarted = "The listeners are not started yet."

// SetListenerState sets the source of the listener state the API reports.
// The listener service is started with Handler, so it exists only after New;
// the source is therefore set late, and may be set while requests are
// served. A nil source reports that the listeners are not started.
func (s *Server) SetListenerState(state func() ListenerState) {
	if state == nil {
		s.listeners.Store(nil)
		return
	}
	s.listeners.Store(&state)
}

// listenerState reads the source SetListenerState set.
func (s *Server) listenerState() ListenerState {
	if state := s.listeners.Load(); state != nil {
		return (*state)()
	}
	return ListenerState{Message: listenersNotStarted}
}

// New returns a server for cfg.
func New(cfg Config) (*Server, error) {
	if cfg.Dispatcher == nil || cfg.Auth == nil || cfg.Registry == nil || cfg.WebCA == nil ||
		cfg.SchemaVersion == nil || cfg.HostID == "" {
		return nil, errors.New("api: Config needs Dispatcher, Auth, Registry, WebCA, SchemaVersion and HostID")
	}
	if cfg.Log == nil {
		cfg.Log = slog.New(slog.DiscardHandler)
	}
	if cfg.Clock == nil {
		cfg.Clock = core.SystemClock{}
	}
	s := &Server{cfg: cfg, log: cfg.Log, mux: http.NewServeMux(), eventStreams: map[string]int{}, streamsDone: make(chan struct{})}
	for _, rt := range s.networkRoutes() {
		h := rt.handler
		if !rt.public {
			h = requireCaller(s.log, h)
		}
		s.mux.Handle(rt.method+" "+rt.path, h)
	}
	s.mux.HandleFunc("/", s.networkFallback)
	s.handler = securityHeaders(recoverPanics(s.log, s.authenticate(s.mux)))
	return s, nil
}

// Handler serves the network listeners. A request is HTTPS exactly when
// Request.TLS is set, which picks the session cookie and HSTS.
func (s *Server) Handler() http.Handler { return s.handler }

// caller is the authenticated party of a request.
type caller struct {
	principal core.Principal
	identity  auth.Identity // zero on the controller socket
	session   string        // the session cookie's value, for a session principal only
}

type callerKey struct{}

func withCaller(ctx context.Context, c caller) context.Context {
	return context.WithValue(ctx, callerKey{}, c)
}

func callerFrom(ctx context.Context) (caller, bool) {
	c, ok := ctx.Value(callerKey{}).(caller)
	return c, ok
}

// sessionCookieName is the session cookie of the listener r arrived on.
func sessionCookieName(r *http.Request) string {
	if r.TLS != nil {
		return SessionCookieSecure
	}
	return SessionCookiePlain
}

// clientAddr is the host part of the peer address. Forwarded headers are
// ignored: no proxy is trusted in M1.
func clientAddr(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

type access int

const (
	accessAuthenticated access = iota // credentials required
	accessPublic                      // credentials ignored
	accessOptional                    // valid credentials used, invalid ones ignored
)

// routeAccess is the public list of spec section 12: login, /healthz, and
// GET or HEAD of the UI's files and index fallback. Everything else, on any
// method and any path, needs credentials.
func routeAccess(method, path string) access {
	get := method == http.MethodGet || method == http.MethodHead
	switch {
	case method == http.MethodPost && path == loginPath:
		return accessPublic
	case get && path == healthzPath:
		return accessOptional
	case get && !isAPIPath(path):
		return accessPublic
	default:
		return accessAuthenticated
	}
}

// streamRoute reports whether r asks for a stream: GET or HEAD of
// /api/v1/events or of a relay, /api/v1/hosts/{host}/streams/{module}/{name}.
// Its credentials are checked passively, so a stream never extends its
// session's idle limit.
func streamRoute(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	if r.URL.Path == "/api/v1/events" {
		return true
	}
	seg := strings.Split(r.URL.Path, "/")
	return len(seg) == 8 && seg[0] == "" && seg[1] == "api" && seg[2] == "v1" && seg[3] == "hosts" && seg[4] != "" &&
		seg[5] == "streams" && seg[6] != "" && seg[7] != ""
}

func safeMethod(method string) bool {
	return method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions
}

// identify authenticates r. When an Authorization header is present only
// its bearer token counts and the cookie is never read (spec section 9). A
// stream request is authenticated passively (streamRoute).
func (s *Server) identify(r *http.Request) (caller, error) {
	var session string
	if ck, err := r.Cookie(sessionCookieName(r)); err == nil {
		session = ck.Value
	}
	id, err := s.cfg.Auth.Authenticate(r.Context(), auth.Credentials{
		Authorization: r.Header.Values("Authorization"),
		Session:       session,
		Source:        clientAddr(r),
		Passive:       streamRoute(r),
	})
	if err != nil {
		return caller{}, err
	}
	c := caller{principal: id.Principal, identity: id}
	if id.FromSession {
		c.session = session
	}
	return c, nil
}

// authenticate is the one authentication middleware of the network
// listeners: it denies every request whose route is not public unless it
// carries valid credentials, and a session request with an unsafe method
// unless its CSRF token matches.
func (s *Server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch routeAccess(r.Method, r.URL.Path) {
		case accessPublic:
			next.ServeHTTP(w, r)
			return
		case accessOptional:
			if c, err := s.identify(r); err == nil {
				r = r.WithContext(withCaller(r.Context(), c))
			}
			next.ServeHTTP(w, r)
			return
		}
		c, err := s.identify(r)
		if err != nil {
			writeProblem(w, s.log, err)
			return
		}
		if c.identity.FromSession && !safeMethod(r.Method) && !s.cfg.Auth.VerifyCSRF(c.session, r.Header.Get(CSRFHeader)) {
			writeProblem(w, s.log, errcode.New(errcode.CSRF, "missing or invalid "+CSRFHeader+" header"))
			return
		}
		next.ServeHTTP(w, r.WithContext(withCaller(r.Context(), c)))
	})
}

// requireCaller refuses a request that reached a protected handler without
// a caller. authenticate already guarantees one; this keeps a mistake in
// routeAccess from exposing a handler.
func requireCaller(log *slog.Logger, next http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := callerFrom(r.Context()); !ok {
			writeProblem(w, log, errcode.New(errcode.AuthRequired, "authentication required"))
			return
		}
		next.ServeHTTP(w, r)
	}
}

// networkFallback serves the UI for GET and HEAD outside the API, and
// answers not_found or method_not_allowed for everything else that no route
// matched, only once the request is authenticated.
func (s *Server) networkFallback(w http.ResponseWriter, r *http.Request) {
	if routeAccess(r.Method, r.URL.Path) == accessPublic && !isAPIPath(r.URL.Path) {
		if !serveUI(w, r, s.cfg.UI) {
			writeProblem(w, s.log, errcode.New(errcode.NotFound, "no such page"))
		}
		return
	}
	if _, ok := callerFrom(r.Context()); !ok {
		writeProblem(w, s.log, errcode.New(errcode.AuthRequired, "authentication required"))
		return
	}
	s.unrouted(w, r, s.mux, s.cfg.UI != nil)
}

// unrouted answers a request no route of mux matched: method_not_allowed
// with Allow when another method has a route at the path, else not_found.
// With ui, every path outside the API allows GET and HEAD.
func (s *Server) unrouted(w http.ResponseWriter, r *http.Request, mux *http.ServeMux, ui bool) {
	allow := []string{http.MethodGet, http.MethodHead}
	if !ui || isAPIPath(r.URL.Path) {
		allow = allowedMethods(mux, r)
	}
	if len(allow) == 0 {
		writeProblem(w, s.log, errcode.New(errcode.NotFound, "no such route"))
		return
	}
	w.Header().Set("Allow", strings.Join(allow, ", "))
	writeProblem(w, s.log, errcode.New(errcode.MethodNotAllowed, r.Method+" is not allowed here"))
}

// allowedMethods lists the methods that a route other than the fallback
// serves at r's path.
func allowedMethods(mux *http.ServeMux, r *http.Request) []string {
	var allow []string
	for _, m := range []string{http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		probe := r.Clone(r.Context())
		probe.Method = m
		if _, pattern := mux.Handler(probe); pattern != "" && pattern != "/" {
			allow = append(allow, m)
		}
	}
	return allow
}
