package npm

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"slices"
	"sync"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/npm/npmapi"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

const sqHeld = "10.9.0.7"

type sqLog struct {
	mu     sync.Mutex
	events []string
}

func (l *sqLog) add(e string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, e)
}

func (l *sqLog) snapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.events)
}

func (l *sqLog) count(e string) int {
	n := 0
	for _, x := range l.snapshot() {
		if x == e {
			n++
		}
	}
	return n
}

func sqIndex(events []string, e string, from int) int {
	for i := from; i < len(events); i++ {
		if events[i] == e {
			return i
		}
	}
	return -1
}

type sqDock struct {
	*thDocker
	log     *sqLog
	before  map[string]func()
	noEndpt string
}

func (d *sqDock) hook(op string) {
	if f := d.before[op]; f != nil {
		f()
	}
}

func (d *sqDock) InspectNetwork(ctx context.Context, id string) (dockerapi.Network, error) {
	d.log.add("inspectnet")
	return d.thDocker.InspectNetwork(ctx, id)
}

func (d *sqDock) ConnectNetwork(ctx context.Context, network, container string, ep *dockerapi.EndpointSettings) error {
	d.log.add("connect")
	d.hook("connect")
	return d.thDocker.ConnectNetwork(ctx, network, container, ep)
}

func (d *sqDock) DisconnectNetwork(ctx context.Context, network, container string, force bool) error {
	d.log.add("disconnect")
	return d.thDocker.DisconnectNetwork(ctx, network, container, force)
}

func (d *sqDock) InspectContainer(ctx context.Context, id string) (dockerapi.ContainerJSON, error) {
	d.log.add("inspect:" + id)
	c, err := d.thDocker.InspectContainer(ctx, id)
	if err == nil && id == containerName && d.noEndpt != "" && c.NetworkSettings.Networks[d.noEndpt] != nil {
		nets := map[string]*dockerapi.EndpointSettings{}
		for k, v := range c.NetworkSettings.Networks {
			if k != d.noEndpt {
				nets[k] = v
			}
		}
		c.NetworkSettings.Networks = nets
	}
	return c, err
}

type sqAPI struct {
	npmAPI
	log *sqLog
}

func (a sqAPI) ProxyHosts(ctx context.Context) ([]npmapi.ProxyHost, error) {
	a.log.add("list")
	return a.npmAPI.ProxyHosts(ctx)
}

type sqWorld struct {
	h    *thHost
	user dockerapi.Network
	n    nm
	dock *sqDock
	log  *sqLog
}

func sqNewWorld(t *testing.T, resolve func(context.Context, string) ([]netip.Addr, error)) *sqWorld {
	t.Helper()
	h, user := nwSetup(t)
	w12aSubnet(t, h, user)
	log := &sqLog{}
	dock := &sqDock{thDocker: h.docker, log: log, before: map[string]func(){}}
	n := h.nm()
	n.d.docker = dock
	inner := n.d.api
	n.d.api = func(ctx context.Context, env core.Env) (npmAPI, error) {
		a, err := inner(ctx, env)
		return sqAPI{a, log}, err
	}
	n.d.resolve = func(ctx context.Context, host string) ([]netip.Addr, error) {
		log.add("lookup")
		return resolve(ctx, host)
	}
	h.nginx.onReload = func() { log.add("reload") }
	return &sqWorld{h: h, user: user, n: n, dock: dock, log: log}
}

func sqAnswers(addr string) func(context.Context, string) ([]netip.Addr, error) {
	return func(context.Context, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr(addr)}, nil
	}
}

func (w *sqWorld) attach(t *testing.T, in AttachInput) error {
	t.Helper()
	w.h.addChange("c1", "npm.network.attach")
	_, err := w.n.networkAttach().Handler.Apply(context.Background(), w.h.env("c1"), nwJSON(t, in))
	return err
}

func (w *sqWorld) holder() {
	w.h.docker.putContainer(thContainer("holder", thEndpoint(nwNet, sqHeld)))
}

func (w *sqWorld) freeHolder(t *testing.T) {
	t.Helper()
	if err := w.h.docker.RemoveContainer(context.Background(), "holder", dockerapi.RemoveContainerOptions{Force: true}); err != nil {
		t.Error(err)
	}
}

// sqUndoEffect detaches NPM from appnet in w and returns the endpoint effect whose inverse the undo of that detach runs.
func sqUndoEffect(t *testing.T, w *sqWorld) core.Effect {
	t.Helper()
	nwSeedAttached(t, w.h, w.user, nwClaimant, false)
	if _, err := nwApplyDetach(t, w.h, "c2", DetachInput{Network: nwNet, Claimant: nwClaimant}); err != nil {
		t.Fatal(err)
	}
	var endpoint core.Effect
	for _, e := range w.h.journal.effects() {
		if e.Kind == core.EffectModify {
			endpoint = e
		}
	}
	if endpoint.Resource == "" {
		t.Fatal("the detach journaled no endpoint effect")
	}
	w.log.mu.Lock()
	w.log.events = nil
	w.log.mu.Unlock()
	w.h.nginx.reloads = 0
	return endpoint
}

func (w *sqWorld) undo(t *testing.T, e core.Effect) error {
	t.Helper()
	_, err := w.n.networkDetach().Handler.Inverse(context.Background(), w.h.env("u1"), e)
	return err
}

func sqWantCode(t *testing.T, err error, code errcode.Code) *errcode.Error {
	t.Helper()
	var coded *errcode.Error
	if !errors.As(err, &coded) || coded.Code != code {
		t.Fatalf("error %v, want code %s", err, code)
	}
	return coded
}

func TestA1_AttachMakesNoListingOrLookupBetweenConnectAndInspection(t *testing.T) {
	w := sqNewWorld(t, sqAnswers("203.0.113.9"))
	w12aSeed(w.h, "up.invalid", 81)
	w12aSeed(w.h, "other.invalid", 82)
	if err := w.attach(t, AttachInput{Network: nwNet, Claimant: nwClaimant, Target: "web"}); err != nil {
		t.Fatal(err)
	}
	ev := w.log.snapshot()
	first := sqIndex(ev, "reload", 0)
	conn := sqIndex(ev, "connect", 0)
	if first < 0 || conn < first {
		t.Fatalf("events %v, want a reload and then the connect", ev)
	}
	between := ev[first+1 : conn]
	if len(between) == 0 || between[0] != "inspectnet" {
		t.Errorf("events %v, want the network read again right after the first reload", ev)
	}
	listings := 0
	for _, e := range between {
		if e == "list" {
			listings++
		}
	}
	if listings != 1 {
		t.Errorf("%d proxy host listings between the first reload and the connect, want 1: %v", listings, ev)
	}
	if conn+1 >= len(ev) || ev[conn+1] != "inspect:"+containerName {
		t.Errorf("events %v, want the inspection of NPM directly after the connect", ev)
	}
}

func TestA2_ForwardedAddressTakenAtConnectIsDroppedBeforeAnyReloadOrRead(t *testing.T) {
	cases := []struct {
		name string
		seed func(w *sqWorld)
	}{
		{"literal", func(w *sqWorld) { w12aSeed(w.h, sqHeld, 3000) }},
		{"name read before the connect", func(w *sqWorld) { w12aSeed(w.h, "up.invalid", 3000) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := sqNewWorld(t, sqAnswers(sqHeld))
			w.holder()
			tc.seed(w)
			w.h.docker.pin(nwNet, netip.MustParseAddr(sqHeld))
			w.dock.before["connect"] = func() { w.freeHolder(t) }
			err := w.attach(t, AttachInput{Network: nwNet, Claimant: nwClaimant, Target: "web"})
			sqWantCode(t, err, errcode.NPMReserved)
			ev := w.log.snapshot()
			conn := sqIndex(ev, "connect", 0)
			if conn < 0 || len(ev) < conn+3 || ev[conn+1] != "inspect:"+containerName || ev[conn+2] != "disconnect" {
				t.Fatalf("events %v, want the inspection and then the disconnect right after the connect", ev)
			}
			if w.log.count("reload") != 1 {
				t.Errorf("%d reloads, want only the one before the join", w.log.count("reload"))
			}
			if nwEndpoint(t, w.h, w.user.ID) != nil {
				t.Error("NPM stayed on the network")
			}
		})
	}
	t.Run("undo of a detach", func(t *testing.T) {
		w := sqNewWorld(t, sqAnswers(sqHeld))
		e := sqUndoEffect(t, w)
		w.holder()
		w12aSeed(w.h, sqHeld, 3000)
		w.h.docker.pin(nwNet, netip.MustParseAddr(sqHeld))
		w.dock.before["connect"] = func() { w.freeHolder(t) }
		err := w.undo(t, e)
		sqWantCode(t, err, errcode.NPMReserved)
		ev := w.log.snapshot()
		conn := sqIndex(ev, "connect", 0)
		if conn < 0 || len(ev) < conn+3 || ev[conn+1] != "inspect:"+containerName || ev[conn+2] != "disconnect" {
			t.Fatalf("events %v, want the inspection and then the disconnect right after the connect", ev)
		}
		if w.log.count("reload") != 1 {
			t.Errorf("%d reloads, want only the one before the join", w.log.count("reload"))
		}
		if nwEndpoint(t, w.h, w.user.ID) != nil {
			t.Error("NPM stayed on the network")
		}
	})
}

func TestA3_AddressFreedAfterTheFirstReloadRefusesBeforeTheConnect(t *testing.T) {
	t.Run("attach", func(t *testing.T) {
		w := sqNewWorld(t, sqAnswers("203.0.113.9"))
		w.holder()
		w12aSeed(w.h, sqHeld, 3000)
		w.h.nginx.onReload = func() {
			w.log.add("reload")
			w.freeHolder(t)
		}
		err := w.attach(t, AttachInput{Network: nwNet, Claimant: nwClaimant, Target: "web"})
		sqWantCode(t, err, errcode.NPMReserved)
		if w.log.count("connect") != 0 || nwEndpoint(t, w.h, w.user.ID) != nil {
			t.Error("the attach connected NPM")
		}
		ev := w.log.snapshot()
		if r := sqIndex(ev, "reload", 0); r < 0 || sqIndex(ev, "inspectnet", r) < 0 {
			t.Errorf("events %v, want the network read again after the first reload", ev)
		}
	})
	t.Run("undo of a detach", func(t *testing.T) {
		w := sqNewWorld(t, sqAnswers("203.0.113.9"))
		e := sqUndoEffect(t, w)
		w.holder()
		w12aSeed(w.h, sqHeld, 3000)
		w.h.nginx.onReload = func() {
			w.log.add("reload")
			w.freeHolder(t)
		}
		err := w.undo(t, e)
		sqWantCode(t, err, errcode.NPMReserved)
		if w.log.count("connect") != 0 || nwEndpoint(t, w.h, w.user.ID) != nil {
			t.Error("the undo connected NPM")
		}
	})
}

func TestA15_AttachOnANetworkNPMIsAlreadyOn(t *testing.T) {
	in := AttachInput{Network: nwNet, Claimant: nwClaimant, Target: "web"}
	t.Run("no claim and no forwarded address reloads once", func(t *testing.T) {
		w := sqNewWorld(t, sqAnswers("203.0.113.9"))
		w12aSeed(w.h, "up.invalid", 3000)
		nwConnect(t, w.h, w.user, nwStatic("10.9.0.40"))
		if err := w.attach(t, in); err != nil {
			t.Fatal(err)
		}
		if w.log.count("reload") != 1 {
			t.Errorf("%d reloads, want 1", w.log.count("reload"))
		}
		if nwEndpoint(t, w.h, w.user.ID) == nil {
			t.Error("NPM left the network")
		}
	})
	t.Run("forwarded address disconnects and refuses", func(t *testing.T) {
		w := sqNewWorld(t, sqAnswers("203.0.113.9"))
		w12aSeed(w.h, sqHeld, 3000)
		nwConnect(t, w.h, w.user, nwStatic(sqHeld))
		sqWantCode(t, w.attach(t, in), errcode.NPMReserved)
		if nwEndpoint(t, w.h, w.user.ID) != nil {
			t.Error("NPM stayed on a network where it holds a forwarded address")
		}
	})
	t.Run("forwarded name disconnects and refuses", func(t *testing.T) {
		w := sqNewWorld(t, sqAnswers(sqHeld))
		w12aSeed(w.h, "up.invalid", 3000)
		nwConnect(t, w.h, w.user, nwStatic(sqHeld))
		sqWantCode(t, w.attach(t, in), errcode.NPMReserved)
		if nwEndpoint(t, w.h, w.user.ID) != nil {
			t.Error("NPM stayed on a network where it holds an address a name forwards to")
		}
	})
	t.Run("unconfirmed reload disconnects and refuses", func(t *testing.T) {
		w := sqNewWorld(t, sqAnswers("203.0.113.9"))
		w12aSeed(w.h, "up.invalid", 3000)
		nwConnect(t, w.h, w.user, nwStatic("10.9.0.40"))
		w.h.nginx.fail(errNginxTest)
		sqWantCode(t, w.attach(t, in), errcode.NPMReserved)
		if nwEndpoint(t, w.h, w.user.ID) != nil {
			t.Error("NPM stayed on the network after an unconfirmed reload")
		}
	})
	t.Run("darkpanel-npm is never disconnected", func(t *testing.T) {
		w := sqNewWorld(t, sqAnswers("203.0.113.9"))
		own, _ := w.h.docker.network(networkName)
		ep := nwEndpoint(t, w.h, own.ID)
		if ep == nil {
			t.Fatal("NPM is not on its own network")
		}
		w12aSeed(w.h, ep.IPAddress.String(), 3000)
		w.h.docker.putContainer(thContainer("web2", func(c *dockerapi.ContainerJSON) {
			c.NetworkSettings.Networks = map[string]*dockerapi.EndpointSettings{networkName: {NetworkID: own.ID}}
		}))
		err := w.attach(t, AttachInput{Network: networkName, Claimant: nwClaimant, Target: "web2"})
		sqWantCode(t, err, errcode.NPMReserved)
		if w.log.count("disconnect") != 0 || nwEndpoint(t, w.h, own.ID) == nil {
			t.Error("NPM was disconnected from darkpanel-npm")
		}
	})
	t.Run("a network with a claim lists, looks up and reloads nothing", func(t *testing.T) {
		w := sqNewWorld(t, sqAnswers("203.0.113.9"))
		w12aSeed(w.h, "up.invalid", 3000)
		nwSeedAttached(t, w.h, w.user, nwOther, false)
		if err := w.attach(t, in); err != nil {
			t.Fatal(err)
		}
		if w.log.count("list") != 0 || w.log.count("lookup") != 0 || w.log.count("reload") != 0 || w.h.nginx.reloads != 0 {
			t.Errorf("events %v, want no listing, lookup or reload", w.log.snapshot())
		}
		if got := len(nwClaims(t, w.h, nwNet)); got != 2 {
			t.Errorf("%d claims, want 2", got)
		}
	})
}

func TestA16_JoinedNPMWithNoEndpointIsDisconnectedAndAttachRefuses(t *testing.T) {
	w := sqNewWorld(t, sqAnswers("203.0.113.9"))
	w12aSeed(w.h, "up.invalid", 3000)
	w.dock.noEndpt = nwNet
	err := w.attach(t, AttachInput{Network: nwNet, Claimant: nwClaimant, Target: "web"})
	if err == nil {
		t.Fatal("the attach succeeded although the inspection showed no endpoint")
	}
	if w.log.count("disconnect") != 1 || nwEndpoint(t, w.h, w.user.ID) != nil {
		t.Errorf("events %v, want one disconnect and NPM off the network", w.log.snapshot())
	}
	if len(nwClaims(t, w.h, nwNet)) != 0 {
		t.Error("the refused attach recorded a claim")
	}
}

var _ = json.Marshal
