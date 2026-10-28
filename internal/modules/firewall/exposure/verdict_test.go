package exposure

import (
	"errors"
	"fmt"
	"maps"
	"net/netip"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/platform/netfilter"
)

const exHead = `*filter
:INPUT DROP [0:0]
:FORWARD DROP [0:0]
:OUTPUT ACCEPT [0:0]
:ufw-user-input - [0:0]
-A INPUT -j ufw-user-input
`

// exRules parses a filter table whose INPUT chain jumps first to
// ufw-user-input, with the given rule lines added.
func exRules(t *testing.T, lines string) *netfilter.Ruleset {
	t.Helper()
	rs, err := netfilter.Parse(strings.NewReader(exHead + lines + "COMMIT\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return rs
}

func exPrivate() []netip.Prefix {
	var out []netip.Prefix
	for _, s := range []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "100.64.0.0/10", "fc00::/7", "fe80::/10"} {
		out = append(out, netip.MustParsePrefix(s))
	}
	return out
}

// exIfaces is a host with a public interface, the tailnet, a Docker bridge,
// loopback and an interface that is down.
func exIfaces() []Iface {
	a := netip.MustParseAddr
	return []Iface{
		{Name: "lo", Up: true, Loopback: true, Addrs: []netip.Addr{a("127.0.0.1")}},
		{Name: "eth0", Up: true, Addrs: []netip.Addr{a("192.0.2.10")}},
		{Name: "tailscale0", Up: true, Tailnet: true, Addrs: []netip.Addr{a("100.64.0.5")}},
		{Name: "docker0", Up: true, Bridge: true, Addrs: []netip.Addr{a("172.17.0.1")}},
		{Name: "eth9", Up: false, Addrs: []netip.Addr{a("192.0.2.99")}},
	}
}

func exKey(v Verdict) string {
	if v.Range != "" {
		return v.Class + " " + v.Range
	}
	return v.Class
}

// exIndex keys the verdicts by class and range and fails on a duplicate.
func exIndex(t *testing.T, vs []Verdict) map[string]Verdict {
	t.Helper()
	out := map[string]Verdict{}
	for _, v := range vs {
		if _, dup := out[exKey(v)]; dup {
			t.Fatalf("two verdicts for %q: %+v", exKey(v), vs)
		}
		out[exKey(v)] = v
	}
	return out
}

var exV4Keys = []string{
	"internet", "private 10.0.0.0/8", "private 172.16.0.0/12", "private 192.168.0.0/16",
	"private 100.64.0.0/10", "tailnet", "localhost",
}

// exWant gives every IPv4 class the verdict base, then the pairs of over
// (key, verdict) replace theirs.
func exWant(base string, over ...string) map[string]string {
	m := map[string]string{}
	for _, k := range exV4Keys {
		m[k] = base
	}
	for pair := range slices.Chunk(over, 2) {
		m[pair[0]] = pair[1]
	}
	return m
}

func exWithout(m map[string]string, keys ...string) map[string]string {
	for _, k := range keys {
		delete(m, k)
	}
	return m
}

func exOnlyIfaces(names ...string) func(in *Input) {
	return func(in *Input) {
		in.Ifaces = slices.DeleteFunc(in.Ifaces, func(i Iface) bool { return !slices.Contains(names, i.Name) })
	}
}

const exOpen = "-A ufw-user-input -p tcp -m tcp --dport 22 -j ACCEPT\n"

func TestExEvaluate(t *testing.T) {
	tcp22 := Target{Proto: "tcp", Addr: netip.IPv4Unspecified(), Port: 22}
	tests := []struct {
		name   string
		lines  string
		edit   func(in *Input)
		target Target
		want   map[string]string
		check  func(t *testing.T, got map[string]Verdict)
	}{
		{
			name: "open to everyone", lines: exOpen, want: exWant("open"),
			check: func(t *testing.T, got map[string]Verdict) {
				if v := got["internet"]; v.DecidedBy != "filter/ufw-user-input#1" || v.Note != "" || len(v.Allowed) != 0 {
					t.Errorf("internet = %+v", v)
				}
				if v := got["tailnet"]; v.Note != "host firewall; tailnet policy not evaluated" {
					t.Errorf("tailnet note = %q", v.Note)
				}
				if v := got["localhost"]; v.DecidedBy != "filter/OUTPUT policy ACCEPT" {
					t.Errorf("localhost decidedBy = %q", v.DecidedBy)
				}
			},
		},
		{
			name: "closed by the default policy", want: exWant("filtered", "localhost", "open"),
			check: func(t *testing.T, got map[string]Verdict) {
				if v := got["internet"]; v.DecidedBy != "filter/INPUT policy DROP" {
					t.Errorf("internet decidedBy = %q", v.DecidedBy)
				}
			},
		},
		{
			name: "rejected", lines: "-A ufw-user-input -p tcp -m tcp --dport 22 -j REJECT --reject-with tcp-reset\n",
			want: exWant("closed", "localhost", "open"),
			check: func(t *testing.T, got map[string]Verdict) {
				if v := got["internet"]; v.DecidedBy != "filter/ufw-user-input#1" {
					t.Errorf("internet decidedBy = %q", v.DecidedBy)
				}
			},
		},
		{
			name: "allowed from one network", lines: "-A ufw-user-input -s 203.0.113.0/24 -p tcp -m tcp --dport 22 -j ACCEPT\n",
			want: exWant("filtered", "internet", "partial", "localhost", "open"),
			check: func(t *testing.T, got map[string]Verdict) {
				v := got["internet"]
				want := []Allowed{{Sources: []string{"203.0.113.0/24"}, DecidedBy: "filter/ufw-user-input#1"}}
				if !slices.EqualFunc(v.Allowed, want, exAllowedEqual) || v.DecidedBy != "" {
					t.Errorf("internet = %+v, want allowed %+v and no decidedBy", v, want)
				}
			},
		},
		{
			name: "allowed from a private range", lines: "-A ufw-user-input -s 192.168.0.0/16 -p tcp -m tcp --dport 22 -j ACCEPT\n",
			want: exWant("filtered", "private 192.168.0.0/16", "open", "localhost", "open"),
		},
		{
			name: "allowed from part of a private range", lines: "-A ufw-user-input -s 192.168.1.0/24 -p tcp -m tcp --dport 22 -j ACCEPT\n",
			want: exWant("filtered", "private 192.168.0.0/16", "partial", "localhost", "open"),
			check: func(t *testing.T, got map[string]Verdict) {
				want := []Allowed{{Sources: []string{"192.168.1.0/24"}, DecidedBy: "filter/ufw-user-input#1"}}
				if v := got["private 192.168.0.0/16"]; !slices.EqualFunc(v.Allowed, want, exAllowedEqual) {
					t.Errorf("allowed = %+v, want %+v", v.Allowed, want)
				}
			},
		},
		{
			name: "allowed on the tailnet interface only", lines: "-A ufw-user-input -i tailscale0 -p tcp -m tcp --dport 22 -j ACCEPT\n",
			want: exWant("filtered", "tailnet", "open", "localhost", "open"),
		},
		{
			name: "tailnet with shields-up", lines: exOpen, edit: func(in *Input) { in.ShieldsUp = true },
			want: exWant("open", "tailnet", "closed"),
			check: func(t *testing.T, got map[string]Verdict) {
				if v := got["tailnet"]; v.DecidedBy != "Tailscale shields-up" || v.Note != "" {
					t.Errorf("tailnet = %+v", v)
				}
			},
		},
		{
			name: "allowed on one of two interfaces", lines: "-A ufw-user-input -i eth1 -p tcp -m tcp --dport 22 -j ACCEPT\n",
			edit: func(in *Input) {
				in.Ifaces = append(in.Ifaces, Iface{Name: "eth1", Up: true, Addrs: []netip.Addr{netip.MustParseAddr("198.51.100.7")}})
			},
			want: exWant("partial", "tailnet", "filtered", "localhost", "open"),
			check: func(t *testing.T, got map[string]Verdict) {
				for _, k := range []string{"internet", "private 10.0.0.0/8"} {
					v := got[k]
					if len(v.Allowed) != 1 || !slices.Equal(v.Allowed[0].Ifaces, []string{"eth1"}) || v.Allowed[0].DecidedBy != "filter/ufw-user-input#1" {
						t.Errorf("%s allowed = %+v", k, v.Allowed)
					}
				}
				if v := got["private 10.0.0.0/8"]; len(v.Allowed) != 1 || !slices.Equal(v.Allowed[0].Sources, []string{"10.0.0.0/8"}) {
					t.Errorf("private allowed = %+v", v.Allowed)
				}
			},
		},
		{
			name: "a down interface, a bridge and loopback are not probed",
			lines: "-A ufw-user-input -i eth9 -p tcp -m tcp --dport 22 -j ACCEPT\n" +
				"-A ufw-user-input -i docker0 -p tcp -m tcp --dport 22 -j ACCEPT\n" +
				"-A ufw-user-input -i lo -p tcp -m tcp --dport 22 -j ACCEPT\n",
			want: exWant("filtered", "localhost", "open"),
		},
		{
			name: "allowed from one source port", lines: "-A ufw-user-input -p tcp -m tcp --sport 53 --dport 22 -j ACCEPT\n",
			want: exWant("partial", "localhost", "open"),
			check: func(t *testing.T, got map[string]Verdict) {
				for _, k := range exV4Keys[:6] {
					v := got[k]
					if len(v.Allowed) != 1 || !slices.Equal(v.Allowed[0].SrcPorts, []string{"53"}) || v.Allowed[0].Ifaces != nil ||
						v.Allowed[0].DecidedBy != "filter/ufw-user-input#1" {
						t.Errorf("%s allowed = %+v", k, v.Allowed)
					}
				}
			},
		},
		{
			name: "allowed from a source port range", lines: "-A ufw-user-input -p tcp -m tcp --sport 1024:65535 --dport 22 -j ACCEPT\n",
			want: exWant("partial", "localhost", "open"),
			check: func(t *testing.T, got map[string]Verdict) {
				if v := got["internet"]; len(v.Allowed) != 1 || !slices.Equal(v.Allowed[0].SrcPorts, []string{"1024-65535"}) {
					t.Errorf("internet allowed = %+v", v.Allowed)
				}
			},
		},
		{
			name: "a rule the walk cannot decide", lines: "-A ufw-user-input -p tcp -m tcp --dport 22 -m limit --limit 3/min -j ACCEPT\n",
			want: exWant("unknown", "localhost", "open"),
			check: func(t *testing.T, got map[string]Verdict) {
				for _, k := range []string{"internet", "private 10.0.0.0/8", "tailnet"} {
					if v := got[k]; !strings.Contains(v.Note, "match limit") || v.DecidedBy != "" || len(v.Allowed) != 0 {
						t.Errorf("%s = %+v", k, v)
					}
				}
			},
		},
		{
			name:  "drop for some sources and reject for others is filtered",
			lines: "-A ufw-user-input -s 198.51.100.0/24 -p tcp -m tcp --dport 22 -j REJECT --reject-with tcp-reset\n",
			want:  exWant("filtered", "localhost", "open"),
			check: func(t *testing.T, got map[string]Verdict) {
				if v := got["internet"]; v.DecidedBy != "filter/INPUT policy DROP; filter/ufw-user-input#1" {
					t.Errorf("internet decidedBy = %q", v.DecidedBy)
				}
			},
		},
		{
			name: "accept for some sources and reject for the rest is partial",
			lines: "-A ufw-user-input -s 203.0.113.0/24 -p tcp -m tcp --dport 22 -j ACCEPT\n" +
				"-A ufw-user-input -p tcp -m tcp --dport 22 -j REJECT --reject-with tcp-reset\n",
			want: exWant("closed", "internet", "partial", "localhost", "open"),
		},
		{
			name:  "open through two rules lists both",
			lines: "-A ufw-user-input -s 203.0.113.0/24 -p tcp -m tcp --dport 22 -j ACCEPT\n" + exOpen,
			want:  exWant("open"),
			check: func(t *testing.T, got map[string]Verdict) {
				if v := got["internet"]; v.DecidedBy != "filter/ufw-user-input#1; filter/ufw-user-input#2" {
					t.Errorf("internet decidedBy = %q", v.DecidedBy)
				}
				if v := got["private 10.0.0.0/8"]; v.DecidedBy != "filter/ufw-user-input#2" {
					t.Errorf("private decidedBy = %q", v.DecidedBy)
				}
			},
		},
		{
			name: "two accepting rules give two allowed sets",
			lines: "-A ufw-user-input -s 203.0.113.0/24 -p tcp -m tcp --dport 22 -j ACCEPT\n" +
				"-A ufw-user-input -s 198.51.100.0/24 -p tcp -m tcp --dport 22 -j ACCEPT\n",
			want: exWant("filtered", "internet", "partial", "localhost", "open"),
			check: func(t *testing.T, got map[string]Verdict) {
				want := []Allowed{
					{Sources: []string{"203.0.113.0/24"}, DecidedBy: "filter/ufw-user-input#1"},
					{Sources: []string{"198.51.100.0/24"}, DecidedBy: "filter/ufw-user-input#2"},
				}
				if v := got["internet"]; !slices.EqualFunc(v.Allowed, want, exAllowedEqual) {
					t.Errorf("allowed = %+v, want %+v", v.Allowed, want)
				}
			},
		},
		{
			name: "link-local and Tailscale-range sources outside privateSources are internet",
			lines: "-A ufw-user-input -s 169.254.0.0/16 -p tcp -m tcp --dport 22 -j ACCEPT\n" +
				"-A ufw-user-input -s 100.64.0.0/10 -p tcp -m tcp --dport 22 -j ACCEPT\n",
			edit: func(in *Input) {
				in.Private = slices.DeleteFunc(in.Private, func(p netip.Prefix) bool { return p == netip.MustParsePrefix("100.64.0.0/10") })
			},
			want: exWithout(exWant("filtered", "internet", "partial", "tailnet", "open", "localhost", "open"), "private 100.64.0.0/10"),
			check: func(t *testing.T, got map[string]Verdict) {
				want := []Allowed{
					{Sources: []string{"169.254.0.0/16"}, DecidedBy: "filter/ufw-user-input#1"},
					{Sources: []string{"100.64.0.0/10"}, DecidedBy: "filter/ufw-user-input#2"},
				}
				if v := got["internet"]; !slices.EqualFunc(v.Allowed, want, exAllowedEqual) {
					t.Errorf("internet allowed = %+v, want %+v", v.Allowed, want)
				}
			},
		},
		{
			name: "a target bound to loopback", lines: exOpen,
			target: Target{Proto: "tcp", Addr: netip.MustParseAddr("127.0.0.1"), Port: 22},
			want:   exWant("closed", "localhost", "open"),
			check: func(t *testing.T, got map[string]Verdict) {
				for _, k := range []string{"internet", "private 10.0.0.0/8", "tailnet"} {
					if v := got[k]; v.DecidedBy != "bound to loopback" || v.Note != "" {
						t.Errorf("%s = %+v", k, v)
					}
				}
			},
		},
		{
			name: "a concrete address is the destination as given", lines: "-A ufw-user-input -d 192.0.2.77/32 -p tcp -m tcp --dport 22 -j ACCEPT\n",
			target: Target{Proto: "tcp", Addr: netip.MustParseAddr("192.0.2.77"), Port: 22},
			want:   exWant("open"),
		},
		{
			name: "a wildcard uses each probe interface's own address", lines: "-A ufw-user-input -d 192.0.2.10/32 -p tcp -m tcp --dport 22 -j ACCEPT\n",
			want: exWant("open", "tailnet", "filtered"),
		},
		{
			name: "a wildcard probes every address of the interface", lines: "-A ufw-user-input -d 192.0.2.11/32 -p tcp -m tcp --dport 22 -j ACCEPT\n",
			edit: func(in *Input) {
				for i := range in.Ifaces {
					if in.Ifaces[i].Name == "eth0" {
						in.Ifaces[i].Addrs = append(in.Ifaces[i].Addrs, netip.MustParseAddr("192.0.2.11"))
					}
				}
			},
			want: exWant("partial", "tailnet", "filtered", "localhost", "open"),
			check: func(t *testing.T, got map[string]Verdict) {
				if v := got["internet"]; len(v.Allowed) != 1 || v.Allowed[0].Ifaces != nil || v.Allowed[0].SrcPorts != nil ||
					v.Allowed[0].DecidedBy != "filter/ufw-user-input#1" {
					t.Errorf("internet allowed = %+v", v.Allowed)
				}
			},
		},
		{
			name: "a local connection dropped on OUTPUT", lines: exOpen + "-A OUTPUT -o lo -p tcp -m tcp --dport 22 -j DROP\n",
			want: exWant("open", "localhost", "filtered"),
			check: func(t *testing.T, got map[string]Verdict) {
				if v := got["localhost"]; v.DecidedBy != "filter/OUTPUT#1" {
					t.Errorf("localhost decidedBy = %q", v.DecidedBy)
				}
			},
		},
		{
			name: "a UDP target", lines: "-A ufw-user-input -p udp -m udp --dport 53 -j ACCEPT\n",
			target: Target{Proto: "udp", Addr: netip.IPv4Unspecified(), Port: 53},
			want:   exWant("open"),
		},
		{
			name: "tailscale0 is down", lines: exOpen,
			edit: func(in *Input) {
				for i := range in.Ifaces {
					if in.Ifaces[i].Tailnet {
						in.Ifaces[i].Up = false
					}
				}
			},
			want: exWithout(exWant("open"), "tailnet"),
		},
		{
			name: "there is no tailscale0", lines: exOpen, edit: func(in *Input) {
				in.Ifaces = slices.DeleteFunc(in.Ifaces, func(i Iface) bool { return i.Tailnet })
			},
			want: exWithout(exWant("open"), "tailnet"),
		},
		{
			name: "no interface to probe", lines: exOpen, edit: exOnlyIfaces("lo"),
			want: exWithout(exWant("unknown", "localhost", "open"), "tailnet"),
			check: func(t *testing.T, got map[string]Verdict) {
				if v := got["internet"]; v.Note != "no interface to probe" {
					t.Errorf("internet note = %q", v.Note)
				}
			},
		},
		{
			name: "an interface with no address of the family is not probed", lines: exOpen,
			edit: func(in *Input) {
				for i := range in.Ifaces {
					if in.Ifaces[i].Name == "eth0" {
						in.Ifaces[i].Addrs = []netip.Addr{netip.MustParseAddr("2001:db8::10")}
					}
				}
			},
			want: exWant("unknown", "tailnet", "open", "localhost", "open"),
		},
		{
			name: "too many partitions", lines: exOpen, edit: func(in *Input) { in.MaxProbes = 2 },
			want: exWant("open", "internet", "unknown"),
			check: func(t *testing.T, got map[string]Verdict) {
				if v := got["internet"]; v.Note != "too many partitions" {
					t.Errorf("internet note = %q", v.Note)
				}
			},
		},
		{
			name: "no ruleset", lines: exOpen, edit: func(in *Input) { in.Rules = nil },
			want: exWant("unknown"),
			check: func(t *testing.T, got map[string]Verdict) {
				if v := got["internet"]; !strings.Contains(v.Note, "no ruleset") {
					t.Errorf("internet note = %q", v.Note)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := Input{Rules: exRules(t, tt.lines), Family: fwstate.V4, Ifaces: exIfaces(), Private: exPrivate()}
			if tt.edit != nil {
				tt.edit(&in)
			}
			target := tt.target
			if target == (Target{}) {
				target = tcp22
			}
			got := exIndex(t, Evaluate(in, target))
			verdicts := map[string]string{}
			for k, v := range got {
				verdicts[k] = v.Verdict
			}
			if !maps.Equal(verdicts, tt.want) {
				t.Fatalf("verdicts = %v, want %v", verdicts, tt.want)
			}
			if tt.check != nil {
				tt.check(t, got)
			}
		})
	}
}

func exAllowedEqual(a, b Allowed) bool {
	return slices.Equal(a.Sources, b.Sources) && slices.Equal(a.Ifaces, b.Ifaces) &&
		slices.Equal(a.SrcPorts, b.SrcPorts) && a.DecidedBy == b.DecidedBy
}

func TestExDescribe(t *testing.T) {
	in := Input{
		Rules:    exRules(t, "-A ufw-user-input -s 203.0.113.0/24 -p tcp -m tcp --dport 22 -j ACCEPT\n"),
		Family:   fwstate.V4,
		Ifaces:   exIfaces(),
		Private:  exPrivate(),
		Describe: func(ref string) string { return "rule " + ref },
	}
	got := exIndex(t, Evaluate(in, Target{Proto: "tcp", Addr: netip.IPv4Unspecified(), Port: 22}))
	if v := got["internet"]; len(v.Allowed) != 1 || v.Allowed[0].DecidedBy != "rule filter/ufw-user-input#1" {
		t.Errorf("internet allowed = %+v", v.Allowed)
	}
	if v := got["private 10.0.0.0/8"]; v.DecidedBy != "rule filter/INPUT policy DROP" {
		t.Errorf("private decidedBy = %q", v.DecidedBy)
	}
	if v := got["localhost"]; v.DecidedBy != "rule filter/OUTPUT policy ACCEPT" {
		t.Errorf("localhost decidedBy = %q", v.DecidedBy)
	}
}

func TestExDefaultMaxProbes(t *testing.T) {
	if DefaultMaxProbes != 4096 {
		t.Fatalf("DefaultMaxProbes = %d", DefaultMaxProbes)
	}
	var lines strings.Builder
	for i := range 2100 {
		fmt.Fprintf(&lines, "-A ufw-user-input -s 198.18.%d.%d/32 -p tcp -m tcp --dport 22 -j ACCEPT\n", 2*i/256, 2*i%256)
	}
	in := Input{Rules: exRules(t, lines.String()), Family: fwstate.V4, Ifaces: exIfaces(), Private: exPrivate()}
	got := exIndex(t, Evaluate(in, Target{Proto: "tcp", Addr: netip.IPv4Unspecified(), Port: 22}))
	if v := got["internet"]; v.Verdict != "unknown" || v.Note != "too many partitions" {
		t.Errorf("internet = %+v, want unknown with too many partitions", v)
	}
	if v := got["localhost"]; v.Verdict != "open" {
		t.Errorf("localhost = %+v", v)
	}
}

// exRoute sends the published network's addresses out of docker0 and
// everything else to this host.
func exRoute(p fwstate.Packet) (string, string, error) {
	switch {
	case netip.MustParsePrefix("172.18.0.0/16").Contains(p.Dst) && p.Path == "output":
		return "output", "docker0", nil
	case netip.MustParsePrefix("172.18.0.0/16").Contains(p.Dst):
		return "forward", "docker0", nil
	case p.Dst.Is4() && !p.Dst.IsLoopback():
		return "input", "", nil
	}
	return "", "", errors.New("no route")
}

func TestExPublishedPort(t *testing.T) {
	const save = `*nat
:PREROUTING ACCEPT [0:0]
:INPUT ACCEPT [0:0]
:OUTPUT ACCEPT [0:0]
:POSTROUTING ACCEPT [0:0]
:DOCKER - [0:0]
-A PREROUTING -m addrtype --dst-type LOCAL -j DOCKER
-A OUTPUT ! -d 127.0.0.0/8 -m addrtype --dst-type LOCAL -j DOCKER
-A DOCKER ! -i docker0 -p tcp -m tcp --dport 8080 -j DNAT --to-destination 172.18.0.2:80
COMMIT
*filter
:INPUT ACCEPT [0:0]
:FORWARD DROP [0:0]
:OUTPUT ACCEPT [0:0]
:DOCKER-USER - [0:0]
-A FORWARD -j DOCKER-USER
-A FORWARD -d 172.18.0.2/32 -o docker0 -p tcp -m tcp --dport 80 -j ACCEPT
-A DOCKER-USER -s 203.0.113.0/24 -j DROP
COMMIT
`
	rs, err := netfilter.Parse(strings.NewReader(save))
	if err != nil {
		t.Fatal(err)
	}
	in := Input{Rules: rs, Family: fwstate.V4, Route: exRoute, Ifaces: exIfaces(), Private: exPrivate()}
	got := exIndex(t, Evaluate(in, Target{Proto: "tcp", Addr: netip.MustParseAddr("192.0.2.10"), Port: 8080}))
	verdicts := map[string]string{}
	for k, v := range got {
		verdicts[k] = v.Verdict
	}
	if want := exWant("open", "internet", "partial"); !maps.Equal(verdicts, want) {
		t.Fatalf("verdicts = %v, want %v", verdicts, want)
	}
	v := got["internet"]
	if len(v.Allowed) != 1 || v.Allowed[0].DecidedBy != "filter/FORWARD#2" {
		t.Fatalf("internet allowed = %+v", v.Allowed)
	}
	if slices.Contains(v.Allowed[0].Sources, "203.0.113.0/24") || len(v.Allowed[0].Sources) == 0 {
		t.Errorf("internet allowed sources = %q, want the internet without 203.0.113.0/24", v.Allowed[0].Sources)
	}
	if got["private 10.0.0.0/8"].DecidedBy != "filter/FORWARD#2" {
		t.Errorf("private decidedBy = %q", got["private 10.0.0.0/8"].DecidedBy)
	}
}

func TestExIPv6(t *testing.T) {
	const save = `*filter
:INPUT DROP [0:0]
:FORWARD DROP [0:0]
:OUTPUT ACCEPT [0:0]
:ufw6-user-input - [0:0]
-A INPUT -j ufw6-user-input
-A ufw6-user-input -s 2001:db8:1::/48 -p tcp -m tcp --dport 22 -j ACCEPT
COMMIT
`
	rs, err := netfilter.Parse(strings.NewReader(save))
	if err != nil {
		t.Fatal(err)
	}
	a := netip.MustParseAddr
	in := Input{
		Rules: rs, Family: fwstate.V6, Private: exPrivate(),
		Ifaces: []Iface{
			{Name: "lo", Up: true, Loopback: true, Addrs: []netip.Addr{a("::1")}},
			{Name: "eth0", Up: true, Addrs: []netip.Addr{a("192.0.2.10"), a("2001:db8::10")}},
			{Name: "tailscale0", Up: true, Tailnet: true, Addrs: []netip.Addr{a("fd7a:115c:a1e0::5")}},
		},
	}
	got := exIndex(t, Evaluate(in, Target{Proto: "tcp", Addr: netip.IPv6Unspecified(), Port: 22}))
	verdicts := map[string]string{}
	for k, v := range got {
		verdicts[k] = v.Verdict
	}
	want := map[string]string{
		"internet": "partial", "private fc00::/7": "filtered", "private fe80::/10": "filtered",
		"tailnet": "filtered", "localhost": "open",
	}
	if !maps.Equal(verdicts, want) {
		t.Fatalf("verdicts = %v, want %v", verdicts, want)
	}
	wantAllowed := []Allowed{{Sources: []string{"2001:db8:1::/48"}, DecidedBy: "filter/ufw6-user-input#1"}}
	if v := got["internet"]; !slices.EqualFunc(v.Allowed, wantAllowed, exAllowedEqual) {
		t.Errorf("internet allowed = %+v, want %+v", v.Allowed, wantAllowed)
	}
}
