// Package runtime is the agent half of Darkpanel (spec sections 1 and 3): it
// owns agent.db and its migrations, the agent lifecycle engine, module
// preflight checks and resource reads, and serves core.AgentClient in
// process. In M2 the mTLS transport serves the same methods.
package runtime

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"io"
	iofs "io/fs"
	"log/slog"
	"net"
	"net/netip"
	"slices"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
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
	Exec          core.Runner    // platform.NewRunner(platform.ProductionRules()) in production
	FilesTestMode bool           // platform.FilesConfig.TestMode; false in production
	// Units starts and reads task units (1.3c); nil refuses task actions.
	// Each module's handlers get it scoped to the module (ModuleUnits).
	Units core.Units
	// TaskBinary is the darkpanel binary a task unit runs, paths.Binary().
	TaskBinary string
	// ControllerConnected is Hello's controllerConnected: nil while the
	// agent is not enrolled, else whether a controller session is live. A
	// nil function reports nil, as an agent without a transport does.
	ControllerConnected func() *bool
	// ControllerAddress is the host:port of the controller the agent is
	// enrolled with, "" when it is not; nil means "". Env.ControllerAddrs
	// resolves it per call.
	ControllerAddress func() string
	// PolicyFile is the host-local policy file, PolicyPath in production;
	// "" reads none and denies nothing.
	PolicyFile string
	// PolicyOwner is the uid that must own PolicyFile: root, 0, in
	// production; tests that do not run as root set their own.
	PolicyOwner uint32
	// Shell starts the console's root shells, platform.NewShell in
	// production; nil refuses every terminal. The console's Env.Shell
	// reaches it only through the runtime's flag and policy check.
	Shell core.ShellStarter
	// ConsoleFS is the console's file manager, platform.NewConsoleFiles in
	// production; nil refuses every file operation. The console's
	// Env.ConsoleFS reaches it only through the runtime's flag and policy
	// check.
	ConsoleFS core.ConsoleFiles
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
	epoch   string                     // the journal epoch, fixed at Open
	// moduleUnits is platform.ModuleUnits over Config.Units: each module's
	// unit client, for the engine and for moduleEnv.
	moduleUnits func(module string) core.Units

	// statusMu also guards policy, so a status is stored against the policy
	// in force when it is stored: a check that began under an older policy
	// cannot store a denied module as available.
	statusMu sync.Mutex
	status   map[string]core.ModuleStatus
	broken   map[string]bool // modules whose migrations failed; unusable until a restart
	policy   Policy          // the host-local policy file's, from Open on

	bg *background // the modules' background tasks, from the end of Open

	streams   streamSet   // the running module streams; Close ends them
	channels  channelSet  // the open module channels; Close ends them
	transfers transferSet // the running uploads and downloads; Close ends them

	// enableMu orders flag changes and policy reloads, so the last one's
	// hook call matches the stored flag and the policy in force.
	enableMu sync.Mutex

	// loopMu is held while a purge runs, so Close waits for it.
	loopMu sync.Mutex
	timer  core.Timer
	closed bool
}

// Open migrates agent.db (core first, then each module's agent migrations; a
// failed module migration makes that module unavailable), reads the policy
// file, builds the one
// platform.Files of the agent from the registry's owned paths, builds the
// agent lifecycle engine, runs its Recover once, runs every module's
// preflight checks, purges expired plans and then every PurgeInterval, and
// starts the modules' background tasks. Call Close on shutdown.
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
	if r.epoch, err = journalEpoch(ctx, cfg.DB, cfg.Clock.Now()); err != nil {
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
	r.moduleUnits = platform.ModuleUnits(cfg.Units, cfg.Registry)
	r.journal = lifecycle.NewAgentJournal(cfg.DB)
	eng, err := lifecycle.New(lifecycle.Config{
		Side: core.SideAgent, Registry: cfg.Registry, Validator: schema.NewValidator(), Journal: r.journal,
		Redact: schema.Redact, Base: r.base, Units: cfg.Units, TaskBinary: cfg.TaskBinary, ModuleUnits: r.moduleUnits,
		ModuleEnv: r.withProviders,
	})
	if err != nil {
		return nil, fmt.Errorf("runtime: %w", err)
	}
	r.engine = eng
	if err := eng.Recover(ctx); err != nil {
		eng.Close()
		return nil, fmt.Errorf("runtime: recover: %w", err)
	}
	r.loadPolicy()
	for _, m := range r.modules() {
		r.setStatus(r.check(ctx, m))
	}
	if err := eng.Purge(ctx); err != nil {
		eng.Close()
		return nil, fmt.Errorf("runtime: %w", err)
	}
	r.callHooks(ctx)
	r.startBackground(ctx)
	r.timer = cfg.Clock.AfterFunc(PurgeInterval, r.purge)
	return r, nil
}

// Close stops the background tasks, waiting for each Run and Watch to return,
// ends every open module stream, waiting until each stream's Run returned,
// closes every open channel with reason "agent restarted", waiting until
// each channel's Open returned,
// stops the purge loop, waiting for a running purge, and closes the engine,
// which waits for a running timer revert. The caller closes the database
// afterwards.
func (r *Runtime) Close() {
	r.stopBackground()
	r.streams.close()
	r.channels.closeAll(CloseAgentRestarted)
	r.transfers.closeAll()
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

// modules returns the registered modules in registration order. The core is
// one of them: its agent-side actions run like any module's, and it has no
// preflight checks, so it is always available.
func (r *Runtime) modules() []core.Manifest {
	return r.cfg.Registry.Modules()
}

func (r *Runtime) migrate(ctx context.Context) error {
	coreSet, err := store.CoreMigrations(store.SideAgent)
	if err != nil {
		return fmt.Errorf("runtime: %w", err)
	}
	var sets []store.MigrationSet
	for _, m := range r.modules() {
		// The core's tables come from coreSet, never from its manifest.
		if fsys := m.Migrations[core.SideAgent]; fsys != nil && m.ID != core.ModuleIDCore {
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
	if res.PruneErr != nil {
		r.cfg.Log.Warn("runtime: old agent database backups could not be pruned", "error", res.PruneErr)
	}
	for _, f := range res.Failed {
		r.cfg.Log.Error("runtime: module migrations failed", "module", f.Module, "error", f.Err)
		r.broken[f.Module] = true
	}
	return nil
}

// journalEpochKey is the core_meta key of the journal epoch (spec section
// 5): a ULID created with agent.db and kept across restarts and
// re-enrollments, so the controller never ingests an acknowledged entry
// twice. A migration that recreates core_journal must delete this row in the
// same file: the next Open then creates a new epoch, and the controller
// restarts its cursor instead of skipping the new journal's first entries.
const journalEpochKey = "journal_epoch"

// journalEpoch returns agent.db's journal epoch, creating it when the
// database has none.
func journalEpoch(ctx context.Context, db *store.DB, now time.Time) (string, error) {
	var epoch string
	err := db.Write(ctx, func(tx *sql.Tx) error {
		err := tx.QueryRowContext(ctx, "SELECT value FROM core_meta WHERE key = ?", journalEpochKey).Scan(&epoch)
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		epoch = ulid.MustNew(ulid.Timestamp(now), rand.Reader).String()
		_, err = tx.ExecContext(ctx, "INSERT INTO core_meta (key, value) VALUES (?, ?)", journalEpochKey, epoch)
		return err
	})
	if err != nil {
		return "", fmt.Errorf("runtime: journal epoch: %w", err)
	}
	return epoch, nil
}

// check runs m's preflight checks in order; the first failure's error text is
// the reason. A module the policy file denies, or whose migrations failed, is
// not checked.
func (r *Runtime) check(ctx context.Context, m core.Manifest) core.ModuleStatus {
	st := core.ModuleStatus{ID: m.ID, Version: m.Version}
	if r.Policy().Denied(m.ID) {
		st.Reason = PolicyReason
		return st
	}
	if r.isBroken(m.ID) {
		st.Reason = ReasonMigrationFailed
		return st
	}
	for _, c := range m.Preflight {
		err := lifecycle.Protect(r.cfg.Log, m.ID, "preflight check "+c.ID, func() error { return c.Run(ctx, r.moduleEnv(m.ID)) })
		if err != nil {
			st.Reason = err.Error()
			return st
		}
	}
	st.Available = true
	return st
}

// moduleEnv is the Env of module's preflight checks and resources: the
// runtime's base with the module's unit client.
func (r *Runtime) moduleEnv(module string) core.Env {
	env := r.base
	env.Units = r.moduleUnits(module)
	return r.withProviders(module, env)
}

// controllerAddrsModule is the one module whose Env carries the
// controller's addresses: fail2ban never bans them.
const controllerAddrsModule = "fail2ban"

// lookupHost resolves a controller host name; tests replace it.
var lookupHost = net.DefaultResolver.LookupHost

// withProviders adds module's own providers to env.
func (r *Runtime) withProviders(module string, env core.Env) core.Env {
	env = ModuleProviders(r.cfg.ControllerAddress, r.cfg.Log)(module, env)
	if module == consoleModule {
		env.Shell = shellGuard{r: r, module: module}
		env.Journal = moduleJournal{r: r}
		env.ConsoleFS = filesGuard{r: r, module: module}
	}
	return env
}

// consoleModule is the one module whose Env carries a shell, the file
// manager and the module journal.
const consoleModule = "console"

// shellGuard is the console's Env.Shell: each Start first checks, as the
// call is made, that the policy file does not deny the module and that its
// enabled flag is 1, so a shell never starts for a module turned off.
type shellGuard struct {
	r      *Runtime
	module string
}

func (g shellGuard) Start(ctx context.Context, cols, rows uint16) (core.ShellSession, error) {
	if g.r.Policy().Denied(g.module) {
		return nil, deniedError(g.module)
	}
	enabled, err := g.r.moduleEnabled(ctx, g.module)
	if err != nil {
		return nil, err
	}
	if !enabled {
		return nil, errcode.New(errcode.ModuleUnavailable, fmt.Sprintf("module %s is unavailable: it is disabled on this host", g.module))
	}
	if g.r.cfg.Shell == nil {
		return nil, errcode.New(errcode.Unavailable, "this agent cannot start a terminal")
	}
	return g.r.cfg.Shell.Start(ctx, cols, rows)
}

// filesGuard is the console's Env.ConsoleFS: each call first checks, as it
// is made, that the policy file does not deny the module and that its
// enabled flag is 1. RemoveTemp, which only removes a temporary file a
// write left behind, runs whatever the flag.
type filesGuard struct {
	r      *Runtime
	module string
}

var _ core.ConsoleFiles = filesGuard{}

func (g filesGuard) check(ctx context.Context) (core.ConsoleFiles, error) {
	if g.r.Policy().Denied(g.module) {
		return nil, deniedError(g.module)
	}
	enabled, err := g.r.moduleEnabled(ctx, g.module)
	if err != nil {
		return nil, err
	}
	if !enabled {
		return nil, errcode.New(errcode.ModuleUnavailable, fmt.Sprintf("module %s is unavailable: it is disabled on this host", g.module))
	}
	if g.r.cfg.ConsoleFS == nil {
		return nil, errcode.New(errcode.Unavailable, "this agent has no file manager")
	}
	return g.r.cfg.ConsoleFS, nil
}

func (g filesGuard) List(ctx context.Context, path, cursor string) (core.DirPage, error) {
	fs, err := g.check(ctx)
	if err != nil {
		return core.DirPage{}, err
	}
	return fs.List(ctx, path, cursor)
}

func (g filesGuard) Stat(ctx context.Context, path string) (core.FileInfo, error) {
	fs, err := g.check(ctx)
	if err != nil {
		return core.FileInfo{}, err
	}
	return fs.Stat(ctx, path)
}

func (g filesGuard) ReadText(ctx context.Context, path string) ([]byte, string, error) {
	fs, err := g.check(ctx)
	if err != nil {
		return nil, "", err
	}
	return fs.ReadText(ctx, path)
}

func (g filesGuard) Open(ctx context.Context, path string) (io.ReadCloser, int64, error) {
	fs, err := g.check(ctx)
	if err != nil {
		return nil, 0, err
	}
	return fs.Open(ctx, path)
}

func (g filesGuard) WriteText(ctx context.Context, path string, data []byte, expectSHA string) (string, error) {
	fs, err := g.check(ctx)
	if err != nil {
		return "", err
	}
	return fs.WriteText(ctx, path, data, expectSHA)
}

func (g filesGuard) Upload(ctx context.Context, path string, r io.Reader, size int64) (string, error) {
	fs, err := g.check(ctx)
	if err != nil {
		return "", err
	}
	return fs.Upload(ctx, path, r, size)
}

func (g filesGuard) Mkdir(ctx context.Context, path string) error {
	fs, err := g.check(ctx)
	if err != nil {
		return err
	}
	return fs.Mkdir(ctx, path)
}

func (g filesGuard) Rename(ctx context.Context, from, to string) error {
	fs, err := g.check(ctx)
	if err != nil {
		return err
	}
	return fs.Rename(ctx, from, to)
}

func (g filesGuard) Delete(ctx context.Context, path, confirm string) error {
	fs, err := g.check(ctx)
	if err != nil {
		return err
	}
	return fs.Delete(ctx, path, confirm)
}

func (g filesGuard) Chmod(ctx context.Context, path string, mode iofs.FileMode) error {
	fs, err := g.check(ctx)
	if err != nil {
		return err
	}
	return fs.Chmod(ctx, path, mode)
}

func (g filesGuard) Chown(ctx context.Context, path string, uid, gid int) error {
	fs, err := g.check(ctx)
	if err != nil {
		return err
	}
	return fs.Chown(ctx, path, uid, gid)
}

func (g filesGuard) RemoveTemp(ctx context.Context, path string) error {
	if g.r.cfg.ConsoleFS == nil {
		return nil // nothing was ever written
	}
	return g.r.cfg.ConsoleFS.RemoveTemp(ctx, path)
}

// moduleJournal is a module's Env.Journal: one entry per transaction
// through the agent journal the lifecycle appends to.
type moduleJournal struct{ r *Runtime }

func (j moduleJournal) Record(ctx context.Context, e core.JournalEntry) error {
	if e.At.IsZero() {
		e.At = j.r.cfg.Clock.Now()
	}
	return j.r.cfg.DB.Write(ctx, func(tx *sql.Tx) error { return j.r.journal.Append(ctx, tx, e) })
}

// homeModule is the one module whose Env carries the users' authorized
// keys.
const homeModule = "users"

// ModuleProviders is the lifecycle ModuleEnv of an agent engine: it sets
// Env.ControllerAddrs for the fail2ban module from controllerAddress, the
// enrolled controller's host:port (nil or "" gives none), resolved on each
// call, and Env.Home for the users module. A name that does not resolve
// within 2 s gives none; log records why.
func ModuleProviders(controllerAddress func() string, log *slog.Logger) func(module string, env core.Env) core.Env {
	return func(module string, env core.Env) core.Env {
		if module == controllerAddrsModule && controllerAddress != nil {
			env.ControllerAddrs = resolveController(controllerAddress(), log)
		}
		if module == homeModule {
			env.Home = platform.NewHomeFiles("/etc/passwd")
		}
		return env
	}
}

func resolveController(hostport string, log *slog.Logger) []string {
	host, _, err := net.SplitHostPort(hostport)
	if err != nil || host == "" {
		return nil
	}
	if a, err := netip.ParseAddr(host); err == nil {
		return []string{a.Unmap().String()}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	names, err := lookupHost(ctx, host)
	if err != nil {
		log.Warn("runtime: resolve the controller address", "host", host, "error", err)
		return nil
	}
	var out []string
	for _, n := range names {
		if a, err := netip.ParseAddr(n); err == nil && !slices.Contains(out, a.Unmap().String()) {
			out = append(out, a.Unmap().String())
		}
	}
	slices.Sort(out)
	return out
}

func (r *Runtime) isBroken(moduleID string) bool {
	r.statusMu.Lock()
	defer r.statusMu.Unlock()
	return r.broken[moduleID]
}

// setStatus stores st, as the policy in force reports it, and returns what
// it stored.
func (r *Runtime) setStatus(st core.ModuleStatus) core.ModuleStatus {
	r.statusMu.Lock()
	defer r.statusMu.Unlock()
	if r.policy.Denied(st.ID) {
		st.Available, st.Reason = false, PolicyReason
	}
	r.status[st.ID] = st
	return st
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
