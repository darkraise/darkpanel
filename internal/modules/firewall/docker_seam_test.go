package firewall

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/modules/firewall/loaded"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
	"github.com/darkraise/darkpanel/internal/platform/netfilter"
	"github.com/darkraise/darkpanel/internal/platform/netstat"
	"github.com/darkraise/darkpanel/internal/platform/tslocal"
)

var _ dockerAPI = (*fakeDocker)(nil)

// TestFdFakeDockerDefaults: a new fake is Docker 29.9.1 with the iptables
// driver, no containers and the default bridge network, and it records
// each call by its method name.
func TestFdFakeDockerDefaults(t *testing.T) {
	ctx := t.Context()
	d := newFakeDocker()
	info, err := d.Info(ctx)
	if err != nil || info.ServerVersion != "29.9.1" || info.FirewallBackend == nil || info.FirewallBackend.Driver != "iptables" {
		t.Errorf("Info = %+v, %v; want Docker 29.9.1 with the iptables driver", info, err)
	}
	if v, err := d.Version(ctx); err != nil || v.Version != "29.9.1" {
		t.Errorf("Version = %+v, %v; want 29.9.1", v, err)
	}
	if cs, err := d.ListContainers(ctx, dockerapi.ListContainersOptions{All: true}); err != nil || len(cs) != 0 {
		t.Errorf("ListContainers = %v, %v; want none", cs, err)
	}
	nets, err := d.ListNetworks(ctx, nil)
	if err != nil || len(nets) != 1 {
		t.Fatalf("ListNetworks = %v, %v; want the bridge network only", nets, err)
	}
	n := nets[0]
	if n.Name != "bridge" || n.Options["com.docker.network.bridge.name"] != "docker0" ||
		len(n.IPAM.Config) != 1 || n.IPAM.Config[0].Subnet != netip.MustParsePrefix("172.17.0.0/16") {
		t.Errorf("network %+v, want bridge on docker0 with 172.17.0.0/16", n)
	}
	want := []string{"Info", "Version", "ListContainers", "ListNetworks"}
	if !reflect.DeepEqual(d.calls, want) {
		t.Errorf("calls %q, want %q", d.calls, want)
	}
}

// TestFdFakeDockerContainers: the list holds the running containers unless
// All is set, an inspect finds a container by id, name or /name and answers
// the daemon's 404 otherwise, and err fails every call.
func TestFdFakeDockerContainers(t *testing.T) {
	ctx := t.Context()
	d := newFakeDocker()
	web := fdContainer("web", func(c *dockerapi.ContainerJSON) { c.Config.Labels["com.docker.compose.project"] = "shop" })
	gone := fdContainer("gone", func(c *dockerapi.ContainerJSON) { c.State = dockerapi.ContainerState{Status: "exited"} })
	d.containers = []dockerapi.ContainerJSON{web, gone}

	running, err := d.ListContainers(ctx, dockerapi.ListContainersOptions{})
	if err != nil || len(running) != 1 {
		t.Fatalf("running containers = %v, %v; want web only", running, err)
	}
	if s := running[0]; s.ID != web.ID || !reflect.DeepEqual(s.Names, []string{"/web"}) || s.State != "running" || s.Labels["com.docker.compose.project"] != "shop" {
		t.Errorf("summary %+v, want web, running, with its labels", s)
	}
	all, err := d.ListContainers(ctx, dockerapi.ListContainersOptions{All: true})
	if err != nil || len(all) != 2 || all[1].State != "exited" {
		t.Errorf("all containers = %v, %v; want web and the exited gone", all, err)
	}

	d.calls = nil
	for _, id := range []string{web.ID, "web", "/web"} {
		got, err := d.InspectContainer(ctx, id)
		if err != nil || got.ID != web.ID {
			t.Errorf("InspectContainer(%q) = %q, %v; want web", id, got.ID, err)
		}
	}
	if _, err := d.InspectContainer(ctx, "nope"); !dockerapi.IsNotFound(err) {
		t.Errorf("InspectContainer(nope) = %v, want the daemon's 404", err)
	}
	if want := []string{"InspectContainer " + web.ID, "InspectContainer web", "InspectContainer /web", "InspectContainer nope"}; !reflect.DeepEqual(d.calls, want) {
		t.Errorf("calls %q, want %q", d.calls, want)
	}

	boom := errors.New("fdtest: the daemon is down")
	d.err = boom
	if _, err := d.Info(ctx); !errors.Is(err, boom) {
		t.Errorf("Info = %v, want %v", err, boom)
	}
	if _, err := d.Version(ctx); !errors.Is(err, boom) {
		t.Errorf("Version = %v, want %v", err, boom)
	}
	if _, err := d.ListContainers(ctx, dockerapi.ListContainersOptions{}); !errors.Is(err, boom) {
		t.Errorf("ListContainers = %v, want %v", err, boom)
	}
	if _, err := d.InspectContainer(ctx, "web"); !errors.Is(err, boom) {
		t.Errorf("InspectContainer = %v, want %v", err, boom)
	}
	if _, err := d.ListNetworks(ctx, nil); !errors.Is(err, boom) {
		t.Errorf("ListNetworks = %v, want %v", err, boom)
	}
	if s, err := d.Events(ctx, dockerapi.EventsOptions{}); !errors.Is(err, boom) || s != nil {
		t.Errorf("Events = %v, %v; want no stream and %v", s, err, boom)
	}
}

// TestFdFakeDockerEvents: a stream delivers what the test sends on events,
// ends with io.EOF once it is closed or events is closed, and with the
// context's error once the context ends.
func TestFdFakeDockerEvents(t *testing.T) {
	d := newFakeDocker()
	s, err := d.Events(t.Context(), dockerapi.EventsOptions{})
	if err != nil {
		t.Fatal(err)
	}
	d.events <- dockerapi.Event{Type: "container", Action: "start"}
	ev, err := s.Next()
	if err != nil || ev.Type != "container" || ev.Action != "start" {
		t.Errorf("Next = %+v, %v; want the container start", ev, err)
	}
	if err := s.Close(); err != nil {
		t.Errorf("Close = %v", err)
	}
	if err := s.Close(); err != nil {
		t.Errorf("second Close = %v", err)
	}
	if _, err := s.Next(); !errors.Is(err, io.EOF) {
		t.Errorf("Next after Close = %v, want io.EOF", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	s2, err := d.Events(ctx, dockerapi.EventsOptions{})
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if _, err := s2.Next(); !errors.Is(err, context.Canceled) {
		t.Errorf("Next after cancel = %v, want context.Canceled", err)
	}

	close(d.events)
	s3, err := d.Events(t.Context(), dockerapi.EventsOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s3.Next(); !errors.Is(err, io.EOF) {
		t.Errorf("Next on a closed events channel = %v, want io.EOF", err)
	}
	if want := []string{"Events", "Events", "Events"}; !reflect.DeepEqual(d.calls, want) {
		t.Errorf("calls %q, want %q", d.calls, want)
	}
}

// TestFdNetwork: fdNetwork names the bridge through Docker's option only
// when one is given, marks IPv6 from the subnets and gives a name its own
// stable 64-digit id.
func TestFdNetwork(t *testing.T) {
	n := fdNetwork("custom", "dpcustom", "172.30.0.0/24", "fd00:dead::/64")
	if n.Name != "custom" || n.Driver != "bridge" || n.Options["com.docker.network.bridge.name"] != "dpcustom" || !n.EnableIPv6 {
		t.Errorf("network %+v, want bridge-driver custom on dpcustom with IPv6", n)
	}
	var subnets []netip.Prefix
	for _, c := range n.IPAM.Config {
		subnets = append(subnets, c.Subnet)
	}
	if want := []netip.Prefix{netip.MustParsePrefix("172.30.0.0/24"), netip.MustParsePrefix("fd00:dead::/64")}; !reflect.DeepEqual(subnets, want) {
		t.Errorf("subnets %v, want %v", subnets, want)
	}
	if len(n.ID) != 64 || n.ID != fdNetwork("custom", "").ID || n.ID == fdNetwork("other", "").ID {
		t.Errorf("id %q, want 64 digits, stable per name and distinct between names", n.ID)
	}
	plain := fdNetwork("plain", "", "10.9.0.0/24")
	if _, ok := plain.Options["com.docker.network.bridge.name"]; ok || plain.EnableIPv6 {
		t.Errorf("network %+v, want no bridge option and no IPv6", plain)
	}
}

// TestFdContainer: a container is running and has empty label, port and
// network maps for options to fill, and an option may change anything.
func TestFdContainer(t *testing.T) {
	c := fdContainer("web")
	if c.Name != "/web" || len(c.ID) != 64 || !c.State.Running || c.State.Status != "running" {
		t.Errorf("container %+v, want /web, a 64-digit id, running", c)
	}
	if c.Config.Labels == nil || c.NetworkSettings.Ports == nil || c.NetworkSettings.Networks == nil {
		t.Errorf("container %+v, want empty non-nil maps", c)
	}
	c2 := fdContainer("web", func(c *dockerapi.ContainerJSON) {
		c.NetworkSettings.Networks["bridge"] = &dockerapi.EndpointSettings{IPAddress: netip.MustParseAddr("172.17.0.2")}
	})
	if c2.ID != c.ID || c2.NetworkSettings.Networks["bridge"].IPAddress != netip.MustParseAddr("172.17.0.2") {
		t.Errorf("container %+v, want the same id and the option's endpoint", c2)
	}
}

// TestFdDeps: fdDeps extends the host's deps with the Docker fake and
// answers every other Docker-facing read as an empty host, or from the
// fake host's hooks, which it reads when called.
func TestFdDeps(t *testing.T) {
	ctx := t.Context()
	h := newFakeHost(t)
	d := newFakeDocker()
	dd := fdDeps(h, d)
	if dd.docker != dockerAPI(d) || dd.root != h.root {
		t.Errorf("docker %v root %q, want the fake and the host's root", dd.docker, dd.root)
	}
	if cfg, err := dd.readConfig(dd.root); err != nil || !cfg.Enabled {
		t.Errorf("readConfig = %v, %v; want the host's configuration", cfg.Enabled, err)
	}
	if fdDeps(h, nil).docker != nil {
		t.Error("fdDeps(h, nil) set docker")
	}

	rs, err := dd.readVariant(ctx, h.env(), fwstate.V4, "legacy", []string{"filter"})
	if rs != nil || err != nil {
		t.Errorf("readVariant = %v, %v; want nil, nil", rs, err)
	}
	if socks, err := dd.sockets(); socks != nil || err != nil {
		t.Errorf("sockets = %v, %v; want none", socks, err)
	}
	if ifs, err := dd.interfaces(); ifs != nil || err != nil {
		t.Errorf("interfaces = %v, %v; want none", ifs, err)
	}
	if _, err := dd.routeGet(netstat.RouteQuery{Dst: netip.MustParseAddr("192.0.2.1")}); !errors.Is(err, netstat.ErrNoRoute) {
		t.Errorf("routeGet = %v, want netstat.ErrNoRoute", err)
	}
	if on, known, err := dd.shieldsUp(ctx); on || known || err != nil {
		t.Errorf("shieldsUp = %v, %v, %v; want false, false, nil", on, known, err)
	}

	want := fhParse(t, fhSave)
	var gotFamily fwstate.Family
	var gotVariant string
	var gotTables []string
	h.onReadVariant = func(f fwstate.Family, variant string, tables []string) (*netfilter.Ruleset, error) {
		gotFamily, gotVariant, gotTables = f, variant, tables
		return want, nil
	}
	if rs, err := dd.readVariant(ctx, h.env(), fwstate.V6, "nft", []string{"filter", "raw"}); rs != want || err != nil ||
		gotFamily != fwstate.V6 || gotVariant != "nft" || !reflect.DeepEqual(gotTables, []string{"filter", "raw"}) {
		t.Errorf("readVariant = %v, %v for %v %q %q; want the hook's ruleset for the arguments", rs, err, gotFamily, gotVariant, gotTables)
	}
	h.onSockets = func() ([]netstat.Socket, error) { return []netstat.Socket{{Proto: netstat.ProtoTCP}}, nil }
	if socks, err := dd.sockets(); err != nil || len(socks) != 1 {
		t.Errorf("sockets = %v, %v; want the hook's socket", socks, err)
	}
	h.onInterfaces = func() ([]netstat.Interface, error) { return []netstat.Interface{{Name: "docker0"}}, nil }
	if ifs, err := dd.interfaces(); err != nil || len(ifs) != 1 || ifs[0].Name != "docker0" {
		t.Errorf("interfaces = %v, %v; want the hook's interface", ifs, err)
	}
	h.onRouteGet = func(q netstat.RouteQuery) (netstat.Route, error) {
		return netstat.Route{OutIface: "eth0", PrefSrc: q.Dst}, nil
	}
	if rt, err := dd.routeGet(netstat.RouteQuery{Dst: netip.MustParseAddr("192.0.2.1")}); err != nil || rt.OutIface != "eth0" {
		t.Errorf("routeGet = %+v, %v; want the hook's route", rt, err)
	}
	h.onShieldsUp = func() (bool, bool, error) { return true, true, nil }
	if on, known, err := dd.shieldsUp(ctx); !on || !known || err != nil {
		t.Errorf("shieldsUp = %v, %v, %v; want true, true, nil", on, known, err)
	}
}

// TestFdProductionDocker: the production deps read Docker through the
// client adapter and the host through the platform packages.
func TestFdProductionDocker(t *testing.T) {
	f := productionFW()
	if _, ok := f.d.docker.(dockerClient); !ok {
		t.Errorf("docker is %T, want dockerClient", f.d.docker)
	}
	wrSameFunc(t, "readVariant", f.d.readVariant, loaded.Reader{}.ReadVariant)
	wrSameFunc(t, "sockets", f.d.sockets, netstat.Sockets)
	wrSameFunc(t, "interfaces", f.d.interfaces, netstat.Interfaces)
	wrSameFunc(t, "routeGet", f.d.routeGet, netstat.RouteGet)
	if f.d.shieldsUp == nil {
		t.Error("shieldsUp is nil")
	}
}

// TestFdShieldsUpVia: shields-up is tailscaled's preference, and a
// tailscaled that cannot be reached is not known to have it on.
func TestFdShieldsUpVia(t *testing.T) {
	boom := errors.New("fdtest: tailscaled failed")
	cases := []struct {
		name      string
		prefs     func(context.Context) (*tslocal.Prefs, error)
		on, known bool
		err       error
	}{
		{"on", func(context.Context) (*tslocal.Prefs, error) { return &tslocal.Prefs{ShieldsUp: true}, nil }, true, true, nil},
		{"off", func(context.Context) (*tslocal.Prefs, error) { return &tslocal.Prefs{}, nil }, false, true, nil},
		{"unavailable", func(context.Context) (*tslocal.Prefs, error) {
			return nil, fmt.Errorf("tslocal: GET prefs: %w", tslocal.ErrUnavailable)
		}, false, false, nil},
		{"failed", func(context.Context) (*tslocal.Prefs, error) { return nil, boom }, false, false, boom},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			on, known, err := shieldsUpVia(tc.prefs)(t.Context())
			if on != tc.on || known != tc.known || !errors.Is(err, tc.err) {
				t.Errorf("shieldsUp = %v, %v, %v; want %v, %v, %v", on, known, err, tc.on, tc.known, tc.err)
			}
		})
	}
}

// fdDaemon serves the Docker API on a Unix socket in a short temporary
// directory and returns the socket's path.
func fdDaemon(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "dp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	mux := http.NewServeMux()
	mux.HandleFunc("GET /version", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"Version":"29.9.1","ApiVersion":"1.49","MinAPIVersion":"1.44"}`)
	})
	mux.HandleFunc("GET /v1.49/info", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"ServerVersion":"29.9.1","FirewallBackend":{"Driver":"iptables"}}`)
	})
	mux.HandleFunc("GET /v1.49/events", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"Type":"container","Action":"start","Actor":{"ID":"c1","Attributes":{"name":"web"}}}`+"\n")
	})
	l, err := net.Listen("unix", filepath.Join(dir, "d.sock"))
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(mux)
	_ = srv.Listener.Close()
	srv.Listener = l
	srv.Start()
	t.Cleanup(srv.Close)
	return filepath.Join(dir, "d.sock")
}

// TestFdDockerClient: the adapter passes the client's answers through,
// hands back the events stream, and returns no stream, not a nil pointer
// inside the interface, when the stream cannot be opened.
func TestFdDockerClient(t *testing.T) {
	c := newDockerClient(fdDaemon(t))
	info, err := c.Info(t.Context())
	if err != nil || info.ServerVersion != "29.9.1" || info.FirewallBackend == nil || info.FirewallBackend.Driver != "iptables" {
		t.Errorf("Info = %+v, %v; want Docker 29.9.1 with the iptables driver", info, err)
	}
	s, err := c.Events(t.Context(), dockerapi.EventsOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	ev, err := s.Next()
	if err != nil || ev.Type != "container" || ev.Action != "start" || ev.Actor.Attributes["name"] != "web" {
		t.Errorf("Next = %+v, %v; want the container start of web", ev, err)
	}
	if _, err := s.Next(); !errors.Is(err, io.EOF) {
		t.Errorf("Next at the end of the stream = %v, want io.EOF", err)
	}

	down := newDockerClient(filepath.Join(t.TempDir(), "none.sock"))
	if _, err := down.Info(t.Context()); !errors.Is(err, dockerapi.ErrUnavailable) {
		t.Errorf("Info without a daemon = %v, want dockerapi.ErrUnavailable", err)
	}
	if s, err := down.Events(t.Context(), dockerapi.EventsOptions{}); !errors.Is(err, dockerapi.ErrUnavailable) || s != nil {
		t.Errorf("Events without a daemon = %v, %v; want no stream and dockerapi.ErrUnavailable", s, err)
	}
}
