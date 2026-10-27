package loaded

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/platform/netfilter"
	"github.com/darkraise/darkpanel/internal/platform/netstat"
)

const fhLegacyWarning = "# Warning: iptables-legacy tables present, use iptables-legacy-save to see them\n"

// fhReader is ldReader with nf_tables loaded or not.
func fhReader(nftLoaded bool) Reader {
	return Reader{
		Interfaces:     func() ([]netstat.Interface, error) { return nil, nil },
		NFTablesLoaded: func() bool { return nftLoaded },
	}
}

// fhProcNet is a proc/net directory listing legacy tables.
func fhProcNet(t *testing.T, v4, v6 string) string {
	t.Helper()
	dir := t.TempDir()
	for name, list := range map[string]string{"ip_tables_names": v4, "ip6_tables_names": v6} {
		if list == "" {
			continue
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(list), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// fhEmpty is the save output of an empty table.
func fhEmpty(table string) string {
	chains := map[string][]string{
		"filter":   {"INPUT", "FORWARD", "OUTPUT"},
		"nat":      {"PREROUTING", "INPUT", "OUTPUT", "POSTROUTING"},
		"raw":      {"PREROUTING", "OUTPUT"},
		"mangle":   {"PREROUTING", "INPUT", "FORWARD", "OUTPUT", "POSTROUTING"},
		"security": {"INPUT", "FORWARD", "OUTPUT"},
	}[table]
	var b strings.Builder
	b.WriteString("*" + table + "\n")
	for _, c := range chains {
		b.WriteString(":" + c + " ACCEPT [0:0]\n")
	}
	b.WriteString("COMMIT\n")
	return b.String()
}

func fhRead(t *testing.T, x *ldRunner, nftLoaded bool, procNet string) []string {
	t.Helper()
	st, err := fhReader(nftLoaded).read(context.Background(), ldEnv(x, nil), procNet)
	if err != nil {
		t.Fatal(err)
	}
	return st.Unsupported
}

func fhWant(t *testing.T, got, want []string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Errorf("unsupported:\n got %q\nwant %q", got, want)
	}
}

// iptables-nft-save 1.8.7 prints its legacy warning on standard output,
// 1.8.9 and later on standard error.
func TestForeignLegacyRules(t *testing.T) {
	for _, onStderr := range []bool{false, true} {
		x := ldGolden(t, "debian-12", "rules")
		nat := x.answers["/usr/sbin/iptables-nft-save -t nat"]
		if onStderr {
			nat.stderr = fhLegacyWarning
		} else {
			nat.out = fhLegacyWarning + nat.out
		}
		x.answers["/usr/sbin/iptables-nft-save -t nat"] = nat
		x.answers["/usr/sbin/iptables-legacy-save -t filter"] = ldResult{out: ldLegacyFilter}
		x.answers["/usr/sbin/iptables-legacy-save -t raw"] = ldResult{out: strings.Replace(ldLegacyRaw, ":PREROUTING ACCEPT", ":PREROUTING DROP", 1)}
		got := fhRead(t, x, false, fhProcNet(t, "filter\nraw\n", "filter\n"))
		fhWant(t, got, []string{
			"iptables-legacy table filter chain INPUT holds 1 rule(s) outside the nft variant UFW uses",
			"iptables-legacy table raw chain PREROUTING has policy DROP outside the nft variant UFW uses",
		})
	}
}

func TestForeignLegacyOnlyWhenWarned(t *testing.T) {
	x := ldGolden(t, "debian-12", "rules")
	fhWant(t, fhRead(t, x, false, fhProcNet(t, "filter\n", "filter\n")), nil)
	for _, c := range x.calls {
		if strings.Contains(c, "legacy") {
			t.Errorf("ran %q without iptables-nft-save's warning", c)
		}
	}
}

func TestForeignNFTUnderLegacy(t *testing.T) {
	x := &ldRunner{answers: map[string]ldResult{
		"/usr/sbin/iptables --version":             {out: "iptables v1.8.9 (legacy)\n"},
		"/usr/sbin/ip6tables --version":            {out: "ip6tables v1.8.9 (legacy)\n"},
		"/usr/sbin/iptables-legacy-save -t filter": {out: ldLegacyFilter},
		"/usr/sbin/nft list tables":                {out: "table ip filter\ntable ip nat\ntable ip6 filter\n"},
	}}
	for _, tool := range []string{"iptables", "ip6tables"} {
		for _, table := range netfilter.Tables() {
			x.answers["/usr/sbin/"+tool+"-nft-save -t "+table] = ldResult{out: fhEmpty(table)}
		}
	}
	x.answers["/usr/sbin/iptables-nft-save -t nat"] = ldResult{out: strings.Replace(fhEmpty("nat"), "COMMIT",
		"-A PREROUTING -p tcp -m tcp --dport 80 -j DNAT --to-destination 192.0.2.9:8080\nCOMMIT", 1)}
	x.answers["/usr/sbin/ip6tables-nft-save -t filter"] = ldResult{out: strings.Replace(fhEmpty("filter"), ":FORWARD ACCEPT", ":FORWARD DROP", 1)}
	got := fhRead(t, x, true, fhProcNet(t, "filter\n", ""))
	fhWant(t, got, []string{
		"iptables-nft table nat chain PREROUTING holds 1 rule(s) outside the legacy variant UFW uses",
		"ip6tables-nft table filter chain FORWARD has policy DROP outside the legacy variant UFW uses",
	})
	if slices.ContainsFunc(x.calls, func(c string) bool { return strings.HasPrefix(c, "/usr/sbin/nft list table ") }) {
		t.Errorf("listed a table the save tools read: %q", x.calls)
	}
}

func TestForeignNFTSkippedUnderLegacyWithoutModule(t *testing.T) {
	x := &ldRunner{answers: map[string]ldResult{
		"/usr/sbin/iptables --version":  {out: "iptables v1.8.9 (legacy)\n"},
		"/usr/sbin/ip6tables --version": {out: "ip6tables v1.8.9 (legacy)\n"},
	}}
	fhWant(t, fhRead(t, x, false, t.TempDir()), nil)
	if len(x.calls) != 2 {
		t.Errorf("commands %q; want only the version checks", x.calls)
	}
}

func TestForeignNative(t *testing.T) {
	x := ldGolden(t, "debian-12", "rules")
	x.answers["/usr/sbin/nft list tables"] = ldResult{out: string(nftTestRead(t, "tables.txt"))}
	for _, name := range []string{"inet dpforeign", "netdev dpnetdev", "ip dpnat", "ip6 dpsetonly"} {
		x.answers["/usr/sbin/nft list table "+name] = ldResult{out: string(nftTestRead(t, strings.ReplaceAll(name, " ", "-")+".txt"))}
	}
	fhWant(t, fhRead(t, x, true, t.TempDir()), []string{
		`nft table inet dpforeign: base chain "dropper" on hook "forward" has 0 rule(s) and policy drop`,
		`nft table inet dpforeign: base chain "ruled" on hook "input" has 2 rule(s) and policy accept`,
		`nft table ip dpnat: base chain "post" on hook "postrouting" has 1 rule(s) and policy accept`,
	})
}

func TestForeignNativeWithoutIP6tables(t *testing.T) {
	x := ldGolden(t, "debian-12", "rules")
	x.answers["/usr/sbin/ip6tables --version"] = ldResult{err: ldMissing("/usr/sbin/ip6tables")}
	x.answers["/usr/sbin/nft list tables"] = ldResult{out: "table ip filter\ntable ip6 filter\n"}
	x.answers["/usr/sbin/nft list table ip6 filter"] = ldResult{out: "table ip6 filter {\n\tchain INPUT {\n\t\ttype filter hook input priority filter; policy drop;\n\t}\n}\n"}
	fhWant(t, fhRead(t, x, true, t.TempDir()), []string{
		`nft table ip6 filter: base chain "INPUT" on hook "input" has 0 rule(s) and policy drop`,
	})
}

func TestForeignNativeNames(t *testing.T) {
	x := ldGolden(t, "debian-12", "rules")
	x.answers["/usr/sbin/nft list tables"] = ldResult{out: "table ip -dash\ntable foo bar\ntable ip a/b\ntable ip a b\ntable inet ok\n"}
	x.answers["/usr/sbin/nft list table inet ok"] = ldResult{out: "table inet ok {\n\tchain c {\n\t\ttype filter hook input priority 0;\n\t}\n}\n"}
	fhWant(t, fhRead(t, x, true, t.TempDir()), []string{
		`nft list tables printed "table ip a b", which Darkpanel cannot read`,
		`nft table "-dash" in family "ip": table name not readable safely`,
		`nft table "bar" in family "foo": table name not readable safely`,
		`nft table "a/b" in family "ip": table name not readable safely`,
		`nft table inet ok: base chain "c" on hook "input" has 0 rule(s) and policy (none printed)`,
	})
}

// What nft prints reaches the operator quoted and cut short, so a control
// character or an overlong line cannot garble the reason.
func TestForeignNativeQuoted(t *testing.T) {
	long := strings.Repeat("x", 300)
	x := ldGolden(t, "debian-12", "rules")
	x.answers["/usr/sbin/nft list tables"] = ldResult{out: "table ip " + long + " extra\n" +
		"table\x1b[2J ip x\ntable ip a\x01b\ntable i\x01p ok\ntable ip " + long + "\n"}
	fhWant(t, fhRead(t, x, true, t.TempDir()), []string{
		`nft list tables printed "table ip ` + strings.Repeat("x", 191) + `...", which Darkpanel cannot read`,
		`nft list tables printed "table\x1b[2J ip x", which Darkpanel cannot read`,
		`nft table "a\x01b" in family "ip": table name not readable safely`,
		`nft table "ok" in family "i\x01p": table name not readable safely`,
		`nft table "` + strings.Repeat("x", 200) + `..." in family "ip": table name not readable safely`,
	})
	if slices.ContainsFunc(x.calls, func(c string) bool { return strings.HasPrefix(c, "/usr/sbin/nft list table ") }) {
		t.Errorf("passed an unsafe table name to nft: %q", x.calls)
	}
}

func TestForeignNativeUnreadable(t *testing.T) {
	x := ldGolden(t, "debian-12", "rules")
	x.answers["/usr/sbin/nft list tables"] = ldResult{out: "table inet odd\n"}
	x.answers["/usr/sbin/nft list table inet odd"] = ldResult{out: "table inet odd {\n\tchain c {\n"}
	fhWant(t, fhRead(t, x, true, t.TempDir()), []string{
		"nft table inet odd: loaded: nft output: the table block is missing or not closed",
	})
}

func TestForeignNFTMissing(t *testing.T) {
	x := ldGolden(t, "debian-12", "rules")
	x.answers["/usr/sbin/nft list tables"] = ldResult{err: ldMissing("/usr/sbin/nft")}
	fhWant(t, fhRead(t, x, true, t.TempDir()), []string{"install the nftables package so Darkpanel can read every rule"})
}

func TestForeignErrors(t *testing.T) {
	cases := []struct {
		name    string
		answers map[string]ldResult
		want    string
	}{
		{"nft list tables fails", map[string]ldResult{"/usr/sbin/nft list tables": {code: 1, stderr: "netlink: Error: cache initialization failed\n"}},
			"loaded: /usr/sbin/nft list tables exited with status 1: netlink: Error: cache initialization failed"},
		{"nft list table fails", map[string]ldResult{
			"/usr/sbin/nft list tables":          {out: "table inet gone\n"},
			"/usr/sbin/nft list table inet gone": {code: 1, stderr: "Error: No such file or directory\n"},
		}, "loaded: /usr/sbin/nft list table inet gone exited with status 1: Error: No such file or directory"},
		{"legacy save fails", map[string]ldResult{
			"/usr/sbin/iptables-nft-save -t filter":    {out: fhLegacyWarning + fhEmpty("filter")},
			"/usr/sbin/iptables-legacy-save -t filter": {code: 1, stderr: "iptables-legacy-save: permission denied\n"},
		}, "loaded: /usr/sbin/iptables-legacy-save -t filter exited with status 1: iptables-legacy-save: permission denied"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			x := ldGolden(t, "debian-12", "rules")
			for k, v := range c.answers {
				x.answers[k] = v
			}
			_, err := fhReader(true).read(context.Background(), ldEnv(x, nil), fhProcNet(t, "filter\n", ""))
			if err == nil || err.Error() != c.want {
				t.Fatalf("err = %v, want %q", err, c.want)
			}
		})
	}
}

func TestOtherVariant(t *testing.T) {
	if otherVariant("nft") != "legacy" || otherVariant("legacy") != "nft" {
		t.Error("otherVariant does not swap the variants")
	}
}
