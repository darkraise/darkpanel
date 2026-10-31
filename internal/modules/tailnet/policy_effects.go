package tailnet

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules/tailnet/policy"
)

const policyResourcePrefix = "tailnet.policy:"

// policyDoc is the state document of a policy: its canonical JSON, so a
// change of comments or spacing alone is not a change of state, and its
// ETag as a JSON string when one is known.
func policyDoc(canon []byte, etag string) core.StateDoc {
	state := make(json.RawMessage, 0, len(canon)+len(`{"policy":}`))
	state = append(append(append(state, `{"policy":`...), canon...), '}')
	doc := core.StateDoc{State: state}
	if etag != "" {
		doc.Version = json.RawMessage(strconv.Quote(etag))
	}
	return doc
}

// policyState is the state document of policy text with its ETag.
func policyState(text []byte, etag string) (core.StateDoc, error) {
	canon, err := policy.Canonical(text)
	if err != nil {
		return core.StateDoc{}, fmt.Errorf("tailnet: read the policy file: %w", err)
	}
	return policyDoc(canon, etag), nil
}

// writePolicy replaces the policy text before, read with etag, by after and
// answers the ETag written. The before-text, comments included, is sealed
// with the effect so its undo can post it back.
func (t tn) writePolicy(ctx context.Context, env core.Env, a *api, acct account, before []byte, etag string, after []byte) (string, error) {
	beforeDoc, err := policyState(before, etag)
	if err != nil {
		return "", core.Definite(err)
	}
	afterCanon, err := policy.Canonical(after)
	if err != nil {
		return "", core.Definite(fmt.Errorf("tailnet: read the new policy file: %w", err))
	}
	e := cloudEffect(core.EffectModify, policyResourcePrefix+acct.Tailnet, beforeDoc.State)
	e.Before = beforeDoc
	afterDoc := policyDoc(afterCanon, "")
	e.After = &afterDoc
	seq, err := env.Effects.Intend(ctx, e, before)
	if err != nil {
		return "", err
	}
	written, err := a.SetPolicy(ctx, after, etag)
	// The outcome is recorded even when ctx ended during the call.
	record := context.WithoutCancel(ctx)
	if err != nil {
		// Only a definite refusal proves nothing committed; any other error
		// stays intended for the engine to settle.
		if core.IsDefinite(err) {
			if serr := env.Effects.Skipped(record, seq); serr != nil {
				return "", errors.Join(err, fmt.Errorf("tailnet: journal %s: %w", e.Resource, serr))
			}
		}
		return "", err
	}
	if err := env.Effects.Made(record, seq, e.Resource, policyDoc(afterCanon, written)); err != nil {
		return "", err
	}
	return written, nil
}

// policyHandler observes and undoes the effect of a policy write.
type policyHandler struct{ t tn }

// Observe reads the tailnet's one policy file, whatever the effect's state.
// The engine compares State only, so a new ETag over the same canonical
// policy is no conflict.
func (h policyHandler) Observe(ctx context.Context, env core.Env, e core.Effect) (core.Observation, error) {
	a, _, err := h.t.connected(ctx, env.DB, env.Sealer, env.Clock)
	if err != nil {
		return core.Observation{}, err
	}
	text, etag, err := a.Policy(ctx)
	if err != nil {
		return core.Observation{}, err
	}
	doc, err := policyState(text, etag)
	if err != nil {
		return core.Observation{}, err
	}
	return core.Observation{Current: doc, Resource: e.Resource, Matches: 1, Exclusive: true}, nil
}

// Inverse posts the sealed before-text back if the policy still has the ETag
// the write answered; a later edit makes the provider refuse it with
// tailnet.policy_conflict, so the edit is never overwritten.
func (h policyHandler) Inverse(ctx context.Context, env core.Env, e core.Effect) (core.InverseResult, error) {
	before, err := env.Effects.Secret(ctx, e.Seq)
	if err != nil {
		return core.InverseResult{}, core.Definite(fmt.Errorf("tailnet: read the replaced text of %s: %w", e.Resource, err))
	}
	if len(before) == 0 {
		return core.InverseResult{}, core.Definite(fmt.Errorf("tailnet: the replaced text of %s is gone", e.Resource))
	}
	if e.Post == nil {
		return core.InverseResult{}, core.Definite(fmt.Errorf("tailnet: effect on %s has no written ETag", e.Resource))
	}
	var ifMatch string
	if err := json.Unmarshal(e.Post.Version, &ifMatch); err != nil {
		return core.InverseResult{}, core.Definite(fmt.Errorf("tailnet: decode the written ETag of %s: %w", e.Resource, err))
	}
	a, _, err := h.t.connected(ctx, env.DB, env.Sealer, env.Clock)
	if err != nil {
		return core.InverseResult{}, core.Definite(err)
	}
	_, err = a.SetPolicy(ctx, before, ifMatch)
	return core.InverseResult{}, err
}
