package tailnet

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules/tailnet/policy"
)

func (t tn) policyPatch() core.ActionDef {
	return core.DefineMutating[PolicyPatchInput, PolicyWritten](core.ActionSpec{
		Name: ID + ".policy.patch", Summary: "Edit the tailnet policy file with typed, convergent edits.", Version: 1,
		Class: core.ClassCompensable, Side: core.SideController, LockKeys: []string{ID}, Converges: true,
	}, policyPatchHandler{policyHandler{t: t}})
}

// policyPatchHandler applies typed edits to the policy file; its undo posts
// the text it replaced back.
type policyPatchHandler struct{ policyHandler }

// patchedPolicy is the live policy and the text the edits make of it.
type patchedPolicy struct {
	a       *api
	acct    account
	current []byte
	etag    string
	patched []byte
	canon   []byte // policy.Canonical of patched
	changed bool   // the edits change the policy's content
}

// read applies the edits to the policy as the provider holds it now. An edit
// the policy package refuses fails before any provider write or validation.
func (h policyPatchHandler) read(ctx context.Context, env core.Env, in PolicyPatchInput) (patchedPolicy, error) {
	a, acct, err := h.t.connected(ctx, env.DB, env.Sealer, env.Clock)
	if err != nil {
		return patchedPolicy{}, err
	}
	current, etag, err := a.Policy(ctx)
	if err != nil {
		return patchedPolicy{}, err
	}
	patched, err := policy.Apply(current, in.Edits)
	if err != nil {
		return patchedPolicy{}, err
	}
	canon, err := policy.Canonical(patched)
	if err != nil {
		return patchedPolicy{}, fmt.Errorf("tailnet: read the patched policy file: %w", err)
	}
	was, err := policy.Canonical(current)
	if err != nil {
		return patchedPolicy{}, fmt.Errorf("tailnet: read the policy file: %w", err)
	}
	return patchedPolicy{
		a: a, acct: acct, current: current, etag: etag, patched: patched, canon: canon,
		changed: !bytes.Equal(canon, was),
	}, nil
}

func (h policyPatchHandler) Plan(ctx context.Context, env core.Env, in PolicyPatchInput) (core.PlanOutcome, error) {
	p, err := h.read(ctx, env, in)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	var diff []core.DiffLine
	if p.changed {
		if err := p.a.ValidatePolicy(ctx, p.patched); err != nil {
			return core.PlanOutcome{}, err
		}
		diff = policy.Diff(p.current, p.patched)
	}
	sum := sha256.New()
	sum.Write([]byte(p.etag))
	sum.Write([]byte{0})
	sum.Write(p.canon)
	return core.PlanOutcome{Diff: diff, StateHash: hex.EncodeToString(sum.Sum(nil))}, nil
}

// Apply patches the policy as it is now rather than as it was at plan, so an
// edit made elsewhere in between is kept; the If-Match on the ETag just read
// refuses one made during the call.
func (h policyPatchHandler) Apply(ctx context.Context, env core.Env, in PolicyPatchInput) (PolicyWritten, error) {
	p, err := h.read(ctx, env, in)
	if err != nil {
		return PolicyWritten{}, core.Definite(err)
	}
	if !p.changed {
		return PolicyWritten{ETag: p.etag}, nil
	}
	written, err := h.t.writePolicy(ctx, env, p.a, p.acct, p.current, p.etag, p.patched)
	if err != nil {
		return PolicyWritten{}, err
	}
	return PolicyWritten{ETag: written}, nil
}

// Verify checks that the live policy still holds the edits: applying them
// again leaves its content unchanged.
func (h policyPatchHandler) Verify(ctx context.Context, env core.Env, in PolicyPatchInput, _ PolicyWritten) error {
	a, _, err := h.t.connected(ctx, env.DB, env.Sealer, env.Clock)
	if err != nil {
		return err
	}
	current, _, err := a.Policy(ctx)
	if err != nil {
		return err
	}
	patched, err := policy.Apply(current, in.Edits)
	if err != nil {
		return fmt.Errorf("tailnet: the policy file no longer holds the patch's edits: %w", err)
	}
	got, err := policy.Canonical(current)
	if err != nil {
		return fmt.Errorf("tailnet: read the policy file: %w", err)
	}
	want, err := policy.Canonical(patched)
	if err != nil {
		return fmt.Errorf("tailnet: read the patched policy file: %w", err)
	}
	if !bytes.Equal(got, want) {
		return fmt.Errorf("tailnet: the policy file no longer holds the patch's edits")
	}
	return nil
}
