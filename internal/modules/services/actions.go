package services

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// UnitInput names the unit a mutating action changes.
type UnitInput struct {
	core.Guarded
	Unit string `json:"unit"`
}

// UnitChanged is the answer of a mutating action: the unit it acted on,
// under its own name when the input named an alias, and for start and
// restart the unit's InactiveExitTimestamp before the job, which Verify
// expects to advance.
type UnitChanged struct {
	Unit         string `json:"unit"`
	LeftInactive uint64 `json:"leftInactive,omitempty"` // microseconds
}

// verifyPoll is how often Verify reads the unit while a job runs.
const verifyPoll = 250 * time.Millisecond

func (m module) mutations() []core.ActionDef {
	guards := []core.GuardSpec{{ID: GuardNetworkStack}, {ID: GuardTailnetLink}, {ID: GuardModuleChange}}
	def := func(o op, summary string, spec core.ActionSpec) core.ActionDef {
		spec.Name, spec.Summary, spec.Version = ID+".unit."+string(o), summary, 1
		spec.Class, spec.Side, spec.LockKeys = core.ClassCompensable, core.SideAgent, []string{ID}
		spec.TypedConfirmation, spec.Guards = true, guards
		return core.DefineMutating[UnitInput, UnitChanged](spec, unitHandler{m: m, op: o})
	}
	return []core.ActionDef{
		def(opStart, "Start a service, socket or timer.", core.ActionSpec{CompensatedBy: ID + ".unit.stop"}),
		def(opStop, "Stop a service, socket or timer.", core.ActionSpec{CompensatedBy: ID + ".unit.start"}),
		def(opRestart, "Restart a service, socket or timer.", core.ActionSpec{ForwardOnly: true, Converges: true}),
		def(opReload, "Reload the configuration of a service, socket or timer.", core.ActionSpec{ForwardOnly: true, Converges: true}),
		def(opEnable, "Enable a service, socket or timer at boot.",
			core.ActionSpec{CompensatedBy: ID + ".unit.disable", MinScope: core.ScopeAdmin}),
		def(opDisable, "Disable a service, socket or timer at boot.",
			core.ActionSpec{CompensatedBy: ID + ".unit.enable", MinScope: core.ScopeAdmin}),
	}
}

// unitHandler is the handler of the action that does op.
type unitHandler struct {
	m  module
	op op
}

// check reads the unit and applies every rule: the refusals, the scope the
// unit asks for and the unit file state enable and disable need. It only
// reads, so its refusals are Definite.
func (h unitHandler) check(ctx context.Context, env core.Env, in UnitInput) (core.UnitInfo, demands, error) {
	if err := checkUnitName(in.Unit); err != nil {
		return core.UnitInfo{}, demands{}, err
	}
	u, err := inspect(ctx, env, in.Unit)
	if err != nil {
		return core.UnitInfo{}, demands{}, err
	}
	if err := refusal(ctx, u, h.op, func(ctx context.Context, name string) (core.UnitInfo, error) {
		return inspect(ctx, env, name)
	}); err != nil {
		return core.UnitInfo{}, demands{}, err
	}
	d := demandsOf(u, h.op)
	if h.op.starts() && (unitType(u.ID) == "timer" || unitType(u.ID) == "socket") {
		// Starting a timer or socket starts what it triggers, so it asks
		// what starting those asks.
		for _, name := range u.Triggers {
			t, err := inspect(ctx, env, name)
			if err != nil {
				return core.UnitInfo{}, demands{}, err
			}
			td := demandsOf(t, opStart)
			d.admin, d.phrase = d.admin || td.admin, d.phrase || td.phrase
			if d.module == "" {
				d.module = td.module
			}
		}
	}
	admin, err := unitAdmin(env)
	if err != nil {
		return core.UnitInfo{}, demands{}, err
	}
	if d.pulled, err = pulledUnits(ctx, admin, u, h.op); err != nil {
		return core.UnitInfo{}, demands{}, err
	}
	if err := pulledRefusal(d.pulled); err != nil {
		return core.UnitInfo{}, demands{}, err
	}
	if d.admin {
		if err := core.RequireScope(env, core.ScopeAdmin); err != nil {
			return core.UnitInfo{}, demands{}, core.Definite(err)
		}
	}
	for _, p := range d.pulled {
		if p.need.admin && core.RequireScope(env, core.ScopeAdmin) != nil {
			return core.UnitInfo{}, demands{}, core.Definite(errcode.New(errcode.Forbidden, p.text+", which needs admin"))
		}
		d.admin, d.phrase = d.admin || p.need.admin, d.phrase || p.need.phrase
		if d.module == "" {
			d.module = p.need.module
		}
	}
	if (h.op == opEnable || h.op == opDisable) && u.UnitFileState != "enabled" && u.UnitFileState != "disabled" {
		state := u.UnitFileState
		if state == "" {
			state = "without a unit file"
		}
		return core.UnitInfo{}, demands{}, invalid("/unit", fmt.Sprintf("%s is %s; only an enabled or disabled unit can be %sd", u.ID, state, h.op))
	}
	if why := h.noChange(u); why != "" {
		return core.UnitInfo{}, demands{}, invalid("/unit", why)
	}
	return u, d, nil
}

// noChange is why op would leave u as it is, or "". Such a change is
// refused: its compensation would then change the unit, such as stopping a
// service that ran before the start.
func (h unitHandler) noChange(u core.UnitInfo) string {
	switch {
	case h.op == opStart && slices.Contains([]string{"active", "activating", "reloading"}, u.ActiveState):
		return u.ID + " is already " + u.ActiveState
	case h.op == opStop && (u.ActiveState == "inactive" || u.ActiveState == "failed"):
		return u.ID + " is already " + u.ActiveState
	case h.op == opEnable && u.UnitFileState == "enabled", h.op == opDisable && u.UnitFileState == "disabled":
		return u.ID + " is already " + u.UnitFileState
	}
	return ""
}

// Plan reports what op does to the unit, the guards it trips and, for a
// unit that asks for it, the unit name as the phrase apply repeats.
func (h unitHandler) Plan(ctx context.Context, env core.Env, in UnitInput) (core.PlanOutcome, error) {
	u, d, err := h.check(ctx, env, in)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	guards, notes, err := h.m.guards(ctx, env, u, h.op, d)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	diff := []core.DiffLine{h.line(u)}
	for _, p := range d.pulled {
		if p.need.admin {
			diff = append(diff, core.DiffLine{Op: " ", Text: p.text})
		}
	}
	diff = append(diff, notes...)
	sum := sha256.Sum256([]byte(strings.Join([]string{string(h.op), u.ID, u.LoadState, u.ActiveState, u.UnitFileState}, "\n")))
	out := core.PlanOutcome{Diff: diff, Guards: guards, StateHash: hex.EncodeToString(sum[:])}
	if d.phrase {
		out.Confirmation = u.ID
	}
	return out, nil
}

// line is the diff line of op on u.
func (h unitHandler) line(u core.UnitInfo) core.DiffLine {
	switch h.op {
	case opStart:
		return core.DiffLine{Op: "+", Text: fmt.Sprintf("start %s (now %s)", u.ID, u.ActiveState)}
	case opStop:
		return core.DiffLine{Op: "-", Text: fmt.Sprintf("stop %s (now %s)", u.ID, u.ActiveState)}
	case opEnable:
		return core.DiffLine{Op: "+", Text: fmt.Sprintf("enable %s at boot (now %s)", u.ID, u.UnitFileState)}
	case opDisable:
		return core.DiffLine{Op: "-", Text: fmt.Sprintf("disable %s at boot (now %s)", u.ID, u.UnitFileState)}
	}
	return core.DiffLine{Op: " ", Text: fmt.Sprintf("%s %s (now %s)", h.op, u.ID, u.ActiveState)}
}

// Apply checks the unit again and queues op's job, or changes its unit
// file links and reloads systemd.
func (h unitHandler) Apply(ctx context.Context, env core.Env, in UnitInput) (UnitChanged, error) {
	u, _, err := h.check(ctx, env, in)
	if err != nil {
		return UnitChanged{}, err
	}
	admin, err := unitAdmin(env)
	if err != nil {
		return UnitChanged{}, err
	}
	switch h.op {
	case opStart:
		err = admin.Start(ctx, u.ID)
	case opStop:
		err = admin.Stop(ctx, u.ID)
	case opRestart:
		err = admin.Restart(ctx, u.ID)
	case opReload:
		err = admin.ReloadUnit(ctx, u.ID)
	case opEnable:
		if err = admin.Enable(ctx, u.ID); err == nil {
			err = admin.Reload(ctx)
		}
	case opDisable:
		if err = admin.Disable(ctx, u.ID); err == nil {
			err = admin.Reload(ctx)
		}
	}
	if err != nil {
		return UnitChanged{}, err
	}
	return UnitChanged{Unit: u.ID, LeftInactive: u.LeftInactive}, nil
}

// Verify waits for the unit's job to finish and checks the state it left:
// started units run, or ran and ended without failing, stopped units are
// inactive or failed, and unit files are enabled or disabled as asked.
func (h unitHandler) Verify(ctx context.Context, env core.Env, in UnitInput, out UnitChanged) error {
	admin, err := unitAdmin(env)
	if err != nil {
		return err
	}
	u, err := admin.Inspect(ctx, in.Unit)
	if err != nil {
		return err
	}
	for {
		if h.op != opStop {
			if u, err = admin.Inspect(ctx, u.ID); err != nil {
				return err
			}
		}
		st, err := admin.State(ctx, u.ID)
		if err != nil {
			return err
		}
		done, problem := h.settled(u, st, out.LeftInactive)
		if done && problem == "" {
			return nil
		}
		if done {
			return errcode.New(errcode.Internal, problem)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("services: %s of %s did not finish: the unit is %s with job %q: %w", h.op, u.ID, st.ActiveState, st.PendingJob, ctx.Err())
		case <-time.After(verifyPoll):
		}
	}
}

// settled reports whether op's effect can be judged yet, and what is wrong
// when it can. A start or restart that left the unit inactive counts only
// when the unit left inactive after before: a oneshot that ran and ended,
// not a job that never started it (a failed dependency, a cancel). A
// Verify after recovery has no before and accepts any such exit.
func (h unitHandler) settled(u core.UnitInfo, st core.UnitState, before uint64) (bool, string) {
	switch h.op {
	case opEnable:
		return true, wrongFileState(u, "enabled", "enabled-runtime")
	case opDisable:
		return true, wrongFileState(u, "disabled")
	}
	if st.PendingJob != "" {
		return false, ""
	}
	if h.op == opStop {
		if st.ActiveState != "inactive" && st.ActiveState != "failed" {
			return true, fmt.Sprintf("%s is %s after the stop", u.ID, st.ActiveState)
		}
		return true, ""
	}
	if st.ActiveState == "failed" {
		return true, fmt.Sprintf("%s failed after the %s; its journal says why", u.ID, h.op)
	}
	if h.op.starts() && st.ActiveState == "inactive" && (u.LeftInactive == 0 || u.LeftInactive <= before) {
		return true, fmt.Sprintf("the %s job ended without starting %s; its journal says why", h.op, u.ID)
	}
	return true, ""
}

func wrongFileState(u core.UnitInfo, want ...string) string {
	if slices.Contains(want, u.UnitFileState) {
		return ""
	}
	return fmt.Sprintf("%s is %s, want %s", u.ID, u.UnitFileState, want[0])
}
