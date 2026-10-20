package install

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/secret"
	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/platform"
	"github.com/darkraise/darkpanel/internal/update"
)

// StopTimeout bounds the wait for a stopped service to become inactive; the
// units stop within their TimeoutStopSec of 10 s.
const StopTimeout = 30 * time.Second

// UninstallConfig is what darkpanel uninstall needs.
type UninstallConfig struct {
	Layout Layout
	// KeepData keeps the data and configuration directories, with the
	// databases, backups, update records, role keys and agent credentials,
	// and every user, so that a later install finds them.
	KeepData bool
	// Modules are the compiled-in modules; each that implements
	// core.Uninstaller runs its step.
	Modules []core.Module
	Systemd platform.Systemd
	Exec    core.Runner
	Users   Users
	Clock   core.Clock
	Log     *slog.Logger
	// FilesTestMode is platform.FilesConfig.TestMode: false in production.
	FilesTestMode bool
	// PollInterval and StopTimeout pace the wait for stopped services; zero
	// means 1 s and StopTimeout.
	PollInterval time.Duration
	StopTimeout  time.Duration
}

func (c UninstallConfig) check() error {
	required := []struct {
		name string
		set  bool
	}{
		{"Systemd", c.Systemd != nil}, {"Exec", c.Exec != nil}, {"Users", c.Users != nil},
		{"Clock", c.Clock != nil}, {"Log", c.Log != nil},
	}
	for _, r := range required {
		if !r.set {
			return fmt.Errorf("install: UninstallConfig.%s is required", r.name)
		}
	}
	return nil
}

// Uninstall is a planned uninstall: the ownership manifest, read into memory
// before anything is removed, and the steps that remove it.
type Uninstall struct {
	cfg     UninstallConfig
	db      *store.DB      // nil when agent.db does not exist
	own     core.Ownership // nil without db
	files   *platform.Files
	sealer  core.Sealer
	entries []core.OwnedEntry
	steps   []uninstallStep
	lockFD  int // the agent lock, -1 until taken
}

type uninstallStep struct {
	name string
	plan []string // what the step does, for Describe
	run  func(ctx context.Context) (string, error)
}

// PlanUninstall reads the ownership manifest of agent.db, adds the core's
// paths in case the manifest lacks them, and plans the steps. Nothing on the
// host changes until Run. Call Run or Close.
func PlanUninstall(ctx context.Context, cfg UninstallConfig) (*Uninstall, error) {
	if err := cfg.check(); err != nil {
		return nil, err
	}
	if cfg.PollInterval == 0 {
		cfg.PollInterval = pollInterval
	}
	if cfg.StopTimeout == 0 {
		cfg.StopTimeout = StopTimeout
	}
	u := &Uninstall{cfg: cfg, lockFD: -1}
	if err := u.load(ctx); err != nil {
		_ = u.Close()
		return nil, fmt.Errorf("install: plan uninstall: %w", err)
	}
	u.steps = u.plan()
	return u, nil
}

// load opens agent.db and the agent key when they exist and collects the
// entries to remove.
func (u *Uninstall) load(ctx context.Context) error {
	p := u.cfg.Layout.Paths
	if _, err := os.Lstat(p.AgentDB()); err == nil {
		if u.db, err = store.Open(ctx, p.AgentDB()); err != nil {
			return err
		}
		u.own = platform.NewOwnership(u.db)
		if u.entries, err = u.own.List(ctx); err != nil {
			return err
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("install: %w", err)
	}
	for _, op := range OwnedPaths(u.cfg.Layout, RoleController) {
		e := core.OwnedEntry{Path: op.Path, Module: core.ModuleIDCore, Kind: op.Kind}
		if !slices.ContainsFunc(u.entries, func(x core.OwnedEntry) bool { return x.Path == e.Path && x.Module == e.Module && x.BlockID == "" }) {
			u.entries = append(u.entries, e)
		}
	}
	if _, err := os.Lstat(p.AgentKey); err == nil {
		key, err := secret.LoadOrCreateKey(p.AgentKey)
		if err != nil {
			return err
		}
		s, err := secret.NewSealer(key)
		if err != nil {
			return err
		}
		u.sealer = s
	}
	if u.db != nil {
		u.files = platform.NewFiles(platform.FilesConfig{
			Owned: u.owned(), Ownership: u.own, Clock: u.cfg.Clock, TestMode: u.cfg.FilesTestMode,
		})
	}
	return nil
}

// owned is every module's declaration plus every file and block of the
// manifest, so an entry whose module no longer declares it is still removed.
func (u *Uninstall) owned() map[string][]core.OwnedPath {
	var mans []core.Manifest
	for _, m := range u.cfg.Modules {
		mans = append(mans, m.Manifest())
	}
	owned := platform.OwnedPaths(mans)
	for _, e := range u.entries {
		ops := owned[e.Module]
		i := slices.IndexFunc(ops, func(op core.OwnedPath) bool { return op.Path == e.Path && op.Kind == e.Kind })
		switch {
		case e.Kind != core.OwnedFile && e.Kind != core.OwnedBlock:
		case i < 0:
			op := core.OwnedPath{Path: e.Path, Kind: e.Kind}
			if e.BlockID != "" {
				op.BlockIDs = []string{e.BlockID}
			}
			owned[e.Module] = append(ops, op)
		case e.BlockID != "" && !slices.Contains(ops[i].BlockIDs, e.BlockID):
			ops[i].BlockIDs = append(slices.Clone(ops[i].BlockIDs), e.BlockID)
		}
	}
	return owned
}

// within reports whether path is dir or lies under it.
func within(path, dir string) bool { return path == dir || strings.HasPrefix(path, dir+"/") }

// kept reports whether the files step leaves e: --keep-data keeps everything
// under the data and configuration directories, and otherwise the core's
// entries there go with the data step, so the agent key outlives every step
// that can fail before it.
func (u *Uninstall) kept(e core.OwnedEntry) bool {
	l := u.cfg.Layout
	return (u.cfg.KeepData || e.Module == core.ModuleIDCore) && (within(e.Path, l.DataDir) || within(e.Path, l.ConfigDir))
}

// selectEntries returns the entries for which keep is true.
func (u *Uninstall) selectEntries(keep func(e core.OwnedEntry) bool) []core.OwnedEntry {
	var out []core.OwnedEntry
	for _, e := range u.entries {
		if keep(e) {
			out = append(out, e)
		}
	}
	return out
}

func describe(entries []core.OwnedEntry) []string {
	var out []string
	for _, e := range entries {
		line := e.Kind + " " + e.Path
		if e.BlockID != "" {
			line += " (block " + e.BlockID + ")"
		}
		out = append(out, line+", of module "+e.Module)
	}
	return out
}

func (u *Uninstall) plan() []uninstallStep {
	l := u.cfg.Layout
	isCore := func(e core.OwnedEntry) bool { return e.Module == core.ModuleIDCore }
	blocks := u.selectEntries(func(e core.OwnedEntry) bool { return e.Kind == core.OwnedBlock && !u.kept(e) })
	files := u.selectEntries(func(e core.OwnedEntry) bool {
		return e.Kind == core.OwnedFile && (!isCore(e) || e.Path != l.Paths.BinaryLink) && !u.kept(e)
	})
	dirs := u.selectEntries(func(e core.OwnedEntry) bool {
		return e.Kind == core.OwnedDir && !isCore(e) && !within(e.Path, l.UnitDir) && !u.kept(e)
	})
	units := u.selectEntries(func(e core.OwnedEntry) bool { return e.Kind == core.OwnedUnit })
	unitDirs := u.selectEntries(func(e core.OwnedEntry) bool { return e.Kind == core.OwnedDir && within(e.Path, l.UnitDir) })
	users := u.selectEntries(func(e core.OwnedEntry) bool { return e.Kind == core.OwnedUser })
	services := []string{update.ControllerUnit, update.AgentUnit, RecoverUnit}

	steps := []uninstallStep{
		{name: "services", plan: []string{"stop and disable " + strings.Join(services, ", ")},
			run: func(ctx context.Context) (string, error) { return u.stopServices(ctx, services) }},
		{name: "agent lock", plan: []string{"make sure no agent runs (" + filepath.Join(l.Paths.AgentDir, AgentLockName) + ")"},
			run: u.takeAgentLock},
		{name: "modules", plan: []string{"run each module's uninstall step"}, run: u.runModules},
		{name: "marked blocks", plan: describe(blocks), run: func(ctx context.Context) (string, error) { return u.removeBlocks(ctx, blocks) }},
		{name: "files", plan: describe(append(slices.Clone(files), dirs...)),
			run: func(ctx context.Context) (string, error) { return u.removeFiles(ctx, files, dirs) }},
		{name: "units", plan: describe(append(slices.Clone(units), unitDirs...)),
			run: func(ctx context.Context) (string, error) { return u.removeUnits(ctx, units, unitDirs) }},
		{name: "binary", plan: []string{"remove " + l.Paths.BinaryDir + " and " + l.Paths.BinaryLink}, run: u.removeBinary},
	}
	if u.cfg.KeepData {
		steps = append(steps, uninstallStep{name: "data",
			plan: []string{"keep " + l.DataDir + ", " + l.ConfigDir + " and every user"},
			run: func(context.Context) (string, error) {
				return "kept " + l.DataDir + ", " + l.ConfigDir + " and every user", u.closeDB()
			}})
	} else {
		steps = append(steps, uninstallStep{name: "data",
			plan: append([]string{"remove " + l.ConfigDir + " and " + l.DataDir + ", agent.db last"}, describe(users)...),
			run:  func(ctx context.Context) (string, error) { return u.removeData(ctx, users) }})
	}
	return steps
}

// Describe writes the plan, one line per step and one per entry it removes.
func (u *Uninstall) Describe(w io.Writer) {
	_, _ = fmt.Fprintln(w, "darkpanel uninstall will, in this order:")
	for i, s := range u.steps {
		_, _ = fmt.Fprintf(w, "%d. %s\n", i+1, s.name)
		for _, line := range s.plan {
			_, _ = fmt.Fprintf(w, "     %s\n", line)
		}
	}
}

// Run runs the steps in order, printing one line per finished step. A failed
// step stops the run, and the error names the steps that did not run;
// running a new plan later picks up where this one stopped. Run closes the
// plan.
func (u *Uninstall) Run(ctx context.Context, w io.Writer) error {
	defer func() { _ = u.Close() }()
	for i, s := range u.steps {
		line, err := s.run(ctx)
		if err != nil {
			var rest []string
			for _, r := range u.steps[i+1:] {
				rest = append(rest, r.name)
			}
			if len(rest) == 0 {
				return fmt.Errorf("uninstall: %s: %w", s.name, err)
			}
			return fmt.Errorf("uninstall: %s: %w; not run: %s", s.name, err, strings.Join(rest, ", "))
		}
		_, _ = fmt.Fprintf(w, "%s: %s\n", s.name, line)
	}
	return nil
}

// Close releases agent.db and the agent lock; it is safe to call twice.
func (u *Uninstall) Close() error {
	err := u.closeDB()
	if u.lockFD >= 0 {
		if cerr := unix.Close(u.lockFD); err == nil {
			err = cerr
		}
		u.lockFD = -1
	}
	return err
}

func (u *Uninstall) closeDB() error {
	if u.db == nil {
		return nil
	}
	err := u.db.Close()
	u.db, u.own = nil, nil
	return err
}

// forget drops e from the manifest, which --keep-data keeps.
func (u *Uninstall) forget(ctx context.Context, e core.OwnedEntry) error {
	if u.own == nil {
		return nil
	}
	return u.own.Forget(ctx, e.Module, e.Path, e.BlockID)
}

// stopServices stops the services that exist, waits until they are down,
// and disables them with the boot recovery unit.
func (u *Uninstall) stopServices(ctx context.Context, units []string) (string, error) {
	var present []string
	for _, unit := range units {
		st, err := u.cfg.Systemd.State(ctx, unit)
		if err != nil {
			return "", err
		}
		if st.LoadState == "not-found" {
			continue
		}
		present = append(present, unit)
		if unit == RecoverUnit {
			continue
		}
		if err := u.cfg.Systemd.Stop(ctx, unit); err != nil {
			return "", err
		}
		if err := u.waitStopped(ctx, unit); err != nil {
			return "", err
		}
	}
	if len(present) == 0 {
		return "no Darkpanel service is installed", nil
	}
	if err := u.cfg.Systemd.Disable(ctx, present...); err != nil {
		return "", err
	}
	return "stopped and disabled " + strings.Join(present, ", "), nil
}

func (u *Uninstall) waitStopped(ctx context.Context, unit string) error {
	wait, cancel := context.WithTimeout(ctx, u.cfg.StopTimeout)
	defer cancel()
	for {
		st, err := u.cfg.Systemd.State(wait, unit)
		if err != nil {
			return err
		}
		if st.ActiveState == "inactive" || st.ActiveState == "failed" {
			return nil
		}
		select {
		case <-wait.Done():
			return fmt.Errorf("%s is still %s after %s", unit, st.ActiveState, u.cfg.StopTimeout)
		case <-time.After(u.cfg.PollInterval):
		}
	}
}

// takeAgentLock holds the lock a running agent holds, so nothing below
// races an agent that was started by hand.
func (u *Uninstall) takeAgentLock(context.Context) (string, error) {
	dir := u.cfg.Layout.Paths.AgentDir
	if _, err := os.Lstat(dir); errors.Is(err, fs.ErrNotExist) {
		return "no agent directory", nil
	}
	path := filepath.Join(dir, AgentLockName)
	fd, err := unix.Open(path, unix.O_RDWR|unix.O_CREAT|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return "", fmt.Errorf("open %s: %w", path, err)
	}
	if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = unix.Close(fd)
		if errors.Is(err, unix.EWOULDBLOCK) {
			return "", fmt.Errorf("an agent still runs and holds %s; stop it first", path)
		}
		return "", fmt.Errorf("lock %s: %w", path, err)
	}
	u.lockFD = fd
	return "no agent runs", nil
}

// runModules runs each module's uninstall step before its blocks and files
// go, so a module can still read them.
func (u *Uninstall) runModules(ctx context.Context) (string, error) {
	if u.db == nil {
		return "no agent database; no module installed anything", nil
	}
	if u.sealer == nil {
		return "no agent key; an earlier run already ran the module steps", nil
	}
	env := core.Env{DB: u.db, Exec: u.cfg.Exec, Files: u.files, Sealer: u.sealer, Clock: u.cfg.Clock, Log: u.cfg.Log}
	var ran []string
	for _, m := range u.cfg.Modules {
		un, ok := m.(core.Uninstaller)
		if !ok {
			continue
		}
		id := m.Manifest().ID
		if err := un.Uninstall(ctx, env, u.cfg.KeepData); err != nil {
			return "", fmt.Errorf("module %s: %w", id, err)
		}
		ran = append(ran, id)
	}
	if len(ran) == 0 {
		return "no module has an uninstall step", nil
	}
	return "ran the uninstall steps of " + strings.Join(ran, ", "), nil
}

func (u *Uninstall) removeBlocks(ctx context.Context, blocks []core.OwnedEntry) (string, error) {
	for _, e := range blocks {
		if err := u.files.RemoveBlock(ctx, e.Module, e.Path, e.BlockID); err != nil {
			return "", err
		}
	}
	return fmt.Sprintf("removed %d marked blocks", len(blocks)), nil
}

func (u *Uninstall) removeFiles(ctx context.Context, files, dirs []core.OwnedEntry) (string, error) {
	for _, e := range files {
		var err error
		if u.files != nil {
			err = u.files.Remove(ctx, e.Module, e.Path)
		} else {
			err = removeTree(e.Path)
		}
		if err != nil {
			return "", err
		}
	}
	for _, e := range dirs {
		if err := removeTree(e.Path); err != nil {
			return "", err
		}
		if err := u.forget(ctx, e); err != nil {
			return "", err
		}
	}
	return fmt.Sprintf("removed %d files and %d directories", len(files), len(dirs)), nil
}

// removeUnits removes the unit files, then their drop-in directories when
// nothing else is left in them, and reloads systemd.
func (u *Uninstall) removeUnits(ctx context.Context, units, dirs []core.OwnedEntry) (string, error) {
	for _, e := range units {
		if err := os.Remove(e.Path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
		if err := u.forget(ctx, e); err != nil {
			return "", err
		}
	}
	var left []string
	for _, e := range dirs {
		err := os.Remove(e.Path)
		switch {
		case errors.Is(err, syscall.ENOTEMPTY) || errors.Is(err, syscall.EEXIST):
			left = append(left, e.Path)
		case err != nil && !errors.Is(err, fs.ErrNotExist):
			return "", err
		}
		if err := u.forget(ctx, e); err != nil {
			return "", err
		}
	}
	if err := u.cfg.Systemd.Reload(ctx); err != nil {
		return "", err
	}
	line := fmt.Sprintf("removed %d unit files and reloaded systemd", len(units))
	if len(left) > 0 {
		line += "; kept " + strings.Join(left, ", ") + ", which holds files Darkpanel did not write"
	}
	return line, nil
}

func (u *Uninstall) removeBinary(ctx context.Context) (string, error) {
	p := u.cfg.Layout.Paths
	for _, path := range []string{p.BinaryLink, p.BinaryDir} {
		if err := removeTree(path); err != nil {
			return "", err
		}
	}
	for _, e := range u.selectEntries(func(e core.OwnedEntry) bool {
		return e.Module == core.ModuleIDCore && (e.Path == p.BinaryLink || e.Path == p.BinaryDir)
	}) {
		if err := u.forget(ctx, e); err != nil {
			return "", err
		}
	}
	return "removed " + p.BinaryDir + " and " + p.BinaryLink, nil
}

// removeData removes the users, the data directory except agent.db, then the
// configuration directory with the agent key, and agent.db last, so a run
// that stops early can be planned again and still has the key unless the
// module steps already ran.
func (u *Uninstall) removeData(ctx context.Context, users []core.OwnedEntry) (string, error) {
	l, p := u.cfg.Layout, u.cfg.Layout.Paths
	var removed []string
	for _, e := range users {
		_, _, found, err := u.cfg.Users.Lookup(e.Path)
		if err != nil {
			return "", err
		}
		if !found {
			continue
		}
		res, err := u.cfg.Exec.Run(ctx, core.Cmd{Path: "/usr/sbin/userdel", Args: []string{e.Path}})
		if err != nil {
			return "", err
		}
		if res.ExitCode != 0 {
			return "", fmt.Errorf("userdel %s exited %d: %s", e.Path, res.ExitCode, strings.TrimSpace(string(res.Stderr)))
		}
		removed = append(removed, e.Path)
	}
	keep := map[string]bool{p.AgentDir: true, filepath.Join(p.AgentDir, AgentLockName): true}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		keep[p.AgentDB()+suffix] = true
	}
	if err := clearExcept(l.DataDir, keep); err != nil {
		return "", err
	}
	if err := removeTree(l.ConfigDir); err != nil {
		return "", err
	}
	// The agent lock stays held through the removal (Linux unlinks a locked
	// file); Run's deferred Close releases it, so no agent starts against a
	// half-removed data directory.
	if err := u.closeDB(); err != nil {
		return "", err
	}
	if err := removeTree(l.DataDir); err != nil {
		return "", err
	}
	line := "removed " + l.ConfigDir + " and " + l.DataDir
	if len(removed) > 0 {
		line += " and the users " + strings.Join(removed, ", ")
	}
	return line, nil
}

// clearExcept removes everything under dir except the paths in keep, going
// into a kept directory to clear the rest of it.
func clearExcept(dir string, keep map[string]bool) error {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, e := range entries {
		path := filepath.Join(dir, e.Name())
		switch {
		case keep[path] && e.IsDir():
			if err := clearExcept(path, keep); err != nil {
				return err
			}
		case keep[path]:
		default:
			if err := removeTree(path); err != nil {
				return err
			}
		}
	}
	return nil
}

// removeTree removes path and everything under it; a symlink is removed
// itself, never followed, and a missing path is not an error.
func removeTree(path string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || path == "/" {
		return fmt.Errorf("refusing to remove %q", path)
	}
	st, err := os.Lstat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil
	case err != nil:
		return err
	case st.IsDir():
		return os.RemoveAll(path)
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}
