package guard

import (
	"context"
	"errors"
	"net/netip"
	"os"
	"reflect"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/platform/netstat"
)

// gHost is the fake Host every test of the package uses. routes maps a
// lookup to its answer; a lookup it does not hold fails with ErrNoRoute.
type gHost struct {
	sockets    []netstat.Socket
	socketsErr error
	ifaces     []netstat.Interface
	ifacesErr  error
	routes     map[netstat.RouteQuery]netstat.Route
	routeErrs  map[netstat.RouteQuery]error
	pid        int
	lookups    []netstat.RouteQuery
}

func (h *gHost) Sockets() ([]netstat.Socket, error) { return h.sockets, h.socketsErr }

func (h *gHost) Interfaces() ([]netstat.Interface, error) { return h.ifaces, h.ifacesErr }

func (h *gHost) RouteGet(q netstat.RouteQuery) (netstat.Route, error) {
	h.lookups = append(h.lookups, q)
	if err, ok := h.routeErrs[q]; ok {
		return netstat.Route{}, err
	}
	if r, ok := h.routes[q]; ok {
		return r, nil
	}
	return netstat.Route{}, netstat.ErrNoRoute
}

func (h *gHost) PID() int { return h.pid }

// gFiles is the fake FileWriter: it answers ReadSysctl from sysctl and
// panics on any other method.
type gFiles struct {
	core.FileWriter
	sysctl map[string]string
	err    error
	reads  []string
}

func (f *gFiles) ReadSysctl(_ context.Context, module, key string) (string, error) {
	f.reads = append(f.reads, module+" "+key)
	if f.err != nil {
		return "", f.err
	}
	v, ok := f.sysctl[key]
	if !ok {
		return "", errors.New("platform: no such key " + key)
	}
	return v, nil
}

// gUnits is the fake Units: it answers State from state and panics on any
// other method.
type gUnits struct {
	core.Units
	state core.UnitState
	err   error
	asked []string
}

func (u *gUnits) State(_ context.Context, unit string) (core.UnitState, error) {
	u.asked = append(u.asked, unit)
	return u.state, u.err
}

// gEnv is an Env with the fakes, the controller unit absent and rp_filter 0
// everywhere for the interfaces of gIfaces.
func gEnv() (core.Env, *gFiles, *gUnits) {
	files := &gFiles{sysctl: map[string]string{
		"net/ipv4/conf/all/rp_filter": "0", "net/ipv4/conf/eth0/rp_filter": "0", "net/ipv4/conf/eth1/rp_filter": "0",
	}}
	units := &gUnits{state: core.UnitState{LoadState: "not-found", ActiveState: "inactive", SubState: "dead"}}
	return core.Env{Files: files, Units: units}, files, units
}

// gIfaces are lo, two up interfaces and one down interface.
func gIfaces() []netstat.Interface {
	return []netstat.Interface{
		{Index: 1, Name: "lo", Up: true, Running: true, Loopback: true, Addrs: []netip.Prefix{netip.MustParsePrefix("127.0.0.1/8"), netip.MustParsePrefix("::1/128")}},
		{Index: 2, Name: "eth0", Up: true, Running: true, Addrs: []netip.Prefix{netip.MustParsePrefix("192.0.2.1/24"), netip.MustParsePrefix("2001:db8::1/64")}},
		{Index: 3, Name: "eth1", Up: true, Running: true, Addrs: []netip.Prefix{netip.MustParsePrefix("10.0.0.1/24")}},
		{Index: 4, Name: "eth2", Up: false, Addrs: []netip.Prefix{netip.MustParsePrefix("10.9.0.1/24")}},
	}
}

func TestGatherFacts(t *testing.T) {
	env, files, units := gEnv()
	files.sysctl["net/ipv4/conf/all/rp_filter"] = "1"
	files.sysctl["net/ipv4/conf/eth1/rp_filter"] = "2"
	units.state = core.UnitState{LoadState: "loaded", ActiveState: "active", SubState: "running", MainPID: 4242}
	socks := []netstat.Socket{{Proto: netstat.ProtoTCP, Family: netstat.IPv4, State: netstat.StateListen,
		Local: netip.MustParseAddrPort("0.0.0.0:22"), Processes: []netstat.Process{{PID: 7, Comm: "sshd"}}}}
	h := &gHost{sockets: socks, ifaces: gIfaces(), pid: 99}
	got, err := gatherFacts(context.Background(), env, h)
	if err != nil {
		t.Fatal(err)
	}
	want := facts{
		sockets: socks, ifaces: gIfaces(),
		rpFilter:   map[string]int{"all": 1, "eth0": 0, "eth1": 2},
		controller: 4242, pid: 99,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("gatherFacts = %+v\nwant %+v", got, want)
	}
	wantReads := []string{
		"firewall net/ipv4/conf/all/rp_filter", "firewall net/ipv4/conf/eth0/rp_filter", "firewall net/ipv4/conf/eth1/rp_filter",
	}
	if !reflect.DeepEqual(files.reads, wantReads) {
		t.Errorf("sysctl reads = %q, want %q", files.reads, wantReads)
	}
	if !reflect.DeepEqual(units.asked, []string{"darkpanel-controller.service"}) {
		t.Errorf("unit states asked = %q", units.asked)
	}
}

func TestGatherFactsController(t *testing.T) {
	for _, tc := range []struct {
		name  string
		state core.UnitState
		want  int
	}{
		{"unit absent", core.UnitState{LoadState: "not-found", ActiveState: "inactive"}, 0},
		{"unit inactive", core.UnitState{LoadState: "loaded", ActiveState: "inactive", SubState: "dead"}, 0},
		{"unit failed without a process", core.UnitState{LoadState: "loaded", ActiveState: "failed", SubState: "failed"}, 0},
		{"unit activating with a process", core.UnitState{LoadState: "loaded", ActiveState: "activating", SubState: "start", MainPID: 77}, 77},
		{"unit running", core.UnitState{LoadState: "loaded", ActiveState: "active", SubState: "running", MainPID: 88}, 88},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env, _, units := gEnv()
			units.state = tc.state
			got, err := gatherFacts(context.Background(), env, &gHost{ifaces: gIfaces()})
			if err != nil {
				t.Fatal(err)
			}
			if got.controller != tc.want {
				t.Errorf("controller = %d, want %d", got.controller, tc.want)
			}
		})
	}
}

func TestGatherFactsErrors(t *testing.T) {
	boom := errors.New("boom")
	for _, tc := range []struct {
		name   string
		mutate func(h *gHost, env *core.Env, f *gFiles, u *gUnits)
		target error
	}{
		{"no socket tables", func(h *gHost, _ *core.Env, _ *gFiles, _ *gUnits) { h.socketsErr = netstat.ErrNoSocketTables }, netstat.ErrNoSocketTables},
		{"socket table unreadable", func(h *gHost, _ *core.Env, _ *gFiles, _ *gUnits) { h.socketsErr = boom }, boom},
		{"interfaces unreadable", func(h *gHost, _ *core.Env, _ *gFiles, _ *gUnits) { h.ifacesErr = boom }, boom},
		{"sysctl unreadable", func(_ *gHost, _ *core.Env, f *gFiles, _ *gUnits) { f.err = boom }, boom},
		{"unit state unreadable", func(_ *gHost, _ *core.Env, _ *gFiles, u *gUnits) { u.err = boom }, boom},
		{"no unit client", func(_ *gHost, env *core.Env, _ *gFiles, _ *gUnits) { env.Units = nil }, nil},
		{"no file writer", func(_ *gHost, env *core.Env, _ *gFiles, _ *gUnits) { env.Files = nil }, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env, files, units := gEnv()
			h := &gHost{ifaces: gIfaces()}
			tc.mutate(h, &env, files, units)
			_, err := gatherFacts(context.Background(), env, h)
			if err == nil {
				t.Fatal("gatherFacts succeeded")
			}
			if tc.target != nil && !errors.Is(err, tc.target) {
				t.Errorf("error %v does not wrap %v", err, tc.target)
			}
		})
	}
}

func TestGatherFactsBadSysctl(t *testing.T) {
	env, files, _ := gEnv()
	files.sysctl["net/ipv4/conf/eth0/rp_filter"] = "strict"
	if _, err := gatherFacts(context.Background(), env, &gHost{ifaces: gIfaces()}); err == nil {
		t.Fatal("gatherFacts accepted a non-numeric rp_filter")
	}
}

func TestLiveHostNoSocketTables(t *testing.T) {
	env, _, _ := gEnv()
	_, err := gatherFacts(context.Background(), env, LiveHost{ProcRoot: t.TempDir()})
	if !errors.Is(err, netstat.ErrNoSocketTables) {
		t.Fatalf("gatherFacts on an empty proc root: %v, want ErrNoSocketTables", err)
	}
}

func TestLiveHost(t *testing.T) {
	h := LiveHost{}
	if h.PID() != os.Getpid() {
		t.Errorf("PID = %d, want %d", h.PID(), os.Getpid())
	}
	socks, err := h.Sockets()
	if err != nil {
		t.Fatal(err)
	}
	if len(socks) == 0 {
		t.Error("the host has no sockets")
	}
	ifaces, err := h.Interfaces()
	if err != nil {
		t.Fatal(err)
	}
	loopback := false
	for _, i := range ifaces {
		loopback = loopback || i.Loopback
	}
	if !loopback {
		t.Error("no loopback interface")
	}
	r, err := h.RouteGet(netstat.RouteQuery{Dst: netip.MustParseAddr("127.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	if r.Type != "local" {
		t.Errorf("route to 127.0.0.1 has type %q, want local", r.Type)
	}
}
