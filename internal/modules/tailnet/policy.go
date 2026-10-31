package tailnet

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/tailnet/policy"
)

func (t tn) policyGet() core.ActionDef {
	return core.DefineRead[PolicyGetInput, Policy](core.ActionSpec{
		Name: ID + ".policy.get", Summary: "Read the tailnet policy file with its comments.", Version: 1, Side: core.SideController,
	}, policyGetHandler{t: t})
}

// policyGetHandler answers the policy file exactly as the provider sent it.
type policyGetHandler struct{ t tn }

func (h policyGetHandler) Read(ctx context.Context, env core.Env, _ PolicyGetInput) (Policy, error) {
	a, _, err := h.t.connected(ctx, env.DB, env.Sealer, env.Clock)
	if err != nil {
		return Policy{}, err
	}
	text, etag, err := a.Policy(ctx)
	if err != nil {
		return Policy{}, err
	}
	return Policy{Text: string(text), ETag: etag}, nil
}

func (t tn) policySet() core.ActionDef {
	return core.DefineMutating[PolicySetInput, PolicyWritten](core.ActionSpec{
		Name: ID + ".policy.set", Summary: "Replace the tailnet policy file.", Version: 1,
		Class: core.ClassCompensable, Side: core.SideController, LockKeys: []string{ID},
	}, policySetHandler{policyHandler{t: t}})
}

// policySetHandler replaces the whole policy file; its undo posts the text
// it replaced back.
type policySetHandler struct{ policyHandler }

// checkPolicyText refuses text that is empty or not HuJSON before any
// provider request.
func checkPolicyText(text string) ([]byte, error) {
	if strings.TrimSpace(text) == "" {
		return nil, errcode.New(errcode.TailnetPolicyInvalid, "the policy file is empty")
	}
	if _, err := policy.Canonical([]byte(text)); err != nil {
		return nil, errcode.New(errcode.TailnetPolicyInvalid, err.Error())
	}
	return []byte(text), nil
}

func (h policySetHandler) Plan(ctx context.Context, env core.Env, in PolicySetInput) (core.PlanOutcome, error) {
	text, err := checkPolicyText(in.Text)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	a, acct, err := h.t.connected(ctx, env.DB, env.Sealer, env.Clock)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	before, etag, err := a.Policy(ctx)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	if err := a.ValidatePolicy(ctx, text); err != nil {
		return core.PlanOutcome{}, err
	}
	return core.PlanOutcome{Diff: policy.Diff(before, text), StateHash: accountHash(acct.ID, etag, in.Text)}, nil
}

func (h policySetHandler) Apply(ctx context.Context, env core.Env, in PolicySetInput) (PolicyWritten, error) {
	text, err := checkPolicyText(in.Text)
	if err != nil {
		return PolicyWritten{}, core.Definite(err)
	}
	a, acct, err := h.t.connected(ctx, env.DB, env.Sealer, env.Clock)
	if err != nil {
		return PolicyWritten{}, core.Definite(err)
	}
	before, etag, err := a.Policy(ctx)
	if err != nil {
		return PolicyWritten{}, err
	}
	// Texts that differ only in comments or spacing are still written: the
	// comments are part of the file the operator keeps.
	if bytes.Equal(before, text) {
		return PolicyWritten{ETag: etag}, nil
	}
	written, err := h.t.writePolicy(ctx, env, a, acct, before, etag, text)
	if err != nil {
		return PolicyWritten{}, err
	}
	return PolicyWritten{ETag: written}, nil
}

// Verify checks that the policy holds the content written. The ETag is not
// compared, so a later edit of comments alone does not fail the change.
func (h policySetHandler) Verify(ctx context.Context, env core.Env, in PolicySetInput, _ PolicyWritten) error {
	a, _, err := h.t.connected(ctx, env.DB, env.Sealer, env.Clock)
	if err != nil {
		return err
	}
	current, _, err := a.Policy(ctx)
	if err != nil {
		return err
	}
	got, err := policy.Canonical(current)
	if err != nil {
		return fmt.Errorf("tailnet: read the policy file: %w", err)
	}
	want, err := policy.Canonical([]byte(in.Text))
	if err != nil {
		return fmt.Errorf("tailnet: read the policy written: %w", err)
	}
	if !bytes.Equal(got, want) {
		return fmt.Errorf("tailnet: the policy file does not hold the policy written")
	}
	return nil
}
