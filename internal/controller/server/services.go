package server

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/darkraise/darkpanel/internal/agent/runtime"
	"github.com/darkraise/darkpanel/internal/buildinfo"
	"github.com/darkraise/darkpanel/internal/controller/audit"
	"github.com/darkraise/darkpanel/internal/controller/auth"
	"github.com/darkraise/darkpanel/internal/controller/coreactions"
	"github.com/darkraise/darkpanel/internal/controller/dispatch"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/lifecycle"
	"github.com/darkraise/darkpanel/internal/core/schema"
	"github.com/darkraise/darkpanel/internal/modules"
	"github.com/darkraise/darkpanel/internal/platform"
	"github.com/darkraise/darkpanel/internal/registry"
)

// PurgeInterval is how often the controller purges expired plans, sessions
// and tokens (spec section 20). The agent runtime runs its own purge.
const PurgeInterval = 10 * time.Minute

// services are the controller's and the local agent's services.
type services struct {
	auditLog    *audit.Store
	authService *auth.Service
	registry    *core.Registry
	runtime     *runtime.Runtime
	hosts       *dispatch.Hosts
	engine      *lifecycle.Engine
	ingester    *audit.Ingester
	dispatcher  *dispatch.Dispatcher

	purgeMu     sync.Mutex // held while a purge runs, so Shutdown waits for it
	purgeTimer  core.Timer
	purgeClosed bool
}

// Registry is the server's registry, built with its services.
func (s *Server) Registry() *core.Registry { return s.registry }

// startServices builds the services over the migrated stores, in the order
// that the controller's foreign keys and recovery need: the agent half says
// Hello (seeding the local host's module rows from DefaultEnabled) before
// the controller engine recovers, and nothing is served until both have.
func (s *Server) startServices(ctx context.Context) error {
	s.auditLog = audit.New(s.controllerDB, s.clock)
	s.authService = auth.New(s.controllerDB, s.controllerSealer, s.clock, s.auditLog, auth.Options{Params: s.opts.AuthParams})
	reg, err := registry.BuildWith(coreactions.Module(coreactions.Deps{
		Auth: s.authService, Audit: s.auditLog, Modules: modules.All(),
	}))
	if err != nil {
		return fmt.Errorf("server: registry: %w", err)
	}
	s.registry = reg

	rt, err := runtime.Open(ctx, runtime.Config{
		DB: s.agentDB, Sealer: s.agentSealer, Clock: s.clock, Log: s.log, Registry: reg,
		HostID: s.localHostID, BinaryVersion: buildinfo.Version, BackupDir: s.opts.Paths.AgentBackups(),
		Exec: platform.NewRunner(platform.ProductionAllowlist()),
	})
	if err != nil {
		return err
	}
	s.runtime = rt
	s.onShutdown("agent runtime", func(context.Context) error { rt.Close(); return nil })

	// Every core_hosts row needs a client here, or Changes and ingestion
	// fail on it; the local host is the only one in M1.
	s.hosts = dispatch.NewHosts(dispatch.HostsConfig{DB: s.controllerDB, Registry: reg, Clock: s.clock, Log: s.log})
	s.hosts.Add(s.localHostID, rt)
	if err := s.hosts.Hello(ctx, s.localHostID); err != nil {
		return fmt.Errorf("server: hello from the local agent: %w", err)
	}

	eng, err := lifecycle.New(lifecycle.Config{
		Side: core.SideController, Registry: reg, Validator: schema.NewValidator(), Journal: s.auditLog,
		Redact: schema.Redact,
		Base: core.Env{
			DB: s.controllerDB, Sealer: s.controllerSealer, Clock: s.clock, Log: s.log, Agents: s.hosts,
		},
	})
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

	s.ingester = audit.NewIngester(audit.IngesterConfig{Store: s.auditLog, Agents: s.hosts, Clock: s.clock, Log: s.log})
	if err := s.ingester.IngestAll(ctx); err != nil {
		s.log.Warn("server: audit ingestion", "error", err)
	}
	s.ingester.Start()
	s.onShutdown("audit ingester", func(context.Context) error { s.ingester.Stop(); return nil })

	s.dispatcher, err = dispatch.New(dispatch.Config{
		Registry: reg, Engine: eng, Hosts: s.hosts, DB: s.controllerDB, Clock: s.clock, Log: s.log, Ingest: s.ingester,
	})
	if err != nil {
		return fmt.Errorf("server: %w", err)
	}
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
