package tailnet

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/darkraise/darkpanel/internal/controller/cloud"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// describedKeys lists the tailnet's auth keys that are not revoked, each with
// its description. The list may carry ids only, so a key listed without a
// description is read on its own; a key gone by then is left out.
func describedKeys(ctx context.Context, a *api) ([]KeyInfo, error) {
	listed, err := a.Keys(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]KeyInfo, 0, len(listed))
	for _, k := range listed {
		if k.Description == "" {
			k, err = a.Key(ctx, k.ID)
			if authkeyGone(err) {
				continue
			}
			if err != nil {
				return nil, err
			}
		}
		if !k.Revoked {
			out = append(out, k)
		}
	}
	return out, nil
}

// authkeyGone reports whether err is the provider saying the key does not
// exist.
func authkeyGone(err error) bool {
	return err != nil && (errcode.From(err).Code == errcode.NotFound || cloud.Status(err) == http.StatusNotFound)
}

// authkeyDescription is the description of the key a creation effect made:
// its recovery marker.
func authkeyDescription(e core.Effect) (string, error) {
	if e.Recovery == nil || e.Recovery.Value == "" {
		return "", fmt.Errorf("tailnet: effect %s has no auth key marker", e.Resource)
	}
	return e.Recovery.Value, nil
}

// Observe finds the key an effect names. A creation not yet named is found by
// its description among the tailnet's live keys; a named one is read by id,
// whatever the effect's state, since the undo leaves no other object.
func (h authkeyHandler) Observe(ctx context.Context, env core.Env, e core.Effect) (core.Observation, error) {
	rest, ok := strings.CutPrefix(e.Resource, authkeyResourcePrefix)
	if !ok || rest == "" {
		return core.Observation{}, fmt.Errorf("tailnet: %q is not an auth key", e.Resource)
	}
	a, _, err := h.t.connected(ctx, env.DB, env.Sealer, env.Clock)
	if err != nil {
		return core.Observation{}, err
	}
	null := core.StateDoc{State: json.RawMessage("null")}
	if strings.HasPrefix(rest, "?") {
		description, err := authkeyDescription(e)
		if err != nil {
			return core.Observation{}, err
		}
		keys, err := describedKeys(ctx, a)
		if err != nil {
			return core.Observation{}, err
		}
		var matched []KeyInfo
		for _, k := range keys {
			if k.Description == description {
				matched = append(matched, k)
			}
		}
		obs := core.Observation{Current: null, Matches: len(matched), Exclusive: true}
		switch len(matched) {
		case 0:
		case 1:
			obs.Resource = authkeyResourcePrefix + matched[0].ID
			obs.Current = core.StateDoc{State: authkeyState(matched[0].ID, matched[0].Description)}
		default:
			for _, k := range matched {
				obs.Candidates = append(obs.Candidates, core.Candidate{
					Resource: authkeyResourcePrefix + k.ID, State: core.StateDoc{State: authkeyState(k.ID, k.Description)},
				})
			}
		}
		return obs, nil
	}
	obs := core.Observation{Current: null, Resource: e.Resource, Matches: 1, Exclusive: true}
	k, err := a.Key(ctx, rest)
	switch {
	case authkeyGone(err):
		return obs, nil
	case err != nil:
		return core.Observation{}, err
	case !k.Revoked:
		obs.Current = core.StateDoc{State: authkeyState(k.ID, k.Description)}
	}
	return obs, nil
}

// Inverse deletes the key the effect made and marks its row deleted.
func (h authkeyHandler) Inverse(ctx context.Context, env core.Env, e core.Effect) (core.InverseResult, error) {
	id, ok := strings.CutPrefix(e.Resource, authkeyResourcePrefix)
	if !ok || id == "" || strings.Contains(id, "?") {
		return core.InverseResult{}, core.Definite(fmt.Errorf("tailnet: %q names no auth key to delete", e.Resource))
	}
	a, _, err := h.t.connected(ctx, env.DB, env.Sealer, env.Clock)
	if err != nil {
		// Nothing was sent to the provider.
		return core.InverseResult{}, core.Definite(err)
	}
	// A not-found answer counts as deleted: the cloud client resends a DELETE
	// whose answer was lost, so a delete that committed can come back 404.
	if err := a.DeleteKey(ctx, id); err != nil && !authkeyGone(err) {
		// The cloud client never marks a DELETE refusal definite, since it
		// resends a DELETE; a 4xx answer still changed nothing.
		if s := cloud.Status(err); s >= 400 && s <= 499 {
			return core.InverseResult{}, core.Definite(err)
		}
		return core.InverseResult{}, err
	}
	// The key is gone whatever happens to the row, so a failed row write is
	// only logged: a live row is touched by no sweep.
	record := context.WithoutCancel(ctx)
	description, err := authkeyDescription(e)
	if err == nil {
		err = env.DB.Write(record, func(tx *sql.Tx) error {
			return setKeyState(record, tx, description, "deleted", "", env.Clock.Now().UTC())
		})
	}
	if err != nil {
		env.Log.Warn("tailnet: record the deleted auth key", "resource", e.Resource, "error", err)
	}
	return core.InverseResult{}, nil
}

// Resolved moves the key's row with the operator's resolution of its
// creation effect.
func (h authkeyHandler) Resolved(ctx context.Context, tx *sql.Tx, e core.Effect, _ string) error {
	description, err := authkeyDescription(e)
	if err != nil {
		return err
	}
	state, keyID := "", ""
	switch e.State {
	case core.EffectAccepted:
		state = "retired"
		if e.Current != nil && !authkeyNull(e.Current.State) {
			var cur struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(e.Current.State, &cur); err != nil {
				return fmt.Errorf("tailnet: decode the accepted state of %s: %w", e.Resource, err)
			}
			if cur.ID == "" {
				return fmt.Errorf("tailnet: the accepted state of %s names no key", e.Resource)
			}
			state, keyID = "live", cur.ID
		}
	case core.EffectMade:
		id, ok := strings.CutPrefix(e.Resource, authkeyResourcePrefix)
		if !ok || id == "" || strings.Contains(id, "?") {
			return fmt.Errorf("tailnet: %q names no auth key", e.Resource)
		}
		state, keyID = "live", id
	case core.EffectSkipped:
		state = "retired"
	default:
		return fmt.Errorf("tailnet: effect %s resolved in state %s", e.Resource, e.State)
	}
	return setKeyState(ctx, tx, description, state, keyID, e.ResolvedAt)
}

// authkeyNull reports whether a state document says no key exists.
func authkeyNull(state json.RawMessage) bool {
	var v any
	return len(state) == 0 || (json.Unmarshal(state, &v) == nil && v == nil)
}
