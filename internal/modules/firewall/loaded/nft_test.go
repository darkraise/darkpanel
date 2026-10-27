package loaded

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// The captures were made with nft 1.0.2, 1.0.6, 1.0.9 and 1.1.3, which
// print them byte for byte alike.
const nftTestDir = "testdata/nft"

func nftTestRead(t testing.TB, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(nftTestDir, name)) //nolint:gosec // G304: a fixed name under testdata
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestParseNFTTables(t *testing.T) {
	tables, bad := parseNFTTables(nftTestRead(t, "tables.txt"))
	want := []nftTable{{"inet", "dpforeign"}, {"netdev", "dpnetdev"}, {"ip", "dpnat"}, {"ip6", "dpsetonly"}, {"ip", "filter"}}
	if !reflect.DeepEqual(tables, want) || bad != nil {
		t.Errorf("tables %v, bad %q", tables, bad)
	}
	tables, bad = parseNFTTables([]byte("table ip a\n\ntable ip b c\ntable\nchain ip x\n  table inet -dash  \n"))
	if want := []nftTable{{"ip", "a"}, {"inet", "-dash"}}; !reflect.DeepEqual(tables, want) {
		t.Errorf("tables %v, want %v", tables, want)
	}
	if want := []string{"table ip b c", "table", "chain ip x"}; !reflect.DeepEqual(bad, want) {
		t.Errorf("bad %q, want %q", bad, want)
	}
}

func TestParseNFTTableCaptures(t *testing.T) {
	cases := map[string][]nftChain{
		"inet-dpforeign.txt": {
			{Name: "empty", Base: true, Hook: "input", Policy: "accept"},
			{Name: "dropper", Base: true, Hook: "forward", Policy: "drop"},
			{Name: "ruled", Base: true, Hook: "input", Policy: "accept", Rules: 2},
			{Name: "regular", Rules: 2},
			{Name: "commented", Base: true, Hook: "output", Policy: "accept"},
		},
		"netdev-dpnetdev.txt": {{Name: "ingress", Base: true, Hook: "ingress", Policy: "accept"}},
		"ip-dpnat.txt":        {{Name: "post", Base: true, Hook: "postrouting", Policy: "accept", Rules: 1}},
		"ip6-dpsetonly.txt":   {{Name: "regular", Rules: 1}},
	}
	for name, want := range cases {
		got, err := parseNFTTable(nftTestRead(t, name))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s:\n got %+v\nwant %+v", name, got, want)
		}
	}
}

func TestParseNFTTableShapes(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []nftChain
	}{
		{"empty table", "table ip t {\n}\n", nil},
		{"multi-line statement", "table inet t {\n\tchain c {\n\t\ttype filter hook input priority 0; policy accept;\n" +
			"\t\ttcp dport vmap {\n\t\t\t22 : accept,\n\t\t\t80 : drop\n\t\t}\n\t\tdrop\n\t}\n}\n",
			[]nftChain{{Name: "c", Base: true, Hook: "input", Policy: "accept", Rules: 2}}},
		{"braces in strings", "table ip t {\n\tchain \"odd name\" {\n\t\tcomment \"{ not a block\"\n" +
			"\t\ttype filter hook output priority 0; policy drop;\n\t\tmeta mark 1 counter comment \"}}\" accept\n\t}\n}\n",
			[]nftChain{{Name: "odd name", Base: true, Hook: "output", Policy: "drop", Rules: 1}}},
		{"policy on its own line", "table ip t {\n\tchain c {\n\t\ttype filter hook input priority 0;\n\t\tpolicy drop;\n\t}\n}\n",
			[]nftChain{{Name: "c", Base: true, Hook: "input", Policy: "drop"}}},
		{"no policy printed", "table ip t {\n\tchain c {\n\t\ttype filter hook input priority 0;\n\t}\n}\n",
			[]nftChain{{Name: "c", Base: true, Hook: "input"}}},
		{"netdev devices", "table netdev t {\n\tchain c {\n\t\ttype filter hook ingress priority 0; policy accept;\n" +
			"\t\tdevices = { eth0, eth1 }\n\t}\n}\n",
			[]nftChain{{Name: "c", Base: true, Hook: "ingress", Policy: "accept"}}},
		{"objects skipped", "table ip t {\n\tflags dormant\n\tcounter cnt {\n\t\tpackets 0 bytes 0\n\t}\n" +
			"\tmap m {\n\t\ttype ipv4_addr : verdict\n\t\telements = { 192.0.2.1 : drop,\n\t\t\t     192.0.2.2 : accept }\n\t}\n}\n",
			nil},
	}
	for _, c := range cases {
		got, err := parseNFTTable([]byte(c.in))
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s:\n got %+v\nwant %+v", c.name, got, c.want)
		}
	}
}

func TestParseNFTTableErrors(t *testing.T) {
	cases := map[string]string{
		"":                                   "the table block is missing or not closed",
		"table ip t {\n\tchain c {\n\t}\n":   "the table block is missing or not closed",
		"table ip t {\n}\n}\n":               "want one table block",
		"table ip t {\n}}\n":                 "unbalanced braces",
		"table ip t {\n}\ntable ip u {\n}\n": "want one table block",
		"chain c {\n}\n":                     "want one table block",
		"table ip t {\n\tcomment \"x\n}\n":   "unterminated string",
		"table ip t { }\n":                   "want one table block",
		"table ip t {\n" + strings.Repeat("x", maxNFTLine+1) + "\n}\n": "token too long",
	}
	for in, want := range cases {
		_, err := parseNFTTable([]byte(in))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%.40q: err = %v, want %q", in, err, want)
		}
	}
}

func FuzzParseNFTTable(f *testing.F) {
	for _, name := range []string{"inet-dpforeign.txt", "netdev-dpnetdev.txt", "ip-dpnat.txt", "ip6-dpsetonly.txt", "tables.txt"} {
		f.Add(nftTestRead(f, name))
	}
	f.Add([]byte("table ip t {\n\tchain c {\n\t\ttcp dport vmap {\n\t\t\t22 : accept\n\t\t}\n\t}\n}\n"))
	f.Fuzz(func(t *testing.T, data []byte) {
		chains, err := parseNFTTable(data)
		if err != nil && chains != nil {
			t.Fatal("chains returned with an error")
		}
		for _, c := range chains {
			if c.Rules < 0 || !c.Base && (c.Hook != "" || c.Policy != "") {
				t.Fatalf("inconsistent chain %+v", c)
			}
		}
		parseNFTTables(data)
	})
}
