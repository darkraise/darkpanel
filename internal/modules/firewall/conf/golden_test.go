package conf

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/modules/firewall/model"
)

// The golden captures testdata/capture.sh writes, one directory per image
// and scenario.
const goldenDir = "../testdata/golden"

var (
	goldenImages    = []string{"ubuntu-22.04", "ubuntu-24.04", "debian-12", "debian-13"}
	goldenScenarios = []string{"fresh", "enabled", "rules", "disabled", "ipv6no", "logging", "manual", "sysctl"}
	goldenVersionRe = regexp.MustCompile(`^iptables v1\.8\.[0-9]+ \(nf_tables\)\nufw 0\.36\.[0-9][^\n]*\n$`)
)

func TestGoldenLayout(t *testing.T) {
	var want []string
	for _, f := range fwstate.StateFiles {
		want = append(want, filepath.Join("files", f))
	}
	want = append(want, "files/etc/ufw/sysctl.conf", "version.txt", "nft-tables.txt", "sysctl.txt", "commands.txt")
	for _, fam := range []string{"v4", "v6"} {
		for _, table := range []string{"filter", "nat", "raw", "mangle", "security"} {
			want = append(want, "save/nft-"+fam+"-"+table+".txt")
		}
	}
	for _, img := range goldenImages {
		for _, sc := range goldenScenarios {
			dir := filepath.Join(goldenDir, img, sc)
			for _, rel := range want {
				fi, err := os.Stat(filepath.Join(dir, rel))
				if err != nil {
					t.Errorf("%s/%s: %v", img, sc, err)
					continue
				}
				if fi.Size() == 0 && rel != "commands.txt" && rel != "nft-tables.txt" {
					t.Errorf("%s/%s/%s is empty", img, sc, rel)
				}
			}
			version, err := os.ReadFile(filepath.Join(dir, "version.txt")) //nolint:gosec // G304: a fixed name under the golden directory
			if err == nil && !goldenVersionRe.Match(version) {
				t.Errorf("%s/%s/version.txt = %q", img, sc, version)
			}
		}
	}
}

// goldenRulesScenario is what model.Merge makes of the "rules" scenario's
// files; manual rules are compared without their derived ids.
var goldenRulesScenario = []model.Rule{
	{ID: "01J00000000000000000000000", Action: model.ActionAllow, Direction: model.DirIn, Proto: model.ProtoTCP, From: model.Any,
		To: model.Any, ToPort: "22", Family: model.FamilyBoth, Log: model.LogOff, Comment: "ssh", Origin: model.OriginDarkpanel},
	{Action: model.ActionAllow, Direction: model.DirRoute, Proto: model.ProtoTCP, From: model.Any, To: "172.17.0.2", ToPort: "80",
		OutIface: "eth0", Family: model.FamilyV4, Log: model.LogOff, Origin: model.OriginManual},
	{Action: model.ActionLimit, Direction: model.DirIn, Proto: model.ProtoTCP, From: model.Any, To: model.Any, ToPort: "2222",
		Family: model.FamilyBoth, Log: model.LogOff, Origin: model.OriginManual},
	{Action: model.ActionReject, Direction: model.DirIn, Proto: model.ProtoAny, From: model.Any, To: model.Any, ToPort: "3000",
		Family: model.FamilyBoth, Log: model.LogOff, Origin: model.OriginManual},
	{Action: model.ActionAllow, Direction: model.DirIn, Proto: model.ProtoTCP, From: "10.0.0.0/8", To: model.Any,
		ToPort: "443,8000:8100", Family: model.FamilyV4, Log: model.LogOn, Origin: model.OriginManual},
	{Action: model.ActionAllow, Direction: model.DirOut, Proto: model.ProtoAny, From: model.Any, To: model.Any, ToPort: "53",
		OutIface: "eth0", Family: model.FamilyBoth, Log: model.LogOff, Origin: model.OriginManual},
	{Action: model.ActionAllow, Direction: model.DirIn, Proto: model.ProtoESP, From: model.Any, To: model.Any, InIface: "eth0",
		Family: model.FamilyBoth, Log: model.LogOff, Origin: model.OriginManual},
	{Action: model.ActionDeny, Direction: model.DirIn, Proto: model.ProtoUDP, From: "2001:db8::/32", To: model.Any,
		ToPort: "5000:5100", Family: model.FamilyV6, Log: model.LogOff, Origin: model.OriginManual},
}

// goldenSameRules compares rules, ignoring manual rules' derived ids.
func goldenSameRules(t *testing.T, where string, got, want []model.Rule) {
	t.Helper()
	if len(got) < len(want) {
		t.Errorf("%s: %d rules, want at least %d", where, len(got), len(want))
		return
	}
	for i, w := range want {
		g := got[i]
		if w.Origin == model.OriginManual {
			if !strings.HasPrefix(g.ID, "manual-") {
				t.Errorf("%s: rule %d id = %q", where, i, g.ID)
			}
			g.ID = ""
		}
		if g != w {
			t.Errorf("%s: rule %d\n got %+v\nwant %+v", where, i, g, w)
		}
	}
}

func TestGoldenRead(t *testing.T) {
	for _, img := range goldenImages {
		for _, sc := range goldenScenarios {
			where := img + "/" + sc
			c, err := Read(filepath.Join(goldenDir, img, sc, "files"))
			if err != nil {
				t.Errorf("%s: %v", where, err)
				continue
			}
			if len(c.Unsupported) != 0 {
				t.Errorf("%s: Unsupported = %q", where, c.Unsupported)
			}
			for p, e := range c.Files {
				if !e.Present {
					t.Errorf("%s: %s is absent", where, p)
				}
			}
			rules := model.Merge(c.Rules4, c.Rules6)
			wantEnabled := sc != "fresh" && sc != "disabled"
			if c.Enabled != wantEnabled || c.IPv6 != (sc != "ipv6no") || c.SysctlFile != "/etc/ufw/sysctl.conf" ||
				c.DefaultInput != "DROP" || c.DefaultOutput != "ACCEPT" || c.DefaultForward != "DROP" {
				t.Errorf("%s: settings = enabled %v, ipv6 %v, sysctl %q, policies %s/%s/%s", where, c.Enabled, c.IPv6,
					c.SysctlFile, c.DefaultInput, c.DefaultOutput, c.DefaultForward)
			}
			wantLog := "low"
			if sc == "logging" {
				wantLog = "high"
			}
			if c.LogLevel != wantLog {
				t.Errorf("%s: LogLevel = %q, want %q", where, c.LogLevel, wantLog)
			}
			switch sc {
			case "fresh", "enabled":
				if len(rules) != 0 {
					t.Errorf("%s: %d rules, want none", where, len(rules))
				}
			case "rules", "disabled", "logging", "sysctl":
				if len(rules) != len(goldenRulesScenario) {
					t.Errorf("%s: %d rules, want %d", where, len(rules), len(goldenRulesScenario))
				}
				goldenSameRules(t, where, rules, goldenRulesScenario)
			case "ipv6no":
				if len(rules) != 1 || len(c.Rules6) != 0 || rules[0].Family != model.FamilyV4 || rules[0].ToPort != "22" {
					t.Errorf("%s: rules = %+v", where, rules)
				}
			case "manual":
				goldenManual(t, where, rules)
			}
			if sc == "sysctl" {
				want := []fwstate.SysctlSetting{{Key: "net/ipv4/ip_forward", Value: "1"}, {Key: "net/ipv6/conf/default/forwarding", Value: "1"}}
				if len(c.Sysctl) < 2 || !slices.Equal(c.Sysctl[len(c.Sysctl)-2:], want) {
					t.Errorf("%s: Sysctl = %q, want it to end with %q", where, c.Sysctl, want)
				}
			}
		}
	}
}

// goldenManual checks the "manual" scenario: the rule set, then an
// application rule and a manual comment rule in both files, then the IPv6
// rule with host bits UFW keeps as written.
func goldenManual(t *testing.T, where string, rules []model.Rule) {
	t.Helper()
	if len(rules) != 11 {
		t.Errorf("%s: %d rules, want 11", where, len(rules))
		return
	}
	goldenSameRules(t, where, rules[:7], goldenRulesScenario[:7])
	if app := rules[7]; app.Raw == "" || app.Family != model.FamilyBoth || !strings.Contains(app.Raw, "DP%20Web") {
		t.Errorf("%s: application rule = %+v", where, app)
	}
	if hand := rules[8]; hand.Raw != "" || hand.Comment != "hand rule" || hand.Origin != model.OriginManual ||
		hand.Family != model.FamilyBoth || hand.ToPort != "8080" {
		t.Errorf("%s: hand rule = %+v", where, hand)
	}
	goldenSameRules(t, where, rules[9:10], goldenRulesScenario[7:])
	if host := rules[10]; host.Raw == "" || host.Family != model.FamilyV6 || !strings.Contains(host.Raw, "2001:db8::1/32") {
		t.Errorf("%s: host-bits rule = %+v", where, host)
	}
}
