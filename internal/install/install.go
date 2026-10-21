package install

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	agentruntime "github.com/darkraise/darkpanel/internal/agent/runtime"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/secret"
	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/platform"
	"github.com/darkraise/darkpanel/internal/update"
)

// The waits of system install.
const (
	// HealthTimeout bounds the wait for a started controller to answer
	// /healthz on its socket.
	HealthTimeout = 60 * time.Second
	// pollInterval is how often a wait asks again.
	pollInterval = time.Second
)

// Controller is the local controller, reached over its socket; the
// controller role needs it.
type Controller interface {
	// Healthy fails unless GET /healthz on the controller socket answers
	// status ok.
	Healthy(ctx context.Context) error
	// EnrollLocal runs core.host.enroll-local and returns its token.
	EnrollLocal(ctx context.Context) (string, error)
}

// CheckResult is the outcome of one handshake with the saved agent
// credentials, the exit status of darkpanel agent check.
type CheckResult int

// The outcomes of Agent.Check, and CheckFailed for an agent check that could
// not try a handshake.
const (
	CheckAccepted    CheckResult = iota // the controller accepted the credentials
	CheckRefused                        // the controller refused them
	CheckUnreachable                    // no handshake completed
	CheckFailed                         // the check did not run: a usage error, no root, unresolvable paths
)

// Agent is this host's agent credentials.
type Agent interface {
	// Check performs one mTLS handshake with the saved credentials.
	Check(ctx context.Context) (CheckResult, error)
	// Enroll consumes token and writes a new credential generation.
	Enroll(ctx context.Context, token string) error
}

// Users looks up system users.
type Users interface {
	// Lookup returns name's uid and gid; found is false when there is no
	// such user.
	Lookup(name string) (uid, gid int, found bool, err error)
}

// SystemUsers reads the system's user database.
type SystemUsers struct{}

// Lookup implements Users with os/user.
func (SystemUsers) Lookup(name string) (int, int, bool, error) {
	u, err := user.Lookup(name)
	var unknown user.UnknownUserError
	if errors.As(err, &unknown) {
		return 0, 0, false, nil
	}
	if err != nil {
		return 0, 0, false, fmt.Errorf("look up user %s: %w", name, err)
	}
	uid, err := strconv.Atoi(u.Uid)
	if err != nil {
		return 0, 0, false, fmt.Errorf("user %s has uid %q", name, u.Uid)
	}
	gid, err := strconv.Atoi(u.Gid)
	if err != nil {
		return 0, 0, false, fmt.Errorf("user %s has gid %q", name, u.Gid)
	}
	return uid, gid, true, nil
}

// Config is what system install needs.
type Config struct {
	Layout         Layout
	Role           Role
	AgentListen    []string // controller role: the --agent-listen values
	AgentAdvertise string   // controller role: the --agent-advertise value
	// Token is the agent role's enrollment token from standard input,
	// trimmed; empty when none was given.
	Token string
	// Modules are the compiled-in modules, whose agent migrations agent.db
	// gets as the agent would apply them.
	Modules       []core.Manifest
	BinaryVersion string
	Systemd       platform.Systemd
	Exec          core.Runner
	Users         Users
	Controller    Controller // controller role only
	Agent         Agent
	Clock         core.Clock
	Out           io.Writer
	// Chown gives path to uid and gid; nil means os.Chown.
	Chown func(path string, uid, gid int) error
	// PollInterval and HealthTimeout pace the wait for the controller; zero
	// means 1 s and HealthTimeout.
	PollInterval  time.Duration
	HealthTimeout time.Duration
}

func (c Config) check() error {
	required := []struct {
		name string
		set  bool
	}{
		{"Systemd", c.Systemd != nil}, {"Exec", c.Exec != nil}, {"Users", c.Users != nil}, {"Agent", c.Agent != nil},
		{"Clock", c.Clock != nil}, {"Out", c.Out != nil}, {"BinaryVersion", c.BinaryVersion != ""},
		{"Controller", c.Role != RoleController || c.Controller != nil},
	}
	for _, r := range required {
		if !r.set {
			return fmt.Errorf("install: Config.%s is required", r.name)
		}
	}
	if _, err := ParseRole(string(c.Role)); err != nil {
		return err
	}
	if c.Role == RoleController && c.Token != "" {
		return errors.New("install: an enrollment token is only for the agent role")
	}
	// Rendering the units checks the flags; doing it here refuses them before
	// anything on the host changes.
	_, err := Units(c.Layout, c.Role, c.AgentListen, c.AgentAdvertise)
	return err
}

// installer is one run of system install.
type installer struct {
	cfg      Config
	uid, gid int // the service user's, controller role only
}

// Install installs role on this host. Every step works out from the host
// what is already done, so rerunning Install after any failure, or after a
// completed install, finishes or repairs it and changes nothing else.
func Install(ctx context.Context, cfg Config) error {
	if err := cfg.check(); err != nil {
		return err
	}
	if cfg.Chown == nil {
		cfg.Chown = os.Chown
	}
	if cfg.PollInterval == 0 {
		cfg.PollInterval = pollInterval
	}
	if cfg.HealthTimeout == 0 {
		cfg.HealthTimeout = HealthTimeout
	}
	in := &installer{cfg: cfg}
	steps := []struct {
		name string
		run  func(context.Context) (string, error)
	}{
		{"directories", in.directories},
		{"keys", in.keys},
		{"database", in.database},
		{"units", in.units},
		{"controller", in.startController},
		{"enrollment", in.enroll},
		{"agent", in.startAgent},
	}
	for _, s := range steps {
		line, err := s.run(ctx)
		if err != nil {
			return fmt.Errorf("install: %s: %w", s.name, err)
		}
		if line != "" {
			_, _ = fmt.Fprintf(cfg.Out, "%s: %s\n", s.name, line)
		}
	}
	if cfg.Role == RoleController {
		_, _ = fmt.Fprintln(cfg.Out, "Create the first admin over the controller socket: sudo darkpanel user create --username admin")
	}
	return nil
}

// dirSpec is one directory system install creates.
type dirSpec struct {
	path    string
	mode    fs.FileMode
	service bool // owned by the service user instead of root
}

func (in *installer) dirSpecs() []dirSpec {
	l, p := in.cfg.Layout, in.cfg.Layout.Paths
	specs := []dirSpec{
		{path: l.DataDir, mode: 0o755}, {path: l.ConfigDir, mode: 0o755}, {path: p.AgentDir, mode: 0o700},
		{path: p.UpdateDir, mode: 0o700}, {path: p.AgentTLSDir, mode: 0o700}, {path: p.BinaryDir, mode: 0o755},
	}
	if in.cfg.Role == RoleController {
		specs = append(specs, dirSpec{path: p.ControllerDir, mode: 0o700, service: true})
	}
	return specs
}

// directories creates the service user for the controller role, then every
// directory with its owner and mode; an existing directory gets them too.
func (in *installer) directories(ctx context.Context) (string, error) {
	if in.cfg.Role == RoleController {
		if err := in.ensureUser(ctx); err != nil {
			return "", err
		}
	}
	for _, d := range in.dirSpecs() {
		if err := os.MkdirAll(d.path, d.mode); err != nil {
			return "", err
		}
		st, err := os.Lstat(d.path)
		if err != nil {
			return "", err
		}
		if !st.IsDir() {
			return "", fmt.Errorf("%s is not a directory", d.path)
		}
		// MkdirAll applies the umask and leaves an existing directory's mode
		// alone.
		if err := os.Chmod(d.path, d.mode); err != nil {
			return "", err
		}
		if err := in.chown(d.path, d.service); err != nil {
			return "", err
		}
	}
	if err := os.MkdirAll(filepath.Dir(in.cfg.Layout.Paths.BinaryLink), 0o755); err != nil { //nolint:gosec // G301: a directory on PATH
		return "", err
	}
	return "ready", nil
}

func (in *installer) chown(path string, service bool) error {
	uid, gid := 0, 0
	if service {
		uid, gid = in.uid, in.gid
	}
	if err := in.cfg.Chown(path, uid, gid); err != nil {
		return fmt.Errorf("chown %s: %w", path, err)
	}
	return nil
}

// ensureUser creates the service user unless it exists, and remembers its
// ids.
func (in *installer) ensureUser(ctx context.Context) error {
	uid, gid, found, err := in.cfg.Users.Lookup(ServiceUser)
	if err != nil {
		return err
	}
	if !found {
		res, err := in.cfg.Exec.Run(ctx, core.Cmd{
			Path: "/usr/sbin/useradd",
			Args: []string{"--system", "--no-create-home", "--shell", "/usr/sbin/nologin", ServiceUser},
		})
		if err != nil {
			return err
		}
		if res.ExitCode != 0 {
			return fmt.Errorf("useradd %s exited %d: %s", ServiceUser, res.ExitCode, strings.TrimSpace(string(res.Stderr)))
		}
		if uid, gid, found, err = in.cfg.Users.Lookup(ServiceUser); err != nil {
			return err
		}
		if !found {
			return fmt.Errorf("useradd succeeded but there is no user %s", ServiceUser)
		}
	}
	in.uid, in.gid = uid, gid
	return nil
}

// keys creates each role key that is absent. An existing key is never
// replaced, because the database it seals is readable only with it; it only
// gets its owner and mode 0600 again.
func (in *installer) keys(context.Context) (string, error) {
	p := in.cfg.Layout.Paths
	created := 0
	keys := []struct {
		path    string
		service bool
	}{{p.AgentKey, false}}
	if in.cfg.Role == RoleController {
		keys = append(keys, struct {
			path    string
			service bool
		}{p.ControllerKey, true})
	}
	for _, k := range keys {
		st, err := os.Lstat(k.path)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			if _, err := secret.LoadOrCreateKey(k.path); err != nil {
				return "", err
			}
			created++
		case err != nil:
			return "", err
		case !st.Mode().IsRegular():
			return "", fmt.Errorf("%s is not a regular file", k.path)
		default:
			// The key loader refuses any group or other access, so a loosened
			// mode would stop the daemon that reads this key.
			if err := os.Chmod(k.path, 0o600); err != nil {
				return "", err
			}
		}
		if err := in.chown(k.path, k.service); err != nil {
			return "", err
		}
	}
	return fmt.Sprintf("%d created, %d kept", created, len(keys)-created), nil
}

// database migrates agent.db as the agent would and records every path this
// role creates in the ownership manifest. Recording an entry again changes
// nothing.
func (in *installer) database(ctx context.Context) (string, error) {
	p := in.cfg.Layout.Paths
	db, err := store.Open(ctx, p.AgentDB())
	if err != nil {
		return "", err
	}
	defer func() { _ = db.Close() }()
	coreSet, err := store.CoreMigrations(store.SideAgent)
	if err != nil {
		return "", err
	}
	var sets []store.MigrationSet
	for _, m := range in.cfg.Modules {
		if fsys := m.Migrations[core.SideAgent]; fsys != nil && m.ID != core.ModuleIDCore {
			sets = append(sets, store.MigrationSet{Module: m.ID, FS: fsys})
		}
	}
	res, err := store.Migrate(ctx, db, store.MigrateOptions{
		Side: store.SideAgent, BinaryVersion: in.cfg.BinaryVersion, BackupRoot: p.AgentBackups(),
		Keep: agentruntime.BackupsKept, Now: in.cfg.Clock.Now(),
	}, coreSet, sets)
	if err != nil {
		return "", err
	}
	if res.PruneErr != nil {
		_, _ = fmt.Fprintf(in.cfg.Out, "warning: old agent database backups could not be pruned: %v\n", res.PruneErr)
	}
	for _, f := range res.Failed {
		_, _ = fmt.Fprintf(in.cfg.Out, "warning: the agent migrations of module %s failed; the agent reports it unavailable\n", f.Module)
	}
	own := platform.NewOwnership(db)
	now := in.cfg.Clock.Now().UTC()
	owned := OwnedPaths(in.cfg.Layout, in.cfg.Role)
	for _, op := range owned {
		e := core.OwnedEntry{Path: op.Path, Module: core.ModuleIDCore, Kind: op.Kind, CreatedAt: now}
		if err := own.Record(ctx, e); err != nil {
			return "", err
		}
	}
	return fmt.Sprintf("agent.db migrated, %d owned paths recorded", len(owned)), nil
}

// units writes the unit files that differ, reloads systemd and enables the
// role's units.
func (in *installer) units(ctx context.Context) (string, error) {
	files, err := Units(in.cfg.Layout, in.cfg.Role, in.cfg.AgentListen, in.cfg.AgentAdvertise)
	if err != nil {
		return "", err
	}
	written := 0
	for _, f := range files {
		wrote, err := writeIfChanged(f.Path, f.Content)
		if err != nil {
			return "", err
		}
		if wrote {
			written++
		}
	}
	if err := in.cfg.Systemd.Reload(ctx); err != nil {
		return "", err
	}
	if err := in.cfg.Systemd.Enable(ctx, EnabledUnits(in.cfg.Role)...); err != nil {
		return "", err
	}
	return fmt.Sprintf("%d written, %d unchanged, enabled %s", written, len(files)-written,
		strings.Join(EnabledUnits(in.cfg.Role), ", ")), nil
}

// restart starts unit with the installed binary, clearing a failed state
// first: a unit that hit its start limit refuses a plain start.
func (in *installer) restart(ctx context.Context, unit string) error {
	st, err := in.cfg.Systemd.State(ctx, unit)
	if err != nil {
		return err
	}
	if st.ActiveState == "failed" {
		if err := in.cfg.Systemd.ResetFailed(ctx, unit); err != nil {
			return err
		}
	}
	return in.cfg.Systemd.Restart(ctx, unit)
}

// startController starts the controller and waits until it answers on its
// socket. The agent role has no controller.
func (in *installer) startController(ctx context.Context) (string, error) {
	if in.cfg.Role != RoleController {
		return "", nil
	}
	if err := in.restart(ctx, update.ControllerUnit); err != nil {
		return "", err
	}
	wait, cancel := context.WithTimeout(ctx, in.cfg.HealthTimeout)
	defer cancel()
	for {
		err := in.cfg.Controller.Healthy(wait)
		if err == nil {
			return "started and healthy", nil
		}
		select {
		case <-wait.Done():
			return "", fmt.Errorf("the controller did not answer /healthz on its socket within %s (see journalctl -u %s): %w",
				in.cfg.HealthTimeout, update.ControllerUnit, err)
		case <-time.After(in.cfg.PollInterval):
		}
	}
}

// enroll keeps saved credentials the controller accepts, and otherwise
// enrolls: the controller role with a token it issues to itself, the agent
// role with the token from standard input.
func (in *installer) enroll(ctx context.Context) (string, error) {
	_, err := os.Lstat(in.cfg.Layout.Paths.AgentTLSCurrent())
	saved := err == nil
	if saved {
		res, err := in.cfg.Agent.Check(ctx)
		if err != nil {
			return "", err
		}
		switch {
		case res == CheckAccepted && in.cfg.Token != "":
			return "the saved credentials are valid; the enrollment token was not used", nil
		case res == CheckAccepted:
			return "the saved credentials are valid", nil
		case res == CheckUnreachable && in.cfg.Role == RoleAgent:
			return "warning: the controller is unreachable; the saved credentials are kept", nil
		}
	}
	token := in.cfg.Token
	switch {
	case in.cfg.Role == RoleController:
		if token, err = in.cfg.Controller.EnrollLocal(ctx); err != nil {
			return "", err
		}
	case token == "" && saved:
		return "", errors.New("the controller refused this agent's saved credentials; create a new token for this host and rerun install.sh with --token-file")
	case token == "":
		return "", errors.New("this agent has no saved credentials and no enrollment token was given; rerun install.sh with --token-file, or on a terminal to be prompted")
	}
	if err := in.cfg.Agent.Enroll(ctx, token); err != nil {
		var e *errcode.Error
		if errors.As(err, &e) && e.Code == errcode.EnrollTokenInvalid {
			return "", fmt.Errorf("the controller refused the enrollment token: it is expired or mistyped, or this host's token was used but no credentials were saved; revoke the host and create a new token: %w", err)
		}
		return "", err
	}
	return "enrolled", nil
}

// startAgent starts the agent with the installed binary.
func (in *installer) startAgent(ctx context.Context) (string, error) {
	if err := in.restart(ctx, update.AgentUnit); err != nil {
		return "", err
	}
	return "started " + update.AgentUnit, nil
}
