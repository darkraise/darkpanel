package dockerfw

import (
	"bytes"
	"net/netip"
	"reflect"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/platform/netfilter"
)

func bkA(s string) netip.Addr   { return netip.MustParseAddr(s) }
func bkP(s string) netip.Prefix { return netip.MustParsePrefix(s) }
func bkPs(s ...string) []netip.Prefix {
	var out []netip.Prefix
	for _, p := range s {
		out = append(out, bkP(p))
	}
	return out
}

// bkRuleset parses restore text into the ruleset iptables-restore would load.
func bkRuleset(t *testing.T, text []byte) *netfilter.Ruleset {
	t.Helper()
	rs, err := netfilter.ParseRestore(bytes.NewReader(text))
	if err != nil {
		t.Fatalf("parse restore input: %v\n%s", err, text)
	}
	return rs
}

// bkInput holds three bindings: aaaaaaaaaaaa is published on both families,
// bbbbbbbbbbbb on IPv4 only and cccccccccccc on IPv6 only.
func bkInput() BlockInput {
	s := DefaultSettings()
	s.ForwardExemptions = []Exemption{
		{InInterface: "eth1", OrigDestination: bkA("203.0.113.9"), OrigPort: 8080, Destination: bkA("10.9.0.5"), Proto: "tcp", Port: 80},
		{OrigDestination: bkA("2001:db8::9"), Destination: bkA("2001:db8:1::5"), Proto: "udp"},
	}
	return BlockInput{
		CustomBridges: []string{"dpcustom"},
		Settings:      s,
		Lines: []BindingLine{
			{Proto: "tcp", ContainerAddr: bkA("172.17.0.2"), ContainerPort: 80, HostPort: 8080, Chain: "dp-b-aaaaaaaaaaaa"},
			{Proto: "tcp", ContainerAddr: bkA("172.30.0.2"), ContainerPort: 80, HostAddr: bkA("203.0.113.7"), HostPort: 9090, Chain: "dp-b-bbbbbbbbbbbb"},
			{Proto: "udp", ContainerAddr: bkA("fd00:dead::2"), ContainerPort: 53, HostAddr: bkA("2001:db8::7"), HostPort: 5353, Chain: "dp-b-cccccccccccc"},
			{Proto: "tcp", ContainerAddr: bkA("fd00:dead::3"), ContainerPort: 80, HostPort: 8080, Chain: "dp-b-aaaaaaaaaaaa"},
		},
		Chains: []BindingChain{
			{Name: "dp-b-aaaaaaaaaaaa", Deny: bkPs("203.0.113.0/24", "2001:db8:bad::/48")},
			{Name: "dp-b-bbbbbbbbbbbb", Allow: bkPs("198.51.100.0/24")},
			{Name: "dp-b-cccccccccccc", Allow: bkPs("2001:db8:aa::/48")},
		},
	}
}

const bkWantV4 = `*filter
:ufw-user-forward - [0:0]
:DOCKER-USER - [0:0]
:darkpanel-docker-bindings - [0:0]
:darkpanel-docker-deny - [0:0]
:dp-b-aaaaaaaaaaaa - [0:0]
:dp-b-bbbbbbbbbbbb - [0:0]
-A DOCKER-USER -m conntrack --ctstate RELATED,ESTABLISHED -j RETURN
-A DOCKER-USER -i docker0 -j RETURN
-A DOCKER-USER -i br-+ -j RETURN
-A DOCKER-USER -i dpcustom -j RETURN
-A DOCKER-USER -s 10.0.0.0/8 -j RETURN
-A DOCKER-USER -s 172.16.0.0/12 -j RETURN
-A DOCKER-USER -s 192.168.0.0/16 -j RETURN
-A DOCKER-USER -s 100.64.0.0/10 -j RETURN
-A DOCKER-USER -i eth1 -m conntrack --ctstate DNAT --ctorigdst 203.0.113.9 --ctorigdstport 8080 -p tcp -d 10.9.0.5 --dport 80 -j RETURN
-A DOCKER-USER -m conntrack --ctstate DNAT -j darkpanel-docker-bindings
-A DOCKER-USER -o docker0 -j darkpanel-docker-deny
-A DOCKER-USER -o br-+ -j darkpanel-docker-deny
-A DOCKER-USER -o dpcustom -j darkpanel-docker-deny
-A DOCKER-USER -d 10.0.0.0/8 -j darkpanel-docker-deny
-A DOCKER-USER -d 172.16.0.0/12 -j darkpanel-docker-deny
-A DOCKER-USER -d 192.168.0.0/16 -j darkpanel-docker-deny
-A DOCKER-USER -j RETURN
-A darkpanel-docker-bindings -p tcp -d 172.17.0.2 --dport 80 -m conntrack --ctorigdstport 8080 -j dp-b-aaaaaaaaaaaa
-A darkpanel-docker-bindings -p tcp -d 172.30.0.2 --dport 80 -m conntrack --ctorigdst 203.0.113.7 --ctorigdstport 9090 -j dp-b-bbbbbbbbbbbb
-A darkpanel-docker-bindings -j darkpanel-docker-deny
-A dp-b-aaaaaaaaaaaa -s 203.0.113.0/24 -j darkpanel-docker-deny
-A dp-b-aaaaaaaaaaaa -s 0.0.0.0/0 -j ufw-user-forward
-A dp-b-aaaaaaaaaaaa -j darkpanel-docker-deny
-A dp-b-bbbbbbbbbbbb -s 198.51.100.0/24 -j ufw-user-forward
-A dp-b-bbbbbbbbbbbb -j darkpanel-docker-deny
-A darkpanel-docker-deny -m limit --limit 3/min -j LOG --log-prefix "[DARKPANEL DOCKER DENY] "
-A darkpanel-docker-deny -j DROP
COMMIT
`

const bkWantV6 = `*filter
:ufw6-user-forward - [0:0]
:DOCKER-USER - [0:0]
:darkpanel-docker-bindings - [0:0]
:darkpanel-docker-deny - [0:0]
:dp-b-aaaaaaaaaaaa - [0:0]
:dp-b-cccccccccccc - [0:0]
-A DOCKER-USER -m conntrack --ctstate RELATED,ESTABLISHED -j RETURN
-A DOCKER-USER -i docker0 -j RETURN
-A DOCKER-USER -i br-+ -j RETURN
-A DOCKER-USER -i dpcustom -j RETURN
-A DOCKER-USER -s fc00::/7 -j RETURN
-A DOCKER-USER -s fe80::/10 -j RETURN
-A DOCKER-USER -m conntrack --ctstate DNAT --ctorigdst 2001:db8::9 -p udp -d 2001:db8:1::5 -j RETURN
-A DOCKER-USER -m conntrack --ctstate DNAT -j darkpanel-docker-bindings
-A DOCKER-USER -o docker0 -j darkpanel-docker-deny
-A DOCKER-USER -o br-+ -j darkpanel-docker-deny
-A DOCKER-USER -o dpcustom -j darkpanel-docker-deny
-A DOCKER-USER -d fc00::/7 -j darkpanel-docker-deny
-A DOCKER-USER -j RETURN
-A darkpanel-docker-bindings -p udp -d fd00:dead::2 --dport 53 -m conntrack --ctorigdst 2001:db8::7 --ctorigdstport 5353 -j dp-b-cccccccccccc
-A darkpanel-docker-bindings -p tcp -d fd00:dead::3 --dport 80 -m conntrack --ctorigdstport 8080 -j dp-b-aaaaaaaaaaaa
-A darkpanel-docker-bindings -j darkpanel-docker-deny
-A dp-b-aaaaaaaaaaaa -s 2001:db8:bad::/48 -j darkpanel-docker-deny
-A dp-b-aaaaaaaaaaaa -s ::/0 -j ufw6-user-forward
-A dp-b-aaaaaaaaaaaa -j darkpanel-docker-deny
-A dp-b-cccccccccccc -s 2001:db8:aa::/48 -j ufw6-user-forward
-A dp-b-cccccccccccc -j darkpanel-docker-deny
-A darkpanel-docker-deny -m limit --limit 3/min -j LOG --log-prefix "[DARKPANEL DOCKER DENY] "
-A darkpanel-docker-deny -j DROP
COMMIT
`

const bkWantV6Empty = `*filter
:ufw6-user-forward - [0:0]
:DOCKER-USER - [0:0]
:darkpanel-docker-bindings - [0:0]
:darkpanel-docker-deny - [0:0]
-A DOCKER-USER -m conntrack --ctstate RELATED,ESTABLISHED -j RETURN
-A DOCKER-USER -i docker0 -j RETURN
-A DOCKER-USER -i br-+ -j RETURN
-A DOCKER-USER -m conntrack --ctstate DNAT -j darkpanel-docker-bindings
-A DOCKER-USER -o docker0 -j darkpanel-docker-deny
-A DOCKER-USER -o br-+ -j darkpanel-docker-deny
-A DOCKER-USER -d fc00::/7 -j darkpanel-docker-deny
-A DOCKER-USER -j RETURN
-A darkpanel-docker-bindings -j darkpanel-docker-deny
-A darkpanel-docker-deny -m limit --limit 3/min -j LOG --log-prefix "[DARKPANEL DOCKER DENY] "
-A darkpanel-docker-deny -j DROP
COMMIT
`

func TestBkRender(t *testing.T) {
	tests := []struct {
		name   string
		in     BlockInput
		family fwstate.Family
		want   string
	}{
		{"IPv4 with bridges, exemptions, bindings and chains", bkInput(), fwstate.V4, bkWantV4},
		{"IPv6 keeps only its own entries", bkInput(), fwstate.V6, bkWantV6},
		{"no settings, bindings or bridges still gates private destinations", BlockInput{}, fwstate.V6, bkWantV6Empty},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := string(Render(tt.in, tt.family)); got != tt.want {
				t.Errorf("Render =\n%s\nwant\n%s", got, tt.want)
			}
		})
	}
}

func TestBkRenderChainFamilies(t *testing.T) {
	in := BlockInput{
		Settings: DefaultSettings(),
		Lines: []BindingLine{
			{Proto: "tcp", ContainerAddr: bkA("172.30.0.2"), ContainerPort: 80, HostPort: 9090, Chain: "dp-b-bbbbbbbbbbbb"},
			{Proto: "tcp", ContainerAddr: bkA("fd00:dead::2"), ContainerPort: 80, HostPort: 9090, Chain: "dp-b-bbbbbbbbbbbb"},
		},
		Chains: []BindingChain{{Name: "dp-b-bbbbbbbbbbbb", Deny: bkPs("203.0.113.0/24"), Allow: bkPs("198.51.100.0/24")}},
	}
	chain := func(f fwstate.Family) []string {
		var out []string
		for _, l := range strings.Split(string(Render(in, f)), "\n") {
			if strings.HasPrefix(l, "-A dp-b-bbbbbbbbbbbb ") {
				out = append(out, l)
			}
		}
		return out
	}
	wantV4 := []string{
		"-A dp-b-bbbbbbbbbbbb -s 203.0.113.0/24 -j darkpanel-docker-deny",
		"-A dp-b-bbbbbbbbbbbb -s 198.51.100.0/24 -j ufw-user-forward",
		"-A dp-b-bbbbbbbbbbbb -j darkpanel-docker-deny",
	}
	// An allow list with no source of the family lets nothing of it through.
	wantV6 := []string{"-A dp-b-bbbbbbbbbbbb -j darkpanel-docker-deny"}
	if got := chain(fwstate.V4); !reflect.DeepEqual(got, wantV4) {
		t.Errorf("IPv4 chain = %q, want %q", got, wantV4)
	}
	if got := chain(fwstate.V6); !reflect.DeepEqual(got, wantV6) {
		t.Errorf("IPv6 chain = %q, want %q", got, wantV6)
	}
}

func TestBkRenderParses(t *testing.T) {
	wantRules := map[fwstate.Family]map[string]int{
		fwstate.V4: {"ufw-user-forward": 0, "DOCKER-USER": 17, "darkpanel-docker-bindings": 3, "darkpanel-docker-deny": 2, "dp-b-aaaaaaaaaaaa": 3, "dp-b-bbbbbbbbbbbb": 2},
		fwstate.V6: {"ufw6-user-forward": 0, "DOCKER-USER": 13, "darkpanel-docker-bindings": 3, "darkpanel-docker-deny": 2, "dp-b-aaaaaaaaaaaa": 3, "dp-b-cccccccccccc": 2},
	}
	for f, want := range wantRules {
		filter := bkRuleset(t, Render(bkInput(), f)).Table("filter")
		if filter == nil {
			t.Fatalf("%s: no filter table", f)
		}
		got := map[string]int{}
		for _, c := range filter.Chains {
			got[c.Name] = len(c.Rules)
			for _, r := range c.Rules {
				if r.Opaque() {
					t.Errorf("%s: rule %q is opaque: %v", f, r.Text, r.Unsupported)
				}
				if r.Target.Name != "" && r.Target.Name != "RETURN" && r.Target.Name != "DROP" && r.Target.Name != "LOG" && !r.Target.Chain {
					t.Errorf("%s: rule %q does not jump to a declared chain", f, r.Text)
				}
			}
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: rules per chain = %v, want %v", f, got, want)
		}
	}
}

func TestBkBlockRules(t *testing.T) {
	text := Render(bkInput(), fwstate.V4)
	got, err := BlockRules(text)
	if err != nil {
		t.Fatalf("BlockRules: %v", err)
	}
	if want := bkRuleset(t, text); !reflect.DeepEqual(got, want) {
		t.Errorf("BlockRules differs from the restore parser's ruleset")
	}
	if _, err := BlockRules([]byte("-A DOCKER-USER -j RETURN\n")); err == nil {
		t.Error("BlockRules accepted input with no table")
	}
}

const (
	bkAfter = "# rules.input-after\n*filter\n:ufw-after-input - [0:0]\nCOMMIT\n"
	bkBody  = "*filter\n:DOCKER-USER - [0:0]\n-A DOCKER-USER -j RETURN\nCOMMIT\n"
	bkBegin = "# BEGIN DARKPANEL docker\n"
	bkEnd   = "# END DARKPANEL docker\n"
	bkOther = "# BEGIN DARKPANEL firewall/other\n-A x\n# END DARKPANEL firewall/other\n"
)

func TestBkWithBlock(t *testing.T) {
	tests := []struct {
		name, file, block, want string
		wantErr                 bool
	}{
		{name: "appends to a file", file: bkAfter, block: bkBody, want: bkAfter + bkBegin + bkBody + bkEnd},
		{name: "appends after a last line without a newline", file: "a\nCOMMIT", block: bkBody, want: "a\nCOMMIT\n" + bkBegin + bkBody + bkEnd},
		{name: "appends to an empty file", file: "", block: bkBody, want: bkBegin + bkBody + bkEnd},
		{name: "ends an unterminated block text with a newline", file: bkAfter, block: "COMMIT", want: bkAfter + bkBegin + "COMMIT\n" + bkEnd},
		{name: "replaces the block in place", file: "before\n" + bkBegin + "*filter\nold\nCOMMIT\n" + bkEnd + "after\n", block: bkBody, want: "before\n" + bkBegin + bkBody + bkEnd + "after\n"},
		{name: "leaves another module's block alone", file: bkOther + bkAfter, block: bkBody, want: bkOther + bkAfter + bkBegin + bkBody + bkEnd},
		{name: "refuses a duplicated block", file: bkBegin + bkBody + bkEnd + bkBegin + bkBody + bkEnd, block: bkBody, wantErr: true},
		{name: "refuses a nested block", file: bkBegin + bkOther + bkEnd, block: bkBody, wantErr: true},
		{name: "refuses an unterminated block", file: bkAfter + bkBegin + bkBody, block: bkBody, wantErr: true},
		{name: "refuses an end marker without its beginning", file: bkAfter + bkEnd, block: bkBody, wantErr: true},
		{name: "refuses a marker line ending in whitespace", file: "# BEGIN DARKPANEL docker \n" + bkBody + bkEnd, block: bkBody, wantErr: true},
		{name: "refuses a block that holds a marker line", file: bkAfter, block: "# END DARKPANEL docker\n", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := WithBlock([]byte(tt.file), []byte(tt.block))
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if err == nil && string(got) != tt.want {
				t.Errorf("WithBlock =\n%q\nwant\n%q", got, tt.want)
			}
		})
	}
}

func TestBkWithBlockIsIdempotent(t *testing.T) {
	once, err := WithBlock([]byte(bkAfter), []byte(bkBody))
	if err != nil {
		t.Fatal(err)
	}
	twice, err := WithBlock(once, []byte(bkBody))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(once, twice) {
		t.Errorf("a second WithBlock changed the file:\n%q\n%q", once, twice)
	}
}

func TestBkWithoutBlock(t *testing.T) {
	tests := []struct {
		name, file, want string
		wantErr          bool
	}{
		{name: "removes the block and its markers", file: bkAfter + bkBegin + bkBody + bkEnd, want: bkAfter},
		{name: "keeps the text on both sides", file: "before\n" + bkBegin + bkBody + bkEnd + "after\n", want: "before\nafter\n"},
		{name: "keeps another module's block", file: bkOther + bkBegin + bkBody + bkEnd, want: bkOther},
		{name: "returns a file without the block as it is", file: "a\nCOMMIT", want: "a\nCOMMIT"},
		{name: "refuses an unterminated block", file: bkBegin + bkBody, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := WithoutBlock([]byte(tt.file))
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if err == nil && string(got) != tt.want {
				t.Errorf("WithoutBlock = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBkBlockOf(t *testing.T) {
	file := []byte(bkAfter + bkBegin + bkBody + bkEnd + "tail\n")
	block, ok, err := BlockOf(file)
	if err != nil || !ok || string(block) != bkBody {
		t.Errorf("BlockOf = %q, %v, %v; want the block's body", block, ok, err)
	}
	if block, ok, err := BlockOf([]byte(bkAfter)); err != nil || ok || block != nil {
		t.Errorf("BlockOf without a block = %q, %v, %v; want nil, false, nil", block, ok, err)
	}
	for name, bad := range map[string]string{
		"duplicated":   bkBegin + bkEnd + bkBegin + bkEnd,
		"nested":       bkBegin + bkOther + bkEnd,
		"unterminated": bkBegin + bkBody,
	} {
		if _, _, err := BlockOf([]byte(bad)); err == nil {
			t.Errorf("BlockOf accepted a %s block", name)
		}
	}
	// What WithBlock writes is what BlockOf reads back.
	rendered := Render(bkInput(), fwstate.V4)
	written, err := WithBlock([]byte(bkAfter), rendered)
	if err != nil {
		t.Fatal(err)
	}
	if back, ok, err := BlockOf(written); err != nil || !ok || !bytes.Equal(back, rendered) {
		t.Errorf("BlockOf(WithBlock(f, b)) = %q, %v, %v; want b", back, ok, err)
	}
}

func TestBkWithoutBlockUndoesWithBlock(t *testing.T) {
	for _, file := range []string{"", bkAfter, bkOther + bkAfter, "before\n" + bkBegin + "old\n" + bkEnd + "after\n"} {
		with, err := WithBlock([]byte(file), []byte(bkBody))
		if err != nil {
			t.Fatal(err)
		}
		without, err := WithoutBlock(with)
		if err != nil {
			t.Fatal(err)
		}
		want := file
		if i := strings.Index(file, bkBegin); i >= 0 {
			want = "before\nafter\n"
		}
		if string(without) != want {
			t.Errorf("WithoutBlock(WithBlock(%q)) = %q, want %q", file, without, want)
		}
	}
}

func TestBkOwned(t *testing.T) {
	for chain, want := range map[string]bool{
		"DOCKER-USER":               true,
		"darkpanel-docker-bindings": true,
		"darkpanel-docker-deny":     true,
		"darkpanel-anything":        true,
		"dp-b-0123456789ab":         true,
		"DOCKER":                    false,
		"DOCKER-FORWARD":            false,
		"ufw-user-forward":          false,
		"docker-user":               false,
		"dp-c-0123456789ab":         false,
		"FORWARD":                   false,
	} {
		if got := Owned(chain); got != want {
			t.Errorf("Owned(%q) = %v, want %v", chain, got, want)
		}
	}
}

// bkChain reads one chain from iptables-save style text, as the loaded state
// holds it; declared are the other chains its rules jump to.
func bkChain(t *testing.T, name, rules string, declared ...string) *netfilter.Chain {
	t.Helper()
	text := "*filter\n:" + name + " - [0:0]\n"
	for _, d := range declared {
		text += ":" + d + " - [0:0]\n"
	}
	text += rules + "COMMIT\n"
	rs, err := netfilter.Parse(strings.NewReader(text))
	if err != nil {
		t.Fatalf("parse save text: %v", err)
	}
	return rs.Table("filter").Chain(name)
}

func bkTexts(rules []netfilter.Rule) []string {
	var out []string
	for _, r := range rules {
		out = append(out, r.Text)
	}
	return out
}

func TestBkForeign(t *testing.T) {
	known := func(f fwstate.Family, chain string) []netfilter.Rule {
		return bkRuleset(t, Render(bkInput(), f)).Table("filter").Chain(chain).Rules
	}
	tests := []struct {
		name   string
		family fwstate.Family
		chain  *netfilter.Chain
		known  []netfilter.Rule
		want   []string
	}{
		{
			name:   "a rule among the block's lines and a conditional return are foreign, in chain order",
			family: fwstate.V4,
			chain: bkChain(t, "DOCKER-USER", `-A DOCKER-USER -m conntrack --ctstate RELATED,ESTABLISHED -j RETURN
-A DOCKER-USER -i docker0 -j RETURN
-A DOCKER-USER -s 192.0.2.0/24 -j DROP
-A DOCKER-USER -i br-+ -j RETURN
-A DOCKER-USER -s 10.0.0.0/8 -j RETURN
-A DOCKER-USER -i eth0 -j RETURN
-A DOCKER-USER -m conntrack --ctstate DNAT -j darkpanel-docker-bindings
-A DOCKER-USER -j RETURN
`, "darkpanel-docker-bindings"),
			known: known(fwstate.V4, "DOCKER-USER"),
			want:  []string{"-A DOCKER-USER -s 192.0.2.0/24 -j DROP", "-A DOCKER-USER -i eth0 -j RETURN"},
		},
		{
			name:   "Docker's default chain holds no foreign rule",
			family: fwstate.V4,
			chain:  bkChain(t, "DOCKER-USER", "-A DOCKER-USER -j RETURN\n"),
			want:   nil,
		},
		{
			name:   "a second copy of a known rule is not foreign",
			family: fwstate.V4,
			chain:  bkChain(t, "DOCKER-USER", "-A DOCKER-USER -i docker0 -j RETURN\n-A DOCKER-USER -i docker0 -j RETURN\n"),
			known:  known(fwstate.V4, "DOCKER-USER"),
			want:   nil,
		},
		{
			name:   "rules compare by key, not by text",
			family: fwstate.V4,
			chain: bkChain(t, "darkpanel-docker-bindings", `-A darkpanel-docker-bindings -d 172.17.0.2/32 -p tcp -m tcp --dport 80 -m conntrack --ctorigdstport 8080 -j dp-b-aaaaaaaaaaaa
-A darkpanel-docker-bindings -d 172.17.0.9/32 -p tcp -m tcp --dport 80 -m conntrack --ctorigdstport 8080 -j dp-b-aaaaaaaaaaaa
`, "dp-b-aaaaaaaaaaaa"),
			known: known(fwstate.V4, "darkpanel-docker-bindings"),
			want:  []string{"-A darkpanel-docker-bindings -d 172.17.0.9/32 -p tcp -m tcp --dport 80 -m conntrack --ctorigdstport 8080 -j dp-b-aaaaaaaaaaaa"},
		},
		{
			name:   "the family selects the defaults a key fills in",
			family: fwstate.V6,
			chain:  bkChain(t, "DOCKER-USER", "-A DOCKER-USER -s 2001:db8::1/128 -j REJECT --reject-with icmp6-port-unreachable\n"),
			known:  bkRuleset(t, []byte("*filter\n:DOCKER-USER - [0:0]\n-A DOCKER-USER -s 2001:db8::1 -j REJECT\nCOMMIT\n")).Table("filter").Chain("DOCKER-USER").Rules,
			want:   nil,
		},
		{name: "no chain has no foreign rule", family: fwstate.V4, chain: nil, want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := bkTexts(Foreign(tt.chain, tt.known, tt.family)); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Foreign = %q, want %q", got, tt.want)
			}
		})
	}
	t.Run("the same rule under the other family is foreign", func(t *testing.T) {
		chain := bkChain(t, "DOCKER-USER", "-A DOCKER-USER -s 2001:db8::1/128 -j REJECT --reject-with icmp6-port-unreachable\n")
		k := bkRuleset(t, []byte("*filter\n:DOCKER-USER - [0:0]\n-A DOCKER-USER -s 2001:db8::1 -j REJECT\nCOMMIT\n")).Table("filter").Chain("DOCKER-USER").Rules
		if got := Foreign(chain, k, fwstate.V4); len(got) != 1 {
			t.Errorf("Foreign under V4 = %q, want the one rule", bkTexts(got))
		}
	})
}
