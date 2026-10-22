package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/spf13/cobra"

	"github.com/darkraise/darkpanel/internal/cli"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/install"
	"github.com/darkraise/darkpanel/internal/modules"
	"github.com/darkraise/darkpanel/internal/platform"
)

// plannedUninstall is what uninstall does with install.PlanUninstall's plan.
type plannedUninstall interface {
	Describe(w io.Writer)
	Run(ctx context.Context, w io.Writer) error
	Close() error
}

// planUninstall plans the uninstall; tests replace it.
var planUninstall = func(ctx context.Context, cfg install.UninstallConfig) (plannedUninstall, error) {
	u, err := install.PlanUninstall(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return u, nil
}

func newUninstallCmd() *cobra.Command {
	var keepData, yes bool
	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove Darkpanel from this host: services, units, users, binary and, unless --keep-data, its data",
		Args:  cobra.NoArgs,
		PreRunE: func(*cobra.Command, []string) error {
			if geteuid() != 0 {
				return errors.New("uninstall runs as root; run it with sudo")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			sd, err := newSystemd(cmd.Context())
			if err != nil {
				return err
			}
			plan, err := planUninstall(cmd.Context(), install.UninstallConfig{
				Layout: installLayout(), KeepData: keepData, Modules: modules.All(),
				Systemd: sd, Exec: platform.NewRunner(platform.ProductionRules()), Users: install.SystemUsers{},
				Clock: core.SystemClock{}, Log: slog.New(slog.NewTextHandler(cmd.ErrOrStderr(), nil)),
			})
			if err != nil {
				return errors.Join(err, sd.Close())
			}
			plan.Describe(cmd.OutOrStdout())
			if err := confirmUninstall(cmd, yes); err != nil {
				return errors.Join(err, plan.Close(), sd.Close())
			}
			return errors.Join(plan.Run(cmd.Context(), cmd.OutOrStdout()), sd.Close())
		},
	}
	cmd.Flags().BoolVar(&keepData, "keep-data", false,
		"keep the databases, backups, update records, keys, agent credentials and users, so a later install finds them")
	cmd.Flags().BoolVar(&yes, "yes", false, "uninstall without asking Proceed? [y/N]")
	return cmd
}

// confirmUninstall asks Proceed? [y/N] on the terminal unless yes; without a
// terminal only --yes proceeds. A refusal exits 3, as a declined prompt does.
func confirmUninstall(cmd *cobra.Command, yes bool) error {
	if yes {
		return nil
	}
	in := cmd.InOrStdin()
	if !stdinTerminal(in) {
		return cli.ExitWith(cli.ExitRefused, errors.New("uninstall needs --yes when standard input is not a terminal"))
	}
	_, _ = fmt.Fprint(cmd.ErrOrStderr(), "Proceed? [y/N] ")
	answer, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	if a := strings.ToLower(strings.TrimSpace(answer)); a != "y" && a != "yes" {
		return cli.ExitWith(cli.ExitRefused, errors.New("uninstall declined"))
	}
	return nil
}
