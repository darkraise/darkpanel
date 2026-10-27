package predict

import (
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/platform/netfilter"
)

// TestPredictGolden predicts each captured transition from the loaded
// state before it and the configured state UFW starts from, and compares
// every chain of every table of both families with the capture. UFW
// starts from the files on disk before its command rewrites them; they
// equal the files after it except for the first enable, which loads the
// package's templates (the fresh files).
func TestPredictGolden(t *testing.T) {
	for _, img := range predictImages {
		for _, tc := range []struct {
			from, cfg, to string
			tr            fwstate.Transition
		}{
			{"fresh", "fresh", "enabled", fwstate.TransEnable},
			{"fresh", "rules", "rules", fwstate.TransEnable},
			{"rules", "disabled", "disabled", fwstate.TransDisable},
			{"fresh", "ipv6no", "ipv6no", fwstate.TransEnable},
			{"rules", "logging", "logging", fwstate.TransReload},
			{"disabled", "rules", "rules", fwstate.TransEnable},
			{"logging", "rules", "rules", fwstate.TransReload},
			{"rules", "rules", "rules", fwstate.TransNone},
			{"fresh", "fresh", "fresh", fwstate.TransDisable},
		} {
			cur := predictLoaded(t, img, tc.from)
			before := predictLoadedClone(cur)
			got, err := Predict(predictConfig(t, img, tc.cfg), cur, tc.tr)
			if err != nil {
				t.Fatalf("%s %s->%s: %v", img, tc.from, tc.to, err)
			}
			want := predictLoaded(t, img, tc.to)
			for _, d := range predictDiff(got.V4, want.V4, false) {
				t.Errorf("%s %s->%s v4: %s", img, tc.from, tc.to, d)
			}
			for _, d := range predictDiff(got.V6, want.V6, true) {
				t.Errorf("%s %s->%s v6: %s", img, tc.from, tc.to, d)
			}
			if got.Variant != cur.Variant || !maps.Equal(got.Sysctl, cur.Sysctl) {
				t.Errorf("%s %s->%s: variant or sysctl not copied from the current state", img, tc.from, tc.to)
			}
			if !reflect.DeepEqual(cur, before) {
				t.Errorf("%s %s->%s: Predict modified the current state", img, tc.from, tc.to)
			}
		}
	}
}

func predictLoadedClone(st fwstate.Loaded) fwstate.Loaded {
	st.V4, st.V6, st.Sysctl = cloneRuleset(st.V4), cloneRuleset(st.V6), maps.Clone(st.Sysctl)
	return st
}

// predictTestChain parses one chain written as iptables -S prints it.
func predictTestChain(t *testing.T, table, chain, policy string, rules ...string) *netfilter.Chain {
	t.Helper()
	decl := ":" + chain + " " + policy + " [0:0]\n"
	for _, r := range rules {
		if _, target, ok := strings.Cut(r, " -j "); ok && strings.HasPrefix(target, "ufw") {
			decl += ":" + target + " - [0:0]\n"
		}
	}
	rs, err := netfilter.Parse(strings.NewReader("*" + table + "\n" + decl + strings.Join(rules, "\n") + "\nCOMMIT\n"))
	if err != nil {
		t.Fatal(err)
	}
	return rs.Tables[0].Chain(chain)
}

func predictTestSame(t *testing.T, step string, got, want *netfilter.Chain) {
	t.Helper()
	if got == nil || got.Policy != want.Policy || !slices.Equal(predictKeys(got, false), predictKeys(want, false)) {
		t.Errorf("%s: %s is %+v, want policy %s and\n  %s", step, want.Name, got, want.Policy, strings.Join(predictKeys(want, false), "\n  "))
	}
}

// TestPredictNewTable checks that a table before.rules writes to is added
// when the host has none, without losing the live logging step, which
// fills the logging chains on a first enable from the templates.
func TestPredictNewTable(t *testing.T) {
	cfg := predictConfig(t, "debian-13", "fresh")
	cfg.Files = maps.Clone(cfg.Files)
	e := cfg.Files["/etc/ufw/before.rules"]
	e.Data = append([]byte("*nat\n:POSTROUTING ACCEPT [0:0]\n-A POSTROUTING -s 10.8.0.0/24 -o eth0 -j MASQUERADE\nCOMMIT\n"), e.Data...)
	cfg.Files["/etc/ufw/before.rules"] = e
	cur := predictLoaded(t, "debian-13", "fresh")
	cur.V4.Tables = slices.DeleteFunc(cur.V4.Tables, func(t netfilter.Table) bool { return t.Name != "filter" })
	cur.V4.Tables = slices.Clip(cur.V4.Tables)
	got, err := Predict(cfg, cur, fwstate.TransEnable)
	if err != nil {
		t.Fatal(err)
	}
	if nat := got.V4.Table("nat"); nat == nil || len(nat.Chain("POSTROUTING").Rules) != 1 {
		t.Errorf("nat table %+v", nat)
	}
	want := predictLoaded(t, "debian-13", "enabled").V4
	for _, d := range predictDiff(&netfilter.Ruleset{Tables: []netfilter.Table{*got.V4.Table("filter")}}, &netfilter.Ruleset{Tables: []netfilter.Table{*want.Table("filter")}}, false) {
		t.Errorf("filter: %s", d)
	}
}

// TestPredictForeignRules follows what ufw 0.36.2-9 did on debian:13 with
// a nat block prepended to before.rules and a foreign INPUT rule: the
// first start appends UFW's jumps after the foreign rule, every start
// appends the nat rule again, disable keeps both, and a rule appended
// while UFW is off stays after UFW's jumps.
func TestPredictForeignRules(t *testing.T) {
	cfg := predictConfig(t, "debian-13", "rules")
	cfg.Files = maps.Clone(cfg.Files)
	e := cfg.Files["/etc/ufw/before.rules"]
	e.Data = append([]byte("*nat\n:POSTROUTING ACCEPT [0:0]\n-A POSTROUTING -s 10.8.0.0/24 -o eth0 -j MASQUERADE\nCOMMIT\n"), e.Data...)
	cfg.Files["/etc/ufw/before.rules"] = e
	foreign1, foreign2 := "-A INPUT -s 198.51.100.1/32 -j ACCEPT", "-A INPUT -s 198.51.100.2/32 -j ACCEPT"
	ufwJumps := []string{"-A INPUT -j ufw-before-logging-input", "-A INPUT -j ufw-before-input", "-A INPUT -j ufw-after-input",
		"-A INPUT -j ufw-after-logging-input", "-A INPUT -j ufw-reject-input", "-A INPUT -j ufw-track-input"}
	masq := "-A POSTROUTING -s 10.8.0.0/24 -o eth0 -j MASQUERADE"

	cur := predictLoaded(t, "debian-13", "fresh")
	in := cur.V4.Table("filter").Chain("INPUT")
	in.Rules = append(in.Rules, predictTestChain(t, "filter", "INPUT", "ACCEPT", foreign1).Rules...)

	st, err := Predict(cfg, cur, fwstate.TransEnable)
	if err != nil {
		t.Fatal(err)
	}
	predictTestSame(t, "enable", st.V4.Table("filter").Chain("INPUT"), predictTestChain(t, "filter", "INPUT", "DROP", append([]string{foreign1}, ufwJumps...)...))
	predictTestSame(t, "enable", st.V4.Table("nat").Chain("POSTROUTING"), predictTestChain(t, "nat", "POSTROUTING", "ACCEPT", masq))

	if st, err = Predict(cfg, st, fwstate.TransReload); err != nil {
		t.Fatal(err)
	}
	predictTestSame(t, "reload", st.V4.Table("filter").Chain("INPUT"), predictTestChain(t, "filter", "INPUT", "DROP", append([]string{foreign1}, ufwJumps...)...))
	predictTestSame(t, "reload", st.V4.Table("nat").Chain("POSTROUTING"), predictTestChain(t, "nat", "POSTROUTING", "ACCEPT", masq, masq))

	if st, err = Predict(cfg, st, fwstate.TransDisable); err != nil {
		t.Fatal(err)
	}
	predictTestSame(t, "disable", st.V4.Table("filter").Chain("INPUT"), predictTestChain(t, "filter", "INPUT", "ACCEPT", append([]string{foreign1}, ufwJumps...)...))
	predictTestSame(t, "disable", st.V4.Table("nat").Chain("POSTROUTING"), predictTestChain(t, "nat", "POSTROUTING", "ACCEPT", masq, masq))

	in = st.V4.Table("filter").Chain("INPUT")
	in.Rules = append(in.Rules, predictTestChain(t, "filter", "INPUT", "ACCEPT", foreign2).Rules...)
	if st, err = Predict(cfg, st, fwstate.TransEnable); err != nil {
		t.Fatal(err)
	}
	predictTestSame(t, "enable again", st.V4.Table("filter").Chain("INPUT"),
		predictTestChain(t, "filter", "INPUT", "DROP", append(append([]string{foreign1}, ufwJumps...), foreign2)...))
	predictTestSame(t, "enable again", st.V4.Table("nat").Chain("POSTROUTING"), predictTestChain(t, "nat", "POSTROUTING", "ACCEPT", masq, masq, masq))
}

// TestPredictIPv6Off checks that starting UFW with IPv6 off replaces the
// whole IPv6 filter table, a foreign chain included, and leaves the other
// IPv6 tables alone.
func TestPredictIPv6Off(t *testing.T) {
	cur := predictLoaded(t, "debian-13", "rules")
	f := cur.V6.Table("filter")
	f.Chains = append(f.Chains, *predictTestChain(t, "filter", "DOCKER-USER", "-", "-A DOCKER-USER -j RETURN"))
	nat := cur.V6.Table("nat")
	nat.Chain("POSTROUTING").Rules = predictTestChain(t, "nat", "POSTROUTING", "ACCEPT", "-A POSTROUTING -s fd00::/64 -j MASQUERADE").Rules
	cfg := predictConfig(t, "debian-13", "ipv6no")
	got, err := Predict(cfg, cur, fwstate.TransReload)
	if err != nil {
		t.Fatal(err)
	}
	want := predictLoaded(t, "debian-13", "ipv6no").V6
	for _, d := range predictDiff(&netfilter.Ruleset{Tables: []netfilter.Table{*got.V6.Table("filter")}}, &netfilter.Ruleset{Tables: []netfilter.Table{*want.Table("filter")}}, true) {
		t.Errorf("v6 filter: %s", d)
	}
	if n := len(got.V6.Table("nat").Chain("POSTROUTING").Rules); n != 1 {
		t.Errorf("v6 nat POSTROUTING has %d rules, want the foreign one kept", n)
	}
	cur.V6 = nil
	if got, err = Predict(cfg, cur, fwstate.TransReload); err != nil || got.V6 != nil {
		t.Errorf("without ip6tables: V6 %v, err %v", got.V6, err)
	}
}

func TestPredictRefuses(t *testing.T) {
	rules := predictLoaded(t, "debian-13", "rules")
	cfg := predictConfig(t, "debian-13", "rules")
	unsupported := cfg
	unsupported.Unsupported = []string{"MANAGE_BUILTINS=yes"}
	for name, tc := range map[string]struct {
		cfg fwstate.Config
		cur fwstate.Loaded
		tr  fwstate.Transition
	}{
		"enable while running":    {cfg, rules, fwstate.TransEnable},
		"unknown transition":      {cfg, rules, "restart"},
		"unsupported reload":      {unsupported, rules, fwstate.TransReload},
		"no IPv4 ruleset":         {cfg, fwstate.Loaded{}, fwstate.TransNone},
		"unsupported first start": {unsupported, predictLoaded(t, "debian-13", "fresh"), fwstate.TransEnable},
	} {
		if _, err := Predict(tc.cfg, tc.cur, tc.tr); err == nil || !strings.HasPrefix(err.Error(), "predict: ") {
			t.Errorf("%s: err = %v, want a predict error", name, err)
		}
	}
	disabled := cfg
	disabled.Enabled = false
	got, err := Predict(disabled, rules, fwstate.TransReload)
	if err != nil {
		t.Fatal(err)
	}
	if d := predictDiff(got.V4, rules.V4, false); len(d) > 0 {
		t.Errorf("reload of a disabled UFW changed the state: %v", d)
	}
}

// TestPredictFirstEnableLogging checks the live logging step after a first
// "ufw --force enable" from the package's templates, against what ufw
// 0.36.2-9 loaded on debian:13 with LOGLEVEL set to off and to medium
// beforehand (the whole filter tables matched; these chains are the ones
// the step decides).
func TestPredictFirstEnableLogging(t *testing.T) {
	for _, tc := range []struct {
		level string
		want  map[string][]string
	}{
		{"off", map[string][]string{
			"ufw-user-limit":          {"-A ufw-user-limit -j REJECT --reject-with icmp-port-unreachable"},
			"ufw-user-logging-input":  {"-A ufw-user-logging-input -j RETURN"},
			"ufw6-user-logging-input": {"-A ufw6-user-logging-input -j RETURN"},
			"ufw6-user-limit":         nil,
			"ufw6-logging-deny":       nil,
		}},
		{"medium", map[string][]string{
			"ufw-user-limit": {"-A ufw-user-limit -m limit --limit 3/min -j LOG --log-prefix \"[UFW LIMIT BLOCK] \"",
				"-A ufw-user-limit -j REJECT --reject-with icmp-port-unreachable"},
			"ufw6-before-logging-input": {"-A ufw6-before-logging-input -m conntrack --ctstate NEW -m limit --limit 3/min --limit-burst 10 -j LOG --log-prefix \"[UFW AUDIT] \""},
			"ufw6-logging-deny": {"-A ufw6-logging-deny -m conntrack --ctstate INVALID -m limit --limit 3/min --limit-burst 10 -j LOG --log-prefix \"[UFW AUDIT INVALID] \"",
				"-A ufw6-logging-deny -m limit --limit 3/min --limit-burst 10 -j LOG --log-prefix \"[UFW BLOCK] \""},
			"ufw6-user-limit":         {"-A ufw6-user-limit -m limit --limit 3/min -j LOG --log-prefix \"[UFW LIMIT BLOCK] \""},
			"ufw6-user-logging-input": nil,
		}},
	} {
		cfg := predictConfig(t, "debian-13", "fresh")
		cfg.LogLevel = tc.level
		got, err := Predict(cfg, predictLoaded(t, "debian-13", "fresh"), fwstate.TransEnable)
		if err != nil {
			t.Fatal(err)
		}
		for chain, rules := range tc.want {
			v6 := strings.HasPrefix(chain, "ufw6-")
			rs := got.V4
			if v6 {
				rs = got.V6
			}
			want := predictKeys(predictTestChain(t, "filter", chain, "-", rules...), v6)
			if have := predictKeys(rs.Table("filter").Chain(chain), v6); !slices.Equal(have, want) {
				t.Errorf("%s %s:\n  %s\nwant\n  %s", tc.level, chain, strings.Join(have, "\n  "), strings.Join(want, "\n  "))
			}
		}
	}
}
