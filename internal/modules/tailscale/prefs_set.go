package tailscale

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/tailscale/link"
	"github.com/darkraise/darkpanel/internal/platform/tslocal"
)

const (
	prefsEffectResource = ID + ".prefs:node" // the one modify effect of prefs.set and prefs.repair
	// prefsCommitWait is how long tailscaled may still commit an edit whose answer was lost: a local daemon's.
	prefsCommitWait = 2 * time.Minute
)

// prefsSetNodeID is a node's stable ID, the other form an exit node may take besides a Tailscale address.
var prefsSetNodeID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

var (
	prefsSetDefault4 = netip.MustParsePrefix("0.0.0.0/0")
	prefsSetDefault6 = netip.MustParsePrefix("::/0")
)

// prefsSet is tailscale.prefs.set: it changes the preferences the input sets and leaves every other one as it is.
// Its revert restores the previous values of exactly those preferences.
func (t ts) prefsSet() core.ActionDef {
	return core.DefineRevertible[PrefsSetInput, PrefsResult](core.ActionSpec{
		Name: ID + ".prefs.set", Summary: "Set the node's Tailscale preferences.",
		Version: 1, Side: core.SideAgent, LockKeys: []string{lockKey}, RepairedBy: ID + ".prefs.repair",
		Guards:    []core.GuardSpec{{ID: link.GuardID, Overridable: true}},
		Available: t.available,
	}, prefsSetHandler{t: t})
}

// prefsSetHandler is prefs.set's handler; its value methods Revert and Observe work on any change whose snapshot
// is the JSON of PrefsFields (the previous values of exactly the fields it sets) and whose effects are the one
// modify effect on prefsEffectResource with Before.State the previous values and Post the values after.
type prefsSetHandler struct{ t ts }

// read reads the node's preferences.
func (h prefsSetHandler) read(ctx context.Context) (*tslocal.Prefs, error) {
	p, err := h.t.d.local().Prefs(ctx)
	if err != nil {
		return nil, fmt.Errorf("tailscale: read the preferences: %w", err)
	}
	return p, nil
}

func (h prefsSetHandler) Plan(ctx context.Context, env core.Env, in PrefsSetInput) (core.PlanOutcome, error) {
	want, err := prefsSetNormalize(in.PrefsFields)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	if want, err = prefsSetResolve(ctx, h.t.d.local(), want); err != nil {
		return core.PlanOutcome{}, err
	}
	lp, raw, err := h.t.currentLinkPrefs(ctx)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	all := prefsFieldsOf(raw)
	cur := prefsSetPick(all, want)
	g, err := h.t.linkGuard(ctx, env, in.Guarded, lp, link.Change{Kind: "prefs", After: prefsSetLinkAfter(lp, all, want)})
	if err != nil {
		return core.PlanOutcome{}, err
	}
	var diff []core.DiffLine
	curFields := prefsSetFields(cur)
	for i, w := range prefsSetFields(want) {
		c := curFields[i]
		if c.key == w.key {
			diff = append(diff, core.DiffLine{Op: " ", Text: w.name + ": " + w.text + " (unchanged)"})
			continue
		}
		diff = append(diff, core.DiffLine{Op: "-", Text: c.name + ": " + c.text}, core.DiffLine{Op: "+", Text: w.name + ": " + w.text})
	}
	sum := sha256.Sum256(prefsSetState(cur).State)
	return core.PlanOutcome{Diff: diff, Guards: []core.Guard{g}, StateHash: hex.EncodeToString(sum[:])}, nil
}

func (h prefsSetHandler) Snapshot(ctx context.Context, _ core.Env, in PrefsSetInput) ([]byte, error) {
	want, err := prefsSetNormalize(in.PrefsFields)
	if err != nil {
		return nil, err
	}
	p, err := h.read(ctx)
	if err != nil {
		return nil, err
	}
	return prefsSetState(prefsSetPick(prefsFieldsOf(p), want)).State, nil
}

// Apply journals the effect before the edit, so a lost answer leaves an intended effect for the engine to settle.
func (h prefsSetHandler) Apply(ctx context.Context, env core.Env, in PrefsSetInput) (PrefsResult, error) {
	want, err := prefsSetNormalize(in.PrefsFields)
	if err != nil {
		return PrefsResult{}, err
	}
	if want, err = prefsSetResolve(ctx, h.t.d.local(), want); err != nil {
		return PrefsResult{}, core.Definite(err)
	}
	p, err := h.read(ctx)
	if err != nil {
		return PrefsResult{}, err
	}
	patch, err := prefsSetPatch(p, want)
	if err != nil {
		return PrefsResult{}, err
	}
	after := prefsSetState(want)
	seq, err := env.Effects.Intend(ctx, core.Effect{
		Kind: core.EffectModify, Resource: prefsEffectResource, Local: false, CommitWait: prefsCommitWait,
		Before: prefsSetState(prefsSetPick(prefsFieldsOf(p), want)), After: &after,
	}, nil)
	if err != nil {
		return PrefsResult{}, err
	}
	got, err := h.t.d.local().EditPrefs(ctx, patch)
	if err != nil {
		refusal := prefsSetRefusal(err)
		if !core.IsDefinite(refusal) {
			return PrefsResult{}, refusal
		}
		if err := env.Effects.Skipped(ctx, seq); err != nil {
			return PrefsResult{}, err
		}
		return PrefsResult{}, refusal
	}
	now := prefsSetPick(prefsFieldsOf(got), want)
	if err := env.Effects.Made(ctx, seq, prefsEffectResource, prefsSetState(now)); err != nil {
		return PrefsResult{}, err
	}
	return PrefsResult{Prefs: now}, nil
}

// Verify checks the host from the input alone, as recovery calls it without an output.
func (h prefsSetHandler) Verify(ctx context.Context, _ core.Env, in PrefsSetInput, _ PrefsResult) error {
	want, err := prefsSetNormalize(in.PrefsFields)
	if err != nil {
		return err
	}
	if want, err = prefsSetResolve(ctx, h.t.d.local(), want); err != nil {
		return err
	}
	p, err := h.read(ctx)
	if err != nil {
		return err
	}
	gotFields := prefsSetFields(prefsSetPick(prefsFieldsOf(p), want))
	for i, w := range prefsSetFields(want) {
		if g := gotFields[i]; g.key != w.key {
			return fmt.Errorf("tailscale: %s is %s after the change, want %s", w.name, g.text, w.text)
		}
	}
	return nil
}

// Observe reads the fields the effect covers, which its Before state names.
func (h prefsSetHandler) Observe(ctx context.Context, _ core.Env, e core.Effect) (core.Observation, error) {
	if e.Resource != prefsEffectResource {
		return core.Observation{}, fmt.Errorf("tailscale: observe %s: the preferences handler observes only %s", e.Resource, prefsEffectResource)
	}
	var covered PrefsFields
	if err := json.Unmarshal(e.Before.State, &covered); err != nil {
		return core.Observation{}, fmt.Errorf("tailscale: decode the before state of effect %d: %w", e.Seq, err)
	}
	p, err := h.read(ctx)
	if err != nil {
		return core.Observation{}, err
	}
	return core.Observation{Current: prefsSetState(prefsSetPick(prefsFieldsOf(p), covered)), Matches: 1, Exclusive: true}, nil
}

// Revert restores the snapshot's fields only when every effect is settled. The link guard judges a change, not a
// state, so there is no connectivity exception to weigh: an effect in doubt blocks the revert, and
// tailscale.prefs.repair is the way out.
func (h prefsSetHandler) Revert(ctx context.Context, env core.Env, snapshot []byte) (core.RevertOutcome, error) {
	var prior PrefsFields
	if err := json.Unmarshal(snapshot, &prior); err != nil {
		return core.RevertOutcome{}, fmt.Errorf("tailscale: decode the snapshot: %w", err)
	}
	effects, err := env.Effects.List(ctx)
	if err != nil {
		return core.RevertOutcome{}, err
	}
	var made []core.Effect
	for _, e := range effects {
		switch e.State {
		case core.EffectIntended, core.EffectUnresolved:
			return core.RevertOutcome{}, fmt.Errorf("tailscale: the revert left the preferences as they are: effect %d (%s) is %s; resolve it, then repair the change", e.Seq, e.Resource, e.State)
		case core.EffectMade:
			made = append(made, e)
		}
	}
	var outcomes []core.EffectOutcome
	var restorable []int
	for _, e := range made {
		obs, err := h.Observe(ctx, env, e)
		if err != nil {
			return core.RevertOutcome{}, err
		}
		if e.Post == nil || !prefsSetSame(e.Post.State, obs.Current.State) {
			current := obs.Current
			outcomes = append(outcomes, core.EffectOutcome{Seq: e.Seq, State: core.EffectUnresolved, Reason: core.ReasonConflict, Current: &current})
			continue
		}
		restorable = append(restorable, e.Seq)
	}
	if len(restorable) > 0 {
		if prior, err = prefsSetResolve(ctx, h.t.d.local(), prior); err != nil {
			return core.RevertOutcome{}, err
		}
		p, err := h.read(ctx)
		if err != nil {
			return core.RevertOutcome{}, err
		}
		patch, err := prefsSetPatch(p, prior)
		if err != nil {
			return core.RevertOutcome{}, err
		}
		got, err := h.t.d.local().EditPrefs(ctx, patch)
		if err != nil {
			return core.RevertOutcome{}, fmt.Errorf("tailscale: restore the preferences: %w", err)
		}
		left, want := prefsSetState(prefsSetPick(prefsFieldsOf(got), prior)).State, prefsSetState(prior).State
		if !prefsSetSame(left, want) {
			return core.RevertOutcome{}, fmt.Errorf("tailscale: the revert left the preferences %s, want %s", left, want)
		}
		for _, seq := range restorable {
			outcomes = append(outcomes, core.EffectOutcome{Seq: seq, State: core.EffectUndone})
		}
	}
	slices.SortFunc(outcomes, func(a, b core.EffectOutcome) int { return a.Seq - b.Seq })
	return core.RevertOutcome{Effects: outcomes}, nil
}

// prefsSetPick keeps of all exactly the fields mask sets.
func prefsSetPick(all, mask PrefsFields) PrefsFields {
	var out PrefsFields
	if mask.WantRunning != nil {
		out.WantRunning = all.WantRunning
	}
	if mask.AdvertiseRoutes != nil {
		out.AdvertiseRoutes = all.AdvertiseRoutes
	}
	if mask.ExitNode != nil {
		out.ExitNode = all.ExitNode
	}
	if mask.AdvertiseExitNode != nil {
		out.AdvertiseExitNode = all.AdvertiseExitNode
	}
	if mask.AcceptRoutes != nil {
		out.AcceptRoutes = all.AcceptRoutes
	}
	if mask.ShieldsUp != nil {
		out.ShieldsUp = all.ShieldsUp
	}
	if mask.RunSSH != nil {
		out.RunSSH = all.RunSSH
	}
	return out
}

// prefsSetInvalid is a definite request.invalid_input refusal, with one field error at path when path is set.
func prefsSetInvalid(path, detail string) error {
	e := &errcode.Error{Code: errcode.InvalidInput, Detail: detail}
	if path != "" {
		e.Fields = []errcode.FieldError{{Path: path, Message: detail}}
	}
	return core.Definite(e)
}

// prefsSetNormalize validates and canonicalises a masked set: routes sorted, without duplicates, in canonical
// form, and an exit node address unmapped.
func prefsSetNormalize(f PrefsFields) (PrefsFields, error) {
	if f == (PrefsFields{}) {
		return PrefsFields{}, prefsSetInvalid("", "set at least one preference")
	}
	out := f
	if f.AdvertiseRoutes != nil {
		prefixes := make([]netip.Prefix, 0, len(*f.AdvertiseRoutes))
		for i, s := range *f.AdvertiseRoutes {
			path := "/advertiseRoutes/" + strconv.Itoa(i)
			p, err := netip.ParsePrefix(s)
			switch {
			case err != nil:
				return PrefsFields{}, prefsSetInvalid(path, s+" is not an IP prefix")
			case p.Bits() == 0:
				return PrefsFields{}, prefsSetInvalid(path, "advertise an exit node with advertiseExitNode, not with a default route")
			case p != p.Masked():
				return PrefsFields{}, prefsSetInvalid(path, s+" has non-address bits set; use "+p.Masked().String())
			}
			prefixes = append(prefixes, p)
		}
		slices.SortFunc(prefixes, netip.Prefix.Compare)
		prefixes = slices.Compact(prefixes)
		routes := make([]string, 0, len(prefixes))
		for _, p := range prefixes {
			routes = append(routes, p.String())
		}
		out.AdvertiseRoutes = &routes
	}
	if f.ExitNode != nil && *f.ExitNode != "" {
		v := *f.ExitNode
		if a, err := netip.ParseAddr(v); err == nil {
			if !link.IsTailscaleAddr(a) {
				return PrefsFields{}, prefsSetInvalid("/exitNode", v+" is not a Tailscale address")
			}
			v = a.Unmap().String()
		} else if !prefsSetNodeID.MatchString(v) {
			return PrefsFields{}, prefsSetInvalid("/exitNode", "the exit node is neither a Tailscale address nor a node's stable ID")
		}
		out.ExitNode = &v
	}
	return out, nil
}

// prefsSetResolve replaces an exit node given by address with the stable ID of the peer that owns it, because
// tailscaled makes the same rewrite on every edit and the preferences read back hold the ID. An address no peer
// owns is kept, as tailscaled keeps it.
func prefsSetResolve(ctx context.Context, local localAPI, f PrefsFields) (PrefsFields, error) {
	if f.ExitNode == nil {
		return f, nil
	}
	a, err := netip.ParseAddr(*f.ExitNode)
	if err != nil {
		return f, nil
	}
	st, err := local.Status(ctx)
	if err != nil {
		return PrefsFields{}, fmt.Errorf("tailscale: read the status: %w", err)
	}
	a = a.Unmap()
	for _, peer := range st.Peer {
		if peer != nil && slices.ContainsFunc(peer.TailscaleIPs, func(ip netip.Addr) bool { return ip.Unmap() == a }) {
			id := peer.ID
			f.ExitNode = &id
			return f, nil
		}
	}
	return f, nil
}

// prefsSetPatch is the tslocal patch that sets exactly the fields want sets, given the current preferences. An
// exit node sets both the ID and the address, so one clears the other. The advertised routes carry the exit-node
// advertisement as the two default routes, so setting either field rewrites the whole list.
func prefsSetPatch(cur *tslocal.Prefs, want PrefsFields) (tslocal.PrefsPatch, error) {
	patch := tslocal.PrefsPatch{WantRunning: want.WantRunning, RouteAll: want.AcceptRoutes, ShieldsUp: want.ShieldsUp, RunSSH: want.RunSSH}
	if want.ExitNode != nil {
		id, ip := *want.ExitNode, netip.Addr{}
		if a, err := netip.ParseAddr(id); err == nil {
			id, ip = "", a
		}
		patch.ExitNodeID, patch.ExitNodeIP = &id, &ip
	}
	if want.AdvertiseRoutes != nil || want.AdvertiseExitNode != nil {
		current := prefsFieldsOf(cur)
		routes, exit := *current.AdvertiseRoutes, *current.AdvertiseExitNode
		if want.AdvertiseRoutes != nil {
			routes = *want.AdvertiseRoutes
		}
		if want.AdvertiseExitNode != nil {
			exit = *want.AdvertiseExitNode
		}
		prefixes := make([]netip.Prefix, 0, len(routes)+2)
		for _, s := range routes {
			p, err := netip.ParsePrefix(s)
			if err != nil {
				return tslocal.PrefsPatch{}, fmt.Errorf("tailscale: route %q: %w", s, err)
			}
			prefixes = append(prefixes, p)
		}
		slices.SortFunc(prefixes, netip.Prefix.Compare)
		if exit {
			prefixes = append(prefixes, prefsSetDefault4, prefsSetDefault6)
		}
		patch.AdvertiseRoutes = &prefixes
	}
	return patch, nil
}

// prefsSetLinkAfter is the node's preferences after want applies, as the guard reads them: a copy of lp with the
// masked fields applied, the advertised routes and exit-node flag as prefsSetPatch writes them, and ExitNode
// replaced only when want's exitNode differs from cur's (cur is prefsFieldsOf of the current preferences).
func prefsSetLinkAfter(lp link.Prefs, cur, want PrefsFields) link.Prefs {
	after := lp
	after.AdvertiseRoutes = slices.Clone(lp.AdvertiseRoutes)
	if want.WantRunning != nil {
		after.WantRunning = *want.WantRunning
	}
	if want.AcceptRoutes != nil {
		after.AcceptRoutes = *want.AcceptRoutes
	}
	if want.ShieldsUp != nil {
		after.ShieldsUp = *want.ShieldsUp
	}
	if want.RunSSH != nil {
		after.RunSSH = *want.RunSSH
	}
	if want.AdvertiseRoutes != nil || want.AdvertiseExitNode != nil {
		// prefsSetPatch rewrites the whole route list from these same values, and it reads the exit-node flag from
		// cur, which needs both default routes, where lp is true for either one alone.
		routes, exit := cur.AdvertiseRoutes, cur.AdvertiseExitNode
		if want.AdvertiseRoutes != nil {
			routes = want.AdvertiseRoutes
		}
		if want.AdvertiseExitNode != nil {
			exit = want.AdvertiseExitNode
		}
		after.AdvertiseExitNode = exit != nil && *exit
		after.AdvertiseRoutes = nil
		if routes != nil {
			// An entry that does not parse is left out, which the guard reads as a route removed: the stricter check.
			for _, s := range *routes {
				if p, err := netip.ParsePrefix(s); err == nil && p.Bits() != 0 {
					after.AdvertiseRoutes = append(after.AdvertiseRoutes, p)
				}
			}
		}
	}
	if want.ExitNode != nil {
		var curExit string
		if cur.ExitNode != nil {
			curExit = *cur.ExitNode
		}
		if *want.ExitNode != curExit {
			after.ExitNode = *want.ExitNode
		}
	}
	return after
}

// prefsSetRefusal classifies a failed edit: a socket that does not answer and a 4xx answer guarantee tailscaled
// committed nothing, so they come back definite; any other error is returned unchanged, the edit in doubt.
func prefsSetRefusal(err error) error {
	var api *tslocal.APIError
	switch {
	case errors.Is(err, tslocal.ErrUnavailable):
		return core.Definite(errcode.Wrap(errcode.TailscaleUnavailable, err, "tailscaled does not answer on its socket"))
	case errors.As(err, &api) && api.StatusCode >= 400 && api.StatusCode <= 499:
		return core.Definite(errcode.Wrap(errcode.InvalidInput, err, "tailscaled refused the preferences: "+api.Message))
	}
	return err
}

// prefsSetState is the state document of a masked set.
func prefsSetState(f PrefsFields) core.StateDoc {
	b, _ := json.Marshal(f)
	return core.StateDoc{State: b}
}

// prefsSetSame reports whether two JSON documents are the same value.
func prefsSetSame(a, b json.RawMessage) bool {
	var va, vb any
	if json.Unmarshal(a, &va) != nil || json.Unmarshal(b, &vb) != nil {
		return false
	}
	return reflect.DeepEqual(va, vb)
}

// prefsSetField is one field a masked set sets: its wire name, its value as a comparison key, and as the diff
// shows it.
type prefsSetField struct{ name, key, text string }

// prefsSetFields lists the fields f sets, in the order of the wire shape.
func prefsSetFields(f PrefsFields) []prefsSetField {
	var out []prefsSetField
	flag := func(name string, b *bool) {
		if b != nil {
			v := strconv.FormatBool(*b)
			out = append(out, prefsSetField{name: name, key: v, text: v})
		}
	}
	orNone := func(s string) string {
		if s == "" {
			return "none"
		}
		return s
	}
	flag("wantRunning", f.WantRunning)
	if f.AdvertiseRoutes != nil {
		out = append(out, prefsSetField{name: "advertiseRoutes", key: strings.Join(*f.AdvertiseRoutes, ","), text: orNone(strings.Join(*f.AdvertiseRoutes, ", "))})
	}
	if f.ExitNode != nil {
		out = append(out, prefsSetField{name: "exitNode", key: *f.ExitNode, text: orNone(*f.ExitNode)})
	}
	flag("advertiseExitNode", f.AdvertiseExitNode)
	flag("acceptRoutes", f.AcceptRoutes)
	flag("shieldsUp", f.ShieldsUp)
	flag("runSSH", f.RunSSH)
	return out
}
