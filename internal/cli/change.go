package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/darkraise/darkpanel/internal/core"
)

// changeColumns are the Change fields `change list` shows; --json has all.
var changeColumns = []string{"id", "hostId", "action", "version", "class", "state", "actor", "updatedAt", "revertDeadline"}

// changeCommand is the hand-written `change` group (spec section 15 and
// 1.3): list, show, confirm and revert, and undo, repair, release, resolve,
// effects and steps, over the network API's change routes.
func (a *app) changeCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "change",
		Short: "List, show, confirm, revert, undo, repair and release changes",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return usagef("unknown command %q for %q", args[0], cmd.CommandPath())
			}
			return cmd.Help()
		},
	}
	cmd.AddCommand(a.changeListCommand(), a.changeShowCommand(),
		a.changeOpCommand("confirm", core.StateConfirmed, "Confirm a pending revertible change before its revert deadline"),
		a.changeOpCommand("revert", core.StateReverted, "Revert a pending revertible change now"),
		a.changeUndoCommand(), a.changeRepairCommand(), a.changeReleaseCommand(), a.changeResolveCommand(),
		a.changeEffectsCommand(), a.changeStepsCommand())
	return cmd
}

func (a *app) changeListCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List changes, newest first",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := a.connect(cmd)
			if err != nil {
				return err
			}
			q := url.Values{}
			for _, name := range []string{"state", "before"} {
				if v, _ := cmd.Flags().GetString(name); v != "" {
					q.Set(name, v)
				}
			}
			if limit, _ := cmd.Flags().GetInt("limit"); limit != 0 {
				q.Set("limit", strconv.Itoa(limit))
			}
			if want, _ := cmd.Flags().GetString("host"); want != "" {
				host, err := resolveHost(cmd.Context(), c, want)
				if err != nil {
					return err
				}
				q.Set("hostId", host)
			}
			path := "/api/v1/changes"
			if len(q) > 0 {
				path += "?" + q.Encode()
			}
			var raw json.RawMessage
			if err := c.do(cmd.Context(), http.MethodGet, path, nil, &raw); err != nil {
				return err
			}
			if asJSON, _ := cmd.Flags().GetBool("json"); asJSON {
				_, err := fmt.Fprintf(cmd.OutOrStdout(), "%s\n", raw)
				return err
			}
			return printChanges(cmd, raw)
		},
	}
	cmd.Flags().String("state", "", "only changes in this state, such as pending, completed, failed or needs_repair")
	cmd.Flags().String("host", "", "only changes on this host, by id or name")
	cmd.Flags().String("before", "", "only changes older than this change id")
	cmd.Flags().Int("limit", 0, "most changes to list, 1 to 500")
	cmd.Flags().Bool("json", false, "print the raw JSON answer")
	addConnFlags(cmd, false, false)
	return cmd
}

// printChanges writes the changeColumns of each change as a table.
func printChanges(cmd *cobra.Command, raw json.RawMessage) error {
	var resp struct {
		Changes []map[string]json.RawMessage `json:"changes"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return fmt.Errorf("decode the changes: %w", err)
	}
	if len(resp.Changes) == 0 {
		_, err := fmt.Fprintln(cmd.OutOrStdout(), "(none)")
		return err
	}
	rows := make([][]string, 0, len(resp.Changes))
	for _, ch := range resp.Changes {
		row := make([]string, len(changeColumns))
		for i, col := range changeColumns {
			row[i] = cellText(ch[col])
		}
		rows = append(rows, row)
	}
	return writeTable(cmd.OutOrStdout(), changeColumns, rows)
}

func (a *app) changeShowCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show <id>",
		Short: "Show one change",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.connect(cmd)
			if err != nil {
				return err
			}
			var raw json.RawMessage
			if err := c.do(cmd.Context(), http.MethodGet, "/api/v1/changes/"+url.PathEscape(args[0]), nil, &raw); err != nil {
				return err
			}
			w := cmd.OutOrStdout()
			if asJSON, _ := cmd.Flags().GetBool("json"); asJSON {
				_, err := fmt.Fprintf(w, "%s\n", raw)
				return err
			}
			if err := render(w, raw); err != nil {
				return err
			}
			var ch changeWire
			if err := json.Unmarshal(raw, &ch); err != nil {
				return fmt.Errorf("decode the change: %w", err)
			}
			if ch.State == core.StateInterrupted && ch.Class == core.ClassCompensable && ch.CompensatedBy != nil {
				_, err = fmt.Fprintf(w, "\nThis change was interrupted and may be partly applied; its compensating action is %s:\n  darkpanel %s\n",
					sanitize(*ch.CompensatedBy), sanitize(strings.Join(CommandPath(*ch.CompensatedBy), " ")))
			}
			return err
		},
	}
	cmd.Flags().Bool("json", false, "print the raw JSON answer")
	addConnFlags(cmd, false, false)
	return cmd
}

// changeOpCommand is `change confirm` or `change revert`; the change must
// end in state want.
func (a *app) changeOpCommand(op, want, short string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   op + " <id>",
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.connect(cmd)
			if err != nil {
				return err
			}
			var raw json.RawMessage
			path := "/api/v1/changes/" + url.PathEscape(args[0]) + "/" + op
			if err := c.do(cmd.Context(), http.MethodPost, path, nil, &raw); err != nil {
				return err
			}
			var ch changeWire
			if err := json.Unmarshal(raw, &ch); err != nil {
				return fmt.Errorf("decode the change: %w", err)
			}
			if asJSON, _ := cmd.Flags().GetBool("json"); asJSON {
				_, err = fmt.Fprintf(cmd.OutOrStdout(), "%s\n", raw)
			} else {
				err = printChange(cmd.OutOrStdout(), ch, nil)
			}
			if err != nil {
				return err
			}
			if ch.State == want {
				return nil
			}
			if ch.Error != nil {
				return fmt.Errorf("change %s is %s, not %s: %s (%s)", ch.ID, ch.State, want, ch.Error.Message, ch.Error.Code)
			}
			return fmt.Errorf("change %s is %s, not %s", ch.ID, ch.State, want)
		},
	}
	cmd.Flags().Bool("json", false, "print the raw JSON answer")
	addConnFlags(cmd, false, false)
	return cmd
}

// changePath is the route of op on change id.
func changePath(id, op string) string {
	return "/api/v1/changes/" + url.PathEscape(id) + "/" + op
}

// decodeChange decodes a Change answer.
func decodeChange(raw json.RawMessage) (changeWire, error) {
	var ch changeWire
	if err := json.Unmarshal(raw, &ch); err != nil {
		return changeWire{}, fmt.Errorf("decode the change: %w", err)
	}
	return ch, nil
}

// showChange prints a change: with --json its raw JSON, else printChange.
func showChange(cmd *cobra.Command, ch changeWire, raw json.RawMessage) error {
	if asJSON, _ := cmd.Flags().GetBool("json"); asJSON {
		_, err := fmt.Fprintf(cmd.OutOrStdout(), "%s\n", raw)
		return err
	}
	return printChange(cmd.OutOrStdout(), ch, nil)
}

// printRaw prints an answer: with --json as it is, else rendered for people.
func printRaw(cmd *cobra.Command, raw json.RawMessage) error {
	if asJSON, _ := cmd.Flags().GetBool("json"); asJSON {
		_, err := fmt.Fprintf(cmd.OutOrStdout(), "%s\n", raw)
		return err
	}
	return render(cmd.OutOrStdout(), raw)
}

// undoOutcome is the error of an undo or a repair that did not end well:
// the change still needs repair, or failed.
func undoOutcome(ch changeWire) error {
	switch ch.State {
	case core.StateNeedsRepair, core.StateFailed, core.StateInterrupted:
	default:
		return nil
	}
	if ch.Error != nil {
		return fmt.Errorf("change %s %s: %s (%s)", ch.ID, ch.State, ch.Error.Message, ch.Error.Code)
	}
	return fmt.Errorf("change %s %s", ch.ID, ch.State)
}

// changeUndoCommand is `change undo`. The undo answers at once with the
// change undoing; the command waits until the undo has ended.
func (a *app) changeUndoCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "undo <id>",
		Short: "Undo a change while no later change took its locks",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.connect(cmd)
			if err != nil {
				return err
			}
			var raw json.RawMessage
			if err := c.do(cmd.Context(), http.MethodPost, changePath(args[0], "undo"), nil, &raw); err != nil {
				return err
			}
			ch, err := decodeChange(raw)
			if err != nil {
				return err
			}
			if ch, raw, err = waitChange(cmd.Context(), c, ch, raw, core.StateUndoing); err != nil {
				return err
			}
			if err := showChange(cmd, ch, raw); err != nil {
				return err
			}
			return undoOutcome(ch)
		},
	}
	cmd.Flags().Bool("json", false, "print the raw JSON answer")
	addConnFlags(cmd, false, false)
	return cmd
}

// changeRepairCommand is `change repair`. The controller answers with the
// change the repair started, which the command waits for, or with a plan of
// the repairing action, which it shows, asks about and applies like any
// plan. The repairing action is known only from that answer, so
// --confirm-timeout is range-checked before the request and refused after
// it when the plan's action is not revertible.
func (a *app) changeRepairCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "repair <id>",
		Short: "Repair a change that needs repair",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := confirmTimeout(cmd); err != nil {
				return err
			}
			body, err := repairBody(cmd)
			if err != nil {
				return err
			}
			c, err := a.connect(cmd)
			if err != nil {
				return err
			}
			var res struct {
				Change json.RawMessage `json:"change"`
				Plan   *planWire       `json:"plan"`
			}
			if err := c.do(cmd.Context(), http.MethodPost, changePath(args[0], "repair"), body, &res); err != nil {
				return err
			}
			if res.Plan != nil {
				return a.applyRepairPlan(cmd, c, *res.Plan)
			}
			ch, err := decodeChange(res.Change)
			if err != nil {
				return err
			}
			ch, raw, err := waitChange(cmd.Context(), c, ch, res.Change, core.StateUndoing, core.StateApplying)
			if err != nil {
				return err
			}
			if err := showChange(cmd, ch, raw); err != nil {
				return err
			}
			return undoOutcome(ch)
		},
	}
	cmd.Flags().String("input", "", `options of the repairing action as a JSON object, such as {"outcome":"keep"}`)
	cmd.Flags().String("step", "", "the step of a composite to decide, with --as")
	cmd.Flags().String("as", "", "what the step counts as: reverted or applied")
	cmd.Flags().Bool("yes", false, "apply a repair plan without asking Apply? [y/N]; an irreversible action still needs its confirmation phrase")
	cmd.Flags().String("confirmation", "", "the confirmation phrase a repair plan names, instead of typing it")
	addConfirmTimeoutFlag(cmd)
	cmd.Flags().Bool("json", false, "print the raw JSON answer")
	addConnFlags(cmd, false, false)
	return cmd
}

// repairBody is the repair request of --input, or of --step and --as.
func repairBody(cmd *cobra.Command) (map[string]any, error) {
	input, _ := cmd.Flags().GetString("input")
	step, _ := cmd.Flags().GetString("step")
	as, _ := cmd.Flags().GetString("as")
	body := map[string]any{}
	switch {
	case input != "" && (step != "" || as != ""):
		return nil, usagef("--input cannot be combined with --step and --as")
	case (step == "") != (as == ""):
		return nil, usagef("--step and --as go together")
	case step != "" && as != "reverted" && as != "applied":
		return nil, usagef("--as must be reverted or applied, not %q", as)
	case step != "":
		body["step"], body["as"] = step, as
	case input != "":
		var obj map[string]json.RawMessage
		if err := json.Unmarshal([]byte(input), &obj); err != nil || obj == nil {
			return nil, usagef("--input must be a JSON object")
		}
		body["input"] = json.RawMessage(input)
	}
	return body, nil
}

// applyRepairPlan shows a repair plan, asks, and applies it through the
// ordinary apply route of its host and action.
func (a *app) applyRepairPlan(cmd *cobra.Command, c *client, plan planWire) error {
	def, ok := a.opts.Registry.Action(core.ActionRef{Name: plan.Action, Version: plan.Version})
	if !ok {
		return fmt.Errorf("the repair plan %s is for %s/v%d, which this darkpanel does not know; apply it through the API",
			sanitize(plan.PlanID), sanitize(plan.Action), plan.Version)
	}
	if timeout, _ := cmd.Flags().GetInt("confirm-timeout"); timeout != 0 && def.Spec.Class != core.ClassRevertible {
		return usagef("--confirm-timeout applies only to a revertible repair action; %s is %s; nothing was applied",
			def.Ref(), def.Spec.Class)
	}
	host := ""
	if plan.HostID != nil {
		host = *plan.HostID
	}
	calls := apiCalls(c, def, host)
	calls.plan = func(context.Context, any) (planWire, error) { return plan, nil }
	ch, raw, err := a.planAndApply(cmd, calls, nil, false)
	if err != nil {
		return err
	}
	if err := showChange(cmd, ch, raw); err != nil {
		return err
	}
	return changeOutcome(ch)
}

func (a *app) changeReleaseCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "release <id>",
		Short: "Give up a change that needs repair, abandoning its unsettled effects",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			reason, _ := cmd.Flags().GetString("reason")
			if strings.TrimSpace(reason) == "" {
				return usagef("--reason is required: say why the change is given up")
			}
			c, err := a.connect(cmd)
			if err != nil {
				return err
			}
			var raw json.RawMessage
			if err := c.do(cmd.Context(), http.MethodPost, changePath(args[0], "release"), map[string]any{"reason": reason}, &raw); err != nil {
				return err
			}
			w := cmd.OutOrStdout()
			if asJSON, _ := cmd.Flags().GetBool("json"); asJSON {
				_, err := fmt.Fprintf(w, "%s\n", raw)
				return err
			}
			var res struct {
				Change    changeWire      `json:"change"`
				Abandoned json.RawMessage `json:"abandoned"`
			}
			if err := json.Unmarshal(raw, &res); err != nil {
				return fmt.Errorf("decode the release: %w", err)
			}
			if err := printChange(w, res.Change, nil); err != nil {
				return err
			}
			if _, err := fmt.Fprintln(w, "Abandoned effects:"); err != nil {
				return err
			}
			return render(w, res.Abandoned)
		},
	}
	cmd.Flags().String("reason", "", "why the change is given up, 1 to 500 characters (required)")
	cmd.Flags().Bool("json", false, "print the raw JSON answer")
	addConnFlags(cmd, false, false)
	return cmd
}

func (a *app) changeResolveCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "resolve <id> accept|reconcile",
		Short: "Resolve an unresolved effect: accept the state found, or reconcile the journal with it",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			resolution := args[1]
			if resolution != core.ResolutionAccept && resolution != core.ResolutionReconcile {
				return usagef("the resolution is accept or reconcile, not %q", resolution)
			}
			seq, _ := cmd.Flags().GetInt("effect")
			if seq < 1 {
				return usagef("--effect is required: the effect's sequence number, from darkpanel change effects")
			}
			body := map[string]any{"resolution": resolution}
			if resource, _ := cmd.Flags().GetString("resource"); resource != "" {
				body["resource"] = resource
			}
			c, err := a.connect(cmd)
			if err != nil {
				return err
			}
			var raw json.RawMessage
			path := changePath(args[0], "effects/"+strconv.Itoa(seq)+"/resolve")
			if err := c.do(cmd.Context(), http.MethodPost, path, body, &raw); err != nil {
				return err
			}
			return printRaw(cmd, raw)
		},
	}
	cmd.Flags().Int("effect", 0, "the sequence number of the effect to resolve")
	cmd.Flags().String("resource", "", "with reconcile: the candidate object to record when the identity matches several")
	cmd.Flags().Bool("json", false, "print the raw JSON answer")
	addConnFlags(cmd, false, false)
	return cmd
}

func (a *app) changeEffectsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "effects <id>",
		Short: "List a change's effects, or observe one with --observe",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			seq, _ := cmd.Flags().GetInt("observe")
			if cmd.Flags().Changed("observe") && seq < 1 {
				return usagef("--observe takes an effect's sequence number, 1 or more")
			}
			c, err := a.connect(cmd)
			if err != nil {
				return err
			}
			path := changePath(args[0], "effects")
			if seq > 0 {
				path += "/" + strconv.Itoa(seq) + "/observe"
			}
			var raw json.RawMessage
			if err := c.do(cmd.Context(), http.MethodGet, path, nil, &raw); err != nil {
				return err
			}
			return printRaw(cmd, raw)
		},
	}
	cmd.Flags().Int("observe", 0, "observe this effect's resource now, changing nothing: the preview of a resolution")
	cmd.Flags().Bool("json", false, "print the raw JSON answer")
	addConnFlags(cmd, false, false)
	return cmd
}

func (a *app) changeStepsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "steps <id>",
		Short: "List a composite change's steps",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.connect(cmd)
			if err != nil {
				return err
			}
			var raw json.RawMessage
			if err := c.do(cmd.Context(), http.MethodGet, changePath(args[0], "steps"), nil, &raw); err != nil {
				return err
			}
			return printRaw(cmd, raw)
		},
	}
	cmd.Flags().Bool("json", false, "print the raw JSON answer")
	addConnFlags(cmd, false, false)
	return cmd
}
