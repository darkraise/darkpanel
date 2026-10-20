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
	"os"
	"os/signal"
	"runtime"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/darkraise/darkpanel/internal/buildinfo"
	"github.com/darkraise/darkpanel/internal/cli"
	"github.com/darkraise/darkpanel/internal/controller/listen"
	"github.com/darkraise/darkpanel/internal/controller/server"
	"github.com/darkraise/darkpanel/internal/paths"
	"github.com/darkraise/darkpanel/internal/registry"
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
// registry.Build() plus the hand-written serve, version and selftest.
func newRoot(opts cli.Options) (*cobra.Command, error) {
	reg, err := registry.Build()
	if err != nil {
		return nil, err
	}
	opts.Registry = reg
	opts.Commands = []*cobra.Command{newServeCmd(), newVersionCmd(), newSelftestCmd()}
	return cli.NewRoot(opts)
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
		dev         bool
		listenFlags []string
		specs       []listen.Spec
	)
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the controller and the local agent",
		Args:  cobra.NoArgs,
		// cli marks only RunE errors as run errors, so these refusals of the
		// command line exit 2 like cobra's own flag errors.
		PreRunE: func(*cobra.Command, []string) error {
			var err error
			if specs, err = listen.ParseSpecs(listenFlags); err != nil {
				return err
			}
			if !dev && geteuid() != 0 {
				return errors.New("serve runs as root in production mode; run it with sudo, or add --dev to use per-user development paths")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
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
			return serveRun(ctx, server.Options{Paths: p, Listen: specs, Dev: dev, Log: log})
		},
	}
	cmd.Flags().BoolVar(&dev, "dev", false, "use per-user development paths under $XDG_STATE_HOME/darkpanel-dev")
	cmd.Flags().StringArrayVar(&listenFlags, "listen", nil,
		"listener to open instead of the defaults (repeatable): loopback, tailscale, https://<ip>:<port> or http://<loopback ip>:<port>")
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
