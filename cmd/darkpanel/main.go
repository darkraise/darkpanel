// Command darkpanel is the Darkpanel controller, agent and CLI.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/netip"
	"os"
	"os/signal"
	"runtime"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/darkraise/darkpanel/internal/buildinfo"
	"github.com/darkraise/darkpanel/internal/cli"
	"github.com/darkraise/darkpanel/internal/controller/listen"
	"github.com/darkraise/darkpanel/internal/controller/server"
	"github.com/darkraise/darkpanel/internal/install"
	"github.com/darkraise/darkpanel/internal/paths"
	"github.com/darkraise/darkpanel/internal/registry"
	"github.com/darkraise/darkpanel/internal/update"
	"github.com/darkraise/darkpanel/internal/update/trustedkeys"
	"github.com/darkraise/darkpanel/web"
)

// serveRun runs the server for serve; tests replace it.
var serveRun = server.Run

// geteuid is the effective uid serve checks; tests replace it.
var geteuid = os.Geteuid

// trustedKeys lists the release keys version and selftest report; tests
// replace it.
var trustedKeys = trustedkeys.Keys

// The harness's faulty builds set these in init (harness_broken.go,
// harness_crash.go); they are zero in every other build.
var (
	healthStatus   string // serve's /healthz status; empty is ok
	neverConnected bool   // agent run's Hello never reports a live session
	crashAtStart   bool   // serve and agent run exit at once
)

// errCrashAtStart is how a harnesscrash build's services exit.
var errCrashAtStart = errors.New("harnesscrash: exiting at start")

type versionInfo struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Go      string `json:"go"`
	OS      string `json:"os"`
	Arch    string `json:"arch"`
	UI      bool   `json:"ui"`
	// TestBuild and TrustedKeys are what the release workflow checks before
	// it publishes a binary.
	TestBuild   bool     `json:"testBuild"`
	TrustedKeys []string `json:"trustedKeys"`
}

func currentVersion() versionInfo {
	ids := []string{}
	for _, k := range trustedKeys() {
		ids = append(ids, k.ID)
	}
	return versionInfo{
		Version: buildinfo.Version,
		Commit:  buildinfo.Commit,
		Go:      runtime.Version(),
		OS:      runtime.GOOS,
		Arch:    runtime.GOARCH,
		// Reading web.Dist here is what keeps the UI in the binary: the linker
		// drops an embed.FS that no reachable code reads.
		UI:          uiEmbedded(web.Dist),
		TestBuild:   trustedkeys.TestBuild(),
		TrustedKeys: ids,
	}
}

func uiEmbedded(dist fs.FS) bool {
	_, err := fs.Stat(dist, "dist/index.html")
	return err == nil
}

// newRoot returns the command tree: the commands generated from
// registry.Build() plus the hand-written serve, version, selftest, agent,
// update, system and uninstall; update adopts the generated update and
// rollout commands.
func newRoot(opts cli.Options) (*cobra.Command, error) {
	reg, err := registry.Build()
	if err != nil {
		return nil, err
	}
	opts.Registry = reg
	opts.Commands = []*cobra.Command{
		newServeCmd(), newVersionCmd(), newSelftestCmd(), newAgentCmd(), newUpdateCmd(), newSystemCmd(), newUninstallCmd(),
	}
	opts.Adopt = map[string][]string{"update": updateActions}
	stderr := opts.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}
	opts.LocalFallbacks = map[string]cli.LocalFallback{update.ActionRollback: rollbackFallback(stderr)}
	root, err := cli.NewRoot(opts)
	if err != nil {
		return nil, err
	}
	if err := addDirectRollback(root); err != nil {
		return nil, err
	}
	return root, nil
}

func newVersionCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print the version, commit, Go version and platform",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return writeVersion(cmd.OutOrStdout(), currentVersion(), asJSON)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the version as a JSON object")
	return cmd
}

func newServeCmd() *cobra.Command {
	var (
		dev            bool
		embeddedAgent  bool
		listenFlags    []string
		agentFlags     []string
		agentAdvertise string
		specs          []listen.Spec
		agentListen    []netip.AddrPort
	)
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the controller",
		Args:  cobra.NoArgs,
		// cli marks only RunE errors as run errors, so these refusals of the
		// command line exit 2 like cobra's own flag errors.
		PreRunE: func(*cobra.Command, []string) error {
			var err error
			if specs, err = listen.ParseSpecs(listenFlags); err != nil {
				return err
			}
			if agentListen, err = listen.ParseAgentListen(agentFlags); err != nil {
				return err
			}
			if agentAdvertise != "" {
				if err := install.CheckAgentAddress(agentAdvertise); err != nil {
					return err
				}
			}
			if embeddedAgent && !dev {
				return errors.New("--embedded-agent runs the M1 in-process agent and needs --dev")
			}
			if embeddedAgent && len(agentListen) > 0 {
				return errors.New("--agent-listen does not apply with --embedded-agent, which opens no agent listener")
			}
			// Run as root, serve would create controller files that the
			// darkpanel user's unit could not open afterwards.
			if !dev && geteuid() == 0 {
				return errors.New("serve runs as the darkpanel user in production; start it with systemctl start darkpanel-controller, or add --dev to use per-user development paths")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			if crashAtStart {
				return errCrashAtStart
			}
			p, err := paths.Resolve(dev)
			if err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			// Releasing the signals once shutdown starts lets a second
			// interrupt end a shutdown that hangs.
			context.AfterFunc(ctx, stop)
			log := slog.New(slog.NewTextHandler(cmd.ErrOrStderr(), nil))
			return serveRun(ctx, server.Options{
				Paths: p, Listen: specs, Dev: dev, Log: log,
				EmbeddedAgent: embeddedAgent, AgentListen: agentListen, AgentAdvertise: agentAdvertise,
				HealthStatus: healthStatus,
			})
		},
	}
	cmd.Flags().BoolVar(&dev, "dev", false, "use per-user development paths under $XDG_STATE_HOME/darkpanel-dev")
	cmd.Flags().StringArrayVar(&listenFlags, "listen", nil,
		"listener to open instead of the defaults (repeatable): loopback, tailscale, https://<ip>:<port> or http://<loopback ip>:<port>")
	cmd.Flags().StringArrayVar(&agentFlags, "agent-listen", nil,
		"extra address of this host for the agent listener, <ip>:<port> (repeatable); 127.0.0.1:7443 and the Tailscale addresses are always bound")
	cmd.Flags().StringVar(&agentAdvertise, "agent-advertise", "",
		"<host>:<port> that enrollment tokens tell agents to connect to when the request names none")
	cmd.Flags().BoolVar(&embeddedAgent, "embedded-agent", false,
		"run the M1 in-process agent over this user's agent database instead of the agent listener (needs --dev)")
	return cmd
}

func writeVersion(w io.Writer, v versionInfo, asJSON bool) error {
	if asJSON {
		return json.NewEncoder(w).Encode(v)
	}
	_, err := fmt.Fprintf(w, "darkpanel %s (commit %s, %s, %s/%s)\n", v.Version, v.Commit, v.Go, v.OS, v.Arch)
	return err
}

// run executes the command line args and returns the process exit code
// (spec section 15). Nil fields of opts are the process's own.
func run(args []string, opts cli.Options) int {
	stderr := opts.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}
	root, err := newRoot(opts)
	if err != nil {
		cli.PrintError(stderr, err)
		return cli.ExitError
	}
	root.SetArgs(args)
	if err := root.ExecuteContext(context.Background()); err != nil {
		cli.PrintError(stderr, err)
		return cli.ExitCode(err)
	}
	return cli.ExitOK
}

func main() {
	os.Exit(run(os.Args[1:], cli.Options{}))
}
