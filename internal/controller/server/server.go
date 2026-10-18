// Package server assembles `darkpanel serve` (spec section 1, part P14): the
// controller and, in M1, the agent half in one process. Start opens the
// stores and starts the services and listeners in the order the spec sets;
// Shutdown stops them in reverse.
package server

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/oklog/ulid/v2"
	"golang.org/x/sys/unix"

	"github.com/darkraise/darkpanel/internal/buildinfo"
	"github.com/darkraise/darkpanel/internal/controller/auth"
	"github.com/darkraise/darkpanel/internal/controller/listen"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/secret"
	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/modules"
	"github.com/darkraise/darkpanel/internal/paths"
)

// LockName is the single-instance lock file, taken in the controller
// socket's directory and in both data directories, so two serves that share
// either the socket or a database refuse each other.
const LockName = "controller.lock"

// BackupsKept is how many migration backups the controller database keeps.
const BackupsKept = 5

// ShutdownTimeout bounds the graceful shutdown of the listeners and the
// controller socket in Run, and in the cleanup of a failed Start. The service
// stops after them take no deadline: the process exits right after, and a
// hung revert holds up Close (register row 51).
const ShutdownTimeout = 30 * time.Second

// lockStep names the Shutdown steps that release the locks.
const lockStep = "lock"

// timeFormat is the fixed-width RFC 3339 UTC form of every stored time.
const timeFormat = "2006-01-02T15:04:05.000000000Z"

// Options configure Start. Paths is required. Production passes
// paths.Resolve(dev), the parsed --listen values and nil or zero for every
// other field; tests override the rest.
type Options struct {
	Paths paths.Paths
	// Listen are the listeners to serve, from listen.ParseSpecs; nil means
	// the defaults, loopback and tailscale.
	Listen []listen.Spec
	// Dev accepts the service's own non-root uid on the controller socket.
	Dev bool
	// Clock is nil for core.SystemClock.
	Clock core.Clock
	// Log is nil to discard the service log.
	Log *slog.Logger
	// Hostname is nil for os.Hostname.
	Hostname func() (string, error)
	// Bind is nil for listen.Bind.
	Bind listen.BindFunc
	// Interfaces is nil for listen.SystemInterfaceAddrs.
	Interfaces listen.InterfaceAddrs
	// AuthParams is zero for auth.DefaultParams.
	AuthParams auth.Params
}

// closer is one thing Shutdown stops, in reverse order of acquisition.
type closer struct {
	name  string
	close func(ctx context.Context) error
}

// Server is one running controller with its local agent.
type Server struct {
	opts     Options
	clock    core.Clock
	log      *slog.Logger
	hostname string // "" when unknown

	controllerDB     *store.DB
	agentDB          *store.DB
	controllerSealer *secret.Sealer
	agentSealer      *secret.Sealer
	localHostID      string

	services
	listeners

	mu       sync.Mutex // serialises Shutdown
	closers  []closer
	shutdown bool
	err      error
}

// Start opens the stores and starts the services and the listeners. On an
// error it stops what it started and returns the error.
func Start(ctx context.Context, opts Options) (*Server, error) {
	if opts.Paths == (paths.Paths{}) {
		return nil, errors.New("server: Options.Paths is required")
	}
	s := &Server{opts: opts, clock: opts.Clock, log: opts.Log}
	if s.clock == nil {
		s.clock = core.SystemClock{}
	}
	if s.log == nil {
		s.log = slog.New(slog.DiscardHandler)
	}
	for _, step := range []func(context.Context) error{s.openStorage, s.startServices, s.startListeners} {
		if err := step(ctx); err != nil {
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), ShutdownTimeout)
			defer cancel()
			return nil, errors.Join(err, s.Shutdown(cleanup))
		}
	}
	return s, nil
}

// LocalHostID is the id of this host's core_hosts row.
func (s *Server) LocalHostID() string { return s.localHostID }

// SocketPath is the controller socket's path.
func (s *Server) SocketPath() string { return s.opts.Paths.ControllerSocket }

// onShutdown registers a step for Shutdown, which runs the steps in reverse.
func (s *Server) onShutdown(name string, stop func(ctx context.Context) error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closers = append(s.closers, closer{name, stop})
}

// Shutdown stops everything Start started, in reverse order, one step at a
// time; listeners let running requests finish until ctx ends. When a step
// failed and ctx has ended, the locks are kept until the process exits: a
// request that did not finish may still change the host, and another serve
// must not recover its change meanwhile. A second call, concurrent or later,
// waits for the first and returns its result.
func (s *Server) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.shutdown {
		return s.err
	}
	s.shutdown = true
	var errs []error
	for i := len(s.closers) - 1; i >= 0; i-- {
		if s.closers[i].name == lockStep && len(errs) > 0 && ctx.Err() != nil {
			s.log.Warn("server: shutdown ran out of time; the lock is kept until the process exits")
			continue
		}
		if err := s.closers[i].close(ctx); err != nil {
			errs = append(errs, fmt.Errorf("server: stop %s: %w", s.closers[i].name, err))
		}
	}
	s.closers = nil
	s.err = errors.Join(errs...)
	return s.err
}

// openStorage creates the directories, checks the socket's directory, loads
// or creates both keys, takes the single-instance locks, opens both
// databases, migrates the controller database and records the local host.
func (s *Server) openStorage(ctx context.Context) error {
	p := s.opts.Paths
	if err := p.MkdirAll(); err != nil {
		return err
	}
	runDir := filepath.Dir(p.ControllerSocket)
	if err := checkPrivateDir(runDir); err != nil {
		return err
	}
	var err error
	if s.controllerSealer, err = loadSealer(p.ControllerKey); err != nil {
		return err
	}
	if s.agentSealer, err = loadSealer(p.AgentKey); err != nil {
		return err
	}
	locked := map[string]bool{}
	for _, dir := range []string{runDir, p.ControllerDir, p.AgentDir} {
		// flock refuses a second lock on one file even within a process.
		if dir = filepath.Clean(dir); locked[dir] {
			continue
		}
		locked[dir] = true
		if err := s.lock(filepath.Join(dir, LockName)); err != nil {
			return err
		}
	}
	if s.controllerDB, err = s.openDB(ctx, "controller database", p.ControllerDB()); err != nil {
		return err
	}
	if s.agentDB, err = s.openDB(ctx, "agent database", p.AgentDB()); err != nil {
		return err
	}
	if err := s.migrateController(ctx); err != nil {
		return err
	}
	return s.ensureLocalHost(ctx)
}

// checkPrivateDir refuses a directory that another user could write, so no
// one else can replace the controller socket or the lock file (register row
// 69, obligation 4).
func checkPrivateDir(dir string) error {
	st, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("server: %w", err)
	}
	if !st.IsDir() {
		return fmt.Errorf("server: the controller socket directory %s is not a directory", dir)
	}
	if st.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("server: the controller socket directory %s is writable by group or other; run chmod 700 %s", dir, dir)
	}
	if sys, ok := st.Sys().(*syscall.Stat_t); ok && int64(sys.Uid) != int64(os.Geteuid()) {
		return fmt.Errorf("server: the controller socket directory %s is owned by uid %d, not by this process's uid %d", dir, sys.Uid, os.Geteuid())
	}
	return nil
}

func loadSealer(path string) (*secret.Sealer, error) {
	key, err := secret.LoadOrCreateKey(path)
	if err != nil {
		return nil, err
	}
	return secret.NewSealer(key)
}

// lock takes an exclusive flock on path, held until Shutdown, so a second
// serve on the same paths fails before it opens a database or touches the
// socket (register row 76).
func (s *Server) lock(path string) error {
	// A raw descriptor rather than an *os.File: no finalizer closes a lock
	// that Shutdown keeps before the process exits.
	fd, err := unix.Open(path, unix.O_RDWR|unix.O_CREAT|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return fmt.Errorf("server: open the lock file: %w", err)
	}
	if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = unix.Close(fd)
		if errors.Is(err, unix.EWOULDBLOCK) {
			return fmt.Errorf("server: another darkpanel serve holds %s; stop it first", path)
		}
		return fmt.Errorf("server: lock %s: %w", path, err)
	}
	// Closing the file releases the lock. The file stays, so a waiting
	// serve never locks a file that a third one has just replaced.
	s.onShutdown(lockStep, func(context.Context) error { return unix.Close(fd) })
	return nil
}

func (s *Server) openDB(ctx context.Context, name, path string) (*store.DB, error) {
	db, err := store.Open(ctx, path)
	if err != nil {
		return nil, err
	}
	s.onShutdown(name, func(context.Context) error { return db.Close() })
	return db, nil
}

// migrateController applies the core controller migrations and then each
// module's controller migrations. A failed module migration is logged and
// does not stop startup (R9); the error may quote SQL, so it is only logged.
func (s *Server) migrateController(ctx context.Context) error {
	coreSet, err := store.CoreMigrations(store.SideController)
	if err != nil {
		return err
	}
	var sets []store.MigrationSet
	for _, m := range modules.All() {
		man := m.Manifest()
		if fsys, ok := man.Migrations[core.SideController]; ok && fsys != nil {
			sets = append(sets, store.MigrationSet{Module: man.ID, FS: fsys})
		}
	}
	res, err := store.Migrate(ctx, s.controllerDB, store.MigrateOptions{
		Side: store.SideController, BinaryVersion: buildinfo.Version, BackupRoot: s.opts.Paths.ControllerBackups(),
		Keep: BackupsKept, Now: s.clock.Now(),
	}, coreSet, sets)
	if err != nil {
		return fmt.Errorf("server: controller migrations: %w", err)
	}
	for _, f := range res.Failed {
		s.log.Error("server: module controller migrations failed", "module", f.Module, "error", f.Err)
	}
	return nil
}

// ensureLocalHost creates this host's core_hosts row and core_meta's
// local_host_id on the first start, named from the hostname, and reads the
// id on every later start.
func (s *Server) ensureLocalHost(ctx context.Context) error {
	hostname := "localhost"
	lookup := s.opts.Hostname
	if lookup == nil {
		lookup = os.Hostname
	}
	if h, err := lookup(); err != nil || h == "" {
		s.log.Warn("server: the hostname is unknown; the local host is named localhost", "error", err)
	} else {
		hostname, s.hostname = h, h
	}
	err := s.controllerDB.Write(ctx, func(tx *sql.Tx) error {
		err := tx.QueryRowContext(ctx, `SELECT value FROM core_meta WHERE key = 'local_host_id'`).Scan(&s.localHostID)
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		now := s.clock.Now().UTC()
		id := ulid.MustNew(ulid.Timestamp(now), rand.Reader).String()
		if _, err := tx.ExecContext(ctx, `INSERT INTO core_hosts (id, name, is_local, created_at) VALUES (?, ?, 1, ?)`,
			id, hostname, now.Format(timeFormat)); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO core_meta (key, value) VALUES ('local_host_id', ?)`, id); err != nil {
			return err
		}
		s.localHostID = id
		return nil
	})
	if err != nil {
		return fmt.Errorf("server: local host record: %w", err)
	}
	return nil
}
