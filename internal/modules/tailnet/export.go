package tailnet

import (
	"cmp"
	"context"
	"slices"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/store"
)

// exportTN is the one value the module's actions and the exported readers
// share, so one token cache and one access token per account.
var exportTN = productionTN()

// readPolicy reads the policy file as tailnet.policy.get answers it.
func (t tn) readPolicy(ctx context.Context, env core.Env) (Policy, error) {
	a, _, err := t.connected(ctx, env.DB, env.Sealer, env.Clock)
	if err != nil {
		return Policy{}, err
	}
	text, etag, err := a.Policy(ctx)
	if err != nil {
		return Policy{}, err
	}
	return Policy{Text: string(text), ETag: etag}, nil
}

// listDevices lists the devices as tailnet.device.list answers them, by name
// then id.
func (t tn) listDevices(ctx context.Context, env core.Env) ([]Device, error) {
	a, _, err := t.connected(ctx, env.DB, env.Sealer, env.Clock)
	if err != nil {
		return nil, err
	}
	devices, err := a.Devices(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Device, 0, len(devices))
	for _, d := range devices {
		out = append(out, deviceView(d))
	}
	slices.SortFunc(out, func(x, y Device) int { return cmp.Or(cmp.Compare(x.Name, y.Name), cmp.Compare(x.ID, y.ID)) })
	return out, nil
}

// ReadPolicy reads the tailnet policy file with its comments and ETag, as
// tailnet.policy.get answers it, for controller-side modules; env needs DB,
// Sealer and Clock. Without an account it answers cloud.not_connected, as the
// action does.
func ReadPolicy(ctx context.Context, env core.Env) (Policy, error) {
	return exportTN.readPolicy(ctx, env)
}

// ListDevices lists the tailnet's devices, as tailnet.device.list answers
// them and in the same order, for controller-side modules; env needs DB,
// Sealer and Clock. Without an account it answers cloud.not_connected, as the
// action does.
func ListDevices(ctx context.Context, env core.Env) ([]Device, error) {
	return exportTN.listDevices(ctx, env)
}

// Connected reports whether the tailnet account row exists, without a
// provider call.
func Connected(ctx context.Context, db *store.DB) (bool, error) {
	_, ok, err := loadAccount(ctx, db.Read())
	return ok, err
}
