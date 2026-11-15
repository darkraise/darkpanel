package npm

import (
	"context"
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

// w12gTrace wraps the test Engine and records the order of the calls an attach makes, with the number of requests NPM
// had received at each.
type w12gTrace struct {
	*thDocker
	h      *thHost
	mu     sync.Mutex
	events []string
	counts []int
	hook   map[string]func()
	strip  string
	lost   error
}

func newW12gTrace(h *thHost) *w12gTrace {
	return &w12gTrace{thDocker: h.docker, h: h, hook: map[string]func(){}}
}

func (d *w12gTrace) rec(ev string) {
	d.mu.Lock()
	d.events = append(d.events, ev)
	d.counts = append(d.counts, len(d.h.npm.Requests()))
	d.mu.Unlock()
}

// index is the position of the first event ev, -1 when there is none.
func (d *w12gTrace) index(ev string) int {
	for i, e := range d.events {
		if e == ev {
			return i
		}
	}
	return -1
}

func (d *w12gTrace) ConnectNetwork(ctx context.Context, network, container string, ep *dockerapi.EndpointSettings) error {
	if f := d.hook["connect"]; f != nil {
		f()
	}
	d.rec("connect")
	err := d.thDocker.ConnectNetwork(ctx, network, container, ep)
	if err == nil && d.lost != nil {
		return d.lost
	}
	return err
}

func (d *w12gTrace) InspectContainer(ctx context.Context, id string) (dockerapi.ContainerJSON, error) {
	c, err := d.thDocker.InspectContainer(ctx, id)
	if err == nil && d.strip != "" && id == containerName {
		delete(c.NetworkSettings.Networks, d.strip)
	}
	d.rec("inspect:" + id)
	return c, err
}

func (d *w12gTrace) DisconnectNetwork(ctx context.Context, network, container string, force bool) error {
	d.rec("disconnect")
	return d.thDocker.DisconnectNetwork(ctx, network, container, force)
}

// after returns the events that follow the first event ev.
func (d *w12gTrace) after(ev string) []string {
	for i, e := range d.events {
		if e == ev {
			return d.events[i+1:]
		}
	}
	return nil
}

const w12gAddr = "10.9.0.7"

// w12gHolder gives appnet a subnet, puts a container holding w12gAddr on it and a proxy host that forwards to it by
// literal and to up.invalid by name; the guard passes while the holder is there. The Engine hands NPM w12gAddr next.
func w12gHolder(t *testing.T, h *thHost, user dockerapi.Network) {
	t.Helper()
	w12aSubnet(t, h, user)
	h.docker.putContainer(thContainer("holder", thEndpoint(nwNet, w12gAddr)))
	w12aSeed(h, w12gAddr, 3000, w12gLoc("/n", "up.invalid"))
	h.docker.pin(nwNet, netip.MustParseAddr(w12gAddr))
}

func w12gAttach(t *testing.T, h *thHost, n nm, in AttachInput) error {
	t.Helper()
	h.addChange("c1", "npm.network.attach")
	_, err := n.networkAttach().Handler.Apply(context.Background(), h.env("c1"), nwJSON(t, in))
	return err
}

func TestW12gAttachMakesNoCallBetweenConnectAndInspection(t *testing.T) {
	h, user := nwSetup(t)
	w12aSubnet(t, h, user)
	w12aSeed(h, "up.invalid", 81)
	tr := newW12gTrace(h)
	n := h.nm()
	n.d.docker = tr
	n.d.resolve = func(context.Context, string) ([]netip.Addr, error) {
		tr.rec("resolve")
		return []netip.Addr{netip.MustParseAddr("203.0.113.9")}, nil
	}
	if err := w12gAttach(t, h, n, AttachInput{Network: nwNet, Claimant: nwClaimant, Target: "web"}); err != nil {
		t.Fatal(err)
	}
	ci := tr.index("connect")
	if ci < 0 || ci+1 >= len(tr.events) || tr.events[ci+1] != "inspect:"+containerName {
		t.Fatalf("events %v, want the inspection of NPM right after the connect", tr.events)
	}
	if tr.counts[ci] != tr.counts[ci+1] {
		t.Errorf("NPM got %d requests at the connect and %d at the next inspection; a listing sits in between", tr.counts[ci], tr.counts[ci+1])
	}
}

func TestW12gAttachDisconnectsBeforeAnyLookupOrReload(t *testing.T) {
	h, user := nwSetup(t)
	w12gHolder(t, h, user)
	tr := newW12gTrace(h)
	tr.hook["connect"] = func() {
		if err := h.docker.RemoveContainer(context.Background(), "holder", dockerapi.RemoveContainerOptions{Force: true}); err != nil {
			t.Error(err)
		}
	}
	h.nginx.onReload = func() { tr.rec("reload") }
	n := h.nm()
	n.d.docker = tr
	n.d.resolve = func(context.Context, string) ([]netip.Addr, error) {
		tr.rec("resolve")
		return []netip.Addr{netip.MustParseAddr("203.0.113.9")}, nil
	}
	err := w12gAttach(t, h, n, AttachInput{Network: nwNet, Claimant: nwClaimant, Target: "web"})
	_ = nwRefused(t, err, errcode.NPMReserved)
	between := tr.after("connect")
	cut := 0
	for i, e := range between {
		if e == "disconnect" {
			cut = i
			break
		}
	}
	for _, e := range between[:cut] {
		if e == "reload" || e == "resolve" {
			t.Errorf("%q ran between the connect and the disconnect: %v", e, between)
		}
	}
	if h.nginx.reloads != 1 {
		t.Errorf("%d reloads, want only the one before the join", h.nginx.reloads)
	}
	w12eNoChange(t, h, user, 1)
}

func TestW12gAttachRereadsTheNetworkAfterTheFirstReload(t *testing.T) {
	h, user := nwSetup(t)
	w12gHolder(t, h, user)
	h.nginx.onReload = func() {
		if err := h.docker.RemoveContainer(context.Background(), "holder", dockerapi.RemoveContainerOptions{Force: true}); err != nil {
			t.Error(err)
		}
	}
	n := h.nm()
	n.d.resolve = w12eAnswer("203.0.113.9")
	err := w12gAttach(t, h, n, AttachInput{Network: nwNet, Claimant: nwClaimant, Target: "web"})
	_ = nwRefused(t, err, errcode.NPMReserved)
	if nwCalls(h, "ConnectNetwork") != 0 {
		t.Error("the attach connected NPM to an address a proxy host forwards to")
	}
	w12eNoChange(t, h, user, 0)
}

func TestW12gReloadRefusals(t *testing.T) {
	for _, tc := range []struct {
		name   string
		seed   func(h *thHost)
		fail   error
		code   errcode.Code
		detail string
	}{
		{"the test fails, no unanswered forward", func(h *thHost) { w12aSeed(h, "up.invalid", 81) }, errNginxTest, errcode.NPMReserved,
			"nginx could not be reloaded before darkpanel-npm joins network appnet: nginx's configuration test failed"},
		{"the test fails, a location has no answer", func(h *thHost) { w12aSeed(h, "up.invalid", 81, w12gLoc("/a", "gone.invalid")) }, errNginxTest,
			errcode.NPMForwardUnanswered, "location /a of proxy host 1 forwards to gone.invalid, which has no answer"},
		{"the exec fails", func(h *thHost) { w12aSeed(h, "up.invalid", 81) }, errors.New("boom"), errcode.NPMUnhealthy,
			"nginx could not be reloaded before darkpanel-npm joins network appnet: boom"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, user := nwSetup(t)
			w12aSubnet(t, h, user)
			tc.seed(h)
			h.nginx.fail(tc.fail)
			n := h.nm()
			n.d.resolve = w12eNoAnswer
			err := w12gAttach(t, h, n, AttachInput{Network: nwNet, Claimant: nwClaimant, Target: "web"})
			coded := nwRefused(t, err, tc.code)
			if coded.Detail != tc.detail {
				t.Errorf("detail %q, want %q", coded.Detail, tc.detail)
			}
			if nwCalls(h, "ConnectNetwork") != 0 {
				t.Error("a refused reload was followed by a connect")
			}
		})
	}
}

func TestW12gSecondReloadFailureDisconnectsBeforeTheDetail(t *testing.T) {
	h, user := nwSetup(t)
	w12aSubnet(t, h, user)
	w12aSeed(h, "up.invalid", 81)
	tr := newW12gTrace(h)
	n := h.nm()
	n.d.docker = tr
	n.d.resolve = w12eAnswer("203.0.113.9")
	h.nginx.fail(nil)
	h.nginx.fail(errNginxTest)
	err := w12gAttach(t, h, n, AttachInput{Network: nwNet, Claimant: nwClaimant, Target: "web"})
	coded := nwRefused(t, err, errcode.NPMReserved)
	if want := "nginx could not be reloaded after darkpanel-npm joined network appnet: nginx's configuration test failed; it was disconnected again"; coded.Detail != want {
		t.Errorf("detail %q, want %q", coded.Detail, want)
	}
	di := tr.index("disconnect")
	if di < 0 {
		t.Fatalf("events %v hold no disconnect", tr.events)
	}
	if got := len(h.npm.Requests()); got <= tr.counts[di] {
		t.Errorf("NPM had %d requests at the disconnect and %d at the end: the detail's listing did not follow the disconnect", tr.counts[di], got)
	}
	w12eNoChange(t, h, user, 1)
}

func TestW12gAttachOnAJoinedNetwork(t *testing.T) {
	in := AttachInput{Network: nwNet, Claimant: nwClaimant, Target: "web"}
	setup := func(t *testing.T, forward string) (*thHost, dockerapi.Network) {
		h, user := nwSetup(t)
		w12aSubnet(t, h, user)
		nwConnect(t, h, user, nwStatic(w12gAddr))
		w12aSeed(h, forward, 3000)
		return h, user
	}
	t.Run("no claim and a hit: NPM leaves before any reload", func(t *testing.T) {
		h, user := setup(t, w12gAddr)
		n := h.nm()
		n.d.resolve = w12eNoAnswer
		_ = nwRefused(t, w12gAttach(t, h, n, in), errcode.NPMReserved)
		if nwEndpoint(t, h, user.ID) != nil {
			t.Error("NPM stayed on a network where it holds a forwarded address")
		}
		if h.nginx.reloads != 0 {
			t.Errorf("%d reloads, want none: a hit disconnects NPM before nginx is reloaded", h.nginx.reloads)
		}
	})
	t.Run("no claim and no hit: one reload and the claim", func(t *testing.T) {
		h, user := setup(t, "10.9.0.40")
		n := h.nm()
		n.d.resolve = w12eNoAnswer
		if err := w12gAttach(t, h, n, in); err != nil {
			t.Fatal(err)
		}
		if h.nginx.reloads != 1 || nwEndpoint(t, h, user.ID) == nil || len(nwClaims(t, h, nwNet)) != 1 {
			t.Errorf("%d reloads, on the network %v, %d claims; want 1, yes and 1", h.nginx.reloads, nwEndpoint(t, h, user.ID) != nil, len(nwClaims(t, h, nwNet)))
		}
	})
	t.Run("a claim exists: the attach only adds a claim", func(t *testing.T) {
		h, user := setup(t, w12gAddr)
		nwClaim(t, h, nwNet, "someone-else", "c0", true)
		n := h.nm()
		n.d.resolve = w12eNoAnswer
		before := len(h.npm.Requests())
		if err := w12gAttach(t, h, n, in); err != nil {
			t.Fatal(err)
		}
		if h.nginx.reloads != 0 || len(h.npm.Requests()) != before || nwEndpoint(t, h, user.ID) == nil {
			t.Errorf("a claim-only attach made %d reloads, %d NPM requests and left NPM on the network %v; want 0, 0 and yes",
				h.nginx.reloads, len(h.npm.Requests())-before, nwEndpoint(t, h, user.ID) != nil)
		}
	})
	t.Run("darkpanel-npm with a hit: NPM stays on it and the attach fails", func(t *testing.T) {
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
		n := h.nm()
		n.d.resolve = w12eNoAnswer
		_ = nwRefused(t, w12gAttach(t, h, n, AttachInput{Network: networkName, Claimant: nwClaimant}), errcode.NPMReserved)
		if nwEndpoint(t, h, own.ID) == nil || nwCalls(h, "DisconnectNetwork") != 0 {
			t.Error("an attach on darkpanel-npm took NPM off it")
		}
	})
}

func TestW12gAttachAndDetachTakeNoDockerKey(t *testing.T) {
	h, _ := nwSetup(t)
	for _, spec := range []core.ActionSpec{h.nm().networkAttach().Spec, h.nm().networkDetach().Spec} {
		for _, k := range spec.LockKeys {
			if k == "docker" {
				t.Errorf("%s takes the docker key: it would close the undo window of every composite that attaches NPM", spec.Name)
			}
		}
	}
}

func TestW12gAttachWhoseConnectAnswerIsLostChecksTheAddress(t *testing.T) {
	h, user := nwSetup(t)
	w12gHolder(t, h, user)
	tr := newW12gTrace(h)
	tr.hook["connect"] = func() {
		if err := h.docker.RemoveContainer(context.Background(), "holder", dockerapi.RemoveContainerOptions{Force: true}); err != nil {
			t.Error(err)
		}
	}
	tr.lost = errors.New("connection reset by peer")
	n := h.nm()
	n.d.docker = tr
	n.d.resolve = w12eAnswer("203.0.113.9")
	if err := w12gAttach(t, h, n, AttachInput{Network: nwNet, Claimant: nwClaimant, Target: "web"}); err == nil {
		t.Fatal("an attach whose connect answer was lost succeeded")
	}
	if nwEndpoint(t, h, user.ID) != nil {
		t.Error("NPM stayed on a network where it holds a forwarded address after a lost connect answer")
	}
	if tr.index("disconnect") < 0 {
		t.Errorf("events %v hold no disconnect", tr.events)
	}
}

// w12gUndoEffect detaches NPM from appnet and returns the effect the undo of that detach takes.
func w12gUndoEffect(t *testing.T, h *thHost, user dockerapi.Network) core.Effect {
	t.Helper()
	w12aSubnet(t, h, user)
	nwSeedAttached(t, h, user, nwClaimant, false)
	if _, err := nwApplyDetach(t, h, "c2", DetachInput{Network: nwNet, Claimant: nwClaimant}); err != nil {
		t.Fatal(err)
	}
	var endpoint core.Effect
	for _, e := range h.journal.effects() {
		if e.Kind == core.EffectModify {
			endpoint = e
		}
	}
	return endpoint
}

func TestW12gUndoOfADetachChecksInTheSameOrder(t *testing.T) {
	ctx := context.Background()
	t.Run("a freed address is caught right after the connect", func(t *testing.T) {
		h, user := nwSetup(t)
		endpoint := w12gUndoEffect(t, h, user)
		h.docker.putContainer(thContainer("holder", thEndpoint(nwNet, w12gAddr)))
		w12aSeed(h, w12gAddr, 3000, w12gLoc("/n", "up.invalid"))
		h.docker.pin(nwNet, netip.MustParseAddr(w12gAddr))
		tr := newW12gTrace(h)
		tr.hook["connect"] = func() {
			if err := h.docker.RemoveContainer(ctx, "holder", dockerapi.RemoveContainerOptions{Force: true}); err != nil {
				t.Error(err)
			}
		}
		h.nginx.onReload = func() { tr.rec("reload") }
		n := h.nm()
		n.d.docker = tr
		n.d.resolve = func(context.Context, string) ([]netip.Addr, error) {
			tr.rec("resolve")
			return []netip.Addr{netip.MustParseAddr("203.0.113.9")}, nil
		}
		_, err := n.networkDetach().Handler.Inverse(ctx, h.env("u1"), endpoint)
		_ = nwRefused(t, err, errcode.NPMReserved)
		for _, e := range tr.after("connect") {
			if e == "disconnect" {
				break
			}
			if e == "reload" || e == "resolve" {
				t.Errorf("%q ran between the connect and the disconnect: %v", e, tr.after("connect"))
			}
		}
		if nwEndpoint(t, h, user.ID) != nil {
			t.Error("the undo left NPM on a network where it holds a forwarded address")
		}
	})
	t.Run("the network is read again after the first reload", func(t *testing.T) {
		h, user := nwSetup(t)
		endpoint := w12gUndoEffect(t, h, user)
		h.docker.putContainer(thContainer("holder", thEndpoint(nwNet, w12gAddr)))
		w12aSeed(h, w12gAddr, 3000, w12gLoc("/n", "up.invalid"))
		h.docker.pin(nwNet, netip.MustParseAddr(w12gAddr))
		h.nginx.onReload = func() {
			if err := h.docker.RemoveContainer(ctx, "holder", dockerapi.RemoveContainerOptions{Force: true}); err != nil {
				t.Error(err)
			}
		}
		tr := newW12gTrace(h)
		n := h.nm()
		n.d.docker = tr
		n.d.resolve = w12eAnswer("203.0.113.9")
		_, err := n.networkDetach().Handler.Inverse(ctx, h.env("u1"), endpoint)
		_ = nwRefused(t, err, errcode.NPMReserved)
		if tr.index("connect") >= 0 {
			t.Errorf("events %v: the undo connected NPM to an address a proxy host forwards to", tr.events)
		}
	})
}

func TestW12gUndoOfADetachLeavesANetworkAnotherClaimHolds(t *testing.T) {
	h, user := nwSetup(t)
	endpoint := w12gUndoEffect(t, h, user)
	nwConnect(t, h, user, nil)
	nwClaim(t, h, nwNet, nwOther, "c3", false)
	w12aSeed(h, "up.invalid", 3000)
	h.nginx.fail(errNginxTest)
	n := h.nm()
	n.d.resolve = w12eAnswer("203.0.113.9")
	if _, err := n.networkDetach().Handler.Inverse(context.Background(), h.env("u1"), endpoint); err != nil {
		t.Fatalf("undo error %v, want done", err)
	}
	if h.nginx.reloads != 0 {
		t.Errorf("%d reloads, want none on a network another claim holds", h.nginx.reloads)
	}
	if nwEndpoint(t, h, user.ID) == nil {
		t.Error("the undo disconnected NPM from a network another claim holds")
	}
}

func TestW12gAJoinedNPMWithNoEndpointIsDisconnected(t *testing.T) {
	h, user := nwSetup(t)
	w12aSubnet(t, h, user)
	w12aSeed(h, "up.invalid", 81)
	tr := newW12gTrace(h)
	tr.strip = nwNet
	n := h.nm()
	n.d.docker = tr
	n.d.resolve = w12eAnswer("203.0.113.9")
	err := w12gAttach(t, h, n, AttachInput{Network: nwNet, Claimant: nwClaimant, Target: "web"})
	if err == nil || !core.IsDefinite(err) || !strings.HasSuffix(err.Error(), "; it was disconnected again") {
		t.Fatalf("error %v, want a definite refusal that says NPM was disconnected again", err)
	}
	w12eNoChange(t, h, user, 1)
}

func TestW12gAttachDeadlines(t *testing.T) {
	h, _ := nwSetup(t)
	need := 2*(resolveTimeout+time.Second) + 2*nginxReloadWait + nginxDisconnectWait
	if got := h.nm().networkAttach().Spec.ApplyTimeout; need > got {
		t.Errorf("two lookup passes, two reloads and a disconnect take up to %v, more than the attach's %v", need, got)
	}
	if got := h.nm().networkDetach().Spec.ApplyTimeout; need > got {
		t.Errorf("two lookup passes, two reloads and a disconnect take up to %v, more than the detach's %v", need, got)
	}
}
