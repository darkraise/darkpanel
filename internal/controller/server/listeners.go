package server

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/api"
	"github.com/darkraise/darkpanel/internal/controller/listen"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/store"
)

// listeners are the network listeners and the controller socket.
type listeners struct {
	apiServer     *api.Server
	listenService *listen.Service
	// serveFailed holds the first error of a listener or of the socket
	// server that stopped serving on its own.
	serveFailed chan error
}

// startListeners loads or creates the web authority, builds the API with
// the controller contributions' routes, starts the network listeners with
// it, serves the controller socket and then starts the contributed services.
// Composite recovery has run by then (startServices), so a service never
// races it.
func (s *Server) startListeners(ctx context.Context) error {
	authority, err := listen.LoadOrCreateAuthority(ctx, s.controllerDB, s.controllerSealer, s.clock)
	if err != nil {
		return err
	}
	routes, services, err := s.contributions()
	if err != nil {
		return err
	}
	srv, err := api.New(api.Config{
		Dispatcher: s.dispatcher, Auth: s.authService, Registry: s.registry, WebCA: authority,
		Availability: s.dispatcher.Availability,
		SchemaVersion: func(ctx context.Context) (int, error) {
			return store.SchemaVersion(ctx, s.controllerDB, store.SideController)
		},
		HostID: s.localHostID, Dev: s.opts.Dev, UI: api.EmbeddedUI(), Log: s.log, HealthStatus: s.opts.HealthStatus,
		Whois:  s.whois,
		Events: s.events, Clock: s.clock, Extensions: routes,
	})
	if err != nil {
		return err
	}
	s.apiServer = srv
	s.serveFailed = make(chan error, 1)

	specs := s.opts.Listen
	if specs == nil {
		if specs, err = listen.ParseSpecs(nil); err != nil {
			return err
		}
	}
	svc, err := listen.Start(ctx, listen.Config{
		Specs: specs, Hostname: s.hostname, DB: s.controllerDB, Authority: authority, Clock: s.clock,
		Handler: srv.Handler(), Log: s.log, Interfaces: s.opts.Interfaces, Bind: s.opts.Bind,
		OnServeError: s.serveFailure,
		Sources:      &listen.SourceDeps{Dispatch: s.dispatcher, LocalHostID: s.localHostID, Log: s.log},
	})
	if err != nil {
		return err
	}
	s.listenService = svc
	s.topo.setWeb(func() []listen.Bound { return svc.State().Listeners })
	s.onShutdown("listeners", svc.Shutdown)
	// The runtime listeners share the HTTPS listeners' TLS configuration
	// (spec 1.10); every listener recorded open, also one an action opened
	// during startup recovery, is bound here.
	tlsConfig, err := svc.TLSConfig()
	if err != nil {
		return err
	}
	if err := s.startRuntimeListeners(ctx, srv.RuntimeHandler, tlsConfig); err != nil {
		return err
	}
	s.onShutdown("runtime listeners", func(ctx context.Context) error {
		ctx, cancel := halfOfRemaining(ctx)
		defer cancel()
		return s.runtimeListeners.Shutdown(ctx)
	})
	// Registered after the listeners, so it runs before their shutdown,
	// which waits for running requests.
	s.onShutdown("stream responses", func(context.Context) error { srv.CloseStreams(); return nil })
	// The source stays set after Shutdown, so the state then reads that the
	// listeners are shut down.
	srv.SetListenerState(func() api.ListenerState {
		st := svc.State()
		return api.ListenerState{TailscaleBound: st.TailscaleBound, Message: withUnbound(st.Message, s.runtimeListeners.Unbound())}
	})
	srv.SetTailscaleAddrs(func() []netip.Addr { return svc.State().Tailscale })

	listenSocket := s.opts.ListenSocket
	if listenSocket == nil {
		listenSocket = api.ListenSocket
	}
	ln, err := listenSocket(s.opts.Paths.ControllerSocket)
	if err != nil {
		return err
	}
	sock := srv.SocketServer()
	served := make(chan error, 1)
	go func() {
		err := sock.Serve(srv.SocketListener(ln, nil))
		if !errors.Is(err, http.ErrServerClosed) {
			s.log.Error("server: the controller socket stopped serving", "err", err)
			s.serveFailure(err)
		}
		served <- err
	}()
	s.onShutdown("controller socket", func(ctx context.Context) error {
		err := sock.Shutdown(ctx)
		if serveErr := <-served; !errors.Is(serveErr, http.ErrServerClosed) {
			err = errors.Join(err, serveErr)
		}
		return err
	})

	// Registered last, so the services stop first and see the dispatcher and
	// the listeners until they have returned.
	runner := startServiceRunner(serviceRunnerConfig{Services: services, Suspended: s.quarantined, Clock: s.clock, Log: s.log})
	s.onShutdown("controller services", func(context.Context) error { runner.stop(); return nil })

	urls := make([]string, 0, len(svc.State().Listeners))
	for _, b := range svc.State().Listeners {
		urls = append(urls, b.Endpoint.URL())
	}
	s.log.Info("server: serving", "message", svc.State().Message, "listeners", urls, "socket", s.opts.Paths.ControllerSocket)
	return nil
}

// startRuntimeListeners starts the runtime listeners and only then registers
// them with the guard topology: until Start has read the open rows the
// listeners cannot say which are missing, so the topology stays incomplete.
func (s *Server) startRuntimeListeners(ctx context.Context, handler listen.RuntimeHandlerFunc, tlsConfig *tls.Config) error {
	if err := s.runtimeListeners.Start(ctx, handler, tlsConfig); err != nil {
		return err
	}
	s.topo.setRuntime(s.runtimeListeners.Listening)
	return nil
}

// halfOfRemaining bounds a step to half of the time ctx has left. The runtime
// listeners stop before the network listeners and share one deadline with
// them, so a runtime listener that does not drain must not use all of it and
// leave the network listeners no time to stop.
func halfOfRemaining(ctx context.Context) (context.Context, context.CancelFunc) {
	deadline, ok := ctx.Deadline()
	if !ok {
		return context.WithCancel(ctx)
	}
	now := time.Now()
	return context.WithDeadline(ctx, now.Add(deadline.Sub(now)/2))
}

// withUnbound adds to the listener message that unbound runtime listeners
// are down, so an exposure whose listener cannot bind does not look live.
func withUnbound(message string, unbound int) string {
	switch unbound {
	case 0:
		return message
	case 1:
		return strings.TrimSpace(message + " One runtime listener could not be bound; retrying.")
	default:
		return strings.TrimSpace(fmt.Sprintf("%s %d runtime listeners could not be bound; retrying.", message, unbound))
	}
}

// serveFailure records err for Run; only the first error is kept.
func (s *Server) serveFailure(err error) {
	select {
	case s.serveFailed <- err:
	default:
	}
}

// Run starts a server, serves until ctx ends or a listener or the socket
// stops serving on its own, and then shuts it down, giving the listeners and
// the socket ShutdownTimeout to finish. It returns that serving error joined
// with Shutdown's. It is `darkpanel serve`.
func Run(ctx context.Context, opts Options) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	s, err := Start(ctx, opts)
	if err != nil {
		return err
	}
	var failed error
	select {
	case <-ctx.Done():
	case failed = <-s.serveFailed:
		s.log.Error("server: shutting down because a server stopped serving", "err", failed)
		cancel()
	}
	stop, stopCancel := context.WithTimeout(context.WithoutCancel(ctx), ShutdownTimeout)
	defer stopCancel()
	err = s.Shutdown(stop)
	// The socket's shutdown step already reports its serving error.
	if failed == nil || errors.Is(err, failed) {
		return err
	}
	return errors.Join(failed, err)
}

// webOrigins are the https origins the panel is served at: each bound
// HTTPS listener's and each open runtime listener's public host. The
// listeners start after the services, so before then only the runtime
// hosts are known.
func (s *Server) webOrigins(ctx context.Context) ([]string, error) {
	var out []string
	for _, b := range s.topo.webBound() {
		if b.Endpoint.TLS {
			out = append(out, b.Endpoint.URL())
		}
	}
	rows, err := s.controllerDB.Read().QueryContext(ctx,
		"SELECT public_host FROM core_listeners WHERE state = ? ORDER BY public_host", core.ListenerOpen)
	if err != nil {
		return nil, fmt.Errorf("server: list runtime listener hosts: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var host string
		if err := rows.Scan(&host); err != nil {
			return nil, fmt.Errorf("server: list runtime listener hosts: %w", err)
		}
		out = append(out, "https://"+host)
	}
	return out, rows.Err()
}

// whoisReader is the principal Tailscale identity sign-in runs
// tailscale.whois.get as, on the controller host's agent.
var whoisReader = core.Principal{Actor: "system:auth.tailscale", Scope: core.ScopeAdmin}

// whois runs tailscale.whois.get for addr on the controller host's agent.
func (s *Server) whois(ctx context.Context, addr netip.Addr) (json.RawMessage, error) {
	in, err := json.Marshal(map[string]string{"addr": addr.String()})
	if err != nil {
		return nil, err
	}
	return s.dispatcher.ReadInternal(ctx, whoisReader, s.localHostID, core.ActionRef{Name: "tailscale.whois.get", Version: 1}, in)
}
