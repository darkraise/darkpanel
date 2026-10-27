package predict

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/platform/netfilter"
)

// predictLoaded reads one golden scenario's loaded state: the five save
// captures of each family, and the sysctl values.
func predictLoaded(t *testing.T, image, scenario string) fwstate.Loaded {
	t.Helper()
	dir := filepath.Join(predictGolden, image, scenario)
	st := fwstate.Loaded{Variant: "nft", Sysctl: map[string]string{}}
	for _, fam := range []string{"v4", "v6"} {
		rs := &netfilter.Ruleset{}
		for _, table := range netfilter.Tables() {
			f, err := os.Open(filepath.Join(dir, "save", "nft-"+fam+"-"+table+".txt")) //nolint:gosec // G304: a fixed name under the golden directory
			if err != nil {
				t.Fatal(err)
			}
			one, err := netfilter.Parse(f)
			_ = f.Close()
			if err != nil {
				t.Fatalf("%s %s %s: %v", dir, fam, table, err)
			}
			rs.Tables = append(rs.Tables, one.Tables...)
		}
		if fam == "v4" {
			st.V4 = rs
		} else {
			st.V6 = rs
		}
	}
	data, err := os.ReadFile(filepath.Join(dir, "sysctl.txt")) //nolint:gosec // G304: a fixed name under the golden directory
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Fields(string(data)) {
		k, v, _ := strings.Cut(line, "=")
		st.Sysctl[k] = v
	}
	return st
}

// predictDiff compares two rulesets of one family chain by chain, by
// netfilter.Key, and describes every difference: a table or chain on one
// side only, a built-in policy, or the rule keys of a chain.
func predictDiff(a, b *netfilter.Ruleset, v6 bool) []string {
	var out []string
	if a == nil || b == nil {
		if a != b {
			out = append(out, fmt.Sprintf("ruleset presence differs: %v, %v", a != nil, b != nil))
		}
		return out
	}
	var tables []string
	for _, rs := range []*netfilter.Ruleset{a, b} {
		for _, t := range rs.Tables {
			if !slices.Contains(tables, t.Name) {
				tables = append(tables, t.Name)
			}
		}
	}
	for _, name := range tables {
		ta, tb := a.Table(name), b.Table(name)
		if ta == nil || tb == nil {
			out = append(out, fmt.Sprintf("table %s on one side only", name))
			continue
		}
		var chains []string
		for _, t := range []*netfilter.Table{ta, tb} {
			for _, c := range t.Chains {
				if !slices.Contains(chains, c.Name) {
					chains = append(chains, c.Name)
				}
			}
		}
		for _, cn := range chains {
			ca, cb := ta.Chain(cn), tb.Chain(cn)
			if ca == nil || cb == nil {
				out = append(out, fmt.Sprintf("%s/%s on one side only", name, cn))
				continue
			}
			if ca.Policy != cb.Policy {
				out = append(out, fmt.Sprintf("%s/%s policy %q, %q", name, cn, ca.Policy, cb.Policy))
			}
			ka, kb := predictKeys(ca, v6), predictKeys(cb, v6)
			if !slices.Equal(ka, kb) {
				out = append(out, fmt.Sprintf("%s/%s rules\n  %s\nvs\n  %s", name, cn, strings.Join(ka, "\n  "), strings.Join(kb, "\n  ")))
			}
		}
	}
	return out
}

func predictKeys(c *netfilter.Chain, v6 bool) []string {
	var out []string
	for _, r := range c.Rules {
		out = append(out, netfilter.Key(r, v6))
	}
	return out
}

// TestTranslateGolden compares the translation of the rules, logging and
// manual scenarios with the filter table captured after UFW loaded them,
// in both families and on every image.
func TestTranslateGolden(t *testing.T) {
	for _, img := range predictImages {
		for _, sc := range []string{"rules", "logging", "manual"} {
			cfg := predictConfig(t, img, sc)
			st := predictLoaded(t, img, sc)
			for _, v6 := range []bool{false, true} {
				got, err := translate(cfg, v6)
				if err != nil {
					t.Fatalf("%s/%s: %v", img, sc, err)
				}
				want := st.V4
				if v6 {
					want = st.V6
				}
				filter := &netfilter.Ruleset{Tables: []netfilter.Table{*want.Table("filter")}}
				if len(got.Tables) != 1 {
					t.Errorf("%s/%s v6=%v: %d tables, want only filter", img, sc, v6, len(got.Tables))
				}
				for _, d := range predictDiff(got, filter, v6) {
					t.Errorf("%s/%s v6=%v: %s", img, sc, v6, d)
				}
				rules := 0
				for _, c := range got.Table("filter").Chains {
					rules += len(c.Rules)
				}
				if n := len(got.Table("filter").Chains); n < 30 || rules < 60 {
					t.Errorf("%s/%s v6=%v: only %d chains and %d rules", img, sc, v6, n, rules)
				}
			}
		}
	}
}

// TestTranslatePolicies checks the reject, track and skip-to-policy rules
// and the built-in policies for each default policy, as ufw-init-functions
// writes them.
func TestTranslatePolicies(t *testing.T) {
	cfg := predictConfig(t, "debian-13", "rules")
	cfg.DefaultInput, cfg.DefaultOutput, cfg.DefaultForward = "REJECT", "DROP", "ACCEPT"
	rs, err := translate(cfg, false)
	if err != nil {
		t.Fatal(err)
	}
	f := rs.Table("filter")
	for chain, want := range map[string][]string{
		"ufw-reject-input":           {"-j REJECT"},
		"ufw-reject-output":          nil,
		"ufw-track-forward":          {"-p tcp -m conntrack --ctstate NEW -j ACCEPT", "-p udp -m conntrack --ctstate NEW -j ACCEPT"},
		"ufw-track-input":            nil,
		"ufw-skip-to-policy-input":   {"-j REJECT"},
		"ufw-skip-to-policy-output":  {"-j DROP"},
		"ufw-skip-to-policy-forward": {"-j ACCEPT"},
	} {
		c := f.Chain(chain)
		if c == nil {
			t.Errorf("%s missing", chain)
			continue
		}
		var got []string
		for _, r := range c.Rules {
			_, rest, _ := strings.Cut(r.Text, chain+" ")
			got = append(got, rest)
		}
		if !slices.Equal(got, want) {
			t.Errorf("%s = %q, want %q", chain, got, want)
		}
	}
	for chain, want := range map[string]string{"INPUT": "DROP", "OUTPUT": "DROP", "FORWARD": "ACCEPT"} {
		if p := f.Chain(chain).Policy; p != want {
			t.Errorf("%s policy %s, want %s", chain, p, want)
		}
	}
}

// TestTranslateOtherTable checks that a nat block in before.rules loads
// into the nat table, and that a missing after.rules is an error.
func TestTranslateOtherTable(t *testing.T) {
	cfg := predictConfig(t, "debian-13", "rules")
	cfg.Files = maps.Clone(cfg.Files)
	e := cfg.Files["/etc/ufw/before.rules"]
	e.Data = append([]byte("*nat\n:POSTROUTING ACCEPT [0:0]\n-A POSTROUTING -s 10.8.0.0/24 -o eth0 -j MASQUERADE\nCOMMIT\n"), e.Data...)
	cfg.Files["/etc/ufw/before.rules"] = e
	rs, err := translate(cfg, false)
	if err != nil {
		t.Fatal(err)
	}
	nat := rs.Table("nat")
	if nat == nil || nat.Chain("POSTROUTING") == nil || len(nat.Chain("POSTROUTING").Rules) != 1 {
		t.Fatalf("nat table %+v", nat)
	}
	cfg.Files["/etc/ufw/after.rules"] = core.RestoreEntry{Path: "/etc/ufw/after.rules"}
	if _, err := translate(cfg, false); err == nil || !strings.HasPrefix(err.Error(), "predict: ") {
		t.Errorf("missing after.rules: err = %v", err)
	}
}

// TestTranslateLoadsTheFiles checks that the user chains come from the
// bytes on disk: the package's template user6.rules, which a first "ufw
// enable" loads, has no rate-limit rules, so the chains ufw_start declares
// for them stay empty; and a missing user6.rules is an error.
func TestTranslateLoadsTheFiles(t *testing.T) {
	cfg := predictConfig(t, "debian-13", "fresh")
	cfg.Files = maps.Clone(cfg.Files)
	rs, err := translate(cfg, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"ufw6-user-limit", "ufw6-user-limit-accept", "ufw6-user-input"} {
		if c := rs.Table("filter").Chain(name); c == nil || len(c.Rules) != 0 {
			t.Errorf("%s = %+v, want an empty chain", name, c)
		}
	}
	if c := rs.Table("filter").Chain("ufw-user-limit"); c != nil {
		t.Errorf("an IPv6 translation has %s", c.Name)
	}
	rs, err = translate(cfg, false)
	if err != nil {
		t.Fatal(err)
	}
	if c := rs.Table("filter").Chain("ufw-user-limit"); c == nil || len(c.Rules) != 2 {
		t.Errorf("ufw-user-limit = %+v, want the template's two rules", c)
	}
	cfg.Files[userRules6] = core.RestoreEntry{Path: userRules6}
	if _, err := translate(cfg, true); err == nil || !strings.HasPrefix(err.Error(), "predict: ") {
		t.Errorf("missing user6.rules: err = %v", err)
	}
}
