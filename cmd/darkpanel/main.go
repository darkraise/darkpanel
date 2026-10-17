// Command darkpanel is the Darkpanel controller, agent and CLI.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"runtime"

	"github.com/spf13/cobra"

	"github.com/darkraise/darkpanel/internal/buildinfo"
	"github.com/darkraise/darkpanel/web"
)

type versionInfo struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Go      string `json:"go"`
	OS      string `json:"os"`
	Arch    string `json:"arch"`
	UI      bool   `json:"ui"`
}

func currentVersion() versionInfo {
	return versionInfo{
		Version: buildinfo.Version,
		Commit:  buildinfo.Commit,
		Go:      runtime.Version(),
		OS:      runtime.GOOS,
		Arch:    runtime.GOARCH,
		// Reading web.Dist here is what keeps the UI in the binary: the linker
		// drops an embed.FS that no reachable code reads.
		UI: uiEmbedded(web.Dist),
	}
}

func uiEmbedded(dist fs.FS) bool {
	_, err := fs.Stat(dist, "dist/index.html")
	return err == nil
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "darkpanel",
		Short:         "Single-binary fleet manager for unmanaged Linux servers",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(newVersionCmd(), newServeCmd())
	return root
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
		dev    bool
		listen []string
	)
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the controller and the local agent",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return errors.New("serve: the controller is not assembled yet")
		},
	}
	cmd.Flags().BoolVar(&dev, "dev", false, "use per-user development paths under $XDG_STATE_HOME/darkpanel-dev")
	cmd.Flags().StringArrayVar(&listen, "listen", nil,
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

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "darkpanel:", err)
		os.Exit(1)
	}
}
