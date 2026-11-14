package guard

import (
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/platform/netstat"
)

const (
	w12cRelayLabel = "exposure exp1 IPv6 relay to 172.17.0.2:443"
	w12cCutRelay   = "-A OUTPUT -d 172.17.0.2/32 -p tcp -m tcp --dport 443 -j DROP"
	w12cKeepRelay  = "-A OUTPUT -d 172.17.0.2/32 -p tcp -m tcp --dport 443 -j ACCEPT"
	w12cOtherPort  = "-A INPUT -p tcp -m tcp --dport 2222 -j ACCEPT"
)

func w12cProxyArgs(target, port string) []string {
	return []string{"/usr/bin/docker-proxy", "-proto", "tcp", "-host-ip", "::", "-host-port", "443", "-container-ip", target, "-container-port", port, "-use-listen-fd"}
}

// w12cHost is the w12b host with docker0 (172.17.0.1/16) and NPM's [::]:443
// listener held by the given processes.
func w12cHost(holders []netstat.Process, cmdlines map[int][]string, errs map[int]error) Host {
	ifaces := append(ckIfaces(), netstat.Interface{Index: 9, Name: "docker0", Up: true, Running: true, Addrs: []netip.Prefix{netip.MustParsePrefix("172.17.0.1/16")}})
	routes := append(w12bRoutes(), v1r21Route("unicast", "172.17.0.0/16", "docker0", "172.17.0.1", 254))
	sock := netstat.Socket{Proto: netstat.ProtoTCP, Family: netstat.IPv6, State: netstat.StateListen, Local: netip.MustParseAddrPort("[::]:443"), Processes: holders}
	return v1r21Fib{&gHost{ifaces: ifaces, table: routes, sockets: []netstat.Socket{sock}, cmdlines: cmdlines, cmdlineErrs: errs}}
}

func w12cRelayFlows(in Input, f facts) []flow {
	var out []flow
	for _, fl := range buildFlows(in, f) {
		if strings.Contains(fl.label, "IPv6 relay") {
			out = append(out, fl)
		}
	}
	return out
}

func TestW12cRelayGuard(t *testing.T) {
	proxy := []netstat.Process{{PID: 900, Comm: "docker-proxy"}}
	for _, tc := range []struct {
		name          string
		holders       []netstat.Process
		cmdlines      map[int][]string
		errs          map[int]error
		before, after []string // IPv4 filter rules
		noBefore      bool
		want          string // "" when the change must pass
	}{
		{"a new drop of the relay is refused", proxy, map[int][]string{900: w12cProxyArgs("172.17.0.2", "443")}, nil,
			nil, []string{w12cCutRelay}, false, w12cRelayLabel},
		{"an allow of the relay ahead of the drop passes", proxy, map[int][]string{900: w12cProxyArgs("172.17.0.2", "443")}, nil,
			nil, []string{w12cKeepRelay, w12cCutRelay}, false, ""},
		{"a relay the rules already cut does not refuse an unrelated change", proxy, map[int][]string{900: w12cProxyArgs("172.17.0.2", "443")}, nil,
			[]string{w12cCutRelay}, []string{w12cCutRelay, w12cOtherPort}, false, ""},
		{"with no before-state the relay must pass", proxy, map[int][]string{900: w12cProxyArgs("172.17.0.2", "443")}, nil,
			nil, []string{w12cCutRelay}, true, w12cRelayLabel},
		{"an unreadable command line is unknown", proxy, nil, map[int]error{900: errors.New("no such process")},
			nil, []string{w12cOtherPort}, false, "the command line of docker-proxy (pid 900) cannot be read: no such process"},
		{"a command line with no target is unknown", proxy, map[int][]string{900: {"/usr/bin/docker-proxy", "-proto", "tcp"}}, nil,
			nil, []string{w12cOtherPort}, false, "docker-proxy (pid 900) names no container address and port"},
		{"a listener held by another process only is unknown", []netstat.Process{{PID: 800, Comm: "dockerd"}}, nil, nil,
			nil, []string{w12cOtherPort}, false, "the listener on [::]:443 is held by dockerd, not docker-proxy"},
		{"a docker-proxy beside dockerd is read", []netstat.Process{{PID: 800, Comm: "dockerd"}, {PID: 900, Comm: "docker-proxy"}}, map[int][]string{900: w12cProxyArgs("172.17.0.2", "443")}, nil,
			nil, []string{w12cCutRelay}, false, w12cRelayLabel},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var before *fwstate.Loaded
			if !tc.noBefore {
				b := v1r21State(t, w11b09Nat+v1r21Table(tc.before...), v1r21Table())
				before = &b
			}
			st := v1r21State(t, w11b09Nat+v1r21Table(tc.after...), v1r21Table())
			g := w12bCheck(t, w12cHost(tc.holders, tc.cmdlines, tc.errs), before, st, "[::]:443", "198.51.100.7", "")
			switch {
			case tc.want == "" && !g.OK:
				t.Errorf("guard = %+v, want OK", g)
			case tc.want != "" && (g.OK || !strings.Contains(g.Message, tc.want)):
				t.Errorf("guard = %+v, want a refusal containing %q", g, tc.want)
			}
		})
	}
}

func TestW12cRelayFlowsOnlyWhereTheyApply(t *testing.T) {
	sock := netstat.Socket{Proto: netstat.ProtoTCP, Family: netstat.IPv6, State: netstat.StateListen, Local: netip.MustParseAddrPort("[::]:443"),
		Processes: []netstat.Process{{PID: 900, Comm: "docker-proxy"}}}
	relay := func(args []string) facts {
		return facts{ifaces: gIfaces(), controller: 4242, sockets: []netstat.Socket{sock}, cmdlines: map[int]cmdline{900: {args: args}}}
	}
	v4bind := w11b09Plan("0.0.0.0:443")
	tunnel := &core.GuardContext{Via: &core.ProxyPath{ExposureID: "exp2", Kind: "tunnel", ProxyAddr: netip.MustParseAddr("172.30.0.3"), ListenerAddr: netip.MustParseAddrPort("172.30.0.1:8080")}, Complete: true}
	for _, tc := range []struct {
		name string
		in   Input
		f    facts
		want int
	}{
		{"an IPv4 target on [::]", Input{Plan: w11b09Plan("[::]:443")}, relay(w12cProxyArgs("172.17.0.2", "443")), 1},
		{"an IPv6 target", Input{Plan: w11b09Plan("[::]:443")}, relay(w12cProxyArgs("fd00::2", "443")), 0},
		{"no listener on the binding", Input{Plan: w11b09Plan("[::]:443")}, facts{ifaces: gIfaces(), controller: 4242}, 0},
		{"an IPv4 binding", Input{Plan: v4bind}, relay(w12cProxyArgs("172.17.0.2", "443")), 0},
		{"no exposure", Input{Plan: &core.GuardContext{Complete: true}}, relay(w12cProxyArgs("172.17.0.2", "443")), 0},
		{"a tunnel exposure", Input{Plan: tunnel}, relay(w12cProxyArgs("172.17.0.2", "443")), 0},
		{"a retired exposure", Input{Plan: w11b09Plan("[::]:443"), Retired: []string{"exp1"}}, relay(w12cProxyArgs("172.17.0.2", "443")), 0},
		{"another host than the controller's", Input{Plan: w11b09Plan("[::]:443")},
			facts{ifaces: gIfaces(), sockets: []netstat.Socket{sock}, cmdlines: map[int]cmdline{900: {args: w12cProxyArgs("172.17.0.2", "443")}}}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := w12cRelayFlows(tc.in, tc.f)
			if len(got) != tc.want {
				t.Fatalf("relay flows %+v, want %d", got, tc.want)
			}
			if tc.want == 1 && (got[0].label != w12cRelayLabel || got[0].pkt.Path != "output" || got[0].pkt.Src.IsValid() && !got[0].pkt.Src.IsUnspecified() || !got[0].compared) {
				t.Errorf("relay flow %+v, want %q, output, source left to routing, compared", got[0], w12cRelayLabel)
			}
		})
	}
}

func TestW12cLiveHostCmdline(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "900"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "900", "cmdline"), []byte("/usr/bin/docker-proxy\x00-container-ip\x00172.17.0.2\x00"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := LiveHost{ProcRoot: root}.Cmdline(900)
	if err != nil || !slices.Equal(got, []string{"/usr/bin/docker-proxy", "-container-ip", "172.17.0.2"}) {
		t.Errorf("Cmdline = %q, %v", got, err)
	}
	if _, err := (LiveHost{ProcRoot: root}).Cmdline(901); err == nil {
		t.Error("Cmdline of a missing process succeeded")
	}
}
