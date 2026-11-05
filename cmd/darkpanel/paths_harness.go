//go:build harness

package main

import (
	"errors"

	"github.com/spf13/cobra"

	"github.com/darkraise/darkpanel/internal/paths"
)

// harnessStateDir is the --state-dir of a harness build. It is a package
// variable, not a flag read from the command, because the update rollback
// fallback resolves its paths without a command.
var harnessStateDir string

func init() {
	addRootFlags = func(root *cobra.Command) {
		root.PersistentFlags().StringVar(&harnessStateDir, "state-dir", "", "harness only: keep every path under this directory")
	}
	selectPaths = func(_ *cobra.Command, dev bool) (paths.Paths, error) {
		switch {
		case harnessStateDir == "":
			return paths.Resolve(dev)
		case dev:
			return paths.Paths{}, errors.New("--state-dir and --dev cannot be combined")
		default:
			return paths.Under(harnessStateDir), nil
		}
	}
}
