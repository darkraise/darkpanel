package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/darkraise/darkpanel/internal/agent/runtime"
	"github.com/darkraise/darkpanel/internal/buildinfo"
	"github.com/darkraise/darkpanel/internal/controller/api"
	"github.com/darkraise/darkpanel/internal/controller/audit"
	"github.com/darkraise/darkpanel/internal/controller/auth"
	"github.com/darkraise/darkpanel/internal/controller/coreactions"
	"github.com/darkraise/darkpanel/internal/controller/dispatch"
	"github.com/darkraise/darkpanel/internal/controller/events"
	"github.com/darkraise/darkpanel/internal/controller/fleet"
	"github.com/darkraise/darkpanel/internal/controller/listen"
	"github.com/darkraise/darkpanel/internal/controller/quarantine"
	"github.com/darkraise/darkpanel/internal/controller/rollout"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/lifecycle"
	"github.com/darkraise/darkpanel/internal/core/schema"
	"github.com/darkraise/darkpanel/internal/install"
	"github.com/darkraise/darkpanel/internal/modules"
	"github.com/darkraise/darkpanel/internal/platform"
	"github.com/darkraise/darkpanel/internal/registry"
	"github.com/darkraise/darkpanel/internal/update"
	"github.com/darkraise/darkpanel/internal/update/trustedkeys"
)

// PurgeInterval is how often the controller purges expired plans, sessions
// and tokens (spec section 20). The agent runtime runs its own purge.
const PurgeInterval = 10 * time.Minute

// indexTimeout bounds one read of the release index. The index is one small
// file, so its client may bound the whole request.
const indexTimeout = 30 * time.Second

// services are the controller's services, with the embedded agent's runtime
// or the agent listener's.
type services struct {
	auditLog    *audit.Store
	authService *auth.Service
	authority   *fleet.Authority
	registry    *core.Registry
	runtime     *runtime.Runtime // with EmbeddedAgent only
	sessions    *fleet.Registry  // without EmbeddedAgent only
	agentListen *listen.AgentService
	hosts       *dispatch.Hosts
	engine      *lifecycle.Engine
	ingester    *audit.Ingester
	dispatcher  *dispatch.Dispatcher
	topo        topology // the dispatcher's guard topology
	events      *events.Bus
	// runtimeListeners are the controller's runtime listeners (spec 1.10),
	// created before the engine so controller-side actions and contributions
	// get them, and started with the network listeners.
	runtimeListeners *listen.Runtime

	purgeMu     sync.Mutex // held while a purge runs, so Shutdown waits for it
	purgeTimer  core.Timer
	purgeClosed bool
}

// Registry is the server's registry, built with its services.
func (s *Server) Registry() *core.Registry { return s.registry }

// startServices builds the services over the migrated stores, in the order
// that the controller's foreign keys and recovery need: the embedded agent
// says Hello (seeding the local host's module rows from DefaultEnabled)
// before the controller engine recovers, and nothing is served until both
// have. Without the embedded agent every host is offline until its agent
// connects to the agent listener, which starts once the dispatcher exists.
//
// The agent authority is loaded in both modes, so the host actions work
// alike and a development database keeps one controller id across modes.
func (s *Server) startServices(ctx context.Context) error {
	s.events = events.NewBus(s.log)
	s.runtimeListeners = listen.NewRuntime(listen.RuntimeConfig{DB: s.controllerDB, Clock: s.clock, Log: s.log, Bind: s.opts.Bind})
	s.topo.setRuntime(s.runtimeListeners.Addrs)
	s.auditLog = audit.New(s.controllerDB, s.clock)
	s.authService = auth.New(s.controllerDB, s.controllerSealer, s.clock, s.auditLog, auth.Options{Params: s.opts.AuthParams})
	var err error
	if s.authority, err = fleet.LoadOrCreateAuthority(ctx, s.controllerDB, s.controllerSealer, s.clock); err != nil {
		return err
	}
	deps := coreactions.Deps{
		Auth: s.authService, Audit: s.auditLog, Modules: modules.All(),
		Extra: registry.CoreActions(nil, buildinfo.Version), OwnedPaths: install.CoreOwnedPaths(),
		Authority: s.authority, AgentAdvertise: s.opts.AgentAdvertise, Version: buildinfo.Version,
	}
	if !s.opts.EmbeddedAgent {
		s.sessions, err = fleet.NewRegistry(fleet.RegistryConfig{
			DB: s.controllerDB, Clock: s.clock, CheckPeer: s.authority.CheckPeer,
			Version: buildinfo.Version, ControllerID: s.authority.ControllerID(), Log: s.log,
			OnOnline: hostOnline(s.events),
		})
		if err != nil {
			return fmt.Errorf("server: %w", err)
		}
		// Assigned only when set: a nil *fleet.Registry in the interface
		// would not read as "no sessions".
		deps.Sessions = s.sessions
	}
	reg, err := registry.BuildWith(coreactions.Module(deps))
	if err != nil {
		return fmt.Errorf("server: registry: %w", err)
	}
	s.registry = reg

	var clients dispatch.ClientSource
	if s.opts.EmbeddedAgent {
		if clients, err = s.startEmbeddedAgent(ctx); err != nil {
			return err
		}
	} else {
		clients = s.sessions
	}
	s.hosts = dispatch.NewHosts(dispatch.HostsConfig{
		DB: s.controllerDB, Registry: reg, Clients: clients, Clock: s.clock, Log: s.log,
	})
	if s.opts.EmbeddedAgent {
		if err := s.hosts.Hello(ctx, s.localHostID); err != nil {
			return fmt.Errorf("server: hello from the local agent: %w", err)
		}
	}

	eng, err := lifecycle.New(s.engineConfig(reg))
	if err != nil {
		return fmt.Errorf("server: %w", err)
	}
	s.engine = eng
	s.onShutdown("controller engine", func(context.Context) error { eng.Close(); return nil })
	if err := eng.Recover(ctx); err != nil {
		return fmt.Errorf("server: recover: %w", err)
	}
	if err := s.startPurges(ctx); err != nil {
		return err
	}
	stopWatchers := events.StartWatchers(s.controllerDB, s.clock, s.events, s.networkVisible, s.log)
	s.onShutdown("event watchers", func(context.Context) error { stopWatchers(); return nil })

	s.ingester = audit.NewIngester(audit.IngesterConfig{
		Store: s.auditLog, Agents: s.hosts, Clock: s.clock, Log: s.log, OnEntries: s.ingestedEntries(eng.Wake, s.agentChange),
	})
	if err := s.ingester.IngestAll(ctx); err != nil {
		s.log.Warn("server: audit ingestion", "error", err)
	}
	s.ingester.Start()
	s.onShutdown("audit ingester", func(context.Context) error { s.ingester.Stop(); return nil })

	if s.sessions != nil {
		s.topo.setPeers(s.sessions.Peers)
	}
	s.dispatcher, err = dispatch.New(dispatch.Config{
		Registry: reg, Engine: eng, Hosts: s.hosts, DB: s.controllerDB, Clock: s.clock, Log: s.log, Ingest: s.ingester,
		Version: buildinfo.Version, Topology: s.topo.read, Quarantine: s.refuseQuarantined,
	})
	if err != nil {
		return fmt.Errorf("server: %w", err)
	}
	eng.SetChildren(s.dispatcher.Children())
	if !s.opts.EmbeddedAgent {
		if err := s.startAgentListener(ctx); err != nil {
			return err
		}
	}
	// After the agent listener (1.3a): composite recovery follows children
	// on agents that reconnect to it.
	if err := eng.RecoverComposites(ctx); err != nil {
		return fmt.Errorf("server: recover composites: %w", err)
	}
	return s.startRollouts(clients)
}

// hostOnline publishes the session registry's online changes as host
// events.
func hostOnline(bus *events.Bus) func(hostID string, online bool) {
	return func(hostID string, online bool) {
		bus.Publish(events.TypeHost, events.HostEvent{HostID: hostID, Online: online})
	}
}

// engineConfig is the controller engine's configuration. Its Publish hands
// the composite.step events to the event bus, and restore quarantine
// suspends its composite recovery, child watcher and module recovery
// handlers.
func (s *Server) engineConfig(reg *core.Registry) lifecycle.Config {
	return lifecycle.Config{
		Side: core.SideController, Registry: reg, Validator: schema.NewValidator(), Journal: s.auditLog,
		Redact: schema.Redact, Publish: s.events.Publish, Suspended: s.quarantined,
		Base: core.Env{
			DB: s.controllerDB, Sealer: s.controllerSealer, Clock: s.clock, Log: s.log, Agents: s.hosts,
			Listeners: s.runtimeListeners,
		},
	}
}

// quarantined reports whether the controller database is in restore
// quarantine (spec 10), which suspends every automatic mutation source.
func (s *Server) quarantined(ctx context.Context) (bool, error) {
	return quarantine.Active(ctx, s.controllerDB)
}

// refuseQuarantined is the dispatcher's gate: core.quarantined while the
// controller is in restore quarantine.
func (s *Server) refuseQuarantined(ctx context.Context) error {
	return quarantine.Check(ctx, s.controllerDB)
}

// ingestedEntries is the audit ingester's OnEntries: it wakes the
// composites following the stored entries' changes and publishes those
// changes, read through change, as change events.
func (s *Server) ingestedEntries(wake func(changeIDs ...string),
	change func(ctx context.Context, hostID, changeID string) (core.ChangeView, error)) func(hostID string, entries []core.JournalEntry) {
	wakeFn, publish := wakeComposites(wake), events.AgentChanges(s.events, change, s.networkVisible, s.log)
	return func(hostID string, entries []core.JournalEntry) {
		wakeFn(hostID, entries)
		publish(hostID, entries)
	}
}

// networkVisible is what the event bus may carry: its only reader is the
// network event stream, which must hide what the network change routes hide.
func (s *Server) networkVisible(v core.ChangeView) bool {
	return api.NetworkVisible(s.registry, v)
}

// agentChange reads changeID from hostID's agent.
func (s *Server) agentChange(ctx context.Context, hostID, changeID string) (core.ChangeView, error) {
	agent, err := s.hosts.Agent(ctx, hostID)
	if err != nil {
		return core.ChangeView{}, err
	}
	return agent.Change(ctx, changeID)
}

// wakePhases are the agent journal phases after which a composite following
// the change looks its child up at once (1.3 Child watcher).
var wakePhases = map[string]bool{
	"apply": true, "interrupt": true, "revert": true, "recover": true, "undo": true, "resolve": true, "release": true,
}

// wakeComposites wakes the composites following the changes of the stored
// entries in wakePhases, each change once: Wake runs a query per id under
// the ingestion lock.
func wakeComposites(wake func(changeIDs ...string)) func(hostID string, entries []core.JournalEntry) {
	return func(_ string, entries []core.JournalEntry) {
		var ids []string
		seen := map[string]bool{}
		for _, e := range entries {
			if wakePhases[e.Phase] && e.ChangeID != "" && !seen[e.ChangeID] {
				seen[e.ChangeID] = true
				ids = append(ids, e.ChangeID)
			}
		}
		if len(ids) > 0 {
			wake(ids...)
		}
	}
}

// startRollouts starts the rollout driver over the dispatcher and the update
// scheduler over clients, which tell it the online hosts. Shutdown stops the
// scheduler, then the driver, before the agent sessions and the dispatcher's
// services; each waits for a running pass.
func (s *Server) startRollouts(clients dispatch.ClientSource) error {
	index := s.opts.ReleaseIndex
	if index == nil {
		index = update.NewIndexSource(trustedkeys.BaseURL(), &http.Client{Timeout: indexTimeout}, s.clock)
	}
	driver, err := rollout.NewDriver(rollout.DriverConfig{
		DB: s.controllerDB, Dispatcher: s.dispatcher, Clock: s.clock, Log: s.log, Suspended: s.quarantined,
	})
	if err != nil {
		return fmt.Errorf("server: %w", err)
	}
	scheduler, err := rollout.NewScheduler(rollout.SchedulerConfig{
		DB: s.controllerDB, Clients: clients, Index: index, Audit: s.auditLog, Clock: s.clock, Log: s.log,
		Version: buildinfo.Version, Suspended: s.quarantined,
	})
	if err != nil {
		return fmt.Errorf("server: %w", err)
	}
	driver.Start()
	s.onShutdown("rollout driver", func(context.Context) error { driver.Stop(); return nil })
	scheduler.Start()
	s.onShutdown("update scheduler", func(context.Context) error { scheduler.Stop(); return nil })
	return nil
}

// startEmbeddedAgent opens the M1 in-process agent runtime over agent.db and
// returns the client source that holds it as the local host's agent.
func (s *Server) startEmbeddedAgent(ctx context.Context) (dispatch.ClientSource, error) {
	rt, err := runtime.Open(ctx, runtime.Config{
		DB: s.agentDB, Sealer: s.agentSealer, Clock: s.clock, Log: s.log, Registry: s.registry,
		HostID: s.localHostID, BinaryVersion: buildinfo.Version, BackupDir: s.opts.Paths.AgentBackups(),
		Exec: platform.NewRunner(platform.ProductionRules()),
	})
	if err != nil {
		return nil, err
	}
	s.runtime = rt
	s.onShutdown("agent runtime", func(context.Context) error { rt.Close(); return nil })

	// Every core_hosts row needs a client here, or Changes and ingestion
	// fail on it; the local host is the only one the embedded agent serves.
	clients := dispatch.NewStaticClients()
	clients.Add(s.localHostID, rt)
	return clients, nil
}

// startAgentListener binds the agent listener and serves its connections
// with the fleet server, whose agent sessions join the session registry.
// Shutdown runs these steps in reverse: the listener stops accepting, the
// fleet server closes the connections, and the registry ends the sessions,
// all before the dispatcher's services stop.
func (s *Server) startAgentListener(ctx context.Context) error {
	s.onShutdown("agent sessions", func(context.Context) error { s.sessions.Close(); return nil })
	srv, err := fleet.NewServer(fleet.ServerConfig{
		Authority: s.authority, Registry: s.sessions, Limiter: fleet.NewEnrollLimiter(s.clock), Log: s.log,
	})
	if err != nil {
		return fmt.Errorf("server: %w", err)
	}
	s.onShutdown("agent connections", func(context.Context) error { srv.Close(); return nil })
	svc, err := listen.StartAgent(ctx, listen.AgentConfig{
		Extra: s.opts.AgentListen, DB: s.controllerDB, Clock: s.clock, Serve: srv.Serve, Log: s.log,
		Interfaces: s.opts.Interfaces, Bind: s.opts.AgentBind,
	})
	if err != nil {
		return err
	}
	s.agentListen = svc
	s.topo.setAgent(svc.Bound)
	s.onShutdown("agent listener", svc.Shutdown)
	addrs := make([]string, 0, len(svc.Bound()))
	for _, b := range svc.Bound() {
		addrs = append(addrs, b.Addr.String())
	}
	s.log.Info("server: serving agents", "addrs", addrs)
	return nil
}

// purge deletes the controller engine's expired plans and the identity
// service's expired sessions and tokens.
func (s *Server) purge(ctx context.Context) error {
	return errors.Join(s.engine.Purge(ctx), s.authService.Purge(ctx))
}

// startPurges purges now, failing startup on an error, and then every
// PurgeInterval, logging an error.
func (s *Server) startPurges(ctx context.Context) error {
	if err := s.purge(ctx); err != nil {
		return fmt.Errorf("server: purge: %w", err)
	}
	s.purgeMu.Lock()
	s.purgeTimer = s.clock.AfterFunc(PurgeInterval, s.purgeTick)
	s.purgeMu.Unlock()
	s.onShutdown("purges", func(context.Context) error {
		s.purgeMu.Lock()
		defer s.purgeMu.Unlock()
		s.purgeClosed = true
		s.purgeTimer.Stop()
		return nil
	})
	return nil
}

func (s *Server) purgeTick() {
	s.purgeMu.Lock()
	defer s.purgeMu.Unlock()
	if s.purgeClosed {
		return
	}
	if err := s.purge(context.Background()); err != nil {
		s.log.Warn("server: purge", "error", err)
	}
	s.purgeTimer = s.clock.AfterFunc(PurgeInterval, s.purgeTick)
}
