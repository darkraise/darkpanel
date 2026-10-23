package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/darkraise/darkpanel/internal/core"
)

// planWire is the part of the API's Plan (spec section 12) the CLI reads.
type planWire struct {
	PlanID       string          `json:"planId"`
	HostID       *string         `json:"hostId"`
	Action       string          `json:"action"`
	Version      int             `json:"version"`
	Diff         []core.DiffLine `json:"diff"`
	Guards       []core.Guard    `json:"guards"`
	Confirmation *string         `json:"confirmation"`
}

// changeWire is the API's Change. A change from the agent socket is
// converted to it, so --json prints the same object either way. The
// lifecycle fields of spec 1.3 are left out when unset, so a change without
// them prints as it did before they existed.
type changeWire struct {
	ID             string           `json:"id"`
	HostID         *string          `json:"hostId"`
	Action         string           `json:"action"`
	Version        int              `json:"version"`
	Class          core.Class       `json:"class"`
	State          string           `json:"state"`
	Actor          string           `json:"actor"`
	CreatedAt      time.Time        `json:"createdAt"`
	UpdatedAt      time.Time        `json:"updatedAt"`
	RevertDeadline *time.Time       `json:"revertDeadline"`
	Output         json.RawMessage  `json:"output"`
	Error          *changeErrorWire `json:"error"`
	CompensatedBy  *string          `json:"compensatedBy"`
	ParentChangeID *string          `json:"parentChangeId,omitempty"`
	Owner          *string          `json:"owner,omitempty"`
	RepairedBy     *string          `json:"repairedBy,omitempty"`
	RepairOutcome  *string          `json:"repairOutcome,omitempty"`
	Override       json.RawMessage  `json:"override,omitempty"`
}

type changeErrorWire struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Cause   string `json:"cause,omitempty"`
}

// changePoll is how often the CLI reads a change it waits for (spec 1.6:
// every second). It is a variable so tests can shorten it.
var changePoll = time.Second

// waitChange reads change ch again every changePoll while its state is one
// of busy, and returns its last view and JSON.
func waitChange(ctx context.Context, c *client, ch changeWire, raw json.RawMessage, busy ...string) (changeWire, json.RawMessage, error) {
	for slices.Contains(busy, ch.State) {
		t := time.NewTimer(changePoll)
		select {
		case <-ctx.Done():
			t.Stop()
			return changeWire{}, nil, ctx.Err()
		case <-t.C:
		}
		var next json.RawMessage
		if err := c.do(ctx, http.MethodGet, "/api/v1/changes/"+url.PathEscape(ch.ID), nil, &next); err != nil {
			return changeWire{}, nil, err
		}
		if err := json.Unmarshal(next, &ch); err != nil {
			return changeWire{}, nil, fmt.Errorf("decode the change: %w", err)
		}
		raw = next
	}
	return ch, raw, nil
}

// addMutatingFlags adds --yes, --confirmation for an irreversible action or
// --confirm for a revertible one, and --override for an action with Guards.
func addMutatingFlags(cmd *cobra.Command, spec core.ActionSpec) {
	cmd.Flags().Bool("yes", false, "apply without asking Apply? [y/N]; an irreversible action still needs its confirmation phrase")
	switch spec.Class {
	case core.ClassIrreversible:
		cmd.Flags().String("confirmation", "", "the confirmation phrase the plan names, instead of typing it")
	case core.ClassRevertible:
		cmd.Flags().Bool("confirm", false, "confirm the change at once instead of leaving it pending until its revert deadline")
	}
	if len(spec.Guards) == 0 {
		return
	}
	var ids []string
	for _, g := range spec.Guards {
		if g.Overridable {
			ids = append(ids, g.ID)
		}
	}
	usage := "plan again with this failing guard overridden (repeatable)"
	if len(ids) > 0 {
		usage += "; overridable: " + strings.Join(ids, ", ")
	}
	cmd.Flags().StringArray("override", nil, usage)
}

// withOverrides sets the input's overrides to the --override ids when the
// flag was given.
func withOverrides(cmd *cobra.Command, input json.RawMessage) (json.RawMessage, error) {
	if !cmd.Flags().Changed("override") {
		return input, nil
	}
	ids, err := cmd.Flags().GetStringArray("override")
	if err != nil {
		return nil, err
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(input, &obj); err != nil {
		return nil, fmt.Errorf("cli: read the input: %w", err)
	}
	if obj["overrides"], err = json.Marshal(ids); err != nil {
		return nil, err
	}
	return json.Marshal(obj)
}

// runMutating runs the mutating flow of one action (spec section 15), on
// the agent socket with --local.
func (a *app) runMutating(cmd *cobra.Command, def core.ActionDef, fields []inputField, secrets []string) error {
	local, err := useLocal(cmd)
	if err != nil {
		return err
	}
	input, err := collectInput(cmd, fields, a.term)
	if err != nil {
		return err
	}
	if input, err = withOverrides(cmd, input); err != nil {
		return err
	}
	if local {
		return a.localMutating(cmd, def, input, secrets)
	}
	c, err := a.connect(cmd)
	if err != nil {
		return err
	}
	host, err := hostFor(cmd, c, def.Spec.Side)
	if err != nil {
		return err
	}
	return a.applyAndShow(cmd, apiCalls(c, def, host), input, secrets)
}

// applyAndShow runs the mutating flow over calls and shows the change: with
// --json its raw JSON on standard output, else its state and output for
// people.
func (a *app) applyAndShow(cmd *cobra.Command, calls actionCalls, input any, secrets []string) error {
	ch, raw, err := a.planAndApply(cmd, calls, input, false)
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	if asJSON, _ := cmd.Flags().GetBool("json"); asJSON {
		if _, err := fmt.Fprintf(out, "%s\n", raw); err != nil {
			return err
		}
	} else if err := printChange(out, ch, secrets); err != nil {
		return err
	}
	return changeOutcome(ch)
}

// actionCalls are the plan and apply of one action, on the controller API
// or on the agent socket. apply returns the change and its JSON.
type actionCalls struct {
	plan  func(ctx context.Context, input any) (planWire, error)
	apply func(ctx context.Context, planID, confirmation string, confirmNow bool) (changeWire, json.RawMessage, error)
}

// apiCalls are def's plan and apply on the API c reaches, under host for an
// agent-side action.
func apiCalls(c *client, def core.ActionDef, host string) actionCalls {
	return actionCalls{
		plan: func(ctx context.Context, input any) (planWire, error) {
			var plan planWire
			err := c.do(ctx, http.MethodPost, actionPath(def, host, "plan"), map[string]any{"input": input}, &plan)
			return plan, err
		},
		apply: func(ctx context.Context, planID, confirmation string, confirmNow bool) (changeWire, json.RawMessage, error) {
			body := map[string]any{"planId": planID}
			if confirmation != "" {
				body["confirmation"] = confirmation
			}
			if confirmNow {
				body["confirmNow"] = true
			}
			var raw json.RawMessage
			if err := c.do(ctx, http.MethodPost, actionPath(def, host, "apply"), body, &raw); err != nil {
				return changeWire{}, nil, err
			}
			var ch changeWire
			if err := json.Unmarshal(raw, &ch); err != nil {
				return changeWire{}, nil, fmt.Errorf("decode the change: %w", err)
			}
			// An apply that answered 202 is still applying (spec 1.3).
			return waitChange(ctx, c, ch, raw, core.StateApplying)
		},
	}
}

// planAndApply plans with input, shows the plan, asks, and applies. A
// failing guard, a declined question or a wrong phrase refuses before
// anything is applied. The plan goes to standard output, or to standard
// error with --json. agreed skips the Apply? [y/N] question, as --yes
// does, for a step of a flow the operator already agreed to; it never
// skips the confirmation phrase.
func (a *app) planAndApply(cmd *cobra.Command, calls actionCalls, input any, agreed bool) (changeWire, json.RawMessage, error) {
	ctx := cmd.Context()
	info := cmd.OutOrStdout()
	if asJSON, _ := cmd.Flags().GetBool("json"); asJSON {
		info = cmd.ErrOrStderr()
	}
	plan, err := calls.plan(ctx, input)
	if err != nil {
		return changeWire{}, nil, err
	}
	printPlan(info, plan)
	for _, g := range plan.Guards {
		if !g.OK {
			if g.Overridable {
				_, _ = fmt.Fprintf(info, "override it with --override %s\n", sanitize(g.ID))
			}
			return changeWire{}, nil, refusedf("guard %s failed: %s; nothing was applied", g.ID, g.Message)
		}
	}
	if err := a.askApply(cmd, agreed); err != nil {
		return changeWire{}, nil, err
	}
	var phrase string
	if plan.Confirmation != nil && *plan.Confirmation != "" {
		if phrase, err = a.confirmation(cmd, *plan.Confirmation); err != nil {
			return changeWire{}, nil, err
		}
	}
	now, _ := cmd.Flags().GetBool("confirm")
	return calls.apply(ctx, plan.PlanID, phrase, now)
}

// askApply asks Apply? [y/N] unless --yes or agreed; without a terminal it
// refuses, because only --yes can answer.
func (a *app) askApply(cmd *cobra.Command, agreed bool) error {
	if yes, _ := cmd.Flags().GetBool("yes"); yes || agreed {
		return nil
	}
	if !a.term.isTerm {
		return refusedf("standard input is not a terminal; pass --yes to apply")
	}
	answer, err := a.term.line("Apply? [y/N] ")
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	if ans := strings.ToLower(strings.TrimSpace(answer)); ans != "y" && ans != "yes" {
		return refusedf("not applied")
	}
	return nil
}

// confirmation returns the phrase an irreversible plan needs: from
// --confirmation, else typed on the terminal. A phrase that differs is
// refused here, before the apply.
func (a *app) confirmation(cmd *cobra.Command, want string) (string, error) {
	given, _ := cmd.Flags().GetString("confirmation")
	if given == "" {
		if !a.term.isTerm {
			return "", refusedf("this change cannot be undone: pass --confirmation %s, or run on a terminal", want)
		}
		var err error
		given, err = a.term.line(fmt.Sprintf("This cannot be undone. Type %s to confirm: ", sanitize(want)))
		if err != nil && !errors.Is(err, io.EOF) {
			return "", err
		}
	}
	if given != want {
		return "", refusedf("the confirmation does not match %q; nothing was applied", want)
	}
	return given, nil
}

// printPlan writes the plan's diff and guards, sanitized.
func printPlan(w io.Writer, p planWire) {
	target := fmt.Sprintf("%s/v%d", p.Action, p.Version)
	if p.HostID != nil {
		target += " on host " + *p.HostID
	}
	_, _ = fmt.Fprintf(w, "Plan for %s:\n", sanitize(target))
	if len(p.Diff) == 0 {
		_, _ = fmt.Fprintln(w, "  (no changes)")
	}
	for _, d := range p.Diff {
		_, _ = fmt.Fprintf(w, "  %s %s\n", sanitize(d.Op), sanitize(d.Text))
	}
	if len(p.Guards) == 0 {
		return
	}
	_, _ = fmt.Fprintln(w, "Guards:")
	for _, g := range p.Guards {
		status := "ok    "
		if !g.OK {
			status = "FAILED"
		}
		_, _ = fmt.Fprintf(w, "  %s  %s: %s\n", status, sanitize(g.ID), sanitize(g.Message))
	}
}

// printChange writes a change's state, how to confirm a pending one, who
// owns it, how it was repaired, how to repair one that needs it, and its
// output with the note on secret outputs.
func printChange(w io.Writer, ch changeWire, secrets []string) error {
	if _, err := fmt.Fprintf(w, "Change %s: %s\n", sanitize(ch.ID), sanitize(ch.State)); err != nil {
		return err
	}
	if ch.State == core.StatePending && ch.RevertDeadline != nil {
		if _, err := fmt.Fprintf(w, "Confirm it before %s with: darkpanel change confirm %s\nUnless confirmed by then, it is reverted.\n",
			ch.RevertDeadline.UTC().Format(time.RFC3339), sanitize(ch.ID)); err != nil {
			return err
		}
	}
	if ch.Owner != nil {
		if _, err := fmt.Fprintf(w, "Owner: %s\n", sanitize(*ch.Owner)); err != nil {
			return err
		}
	}
	if ch.RepairedBy != nil {
		outcome := ""
		if ch.RepairOutcome != nil {
			outcome = " (" + *ch.RepairOutcome + ")"
		}
		if _, err := fmt.Fprintf(w, "Repaired by change %s%s\n", sanitize(*ch.RepairedBy), sanitize(outcome)); err != nil {
			return err
		}
	}
	if ch.State == core.StateNeedsRepair {
		if ch.Error != nil {
			cause := ""
			if ch.Error.Cause != "" {
				cause = ", caused by " + ch.Error.Cause
			}
			if _, err := fmt.Fprintf(w, "Needs repair: %s (%s%s)\n",
				sanitize(ch.Error.Message), sanitize(ch.Error.Code), sanitize(cause)); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintf(w, "Repair it with: darkpanel change repair %s\nOr give it up with: darkpanel change release %s --reason <why>\n",
			sanitize(ch.ID), sanitize(ch.ID)); err != nil {
			return err
		}
	}
	if len(ch.Output) == 0 || string(ch.Output) == "null" {
		return nil
	}
	if err := render(w, ch.Output); err != nil {
		return err
	}
	noteSecrets(w, secrets, ch.Output)
	return nil
}

// changeOutcome is the error of a change that did not end well: failed,
// reverted or interrupted. Apply answers such changes with status 200
// (spec section 12), so the CLI decides the exit code from the state.
func changeOutcome(ch changeWire) error {
	switch ch.State {
	case core.StateFailed, core.StateReverted, core.StateInterrupted:
	default:
		return nil
	}
	if ch.Error != nil {
		return fmt.Errorf("change %s %s: %s (%s)", ch.ID, ch.State, ch.Error.Message, ch.Error.Code)
	}
	return fmt.Errorf("change %s %s", ch.ID, ch.State)
}
