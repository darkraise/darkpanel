package dockerfw

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/platform/netfilter"
)

const (
	chForeignA = "-A DOCKER-USER -s 198.51.100.0/24 -j DROP"
	chForeignB = "-A DOCKER-USER -p tcp -m tcp --dport 22 -j ACCEPT"
	chForeignC = "-A DOCKER-USER -i eth9 -j REJECT --reject-with icmp-port-unreachable"
)

var chBlock = []string{
	"-A DOCKER-USER -i docker0 -j RETURN",
	"-A DOCKER-USER -m conntrack --ctstate DNAT -j darkpanel-docker-bindings",
	"-A DOCKER-USER -j RETURN",
}

func chRules(t *testing.T, lines ...string) []netfilter.Rule {
	t.Helper()
	c := chChain(t, lines...)
	return c.Rules
}

func chChain(t *testing.T, lines ...string) *netfilter.Chain {
	t.Helper()
	text := "*filter\n:DOCKER-USER - [0:0]\n" + strings.Join(lines, "\n") + "\nCOMMIT\n"
	rs, err := netfilter.ParseRestore(strings.NewReader(text))
	if err != nil {
		t.Fatalf("parse %q: %v", lines, err)
	}
	return rs.Table("filter").Chain("DOCKER-USER")
}

func chJoin(parts ...[]string) []string { return slices.Concat(parts...) }

func TestChLayoutOf(t *testing.T) {
	a, b, c := []string{chForeignA}, []string{chForeignB}, []string{chForeignC}
	tests := []struct {
		name  string
		chain []string // nil: no chain at all
		known []string
		want  Layout
	}{
		{name: "no chain", want: Layout{}},
		{name: "only the block", chain: chBlock, known: chBlock, want: Layout{}},
		{name: "foreign before the block", chain: chJoin(a, chBlock), known: chBlock, want: Layout{Before: a}},
		{name: "foreign after the block", chain: chJoin(chBlock, a), known: chBlock, want: Layout{After: a}},
		{
			name:  "foreign on both sides",
			chain: chJoin(a, c, chBlock, b),
			known: chBlock,
			want:  Layout{Before: chJoin(a, c), After: b},
		},
		{name: "only foreign rules", chain: chJoin(a, b), known: chBlock, want: Layout{Before: chJoin(a, b)}},
		{
			name:  "Docker's default RETURN counts as the block",
			chain: chJoin(a, []string{"-A DOCKER-USER -j RETURN"}, b),
			want:  Layout{Before: a, After: b},
		},
		{
			name:  "foreign between block lines",
			chain: chJoin(a, chBlock[:1], b, chBlock[1:]),
			known: chBlock,
			want: Layout{
				Before:      chJoin(a, b),
				Interleaved: true,
				Order:       chJoin(a, chBlock[:1], b, chBlock[1:]),
			},
		},
		{
			name:  "interleaved moves the rule after the block too",
			chain: chJoin(chBlock[:1], a, chBlock[1:], b),
			known: chBlock,
			want: Layout{
				Before:      chJoin(a, b),
				Interleaved: true,
				Order:       chJoin(chBlock[:1], a, chBlock[1:], b),
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var chain *netfilter.Chain
			if tc.chain != nil {
				chain = chChain(t, tc.chain...)
			}
			var known []netfilter.Rule
			if tc.known != nil {
				known = chRules(t, tc.known...)
			}
			if got := LayoutOf(chain, known, fwstate.V4); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("LayoutOf = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestChLayoutOfIPv6(t *testing.T) {
	block := []string{"-A DOCKER-USER -d fc00::/7 -j darkpanel-docker-deny", "-A DOCKER-USER -j RETURN"}
	foreign := "-A DOCKER-USER -s 2001:db8::/32 -j DROP"
	got := LayoutOf(chChain(t, chJoin([]string{foreign}, block)...), chRules(t, block...), fwstate.V6)
	if want := (Layout{Before: []string{foreign}}); !reflect.DeepEqual(got, want) {
		t.Errorf("LayoutOf = %#v, want %#v", got, want)
	}
}

func TestChDockerUserInput(t *testing.T) {
	a, b := []string{chForeignA}, []string{chForeignB}
	const head = "*filter\n:DOCKER-USER - [0:0]\n"
	tests := []struct {
		name  string
		block []string
		l     Layout
		want  string
	}{
		{name: "no block, no foreign rules", want: head + "-A DOCKER-USER -j RETURN\nCOMMIT\n"},
		{
			name:  "the block alone",
			block: chBlock,
			want:  head + strings.Join(chBlock, "\n") + "\nCOMMIT\n",
		},
		{
			name:  "foreign rules around the block",
			block: chBlock,
			l:     Layout{Before: a, After: b},
			want:  head + chForeignA + "\n" + strings.Join(chBlock, "\n") + "\n" + chForeignB + "\nCOMMIT\n",
		},
		{
			name: "foreign rules around Docker's default RETURN",
			l:    Layout{Before: a, After: b},
			want: head + chForeignA + "\n-A DOCKER-USER -j RETURN\n" + chForeignB + "\nCOMMIT\n",
		},
		{
			name:  "an empty block slice is -j RETURN",
			block: []string{},
			want:  head + "-A DOCKER-USER -j RETURN\nCOMMIT\n",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := string(DockerUserInput(tc.block, tc.l)); got != tc.want {
				t.Errorf("DockerUserInput =\n%s\nwant\n%s", got, tc.want)
			}
		})
	}
}

// A layout that is not interleaved survives a reload: the chain the input
// builds gives the same layout back.
func TestChLayoutRoundTrip(t *testing.T) {
	l := Layout{Before: []string{chForeignA, chForeignC}, After: []string{chForeignB}}
	rs, err := netfilter.ParseRestore(strings.NewReader(string(DockerUserInput(chBlock, l))))
	if err != nil {
		t.Fatalf("the input does not parse: %v", err)
	}
	chain := rs.Table("filter").Chain("DOCKER-USER")
	if got := LayoutOf(chain, chRules(t, chBlock...), fwstate.V4); !reflect.DeepEqual(got, l) {
		t.Errorf("LayoutOf(reloaded) = %#v, want %#v", got, l)
	}
}

func TestChCleanupInput(t *testing.T) {
	const head = "*filter\n:DOCKER-USER - [0:0]\n-A DOCKER-USER -j RETURN\n"
	loaded := func(t *testing.T, text string) *netfilter.Ruleset {
		t.Helper()
		rs, err := netfilter.ParseRestore(strings.NewReader(text))
		if err != nil {
			t.Fatal(err)
		}
		return rs
	}
	full := loaded(t, "*filter\n:INPUT DROP [0:0]\n:FORWARD DROP [0:0]\n:DOCKER-USER - [0:0]\n:ufw-user-forward - [0:0]\n"+
		":darkpanel-docker-bindings - [0:0]\n:darkpanel-docker-deny - [0:0]\n:dp-b-0123456789ab - [0:0]\n:dp-b-fedcba987654 - [0:0]\n"+
		strings.Join(chBlock, "\n")+"\n-A dp-b-0123456789ab -j darkpanel-docker-deny\nCOMMIT\n"+
		"*nat\n:PREROUTING ACCEPT [0:0]\n:darkpanel-nat - [0:0]\nCOMMIT\n")
	bare := loaded(t, "*filter\n:INPUT DROP [0:0]\n:DOCKER-USER - [0:0]\n:ufw-user-forward - [0:0]\n-A DOCKER-USER -j RETURN\nCOMMIT\n")
	tests := []struct {
		name string
		rs   *netfilter.Ruleset
		want string
	}{
		{
			name: "every darkpanel and binding chain is flushed, then deleted",
			rs:   full,
			want: head +
				"-F darkpanel-docker-bindings\n-F darkpanel-docker-deny\n-F dp-b-0123456789ab\n-F dp-b-fedcba987654\n" +
				"-X darkpanel-docker-bindings\n-X darkpanel-docker-deny\n-X dp-b-0123456789ab\n-X dp-b-fedcba987654\nCOMMIT\n",
		},
		{name: "no owned chain besides DOCKER-USER", rs: bare, want: head + "COMMIT\n"},
		{name: "no ruleset", rs: nil, want: head + "COMMIT\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := string(CleanupInput(tc.rs)); got != tc.want {
				t.Errorf("CleanupInput =\n%s\nwant\n%s", got, tc.want)
			}
		})
	}
}
