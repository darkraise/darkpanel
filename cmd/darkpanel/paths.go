package main

import (
	"github.com/spf13/cobra"

	"github.com/darkraise/darkpanel/internal/paths"
)

// selectPaths resolves the process's paths; a harness build replaces it.
// cmd is the running command, or nil where none is at hand (the update
// rollback fallback), so a replacement must accept nil.
var selectPaths = func(_ *cobra.Command, dev bool) (paths.Paths, error) { return paths.Resolve(dev) }

// addRootFlags lets a harness build add persistent flags.
var addRootFlags = func(*cobra.Command) {}
