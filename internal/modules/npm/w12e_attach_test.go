package npm

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/npm/fake"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

// w12eConnectHook calls at before each ConnectNetwork reaches the test Engine.
type w12eConnectHook struct {
	*thDocker
	at func()
}

func (d w12eConnectHook) ConnectNetwork(ctx context.Context, network, container string, ep *dockerapi.EndpointSettings) error {
	d.at()
	return d.thDocker.ConnectNetwork(ctx, network, container, ep)
}

func w12eNoAnswer(_ context.Context, host string) ([]netip.Addr, error) {
	return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
}

func w12eAnswer(addr string) func(context.Context, string) ([]netip.Addr, error) {
	return func(context.Context, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr(addr)}, nil
	}
}

// w12eNoChange fails the test unless the refused attach left NPM off the network with no claim, and journaled
// connects effects, each recorded skipped.
func w12eNoChange(t *testing.T, h *thHost, user dockerapi.Network, connects int) {
	t.Helper()
	if nwEndpoint(t, h, user.ID) != nil || len(nwClaims(t, h, nwNet)) != 0 {
		t.Error("a refused attach left NPM on the network or recorded a claim")
	}
	effects := h.journal.effects()
	if len(effects) != connects {
		t.Errorf("%d effects journaled, want %d", len(effects), connects)
	}
	for _, e := range effects {
		if e.State != core.EffectSkipped {
			t.Errorf("effect %s was recorded %s, want skipped", e.Resource, e.State)
		}
	}
}

// TestW12eAttachReloadsBeforeJoin: nginx still forwards a name that lost its answer to the address it held; the
// reload before the join fails and nothing connects, for a static and for an Engine-chosen address.
func TestW12eAttachReloadsBeforeJoin(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   AttachInput
	}{
		{"static", AttachInput{Network: nwNet, Claimant: nwClaimant, Target: "web", Address: netip.MustParseAddr("10.9.0.7")}},
		{"Engine-chosen", AttachInput{Network: nwNet, Claimant: nwClaimant, Target: "web"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, user := nwSetup(t)
			w12aSubnet(t, h, user)
			h.docker.pin(nwNet, netip.MustParseAddr("10.9.0.7"))
			w12aSeed(h, "up.invalid", 81)
			h.nginx.fail(errNginxTest)
			n := h.nm()
			n.d.resolve = w12eNoAnswer
			h.addChange("c1", "npm.network.attach")
			_, err := n.networkAttach().Handler.Apply(context.Background(), h.env("c1"), nwJSON(t, tc.in))
			coded := nwRefused(t, err, errcode.NPMReserved)
			if want := `nginx could not be reloaded before darkpanel-npm joins network appnet: nginx's configuration test failed`; coded.Detail != want {
				t.Errorf("detail %q, want %q", coded.Detail, want)
			}
			if nwCalls(h, "ConnectNetwork") != 0 {
				t.Error("the refused attach connected NPM")
			}
			w12eNoChange(t, h, user, 0)
		})
	}
}

// TestW12eAttachReloadsAfterJoin: with the name answering another address the attach succeeds, with one reload before
// the connect and one after, also with no enabled proxy host; the undo of a detach does the same.
func TestW12eAttachReloadsAfterJoin(t *testing.T) {
	ctx := context.Background()
	t.Run("attach", func(t *testing.T) {
		h, user := nwSetup(t)
		w12aSubnet(t, h, user)
		w12aSeed(h, "up.invalid", 81)
		n := h.nm()
		n.d.resolve = w12eAnswer("203.0.113.9")
		atConnect := -1
		n.d.docker = w12eConnectHook{h.docker, func() { atConnect = h.nginx.reloads }}
		h.addChange("c1", "npm.network.attach")
		in := AttachInput{Network: nwNet, Claimant: nwClaimant, Target: "web", Address: netip.MustParseAddr("10.9.0.7")}
		if _, err := n.networkAttach().Handler.Apply(ctx, h.env("c1"), nwJSON(t, in)); err != nil {
			t.Fatal(err)
		}
		if atConnect != 1 || h.nginx.reloads != 2 {
			t.Errorf("%d reloads before the connect and %d in all, want 1 and 2", atConnect, h.nginx.reloads)
		}
		if nwEndpoint(t, h, user.ID) == nil {
			t.Error("NPM is not on the network")
		}
	})
	t.Run("no enabled proxy host", func(t *testing.T) {
		h, user := nwSetup(t)
		w12aSubnet(t, h, user)
		w12aSeed(h, "up.invalid", 81)
		h.npm.DisableProxyHost(1)
		n := h.nm()
		n.d.resolve = w12eNoAnswer
		h.addChange("c1", "npm.network.attach")
		in := AttachInput{Network: nwNet, Claimant: nwClaimant, Target: "web"}
		if _, err := n.networkAttach().Handler.Apply(ctx, h.env("c1"), nwJSON(t, in)); err != nil {
			t.Fatal(err)
		}
		if h.nginx.reloads != 2 {
			t.Errorf("%d reloads, want 2: the exec reload runs with no enabled proxy host too", h.nginx.reloads)
		}
	})
	t.Run("undo of a detach", func(t *testing.T) {
		h, user := nwSetup(t)
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
		w12aSeed(h, "up.invalid", 81)
		h.docker.pin(nwNet, netip.MustParseAddr("10.9.0.7"))
		n := h.nm()
		n.d.resolve = w12eAnswer("203.0.113.9")
		atConnect := -1
		n.d.docker = w12eConnectHook{h.docker, func() { atConnect = h.nginx.reloads }}
		if _, err := n.networkDetach().Handler.Inverse(ctx, h.env("u1"), endpoint); err != nil {
			t.Fatal(err)
		}
		if atConnect != 1 || h.nginx.reloads != 2 {
			t.Errorf("%d reloads before the connect and %d in all, want 1 and 2", atConnect, h.nginx.reloads)
		}
		// A repeat finds NPM on the network: it checks the held address and reloads once before it answers done.
		if _, err := n.networkDetach().Handler.Inverse(ctx, h.env("u1"), endpoint); err != nil {
			t.Fatal(err)
		}
		if h.nginx.reloads != 3 {
			t.Errorf("%d reloads after the repeat, want 3", h.nginx.reloads)
		}
		n.d.resolve = w12eAnswer("10.9.0.7")
		_, err := n.networkDetach().Handler.Inverse(ctx, h.env("u1"), endpoint)
		_ = nwRefused(t, err, errcode.NPMReserved)
		if nwEndpoint(t, h, user.ID) != nil {
			t.Error("a repeat whose held address is forwarded left NPM on the network")
		}
	})
}

// TestW12eAttachUnconfirmedReloadDisconnects: a second reload that fails, or a held address that a name now answers,
// disconnects NPM again; a disconnect that fails leaves the effect intended.
func TestW12eAttachUnconfirmedReloadDisconnects(t *testing.T) {
	ctx := context.Background()
	in := AttachInput{Network: nwNet, Claimant: nwClaimant, Target: "web", Address: netip.MustParseAddr("10.9.0.7")}
	t.Run("the second reload fails", func(t *testing.T) {
		h, user := nwSetup(t)
		w12aSubnet(t, h, user)
		w12aSeed(h, "up.invalid", 81)
		n := h.nm()
		n.d.resolve = w12eAnswer("203.0.113.9")
		n.d.docker = w12eConnectHook{h.docker, func() { h.nginx.fail(errNginxTest) }}
		h.addChange("c1", "npm.network.attach")
		_, err := n.networkAttach().Handler.Apply(ctx, h.env("c1"), nwJSON(t, in))
		coded := nwRefused(t, err, errcode.NPMReserved)
		if want := "nginx could not be reloaded after darkpanel-npm joined network appnet: nginx's configuration test failed; it was disconnected again"; coded.Detail != want {
			t.Errorf("detail %q, want %q", coded.Detail, want)
		}
		w12eNoChange(t, h, user, 1)
	})
	t.Run("the second reload's exec fails", func(t *testing.T) {
		h, user := nwSetup(t)
		w12aSubnet(t, h, user)
		w12aSeed(h, "up.invalid", 81)
		n := h.nm()
		n.d.resolve = w12eAnswer("203.0.113.9")
		n.d.docker = w12eConnectHook{h.docker, func() { h.nginx.fail(errors.New("boom")) }}
		h.addChange("c1", "npm.network.attach")
		_, err := n.networkAttach().Handler.Apply(ctx, h.env("c1"), nwJSON(t, in))
		coded := nwRefused(t, err, errcode.NPMUnhealthy)
		if !strings.HasPrefix(coded.Detail, "nginx could not be reloaded after darkpanel-npm joined network appnet: ") ||
			!strings.HasSuffix(coded.Detail, "; it was disconnected again") {
			t.Errorf("detail %q, want the unanswered reload after the join named", coded.Detail)
		}
		w12eNoChange(t, h, user, 1)
	})
	for _, tc := range []struct {
		name string
		in   AttachInput
	}{
		{"the held address is forwarded", in},
		// The Engine picks the address, so only the endpoint NPM got, not the input, names it.
		{"the held address an Engine-chosen join took is forwarded", AttachInput{Network: nwNet, Claimant: nwClaimant, Target: "web"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, user := nwSetup(t)
			w12aSubnet(t, h, user)
			w12aSeed(h, "up.invalid", 81)
			if !tc.in.Address.IsValid() {
				h.docker.pin(nwNet, netip.MustParseAddr("10.9.0.7"))
			}
			n := h.nm()
			// The handler holds its own copy of n, so the hook changes the answer the resolver reads, not n.d.resolve.
			var mu sync.Mutex
			answer := "203.0.113.9"
			n.d.resolve = func(context.Context, string) ([]netip.Addr, error) {
				mu.Lock()
				defer mu.Unlock()
				return []netip.Addr{netip.MustParseAddr(answer)}, nil
			}
			n.d.docker = w12eConnectHook{h.docker, func() {
				mu.Lock()
				defer mu.Unlock()
				answer = "10.9.0.7"
			}}
			h.addChange("c1", "npm.network.attach")
			_, err := n.networkAttach().Handler.Apply(ctx, h.env("c1"), nwJSON(t, tc.in))
			coded := nwRefused(t, err, errcode.NPMReserved)
			if want := "proxy host 1 forwards to up.invalid, which resolves to 10.9.0.7, the address darkpanel-npm took on network appnet; it was disconnected again"; coded.Detail != want {
				t.Errorf("detail %q, want %q", coded.Detail, want)
			}
			w12eNoChange(t, h, user, 1)
		})
	}
	t.Run("the check after the join cannot be made", func(t *testing.T) {
		h, user := nwSetup(t)
		w12aSubnet(t, h, user)
		w12aSeed(h, "up.invalid", 81)
		n := h.nm()
		n.d.resolve = w12eAnswer("203.0.113.9")
		n.d.docker = w12eConnectHook{h.docker, func() {
			for range 8 {
				h.npm.Fail(http.MethodGet, "/api/nginx/proxy-hosts", fake.Fault{Status: http.StatusBadGateway})
			}
		}}
		h.addChange("c1", "npm.network.attach")
		_, err := n.networkAttach().Handler.Apply(ctx, h.env("c1"), nwJSON(t, in))
		_ = nwRefused(t, err, errcode.NPMUnhealthy)
		if !strings.HasSuffix(err.Error(), "; it was disconnected again") {
			t.Errorf("error %q does not say NPM was disconnected again", err)
		}
		w12eNoChange(t, h, user, 1)
	})
	t.Run("the disconnect fails", func(t *testing.T) {
		h, user := nwSetup(t)
		w12aSubnet(t, h, user)
		w12aSeed(h, "up.invalid", 81)
		h.docker.Fail("DisconnectNetwork", errors.New("engine down"))
		n := h.nm()
		n.d.resolve = w12eAnswer("203.0.113.9")
		n.d.docker = w12eConnectHook{h.docker, func() { h.nginx.fail(errNginxTest) }}
		h.addChange("c1", "npm.network.attach")
		_, err := n.networkAttach().Handler.Apply(ctx, h.env("c1"), nwJSON(t, in))
		if err == nil || core.IsDefinite(err) {
			t.Fatalf("error %v, want a failure that is not definite", err)
		}
		intended := false
		for _, e := range h.journal.effects() {
			intended = intended || e.State == core.EffectIntended
		}
		if !intended || nwEndpoint(t, h, user.ID) == nil {
			t.Error("a connect that could not be taken back was not left intended")
		}
	})
}
