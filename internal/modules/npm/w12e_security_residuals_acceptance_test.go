package npm

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/npm/fake"
	"github.com/darkraise/darkpanel/internal/modules/npm/npmapi"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

const qaHeld = "10.9.0.7" // the address A of the statements: where NPM would land on appnet

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
			h.nginx.fail(errNginxTest)
			n := h.nm()
			n.d.resolve = qaNoAnswer
			err := qaApplyAttach(t, n, h, "c1", qaAttachInput(h, static))
			_ = nwRefused(t, err, errcode.NPMReserved)
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
			n.d.docker = qaConnectHook{dockerAPI: n.d.docker, before: func() { atConnect = h.nginx.reloads }}
			if err := qaApplyAttach(t, n, h, "c1", qaAttachInput(h, static)); err != nil {
				t.Fatalf("the attach failed: %v", err)
			}
			if atConnect != 1 || h.nginx.reloads != 2 {
				t.Errorf("%d reloads before the connect and %d in all, want one before and one after", atConnect, h.nginx.reloads)
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
		n.d.docker = qaConnectHook{dockerAPI: n.d.docker, before: func() { atConnect = h.nginx.reloads }}
		if err := qaApplyAttach(t, n, h, "c1", qaAttachInput(h, false)); err != nil {
			t.Fatalf("the attach failed: %v", err)
		}
		if atConnect != 1 || h.nginx.reloads != 2 {
			t.Errorf("%d reloads before the connect and %d in all, want 1 and 2", atConnect, h.nginx.reloads)
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
				if got := h.nginx.reloads; got != 2 {
					t.Errorf("%d reloads, want 2: the exec reload runs with no enabled proxy host too", got)
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
		base := h.nginx.reloads
		atConnect := -1
		n.d.docker = qaConnectHook{dockerAPI: n.d.docker, before: func() { atConnect = h.nginx.reloads - base }}
		if _, err := n.networkDetach().Handler.Inverse(ctx, h.env("u1"), endpoint); err != nil {
			t.Fatalf("the undo failed: %v", err)
		}
		if atConnect != 1 || h.nginx.reloads-base != 2 {
			t.Errorf("%d reloads before the connect and %d in all, want 1 and 2", atConnect, h.nginx.reloads-base)
		}
		if nwEndpoint(t, h, user.ID) == nil {
			t.Error("NPM is not on the network after the undo")
		}
	})
	t.Run("the undo of a detach with no enabled proxy host reloads before and after the connect", func(t *testing.T) {
		h, user, endpoint := detached(t)
		if _, err := h.nm().networkDetach().Handler.Inverse(ctx, h.env("u1"), endpoint); err != nil {
			t.Fatalf("the undo failed: %v", err)
		}
		if got := h.nginx.reloads; got != 2 {
			t.Errorf("%d reloads, want 2: the exec reload runs with no enabled proxy host too", got)
		}
		if nwEndpoint(t, h, user.ID) == nil {
			t.Error("NPM is not on the network after the undo")
		}
	})
	t.Run("the already-on-the-network answer reloads once", func(t *testing.T) {
		h, user, endpoint := detached(t)
		w12aSeed(h, "up.invalid", 81)
		n := h.nm()
		n.d.resolve = qaAnswers("203.0.113.9")
		nwConnect(t, h, user, nwStatic("10.9.0.30")) // an earlier run connected NPM and was cut off
		base := h.nginx.reloads
		if _, err := n.networkDetach().Handler.Inverse(ctx, h.env("u1"), endpoint); err != nil {
			t.Fatalf("the undo with NPM already on the network failed: %v", err)
		}
		if got := h.nginx.reloads - base; got != 1 {
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

// TestA3_AnUnconfirmedSecondReloadOrAHeldTargetDisconnectsNPMAgain: the join is undone and fails with npm.reserved (a
// failed nginx -t or a held target) or npm.unhealthy (an exec that failed or a reload no new worker confirmed), the
// connect's effect recorded skipped; when the disconnect fails the effect stays intended and the error carries no
// Definite mark.
func TestA3_AnUnconfirmedSecondReloadOrAHeldTargetDisconnectsNPMAgain(t *testing.T) {
	type scenario struct {
		name string
		// arm makes the second reload fail, or the held address a target; it runs once the connect happened.
		setup func(h *thHost, n *nm)
		code  errcode.Code
	}
	var scenarios = []scenario{
		{"an nginx -t failure", func(h *thHost, n *nm) {
			w12aSeed(h, "up.invalid", 81)
			n.d.resolve = qaAnswers("203.0.113.9")
			n.d.docker = qaConnectHook{dockerAPI: n.d.docker, after: func() { h.nginx.fail(errNginxTest) }}
		}, errcode.NPMReserved},
		{"a reload no new worker confirms", func(h *thHost, n *nm) {
			w12aSeed(h, "up.invalid", 81)
			n.d.resolve = qaAnswers("203.0.113.9")
			n.d.docker = qaConnectHook{dockerAPI: n.d.docker, after: func() {
				h.nginx.fail(errors.New("npm: nginx reload: no new nginx worker after the reload"))
			}}
		}, errcode.NPMUnhealthy},
		{"the reload's exec failing", func(h *thHost, n *nm) {
			w12aSeed(h, "up.invalid", 81)
			n.d.resolve = qaAnswers("203.0.113.9")
			n.d.docker = qaConnectHook{dockerAPI: n.d.docker, after: func() {
				h.nginx.fail(errors.New("npm: nginx reload: dockerapi: daemon unavailable"))
			}}
		}, errcode.NPMUnhealthy},
		{"a held address that is a target", func(h *thHost, n *nm) {
			h.docker.putContainer(thContainer("db", thEndpoint(nwNet, qaHeld))) // holds A while NPM is planned
			w12aSeed(h, qaHeld, 3000)
			n.d.docker = qaConnectHook{dockerAPI: n.d.docker, before: func() {
				// the holder goes away between the check and the connect
				if err := h.docker.RemoveContainer(context.Background(), "db", dockerapi.RemoveContainerOptions{Force: true}); err != nil {
					t.Error(err)
				}
			}}
		}, errcode.NPMReserved},
	}
	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			h, user := nwSetup(t)
			w12aSubnet(t, h, user)
			n := h.nm()
			sc.setup(h, &n)
			in := qaAttachInput(h, false)
			err := qaApplyAttach(t, n, h, "c1", in)
			if errcode.From(err).Code != sc.code {
				t.Fatalf("error %v, want %s", err, sc.code)
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

const (
	qaDB      = "172.30.0.20" // the address of the container db on app-net, which a proxy host forwards to
	qaDBOnNPM = "172.20.0.30" // the address of the container db2 on darkpanel-npm, which a proxy host forwards to
)

// qaTimeline records the Engine calls that change containers or networks, in order, as "Call", with the network of a
// disconnect appended.
type qaTimeline struct {
	dockerAPI
	log []string
}

func (d *qaTimeline) StartContainer(ctx context.Context, id string) (bool, error) {
	d.log = append(d.log, "StartContainer")
	return d.dockerAPI.StartContainer(ctx, id)
}

func (d *qaTimeline) CreateContainer(ctx context.Context, cfg dockerapi.ContainerCreate) (string, error) {
	d.log = append(d.log, "CreateContainer")
	return d.dockerAPI.CreateContainer(ctx, cfg)
}

func (d *qaTimeline) RemoveContainer(ctx context.Context, id string, o dockerapi.RemoveContainerOptions) error {
	d.log = append(d.log, "RemoveContainer")
	return d.dockerAPI.RemoveContainer(ctx, id, o)
}

func (d *qaTimeline) DisconnectNetwork(ctx context.Context, network, container string, force bool) error {
	d.log = append(d.log, "DisconnectNetwork "+network)
	return d.dockerAPI.DisconnectNetwork(ctx, network, container, force)
}

func (d *qaTimeline) count(entry string) int {
	n := 0
	for _, e := range d.log {
		if e == entry {
			n++
		}
	}
	return n
}

// qaUpSetup is an upgrade setup whose two networks report their subnets, with NPM on app-net at an Engine-chosen
// address, and two other containers: db holds 172.30.0.20 on app-net and db2 holds 172.20.0.30 on darkpanel-npm. A
// proxy host forwards to each, so the upgrade passes its checks while they hold the addresses.
func qaUpSetup(t *testing.T) (*upSetup, *qaTimeline) {
	t.Helper()
	s := w12aUpSetup(t)
	c, ok := s.h.docker.container(s.inst.ContainerID)
	if !ok {
		t.Fatal("the NPM container is missing")
	}
	c.NetworkSettings.Networks[upAppNet].IPAMConfig = nil
	s.h.docker.putContainer(c)
	s.h.docker.putContainer(thContainer("db", thEndpoint(upAppNet, qaDB)))
	s.h.docker.putContainer(thContainer("db2", thEndpoint(networkName, qaDBOnNPM)))
	w12aSeed(s.h, qaDB, 5432)
	w12aSeed(s.h, qaDBOnNPM, 5432)
	tl := &qaTimeline{dockerAPI: s.n.d.docker}
	s.n.d.docker = tl
	return s, tl
}

// qaRelease stops the container name, which gives up its addresses as the Engine does for a stopped container.
func qaRelease(t *testing.T, h *thHost, name string) {
	t.Helper()
	c, ok := h.docker.container(name)
	if !ok {
		t.Fatalf("no container %s", name)
	}
	c.State.Running, c.State.Status = false, "exited"
	for _, ep := range c.NetworkSettings.Networks {
		ep.IPAddress = netip.Addr{}
	}
	h.docker.putContainer(c)
}

// qaOnce returns a function that runs f the first time it is called with one of calls.
func qaOnce(calls []string, f func()) func(string) {
	done := false
	return func(call string) {
		if done {
			return
		}
		for _, c := range calls {
			if c == call {
				done = true
				f()
				return
			}
		}
	}
}

// qaDone runs the upgrade of c1 to the end and returns its journaled effect.
func qaDone(t *testing.T, s *upSetup) core.Effect {
	t.Helper()
	if _, err := s.apply(); err != nil {
		t.Fatalf("the upgrade failed: %v", err)
	}
	if got := s.phase(); got != "done" {
		t.Fatalf("phase %q, want done", got)
	}
	effects := s.effects()
	if len(effects) != 1 {
		t.Fatalf("%d effects, want 1", len(effects))
	}
	return effects[0]
}

func (s *upSetup) undo(e core.Effect) error {
	s.t.Helper()
	var err error
	upDrive(s.t, s.h, func() {
		_, err = s.n.instanceUpgrade().Handler.Inverse(context.Background(), s.h.env("c1"), e)
	})
	return err
}

func (s *upSetup) recoverRun() (core.RecoveryOutcome, error) {
	s.t.Helper()
	var out core.RecoveryOutcome
	var err error
	upDrive(s.t, s.h, func() {
		out, err = s.n.instanceUpgrade().Handler.Recover(context.Background(), s.h.env("c1"))
	})
	return out, err
}

// qaRunning checks that the NPM container is the original image, running.
func qaRunning(t *testing.T, h *thHost, image string) dockerapi.ContainerJSON {
	t.Helper()
	c, ok := h.docker.container(containerName)
	if !ok || !c.State.Running || c.Config.Image != image {
		t.Fatalf("container %+v (found %v), want it running %s", c.State, ok, image)
	}
	return c
}

// qaListFails makes the next n listings of the proxy hosts end without an answer, as an NPM that is down does.
func qaListFails(h *thHost, n int) {
	for range n {
		h.npm.Fail(http.MethodGet, "/api/nginx/proxy-hosts", fake.Fault{AfterCommit: true})
	}
}

// TestA4_TheUndoOfAFinishedUpgradeIsCheckedAgainstTheForwards: a proxy host forwards to an address the restored container
// could take on a network of the row's Config, or on darkpanel-npm, so the undo is refused with npm.reserved and the
// upgraded container keeps running; the check follows the pull of the original image; a missing network or a silent API
// refuses the undo too.
func TestA4_TheUndoOfAFinishedUpgradeIsCheckedAgainstTheForwards(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name  string
		leave bool // the upgraded container leaves app-net after the upgrade
		free  string
	}{
		{"a free address of a network the upgraded container has left", true, "db"},
		{"a free address of a network the upgraded container is still on", false, "db"},
		{"a free address of darkpanel-npm", false, "db2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, tl := qaUpSetup(t)
			e := qaDone(t, s)
			if tc.leave {
				if err := s.h.docker.DisconnectNetwork(ctx, upAppNet, containerName, false); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.h.docker.RemoveContainer(ctx, tc.free, dockerapi.RemoveContainerOptions{Force: true}); err != nil {
				t.Fatal(err)
			}
			creates := tl.count("CreateContainer")
			coded := nwRefused(t, s.undo(e), errcode.NPMReserved)
			if !strings.Contains(coded.Detail, "forwards to") {
				t.Errorf("detail %q does not name the forwarding proxy host", coded.Detail)
			}
			qaRunning(t, s.h, Image)
			if got := s.phase(); got != "done" {
				t.Errorf("phase %q, want done", got)
			}
			if tl.count("CreateContainer") != creates {
				t.Error("the refused undo created a container")
			}
		})
	}

	t.Run("the check follows the pull of the original image", func(t *testing.T) {
		s, _ := qaUpSetup(t)
		e := qaDone(t, s)
		if err := s.h.docker.RemoveContainer(ctx, "db", dockerapi.RemoveContainerOptions{Force: true}); err != nil {
			t.Fatal(err)
		}
		s.dock.missing[upFromImage] = true
		s.dock.trace = qaOnce([]string{"PullImage"}, func() { s.dock.missing[upFromImage] = false })
		pulledAtFirstUse := -1
		note := func() {
			if pulledAtFirstUse < 0 {
				pulledAtFirstUse = s.dock.counts["PullImage"]
			}
		}
		api, anon := s.n.d.api, s.n.d.anon
		s.n.d.api = func(ctx context.Context, env core.Env) (npmAPI, error) { note(); return api(ctx, env) }
		s.n.d.anon = func() npmAPI { note(); return anon() }
		pulls := s.dock.counts["PullImage"]
		_ = nwRefused(t, s.undo(e), errcode.NPMReserved)
		if pulledAtFirstUse < 0 {
			t.Fatal("the undo never asked NPM's API about the forwards")
		}
		if pulledAtFirstUse != pulls+1 {
			t.Errorf("%d pulls of the original image before the forwards were read, want it pulled once first", pulledAtFirstUse-pulls)
		}
	})

	t.Run("a network of the row's Config no longer exists", func(t *testing.T) {
		s, tl := qaUpSetup(t)
		e := qaDone(t, s)
		for _, step := range []func() error{
			func() error { return s.h.docker.DisconnectNetwork(ctx, upAppNet, containerName, false) },
			func() error {
				return s.h.docker.RemoveContainer(ctx, "db", dockerapi.RemoveContainerOptions{Force: true})
			},
			func() error { return s.h.docker.RemoveNetwork(ctx, upAppNet) },
		} {
			if err := step(); err != nil {
				t.Fatal(err)
			}
		}
		removes := tl.count("RemoveContainer")
		err := s.undo(e)
		if err == nil || !core.IsDefinite(err) {
			t.Fatalf("error %v, want a Definite refusal", err)
		}
		if tl.count("RemoveContainer") != removes || tl.count("CreateContainer") != 1 {
			t.Error("the refused undo removed or created a container")
		}
		qaRunning(t, s.h, Image)
		if got := s.phase(); got != "done" {
			t.Errorf("phase %q, want done", got)
		}
	})

	t.Run("NPM's API does not answer", func(t *testing.T) {
		s, tl := qaUpSetup(t)
		e := qaDone(t, s)
		qaListFails(s.h, 12)
		removes := tl.count("RemoveContainer")
		_ = nwRefused(t, s.undo(e), errcode.NPMUnhealthy)
		if tl.count("RemoveContainer") != removes {
			t.Error("the refused undo removed a container")
		}
		qaRunning(t, s.h, Image)
		if got := s.phase(); got != "done" {
			t.Errorf("phase %q, want done", got)
		}
	})
}

// TestA4a_ARetriedUndoUsesTheRecordedTargetList: a run cut off after restore removed the container leaves the row done
// and the container missing; the retry reads the forwards from the row, not from the API (which does not answer),
// refuses on a hit and otherwise restores the original.
func TestA4a_ARetriedUndoUsesTheRecordedTargetList(t *testing.T) {
	ctx := context.Background()
	cutOff := func(t *testing.T) (*upSetup, core.Effect) {
		t.Helper()
		s, _ := qaUpSetup(t)
		e := qaDone(t, s)
		if err := s.h.docker.RemoveContainer(ctx, containerName, dockerapi.RemoveContainerOptions{Force: true}); err != nil {
			t.Fatal(err)
		}
		qaListFails(s.h, 40)
		return s, e
	}
	t.Run("a hit is refused", func(t *testing.T) {
		s, e := cutOff(t)
		if err := s.h.docker.RemoveContainer(ctx, "db", dockerapi.RemoveContainerOptions{Force: true}); err != nil {
			t.Fatal(err)
		}
		_ = nwRefused(t, s.undo(e), errcode.NPMReserved)
		if _, ok := s.h.docker.container(containerName); ok {
			t.Error("the refused retry created the original container")
		}
	})
	t.Run("no hit restores the original", func(t *testing.T) {
		s, e := cutOff(t)
		if err := s.undo(e); err != nil {
			t.Fatalf("the retry failed: %v", err)
		}
		qaRunning(t, s.h, upFromImage)
		if got := s.phase(); got != "rolled_back" {
			t.Errorf("phase %q, want rolled_back", got)
		}
	})
}

// TestA5_UndoRollbackAndRecoveryOfAnUpgradeWithNoForwardRestoreTheOriginal: the undo of a finished upgrade with no
// forward to an address the original could take restores it; the rollback of a failed upgrade and the recovery run
// no refusing check, so they restore it while a forwarded address is free.
func TestA5_UndoRollbackAndRecoveryOfAnUpgradeWithNoForwardRestoreTheOriginal(t *testing.T) {
	ctx := context.Background()
	t.Run("the undo of a finished upgrade", func(t *testing.T) {
		s := w12aUpSetup(t)
		e := qaDone(t, s)
		if err := s.undo(e); err != nil {
			t.Fatalf("the undo failed: %v", err)
		}
		c := qaRunning(t, s.h, upFromImage)
		if c.ID == s.inst.ContainerID || c.NetworkSettings.Networks[upAppNet] == nil || c.NetworkSettings.Networks[networkName] == nil {
			t.Errorf("container %s on %d networks, want a recreated original on app-net and darkpanel-npm", c.ID, len(c.NetworkSettings.Networks))
		}
		if got := s.phase(); got != "rolled_back" {
			t.Errorf("phase %q, want rolled_back", got)
		}
	})
	t.Run("the rollback of a failed upgrade", func(t *testing.T) {
		s, _ := qaUpSetup(t)
		// db lets go of its address during the stop, and the new container does not get it; its API never answers, so the
		// upgrade fails and rolls back while the forwarded address is free.
		s.dock.trace = qaOnce([]string{"StopContainer"}, func() { qaRelease(t, s.h, "db") })
		s.apiDown()
		_, err := s.apply()
		if err == nil {
			t.Fatal("the upgrade of a container whose API never answers succeeded")
		}
		if code := errcode.From(err).Code; code == errcode.NPMReserved {
			t.Errorf("error %v: the rollback was refused", err)
		}
		qaRunning(t, s.h, upFromImage)
		if got := s.phase(); got != "rolled_back" {
			t.Errorf("phase %q, want rolled_back", got)
		}
	})
	t.Run("the recovery of an upgrade cut off while recreating", func(t *testing.T) {
		s, _ := qaUpSetup(t)
		qaDone(t, s)
		u := s.upgrade()
		if err := setUpgradePhase(ctx, s.h.db, "c1", "recreating", "", u.Archives, s.h.clock.Now()); err != nil {
			t.Fatal(err)
		}
		qaRelease(t, s.h, "db")
		out, err := s.recoverRun()
		if err != nil || out.State != core.RecoveryRolledBack {
			t.Fatalf("recovery %+v, error %v; want rolled back", out, err)
		}
		qaRunning(t, s.h, upFromImage)
	})
}

// TestA6_AHolderStoppedDuringThePullRefusesTheUpgradeAtTheLateCheck: nothing is journaled and NPM is never stopped.
func TestA6_AHolderStoppedDuringThePullRefusesTheUpgradeAtTheLateCheck(t *testing.T) {
	s, tl := qaUpSetup(t)
	s.dock.missing[Image] = true // the pinned image must be pulled
	s.dock.trace = qaOnce([]string{"PullImage"}, func() {
		s.dock.missing[Image] = false
		qaRelease(t, s.h, "db")
	})
	_, err := s.apply()
	_ = nwRefused(t, err, errcode.NPMReserved)
	if s.dock.counts["PullImage"] == 0 {
		t.Fatal("the upgrade never pulled the image, so the holder was never stopped")
	}
	if len(s.effects()) != 0 || s.phase() != "no row" {
		t.Errorf("effects %+v and phase %q, want nothing journaled and no row", s.effects(), s.phase())
	}
	if s.dock.counts["StopContainer"] != 0 || tl.count("CreateContainer") != 0 {
		t.Error("NPM was stopped or recreated")
	}
	qaRunning(t, s.h, upFromImage)
}

// TestA6a_TheStartOfEveryNPMContainerIsVerifiedAgainstTheForwards: the container that takes a forwarded address at its
// start leaves that network at once.
func TestA6a_TheStartOfEveryNPMContainerIsVerifiedAgainstTheForwards(t *testing.T) {
	ctx := context.Background()
	t.Run("an upgrade whose holder is stopped during the stop", func(t *testing.T) {
		s, tl := qaUpSetup(t)
		s.dock.trace = qaOnce([]string{"StopContainer"}, func() {
			qaRelease(t, s.h, "db")
			s.h.docker.pin(upAppNet, netip.MustParseAddr(qaDB))
		})
		_, err := s.apply()
		if errcode.From(err).Code != errcode.NPMReserved {
			t.Fatalf("error %v, want npm.reserved", err)
		}
		if !strings.Contains(err.Error(), upAppNet) {
			t.Errorf("error %v does not name the dropped network %s", err, upAppNet)
		}
		// The new container starts, leaves app-net at once, and only then the rollback recreates the original, which
		// takes the pinned address again and is disconnected the same way.
		start, firstDisc := -1, -1
		for i, e := range tl.log {
			switch {
			case e == "StartContainer" && start < 0:
				start = i
			case e == "DisconnectNetwork "+upAppNet && firstDisc < 0:
				firstDisc = i
			}
		}
		if start < 0 || firstDisc < start || tl.log[start+1] != "DisconnectNetwork "+upAppNet {
			t.Errorf("Engine calls %v: the new container was not disconnected from %s right after its start", tl.log, upAppNet)
		}
		if got := tl.count("DisconnectNetwork " + upAppNet); got != 2 {
			t.Errorf("%d disconnects of %s, want the new container's and the restored original's", got, upAppNet)
		}
		u := s.upgrade()
		if u.Phase != "rolled_back" || !strings.Contains(u.Result, upAppNet) {
			t.Errorf("phase %q and result %q, want rolled_back with the dropped network recorded", u.Phase, u.Result)
		}
		c := qaRunning(t, s.h, upFromImage)
		if c.NetworkSettings.Networks[upAppNet] != nil {
			t.Error("the restored original is on the network where it held a forwarded address")
		}
	})

	t.Run("the undo of a finished upgrade", func(t *testing.T) {
		s, _ := qaUpSetup(t)
		e := qaDone(t, s)
		s.dock.trace = qaOnce([]string{"RemoveContainer"}, func() {
			qaRelease(t, s.h, "db")
			s.h.docker.pin(upAppNet, netip.MustParseAddr(qaDB))
		})
		_ = s.undo(e)
		c := qaRunning(t, s.h, upFromImage)
		if c.NetworkSettings.Networks[upAppNet] != nil {
			t.Error("the restored original stayed on app-net at a forwarded address")
		}
		if u := s.upgrade(); !strings.Contains(u.Result, upAppNet) {
			t.Errorf("result %q does not record the dropped network", u.Result)
		}
	})

	t.Run("the recovery rollback", func(t *testing.T) {
		s, _ := qaUpSetup(t)
		qaDone(t, s)
		if err := setUpgradePhase(ctx, s.h.db, "c1", "recreating", "", s.upgrade().Archives, s.h.clock.Now()); err != nil {
			t.Fatal(err)
		}
		qaRelease(t, s.h, "db")
		s.h.docker.pin(upAppNet, netip.MustParseAddr(qaDB))
		out, err := s.recoverRun()
		if err != nil || out.State != core.RecoveryRolledBack {
			t.Fatalf("recovery %+v, error %v; want rolled back", out, err)
		}
		c := qaRunning(t, s.h, upFromImage)
		if c.NetworkSettings.Networks[upAppNet] != nil {
			t.Error("the restored original stayed on app-net at a forwarded address")
		}
	})

	t.Run("the restart of the original by recovery", func(t *testing.T) {
		s, _ := qaUpSetup(t)
		qaDone(t, s)
		if err := setUpgradePhase(ctx, s.h.db, "c1", "stopped", "", nil, s.h.clock.Now()); err != nil {
			t.Fatal(err)
		}
		// The original container is stopped; the Engine hands it the address db freed when it starts again.
		c, _ := s.h.docker.container(containerName)
		c.State.Running, c.State.Status = false, "exited"
		c.NetworkSettings.Networks[upAppNet].IPAddress = netip.MustParseAddr(qaDB)
		s.h.docker.putContainer(c)
		qaRelease(t, s.h, "db")
		out, err := s.recoverRun()
		if err != nil || out.State != core.RecoveryRolledBack {
			t.Fatalf("recovery %+v, error %v; want rolled back", out, err)
		}
		if c := qaRunning(t, s.h, Image); c.NetworkSettings.Networks[upAppNet] != nil {
			t.Error("the restarted container stayed on app-net at a forwarded address")
		}
	})

	t.Run("the restart of the original by resume", func(t *testing.T) {
		s, _ := qaUpSetup(t)
		s.dock.trace = qaOnce([]string{"StartContainer"}, func() {
			qaRelease(t, s.h, "db")
			c, _ := s.h.docker.container(containerName)
			c.NetworkSettings.Networks[upAppNet].IPAddress = netip.MustParseAddr(qaDB)
			s.h.docker.putContainer(c)
		})
		s.dock.fail = func(call string, _ int) error {
			if strings.HasPrefix(call, "CopyFromContainer") {
				return errors.New("the archive was cut off")
			}
			return nil
		}
		_, err := s.apply()
		if err == nil || !strings.Contains(err.Error(), upAppNet) {
			t.Fatalf("error %v, want the restored original named without %s", err, upAppNet)
		}
		if c := qaRunning(t, s.h, upFromImage); c.NetworkSettings.Networks[upAppNet] != nil {
			t.Error("the restarted original stayed on app-net at a forwarded address")
		}
		if u := s.upgrade(); !strings.Contains(u.Result, upAppNet) {
			t.Errorf("result %q does not record the dropped network", u.Result)
		}
	})
}

// TestA6a_ARowWithNoListReadsTheTargetsThroughTheAPI: a row written before the list was recorded makes the start read
// the forwards from NPM's API; when it does not answer within instanceUpgradeAPIWait the outcome is needs_repair and
// the container keeps running.
func TestA6a_ARowWithNoListReadsTheTargetsThroughTheAPI(t *testing.T) {
	stopped := func(t *testing.T) *urFixture {
		t.Helper()
		f := urSetup(t)
		f.h.docker.putNetwork(thNetwork(upAppNet, thSubnet("172.30.0.0/24", "172.30.0.1")))
		f.record("stopped", "", nil)
		if err := f.h.docker.ConnectNetwork(context.Background(), upAppNet, f.inst.ContainerID, nwStatic(qaDB)); err != nil {
			t.Fatal(err)
		}
		f.setRunning(f.inst.ContainerID, false)
		w12aSeed(f.h, qaDB, 3000)
		return f
	}
	t.Run("the API answers", func(t *testing.T) {
		f := stopped(t)
		var out core.RecoveryOutcome
		var err error
		upDrive(t, f.h, func() { out, err = f.recover() })
		if err != nil || out.State != core.RecoveryRolledBack {
			t.Fatalf("recovery %+v, error %v; want rolled back", out, err)
		}
		c, _ := f.h.docker.container(f.inst.ContainerID)
		if !c.State.Running || c.NetworkSettings.Networks[upAppNet] != nil {
			t.Errorf("container running %v on app-net %v, want it running without app-net", c.State.Running, c.NetworkSettings.Networks[upAppNet] != nil)
		}
	})
	t.Run("the API does not answer", func(t *testing.T) {
		f := stopped(t)
		qaListFails(f.h, 600)
		var out core.RecoveryOutcome
		var err error
		upDrive(t, f.h, func() { out, err = f.recover() })
		if err != nil || out.State != core.RecoveryNeedsRepair {
			t.Fatalf("recovery %+v, error %v; want needs_repair", out, err)
		}
		if c, _ := f.h.docker.container(f.inst.ContainerID); !c.State.Running {
			t.Error("the container was not left running")
		}
	})
}

// TestA6b_AnUpgradeUndoAndRecoveryThatHoldNoForwardTargetBehaveAsToday: no network is disconnected and the instance ends
// where it did before w12e.
func TestA6b_AnUpgradeUndoAndRecoveryThatHoldNoForwardTargetBehaveAsToday(t *testing.T) {
	ctx := context.Background()
	t.Run("the upgrade and its undo", func(t *testing.T) {
		s := w12aUpSetup(t)
		e := qaDone(t, s)
		if row := s.instanceRow(); row.Image != Image {
			t.Errorf("instance row image %q, want %q", row.Image, Image)
		}
		qaRunning(t, s.h, Image)
		if err := s.undo(e); err != nil {
			t.Fatalf("the undo failed: %v", err)
		}
		qaRunning(t, s.h, upFromImage)
		if got := nwCalls(s.h, "DisconnectNetwork"); got != 0 {
			t.Errorf("%d disconnects, want none", got)
		}
	})
	t.Run("the recovery", func(t *testing.T) {
		s := w12aUpSetup(t)
		qaDone(t, s)
		if err := setUpgradePhase(ctx, s.h.db, "c1", "recreating", "", s.upgrade().Archives, s.h.clock.Now()); err != nil {
			t.Fatal(err)
		}
		out, err := s.recoverRun()
		if err != nil || out.State != core.RecoveryRolledBack {
			t.Fatalf("recovery %+v, error %v; want rolled back", out, err)
		}
		c := qaRunning(t, s.h, upFromImage)
		if c.NetworkSettings.Networks[upAppNet] == nil || nwCalls(s.h, "DisconnectNetwork") != 0 {
			t.Errorf("recovered container is on app-net %v after %d disconnects", c.NetworkSettings.Networks[upAppNet] != nil, nwCalls(s.h, "DisconnectNetwork"))
		}
	})
}

// TestA11_UndoingAProxyHostChangeRefusesALocationForwardingToAnNPMAddressWithAPath: the forward host of a restored
// location is an address of NPM followed by "/" and a path, including "/$request_uri"; NPM proxies to the part before
// the first "/", so the undo is refused with npm.reserved. Locations the undo cannot check are still restored.
func TestA11_UndoingAProxyHostChangeRefusesALocationForwardingToAnNPMAddressWithAPath(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		host string
		want errcode.Code // "" when the location is restored as it was
	}{
		{"127.0.0.1/x", errcode.NPMReserved},
		{"127.0.0.1/", errcode.NPMReserved},
		{"127.0.0.1/$request_uri", errcode.NPMReserved},
		{"172.20.0.2/x", errcode.NPMReserved},
		{"172.20.0.2/$request_uri", errcode.NPMReserved},
		{"198.51.100.5/$request_uri", ""},
		{"198.51.100.5/x", ""},
		{"$upstream/x", ""},
	} {
		seed := func(t *testing.T) (*thHost, nm, int) {
			t.Helper()
			h := newTHHost(t)
			h.deployed()
			h.addChange("c0", "npm.admin.expose")
			body := proxyHostBody(puInput(), "")
			body.Locations = append(body.Locations, qaLocation(tc.host))
			host := h.npm.SeedProxyHost(puHostOf(body))
			n := h.nm()
			resolve := n.d.resolve
			n.d.resolve = func(ctx context.Context, name string) ([]netip.Addr, error) {
				if name != "app" {
					return nil, nil
				}
				return resolve(ctx, name)
			}
			return h, n, host.ID
		}
		check := func(t *testing.T, h *thHost, err error) {
			t.Helper()
			if tc.want != "" {
				if errcode.From(err).Code != tc.want || !core.IsDefinite(err) {
					t.Fatalf("inverse = %v, want a definite %s", err, tc.want)
				}
				return
			}
			if err != nil {
				t.Fatalf("inverse = %v, want the location restored", err)
			}
			hosts := puHosts(t, h)
			if len(hosts) != 1 || hosts[0].Locations[len(hosts[0].Locations)-1].ForwardHost != tc.host {
				t.Errorf("NPM holds %+v, want the location forwarding to %q", hosts, tc.host)
			}
		}
		t.Run("update, "+tc.host, func(t *testing.T) {
			h, n, id := seed(t)
			h.addChange("c1", "npm.proxyhost.update")
			def := n.proxyhostUpdate()
			next := puInput(func(in *ProxyHostInput) { in.ForwardPort = 9090 })
			if _, err := def.Handler.Apply(ctx, h.env("c1"), puJSON(t, ProxyHostUpdateInput{ID: id, ProxyHostInput: next})); err != nil {
				t.Fatalf("the update refused a kept location: %v", err)
			}
			effects := h.journal.effects()
			_, err := def.Handler.Inverse(ctx, h.env("c1"), effects[len(effects)-1])
			check(t, h, err)
		})
		t.Run("removal, "+tc.host, func(t *testing.T) {
			h, n, id := seed(t)
			h.addChange("c1", "npm.proxyhost.remove")
			def := n.proxyhostRemove()
			if _, err := def.Handler.Apply(ctx, h.env("c1"), puJSON(t, ProxyHostRemoveInput{ID: id})); err != nil {
				t.Fatal(err)
			}
			e := puEffects(t, h)[0]
			e.UndoTo = &core.StateDoc{State: e.Before.State}
			_, err := def.Handler.Inverse(ctx, h.env("c1"), e)
			check(t, h, err)
		})
	}
}

func qaLocation(host string) npmapi.Location {
	return npmapi.Location{Path: "/api", ForwardScheme: "http", ForwardHost: host, ForwardPort: 81}
}

// TestA12_EveryNamedRegressionTestExists: the nine tests the spec names exist, each in the package of its fix. It
// catches a deleted or renamed test only; that each test fails when its fix is undone is recorded in
// docs/dforge/notes/w12e-evidence.md "Revert check".
func TestA12_EveryNamedRegressionTestExists(t *testing.T) {
	want := map[string][]string{
		".": {"TestW12eAttachReloadsBeforeJoin", "TestW12eAttachReloadsAfterJoin", "TestW12eAttachUnconfirmedReloadDisconnects",
			"TestW12eUpgradeUndoChecksForwards", "TestW12eUpgradeLateForwardCheck", "TestW12eStartVerifiesHeldAddresses",
			"TestW12eRestoredLocationCutAtSlash"},
		"../firewall/guard": {"TestW12eLinkLocalOnlyHostGuardsIPv6443"},
		"../tailscale":      {"TestW12eTailnetClientOfAnExposure"},
	}
	for dir, names := range want {
		for _, name := range names {
			requireTestDeclared(t, dir, name)
		}
	}
}

func requireTestDeclared(t *testing.T, dir, name string) {
	t.Helper()
	fset := token.NewFileSet()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(dir, entry.Name()), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range f.Decls {
			if fn, ok := d.(*ast.FuncDecl); ok && fn.Recv == nil && fn.Name.Name == name {
				return
			}
		}
	}
	t.Errorf("%s: no test function %s", dir, name)
}
