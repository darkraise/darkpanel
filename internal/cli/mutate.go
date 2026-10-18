package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
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

// changeWire is the part of the API's Change the CLI reads.
type changeWire struct {
	ID             string          `json:"id"`
	HostID         *string         `json:"hostId"`
	Action         string          `json:"action"`
	Version        int             `json:"version"`
	Class          core.Class      `json:"class"`
	State          string          `json:"state"`
	RevertDeadline *time.Time      `json:"revertDeadline"`
	Output         json.RawMessage `json:"output"`
	Error          *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
	CompensatedBy *string `json:"compensatedBy"`
}

// addMutatingFlags adds --yes, and --confirmation for an irreversible
// action or --confirm for a revertible one.
func addMutatingFlags(cmd *cobra.Command, class core.Class) {
	cmd.Flags().Bool("yes", false, "apply without asking Apply? [y/N]; an irreversible action still needs its confirmation phrase")
	switch class {
	case core.ClassIrreversible:
		cmd.Flags().String("confirmation", "", "the confirmation phrase the plan names, instead of typing it")
	case core.ClassRevertible:
		cmd.Flags().Bool("confirm", false, "confirm the change at once instead of leaving it pending until its revert deadline")
	}
}

// runMutating runs the mutating flow of one action (spec section 15) and
// shows the change: with --json its raw JSON on standard output, else its
// state and output for people.
func (a *app) runMutating(cmd *cobra.Command, def core.ActionDef, fields []inputField, secrets []string) error {
	input, err := collectInput(cmd, fields, a.term)
	if err != nil {
		return err
	}
	c, err := a.connect(cmd)
	if err != nil {
		return err
	}
	host, err := hostFor(cmd, c, def.Spec.Side)
	if err != nil {
		return err
	}
	ch, raw, err := a.planAndApply(cmd, c, def, host, input, false)
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

// planAndApply plans def with input, shows the plan, asks, and applies. A
// failing guard, a declined question or a wrong phrase refuses before
// anything is applied. The plan goes to standard output, or to standard
// error with --json. agreed skips the Apply? [y/N] question, as --yes
// does, for a step of a flow the operator already agreed to; it never
// skips the confirmation phrase.
func (a *app) planAndApply(cmd *cobra.Command, c *client, def core.ActionDef, host string, input any,
	agreed bool) (changeWire, json.RawMessage, error) {
	ctx := cmd.Context()
	info := cmd.OutOrStdout()
	if asJSON, _ := cmd.Flags().GetBool("json"); asJSON {
		info = cmd.ErrOrStderr()
	}
	var plan planWire
	if err := c.do(ctx, http.MethodPost, actionPath(def, host, "plan"), map[string]any{"input": input}, &plan); err != nil {
		return changeWire{}, nil, err
	}
	printPlan(info, plan)
	for _, g := range plan.Guards {
		if !g.OK {
			return changeWire{}, nil, refusedf("guard %s failed: %s; nothing was applied", g.ID, g.Message)
		}
	}
	if yes, _ := cmd.Flags().GetBool("yes"); !yes && !agreed {
		if !a.term.isTerm {
			return changeWire{}, nil, refusedf("standard input is not a terminal; pass --yes to apply")
		}
		answer, err := a.term.line("Apply? [y/N] ")
		if err != nil && !errors.Is(err, io.EOF) {
			return changeWire{}, nil, err
		}
		if ans := strings.ToLower(strings.TrimSpace(answer)); ans != "y" && ans != "yes" {
			return changeWire{}, nil, refusedf("not applied")
		}
	}
	body := map[string]any{"planId": plan.PlanID}
	if plan.Confirmation != nil && *plan.Confirmation != "" {
		phrase, err := a.confirmation(cmd, *plan.Confirmation)
		if err != nil {
			return changeWire{}, nil, err
		}
		body["confirmation"] = phrase
	}
	if now, _ := cmd.Flags().GetBool("confirm"); now {
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
	return ch, raw, nil
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

// printChange writes a change's state, how to confirm a pending one, and
// its output with the note on secret outputs.
func printChange(w io.Writer, ch changeWire, secrets []string) error {
	_, _ = fmt.Fprintf(w, "Change %s: %s\n", sanitize(ch.ID), sanitize(ch.State))
	if ch.State == core.StatePending && ch.RevertDeadline != nil {
		_, _ = fmt.Fprintf(w, "Confirm it before %s with: darkpanel change confirm %s\n",
			ch.RevertDeadline.UTC().Format(time.RFC3339), sanitize(ch.ID))
		_, _ = fmt.Fprintln(w, "Unless confirmed by then, it is reverted.")
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
