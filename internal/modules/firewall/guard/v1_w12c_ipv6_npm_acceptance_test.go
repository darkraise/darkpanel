package guard

import (
	"context"
	"encoding/binary"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules/firewall/eval"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/platform/netstat"
)

const (
	qa12cTarget     = "172.31.0.2" // NPM's address on the darkpanel-npm network
	qa12cRelayLabel = "exposure exp1 IPv6 relay to " + qa12cTarget + ":443"
)

// qa12cTable is save output with ACCEPT policies except an OUTPUT policy of out, and the given rules.
func qa12cTable(out string, rules ...string) string {
	return "*filter\n:INPUT ACCEPT [0:0]\n:FORWARD ACCEPT [0:0]\n:OUTPUT " + out + " [0:0]\n" + strings.Join(rules, "\n") + "\nCOMMIT\n"
}

// qa12cEncode is the kernel's /proc/net/tcp6 spelling of an address and port: 32-bit words in host byte order.
func qa12cEncode(ap netip.AddrPort) string {
	b := ap.Addr().As16()
	var sb strings.Builder
	for i := 0; i < 16; i += 4 {
		var w [4]byte
		binary.BigEndian.PutUint32(w[:], binary.NativeEndian.Uint32(b[i:]))
		fmt.Fprintf(&sb, "%X", w[:])
	}
	return fmt.Sprintf("%s:%04X", sb.String(), ap.Port())
}

type qa12cListener struct {
	bind  string // "[::]:443"
	inode uint64
}

type qa12cProc struct {
	pid     int
	comm    string
	inode   uint64   // the listener socket it holds
	cmdline []string // nil: no cmdline file, as for a process that exited
}

// qa12cProcRoot writes a proc root with the given IPv6 TCP listeners and the processes that hold them.
func qa12cProcRoot(t *testing.T, listeners []qa12cListener, procs []qa12cProc) string {
	t.Helper()
	root := t.TempDir()
	write := func(path, data string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	header := "  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n"
	write(filepath.Join(root, "net", "tcp"), header)
	tcp6 := header
	for i, l := range listeners {
		tcp6 += fmt.Sprintf("%4d: %s %s 0A 00000000:00000000 00:00000000 00000000     0        0 %d 1 0000000000000000 100 0 0 10 0\n",
			i, qa12cEncode(netip.MustParseAddrPort(l.bind)), qa12cEncode(netip.AddrPortFrom(netip.IPv6Unspecified(), 0)), l.inode)
	}
	write(filepath.Join(root, "net", "tcp6"), tcp6)
	for _, p := range procs {
		dir := filepath.Join(root, strconv.Itoa(p.pid))
		if err := os.MkdirAll(filepath.Join(dir, "fd"), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(fmt.Sprintf("socket:[%d]", p.inode), filepath.Join(dir, "fd", "3")); err != nil {
			t.Fatal(err)
		}
		write(filepath.Join(dir, "comm"), p.comm+"\n")
		if p.cmdline != nil {
			write(filepath.Join(dir, "cmdline"), strings.Join(p.cmdline, "\x00")+"\x00")
		}
	}
	return root
}

// qa12cProxy is the command line of a docker-proxy that relays to target on port 443.
func qa12cProxy(host, target string) []string {
	return []string{"/usr/bin/docker-proxy", "-proto", "tcp", "-host-ip", host, "-host-port", "443",
		"-container-ip", target, "-container-port", "443", "-use-listen-fd"}
}

// qa12cHostOver is a host whose sockets and command lines come from the proc root and whose interfaces and routes
// are the fixture's, with the darkpanel-npm bridge.
type qa12cHostOver struct {
	LiveHost
	fib v1r21Fib
}

func (h qa12cHostOver) Interfaces() ([]netstat.Interface, error) { return h.fib.Interfaces() }

func (h qa12cHostOver) RouteGet(q netstat.RouteQuery) (netstat.Route, error) {
	return h.fib.RouteGet(q)
}

func (h qa12cHostOver) Routes(f netstat.Family) ([]netstat.Route, error) { return h.fib.Routes(f) }

func (h qa12cHostOver) Rules(f netstat.Family) ([]netstat.Rule, error) { return h.fib.Rules(f) }

func qa12cNewHost(root string) qa12cHostOver {
	ifaces := append(ckIfaces(), netstat.Interface{Index: 10, Name: "br-npm", Up: true, Running: true,
		Addrs: []netip.Prefix{netip.MustParsePrefix("172.31.0.1/24")}})
	table := append(v1r21HostRoutes(),
		v1r21Route("unicast", "::/0", "eth0", "2001:db8::1", 254),
		v1r21Route("unicast", "172.31.0.0/24", "br-npm", "172.31.0.1", 254),
		v1r21Route("local", "172.31.0.1/32", "br-npm", "172.31.0.1", 255))
	return qa12cHostOver{LiveHost: LiveHost{ProcRoot: root}, fib: v1r21Fib{&gHost{ifaces: ifaces, table: table}}}
}

// qa12cPlanFor is the guard context of a request that came through exposure exp1 with the given public bindings.
func qa12cPlanFor(kind string, public ...string) *core.GuardContext {
	var bindings []netip.AddrPort
	for _, p := range public {
		bindings = append(bindings, netip.MustParseAddrPort(p))
	}
	return &core.GuardContext{Complete: true, Via: &core.ProxyPath{ExposureID: "exp1", Kind: kind,
		ProxyAddr: netip.MustParseAddr("172.30.0.2"), ListenerAddr: netip.MustParseAddrPort("172.30.0.1:8080"), Public: bindings}}
}

type qa12cRun struct {
	plan       *core.GuardContext
	retired    []string
	noControl  bool // the controller does not run on this host
	before, st *fwstate.Loaded
}

func qa12cCheck(t *testing.T, root string, r qa12cRun) (core.Guard, []FlowResult) {
	t.Helper()
	env, files, _ := ckEnv()
	files.sysctl["net/ipv4/conf/br-npm/rp_filter"] = "0"
	if !r.noControl {
		ckRunning(env)
	}
	g, results, err := Checker{Host: qa12cNewHost(root)}.Check(context.Background(), env, *r.st,
		Input{Plan: r.plan, Retired: r.retired, Before: r.before, Eval: eval.Evaluate})
	if err != nil {
		t.Fatal(err)
	}
	return g, results
}

func qa12cState(t *testing.T, out string, v4 ...string) *fwstate.Loaded {
	t.Helper()
	st := v1r21State(t, qa12cTable(out, v4...), qa12cTable(out))
	return &st
}

func qa12cRelayResults(results []FlowResult) []FlowResult {
	var out []FlowResult
	for _, r := range results {
		if strings.Contains(r.Flow, "IPv6 relay") {
			out = append(out, r)
		}
	}
	return out
}

// qa12cRoot is a host whose [::]:443 listener is held by one docker-proxy that relays to the NPM container.
func qa12cRoot(t *testing.T) string {
	return qa12cProcRoot(t, []qa12cListener{{"[::]:443", 5001}},
		[]qa12cProc{{pid: 700, comm: "docker-proxy", inode: 5001, cmdline: qa12cProxy("::", qa12cTarget)}})
}

const qa12cAllowRelay = "-A OUTPUT -d 172.31.0.2/32 -p tcp -m tcp --dport 443 -j ACCEPT"

func TestA9_DefaultDenyOutputIsRefusedNamingTheRelay(t *testing.T) {
	for _, bind := range []string{"[::]:443", "[2001:db8::1]:443"} {
		t.Run(bind, func(t *testing.T) {
			root := qa12cProcRoot(t, []qa12cListener{{bind, 5001}},
				[]qa12cProc{{pid: 700, comm: "docker-proxy", inode: 5001, cmdline: qa12cProxy(strings.Trim(strings.Split(bind, "]")[0], "["), qa12cTarget)}})
			g, results := qa12cCheck(t, root, qa12cRun{plan: qa12cPlanFor("npm", bind), before: qa12cState(t, "ACCEPT"), st: qa12cState(t, "DROP")})
			if g.OK || g.ID != GuardID || !strings.Contains(g.Message, qa12cRelayLabel) {
				t.Fatalf("guard = %+v, want a refusal of %s naming %q", g, GuardID, qa12cRelayLabel)
			}
			if relay := qa12cRelayResults(results); len(relay) == 0 {
				t.Errorf("results %+v hold no relay flow", results)
			}
		})
	}
}

func TestA10_TheSameChangeAllowingTheRelayPasses(t *testing.T) {
	root := qa12cRoot(t)
	g, results := qa12cCheck(t, root, qa12cRun{plan: qa12cPlanFor("npm", "[::]:443"), before: qa12cState(t, "ACCEPT"), st: qa12cState(t, "DROP", qa12cAllowRelay)})
	if !g.OK {
		t.Errorf("guard = %+v, want OK: the change allows output to %s:443", g, qa12cTarget)
	}
	if len(qa12cRelayResults(results)) == 0 {
		t.Errorf("results %+v hold no relay flow, which the allow should have let pass", results)
	}
	// The allow must name the container: another address does not pass the relay.
	other := "-A OUTPUT -d 172.31.0.9/32 -p tcp -m tcp --dport 443 -j ACCEPT"
	if g, _ := qa12cCheck(t, root, qa12cRun{plan: qa12cPlanFor("npm", "[::]:443"), before: qa12cState(t, "ACCEPT"), st: qa12cState(t, "DROP", other)}); g.OK {
		t.Errorf("guard = %+v, want a refusal: output to another address and port is not the relay", g)
	}
}

func TestA11_NoRelayFlowForAnIPv6TargetOrNoListenerOrAnIPv4Binding(t *testing.T) {
	for _, tc := range []struct {
		name string
		root string
		bind string
	}{
		{"a target that is an IPv6 address", qa12cProcRoot(t, []qa12cListener{{"[::]:443", 5001}},
			[]qa12cProc{{pid: 700, comm: "docker-proxy", inode: 5001, cmdline: qa12cProxy("::", "fd00:1::2")}}), "[::]:443"},
		{"no listener on the binding", qa12cProcRoot(t, nil, nil), "[::]:443"},
		{"a listener on another port", qa12cProcRoot(t, []qa12cListener{{"[::]:8443", 5001}},
			[]qa12cProc{{pid: 700, comm: "docker-proxy", inode: 5001, cmdline: qa12cProxy("::", qa12cTarget)}}), "[::]:443"},
		{"an IPv4 binding", qa12cRoot(t), "0.0.0.0:443"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g, results := qa12cCheck(t, tc.root, qa12cRun{plan: qa12cPlanFor("npm", tc.bind), before: qa12cState(t, "ACCEPT"), st: qa12cState(t, "DROP")})
			if relay := qa12cRelayResults(results); len(relay) != 0 {
				t.Errorf("relay flows %+v, want none", relay)
			}
			if strings.Contains(g.Message, "relay") {
				t.Errorf("guard message %q names a relay", g.Message)
			}
		})
	}
	// The same host with a relay that exists is the control: the flow is there for an IPv6 binding.
	_, results := qa12cCheck(t, qa12cRoot(t), qa12cRun{plan: qa12cPlanFor("npm", "[::]:443"), before: qa12cState(t, "ACCEPT"), st: qa12cState(t, "DROP")})
	if len(qa12cRelayResults(results)) == 0 {
		t.Error("the control host gave no relay flow")
	}
}

func TestA12_AnUnreadableOrForeignRelayRefusesTheChange(t *testing.T) {
	proxy := func(cmdline []string) string {
		return qa12cProcRoot(t, []qa12cListener{{"[::]:443", 5001}}, []qa12cProc{{pid: 700, comm: "docker-proxy", inode: 5001, cmdline: cmdline}})
	}
	run := func(root string) core.Guard {
		// The before-state allows everything and the change blocks nothing the relay needs: only an unknown flow can fail.
		g, _ := qa12cCheck(t, root, qa12cRun{plan: qa12cPlanFor("npm", "[::]:443"), before: qa12cState(t, "ACCEPT"), st: qa12cState(t, "ACCEPT")})
		return g
	}
	for _, tc := range []struct {
		name  string
		root  string
		wants []string // any of them names the cause
	}{
		{"a command line that cannot be read", proxy(nil), []string{"700", "docker-proxy", "command line", "cmdline"}},
		{"a command line without the container address", proxy([]string{"/usr/bin/docker-proxy", "-proto", "tcp", "-host-port", "443"}),
			[]string{"container", "docker-proxy", "700"}},
		{"a command line without the container port", proxy([]string{"/usr/bin/docker-proxy", "-container-ip", qa12cTarget}),
			[]string{"container", "docker-proxy", "700"}},
		{"a listener held by another process only", qa12cProcRoot(t, []qa12cListener{{"[::]:443", 5001}},
			[]qa12cProc{{pid: 800, comm: "nginx", inode: 5001, cmdline: []string{"nginx"}}}), []string{"nginx"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := run(tc.root)
			if g.OK || g.ID != GuardID {
				t.Fatalf("guard = %+v, want a refusal", g)
			}
			named := false
			for _, w := range tc.wants {
				named = named || strings.Contains(g.Message, w)
			}
			if !named {
				t.Errorf("guard message %q names none of %q", g.Message, tc.wants)
			}
			if !strings.Contains(g.Message, "exposure exp1") {
				t.Errorf("guard message %q does not name the exposure", g.Message)
			}
		})
	}

	t.Run("a docker-proxy that shares the listener with another process", func(t *testing.T) {
		root := qa12cProcRoot(t, []qa12cListener{{"[::]:443", 5001}}, []qa12cProc{
			{pid: 50, comm: "dockerd", inode: 5001, cmdline: []string{"/usr/bin/dockerd", "-H", "fd://"}},
			{pid: 700, comm: "docker-proxy", inode: 5001, cmdline: qa12cProxy("::", qa12cTarget)},
		})
		g, _ := qa12cCheck(t, root, qa12cRun{plan: qa12cPlanFor("npm", "[::]:443"), before: qa12cState(t, "ACCEPT"), st: qa12cState(t, "DROP")})
		if g.OK || !strings.Contains(g.Message, qa12cRelayLabel) {
			t.Errorf("guard = %+v, want a refusal naming %q: the relay is read through its docker-proxy holder", g, qa12cRelayLabel)
		}
		g, _ = qa12cCheck(t, root, qa12cRun{plan: qa12cPlanFor("npm", "[::]:443"), before: qa12cState(t, "ACCEPT"), st: qa12cState(t, "DROP", qa12cAllowRelay)})
		if !g.OK {
			t.Errorf("guard = %+v, want OK once the change allows the relay", g)
		}
	})
}

func TestA13_NoRelayFlowOutsideAnNPMExposureOfTheControllersHost(t *testing.T) {
	root := qa12cRoot(t)
	deny, accept := qa12cState(t, "DROP"), qa12cState(t, "ACCEPT")
	// The control: the same host and change with an NPM exposure gives the flow.
	_, results := qa12cCheck(t, root, qa12cRun{plan: qa12cPlanFor("npm", "[::]:443"), before: accept, st: deny})
	if len(qa12cRelayResults(results)) == 0 {
		t.Fatal("the control gave no relay flow")
	}
	direct := &core.GuardContext{Complete: true, Client: &core.Peer{Addr: netip.MustParseAddr("203.0.113.7"), Port: 51000},
		PanelListen: []netip.AddrPort{netip.MustParseAddrPort("192.0.2.1:443")}}
	for _, tc := range []struct {
		name string
		run  qa12cRun
	}{
		{"a request that came directly", qa12cRun{plan: direct, before: accept, st: deny}},
		{"a request through a tunnel exposure", qa12cRun{plan: qa12cPlanFor("tunnel", "[::]:443"), before: accept, st: deny}},
		{"a retired exposure", qa12cRun{plan: qa12cPlanFor("npm", "[::]:443"), retired: []string{"exp1"}, before: accept, st: deny}},
		{"a host that is not the controller's", qa12cRun{plan: qa12cPlanFor("npm", "[::]:443"), noControl: true, before: accept, st: deny}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, results := qa12cCheck(t, root, tc.run)
			if relay := qa12cRelayResults(results); len(relay) != 0 {
				t.Errorf("relay flows %+v, want none", relay)
			}
		})
	}
}

func TestA22_ARelayTheRulesAlreadyBlockedDoesNotRefuseAnUnrelatedChange(t *testing.T) {
	root := qa12cRoot(t)
	plan := qa12cPlanFor("npm", "[::]:443")
	blocked := qa12cState(t, "DROP")
	unrelated := qa12cState(t, "DROP", "-A INPUT -p tcp -m tcp --dport 9999 -j ACCEPT")
	g, _ := qa12cCheck(t, root, qa12cRun{plan: plan, before: blocked, st: unrelated})
	if !g.OK {
		t.Errorf("guard = %+v, want OK: the relay was blocked before and the change does not touch it", g)
	}
	if !strings.Contains(g.Message, "already blocked") {
		t.Errorf("guard message %q, want the already-blocked leg counted with the sources already blocked", g.Message)
	}
	// With no before-state the relay must pass, so the same state is refused.
	if g, _ := qa12cCheck(t, root, qa12cRun{plan: plan, st: unrelated}); g.OK {
		t.Errorf("guard = %+v, want a refusal without a before-state", g)
	}
}
