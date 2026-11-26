package lifecycle

import (
	"context"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// phrModule has a compensable action that asks for its phrase only when its
// input says so, as services.unit.restart does for a network unit.
type phrModule struct{}

type phrInput struct {
	Ask bool `json:"ask"`
}

type phrOutput struct{}

type phrHandler struct{}

func (phrHandler) Apply(context.Context, core.Env, phrInput) (phrOutput, error) {
	return phrOutput{}, nil
}

func (phrHandler) Verify(context.Context, core.Env, phrInput, phrOutput) error { return nil }

func (phrHandler) Plan(_ context.Context, _ core.Env, in phrInput) (core.PlanOutcome, error) {
	out := core.PlanOutcome{StateHash: "x"}
	if in.Ask {
		out.Confirmation = "restart it"
	}
	return out, nil
}

var refPhrase = core.ActionRef{Name: "phr.unit.restart", Version: 1}

func (phrModule) Manifest() core.Manifest {
	return core.Manifest{ID: "phr", Version: 1, Actions: []core.ActionDef{
		core.DefineMutating[phrInput, phrOutput](core.ActionSpec{Name: refPhrase.Name, Version: 1, Class: core.ClassCompensable,
			Side: core.SideAgent, ForwardOnly: true, Converges: true, TypedConfirmation: true}, phrHandler{}),
	}}
}

func TestTypedConfirmationPhraseIsOptional(t *testing.T) {
	f := newFixtureWith(t, phrModule{})
	ctx := context.Background()
	quiet := f.plan(alice, refPhrase, `{"ask":false}`)
	if quiet.Confirmation != "" {
		t.Fatalf("plan without a phrase asks %q", quiet.Confirmation)
	}
	if v, err := f.eng.Apply(ctx, alice, quiet.PlanID, core.ApplyOptions{}); err != nil || v.State != core.StateCompleted {
		t.Errorf("apply of a plan without a phrase: %+v, %v", v, err)
	}
	asks := f.plan(alice, refPhrase, `{"ask":true}`)
	if asks.Confirmation != "restart it" {
		t.Fatalf("plan with a phrase asks %q", asks.Confirmation)
	}
	_, err := f.eng.Apply(ctx, alice, asks.PlanID, core.ApplyOptions{})
	if errcode.From(err).Code != errcode.PlanConfirmationNeeded {
		t.Errorf("apply without the phrase: %v, want %s", err, errcode.PlanConfirmationNeeded)
	}
	if v, err := f.eng.Apply(ctx, alice, asks.PlanID, core.ApplyOptions{Confirmation: "restart it"}); err != nil || v.State != core.StateCompleted {
		t.Errorf("apply with the phrase: %+v, %v", v, err)
	}
}
