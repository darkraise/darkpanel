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
}

// Listeners are the network listeners currently bound.
func (s *Server) Listeners() []listen.Bound { return s.listenService.State().Listeners }

// startListeners loads or creates the web authority, builds the API, starts
// the network listeners with it and serves the controller socket.
func (s *Server) startListeners(ctx context.Context) error {
	authority, err := listen.LoadOrCreateAuthority(ctx, s.controllerDB, s.controllerSealer, s.clock)
	if err != nil {
		return err
	}
	srv, err := api.New(api.Config{
		Dispatcher: s.dispatcher, Auth: s.authService, Registry: s.registry, WebCA: authority,
		SchemaVersion: func(ctx context.Context) (int, error) {
			return store.SchemaVersion(ctx, s.controllerDB, store.SideController)
		},
		HostID: s.localHostID, Dev: s.opts.Dev, UI: api.EmbeddedUI(), Log: s.log,
	})
	if err != nil {
		return err
	}
	s.apiServer = srv

	specs := s.opts.Listen
	if specs == nil {
		if specs, err = listen.ParseSpecs(nil); err != nil {
			return err
		}
	}
	svc, err := listen.Start(ctx, listen.Config{
		Specs: specs, Hostname: s.hostname, DB: s.controllerDB, Authority: authority, Clock: s.clock,
		Handler: srv.Handler(), Log: s.log, Interfaces: s.opts.Interfaces, Bind: s.opts.Bind,
	})
	if err != nil {
		return err
	}
	s.listenService = svc
	s.onShutdown("listeners", svc.Shutdown)
	// The source stays set after Shutdown, so the state then reads that the
	// listeners are shut down.
	srv.SetListenerState(func() api.ListenerState {
		st := svc.State()
		return api.ListenerState{TailscaleBound: st.TailscaleBound, Message: st.Message}
	})

	ln, err := api.ListenSocket(s.opts.Paths.ControllerSocket)
	if err != nil {
		return err
	}
	sock := srv.SocketServer()
	served := make(chan error, 1)
	go func() { served <- sock.Serve(srv.SocketListener(ln, nil)) }()
	s.onShutdown("controller socket", func(ctx context.Context) error {
		err := sock.Shutdown(ctx)
		if serveErr := <-served; !errors.Is(serveErr, http.ErrServerClosed) {
			err = errors.Join(err, serveErr)
		}
		return err
	})

	urls := make([]string, 0, len(svc.State().Listeners))
	for _, b := range svc.State().Listeners {
		urls = append(urls, b.Endpoint.URL())
	}
	s.log.Info("server: serving", "message", svc.State().Message, "listeners", urls, "socket", s.opts.Paths.ControllerSocket)
	return nil
}

// Run starts a server, serves until ctx ends and then shuts it down, giving
// the listeners and the socket ShutdownTimeout to finish. It is
// `darkpanel serve`.
func Run(ctx context.Context, opts Options) error {
	s, err := Start(ctx, opts)
	if err != nil {
		return err
	}
	<-ctx.Done()
	stop, cancel := context.WithTimeout(context.WithoutCancel(ctx), ShutdownTimeout)
	defer cancel()
	return s.Shutdown(stop)
}
