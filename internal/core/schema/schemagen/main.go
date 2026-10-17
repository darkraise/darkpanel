// Command schemagen writes the schema snapshot of every registered action and
// resource (spec section 7). Run it through `make schemas`; a test in
// internal/modules fails when the committed snapshots are out of date.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/darkraise/darkpanel/internal/core/schema"
	"github.com/darkraise/darkpanel/internal/registry"
)

func main() {
	out := flag.String("out", "schemas", "directory to write the snapshots into")
	flag.Parse()
	if err := run(*out); err != nil {
		fmt.Fprintln(os.Stderr, "schemagen:", err)
		os.Exit(1)
	}
}

func run(out string) error {
	reg, err := registry.Build()
	if err != nil {
		return err
	}
	files, err := schema.Snapshots(reg)
	if err != nil {
		return err
	}
	return schema.WriteSnapshots(out, files)
}
