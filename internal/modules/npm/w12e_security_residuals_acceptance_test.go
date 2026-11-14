package npm

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/npm/fake"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

const qaHeld = "10.9.0.7" // the address A of the statements: where NPM would land on appnet

// qaReloads counts the proxy-host updates NPM received; the module reloads nginx through them.
func qaReloads(h *thHost) int {
	n := 0
	for _, r := range h.npm.Requests() {
		if r.Method == http.MethodPut && strings.HasPrefix(r.Path, "/api/nginx/proxy-hosts/") {
			n++
		}
	}
	return n
}

// qaConnectHook runs before just before and after just after each ConnectNetwork that reaches the Engine.
type qaConnectHook struct {
	dockerAPI
	before, after func()
}

func (d qaConnectHook) ConnectNetwork(ctx context.Context, network, container string, ep *dockerapi.EndpointSettings) error {
	if d.before != nil {
		d.before()
	}
	err := d.dockerAPI.ConnectNetwork(ctx, network, container, ep)
	if err == nil && d.after != nil {
		d.after()
	}
	return err
}

func qaNoAnswer(_ context.Context, host string) ([]netip.Addr, error) {
	return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
}

func qaAnswers(addr string) func(context.Context, string) ([]netip.Addr, error) {
	return func(context.Context, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr(addr)}, nil
	}
}

// qaAttachInput is an attach of NPM to appnet where it would take qaHeld: at a static address, or the one the Engine
// chooses, which the test Engine is pinned to.
func qaAttachInput(h *thHost, static bool) AttachInput {
	in := AttachInput{Network: nwNet, Claimant: nwClaimant, Target: "web"}
	if static {
		in.Address = netip.MustParseAddr(qaHeld)
	} else {
		h.docker.pin(nwNet, netip.MustParseAddr(qaHeld))
	}
	return in
}

// qaEndpointEffect is the effect of the connect (or disconnect) of NPM on appnet.
func qaEndpointEffect(t *testing.T, h *thHost) core.Effect {
	t.Helper()
	for _, e := range h.journal.effects() {
		if e.Resource == "npm.endpoint:"+nwNet {
			return e
		}
	}
	t.Fatalf("no effect for npm.endpoint:%s in %+v", nwNet, h.journal.effects())
	return core.Effect{}
}

func qaApplyAttach(t *testing.T, n nm, h *thHost, change string, in AttachInput) error {
	t.Helper()
	h.addChange(change, "npm.network.attach")
	_, err := n.networkAttach().Handler.Apply(context.Background(), h.env(change), nwJSON(t, in))
	return err
}

// TestA1_ANameWithoutAnAnswerRefusesTheJoinBeforeAnyConnect: a proxy host forwards to up.invalid, which answered the
// address NPM would take; once the name has no answer nginx can no longer be reloaded, and the attach is refused with
// npm.reserved before any connect. Once the name answers another address the attach succeeds, with one confirmed reload
// before the connect and one after.
func TestA1_ANameWithoutAnAnswerRefusesTheJoinBeforeAnyConnect(t *testing.T) {
	for _, static := range []bool{true, false} {
		kind := map[bool]string{true: "a static address", false: "an Engine-chosen address"}[static]
		t.Run("no answer, "+kind, func(t *testing.T) {
			h, user := nwSetup(t)
			w12aSubnet(t, h, user)
			w12aSeed(h, "up.invalid", 81)
			h.npm.SetNginxOnline(false, `host not found in upstream "up.invalid"`)
			n := h.nm()
			n.d.resolve = qaNoAnswer
			err := qaApplyAttach(t, n, h, "c1", qaAttachInput(h, static))
			nwRefused(t, err, errcode.NPMReserved)
			if got := nwCalls(h, "ConnectNetwork"); got != 0 {
				t.Errorf("the refused attach made %d ConnectNetwork calls, want none", got)
			}
			if nwEndpoint(t, h, user.ID) != nil {
				t.Error("NPM is on the network after a refused attach")
			}
		})
		t.Run("another answer, "+kind, func(t *testing.T) {
			h, user := nwSetup(t)
			w12aSubnet(t, h, user)
			w12aSeed(h, "up.invalid", 81)
			n := h.nm()
			n.d.resolve = qaAnswers("203.0.113.9")
			atConnect := -1
			n.d.docker = qaConnectHook{dockerAPI: n.d.docker, before: func() { atConnect = qaReloads(h) }}
			if err := qaApplyAttach(t, n, h, "c1", qaAttachInput(h, static)); err != nil {
				t.Fatalf("the attach failed: %v", err)
			}
			if atConnect != 1 || qaReloads(h) != 2 {
				t.Errorf("%d reloads before the connect and %d in all, want one before and one after", atConnect, qaReloads(h))
			}
			if nwEndpoint(t, h, user.ID) == nil {
				t.Error("NPM is not on the network")
			}
		})
	}
}

// TestA2_AConfirmedAttachReloadsOnceBeforeAndOnceAfterTheConnect covers the attach, the undo of a detach and its
// already-on-the-network answer, and the case of no enabled proxy host.
func TestA2_AConfirmedAttachReloadsOnceBeforeAndOnceAfterTheConnect(t *testing.T) {
	ctx := context.Background()
	t.Run("an attach whose held address is no target", func(t *testing.T) {
		h, user := nwSetup(t)
		w12aSubnet(t, h, user)
		w12aSeed(h, "10.9.0.20", 3000) // a literal address NPM does not take
		h.docker.putContainer(thContainer("db", thEndpoint(nwNet, "10.9.0.20")))
		n := h.nm()
		atConnect := -1
		n.d.docker = qaConnectHook{dockerAPI: n.d.docker, before: func() { atConnect = qaReloads(h) }}
		if err := qaApplyAttach(t, n, h, "c1", qaAttachInput(h, false)); err != nil {
			t.Fatalf("the attach failed: %v", err)
		}
		if atConnect != 1 || qaReloads(h) != 2 {
			t.Errorf("%d reloads before the connect and %d in all, want 1 and 2", atConnect, qaReloads(h))
		}
		if nwEndpoint(t, h, user.ID) == nil {
			t.Error("NPM is not on the network")
		}
	})
	t.Run("no enabled proxy host", func(t *testing.T) {
		for name, seed := range map[string]func(h *thHost){
			"no proxy host": func(*thHost) {},
			"a disabled one": func(h *thHost) {
				w12aSeed(h, "up.invalid", 81)
				h.npm.DisableProxyHost(1)
			},
		} {
			t.Run(name, func(t *testing.T) {
				h, user := nwSetup(t)
				w12aSubnet(t, h, user)
				seed(h)
				n := h.nm()
				n.d.resolve = qaAnswers("203.0.113.9")
				if err := qaApplyAttach(t, n, h, "c1", qaAttachInput(h, true)); err != nil {
					t.Fatalf("the attach failed: %v", err)
				}
				if got := qaReloads(h); got != 0 {
					t.Errorf("%d reloads, want none with no enabled proxy host", got)
				}
				if nwEndpoint(t, h, user.ID) == nil {
					t.Error("NPM is not on the network")
				}
			})
		}
	})

	detached := func(t *testing.T) (*thHost, dockerapi.Network, core.Effect) {
		t.Helper()
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
		if nwEndpoint(t, h, user.ID) != nil {
			t.Fatal("the detach left NPM on the network")
		}
		return h, user, endpoint
	}
	t.Run("the undo of a detach reloads before and after the connect", func(t *testing.T) {
		h, user, endpoint := detached(t)
		w12aSeed(h, "up.invalid", 81)
		n := h.nm()
		n.d.resolve = qaAnswers("203.0.113.9")
		base := qaReloads(h)
		atConnect := -1
		n.d.docker = qaConnectHook{dockerAPI: n.d.docker, before: func() { atConnect = qaReloads(h) - base }}
		if _, err := n.networkDetach().Handler.Inverse(ctx, h.env("u1"), endpoint); err != nil {
			t.Fatalf("the undo failed: %v", err)
		}
		if atConnect != 1 || qaReloads(h)-base != 2 {
			t.Errorf("%d reloads before the connect and %d in all, want 1 and 2", atConnect, qaReloads(h)-base)
		}
		if nwEndpoint(t, h, user.ID) == nil {
			t.Error("NPM is not on the network after the undo")
		}
	})
	t.Run("the undo of a detach with no enabled proxy host reloads nothing", func(t *testing.T) {
		h, user, endpoint := detached(t)
		if _, err := h.nm().networkDetach().Handler.Inverse(ctx, h.env("u1"), endpoint); err != nil {
			t.Fatalf("the undo failed: %v", err)
		}
		if got := qaReloads(h); got != 0 {
			t.Errorf("%d reloads, want none", got)
		}
		if nwEndpoint(t, h, user.ID) == nil {
			t.Error("NPM is not on the network after the undo")
		}
	})
	t.Run("the already-on-the-network answer reloads first", func(t *testing.T) {
		h, user, endpoint := detached(t)
		w12aSeed(h, "up.invalid", 81)
		n := h.nm()
		n.d.resolve = qaAnswers("203.0.113.9")
		nwConnect(t, h, user, nwStatic("10.9.0.30")) // an earlier run connected NPM and was cut off
		base := qaReloads(h)
		if _, err := n.networkDetach().Handler.Inverse(ctx, h.env("u1"), endpoint); err != nil {
			t.Fatalf("the undo with NPM already on the network failed: %v", err)
		}
		if got := qaReloads(h) - base; got != 1 {
			t.Errorf("%d reloads, want 1 before the answer", got)
		}
	})
	t.Run("the already-on-the-network answer runs the held-address check", func(t *testing.T) {
		h, user, endpoint := detached(t)
		w12aSeed(h, qaHeld, 3000)
		nwConnect(t, h, user, nwStatic(qaHeld)) // NPM holds an address a proxy host forwards to
		_, err := h.nm().networkDetach().Handler.Inverse(ctx, h.env("u1"), endpoint)
		if errcode.From(err).Code != errcode.NPMReserved {
			t.Errorf("the undo answered %v, want npm.reserved for a held address that is a target", err)
		}
	})
}

// TestA3_AnUnconfirmedSecondReloadOrAHeldTargetDisconnectsNPMAgain: the join is undone and fails with npm.reserved, the
// connect's effect recorded skipped; when the disconnect fails the effect stays intended and the error carries no
// Definite mark.
func TestA3_AnUnconfirmedSecondReloadOrAHeldTargetDisconnectsNPMAgain(t *testing.T) {
	type scenario struct {
		name string
		// arm makes the second reload fail, or the held address a target; it runs once the connect happened.
		setup func(h *thHost, n *nm)
	}
	var scenarios = []scenario{
		{"an nginx -t failure", func(h *thHost, n *nm) {
			w12aSeed(h, "up.invalid", 81)
			n.d.resolve = qaAnswers("203.0.113.9")
			n.d.docker = qaConnectHook{dockerAPI: n.d.docker, after: func() {
				h.npm.SetNginxOnline(false, `nginx: [emerg] host not found in upstream "up.invalid" in /data/nginx/proxy_host/1.conf:42`)
			}}
		}},
		{"nginx_online false without a reason", func(h *thHost, n *nm) {
			w12aSeed(h, "up.invalid", 81)
			n.d.resolve = qaAnswers("203.0.113.9")
			n.d.docker = qaConnectHook{dockerAPI: n.d.docker, after: func() { h.npm.SetNginxOnline(false, "") }}
		}},
		{"NPM's API not answering the reload", func(h *thHost, n *nm) {
			w12aSeed(h, "up.invalid", 81)
			n.d.resolve = qaAnswers("203.0.113.9")
			n.d.docker = qaConnectHook{dockerAPI: n.d.docker, after: func() {
				for range 8 {
					h.npm.Fail(http.MethodPut, "/api/nginx/proxy-hosts/1", fake.Fault{Status: http.StatusBadGateway})
				}
			}}
		}},
		{"a held address that is a target", func(h *thHost, n *nm) {
			h.docker.putContainer(thContainer("db", thEndpoint(nwNet, qaHeld))) // holds A while NPM is planned
			w12aSeed(h, qaHeld, 3000)
			n.d.docker = qaConnectHook{dockerAPI: n.d.docker, before: func() {
				// the holder goes away between the check and the connect
				if err := h.docker.RemoveContainer(context.Background(), "db", dockerapi.RemoveContainerOptions{Force: true}); err != nil {
					t.Error(err)
				}
			}}
		}},
	}
	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			h, user := nwSetup(t)
			w12aSubnet(t, h, user)
			n := h.nm()
			sc.setup(h, &n)
			in := qaAttachInput(h, false)
			err := qaApplyAttach(t, n, h, "c1", in)
			if errcode.From(err).Code != errcode.NPMReserved {
				t.Fatalf("error %v, want npm.reserved", err)
			}
			if nwEndpoint(t, h, user.ID) != nil {
				t.Error("NPM is still on the network")
			}
			if nwCalls(h, "DisconnectNetwork") != 1 {
				t.Errorf("%d disconnects, want 1", nwCalls(h, "DisconnectNetwork"))
			}
			if e := qaEndpointEffect(t, h); e.State != core.EffectSkipped {
				t.Errorf("the connect's effect is %s, want skipped", e.State)
			}
		})
		t.Run(sc.name+", and the disconnect fails", func(t *testing.T) {
			h, user := nwSetup(t)
			w12aSubnet(t, h, user)
			n := h.nm()
			sc.setup(h, &n)
			h.docker.Fail("DisconnectNetwork", thAPIError(http.StatusInternalServerError, "engine down"))
			err := qaApplyAttach(t, n, h, "c1", qaAttachInput(h, false))
			if err == nil {
				t.Fatal("the attach succeeded")
			}
			if core.IsDefinite(err) {
				t.Errorf("error %v is Definite; a join that could not be undone must settle needs_repair", err)
			}
			if e := qaEndpointEffect(t, h); e.State != core.EffectIntended {
				t.Errorf("the connect's effect is %s, want intended", e.State)
			}
			if nwEndpoint(t, h, user.ID) == nil {
				t.Error("NPM is not on the network although the disconnect failed")
			}
		})
	}
}

// TestA3_AWholeAPIOutageAfterTheConnectDisconnectsNPMAgain: when NPM's API stops answering altogether after the connect,
// the held-address check cannot list the proxy hosts, so the statement's "API not answering" can be read as this
// outage or as the reload call alone (the test above). Under both readings NPM is disconnected again with the connect's
// effect skipped; the error code is logged, not asserted.
func TestA3_AWholeAPIOutageAfterTheConnectDisconnectsNPMAgain(t *testing.T) {
	h, user := nwSetup(t)
	w12aSubnet(t, h, user)
	w12aSeed(h, "up.invalid", 81)
	n := h.nm()
	n.d.resolve = qaAnswers("203.0.113.9")
	n.d.docker = qaConnectHook{dockerAPI: n.d.docker, after: func() {
		for range 8 {
			h.npm.Fail(http.MethodGet, "/api/nginx/proxy-hosts", fake.Fault{Status: http.StatusBadGateway})
		}
	}}
	err := qaApplyAttach(t, n, h, "c1", qaAttachInput(h, false))
	if err == nil {
		t.Fatal("the attach succeeded while NPM's API did not answer after the connect")
	}
	t.Logf("error code %s: %v", errcode.From(err).Code, err)
	if nwEndpoint(t, h, user.ID) != nil {
		t.Error("NPM is still on the network")
	}
	if e := qaEndpointEffect(t, h); e.State != core.EffectSkipped {
		t.Errorf("the connect's effect is %s, want skipped", e.State)
	}
}
