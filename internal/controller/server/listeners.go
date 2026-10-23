package server

import (
	"context"
	"errors"
	"net/http"

	"github.com/darkraise/darkpanel/internal/controller/api"
	"github.com/darkraise/darkpanel/internal/controller/listen"
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

// Listeners are the network listeners currently bound.
func (s *Server) Listeners() []listen.Bound { return s.listenService.State().Listeners }

// AgentListeners are the agent listener's bound sockets: none with the
// embedded agent, and none after Shutdown.
func (s *Server) AgentListeners() []listen.Bound {
	if s.agentListen == nil {
		return nil
	}
	return s.agentListen.Bound()
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
	if err := s.runtimeListeners.Start(ctx, srv.RuntimeHandler, tlsConfig); err != nil {
		return err
	}
	s.onShutdown("runtime listeners", s.runtimeListeners.Shutdown)
	// Registered after the listeners, so it runs before their shutdown,
	// which waits for running requests.
	s.onShutdown("stream responses", func(context.Context) error { srv.CloseStreams(); return nil })
	// The source stays set after Shutdown, so the state then reads that the
	// listeners are shut down.
	srv.SetListenerState(func() api.ListenerState {
		st := svc.State()
		return api.ListenerState{TailscaleBound: st.TailscaleBound, Message: st.Message}
	})

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
