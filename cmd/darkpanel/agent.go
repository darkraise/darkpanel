package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/darkraise/darkpanel/internal/agent/api"
	"github.com/darkraise/darkpanel/internal/agent/daemon"
	"github.com/darkraise/darkpanel/internal/agent/runtime"
	"github.com/darkraise/darkpanel/internal/buildinfo"
	"github.com/darkraise/darkpanel/internal/cli"
	"github.com/darkraise/darkpanel/internal/controller/coreactions"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/secret"
	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/install"
	"github.com/darkraise/darkpanel/internal/paths"
	"github.com/darkraise/darkpanel/internal/platform"
	"github.com/darkraise/darkpanel/internal/registry"
	"github.com/darkraise/darkpanel/internal/update"
	"github.com/darkraise/darkpanel/internal/update/trustedkeys"
)

// The agent commands' seams; tests replace them.
var (
	daemonRun    = daemon.Run
	daemonEnroll = daemon.Enroll
	daemonCheck  = daemon.Check
	newSystemd   = func(ctx context.Context) (systemdConn, error) { return platform.NewSystemd(ctx) }
	newUpdater   = update.New
	resolvePaths = paths.Resolve
	// installLayout is the installed layout, whose controller unit file
	// tells an agent that the controller runs on its host.
	installLayout = install.Production
	// stdinTerminal reports whether r is an interactive terminal.
	stdinTerminal = func(r io.Reader) bool {
		f, ok := r.(*os.File)
		return ok && term.IsTerminal(int(f.Fd())) //nolint:gosec // G115: a file descriptor fits in an int
	}
	// readHidden reads one line from the terminal r without echo.
	readHidden = func(r io.Reader) (string, error) {
		f, ok := r.(*os.File)
		if !ok {
			return "", errors.New("standard input is not a terminal")
		}
		b, err := term.ReadPassword(int(f.Fd())) //nolint:gosec // G115: a file descriptor fits in an int
		return string(b), err
	}
)

// systemdConn is the systemd provider and its connection's Close.
type systemdConn interface {
	platform.Systemd
	Close() error
}

// errDevSystemd is every unit operation of a development agent's updater.
var errDevSystemd = errors.New("agent run --dev does not manage systemd units")

// devSystemd is the systemd provider of a development agent: the host's units
// belong to the installed services, so a development agent's update actions
// fail and its start recovery only logs.
type devSystemd struct{}

func (devSystemd) StartTransientService(context.Context, string, platform.TransientService) error {
	return errDevSystemd
}

func (devSystemd) StartTransientTimer(context.Context, string, platform.TransientTimer) error {
	return errDevSystemd
}

func (devSystemd) Start(context.Context, string) error                { return errDevSystemd }
func (devSystemd) Stop(context.Context, string) error                 { return errDevSystemd }
func (devSystemd) Restart(context.Context, string) error              { return errDevSystemd }
func (devSystemd) Kill(context.Context, string, syscall.Signal) error { return errDevSystemd }
func (devSystemd) ResetFailed(context.Context, string) error          { return errDevSystemd }
func (devSystemd) Enable(context.Context, ...string) error            { return errDevSystemd }
func (devSystemd) Disable(context.Context, ...string) error           { return errDevSystemd }
func (devSystemd) Reload(context.Context) error                       { return errDevSystemd }
func (devSystemd) Close() error                                       { return nil }
func (devSystemd) State(context.Context, string) (platform.UnitState, error) {
	return platform.UnitState{}, errDevSystemd
}

// maxStdinToken is more than any token, as daemon.ReadTokenFile allows.
const maxStdinToken = 4096

// agentHost is what every agent-side command opens: the agent database,
// the agent key's sealer, the updater and the registry whose update actions
// run on that updater.
type agentHost struct {
	DB       *store.DB
	Sealer   *secret.Sealer
	Updater  *update.Updater
	Registry *core.Registry
	Close    func() error
}

// openAgent opens the agent's state at p without the agent lock, which only
// darkpanel agent run takes: the update runner and recovery open agent.db
// beside the running daemon. It creates the agent's missing directories, as
// system install does, so a development layout works from an empty state.
func openAgent(ctx context.Context, p paths.Paths, log *slog.Logger) (*agentHost, error) {
	for _, d := range []string{p.AgentDir, filepath.Dir(p.AgentKey)} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return nil, fmt.Errorf("create %s: %w", d, err)
		}
	}
	if err := p.MkdirAgent(); err != nil {
		return nil, err
	}
	key, err := secret.LoadOrCreateKey(p.AgentKey)
	if err != nil {
		return nil, err
	}
	sealer, err := secret.NewSealer(key)
	if err != nil {
		return nil, err
	}
	db, err := store.Open(ctx, p.AgentDB())
	if err != nil {
		return nil, err
	}
	// Only the installed layout's agent manages the host's units.
	l := installLayout()
	installed := p == l.Paths
	var sd systemdConn = devSystemd{}
	if installed {
		if sd, err = newSystemd(ctx); err != nil {
			_ = db.Close()
			return nil, err
		}
	}
	closeAll := func() error { return errors.Join(sd.Close(), db.Close()) }
	agentSocket := api.NewClient(api.ClientConfig{Dial: func(ctx context.Context) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", p.AgentSocket)
	}})
	probes := update.Probes{Agent: agentSocket.Hello}
	// Only the installed layout has a controller unit; a development agent
	// never waits for a controller after an update.
	if installed {
		if _, err := os.Stat(l.UnitPath(update.ControllerUnit)); err == nil {
			probes.Controller = update.ControllerProbe(p.ControllerSocket)
		}
	}
	cfg := update.Config{
		Paths: p, DB: db, Systemd: sd, Clock: core.SystemClock{}, Log: log,
		Version: buildinfo.Version, Arch: goruntime.GOARCH,
		BaseURL: trustedkeys.BaseURL(), Keys: trustedKeys(),
		Index:  update.NewIndexSource(trustedkeys.BaseURL(), &http.Client{Timeout: 30 * time.Second}, core.SystemClock{}),
		Probes: probes,
	}
	// A rollback on the controller's host must not leave enrolled agents
	// newer than the controller it returns to.
	if _, err := os.Stat(p.ControllerDB()); err == nil {
		cfg.NewerAgents = newerAgents(p.ControllerDB())
	}
	upd, err := newUpdater(cfg)
	if err != nil {
		_ = closeAll()
		return nil, err
	}
	reg, err := registry.BuildWith(coreactions.Module(coreactions.Deps{
		Extra: registry.CoreActions(upd, ""), OwnedPaths: install.CoreOwnedPaths(),
	}))
	if err != nil {
		_ = closeAll()
		return nil, err
	}
	return &agentHost{DB: db, Sealer: sealer, Updater: upd, Registry: reg, Close: closeAll}, nil
}

func newAgentCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agent",
		Short: "Run, enroll and check this host's agent",
		RunE:  runGroup,
	}
	cmd.AddCommand(newAgentRunCmd(), newAgentEnrollCmd(), newAgentCheckCmd())
	return cmd
}

// runGroup is the RunE of a hand-written command group: cobra checks for an
// unknown subcommand only below the root, so a group refuses one itself and
// exits 2 as the generated groups do.
func runGroup(cmd *cobra.Command, args []string) error {
	if len(args) > 0 {
		return cli.ExitWith(cli.ExitUsage, fmt.Errorf("unknown command %q for %q", args[0], cmd.CommandPath()))
	}
	return cmd.Help()
}

// requireRoot is the PreRunE of the agent commands that change the agent's
// root-owned state; cli exits 2 for it, like a flag error.
func requireRoot(name string, dev *bool) func(*cobra.Command, []string) error {
	return func(*cobra.Command, []string) error {
		if !*dev && geteuid() != 0 {
			return fmt.Errorf("agent %s runs as root in production mode; run it with sudo, or add --dev", name)
		}
		return nil
	}
}

func addDevFlag(cmd *cobra.Command, dev *bool) {
	cmd.Flags().BoolVar(dev, "dev", false, "use per-user development paths under $XDG_STATE_HOME/darkpanel-dev")
}

func newAgentRunCmd() *cobra.Command {
	var dev bool
	cmd := &cobra.Command{
		Use:     "run",
		Short:   "Run the agent: serve the agent socket and keep a session to the controller",
		Args:    cobra.NoArgs,
		PreRunE: requireRoot("run", &dev),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if crashAtStart {
				return errCrashAtStart
			}
			p, err := resolvePaths(dev)
			if err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			// Releasing the signals once shutdown starts lets a second
			// interrupt end a shutdown that hangs.
			context.AfterFunc(ctx, stop)
			log := slog.New(slog.NewTextHandler(cmd.ErrOrStderr(), nil))
			host, err := openAgent(ctx, p, log)
			if err != nil {
				return err
			}
			cfg := daemon.Config{
				Paths: p,
				Runtime: runtime.Config{
					DB: host.DB, Sealer: host.Sealer, Clock: core.SystemClock{}, Log: log, Registry: host.Registry,
					BinaryVersion: buildinfo.Version, BackupDir: p.AgentBackups(),
					Exec: platform.NewRunner(platform.ProductionAllowlist()),
				},
				Updater:        host.Updater,
				NeverConnected: neverConnected,
			}
			if dev {
				// The development agent runs as the invoking user, whose own
				// CLI must reach the socket.
				uid := uint32(os.Getuid()) //nolint:gosec // G115: a uid fits in 32 bits
				cfg.AllowPeer = func(peer uint32) bool { return peer == 0 || peer == uid }
			}
			return errors.Join(daemonRun(ctx, cfg), host.Close())
		},
	}
	addDevFlag(cmd, &dev)
	return cmd
}

func newAgentEnrollCmd() *cobra.Command {
	var (
		dev       bool
		tokenFile string
	)
	cmd := &cobra.Command{
		Use:   "enroll",
		Short: "Enroll this host's agent with an enrollment token",
		Long: "Enroll this host's agent with an enrollment token from --token-file, a hidden prompt on the " +
			"terminal, or standard input. A running agent uses the new credentials from its next connection " +
			"attempt; it needs no restart.",
		Args:    cobra.NoArgs,
		PreRunE: requireRoot("enroll", &dev),
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, err := resolvePaths(dev)
			if err != nil {
				return err
			}
			token, err := readToken(cmd, tokenFile, dev)
			if err != nil {
				return err
			}
			creds, err := daemonEnroll(cmd.Context(), p.AgentTLSDir, token)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "enrolled as host %s\n"+
				"A running agent uses the new credentials from its next connection attempt; it needs no restart.\n",
				creds.HostID())
			return err
		},
	}
	addDevFlag(cmd, &dev)
	cmd.Flags().StringVar(&tokenFile, "token-file", "", "read the token from this file, owned by root with mode 0600 or stricter")
	return cmd
}

// readToken reads the enrollment token from tokenFile, else from a hidden
// prompt when standard input is a terminal, else from standard input.
func readToken(cmd *cobra.Command, tokenFile string, dev bool) (string, error) {
	if tokenFile != "" {
		uid := 0
		if dev {
			uid = os.Getuid()
		}
		return daemon.ReadTokenFile(tokenFile, uid)
	}
	in := cmd.InOrStdin()
	if stdinTerminal(in) {
		_, _ = fmt.Fprint(cmd.ErrOrStderr(), "Enrollment token: ")
		token, err := readHidden(in)
		_, _ = fmt.Fprintln(cmd.ErrOrStderr())
		if err != nil {
			return "", err
		}
		if token = strings.TrimSpace(token); token == "" {
			return "", errors.New("no enrollment token was entered")
		}
		return token, nil
	}
	data, err := io.ReadAll(io.LimitReader(in, maxStdinToken+1))
	if err != nil {
		return "", fmt.Errorf("read the token from standard input: %w", err)
	}
	token := strings.TrimSpace(string(data))
	switch {
	case len(data) > maxStdinToken:
		return "", errors.New("standard input is larger than an enrollment token")
	case token == "":
		return "", errors.New("no enrollment token on standard input; pass --token-file or run on a terminal")
	}
	return token, nil
}

func newAgentCheckCmd() *cobra.Command {
	var dev bool
	cmd := &cobra.Command{
		Use:   "check",
		Short: "Check that the controller accepts this agent's credentials",
		Long: "Check that the controller accepts this agent's credentials with one handshake. The controller " +
			"treats the check as this host's session, so it replaces a running agent's session for a moment " +
			"and the agent reconnects.\n\n" +
			"Exit status: 0 accepted, 1 refused (enroll again with a new token), 2 unreachable, " +
			"3 the check could not run.",
		Args: func(cmd *cobra.Command, args []string) error {
			return checkFailed(cobra.NoArgs(cmd, args))
		},
		PreRunE: func(cmd *cobra.Command, args []string) error {
			return checkFailed(requireRoot("check", &dev)(cmd, args))
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, err := resolvePaths(dev)
			if err != nil {
				return checkFailed(err)
			}
			res, err := daemonCheck(cmd.Context(), p.AgentTLSDir)
			if _, werr := fmt.Fprintln(cmd.OutOrStdout(), checkWords(res)); werr != nil && err == nil {
				err = werr
			}
			return cli.ExitWith(int(res), err)
		},
	}
	cmd.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return checkFailed(err) })
	addDevFlag(cmd, &dev)
	return cmd
}

// checkFailed gives a failure of agent check that is not a handshake outcome
// its own exit status, so a script never reads it as refused or unreachable.
func checkFailed(err error) error {
	if err == nil {
		return nil
	}
	return cli.ExitWith(int(install.CheckFailed), err)
}

func checkWords(res install.CheckResult) string {
	switch res {
	case install.CheckAccepted:
		return "accepted: the controller accepted this agent's credentials"
	case install.CheckRefused:
		return "refused: the controller refused this agent's credentials, or there are none; enroll again with a new token"
	default:
		return "unreachable: no handshake with the controller completed"
	}
}
