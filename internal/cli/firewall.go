package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/darkraise/darkpanel/internal/agent/api"
	"github.com/darkraise/darkpanel/internal/core"
)

// The firewall names the break-glass rollback uses (spec 2.5).
const (
	firewallRestoreAction = "firewall.snapshot.restore"
	firewallModulePrefix  = "firewall."
	systemActorPrefix     = "system:"
)

// firewallChangePage is how many changes one listing of the agent socket
// asks for while rollback looks for the newest firewall change. It is a
// variable so tests can shorten it.
var firewallChangePage = 100

// firewallCommands are the hand-written commands of the firewall module,
// added under "darkpanel firewall": the break-glass rollback (spec 2.5).
// The break-glass snapshot restore is the generated command of
// firewall.snapshot.restore, which --local already serves.
func firewallCommands(a *app) []*cobra.Command {
	return []*cobra.Command{a.firewallRollbackCommand()}
}

func (a *app) firewallRollbackCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rollback",
		Short: "Roll back a firewall change through this host's agent socket (break-glass, needs --local)",
		Long: "Roll back the newest firewall change that no background task made, or the change --change names, " +
			"through this host's agent socket. A pending change is reverted; a confirmed change is undone by restoring " +
			"the snapshot taken before it, and the restore is confirmed as soon as it verifies. Run it as root with --local.",
		Args:        cobra.NoArgs,
		Annotations: map[string]string{annotationLocal: "true"},
		RunE:        func(cmd *cobra.Command, _ []string) error { return a.runFirewallRollback(cmd) },
	}
	cmd.Flags().String("change", "", "the firewall change to roll back (default: the newest one no background task made)")
	cmd.Flags().Bool("yes", false, "roll back without asking Apply? [y/N]")
	cmd.Flags().Bool("manual", false, "let the snapshot restore add or remove manual rules, which its plan lists")
	cmd.Flags().StringArray("override", nil, "plan the snapshot restore again with this failing guard overridden (repeatable); overridable: firewall.lockout")
	cmd.Flags().Bool("json", false, "print the raw JSON of the change")
	return cmd
}

// runFirewallRollback reverts a pending firewall change, or restores the
// snapshot of a confirmed one and confirms the restore in the same run.
func (a *app) runFirewallRollback(cmd *cobra.Command) error {
	if local, _ := cmd.Flags().GetBool("local"); !local {
		return usagef("%s is a break-glass command: run it as root on the host with --local", cmd.CommandPath())
	}
	id, _ := cmd.Flags().GetString("change")
	return a.withAgent(cmd, nil, nil, nil, func(c *api.Client) error {
		ch, err := firewallTarget(cmd.Context(), c, id)
		if err != nil {
			return err
		}
		switch ch.State {
		case core.StatePending:
			return a.firewallRevert(cmd, c, ch)
		case core.StateConfirmed:
			return a.firewallRestore(cmd, c, ch)
		default:
			return refusedf("change %s (%s) is %s: only a pending or a confirmed firewall change can be rolled back",
				sanitize(ch.ID), sanitize(ch.Action), sanitize(ch.State))
		}
	})
}

// firewallTarget returns the change id names, which must be a firewall
// change, or else the newest firewall change that is neither a background
// task's nor a step of a composite.
func firewallTarget(ctx context.Context, c *api.Client, id string) (core.ChangeView, error) {
	if id != "" {
		cctx, cancel := context.WithTimeout(ctx, localTimeout)
		defer cancel()
		v, err := c.Change(cctx, id)
		if err != nil {
			return core.ChangeView{}, localError(err)
		}
		if !strings.HasPrefix(v.Action, firewallModulePrefix) {
			return core.ChangeView{}, refusedf("change %s is %s, not a firewall change", sanitize(v.ID), sanitize(v.Action))
		}
		return v, nil
	}
	before := ""
	for {
		cctx, cancel := context.WithTimeout(ctx, localTimeout)
		page, err := c.Changes(cctx, core.ChangeFilter{Before: before, Limit: firewallChangePage})
		cancel()
		if err != nil {
			return core.ChangeView{}, localError(err)
		}
		for _, v := range page {
			if strings.HasPrefix(v.Action, firewallModulePrefix) && !strings.HasPrefix(v.Actor, systemActorPrefix) && v.ParentChangeID == "" {
				return v, nil
			}
		}
		if len(page) < firewallChangePage {
			return core.ChangeView{}, refusedf("this host has no firewall change to roll back")
		}
		before = page[len(page)-1].ID
	}
}

// firewallRevert reverts the pending change ch through the agent's revert
// route, after the Apply? [y/N] question.
func (a *app) firewallRevert(cmd *cobra.Command, c *api.Client, ch core.ChangeView) error {
	info := firewallInfo(cmd)
	_, _ = fmt.Fprintf(info, "Change %s (%s) is pending: it will be reverted now.\n", sanitize(ch.ID), sanitize(ch.Action))
	if err := a.askApply(cmd, false); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(cmd.Context(), localTimeout)
	defer cancel()
	v, err := c.Revert(ctx, core.Principal{}, ch.ID)
	if err != nil {
		return localError(err)
	}
	return firewallShow(cmd, localChange(v), core.StateReverted)
}

// firewallRestore plans firewall.snapshot.restore with the snapshot taken
// before the confirmed change ch, shows the plan, asks, applies it and
// confirms it as soon as it verifies.
func (a *app) firewallRestore(cmd *cobra.Command, c *api.Client, ch core.ChangeView) error {
	def, ok := latestAction(a.opts.Registry, firewallRestoreAction)
	if !ok {
		return fmt.Errorf("this darkpanel has no %s action, so it cannot restore a snapshot", firewallRestoreAction)
	}
	snap, err := firewallSnapshotOf(ch)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(firewallInfo(cmd), "Change %s (%s) is confirmed: snapshot %s, taken before it, will be restored and confirmed.\n",
		sanitize(ch.ID), sanitize(ch.Action), sanitize(snap))
	in := map[string]any{"id": snap}
	if manual, _ := cmd.Flags().GetBool("manual"); manual {
		in["manual"] = true
	}
	raw, err := json.Marshal(in)
	if err != nil {
		return err
	}
	input, err := withOverrides(cmd, raw)
	if err != nil {
		return err
	}
	calls := localCalls(c, def, a.opts.AgentSocket)
	apply := calls.apply
	calls.apply = func(ctx context.Context, planID, confirmation string, _ bool, timeout time.Duration) (changeWire, json.RawMessage, error) {
		return apply(ctx, planID, confirmation, true, timeout)
	}
	restored, rawChange, err := a.planAndApply(cmd, calls, input, false)
	if err != nil {
		return err
	}
	if asJSON, _ := cmd.Flags().GetBool("json"); asJSON {
		if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%s\n", rawChange); err != nil {
			return err
		}
	} else if err := printChange(cmd.OutOrStdout(), restored, nil); err != nil {
		return err
	}
	return firewallOutcome(restored, core.StateConfirmed)
}

// firewallSnapshotOf returns the id of the snapshot the firewall took
// before change ch, which every mutating firewall action's output names.
func firewallSnapshotOf(ch core.ChangeView) (string, error) {
	var out struct {
		Snapshot string `json:"snapshot"`
	}
	if len(ch.Output) > 0 {
		if err := json.Unmarshal(ch.Output, &out); err != nil {
			return "", fmt.Errorf("decode the output of change %s: %w", ch.ID, err)
		}
	}
	if out.Snapshot == "" {
		return "", refusedf("change %s names no snapshot taken before it, so it cannot be rolled back; "+
			"list the snapshots with darkpanel firewall snapshots get --local", sanitize(ch.ID))
	}
	return out.Snapshot, nil
}

// firewallInfo is where rollback writes what it is about to do: standard
// output, or standard error with --json.
func firewallInfo(cmd *cobra.Command) io.Writer {
	if asJSON, _ := cmd.Flags().GetBool("json"); asJSON {
		return cmd.ErrOrStderr()
	}
	return cmd.OutOrStdout()
}

// firewallShow prints a change as rollback ended it, and fails unless it
// is in state want.
func firewallShow(cmd *cobra.Command, ch changeWire, want string) error {
	if asJSON, _ := cmd.Flags().GetBool("json"); asJSON {
		raw, err := json.Marshal(ch)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%s\n", raw); err != nil {
			return err
		}
	} else if err := printChange(cmd.OutOrStdout(), ch, nil); err != nil {
		return err
	}
	return firewallOutcome(ch, want)
}

// firewallOutcome is the error of a rollback whose change did not end in
// state want.
func firewallOutcome(ch changeWire, want string) error {
	if ch.State == want {
		return nil
	}
	if ch.Error != nil {
		return fmt.Errorf("change %s is %s, not %s: %s (%s)", ch.ID, ch.State, want, ch.Error.Message, ch.Error.Code)
	}
	return fmt.Errorf("change %s is %s, not %s", ch.ID, ch.State, want)
}
