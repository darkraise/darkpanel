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

	"github.com/darkraise/darkpanel/internal/agent/daemon"
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

// abortUninstall clears the uninstall barrier; tests replace it.
var abortUninstall = install.AbortUninstall

// loadCredentials reads the agent's credentials, which name the audit
// actor of --abort; tests replace it.
var loadCredentials = daemon.LoadCredentials

func newUninstallCmd() *cobra.Command {
	var keepData, yes, stopTasks, abort bool
	var optionFlags []string
	var options map[string]map[string]string
	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove Darkpanel from this host: services, units, users, binary and, unless --keep-data, its data",
		Args:  cobra.NoArgs,
		// cli marks only RunE errors as run errors, so a malformed
		// --module-option exits 2 like cobra's own flag errors.
		PreRunE: func(*cobra.Command, []string) error {
			if geteuid() != 0 {
				return errors.New("uninstall runs as root; run it with sudo")
			}
			if abort && (stopTasks || keepData || len(optionFlags) > 0) {
				return errors.New("--abort takes none of --stop-tasks, --keep-data and --module-option")
			}
			var err error
			options, err = parseModuleOptions(optionFlags)
			return err
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			sd, err := newSystemd(cmd.Context())
			if err != nil {
				return err
			}
			if abort {
				return errors.Join(runAbort(cmd, sd), sd.Close())
			}
			plan, err := planUninstall(cmd.Context(), install.UninstallConfig{
				Layout: installLayout(), KeepData: keepData, StopTasks: stopTasks, Modules: modules.All(), ModuleOptions: options,
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
	cmd.Flags().StringArrayVar(&optionFlags, "module-option", nil,
		"<module>.<key>=<value>: an option of a module's uninstall step (repeatable); the plan lists every option with its default")
	cmd.Flags().BoolVar(&stopTasks, "stop-tasks", false,
		"stop the task units of unfinished changes instead of refusing to uninstall while one is active")
	cmd.Flags().BoolVar(&abort, "abort", false,
		"clear the uninstall barrier of an uninstall that stopped before it stopped the services, so the agent accepts changes again")
	return cmd
}

// runAbort is uninstall --abort: it clears the uninstall barrier while the
// agent service still runs, audited as this host's local root user.
func runAbort(cmd *cobra.Command, sd platform.Systemd) error {
	l := installLayout()
	actor, err := abortActor(l.Paths.AgentTLSDir)
	if err != nil {
		return err
	}
	line, err := abortUninstall(cmd.Context(), install.UninstallConfig{Layout: l, Systemd: sd, Actor: actor})
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(cmd.OutOrStdout(), line)
	return err
}

// abortActor names the local root user after the host the agent's
// credentials name, as the agent socket does, and core.ActorUnenrolled
// before the agent enrolled.
func abortActor(tlsDir string) (string, error) {
	creds, err := loadCredentials(tlsDir)
	switch {
	case errors.Is(err, daemon.ErrNotEnrolled):
		return core.ActorUnenrolled, nil
	case err != nil:
		return "", fmt.Errorf("read the agent credentials: %w", err)
	}
	return "local:root@" + creds.HostID(), nil
}

// parseModuleOptions reads --module-option values, each
// <module>.<key>=<value>, by module id and key. The key ends at the first
// "=", so a value may hold one.
func parseModuleOptions(values []string) (map[string]map[string]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	out := map[string]map[string]string{}
	for _, v := range values {
		module, rest, dot := strings.Cut(v, ".")
		key, value, eq := strings.Cut(rest, "=")
		if !dot || !eq || module == "" || key == "" {
			return nil, fmt.Errorf("--module-option %q is not <module>.<key>=<value>", v)
		}
		if _, dup := out[module][key]; dup {
			return nil, fmt.Errorf("--module-option %s.%s is given twice", module, key)
		}
		if out[module] == nil {
			out[module] = map[string]string{}
		}
		out[module][key] = value
	}
	return out, nil
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
