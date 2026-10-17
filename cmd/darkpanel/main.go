// Command darkpanel is the Darkpanel controller, agent and CLI.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"runtime"

	"github.com/spf13/cobra"

	"github.com/darkraise/darkpanel/web"
)

// Set by the build with -ldflags "-X main.version=... -X main.commit=...".
var (
	version = "dev"
	commit  = "unknown"
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
		Version: version,
		Commit:  commit,
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
	root.AddCommand(newVersionCmd())
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
