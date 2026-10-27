package loaded

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/platform/netfilter"
)

func cmpParse(t *testing.T, text string) *netfilter.Ruleset {
	t.Helper()
	rs, err := netfilter.Parse(strings.NewReader(text))
	if err != nil {
		t.Fatal(err)
	}
	return rs
}

// cmpGolden reads one family's five golden tables into one ruleset.
func cmpGolden(t *testing.T, image, scenario, fam string) *netfilter.Ruleset {
	t.Helper()
	out := &netfilter.Ruleset{}
	for _, table := range netfilter.Tables() {
		data, err := os.ReadFile(filepath.Join("../testdata/golden", image, scenario, "save", "nft-"+fam+"-"+table+".txt")) //nolint:gosec // G304: a fixed name under the golden directory
		if err != nil {
			t.Fatal(err)
		}
		out.Tables = append(out.Tables, cmpParse(t, string(data)).Tables...)
	}
	return out
}

func cmpLoaded(t *testing.T, image, scenario string) fwstate.Loaded {
	return fwstate.Loaded{Variant: "nft", V4: cmpGolden(t, image, scenario, "v4"), V6: cmpGolden(t, image, scenario, "v6")}
}

const cmpBase = `*filter
:INPUT DROP [0:0]
:FORWARD DROP [0:0]
:OUTPUT ACCEPT [0:0]
:ufw-user-input - [0:0]
-A INPUT -j ufw-user-input
-A ufw-user-input -p tcp -m tcp --dport 22 -j ACCEPT
-A ufw-user-input -p udp -m udp --dport 53 -j ACCEPT
-A ufw-user-input -p tcp -m tcp --dport 3000 -j REJECT
COMMIT
`

func cmpV4(t *testing.T, text string) fwstate.Loaded {
	return fwstate.Loaded{V4: cmpParse(t, text)}
}

// cmpKey is the IPv4 key of one rule given without its "-A <chain>".
func cmpKey(t *testing.T, rule string) string {
	t.Helper()
	rs := cmpParse(t, "*filter\n:INPUT ACCEPT [0:0]\n-A INPUT "+rule+"\nCOMMIT\n")
	return netfilter.Key(rs.Tables[0].Chains[0].Rules[0], false)
}

func TestCompareGolden(t *testing.T) {
	for _, img := range []string{"ubuntu-22.04", "ubuntu-24.04", "debian-12", "debian-13"} {
		a := cmpLoaded(t, img, "rules")
		if d := Compare(a, cmpLoaded(t, img, "rules"), nil); d != nil {
			t.Errorf("%s: identical states differ: %+v", img, d)
		}
		d := Compare(a, cmpLoaded(t, img, "disabled"), nil)
		if len(d) == 0 {
			t.Fatalf("%s: enabled and disabled do not differ", img)
		}
		for _, c := range d {
			if c.Table != "filter" {
				t.Errorf("%s: %s %s/%s differs", img, c.Family, c.Table, c.Chain)
			}
		}
	}
}

func TestCompareChanges(t *testing.T) {
	cases := []struct {
		name string
		b    string
		want []fwstate.ChainDiff
	}{
		{"policy", strings.Replace(cmpBase, ":INPUT DROP", ":INPUT ACCEPT", 1),
			[]fwstate.ChainDiff{{Family: fwstate.V4, Table: "filter", Chain: "INPUT", PolicyA: "DROP", PolicyB: "ACCEPT"}}},
		{"extra rule", strings.Replace(cmpBase, "COMMIT", "-A ufw-user-input -s 192.0.2.7/32 -j DROP\nCOMMIT", 1),
			[]fwstate.ChainDiff{{Family: fwstate.V4, Table: "filter", Chain: "ufw-user-input", OnlyB: []string{cmpKey(t, "-s 192.0.2.7/32 -j DROP")}}}},
		{"order", strings.Replace(cmpBase, "-A ufw-user-input -p tcp -m tcp --dport 22 -j ACCEPT\n-A ufw-user-input -p udp -m udp --dport 53 -j ACCEPT",
			"-A ufw-user-input -p udp -m udp --dport 53 -j ACCEPT\n-A ufw-user-input -p tcp -m tcp --dport 22 -j ACCEPT", 1),
			[]fwstate.ChainDiff{{Family: fwstate.V4, Table: "filter", Chain: "ufw-user-input", OrderDiffers: true}}},
		{"user chain on one side", strings.Replace(cmpBase, ":ufw-user-input - [0:0]", ":ufw-user-input - [0:0]\n:dp-extra - [0:0]", 1),
			[]fwstate.ChainDiff{{Family: fwstate.V4, Table: "filter", Chain: "dp-extra"}}},
		{"formatting only", strings.Replace(cmpBase, "--dport 3000 -j REJECT", "--dport 3000 -j REJECT --reject-with icmp-port-unreachable", 1), nil},
		{"empty table of the save tool", cmpBase + "*nat\n:PREROUTING ACCEPT [0:0]\n:INPUT ACCEPT [0:0]\n:OUTPUT ACCEPT [0:0]\n:POSTROUTING ACCEPT [0:0]\nCOMMIT\n", nil},
		{"table with a policy on one side", cmpBase + "*raw\n:PREROUTING DROP [0:0]\n:OUTPUT ACCEPT [0:0]\nCOMMIT\n",
			[]fwstate.ChainDiff{{Family: fwstate.V4, Table: "raw", Chain: "PREROUTING", PolicyA: "ACCEPT", PolicyB: "DROP"}}},
	}
	a := cmpV4(t, cmpBase)
	for _, c := range cases {
		got := Compare(a, cmpV4(t, c.b), nil)
		if len(got) != len(c.want) {
			t.Fatalf("%s: got %+v, want %+v", c.name, got, c.want)
		}
		for i := range got {
			if !reflect.DeepEqual(got[i], c.want[i]) {
				t.Errorf("%s: got %+v, want %+v", c.name, got[i], c.want[i])
			}
		}
	}
}

func TestCompareSides(t *testing.T) {
	a := cmpV4(t, cmpBase)
	b := cmpV4(t, strings.Replace(cmpBase, "-A ufw-user-input -p udp -m udp --dport 53 -j ACCEPT\n", "", 1))
	ab, ba := Compare(a, b, nil), Compare(b, a, nil)
	if len(ab) != 1 || len(ab[0].OnlyA) != 1 || ab[0].OnlyB != nil || len(ba) != 1 || len(ba[0].OnlyB) != 1 || ba[0].OnlyA != nil {
		t.Errorf("a-b %+v, b-a %+v", ab, ba)
	}
	if ab[0].OnlyA[0] != netfilter.Key(a.V4.Table("filter").Chain("ufw-user-input").Rules[1], false) {
		t.Errorf("OnlyA %q is not the rule's key", ab[0].OnlyA)
	}
	var chains []string
	for _, d := range Compare(a, fwstate.Loaded{}, nil) {
		chains = append(chains, d.Chain)
	}
	if want := []string{"FORWARD", "INPUT", "ufw-user-input"}; !reflect.DeepEqual(chains, want) {
		t.Errorf("against nothing: %q, want %q", chains, want)
	}
}

func TestCompareOrderAndKeep(t *testing.T) {
	v6 := "*filter\n:INPUT ACCEPT [0:0]\n:FORWARD ACCEPT [0:0]\n:OUTPUT ACCEPT [0:0]\n-A INPUT -s 2001:db8::1/128 -j DROP\nCOMMIT\n"
	a := fwstate.Loaded{V4: cmpParse(t, cmpBase), V6: cmpParse(t, v6)}
	b := fwstate.Loaded{
		V4: cmpParse(t, strings.NewReplacer(":INPUT DROP", ":INPUT ACCEPT", ":FORWARD DROP", ":FORWARD ACCEPT").Replace(cmpBase)+
			"*mangle\n:PREROUTING ACCEPT [0:0]\n:INPUT ACCEPT [0:0]\n:FORWARD ACCEPT [0:0]\n:OUTPUT ACCEPT [0:0]\n:POSTROUTING ACCEPT [0:0]\n-A OUTPUT -j MARK --set-xmark 0x1/0xffffffff\nCOMMIT\n"),
		V6: cmpParse(t, strings.Replace(v6, "-A INPUT -s 2001:db8::1/128 -j DROP\n", "", 1)),
	}
	var got []string
	for _, d := range Compare(a, b, nil) {
		got = append(got, string(d.Family)+" "+d.Table+" "+d.Chain)
	}
	want := []string{"v4 filter FORWARD", "v4 filter INPUT", "v4 mangle OUTPUT", "v6 filter INPUT"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("order %q, want %q", got, want)
	}
	only := Compare(a, b, func(f fwstate.Family, table, chain string) bool {
		return f == fwstate.V4 && table == "filter" && chain == "INPUT"
	})
	if len(only) != 1 || only[0].Chain != "INPUT" || only[0].Family != fwstate.V4 {
		t.Errorf("keep: %+v", only)
	}
}

func TestCompareDuplicates(t *testing.T) {
	dup := "*filter\n:INPUT ACCEPT [0:0]\n:FORWARD ACCEPT [0:0]\n:OUTPUT ACCEPT [0:0]\n-A INPUT -s 192.0.2.1/32 -j DROP\n-A INPUT -s 192.0.2.2/32 -j DROP\n-A INPUT -s 192.0.2.1/32 -j DROP\nCOMMIT\n"
	moved := strings.Replace(dup, "-A INPUT -s 192.0.2.2/32 -j DROP\n-A INPUT -s 192.0.2.1/32 -j DROP\n", "-A INPUT -s 192.0.2.1/32 -j DROP\n-A INPUT -s 192.0.2.2/32 -j DROP\n", 1)
	d := Compare(cmpV4(t, dup), cmpV4(t, moved), nil)
	if len(d) != 1 || !d[0].OrderDiffers || d[0].OnlyA != nil || d[0].OnlyB != nil {
		t.Errorf("a moved duplicate: %+v", d)
	}
	once := strings.Replace(dup, "-A INPUT -s 192.0.2.1/32 -j DROP\nCOMMIT", "COMMIT", 1)
	d = Compare(cmpV4(t, dup), cmpV4(t, once), nil)
	if len(d) != 1 || len(d[0].OnlyA) != 1 || d[0].OrderDiffers {
		t.Errorf("a removed duplicate: %+v", d)
	}
}
