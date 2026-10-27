package predict

import (
	"errors"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/platform/netfilter"
)

// refusalTestState returns a golden scenario's loaded state with extra
// IPv6 filter chains (written as iptables -S prints them) and extra rules
// appended to built-in chains.
func refusalTestState(t *testing.T, scenario string, chains map[string][]string, rules map[string]string) fwstate.Loaded {
	t.Helper()
	st := predictLoaded(t, "debian-13", scenario)
	f := st.V6.Table("filter")
	for name, rs := range chains {
		f.Chains = append(f.Chains, *predictTestChain(t, "filter", name, "-", rs...))
	}
	for chain, rule := range rules {
		c := f.Chain(chain)
		c.Rules = append(c.Rules, predictTestChain(t, "filter", chain, "ACCEPT", rule).Rules...)
	}
	return st
}

func TestIPv6Refusal(t *testing.T) {
	noV6 := predictConfig(t, "debian-13", "ipv6no")
	withV6 := predictConfig(t, "debian-13", "rules")
	disabled := noV6
	disabled.Enabled = false
	docker := refusalTestState(t, "rules", map[string][]string{"DOCKER-USER": {"-A DOCKER-USER -j RETURN"}}, nil)
	tailscale := refusalTestState(t, "ipv6no", nil, map[string]string{"INPUT": "-A INPUT -i tailscale0 -j ACCEPT"})
	matchedJump := refusalTestState(t, "rules", nil, map[string]string{"FORWARD": "-A FORWARD -i eth0 -j ufw6-before-forward"})
	noIP6 := predictLoaded(t, "debian-13", "rules")
	noIP6.V6 = nil
	for _, tc := range []struct {
		name string
		cfg  fwstate.Config
		cur  fwstate.Loaded
		tr   fwstate.Transition
		want []string // substrings of the detail; nil for no refusal
	}{
		{"IPv6 on", withV6, docker, fwstate.TransEnable, nil},
		{"no ip6tables", noV6, noIP6, fwstate.TransEnable, nil},
		{"disable", noV6, docker, fwstate.TransDisable, nil},
		{"none", noV6, docker, fwstate.TransNone, nil},
		{"reload while disabled", disabled, docker, fwstate.TransReload, nil},
		{"fresh host", noV6, predictLoaded(t, "debian-13", "fresh"), fwstate.TransEnable, nil},
		{"UFW's IPv6 chains", noV6, predictLoaded(t, "debian-13", "rules"), fwstate.TransReload, nil},
		{"UFW's plain table", noV6, predictLoaded(t, "debian-13", "ipv6no"), fwstate.TransReload, nil},
		{"Docker chain", noV6, docker, fwstate.TransEnable, []string{"chain DOCKER-USER", "IPV6=yes"}},
		{"tailscale rule", noV6, tailscale, fwstate.TransReload, []string{"INPUT rule 2 (-A INPUT -i tailscale0 -j ACCEPT)"}},
		{"jump with a match", noV6, matchedJump, fwstate.TransEnable, []string{"FORWARD rule 7 (-A FORWARD -i eth0 -j ufw6-before-forward)"}},
	} {
		err := IPv6Refusal(tc.cfg, tc.cur, tc.tr)
		if tc.want == nil {
			if err != nil {
				t.Errorf("%s: %v", tc.name, err)
			}
			continue
		}
		var ce *errcode.Error
		if !errors.As(err, &ce) || ce.Code != errcode.FirewallUnsupportedConfiguration {
			t.Errorf("%s: err = %v, want %s", tc.name, err, errcode.FirewallUnsupportedConfiguration)
			continue
		}
		for _, w := range tc.want {
			if !strings.Contains(ce.Detail, w) {
				t.Errorf("%s: detail %q lacks %q", tc.name, ce.Detail, w)
			}
		}
	}
}

// TestIPv6RefusalUFWItems checks the items UFW owns in the built-in
// chains against the keys netfilter gives the rules UFW writes.
func TestIPv6RefusalUFWItems(t *testing.T) {
	rs, err := netfilter.ParseRestore(strings.NewReader("*filter\n:ufw6-before-input - [0:0]\n" +
		"-A INPUT -j ufw6-before-input\n-A INPUT -i lo -j ACCEPT\n-A OUTPUT -o lo -j ACCEPT\n-A OUTPUT -i lo -j ACCEPT\nCOMMIT\n"))
	if err != nil {
		t.Fatal(err)
	}
	f := rs.Table("filter")
	for _, tc := range []struct {
		chain string
		i     int
		want  bool
	}{{"INPUT", 0, true}, {"INPUT", 1, true}, {"OUTPUT", 0, true}, {"OUTPUT", 1, false}} {
		if got := ufwBuiltinRule(tc.chain, f.Chain(tc.chain).Rules[tc.i]); got != tc.want {
			t.Errorf("%s rule %d: %v, want %v", tc.chain, tc.i+1, got, tc.want)
		}
	}
}
