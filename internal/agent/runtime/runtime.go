// Package runtime is the agent half of Darkpanel (spec sections 1 and 3): it
// owns agent.db and its migrations, the agent lifecycle engine, module
// preflight checks and resource reads, and serves core.AgentClient in
// process. In M2 the mTLS transport serves the same methods.
package runtime

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/lifecycle"
	"github.com/darkraise/darkpanel/internal/core/schema"
	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/platform"
	"github.com/darkraise/darkpanel/schemas"
)

// ProtocolVersion is the agent protocol Hello reports.
const ProtocolVersion = 1

// PurgeInterval is how often expired plans are deleted (spec section 20).
const PurgeInterval = 10 * time.Minute

// BackupsKept is how many pre-migration backups of agent.db are kept.
const BackupsKept = 5

// ReasonMigrationFailed is the reason of a module whose agent migrations
// failed. The error itself is logged, because it may quote SQL.
const ReasonMigrationFailed = "the module's agent migrations failed; see the service log"

// Config is what the agent half needs, already resolved by the server.
type Config struct {
	DB            *store.DB      // agent.db, opened; Open migrates it
	Sealer        core.Sealer    // the agent key's sealer
	Clock         core.Clock     // also handed to handlers (hostinfo needs it)
	Log           *slog.Logger   // the service logger
	Registry      *core.Registry // the one registry of the process
	HostID        string         // the local host id
	BinaryVersion string         // buildinfo.Version, for the migration backup
	BackupDir     string         // paths.AgentBackups()
	Exec          core.Runner    // platform.NewRunner(platform.ProductionAllowlist()) in production
	FilesTestMode bool           // platform.FilesConfig.TestMode; false in production
}

func (c Config) check() error {
	required := []struct {
		name string
		set  bool
	}{
		{"DB", c.DB != nil}, {"Sealer", c.Sealer != nil}, {"Clock", c.Clock != nil}, {"Log", c.Log != nil},
		{"Registry", c.Registry != nil}, {"HostID", c.HostID != ""}, {"BinaryVersion", c.BinaryVersion != ""},
		{"BackupDir", c.BackupDir != ""}, {"Exec", c.Exec != nil},
	}
	for _, r := range required {
		if !r.set {
			return fmt.Errorf("runtime: Config.%s is required", r.name)
		}
	}
	return nil
}

// Runtime is the agent half of one host. Its methods are safe for concurrent
// use.
type Runtime struct {
	cfg     Config
	base    core.Env
	engine  *lifecycle.Engine
	journal *lifecycle.AgentJournal
	hashes  map[string]core.SchemaHash // every agent-side action version's schema hashes, fixed at Open

	statusMu sync.Mutex
	status   map[string]core.ModuleStatus
	broken   map[string]bool // modules whose migrations failed; unusable until a restart

	// loopMu is held while a purge runs, so Close waits for it.
	loopMu sync.Mutex
	timer  core.Timer
	closed bool
}

// Open migrates agent.db (core first, then each module's agent migrations; a
// failed module migration makes that module unavailable), builds the one
// platform.Files of the agent from the registry's owned paths, builds the
// agent lifecycle engine, runs its Recover once, runs every module's
// preflight checks, purges expired plans and then every PurgeInterval. Call
// Close on shutdown.
func Open(ctx context.Context, cfg Config) (*Runtime, error) {
	if err := cfg.check(); err != nil {
		return nil, err
	}
	r := &Runtime{cfg: cfg, status: map[string]core.ModuleStatus{}, broken: map[string]bool{}}
	hashes, err := schemas.ActionHashes(cfg.Registry, core.SideAgent)
	if err != nil {
		return nil, fmt.Errorf("runtime: %w", err)
	}
	r.hashes = hashes
	if err := r.migrate(ctx); err != nil {
		return nil, err
	}
	files := platform.NewFiles(platform.FilesConfig{
		Owned:     platform.OwnedPaths(cfg.Registry.Modules()),
		Ownership: platform.NewOwnership(cfg.DB),
		Clock:     cfg.Clock,
		TestMode:  cfg.FilesTestMode,
	})
	r.base = core.Env{
		HostID: cfg.HostID, DB: cfg.DB, Exec: cfg.Exec, Files: files, Sealer: cfg.Sealer, Clock: cfg.Clock, Log: cfg.Log,
	}
	r.journal = lifecycle.NewAgentJournal(cfg.DB)
	eng, err := lifecycle.New(lifecycle.Config{
		Side: core.SideAgent, Registry: cfg.Registry, Validator: schema.NewValidator(), Journal: r.journal,
		Redact: schema.Redact, Base: r.base,
	})
	if err != nil {
		return nil, fmt.Errorf("runtime: %w", err)
	}
	r.engine = eng
	if err := eng.Recover(ctx); err != nil {
		eng.Close()
		return nil, fmt.Errorf("runtime: recover: %w", err)
	}
	for _, m := range r.modules() {
		r.setStatus(r.check(ctx, m))
	}
	if err := eng.Purge(ctx); err != nil {
		eng.Close()
		return nil, fmt.Errorf("runtime: %w", err)
	}
	r.timer = cfg.Clock.AfterFunc(PurgeInterval, r.purge)
	return r, nil
}

// Close stops the purge loop, waiting for a running purge, and closes the
// engine, which waits for a running timer revert. The caller closes the
// database afterwards.
func (r *Runtime) Close() {
	r.loopMu.Lock()
	r.closed = true
	if r.timer != nil {
		r.timer.Stop()
	}
	r.loopMu.Unlock()
	r.engine.Close()
}

func (r *Runtime) purge() {
	r.loopMu.Lock()
	defer r.loopMu.Unlock()
	if r.closed {
		return
	}
	if err := r.engine.Purge(context.Background()); err != nil {
		r.cfg.Log.Warn("runtime: purge", "error", err)
	}
	r.timer = r.cfg.Clock.AfterFunc(PurgeInterval, r.purge)
}

// modules returns the registered modules except the core, which has no agent
// half, in registration order.
func (r *Runtime) modules() []core.Manifest {
	var out []core.Manifest
	for _, m := range r.cfg.Registry.Modules() {
		if m.ID != core.ModuleIDCore {
			out = append(out, m)
		}
	}
	return out
}

func (r *Runtime) migrate(ctx context.Context) error {
	coreSet, err := store.CoreMigrations(store.SideAgent)
	if err != nil {
		return fmt.Errorf("runtime: %w", err)
	}
	var sets []store.MigrationSet
	for _, m := range r.modules() {
		if fsys := m.Migrations[core.SideAgent]; fsys != nil {
			sets = append(sets, store.MigrationSet{Module: m.ID, FS: fsys})
		}
	}
	res, err := store.Migrate(ctx, r.cfg.DB, store.MigrateOptions{
		Side: store.SideAgent, BinaryVersion: r.cfg.BinaryVersion, BackupRoot: r.cfg.BackupDir,
		Keep: BackupsKept, Now: r.cfg.Clock.Now(),
	}, coreSet, sets)
	if err != nil {
		return fmt.Errorf("runtime: agent migrations: %w", err)
	}
	if res.BackupDir != "" {
		r.cfg.Log.Info("runtime: agent database backed up before migrating", "dir", res.BackupDir)
	}
	for _, f := range res.Failed {
		r.cfg.Log.Error("runtime: module migrations failed", "module", f.Module, "error", f.Err)
		r.broken[f.Module] = true
	}
	return nil
}

// check runs m's preflight checks in order; the first failure's error text is
// the reason. A module whose migrations failed is not checked.
func (r *Runtime) check(ctx context.Context, m core.Manifest) core.ModuleStatus {
	st := core.ModuleStatus{ID: m.ID, Version: m.Version}
	if r.isBroken(m.ID) {
		st.Reason = ReasonMigrationFailed
		return st
	}
	for _, c := range m.Preflight {
		err := lifecycle.Protect(r.cfg.Log, m.ID, "preflight check "+c.ID, func() error { return c.Run(ctx, r.base) })
		if err != nil {
			st.Reason = err.Error()
			return st
		}
	}
	st.Available = true
	return st
}

func (r *Runtime) isBroken(moduleID string) bool {
	r.statusMu.Lock()
	defer r.statusMu.Unlock()
	return r.broken[moduleID]
}

func (r *Runtime) setStatus(st core.ModuleStatus) {
	r.statusMu.Lock()
	defer r.statusMu.Unlock()
	r.status[st.ID] = st
}

// statuses returns every module's last status in registration order.
func (r *Runtime) statuses() []core.ModuleStatus {
	r.statusMu.Lock()
	defer r.statusMu.Unlock()
	out := []core.ModuleStatus{}
	for _, m := range r.modules() {
		if st, ok := r.status[m.ID]; ok {
			out = append(out, st)
		}
	}
	return out
}
