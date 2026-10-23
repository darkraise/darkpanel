package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"slices"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/darkraise/darkpanel/internal/cli"
	"github.com/darkraise/darkpanel/internal/controller/rollout"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/paths"
	"github.com/darkraise/darkpanel/internal/update"
)

// updateActions are the generated commands the update group adopts: the
// cli refuses a reserved group that leaves one of them out.
var updateActions = []string{
	update.ActionApply, update.ActionRollback, update.ActionStatus,
	rollout.ActionRollout, rollout.ActionRolloutCancel, rollout.ActionRollouts,
}

// hostUpdater is what the update commands call on the agent's updater.
type hostUpdater interface {
	Run(ctx context.Context, tx string) error
	Recover(ctx context.Context, mode update.RecoverMode, tx string) error
	RunRollback(ctx context.Context, tx string) error
	DirectRollback(ctx context.Context, force bool) (core.UpdateTransaction, error)
}

// openUpdater opens the installed agent's updater and returns it with its
// close; tests replace it.
var openUpdater = func(ctx context.Context, p paths.Paths, log *slog.Logger) (hostUpdater, func() error, error) {
	host, err := openAgent(ctx, p, log)
	if err != nil {
		return nil, nil, err
	}
	return host.Updater, host.Close, nil
}

// withUpdater runs fn on the installed agent's updater. These commands act
// on the installed binary, so they have no --dev.
func withUpdater(cmd *cobra.Command, fn func(context.Context, hostUpdater) error) error {
	p, err := selectPaths(cmd, false)
	if err != nil {
		return err
	}
	log := slog.New(slog.NewTextHandler(cmd.ErrOrStderr(), nil))
	u, closeFn, err := openUpdater(cmd.Context(), p, log)
	if err != nil {
		return err
	}
	return errors.Join(fn(cmd.Context(), u), closeFn())
}

// rootRefusal is the refusal of an update command run without root; cli
// exits 2 for it, like a flag error.
func rootRefusal(cmd *cobra.Command) error {
	if geteuid() == 0 {
		return nil
	}
	return fmt.Errorf("%s acts on the installed binary and runs as root; run it with sudo",
		strings.TrimPrefix(cmd.CommandPath(), "darkpanel "))
}

func newUpdateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Update, roll back and recover this host's binary, and run rollouts",
		RunE:  runGroup,
	}
	cmd.AddCommand(newUpdateRunCmd(), newUpdateRecoverCmd())
	return cmd
}

func newUpdateRunCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "run <transaction>",
		Short:   "Run one update transaction; the update's transient unit runs this",
		Args:    cobra.ExactArgs(1),
		PreRunE: func(cmd *cobra.Command, _ []string) error { return rootRefusal(cmd) },
		RunE: func(cmd *cobra.Command, args []string) error {
			return withUpdater(cmd, func(ctx context.Context, u hostUpdater) error { return u.Run(ctx, args[0]) })
		},
	}
}

func newUpdateRecoverCmd() *cobra.Command {
	var (
		boot     bool
		deadline string
	)
	cmd := &cobra.Command{
		Use:   "recover",
		Short: "Bring unfinished updates to a consistent state; the recovery units and the deadline timer run this",
		Args:  cobra.NoArgs,
		PreRunE: func(cmd *cobra.Command, _ []string) error {
			if boot && deadline != "" {
				return errors.New("--boot and --deadline exclude each other")
			}
			return rootRefusal(cmd)
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			mode := update.RecoverFailure
			switch {
			case boot:
				mode = update.RecoverBoot
			case deadline != "":
				mode = update.RecoverDeadline
			}
			return withUpdater(cmd, func(ctx context.Context, u hostUpdater) error { return u.Recover(ctx, mode, deadline) })
		},
	}
	cmd.Flags().BoolVar(&boot, "boot", false, "recover at boot, before the services start: roll back without restarting anything")
	cmd.Flags().StringVar(&deadline, "deadline", "", "roll back this swapped transaction whose health deadline passed")
	return cmd
}

// addDirectRollback gives the generated update rollback command --direct and
// --transaction. Without --direct the generated flow runs unchanged, also
// with --local, where the agent socket and the local fallback serve it.
func addDirectRollback(root *cobra.Command) error {
	cmd, ok := cli.FindCommand(root, update.ActionRollback)
	if !ok {
		return fmt.Errorf("no command for %s", update.ActionRollback)
	}
	var (
		direct bool
		tx     string
	)
	cmd.Flags().BoolVar(&direct, "direct", false,
		"roll back in this process, without the agent; the break-glass path when no agent answers (root)")
	cmd.Flags().StringVar(&tx, "transaction", "", "with --direct, run this rollback transaction; the rollback's transient unit passes it")
	// --direct asks nothing: the rollback unit and the --local fallback,
	// which has already asked, call it. People are steered to --local --yes.
	for _, name := range []string{"direct", "transaction"} {
		if err := cmd.Flags().MarkHidden(name); err != nil {
			return err
		}
	}
	generated := cmd.RunE
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if !direct {
			if tx != "" {
				return cli.ExitWith(cli.ExitUsage, errors.New("--transaction needs --direct"))
			}
			return generated(cmd, args)
		}
		for _, name := range []string{"host", "url"} {
			if cmd.Flags().Changed(name) {
				return cli.ExitWith(cli.ExitUsage, fmt.Errorf("--%s does not apply with --direct, which rolls back this host", name))
			}
		}
		for _, name := range []string{"local", "yes", "confirmation", "input"} {
			if cmd.Flags().Changed(name) {
				return cli.ExitWith(cli.ExitUsage, fmt.Errorf("--%s does not apply with --direct, which rolls back in this process without a plan", name))
			}
		}
		if tx != "" {
			if cmd.Flags().Changed("force") {
				return cli.ExitWith(cli.ExitUsage, errors.New("--force does not apply with --transaction: the guards ran when the rollback started"))
			}
			if cmd.Flags().Changed("json") {
				return cli.ExitWith(cli.ExitUsage, errors.New("--json does not apply with --transaction, which prints no result"))
			}
		}
		if err := rootRefusal(cmd); err != nil {
			return cli.ExitWith(cli.ExitUsage, err)
		}
		// The replaced RunE is outside the cli's own marking, so a failure
		// carries its exit status itself.
		if err := directRollback(cmd, tx); err != nil {
			return cli.ExitWith(cli.ExitError, err)
		}
		return nil
	}
	return nil
}

func directRollback(cmd *cobra.Command, tx string) error {
	force, err := cmd.Flags().GetBool("force")
	if err != nil {
		return err
	}
	asJSON, err := cmd.Flags().GetBool("json")
	if err != nil {
		return err
	}
	return withUpdater(cmd, func(ctx context.Context, u hostUpdater) error {
		if tx != "" {
			return u.RunRollback(ctx, tx)
		}
		tr, err := u.DirectRollback(ctx, force)
		if err != nil {
			return err
		}
		if asJSON {
			return json.NewEncoder(cmd.OutOrStdout()).Encode(tr)
		}
		_, err = fmt.Fprintf(cmd.OutOrStdout(), "rollback %s from %s to %s: %s\n", tr.ID, tr.FromVersion, tr.ToVersion, tr.State)
		return err
	})
}

// rollbackFallback is core.update.rollback when --local finds no agent
// socket: the previous binary rolls back in its own process, because the
// rollback replaces the binary this process runs. That process runs with
// --direct, which never starts another, so the re-execution cannot loop.
func rollbackFallback(stderr io.Writer) cli.LocalFallback {
	return func(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
		var in core.UpdateRollbackInput
		if err := json.Unmarshal(input, &in); err != nil {
			return nil, err
		}
		p, err := selectPaths(nil, false)
		if err != nil {
			return nil, err
		}
		prev := p.PrevBinary()
		switch _, err := os.Stat(prev); {
		case errors.Is(err, fs.ErrNotExist):
			return nil, errcode.New(errcode.UpdateNothingToRollBack, "there is no previous binary to roll back to")
		case err != nil:
			return nil, err
		}
		args := []string{"update", "rollback", "--direct", "--json"}
		if in.Force {
			args = append(args, "--force")
		}
		var out bytes.Buffer
		run := exec.CommandContext(ctx, prev, args...) //nolint:gosec // G204: the installed previous binary, never a path from a request
		run.Stdout, run.Stderr = &out, stderr
		// A rollback cut short leaves the services stopped. The previous
		// binary may predate this code, so it is protected from here: in its
		// own process group a terminal's Ctrl+C and hang-up never reach it,
		// and this process holds both until it has finished and reported.
		run.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		held := make(chan os.Signal, 1)
		signal.Notify(held, os.Interrupt, syscall.SIGHUP)
		defer signal.Stop(held)
		if err := run.Run(); err != nil {
			return nil, fmt.Errorf("%s %s: %w", prev, strings.Join(args, " "), err)
		}
		if !json.Valid(out.Bytes()) {
			return nil, fmt.Errorf("%s %s printed no JSON transaction", prev, strings.Join(args, " "))
		}
		return json.RawMessage(bytes.TrimSpace(out.Bytes())), nil
	}
}

// newerAgents is update.Config.NewerAgents on the controller's host: the
// names of the enrolled hosts whose agent reports a version newer than
// version, read from controller.db. It only reads; run as root, SQLite gives
// any -wal or -shm file it creates the database file's owner, so the
// controller can still open them. The open is read-only because the agent
// checked for controller.db when it started, possibly long before: one
// removed since must not come back empty and owned by root.
func newerAgents(controllerDB string) func(ctx context.Context, version string) ([]string, error) {
	return func(ctx context.Context, version string) ([]string, error) {
		v, err := update.ParseVersion(version)
		if err != nil {
			return nil, err
		}
		db, err := sql.Open("sqlite", "file:"+controllerDB+"?mode=ro&_pragma=busy_timeout(5000)&_pragma=query_only(1)")
		if err != nil {
			return nil, err
		}
		defer func() { _ = db.Close() }()
		rows, err := db.QueryContext(ctx,
			`SELECT name, agent_version FROM core_hosts WHERE state = 'enrolled' AND agent_version IS NOT NULL`)
		if err != nil {
			return nil, err
		}
		defer func() { _ = rows.Close() }()
		var newer []string
		for rows.Next() {
			var name, agent string
			if err := rows.Scan(&name, &agent); err != nil {
				return nil, err
			}
			// A version that does not parse is not known to be newer.
			if av, err := update.ParseVersion(agent); err == nil && av.Compare(v) > 0 {
				newer = append(newer, name)
			}
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
		slices.Sort(newer)
		return newer, nil
	}
}
