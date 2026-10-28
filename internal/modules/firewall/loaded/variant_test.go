package loaded

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/platform/netfilter"
)

// vrReader is a Reader that sees no interfaces and the given nf_tables state.
func vrReader(nftLoaded bool) Reader {
	r := ldReader()
	r.NFTablesLoaded = func() bool { return nftLoaded }
	return r
}

// vrNames writes the kernel's list of legacy tables for one family into a
// fresh stand-in for /proc/net and returns its path.
func vrNames(t *testing.T, file, tables string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, file), []byte(tables), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func vrTableNames(rs *netfilter.Ruleset) []string {
	var names []string
	for _, tb := range rs.Tables {
		names = append(names, tb.Name)
	}
	return names
}

func TestVrReadVariantNFT(t *testing.T) {
	cases := []struct {
		fam  fwstate.Family
		tool string
		key  string
	}{
		{fwstate.V4, "iptables", "v4"},
		{fwstate.V6, "ip6tables", "v6"},
	}
	for _, c := range cases {
		t.Run(c.key, func(t *testing.T) {
			x := ldGolden(t, "debian-12", "rules")
			rs, err := vrReader(true).readVariant(context.Background(), ldEnv(x, nil), t.TempDir(), c.fam, "nft", []string{"nat", "filter"})
			if err != nil {
				t.Fatal(err)
			}
			if got := vrTableNames(rs); !slices.Equal(got, []string{"nat", "filter"}) {
				t.Fatalf("tables %v, want the requested nat and filter in that order", got)
			}
			for _, tb := range rs.Tables {
				data, err := os.ReadFile(filepath.Join(ldGoldenDir, "debian-12", "rules", "save", "nft-"+c.key+"-"+tb.Name+".txt")) //nolint:gosec // G304: a fixed name under the golden directory
				if err != nil {
					t.Fatal(err)
				}
				if want := ldParse(t, string(data)).Tables[0]; !reflect.DeepEqual(tb, want) {
					t.Errorf("table %s differs from the capture", tb.Name)
				}
			}
			want := []string{"/usr/sbin/" + c.tool + "-nft-save -t nat", "/usr/sbin/" + c.tool + "-nft-save -t filter"}
			if !slices.Equal(x.calls, want) {
				t.Errorf("commands %q, want only %q", x.calls, want)
			}
		})
	}
}

func TestVrReadVariantNFTNotLoaded(t *testing.T) {
	x := &ldRunner{answers: map[string]ldResult{}}
	rs, err := vrReader(false).readVariant(context.Background(), ldEnv(x, nil), t.TempDir(), fwstate.V4, "nft", netfilter.Tables())
	if err != nil || rs != nil {
		t.Fatalf("= %+v, %v; want nil, nil while nf_tables is not loaded", rs, err)
	}
	if len(x.calls) != 0 {
		t.Errorf("ran %q while nf_tables is not loaded", x.calls)
	}
}

func TestVrReadVariantLegacy(t *testing.T) {
	procNet := vrNames(t, "ip_tables_names", "raw\nfilter\nbogus\n")
	x := &ldRunner{answers: map[string]ldResult{
		"/usr/sbin/iptables-legacy-save -t filter": {out: ldLegacyFilter},
		"/usr/sbin/iptables-legacy-save -t raw":    {out: ldLegacyRaw},
	}}
	rs, err := vrReader(true).readVariant(context.Background(), ldEnv(x, nil), procNet, fwstate.V4, "legacy", []string{"filter", "nat", "raw", "security", "filter"})
	if err != nil {
		t.Fatal(err)
	}
	if got := vrTableNames(rs); !slices.Equal(got, []string{"filter", "raw"}) {
		t.Fatalf("tables %v, want the listed ones among those requested, once each, in the requested order", got)
	}
	if got := rs.Table("filter").Chain("INPUT").Rules[0].Text; got != "-A INPUT -s 198.51.100.7/32 -j DROP" {
		t.Errorf("filter INPUT rule %q", got)
	}
	want := []string{"/usr/sbin/iptables-legacy-save -t filter", "/usr/sbin/iptables-legacy-save -t raw"}
	if !slices.Equal(x.calls, want) {
		t.Errorf("commands %q, want only %q: an unlisted legacy table is never asked for", x.calls, want)
	}
}

func TestVrReadVariantLegacyIPv6(t *testing.T) {
	procNet := vrNames(t, "ip6_tables_names", "filter\n")
	x := &ldRunner{answers: map[string]ldResult{
		"/usr/sbin/ip6tables-legacy-save -t filter": {out: ldLegacyFilter},
	}}
	rs, err := vrReader(false).readVariant(context.Background(), ldEnv(x, nil), procNet, fwstate.V6, "legacy", []string{"filter", "raw"})
	if err != nil {
		t.Fatal(err)
	}
	if got := vrTableNames(rs); !slices.Equal(got, []string{"filter"}) {
		t.Fatalf("tables %v, want only filter: the IPv6 list names no other", got)
	}
	if want := []string{"/usr/sbin/ip6tables-legacy-save -t filter"}; !slices.Equal(x.calls, want) {
		t.Errorf("commands %q, want %q", x.calls, want)
	}
}

func TestVrReadVariantLegacyNothingListed(t *testing.T) {
	cases := []struct {
		name    string
		procNet func(t *testing.T) string
	}{
		{"no list: the module is not loaded", func(t *testing.T) string { return t.TempDir() }},
		{"a list without the requested tables", func(t *testing.T) string { return vrNames(t, "ip_tables_names", "nat\nmangle\n") }},
		{"an empty list", func(t *testing.T) string { return vrNames(t, "ip_tables_names", "") }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			x := &ldRunner{answers: map[string]ldResult{}}
			rs, err := vrReader(true).readVariant(context.Background(), ldEnv(x, nil), c.procNet(t), fwstate.V4, "legacy", []string{"filter", "raw"})
			if err != nil {
				t.Fatal(err)
			}
			if rs == nil || len(rs.Tables) != 0 {
				t.Fatalf("= %+v, want an empty ruleset: no such legacy table exists", rs)
			}
			if len(x.calls) != 0 {
				t.Errorf("ran %q although no requested table exists", x.calls)
			}
		})
	}
}

func TestVrReadVariantToolNotInstalled(t *testing.T) {
	t.Run("nft", func(t *testing.T) {
		x := &ldRunner{answers: map[string]ldResult{
			"/usr/sbin/ip6tables-nft-save -t filter": {err: ldMissing("/usr/sbin/ip6tables-nft-save")},
		}}
		rs, err := vrReader(true).readVariant(context.Background(), ldEnv(x, nil), t.TempDir(), fwstate.V6, "nft", []string{"filter"})
		if err != nil || rs != nil {
			t.Fatalf("= %+v, %v; want nil, nil for a save tool that is not installed", rs, err)
		}
	})
	t.Run("legacy", func(t *testing.T) {
		procNet := vrNames(t, "ip_tables_names", "filter\n")
		x := &ldRunner{answers: map[string]ldResult{
			"/usr/sbin/iptables-legacy-save -t filter": {err: ldMissing("/usr/sbin/iptables-legacy-save")},
		}}
		rs, err := vrReader(true).readVariant(context.Background(), ldEnv(x, nil), procNet, fwstate.V4, "legacy", []string{"filter"})
		if err != nil || rs != nil {
			t.Fatalf("= %+v, %v; want nil, nil for a save tool that is not installed", rs, err)
		}
	})
}

func TestVrReadVariantErrors(t *testing.T) {
	cases := []struct {
		name    string
		fam     fwstate.Family
		variant string
		tables  []string
		answers map[string]ldResult
		want    string
	}{
		{"unknown variant", fwstate.V4, "nftables", []string{"filter"}, nil, `loaded: unknown iptables variant "nftables"`},
		{"unknown family", "v5", "nft", []string{"filter"}, nil, `loaded: unknown family "v5"`},
		{"unknown table", fwstate.V4, "nft", []string{"filter", "bogus"}, nil, `loaded: unknown table "bogus"`},
		{"save fails", fwstate.V4, "nft", []string{"raw"},
			map[string]ldResult{"/usr/sbin/iptables-nft-save -t raw": {code: 1, stderr: "permission denied\nmore"}},
			"loaded: /usr/sbin/iptables-nft-save -t raw exited with status 1: permission denied"},
		{"runner error", fwstate.V6, "nft", []string{"nat"},
			map[string]ldResult{"/usr/sbin/ip6tables-nft-save -t nat": {err: context.DeadlineExceeded}},
			"loaded: /usr/sbin/ip6tables-nft-save -t nat: context deadline exceeded"},
		{"empty save", fwstate.V4, "nft", []string{"mangle"},
			map[string]ldResult{"/usr/sbin/iptables-nft-save -t mangle": {}},
			"loaded: /usr/sbin/iptables-nft-save -t mangle: netfilter: no tables in the input"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			x := &ldRunner{answers: c.answers}
			rs, err := vrReader(true).readVariant(context.Background(), ldEnv(x, nil), t.TempDir(), c.fam, c.variant, c.tables)
			if err == nil || err.Error() != c.want {
				t.Fatalf("err = %v, want %q", err, c.want)
			}
			if rs != nil {
				t.Errorf("ruleset %+v with an error", rs)
			}
			if c.name == "empty save" && !errors.Is(err, netfilter.ErrNoTables) {
				t.Errorf("err does not wrap netfilter.ErrNoTables")
			}
			if c.answers == nil && len(x.calls) != 0 {
				t.Errorf("ran %q before rejecting the arguments", x.calls)
			}
		})
	}
}

func TestVrReadVariantExported(t *testing.T) {
	x := ldGolden(t, "debian-13", "enabled")
	rs, err := vrReader(true).ReadVariant(context.Background(), ldEnv(x, nil), fwstate.V4, "nft", []string{"filter"})
	if err != nil {
		t.Fatal(err)
	}
	if rs == nil || len(rs.Tables) != 1 || rs.Tables[0].Name != "filter" {
		t.Fatalf("= %+v, want the filter table", rs)
	}
	if want := []string{"/usr/sbin/iptables-nft-save -t filter"}; !slices.Equal(x.calls, want) {
		t.Errorf("commands %q, want %q", x.calls, want)
	}
}
