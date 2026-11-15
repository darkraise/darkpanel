package npm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

// reconcileApplyTimeout covers a disconnect per hit, the stop of a failed one and a reload.
const reconcileApplyTimeout = 45 * time.Second

// ReconcileInput is the input of npm.instance.reconcile, which has none.
type ReconcileInput struct{}

// Reconciled is the output of npm.instance.reconcile.
type Reconciled struct {
	Dropped []DroppedNetwork `json:"dropped"`
}

// DroppedNetwork is a network NPM was taken off: the forwarded address it held there and the claimants whose claims
// on it stay.
type DroppedNetwork struct {
	Network   string   `json:"network"`
	Address   string   `json:"address"`
	Claimants []string `json:"claimants"`
}

// reconcileHit is a network on which NPM holds an address Where forwards to.
type reconcileHit struct {
	Network, NetworkID string
	Addr               netip.Addr
	Where              string
}

// instanceReconcile declares npm.instance.reconcile. It is forward only, because the background task that applies it
// may not apply an irreversible action, and putting NPM back at a forwarded address is what it exists to prevent.
func (n nm) instanceReconcile() core.ActionDef {
	return core.DefineMutating[ReconcileInput, Reconciled](core.ActionSpec{
		Name:         "npm.instance.reconcile",
		Summary:      "Take NPM off a network where it holds an address a proxy host forwards to.",
		Version:      1,
		Class:        core.ClassCompensable,
		Side:         core.SideAgent,
		LockKeys:     []string{lockKey},
		ForwardOnly:  true,
		Converges:    true,
		MinScope:     core.ScopeAdmin,
		ApplyTimeout: reconcileApplyTimeout,
	}, instanceReconcileHandler{n: n})
}

// instanceReconcileHandler is the handler of npm.instance.reconcile.
type instanceReconcileHandler struct{ n nm }

// reconcileHits finds the networks on which the running NPM holds a forwarded address, in network name order, one hit
// per network.
func (n nm) reconcileHits(ctx context.Context, env core.Env) ([]reconcileHit, error) {
	hits, _, err := n.reconcileScan(ctx, env)
	return hits, err
}

// reconcileScan is reconcileHits with the NPM container it inspected. No instance, no container and a stopped one hold
// nothing. When the lookups fail, only the literal forwards are compared and the failure is logged: a hit found from
// them is still a hit.
func (n nm) reconcileScan(ctx context.Context, env core.Env) ([]reconcileHit, dockerapi.ContainerJSON, error) {
	hits, c, _, err := n.reconcileScanLookups(ctx, env)
	return hits, c, err
}

// reconcileScanLookups is reconcileScan that also returns the lookup failure it logged, nil when the names resolved.
func (n nm) reconcileScanLookups(ctx context.Context, env core.Env) ([]reconcileHit, dockerapi.ContainerJSON, error, error) {
	if _, ok, err := loadInstance(ctx, env.DB.Read()); err != nil || !ok {
		return nil, dockerapi.ContainerJSON{}, nil, err
	}
	c, err := n.d.docker.InspectContainer(ctx, containerName)
	switch {
	case dockerapi.IsNotFound(err):
		return nil, dockerapi.ContainerJSON{}, nil, nil
	case err != nil:
		return nil, dockerapi.ContainerJSON{}, nil, fmt.Errorf("npm: inspect container %s: %w", containerName, engineCoded(err))
	case !c.State.Running:
		return nil, c, nil, nil
	}
	refs, err := n.forwardRefs(ctx, env)
	if err != nil {
		return nil, c, nil, err
	}
	answers, lookupErr := n.forwardAnswers(ctx, refNames(refs))
	if lookupErr != nil {
		answers = nil
		if env.Log != nil {
			env.Log.Warn("npm: the forward names could not be resolved; only the literal forwards were compared", "error", lookupErr)
		}
	}
	targets, _ := forwardsOf(refs, answers)
	// Literal addresses come first, as verifyHeld compares them.
	slices.SortStableFunc(targets, func(a, b forwardTarget) int {
		switch {
		case a.name == b.name:
			return 0
		case a.name:
			return 1
		}
		return -1
	})
	held := heldAddresses(c)
	var hits []reconcileHit
	for _, network := range slices.Sorted(maps.Keys(held)) {
		for _, t := range targets {
			if slices.Contains(held[network], t.addr.Unmap()) {
				hits = append(hits, reconcileHit{Network: network, NetworkID: c.NetworkSettings.Networks[network].NetworkID, Addr: t.addr, Where: t.where})
				break
			}
		}
	}
	return hits, c, lookupErr, nil
}

func reconcileStateHash(hits []reconcileHit, c dockerapi.ContainerJSON) string {
	var b strings.Builder
	for _, hit := range hits {
		endpoint := ""
		if ep := c.NetworkSettings.Networks[hit.Network]; ep != nil {
			endpoint = ep.EndpointID
		}
		b.WriteString(hit.Network + "\n" + hit.Addr.String() + "\n" + endpoint + "\n")
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

func (hit reconcileHit) text() string {
	return fmt.Sprintf("%s forwards to %s, which %s holds on network %s", hit.Where, hit.Addr, containerName, hit.Network)
}

// Plan lists the networks NPM leaves. The state hash covers the hits, not every target, so a name whose answers rotate
// does not change it.
func (h instanceReconcileHandler) Plan(ctx context.Context, env core.Env, _ ReconcileInput) (core.PlanOutcome, error) {
	hits, c, err := h.n.reconcileScan(ctx, env)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	var diff []core.DiffLine
	for _, hit := range hits {
		if hit.Network == networkName {
			diff = append(diff, core.DiffLine{Op: " ", Text: hit.text() + "; it stays on " + networkName})
			continue
		}
		diff = append(diff, core.DiffLine{Op: "-", Text: "container " + containerName + " leaves network " + hit.Network})
	}
	return core.PlanOutcome{Diff: diff, StateHash: reconcileStateHash(hits, c)}, nil
}

// Apply finds the hits again under the lock, since an attach, a detach or an upgrade may have finished since the plan,
// takes NPM off each network of a hit other than darkpanel-npm and reloads nginx. The claims stay. A scan whose lookups
// failed and found no hit does not show that the pass's hits went away: it refuses, Definite, as nothing changed.
func (h instanceReconcileHandler) Apply(ctx context.Context, env core.Env, _ ReconcileInput) (Reconciled, error) {
	hits, c, lookupErr, err := h.n.reconcileScanLookups(ctx, env)
	if err != nil {
		return Reconciled{}, err
	}
	if lookupErr != nil && len(hits) == 0 {
		return Reconciled{}, core.Definite(errcode.New(errcode.NPMForwardUnresolved,
			"the forward names could not be resolved under the lock, so no hit was checked: "+lookupErr.Error()))
	}
	out := Reconciled{Dropped: []DroppedNetwork{}}
	var own *reconcileHit
	var left []string
	for _, hit := range hits {
		if hit.Network == networkName {
			own = &hit
			continue
		}
		if err := h.drop(ctx, env, c.ID, hit); err != nil {
			return Reconciled{}, err
		}
		cl, err := claims(ctx, env.DB.Read(), hit.Network)
		if err != nil {
			return Reconciled{}, err
		}
		claimants := []string{}
		for _, r := range cl {
			claimants = append(claimants, r.Claimant)
		}
		out.Dropped = append(out.Dropped, DroppedNetwork{Network: hit.Network, Address: hit.Addr.String(), Claimants: claimants})
		left = append(left, hit.Network)
		if env.Log != nil {
			env.Log.Warn("npm: took "+containerName+" off a network where it held a forwarded address",
				"network", hit.Network, "address", hit.Addr.String(), "forward", hit.Where, "claimants", claimants)
		}
	}
	if len(left) > 0 {
		if err := h.n.reloadNginx(ctx); err != nil {
			detail := "nginx could not be reloaded after " + containerName + " left network " + strings.Join(left, ", ") + ": " + err.Error()
			if errors.Is(err, errNginxTest) {
				if us, listErr := h.n.unansweredForwards(ctx, env); listErr == nil && len(us) > 0 {
					detail += "; " + unansweredDetail(us)
				}
			}
			return Reconciled{}, errcode.New(errcode.NPMUnhealthy, detail)
		}
	}
	if own != nil {
		detail := own.text() + "; it stays on " + networkName
		if len(left) == 0 {
			return Reconciled{}, core.Definite(errcode.New(errcode.NPMReserved, detail))
		}
		return Reconciled{}, errcode.New(errcode.NPMReserved, detail+"; it left network "+strings.Join(left, ", "))
	}
	return out, nil
}

// drop journals and makes the disconnect of NPM from hit's network as the detach journals its own; a disconnect that
// fails stops NPM (dropOrStop).
func (h instanceReconcileHandler) drop(ctx context.Context, env core.Env, containerID string, hit reconcileHit) error {
	resource := resourceOf(nounEndpoint, hit.Network)
	obs, err := h.n.observeEngine(ctx, env, core.Effect{Kind: core.EffectModify, Resource: resource, State: core.EffectIntended})
	if err != nil {
		return err
	}
	e := npmEffect(core.EffectModify, resource, obs.Current.State, commitWait)
	e.InverseRecovery = &core.RecoveryID{Kind: "name", Value: containerName + "@" + hit.Network}
	return h.n.run(ctx, env, e, h.n.observeEngine, func(ctx context.Context) (string, error) {
		if err := instanceUpgradeHandler(h).dropOrStop(ctx, containerID, hit.Network, hit.text()); err != nil {
			return "", err
		}
		return resource, nil
	})
}

// Verify checks from the host alone that NPM holds no forwarded address on any network.
func (h instanceReconcileHandler) Verify(ctx context.Context, env core.Env, _ ReconcileInput, _ Reconciled) error {
	hits, err := h.n.reconcileHits(ctx, env)
	if err != nil {
		return err
	}
	if len(hits) > 0 {
		return errcode.New(errcode.NPMReserved, hits[0].text())
	}
	return nil
}

// Observe reads the endpoints the action journals.
func (h instanceReconcileHandler) Observe(ctx context.Context, env core.Env, e core.Effect) (core.Observation, error) {
	return h.n.observeEngine(ctx, env, e)
}

// The background task that takes NPM off a network where it holds a forwarded address: a pass at the agent's start,
// every reconcileEvery and on NPM's start and on each join of NPM to a network.
const (
	reconcileID    = ID + ".reconcile"
	reconcileEvery = 5 * time.Minute
)

// reconcileTask declares npm.reconcile. A pass reads the host and the proxy hosts and applies npm.instance.reconcile
// only on a hit. A hit on darkpanel-npm, which NPM never leaves, fails with npm.reserved; a pass whose hits are all on
// it is remembered in memory, so the same hit is reported once, and once more after an agent restart. Hits whose change
// failed twice in a row with npm.forward_unresolved are applied again at most once per reconcileEvery: a pass in
// between returns nil, so the runner's failure count does not hold back wakes. Any other failure is returned
// unremembered, so the runner's backoff retries it. The engine reports a change that failed in its handler through
// the returned view, not through the error.
func (n nm) reconcileTask() core.BackgroundTask {
	var mu sync.Mutex
	var failedKey string
	var unresolved struct {
		key   string
		count int
		at    time.Time
	}
	run := func(ctx context.Context, tc core.TaskContext) error {
		hits, err := n.reconcileHits(ctx, tc.Env)
		if err != nil {
			return err
		}
		mu.Lock()
		defer mu.Unlock()
		if len(hits) == 0 {
			failedKey = ""
			unresolved.key, unresolved.count = "", 0
			return nil
		}
		var b strings.Builder
		for _, hit := range hits {
			b.WriteString(hit.Network + "\n" + hit.Addr.String() + "\n" + hit.Where + "\n")
		}
		key := b.String()
		if key == failedKey {
			return nil
		}
		failedKey = ""
		now := tc.Env.Clock.Now()
		if key == unresolved.key && unresolved.count >= 2 && now.Sub(unresolved.at) < reconcileEvery {
			return nil
		}
		v, err := tc.Apply(ctx, "npm.instance.reconcile", ReconcileInput{})
		if err == nil && v.Error != nil && (v.State == core.StateFailed || v.State == core.StateNeedsRepair) {
			err = errcode.New(errcode.Code(v.Error.Code), v.Error.Message)
		}
		code := errcode.From(err).Code
		switch {
		case err == nil || code != errcode.NPMForwardUnresolved:
			unresolved.key, unresolved.count = "", 0
		case key == unresolved.key:
			unresolved.count, unresolved.at = unresolved.count+1, now
		default:
			unresolved.key, unresolved.count, unresolved.at = key, 1, now
		}
		if err != nil {
			if code == errcode.NPMReserved && onOwnNetwork(hits) {
				failedKey = key
			}
			return err
		}
		return nil
	}
	return core.BackgroundTask{
		ID:       reconcileID,
		Every:    reconcileEvery,
		LockKeys: []string{lockKey},
		Run:      run,
		Watch:    n.reconcileWatch,
	}
}

// onOwnNetwork reports whether every hit is on darkpanel-npm, the one network NPM never leaves. A Verify that fails on
// a hit elsewhere, which Apply missed when its lookups failed, is retried rather than remembered.
func onOwnNetwork(hits []reconcileHit) bool {
	for _, hit := range hits {
		if hit.Network != networkName {
			return false
		}
	}
	return true
}

// reconcileWatch wakes the task on a start of NPM's container and on a join of it to a network, until ctx ends (nil)
// or the stream fails. NPM's container id is read when an event arrives, since an upgrade changes it.
func (n nm) reconcileWatch(ctx context.Context, _ core.Env, wake func()) error {
	stream, err := n.d.docker.Events(ctx, dockerapi.EventsOptions{Filters: dockerapi.Filters{
		"type":  {"container", "network"},
		"event": {"start", "connect"},
	}})
	if err == nil {
		defer func() { _ = stream.Close() }()
		for {
			var ev dockerapi.Event
			if ev, err = stream.Next(); err != nil {
				break
			}
			if n.reconcileEvent(ctx, ev) {
				wake()
			}
		}
	}
	if ctx.Err() != nil {
		return nil
	}
	return fmt.Errorf("npm: reconcile: watch Docker events: %w", err)
}

// reconcileEvent reports whether ev is a start of NPM's container or a join of it to a network. The whole action is
// compared, since actions such as "exec_start: sh" carry a suffix.
func (n nm) reconcileEvent(ctx context.Context, ev dockerapi.Event) bool {
	var who string
	switch {
	case ev.Type == "container" && ev.Action == "start":
		if ev.Actor.Attributes["name"] == containerName {
			return true
		}
		who = ev.Actor.ID
	case ev.Type == "network" && ev.Action == "connect":
		who = ev.Actor.Attributes["container"]
	default:
		return false
	}
	if who == "" {
		return false
	}
	if who == containerName {
		return true
	}
	c, err := n.d.docker.InspectContainer(ctx, containerName)
	return err == nil && c.ID == who
}
