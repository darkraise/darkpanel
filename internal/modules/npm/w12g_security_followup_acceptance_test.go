package npm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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

	changes int
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
	w.changes++
	id := "c" + strconv.Itoa(w.changes)
	w.h.addChange(id, "npm.network.attach")
	_, err := w.n.networkAttach().Handler.Apply(context.Background(), w.h.env(id), nwJSON(t, in))
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

type sqExecCall struct {
	container, user string
	cmd             []string
}

type sqExec struct {
	mu          sync.Mutex
	env         []string
	inspectErr  error
	createErr   error
	stopped     bool
	exit        map[string]int
	hang        map[string]bool
	noCode      map[string]bool
	gen         int
	stuck       bool
	calls       []sqExecCall
	lines       map[string]string
	releaseHang bool
}

func sqNewExec() *sqExec {
	return &sqExec{exit: map[string]int{}, hang: map[string]bool{}, noCode: map[string]bool{}, lines: map[string]string{},
		gen: 11}
}

const (
	sqTest   = "nginx -t -g error_log off;"
	sqReload = "nginx -s reload"
)

func (f *sqExec) InspectContainer(context.Context, string) (dockerapi.ContainerJSON, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.inspectErr != nil {
		return dockerapi.ContainerJSON{}, f.inspectErr
	}
	return dockerapi.ContainerJSON{Config: dockerapi.ContainerConfig{Env: f.env}, State: dockerapi.ContainerState{Running: !f.stopped}}, nil
}

func (f *sqExec) ExecCreate(_ context.Context, container string, o dockerapi.ExecCreate) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.createErr != nil {
		return "", f.createErr
	}
	f.calls = append(f.calls, sqExecCall{container, o.User, o.Cmd})
	id := "exec" + strconv.Itoa(len(f.calls))
	f.lines[id] = strings.Join(o.Cmd, " ")
	return id, nil
}

func (f *sqExec) ExecStart(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.lines[id] == sqReload && !f.stuck {
		f.gen++
	}
	return nil
}

func (f *sqExec) ExecInspect(_ context.Context, id string) (dockerapi.ExecInspect, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	line := f.lines[id]
	switch {
	case f.hang[line] && !f.releaseHang:
		return dockerapi.ExecInspect{ID: id, Running: true}, nil
	case f.noCode[line]:
		return dockerapi.ExecInspect{ID: id}, nil
	}
	code := f.exit[line]
	return dockerapi.ExecInspect{ID: id, ExitCode: &code}, nil
}

func (f *sqExec) ContainerTop(context.Context, string, string) (dockerapi.TopResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	rows := [][]string{{"10", "1", "nginx: master process nginx"}, {strconv.Itoa(f.gen), "10", "nginx: worker process"}}
	return dockerapi.TopResult{Titles: []string{"PID", "PPID", "COMMAND"}, Processes: rows}, nil
}

func (f *sqExec) lineCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (w *sqWorld) withExec(f *sqExec) *nginxReloader {
	rl := newNginxReloader(f)
	rl.wait, rl.poll = 400*time.Millisecond, 10*time.Millisecond
	w.n.d.reload = rl.Reload
	return rl
}

func TestA4_ReloadRunsTheTestAndThenTheReloadByExecAsTheContainersUser(t *testing.T) {
	wantCmds := [][]string{{"nginx", "-t", "-g", "error_log off;"}, {"nginx", "-s", "reload"}}
	for _, tc := range []struct {
		name  string
		env   []string
		users []string
	}{
		{"PUID and PGID set", []string{"PUID=1234", "PGID=5678", "X=1"}, []string{"1234:5678"}},
		{"unset", []string{"X=1"}, []string{"0:0", "0", "root", "root:root"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := sqNewWorld(t, sqAnswers("203.0.113.9"))
			f := sqNewExec()
			f.env = tc.env
			w.withExec(f)
			if err := w.n.reloadNginx(context.Background()); err != nil {
				t.Fatal(err)
			}
			if len(f.calls) != 2 {
				t.Fatalf("%d execs, want 2", len(f.calls))
			}
			for i, c := range f.calls {
				if !slices.Equal(c.cmd, wantCmds[i]) || c.container != "darkpanel-npm" || !slices.Contains(tc.users, c.user) {
					t.Errorf("exec %d is %+v, want %q in darkpanel-npm as one of %q", i, c, wantCmds[i], tc.users)
				}
			}
		})
	}
}

func TestA4_RefusalsOfTheReloadBeforeTheJoin(t *testing.T) {
	in := AttachInput{Network: nwNet, Claimant: nwClaimant, Target: "web"}
	cases := []struct {
		name    string
		arrange func(f *sqExec)
		seed    func(w *sqWorld)
		code    errcode.Code
		detail  []string
	}{
		{"a failed test", func(f *sqExec) { f.exit[sqTest] = 1 }, nil, errcode.NPMReserved, nil},
		{"a failed test with an unanswered custom location", func(f *sqExec) { f.exit[sqTest] = 1 }, func(w *sqWorld) {
			w12aSeed(w.h, "app.example.net", 8080, npmapi.Location{Path: "/x", ForwardScheme: "http", ForwardHost: "gone.invalid", ForwardPort: 80})
			w.n.d.resolve = sqNoAnswer
		}, errcode.NPMForwardUnanswered, []string{"/x", "gone.invalid"}},
		{"no exit code of the test", func(f *sqExec) { f.noCode[sqTest] = true }, nil, errcode.NPMUnhealthy, nil},
		{"the test still running at the bound", func(f *sqExec) { f.hang[sqTest] = true }, nil, errcode.NPMUnhealthy, nil},
		{"a non-zero exit of the reload", func(f *sqExec) { f.exit[sqReload] = 1 }, nil, errcode.NPMUnhealthy, nil},
		{"no new worker after the reload", func(f *sqExec) { f.stuck = true }, nil, errcode.NPMUnhealthy, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := sqNewWorld(t, sqAnswers("203.0.113.9"))
			f := sqNewExec()
			tc.arrange(f)
			w.withExec(f)
			if tc.seed != nil {
				tc.seed(w)
			}
			err := w.attach(t, in)
			coded := sqWantCode(t, err, tc.code)
			for _, d := range tc.detail {
				if !strings.Contains(coded.Detail, d) {
					t.Errorf("detail %q does not name %q", coded.Detail, d)
				}
			}
			if w.log.count("connect") != 0 || nwEndpoint(t, w.h, w.user.ID) != nil {
				t.Error("NPM was connected")
			}
		})
	}
}

func TestA5_ReloadSendsNoRequestToNPMAndNeedsNoEnabledProxyHost(t *testing.T) {
	w := sqNewWorld(t, sqAnswers("203.0.113.9"))
	w12aSeed(w.h, "up.invalid", 3000)
	w.h.npm.DisableProxyHost(1)
	w.withExec(sqNewExec())
	hostsBefore, requestsBefore := w.h.npm.ProxyHosts(), len(w.h.npm.Requests())
	if err := w.n.reloadNginx(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := len(w.h.npm.Requests()); got != requestsBefore {
		t.Errorf("%d requests reached NPM during the reload, want none: %v", got-requestsBefore, w.h.npm.Requests()[requestsBefore:])
	}
	if !reflect.DeepEqual(hostsBefore, w.h.npm.ProxyHosts()) {
		t.Error("the reload changed a proxy host")
	}
	w2 := sqNewWorld(t, sqAnswers("203.0.113.9"))
	w12aSeed(w2.h, "up.invalid", 3000)
	w2.withExec(sqNewExec())
	if err := w2.attach(t, AttachInput{Network: nwNet, Claimant: nwClaimant, Target: "web"}); err != nil {
		t.Fatal(err)
	}
	for _, r := range w2.h.npm.Requests() {
		if r.Method != http.MethodGet && !strings.HasSuffix(r.Path, "/tokens") && !strings.HasSuffix(r.Path, "/token") {
			t.Errorf("the attach sent %s %s to NPM; only reads and the token are expected", r.Method, r.Path)
		}
	}
}

func TestA6_RefusalsOfTheReloadAreUnhealthy(t *testing.T) {
	in := AttachInput{Network: nwNet, Claimant: nwClaimant, Target: "web"}
	cases := []struct {
		name    string
		arrange func(f *sqExec)
	}{
		{"a stopped container", func(f *sqExec) { f.stopped = true }},
		{"a missing container", func(f *sqExec) {
			f.inspectErr = &dockerapi.APIError{StatusCode: http.StatusNotFound, Message: "No such container"}
		}},
		{"an Engine error", func(f *sqExec) { f.createErr = errors.New("engine unreachable") }},
		{"an Engine error on the inspection", func(f *sqExec) { f.inspectErr = errors.New("engine unreachable") }},
		{"a non-numeric PUID", func(f *sqExec) { f.env = []string{"PUID=abc", "PGID=1000"} }},
		{"a non-numeric PGID", func(f *sqExec) { f.env = []string{"PUID=1000", "PGID=1000; id"} }},
		{"an exec pair that runs past the bound", func(f *sqExec) { f.hang[sqReload] = true }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := sqNewWorld(t, sqAnswers("203.0.113.9"))
			f := sqNewExec()
			tc.arrange(f)
			w.withExec(f)
			err := w.attach(t, in)
			sqWantCode(t, err, errcode.NPMUnhealthy)
			if w.log.count("connect") != 0 {
				t.Error("NPM was connected")
			}
			if strings.Contains(err.Error(), "nginx: [") {
				t.Errorf("error %q holds nginx's output", err)
			}
		})
	}
	t.Run("an earlier exec of its own still running", func(t *testing.T) {
		w := sqNewWorld(t, sqAnswers("203.0.113.9"))
		f := sqNewExec()
		f.hang[sqTest] = true
		w.withExec(f)
		sqWantCode(t, w.attach(t, in), errcode.NPMUnhealthy)
		created := f.lineCount()
		sqWantCode(t, w.attach(t, in), errcode.NPMUnhealthy)
		if f.lineCount() != created {
			t.Errorf("a second reload started %d more execs while the first still ran", f.lineCount()-created)
		}
	})
	t.Run("the bound is 10 seconds and deps.reload takes neither container nor command", func(t *testing.T) {
		if got := newNginxReloader(sqNewExec()).wait; got != 10*time.Second {
			t.Errorf("the reload is bounded by %s, want 10s", got)
		}
		field, _ := reflect.TypeOf(deps{}).FieldByName("reload")
		ft := field.Type
		ctxType := reflect.TypeOf((*context.Context)(nil)).Elem()
		if ft.Kind() != reflect.Func || ft.NumIn() != 1 || ft.In(0) != ctxType || ft.NumOut() != 1 {
			t.Errorf("deps.reload is %s, want func(context.Context) error", ft)
		}
	})
}

func sqNoAnswer(_ context.Context, host string) ([]netip.Addr, error) {
	return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
}

func TestA8_HarnessHoldsNoAuthorizationHeaderNoTokenAndNoReloadCall(t *testing.T) {
	for _, rel := range []string{"../../../test/harness/m6_npm_test.go", "../../../test/harness/vm/vm.go"} {
		b, err := os.ReadFile(rel)
		if err != nil {
			t.Fatal(err)
		}
		text := strings.ToLower(string(b))
		for _, banned := range []string{"authorization", "bearer", "reloadcall"} {
			if strings.Contains(text, banned) {
				t.Errorf("%s holds %q", rel, banned)
			}
		}
	}
}

func sqHit(t *testing.T) (*thHost, dockerapi.Network) {
	t.Helper()
	h, user := nwSetup(t)
	w12aSubnet(t, h, user)
	nwConnect(t, h, user, nwStatic(sqHeld))
	nwClaim(t, h, nwNet, nwClaimant, "c0", false)
	return h, user
}

var sqPassIDs atomic.Int32

// sqPass runs one pass of the reconcile task; Apply runs the action's handler as the engine would and counts calls.
func sqPass(t *testing.T, h *thHost, n nm, task core.BackgroundTask, applies *int) error {
	t.Helper()
	id := "p" + strconv.Itoa(int(sqPassIDs.Add(1)))
	h.addChange(id, "npm.instance.reconcile")
	env := h.env(id)
	tc := core.TaskContext{Env: env, Apply: func(ctx context.Context, action string, input any) (core.ChangeView, error) {
		*applies++
		if action != "npm.instance.reconcile" {
			t.Errorf("the pass applied %q", action)
		}
		_, err := n.instanceReconcile().Handler.Apply(ctx, env, nwJSON(t, input))
		return core.ChangeView{}, err
	}}
	return task.Run(context.Background(), tc)
}

func TestA9_ReconcileTaskDeclaration(t *testing.T) {
	h, _ := nwSetup(t)
	tasks := module{n: h.nm()}.BackgroundTasks()
	var task *core.BackgroundTask
	for i := range tasks {
		if tasks[i].ID == "npm.reconcile" {
			task = &tasks[i]
		}
	}
	if task == nil {
		t.Fatalf("tasks %v, want npm.reconcile", tasks)
	}
	if task.Every != 5*time.Minute || task.Watch == nil || task.Run == nil {
		t.Errorf("task %+v, want a pass every 5 minutes with a Docker events watch", task)
	}
	if !slices.Contains(task.LockKeys, "npm") {
		t.Errorf("lock keys %v, want npm", task.LockKeys)
	}
}

type sqEvents struct {
	events []dockerapi.Event
	i      int
	done   chan struct{}
}

func (s *sqEvents) Next() (dockerapi.Event, error) {
	if s.i < len(s.events) {
		e := s.events[s.i]
		s.i++
		return e, nil
	}
	<-s.done
	return dockerapi.Event{}, io.EOF
}
func (s *sqEvents) Close() error { return nil }

type sqEventDock struct {
	*thDocker
	stream *sqEvents
}

func (d sqEventDock) Events(context.Context, dockerapi.EventsOptions) (dockerEvents, error) {
	return d.stream, nil
}

func TestA9_ReconcileWatchWakesOnStartAndConnectOfNPM(t *testing.T) {
	h, _ := nwSetup(t)
	npmID, _ := h.docker.containerID(containerName)
	webID, _ := h.docker.containerID("web")
	mk := func(typ, action, id string, attrs map[string]string) dockerapi.Event {
		ev := dockerapi.Event{Type: typ, Action: action}
		ev.Actor.ID = id
		ev.Actor.Attributes = attrs
		return ev
	}
	stream := &sqEvents{done: make(chan struct{}), events: []dockerapi.Event{
		mk("container", "start", webID, map[string]string{"name": "web"}),
		mk("container", "start", npmID, map[string]string{"name": containerName}),
		mk("network", "connect", "n1", map[string]string{"container": webID}),
		mk("network", "connect", "n1", map[string]string{"container": npmID}),
		mk("container", "die", npmID, map[string]string{"name": containerName}),
	}}
	n := h.nm()
	n.d.docker = sqEventDock{h.docker, stream}
	task := n.reconcileTask()
	var wakes atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() { errc <- task.Watch(ctx, h.env(""), func() { wakes.Add(1) }) }()
	deadline := time.Now().Add(5 * time.Second)
	for wakes.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond)
	cancel()
	close(stream.done)
	if err := <-errc; err != nil {
		t.Errorf("watch ended with %v", err)
	}
	if got := wakes.Load(); got != 2 {
		t.Errorf("%d wakes, want 2: the start of NPM and the connect of NPM only", got)
	}
}

func TestA9_PassDisconnectsNPMReloadsAndRecordsTheNetworkAndItsClaimants(t *testing.T) {
	for _, tc := range []struct {
		name    string
		resolve func(context.Context, string) ([]netip.Addr, error)
		host    string
	}{
		{"literal", sqNoAnswer, sqHeld},
		{"name", sqAnswers(sqHeld), "up.invalid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, user := sqHit(t)
			w12aSeed(h, tc.host, 3000)
			n := h.nm()
			n.d.resolve = tc.resolve
			h.addChange("r1", "npm.instance.reconcile")
			raw, err := n.instanceReconcile().Handler.Apply(context.Background(), h.env("r1"), nwJSON(t, ReconcileInput{}))
			if err != nil {
				t.Fatal(err)
			}
			var out Reconciled
			if err := json.Unmarshal(raw, &out); err != nil {
				t.Fatal(err)
			}
			if nwEndpoint(t, h, user.ID) != nil {
				t.Error("NPM stayed on the network")
			}
			if h.nginx.reloads != 1 {
				t.Errorf("%d reloads, want 1", h.nginx.reloads)
			}
			if len(out.Dropped) != 1 || out.Dropped[0].Network != nwNet || out.Dropped[0].Address != sqHeld ||
				!slices.Equal(out.Dropped[0].Claimants, []string{nwClaimant}) {
				t.Errorf("result %+v, want the network %s, address %s and claimant %s", out, nwNet, sqHeld, nwClaimant)
			}
			applies := 0
			if err := sqPass(t, h, n, n.reconcileTask(), &applies); err != nil || applies != 0 {
				t.Errorf("a pass after the disconnect applied %d times with error %v, want none", applies, err)
			}
		})
	}
	t.Run("a pass applies once on a hit", func(t *testing.T) {
		h, user := sqHit(t)
		w12aSeed(h, sqHeld, 3000)
		n := h.nm()
		n.d.resolve = sqNoAnswer
		applies := 0
		if err := sqPass(t, h, n, n.reconcileTask(), &applies); err != nil || applies != 1 {
			t.Fatalf("pass applied %d times with error %v, want once", applies, err)
		}
		if nwEndpoint(t, h, user.ID) != nil {
			t.Error("NPM stayed on the network")
		}
	})
	t.Run("an apply that finds no hit under the lock changes nothing", func(t *testing.T) {
		h, user := sqHit(t)
		w12aSeed(h, "10.9.0.99", 3000)
		n := h.nm()
		n.d.resolve = sqNoAnswer
		h.addChange("r1", "npm.instance.reconcile")
		if _, err := n.instanceReconcile().Handler.Apply(context.Background(), h.env("r1"), nwJSON(t, ReconcileInput{})); err != nil {
			t.Fatal(err)
		}
		if nwEndpoint(t, h, user.ID) == nil || h.nginx.reloads != 0 || len(h.journal.effects()) != 0 {
			t.Errorf("an apply with no hit changed the host: on network %v, %d reloads, %d effects", nwEndpoint(t, h, user.ID) != nil, h.nginx.reloads, len(h.journal.effects()))
		}
	})
	t.Run("a failed reload leaves NPM disconnected and fails unhealthy naming the networks", func(t *testing.T) {
		h, user := sqHit(t)
		w12aSeed(h, sqHeld, 3000)
		n := h.nm()
		n.d.resolve = sqNoAnswer
		h.nginx.fail(errors.New("npm: nginx reload: no new nginx worker after the reload"))
		h.addChange("r1", "npm.instance.reconcile")
		_, err := n.instanceReconcile().Handler.Apply(context.Background(), h.env("r1"), nwJSON(t, ReconcileInput{}))
		coded := sqWantCode(t, err, errcode.NPMUnhealthy)
		if !strings.Contains(coded.Detail, nwNet) {
			t.Errorf("detail %q does not name network %s", coded.Detail, nwNet)
		}
		if nwEndpoint(t, h, user.ID) != nil {
			t.Error("NPM was put back on the network after a failed reload")
		}
	})
}

func TestA10_APassWithNoHitRecordsNoChangeAndAHitOnNPMsOwnNetworkFailsOnce(t *testing.T) {
	t.Run("no hit", func(t *testing.T) {
		h, _ := sqHit(t)
		w12aSeed(h, "10.9.0.99", 3000)
		n := h.nm()
		n.d.resolve = sqNoAnswer
		applies := 0
		for range 3 {
			if err := sqPass(t, h, n, n.reconcileTask(), &applies); err != nil {
				t.Fatal(err)
			}
		}
		if applies != 0 {
			t.Errorf("%d changes were applied for passes with no hit", applies)
		}
	})
	t.Run("a hit on darkpanel-npm", func(t *testing.T) {
		h, _ := nwSetup(t)
		own, _ := h.docker.network(networkName)
		ep := nwEndpoint(t, h, own.ID)
		w12aSeed(h, ep.IPAddress.String(), 3000)
		n := h.nm()
		n.d.resolve = sqNoAnswer
		task := n.reconcileTask()
		applies, failures := 0, 0
		for range 4 {
			err := sqPass(t, h, n, task, &applies)
			if err != nil {
				failures++
				sqWantCode(t, err, errcode.NPMReserved)
			}
		}
		if applies != 1 || failures != 1 {
			t.Errorf("%d applies and %d failures over 4 passes on the same hit, want 1 and 1", applies, failures)
		}
		if nwEndpoint(t, h, own.ID) == nil {
			t.Error("NPM left its own network")
		}
		if c, _ := h.docker.container(containerName); !c.State.Running {
			t.Error("NPM was stopped")
		}
		h.npm.DisableProxyHost(1)
		w12aSeed(h, "10.9.99.1", 3000)
		w12aSeed(h, ep.IPAddress.String(), 3001)
		if err := sqPass(t, h, n, task, &applies); err == nil || applies != 2 {
			t.Errorf("after the hit changed: %d applies, error %v; want the second apply to fail", applies, err)
		}
	})
}

func TestA11_APassDoesNothingWhileNoInstanceNPMStoppedOrTheLockHeld(t *testing.T) {
	t.Run("no instance", func(t *testing.T) {
		h := newTHHost(t)
		n := h.nm()
		applies := 0
		if err := sqPass(t, h, n, n.reconcileTask(), &applies); err != nil || applies != 0 {
			t.Errorf("applied %d times with error %v", applies, err)
		}
	})
	t.Run("NPM stopped", func(t *testing.T) {
		h, _ := sqHit(t)
		w12aSeed(h, sqHeld, 3000)
		if _, err := h.docker.StopContainer(context.Background(), containerName, time.Second); err != nil {
			t.Fatal(err)
		}
		n := h.nm()
		n.d.resolve = sqNoAnswer
		applies := 0
		if err := sqPass(t, h, n, n.reconcileTask(), &applies); err != nil || applies != 0 {
			t.Errorf("applied %d times with error %v", applies, err)
		}
	})
	t.Run("lock held", func(t *testing.T) {
		h, _ := sqHit(t)
		w12aSeed(h, sqHeld, 3000)
		n := h.nm()
		n.d.resolve = sqNoAnswer
		task := n.reconcileTask()
		if !slices.Equal(task.LockKeys, []string{"npm"}) {
			t.Fatalf("lock keys %v, want exactly the npm key that every mutating npm action takes", task.LockKeys)
		}
		for _, d := range (module{n: n}).Manifest().Actions {
			if d.Spec.Name == "npm.network.attach" && !slices.Equal(d.Spec.LockKeys, task.LockKeys) {
				t.Errorf("attach takes %v, the task %v", d.Spec.LockKeys, task.LockKeys)
			}
		}
	})
}

const sqUnansweredLoc = "location /x of proxy host 1 forwards to gone.invalid, which has no answer"

func sqSeedUnansweredLocation(h *thHost) {
	w12aSeed(h, "app.example.net", 8080, npmapi.Location{Path: "/x", ForwardScheme: "http", ForwardHost: "gone.invalid", ForwardPort: 80})
}

func sqWantUnanswered(t *testing.T, err error, what string) {
	t.Helper()
	coded := sqWantCode(t, err, errcode.NPMForwardUnanswered)
	if !core.IsDefinite(err) {
		t.Errorf("%s: error %v is not Definite", what, err)
	}
	if coded.Code.Status() != http.StatusConflict {
		t.Errorf("%s: status %d, want 409", what, coded.Code.Status())
	}
	for _, part := range []string{"/x", "gone.invalid"} {
		if !strings.Contains(coded.Detail, part) {
			t.Errorf("%s: detail %q does not name %q", what, coded.Detail, part)
		}
	}
}

func TestA12_UpgradeRefusesAnUnansweredCustomLocation(t *testing.T) {
	ctx := context.Background()
	t.Run("plan and apply before the pull", func(t *testing.T) {
		s := w12aUpSetup(t)
		sqSeedUnansweredLocation(s.h)
		s.n.d.resolve = sqNoAnswer
		_, err := s.n.instanceUpgrade().Handler.Plan(ctx, s.h.env(""), json.RawMessage(`{}`))
		sqWantUnanswered(t, err, "plan")
		_, err = s.apply()
		sqWantUnanswered(t, err, "apply")
		if len(s.effects()) != 0 || s.dock.counts["StopContainer"] != 0 || s.dock.counts["PullImage"] != 0 || len(s.dock.created) != 0 {
			t.Error("the refused upgrade journaled an effect, pulled, stopped NPM or created a container")
		}
	})
	t.Run("apply after the pull", func(t *testing.T) {
		s := w12aUpSetup(t)
		sqSeedUnansweredLocation(s.h)
		answering := true
		s.n.d.resolve = func(c context.Context, h string) ([]netip.Addr, error) {
			if answering {
				return []netip.Addr{netip.MustParseAddr("203.0.113.9")}, nil
			}
			return sqNoAnswer(c, h)
		}
		s.dock.missing[Image] = true
		s.dock.trace = func(c string) {
			if c == "PullImage" {
				answering = false
				s.dock.missing[Image] = false
			}
		}
		_, err := s.apply()
		sqWantUnanswered(t, err, "apply after the pull")
		if s.dock.counts["PullImage"] == 0 {
			t.Error("the upgrade did not pull, so the check after the pull was not exercised")
		}
		if len(s.effects()) != 0 || s.dock.counts["StopContainer"] != 0 {
			t.Error("the refused upgrade journaled an effect or stopped NPM")
		}
	})
	t.Run("undo of a finished upgrade", func(t *testing.T) {
		f := urSetup(t)
		f.replace(urTo)
		w12eDone(t, f, w12eAppNet(), nil)
		sqSeedUnansweredLocation(f.h)
		f.n.d.resolve = sqNoAnswer
		mark := f.mark()
		err := f.inverse(urResource)
		sqWantUnanswered(t, err, "undo")
		if urCalled(f.callsSince(mark), "StopContainer") || urCalled(f.callsSince(mark), "RemoveContainer") {
			t.Error("the refused undo stopped or removed NPM")
		}
	})
	t.Run("a default forward host with no answer does not refuse", func(t *testing.T) {
		s := w12aUpSetup(t)
		w12aSeed(s.h, "gone.invalid", 8080)
		s.n.d.resolve = sqNoAnswer
		if _, err := s.apply(); err != nil {
			t.Fatalf("an unanswered default forward host refused the upgrade: %v", err)
		}
	})
	t.Run("an unanswered location leaves the join guard's targets unchanged", func(t *testing.T) {
		h, _ := nwSetup(t)
		w12aSubnet(t, h, dockerapi.Network{})
		sqSeedUnansweredLocation(h)
		w12aSeed(h, "10.9.0.50", 3000)
		n := h.nm()
		n.d.resolve = sqNoAnswer
		targets, err := n.forwardTargets(context.Background(), h.env(""))
		if err != nil {
			t.Fatal(err)
		}
		if len(targets) != 1 || targets[0].addr != netip.MustParseAddr("10.9.0.50") {
			t.Errorf("targets %+v, want only the literal 10.9.0.50", targets)
		}
	})
}

func TestA13_DisabledProxyHostsAreIgnoredAndEnabledOnesAreChecked(t *testing.T) {
	in := AttachInput{Network: nwNet, Claimant: nwClaimant, Target: "web"}
	t.Run("the join guard and the held-address check", func(t *testing.T) {
		w := sqNewWorld(t, sqAnswers("203.0.113.9"))
		w12aSeed(w.h, sqHeld, 3000)
		w.h.docker.pin(nwNet, netip.MustParseAddr(sqHeld))
		w.h.npm.DisableProxyHost(1)
		if err := w.attach(t, in); err != nil {
			t.Fatalf("a disabled proxy host refused the attach: %v", err)
		}
		if ep := nwEndpoint(t, w.h, w.user.ID); ep == nil || ep.IPAddress != netip.MustParseAddr(sqHeld) {
			t.Errorf("endpoint %+v, want NPM on the network at %s", ep, sqHeld)
		}
		w2 := sqNewWorld(t, sqAnswers("203.0.113.9"))
		w12aSeed(w2.h, sqHeld, 3000)
		w2.h.docker.pin(nwNet, netip.MustParseAddr(sqHeld))
		qaErr := w2.attach(t, in)
		sqWantCode(t, qaErr, errcode.NPMReserved)
	})
	t.Run("a reconcile pass after an administrator enables the host", func(t *testing.T) {
		h, user := sqHit(t)
		w12aSeed(h, sqHeld, 3000)
		h.npm.DisableProxyHost(1)
		n := h.nm()
		n.d.resolve = sqNoAnswer
		task := n.reconcileTask()
		applies := 0
		if err := sqPass(t, h, n, task, &applies); err != nil || applies != 0 || nwEndpoint(t, h, user.ID) == nil {
			t.Fatalf("a pass with only a disabled host applied %d times (error %v) or took NPM off", applies, err)
		}
		w12aSeed(h, sqHeld, 3001)
		if err := sqPass(t, h, n, task, &applies); err != nil || applies != 1 || nwEndpoint(t, h, user.ID) != nil {
			t.Errorf("after a host was enabled the pass applied %d times (error %v) and NPM is on the network: %v", applies, err, nwEndpoint(t, h, user.ID) != nil)
		}
	})
	t.Run("the upgrade checks", func(t *testing.T) {
		s := w12aUpSetup(t)
		sqSeedUnansweredLocation(s.h)
		s.h.npm.DisableProxyHost(1)
		s.n.d.resolve = sqNoAnswer
		if _, err := s.apply(); err != nil {
			t.Errorf("a disabled host's unanswered location refused the upgrade: %v", err)
		}
		s2 := w12aUpSetup(t)
		w12aSeed(s2.h, "172.20.0.9", 3000)
		s2.h.npm.DisableProxyHost(1)
		if _, err := s2.apply(); err != nil {
			t.Errorf("a disabled host's forward to an address NPM could take refused the upgrade: %v", err)
		}
	})
	t.Run("Darkpanel can neither send nor call an enabled member", func(t *testing.T) {
		if _, ok := reflect.TypeOf(npmapi.ProxyHostBody{}).FieldByName("Enabled"); ok {
			t.Error("ProxyHostBody has an Enabled member")
		}
		raw, err := json.Marshal(npmapi.ProxyHostBody{})
		if err != nil || strings.Contains(string(raw), `"enabled"`) {
			t.Errorf("body %s (%v) holds an enabled member", raw, err)
		}
		api := reflect.TypeOf((*npmAPI)(nil)).Elem()
		for i := range api.NumMethod() {
			if strings.Contains(strings.ToLower(api.Method(i).Name), "enable") {
				t.Errorf("npmAPI has %s", api.Method(i).Name)
			}
		}
	})
}

type sqSlowEngine struct{ *thDocker }

func (d sqSlowEngine) InspectContainer(ctx context.Context, id string) (dockerapi.ContainerJSON, error) {
	if id == containerName {
		<-ctx.Done()
		return dockerapi.ContainerJSON{}, ctx.Err()
	}
	return d.thDocker.InspectContainer(ctx, id)
}

func TestA14_AnUnresolvableForwardNameRefusesAsUnresolved(t *testing.T) {
	temporary := func(_ context.Context, host string) ([]netip.Addr, error) {
		return nil, &net.DNSError{Err: "server misbehaving", Name: host, IsTemporary: true}
	}
	in := AttachInput{Network: nwNet, Claimant: nwClaimant, Target: "web"}
	t.Run("a proxy host", func(t *testing.T) {
		w := sqNewWorld(t, temporary)
		w12aSeed(w.h, "db.internal", 5432)
		err := w.attach(t, in)
		coded := sqWantCode(t, err, errcode.NPMForwardUnresolved)
		if coded.Code.Status() != http.StatusConflict {
			t.Errorf("status %d, want 409", coded.Code.Status())
		}
		for _, part := range []string{"proxy host 1", "db.internal", "stopped or removed container"} {
			if !strings.Contains(coded.Detail, part) {
				t.Errorf("detail %q does not hold %q", coded.Detail, part)
			}
		}
	})
	t.Run("a custom location", func(t *testing.T) {
		w := sqNewWorld(t, temporary)
		w12aSeed(w.h, "10.9.0.50", 80, npmapi.Location{Path: "/api", ForwardScheme: "http", ForwardHost: "db.internal", ForwardPort: 80})
		coded := sqWantCode(t, w.attach(t, in), errcode.NPMForwardUnresolved)
		for _, part := range []string{"location /api", "proxy host 1", "db.internal", "stopped or removed container"} {
			if !strings.Contains(coded.Detail, part) {
				t.Errorf("detail %q does not hold %q", coded.Detail, part)
			}
		}
	})
	t.Run("Docker's names read too late", func(t *testing.T) {
		h, _ := nwSetup(t)
		w12aSeed(h, "db.internal", 5432)
		n := h.nm()
		n.d.docker = sqSlowEngine{h.docker}
		n.d.resolve = sqNoAnswer
		_, err := n.forwardTargets(context.Background(), h.env(""))
		coded := sqWantCode(t, err, errcode.NPMForwardUnresolved)
		if !strings.Contains(strings.ToLower(coded.Detail), "retry") {
			t.Errorf("detail %q does not say to retry", coded.Detail)
		}
		for _, part := range []string{"proxy host", "db.internal"} {
			if strings.Contains(coded.Detail, part) {
				t.Errorf("detail %q names %q, want no host", coded.Detail, part)
			}
		}
	})
}

func TestA21_AttachLookupPassesAndTheDeadlineBudget(t *testing.T) {
	w := sqNewWorld(t, sqAnswers("203.0.113.9"))
	w12aSeed(w.h, "up.invalid", 3000)
	if err := w.attach(t, AttachInput{Network: nwNet, Claimant: nwClaimant, Target: "web"}); err != nil {
		t.Fatal(err)
	}
	if got := w.log.count("lookup"); got != 2 {
		t.Errorf("%d lookup passes of the one name, want 2: the guard in Apply and the afresh read after the join", got)
	}
	if got := w.h.nginx.reloads; got != 2 {
		t.Errorf("%d reloads, want 2", got)
	}
	budget := w.n.networkAttach().Spec.ApplyTimeout
	if budget != 45*time.Second {
		t.Errorf("the attach's apply budget is %s, want 45s", budget)
	}
	const lookupPass, reload, disconnect = 6 * time.Second, 10 * time.Second, 10 * time.Second
	if worst := 2*lookupPass + 2*reload + disconnect; worst > budget {
		t.Errorf("two lookup passes, two reloads and the disconnect take %s, over the %s budget", worst, budget)
	}
	if nginxReloadWait != reload || nginxDisconnectWait != disconnect || resolveTimeout+time.Second != lookupPass {
		t.Errorf("constants reload %s, disconnect %s, lookup %s differ from the 10s, 10s and 6s the budget counts",
			nginxReloadWait, nginxDisconnectWait, resolveTimeout+time.Second)
	}
}
