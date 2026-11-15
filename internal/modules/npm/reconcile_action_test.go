package npm

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

// w12gHit puts NPM on appnet at 10.9.0.7 with a proxy host that forwards there, and a claim of the claimant.
func w12gHit(t *testing.T) (*thHost, dockerapi.Network) {
	t.Helper()
	h, user := nwSetup(t)
	w12aSubnet(t, h, user)
	nwConnect(t, h, user, nwStatic(w12gAddr))
	nwClaim(t, h, nwNet, nwClaimant, "c0", false)
	w12aSeed(h, w12gAddr, 3000)
	return h, user
}

func w12gReconcile(t *testing.T, h *thHost) (Reconciled, error) {
	t.Helper()
	n := h.nm()
	n.d.resolve = w12eNoAnswer
	h.addChange("r1", "npm.instance.reconcile")
	raw, err := n.instanceReconcile().Handler.Apply(context.Background(), h.env("r1"), nwJSON(t, ReconcileInput{}))
	var out Reconciled
	if err == nil {
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatal(err)
		}
	}
	return out, err
}

func TestW12gReconcileDisconnectsNPMFromAForwardedAddress(t *testing.T) {
	h, user := w12gHit(t)
	out, err := w12gReconcile(t, h)
	if err != nil {
		t.Fatal(err)
	}
	if nwEndpoint(t, h, user.ID) != nil {
		t.Error("NPM stayed on a network where it holds a forwarded address")
	}
	if h.nginx.reloads != 1 {
		t.Errorf("%d reloads, want 1 after the disconnect", h.nginx.reloads)
	}
	if got := len(nwClaims(t, h, nwNet)); got != 1 {
		t.Errorf("%d claims left, want the claim kept", got)
	}
	if len(out.Dropped) != 1 || out.Dropped[0].Network != nwNet || out.Dropped[0].Address != w12gAddr ||
		len(out.Dropped[0].Claimants) != 1 || out.Dropped[0].Claimants[0] != nwClaimant {
		t.Errorf("dropped %+v, want %s at %s with claimant %s", out.Dropped, nwNet, w12gAddr, nwClaimant)
	}
}

func TestW12gReconcileApplyFindsNoHitAndChangesNothing(t *testing.T) {
	h, user := nwSetup(t)
	w12aSubnet(t, h, user)
	nwConnect(t, h, user, nwStatic(w12gAddr))
	w12aSeed(h, "10.9.0.40", 3000)
	out, err := w12gReconcile(t, h)
	if err != nil || len(out.Dropped) != 0 {
		t.Fatalf("an apply with no hit returned %+v and %v", out, err)
	}
	if nwEndpoint(t, h, user.ID) == nil || h.nginx.reloads != 0 {
		t.Error("an apply with no hit disconnected NPM or reloaded nginx")
	}
}

func TestW12gReconcileFailedReloadLeavesNPMDisconnected(t *testing.T) {
	h, user := w12gHit(t)
	h.nginx.fail(errors.New("boom"))
	_, err := w12gReconcile(t, h)
	var coded *errcode.Error
	if !errors.As(err, &coded) || coded.Code != errcode.NPMUnhealthy {
		t.Fatalf("error %v, want %s", err, errcode.NPMUnhealthy)
	}
	if want := "nginx could not be reloaded after darkpanel-npm left network appnet: boom"; coded.Detail != want {
		t.Errorf("detail %q, want %q", coded.Detail, want)
	}
	if nwEndpoint(t, h, user.ID) != nil {
		t.Error("a failed reload put NPM back on the network")
	}
}

func TestW12gReconcileAHitOnNPMsOwnNetworkLeavesNPMRunning(t *testing.T) {
	h, _ := nwSetup(t)
	own, ok := h.docker.network(networkName)
	if !ok {
		t.Fatal("NPM's own network is missing")
	}
	ep := nwEndpoint(t, h, own.ID)
	if ep == nil {
		t.Fatal("NPM is not on its own network")
	}
	w12aSeed(h, ep.IPAddress.String(), 3000)
	_, err := w12gReconcile(t, h)
	var coded *errcode.Error
	if !errors.As(err, &coded) || coded.Code != errcode.NPMReserved {
		t.Fatalf("error %v, want %s", err, errcode.NPMReserved)
	}
	if nwEndpoint(t, h, own.ID) == nil {
		t.Error("NPM was taken off its own network")
	}
	if c, ok := h.docker.container(containerName); !ok || !c.State.Running {
		t.Error("NPM was stopped")
	}
}

func TestW12gReconcilePlanListsTheHitsAndHashesThemNotTheTargets(t *testing.T) {
	ctx := context.Background()
	h, _ := w12gHit(t)
	w12aSeed(h, "rot.invalid", 80)
	var mu sync.Mutex
	answer := "203.0.113.9"
	n := h.nm()
	n.d.resolve = func(context.Context, string) ([]netip.Addr, error) {
		mu.Lock()
		defer mu.Unlock()
		return []netip.Addr{netip.MustParseAddr(answer)}, nil
	}
	plan := func() core.PlanOutcome {
		out, err := n.instanceReconcile().Handler.Plan(ctx, h.env(""), nwJSON(t, ReconcileInput{}))
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	first := plan()
	mu.Lock()
	answer = "203.0.113.10"
	mu.Unlock()
	second := plan()
	if first.StateHash == "" || first.StateHash != second.StateHash {
		t.Errorf("the state hash changed from %q to %q when only an unrelated name's answer changed", first.StateHash, second.StateHash)
	}
	found := false
	for _, l := range first.Diff {
		found = found || strings.Contains(l.Text, "container darkpanel-npm leaves network "+nwNet)
	}
	if !found {
		t.Errorf("diff %+v does not say NPM leaves %s", first.Diff, nwNet)
	}
	h2, user2 := nwSetup(t)
	w12aSubnet(t, h2, user2)
	nwConnect(t, h2, user2, nwStatic(w12gAddr))
	out, err := h2.nm().instanceReconcile().Handler.Plan(ctx, h2.env(""), nwJSON(t, ReconcileInput{}))
	if err != nil || len(out.Diff) != 0 {
		t.Errorf("a plan with no hit gave diff %+v and %v, want an empty diff", out.Diff, err)
	}
}

func TestW12gReconcileFailedDisconnectStopsNPM(t *testing.T) {
	h, _ := w12gHit(t)
	h.docker.Fail("DisconnectNetwork", errors.New("engine down"))
	_, err := w12gReconcile(t, h)
	if err == nil || core.IsDefinite(err) {
		t.Fatalf("error %v, want a failure that is not definite", err)
	}
	if c, ok := h.docker.container(containerName); !ok || c.State.Running {
		t.Error("NPM kept running with a forwarded address it could not leave")
	}
}

func TestW12gReconcileFailedReloadNamesTheUnansweredForwards(t *testing.T) {
	h, _ := w12gHit(t)
	w12aSeed(h, "def.invalid", 80, w12gLoc("/a", "gone.invalid"))
	h.nginx.fail(errNginxTest)
	_, err := w12gReconcile(t, h)
	var coded *errcode.Error
	if !errors.As(err, &coded) || coded.Code != errcode.NPMUnhealthy {
		t.Fatalf("error %v, want %s", err, errcode.NPMUnhealthy)
	}
	want := "nginx could not be reloaded after darkpanel-npm left network appnet: nginx's configuration test failed; location /a of proxy host 2 forwards to gone.invalid, which has no answer"
	if coded.Detail != want {
		t.Errorf("detail %q, want %q", coded.Detail, want)
	}
}

func TestW12gReconcileVerifyRequiresNoForwardedAddress(t *testing.T) {
	ctx := context.Background()
	h, _ := w12gHit(t)
	n := h.nm()
	n.d.resolve = w12eNoAnswer
	if err := n.instanceReconcile().Handler.Verify(ctx, h.env(""), nwJSON(t, ReconcileInput{}), nwJSON(t, Reconciled{})); err == nil {
		t.Error("Verify passed while NPM holds a forwarded address")
	}
	if _, err := w12gReconcile(t, h); err != nil {
		t.Fatal(err)
	}
	if err := n.instanceReconcile().Handler.Verify(ctx, h.env("r1"), nwJSON(t, ReconcileInput{}), nwJSON(t, Reconciled{})); err != nil {
		t.Errorf("Verify after the disconnect: %v", err)
	}
}

func TestW12gReconcileActionDeclaration(t *testing.T) {
	h, _ := nwSetup(t)
	spec := h.nm().instanceReconcile().Spec
	if spec.Name != "npm.instance.reconcile" || spec.Class != core.ClassCompensable || !spec.ForwardOnly || !spec.Converges ||
		spec.MinScope != core.ScopeAdmin || spec.ApplyTimeout != 45*time.Second || len(spec.LockKeys) != 1 || spec.LockKeys[0] != "npm" {
		t.Errorf("action spec %+v, want npm.instance.reconcile, compensable, forward-only, converging, admin, 45s, the npm key", spec)
	}
}
