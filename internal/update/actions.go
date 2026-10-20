package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// The core module's agent-side update actions (spec section 8). ADR 0003
// freezes their names for protocol 1.
const (
	ActionApply    = "core.update.apply"
	ActionRollback = "core.update.rollback"
	ActionStatus   = "core.update.status"
)

// Actions returns core.update.apply/v1, core.update.rollback/v1 and
// core.update.status/v1 over u, for coreactions.Deps.Extra. Every process
// registers the one core manifest, but only the agent runs updates: the
// controller process and the embedded agent pass nil, and every handler then
// fails with unavailable, which the rollout driver treats as a failed host
// rather than one to wait for.
func Actions(u *Updater) []core.ActionDef {
	a := updateActions{u: u}
	return []core.ActionDef{
		core.DefineMutating[core.UpdateApplyInput, core.UpdateTransaction](core.ActionSpec{
			Name: ActionApply, Version: 1, Class: core.ClassCompensable, Side: core.SideAgent,
			CompensatedBy: ActionRollback,
			Summary: "Update this host to a signed release; the change completes before the services restart, " +
				"and core.update.status follows the update.",
		}, applyAction{a}),
		core.DefineMutating[core.UpdateRollbackInput, core.UpdateTransaction](core.ActionSpec{
			Name: ActionRollback, Version: 1, Class: core.ClassIrreversible, Side: core.SideAgent,
			Summary: "Roll this host back to the binary its last update replaced; the confirmation phrase is " +
				"that binary's version.",
		}, rollbackAction{a}),
		core.DefineRead[core.UpdateStatusInput, core.UpdateStatus](core.ActionSpec{
			Name: ActionStatus, Version: 1, Side: core.SideAgent,
			Summary: "List this host's update and rollback transactions, newest first.",
		}, statusAction{a}),
	}
}

type updateActions struct{ u *Updater }

func (a updateActions) updater() (*Updater, error) {
	if a.u == nil {
		return nil, errcode.New(errcode.Unavailable, "the update actions run only in darkpanel agent run")
	}
	return a.u, nil
}

// actionStateHash is a plan's state hash over the parts that decide what
// an apply would do.
func actionStateHash(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}

// verifyStarted checks that the record a change's Apply wrote exists and has
// not failed: the record id from the change's output, or, for the Verify
// that startup recovery runs without an output, the oldest record of the
// operation changeID started. A failed record's error code becomes the
// change's.
func (u *Updater) verifyStarted(ctx context.Context, changeID, id string) error {
	var tr core.UpdateTransaction
	switch {
	case id != "":
		var err error
		if tr, err = u.Transaction(ctx, id); err != nil {
			return err
		}
	case changeID == "":
		return errors.New("update: verify without a change or a transaction")
	default:
		st, err := u.Status(ctx, core.UpdateStatusInput{ChangeID: changeID})
		if err != nil {
			return err
		}
		if len(st.Transactions) == 0 {
			return fmt.Errorf("update: change %s wrote no update record", changeID)
		}
		tr = st.Transactions[len(st.Transactions)-1]
	}
	if tr.State != core.UpdateFailed {
		return nil
	}
	if tr.Error != nil && errcode.Known(errcode.Code(tr.Error.Code)) {
		return errcode.New(errcode.Code(tr.Error.Code), tr.Error.Message)
	}
	return fmt.Errorf("update: transaction %s failed", tr.ID)
}

type applyAction struct{ updateActions }

func (h applyAction) Plan(ctx context.Context, _ core.Env, in core.UpdateApplyInput) (core.PlanOutcome, error) {
	u, err := h.updater()
	if err != nil {
		return core.PlanOutcome{}, err
	}
	steps, err := u.CheckApply(ctx, in)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	versions := make([]string, len(steps))
	hashed := []string{u.cfg.Version}
	for i, s := range steps {
		versions[i] = s.Version
		hashed = append(hashed, s.Version+"@"+s.Channel)
	}
	diff := []core.DiffLine{
		{Op: "-", Text: "darkpanel " + u.cfg.Version},
		{Op: "+", Text: fmt.Sprintf("darkpanel %s (%s)", in.Version, in.Channel)},
	}
	if len(steps) > 1 {
		diff = append(diff, core.DiffLine{Op: " ", Text: "installs " + strings.Join(versions, ", then ") +
			", one minor version at a time"})
	}
	diff = append(diff, core.DiffLine{Op: " ", Text: "the services restart after this change completes; " +
		"a failed health check rolls the host back automatically"})
	return core.PlanOutcome{Diff: diff, Guards: []core.Guard{}, StateHash: actionStateHash(hashed...)}, nil
}

func (h applyAction) Apply(ctx context.Context, env core.Env, in core.UpdateApplyInput) (core.UpdateTransaction, error) {
	u, err := h.updater()
	if err != nil {
		return core.UpdateTransaction{}, err
	}
	return u.StartUpdate(ctx, env.ChangeID, in)
}

func (h applyAction) Verify(ctx context.Context, env core.Env, _ core.UpdateApplyInput, out core.UpdateTransaction) error {
	u, err := h.updater()
	if err != nil {
		return err
	}
	return u.verifyStarted(ctx, env.ChangeID, out.ID)
}

type rollbackAction struct{ updateActions }

func (h rollbackAction) Plan(ctx context.Context, _ core.Env, in core.UpdateRollbackInput) (core.PlanOutcome, error) {
	u, err := h.updater()
	if err != nil {
		return core.PlanOutcome{}, err
	}
	last, err := u.rollbackTarget(ctx, in.Force)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	return core.PlanOutcome{
		Diff: []core.DiffLine{
			{Op: "-", Text: "darkpanel " + last.ToVersion},
			{Op: "+", Text: fmt.Sprintf("darkpanel %s, the binary update %s replaced", last.FromVersion, last.OperationID)},
			{Op: " ", Text: "the services restart; core.update.status shows the rollback's result"},
		},
		Guards:       []core.Guard{},
		StateHash:    actionStateHash(last.ID, last.State, last.PrevSHA256),
		Confirmation: last.FromVersion,
	}, nil
}

func (h rollbackAction) Apply(ctx context.Context, env core.Env, in core.UpdateRollbackInput) (core.UpdateTransaction, error) {
	u, err := h.updater()
	if err != nil {
		return core.UpdateTransaction{}, err
	}
	return u.StartRollback(ctx, env.ChangeID, in)
}

func (h rollbackAction) Verify(ctx context.Context, env core.Env, _ core.UpdateRollbackInput, out core.UpdateTransaction) error {
	u, err := h.updater()
	if err != nil {
		return err
	}
	return u.verifyStarted(ctx, env.ChangeID, out.ID)
}

type statusAction struct{ updateActions }

func (h statusAction) Read(ctx context.Context, _ core.Env, in core.UpdateStatusInput) (core.UpdateStatus, error) {
	u, err := h.updater()
	if err != nil {
		return core.UpdateStatus{}, err
	}
	return u.Status(ctx, in)
}
