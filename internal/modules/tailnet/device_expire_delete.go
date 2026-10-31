package tailnet

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"

	"github.com/darkraise/darkpanel/internal/controller/cloud"
	"github.com/darkraise/darkpanel/internal/core"
)

// endResourcePrefix starts the resource of an expire or delete effect; the
// device's node id follows it.
const endResourcePrefix = "tailnet.device:"

// deviceExpire declares tailnet.device.expire, which expires a device's
// node key.
func (t tn) deviceExpire() core.ActionDef {
	return core.DefineMutating[DeviceInput, DeviceRef](core.ActionSpec{
		Name: ID + ".device.expire", Summary: "Expire a device's node key so it must authenticate again.", Version: 1,
		Class: core.ClassIrreversible, Side: core.SideController, LockKeys: []string{ID},
	}, deviceEnd{t: t})
}

// deviceDelete declares tailnet.device.delete, which removes a device from
// the tailnet.
func (t tn) deviceDelete() core.ActionDef {
	return core.DefineMutating[DeviceInput, DeviceRef](core.ActionSpec{
		Name: ID + ".device.delete", Summary: "Remove a device from the tailnet.", Version: 1,
		Class: core.ClassIrreversible, Side: core.SideController, LockKeys: []string{ID},
	}, deviceEnd{t: t, remove: true})
}

// deviceEnd expires a device's node key, or removes the device when remove
// is set. Neither can be undone, so it has no Inverse.
type deviceEnd struct {
	t      tn
	remove bool
}

// endState is the expire form of a device's state: its key expiry, which
// the provider sets when it expires the key.
func endState(d Device) (json.RawMessage, error) {
	b, err := json.Marshal(struct {
		Expires string `json:"expires"`
	}{d.Expires})
	if err != nil {
		return nil, fmt.Errorf("tailnet: encode the expiry of device %s: %w", d.ID, err)
	}
	return b, nil
}

// endRefused reports whether the provider refused a write before changing
// anything. The cloud client marks only non-GET, non-DELETE 4xx answers
// Definite, so a refused DELETE is recognised by its status.
func endRefused(err error) bool {
	s := cloud.Status(err)
	return core.IsDefinite(err) || (s >= 400 && s <= 499)
}

// endSameJSON reports whether a and b hold the same JSON value, ignoring key
// order and insignificant whitespace.
func endSameJSON(a, b json.RawMessage) (bool, error) {
	var av, bv any
	if err := json.Unmarshal(a, &av); err != nil {
		return false, fmt.Errorf("tailnet: decode state %q: %w", a, err)
	}
	if err := json.Unmarshal(b, &bv); err != nil {
		return false, fmt.Errorf("tailnet: decode state %q: %w", b, err)
	}
	return reflect.DeepEqual(av, bv), nil
}

// form is the state of a present device as an effect of kind records it:
// the device's identity for a deletion, its expiry otherwise.
func (deviceEnd) form(kind core.EffectKind, d Device) (json.RawMessage, error) {
	if kind != core.EffectDelete {
		return endState(d)
	}
	b, err := json.Marshal(DeviceRef{ID: d.ID, Name: d.Name})
	if err != nil {
		return nil, fmt.Errorf("tailnet: encode the identity of device %s: %w", d.ID, err)
	}
	return b, nil
}

// kind is the kind of the one effect the action journals.
func (h deviceEnd) kind() core.EffectKind {
	if h.remove {
		return core.EffectDelete
	}
	return core.EffectModify
}

// target reads the device the input names through the connected account.
// The input's device is the provider's node id; a name is not resolved.
func (h deviceEnd) target(ctx context.Context, env core.Env, in DeviceInput) (*api, account, Device, error) {
	if err := requireDevice(in.Device); err != nil {
		return nil, account{}, Device{}, err
	}
	a, acct, err := h.t.connected(ctx, env.DB, env.Sealer, env.Clock)
	if err != nil {
		return nil, account{}, Device{}, err
	}
	d, err := a.Device(ctx, in.Device)
	if err != nil {
		return nil, account{}, Device{}, err
	}
	return a, acct, d, nil
}

// Plan describes what the call removes and asks for the device's name as
// the confirmation.
func (h deviceEnd) Plan(ctx context.Context, env core.Env, in DeviceInput) (core.PlanOutcome, error) {
	_, acct, d, err := h.target(ctx, env, in)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	// LastSeen is left out: it changes on every read.
	hashed, err := json.Marshal([]any{d.ID, d.Name, d.Expires, d.Authorized, d.KeyExpiryDisabled})
	if err != nil {
		return core.PlanOutcome{}, fmt.Errorf("tailnet: encode the state of device %s: %w", d.ID, err)
	}
	sum := sha256.Sum256(hashed)
	named := d.Name + " (" + d.ID + ")"
	var diff []core.DiffLine
	if h.remove {
		diff = []core.DiffLine{{Op: "-", Text: "device " + named + " with addresses " + strings.Join(d.Addresses, ", ") +
			" leaves tailnet " + acct.Tailnet}}
	} else {
		expires := d.Expires
		if expires == "" || d.KeyExpiryDisabled {
			expires = "never"
		}
		diff = []core.DiffLine{
			{Op: "-", Text: "node key of " + named + " expires " + expires},
			{Op: "+", Text: "node key of " + named + " expires now; the device must authenticate again to rejoin " + acct.Tailnet},
		}
	}
	return core.PlanOutcome{Diff: diff, StateHash: hex.EncodeToString(sum[:]), Confirmation: d.Name}, nil
}

// Apply journals one effect, makes the provider call and records the
// device's state after it.
func (h deviceEnd) Apply(ctx context.Context, env core.Env, in DeviceInput) (DeviceRef, error) {
	a, _, d, err := h.target(ctx, env, in)
	if err != nil {
		return DeviceRef{}, err
	}
	resource := endResourcePrefix + d.ID
	before, err := h.form(h.kind(), d)
	if err != nil {
		return DeviceRef{}, err
	}
	e := cloudEffect(h.kind(), resource, before)
	if h.remove {
		e.After = &core.StateDoc{State: json.RawMessage("null")}
	}
	seq, err := env.Effects.Intend(ctx, e, nil)
	if err != nil {
		return DeviceRef{}, err
	}
	if h.remove {
		err = a.DeleteDevice(ctx, d.ID)
		// The cloud client resends a DELETE whose response was lost, and the
		// resend finds the device already gone.
		if cloud.Status(err) == http.StatusNotFound {
			err = nil
		}
	} else {
		err = a.Expire(ctx, d.ID)
	}
	if err != nil {
		// Any error but a refusal stays intended for the engine to settle.
		if endRefused(err) {
			if serr := env.Effects.Skipped(ctx, seq); serr != nil {
				return DeviceRef{}, errors.Join(err, fmt.Errorf("tailnet: journal %s: %w", resource, serr))
			}
			return DeviceRef{}, core.Definite(err)
		}
		return DeviceRef{}, err
	}
	post := json.RawMessage("null")
	if !h.remove {
		now, err := a.Device(ctx, d.ID)
		if err != nil {
			return DeviceRef{}, err
		}
		if post, err = endState(now); err != nil {
			return DeviceRef{}, err
		}
	}
	if err := env.Effects.Made(ctx, seq, resource, core.StateDoc{State: post}); err != nil {
		return DeviceRef{}, err
	}
	return DeviceRef{ID: d.ID, Name: d.Name}, nil
}

// Verify passes once the provider no longer shows the state the made
// effect recorded before the call. It reads only the provider and the
// journal, so startup recovery can call it.
func (h deviceEnd) Verify(ctx context.Context, env core.Env, in DeviceInput, _ DeviceRef) error {
	effs, err := env.Effects.List(ctx)
	if err != nil {
		return err
	}
	resource := endResourcePrefix + in.Device
	var made *core.Effect
	for i := range effs {
		if effs[i].State == core.EffectMade && effs[i].Resource == resource {
			made = &effs[i]
		}
	}
	if made == nil {
		return fmt.Errorf("tailnet: no call on %s was recorded", resource)
	}
	obs, err := h.Observe(ctx, env, *made)
	if err != nil {
		return err
	}
	same, err := endSameJSON(obs.Current.State, made.Before.State)
	if err != nil {
		return err
	}
	if same {
		return fmt.Errorf("tailnet: the provider still shows %s as it was before the call", resource)
	}
	return nil
}

// Observe reads the device an effect names in the form its Before records,
// so a provider still showing the before-state reconciles as not made. A
// device that no longer exists observes null.
func (h deviceEnd) Observe(ctx context.Context, env core.Env, e core.Effect) (core.Observation, error) {
	a, _, err := h.t.connected(ctx, env.DB, env.Sealer, env.Clock)
	if err != nil {
		return core.Observation{}, err
	}
	d, err := a.Device(ctx, strings.TrimPrefix(e.Resource, endResourcePrefix))
	return observedDevice(e, d, err, func(d Device) (json.RawMessage, error) { return h.form(e.Kind, d) })
}
