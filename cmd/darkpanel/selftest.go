package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"

	"github.com/spf13/cobra"

	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/registry"
	"github.com/darkraise/darkpanel/schemas"
)

// selfTestCheck is one check of selftest, named in its failure.
type selfTestCheck struct {
	name string
	run  func() error
}

// selfTestChecks are the checks the updater relies on before it switches a
// host to a new binary: they need no database, socket or network, so they
// pass on any host the binary was built for.
var selfTestChecks = []selfTestCheck{
	{"registry", func() error {
		_, err := registry.Build()
		return err
	}},
	{"migrations", checkCoreMigrations},
	{"schemas", checkSchemaSnapshot},
	{"trusted keys", checkTrustedKeys},
}

func newSelftestCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "selftest",
		Short: "Check that this binary can run; the updater runs it on a new binary before switching to it",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			for _, c := range selfTestChecks {
				if err := c.run(); err != nil {
					return fmt.Errorf("selftest failed the %s check: %w", c.name, err)
				}
			}
			_, err := fmt.Fprintln(cmd.OutOrStdout(), "selftest: ok")
			return err
		},
	}
}

func checkCoreMigrations() error {
	for _, side := range []string{store.SideController, store.SideAgent} {
		set, err := store.CoreMigrations(side)
		if err != nil {
			return err
		}
		files, err := fs.Glob(set.FS, "*.sql")
		if err != nil {
			return err
		}
		if len(files) == 0 {
			return fmt.Errorf("no %s core migrations are embedded", side)
		}
	}
	return nil
}

func checkSchemaSnapshot() error {
	files, err := fs.Glob(schemas.FS, "*/v*.json")
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return errors.New("no schema snapshots are embedded")
	}
	data, err := fs.ReadFile(schemas.FS, files[0])
	if err != nil {
		return err
	}
	if !json.Valid(data) {
		return fmt.Errorf("schema snapshot %s is not valid JSON", files[0])
	}
	return nil
}

// checkTrustedKeys refuses a binary that trusts no release key: once the
// updater switched a host to it, the host could never verify, and so never
// install, another update.
func checkTrustedKeys() error {
	if len(trustedKeys()) == 0 {
		return errors.New("this build trusts no release signing key, so it could never verify an update")
	}
	return nil
}
