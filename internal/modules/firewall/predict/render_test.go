package predict

import (
	"bytes"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/modules/firewall/conf"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/modules/firewall/model"
)

// predictGolden is the golden tree testdata/capture.sh writes (spec 2.3).
const predictGolden = "../testdata/golden"

var predictImages = []string{"ubuntu-22.04", "ubuntu-24.04", "debian-12", "debian-13"}

// predictConfig reads one golden scenario's configured state.
func predictConfig(t *testing.T, image, scenario string) fwstate.Config {
	t.Helper()
	cfg, err := conf.Read(filepath.Join(predictGolden, image, scenario, "files"))
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

// TestFilesGolden renders every scenario UFW has written its files in (all
// but fresh, whose files are the package's templates) and compares the
// bytes with the capture.
func TestFilesGolden(t *testing.T) {
	for _, img := range predictImages {
		for _, sc := range []string{"enabled", "rules", "disabled", "ipv6no", "logging", "manual", "sysctl"} {
			cfg := predictConfig(t, img, sc)
			got, err := Files(cfg)
			if err != nil {
				t.Fatalf("%s/%s: %v", img, sc, err)
			}
			if len(got) != 4 {
				t.Errorf("%s/%s: Files returned %d files, want 4", img, sc, len(got))
			}
			for path, data := range got {
				want, err := os.ReadFile(filepath.Join(predictGolden, img, sc, "files", path)) //nolint:gosec // G304: a state file under the golden directory
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(data, want) {
					t.Errorf("%s/%s %s differs:\n--- got\n%s--- want\n%s", img, sc, path, data, want)
				}
			}
		}
	}
}

// filesTestLevels are the logging sections ufw 0.36.2-6 wrote on
// ubuntu:24.04 for each log level, with the rule "allow in log proto tcp to
// any port 443" and the policies given.
var filesTestLevels = []struct {
	name, level            string
	v6                     bool
	input, output, forward string
	want                   string
}{
	{"off user.rules", "off", false, "DROP", "ACCEPT", "DROP", `### LOGGING ###
-I ufw-user-logging-input -j RETURN
-I ufw-user-logging-output -j RETURN
-I ufw-user-logging-forward -j RETURN
### END LOGGING ###

### RATE LIMITING ###
-A ufw-user-limit -j REJECT
-A ufw-user-limit-accept -j ACCEPT
### END RATE LIMITING ###
COMMIT
`},
	{"low user.rules", "low", false, "DROP", "ACCEPT", "DROP", `### LOGGING ###
-A ufw-after-logging-input -j LOG --log-prefix "[UFW BLOCK] " -m limit --limit 3/min --limit-burst 10
-A ufw-after-logging-forward -j LOG --log-prefix "[UFW BLOCK] " -m limit --limit 3/min --limit-burst 10
-I ufw-logging-deny -m conntrack --ctstate INVALID -j RETURN -m limit --limit 3/min --limit-burst 10
-A ufw-logging-deny -j LOG --log-prefix "[UFW BLOCK] " -m limit --limit 3/min --limit-burst 10
-A ufw-logging-allow -j LOG --log-prefix "[UFW ALLOW] " -m limit --limit 3/min --limit-burst 10
### END LOGGING ###

### RATE LIMITING ###
-A ufw-user-limit -m limit --limit 3/minute -j LOG --log-prefix "[UFW LIMIT BLOCK] "
-A ufw-user-limit -j REJECT
-A ufw-user-limit-accept -j ACCEPT
### END RATE LIMITING ###
COMMIT
`},
	{"medium user.rules", "medium", false, "DROP", "ACCEPT", "DROP", `### LOGGING ###
-A ufw-after-logging-input -j LOG --log-prefix "[UFW BLOCK] " -m limit --limit 3/min --limit-burst 10
-A ufw-after-logging-output -j LOG --log-prefix "[UFW ALLOW] " -m limit --limit 3/min --limit-burst 10
-A ufw-after-logging-forward -j LOG --log-prefix "[UFW BLOCK] " -m limit --limit 3/min --limit-burst 10
-A ufw-logging-deny -m conntrack --ctstate INVALID -j LOG --log-prefix "[UFW AUDIT INVALID] " -m limit --limit 3/min --limit-burst 10
-A ufw-logging-deny -j LOG --log-prefix "[UFW BLOCK] " -m limit --limit 3/min --limit-burst 10
-A ufw-logging-allow -j LOG --log-prefix "[UFW ALLOW] " -m limit --limit 3/min --limit-burst 10
-I ufw-before-logging-input -j LOG --log-prefix "[UFW AUDIT] " -m conntrack --ctstate NEW -m limit --limit 3/min --limit-burst 10
-I ufw-before-logging-output -j LOG --log-prefix "[UFW AUDIT] " -m conntrack --ctstate NEW -m limit --limit 3/min --limit-burst 10
-I ufw-before-logging-forward -j LOG --log-prefix "[UFW AUDIT] " -m conntrack --ctstate NEW -m limit --limit 3/min --limit-burst 10
### END LOGGING ###

### RATE LIMITING ###
-A ufw-user-limit -m limit --limit 3/minute -j LOG --log-prefix "[UFW LIMIT BLOCK] "
-A ufw-user-limit -j REJECT
-A ufw-user-limit-accept -j ACCEPT
### END RATE LIMITING ###
COMMIT
`},
	{"high user.rules", "high", false, "DROP", "ACCEPT", "DROP", `### LOGGING ###
-A ufw-after-logging-input -j LOG --log-prefix "[UFW BLOCK] "
-A ufw-after-logging-output -j LOG --log-prefix "[UFW ALLOW] "
-A ufw-after-logging-forward -j LOG --log-prefix "[UFW BLOCK] "
-A ufw-logging-deny -m conntrack --ctstate INVALID -j LOG --log-prefix "[UFW AUDIT INVALID] "
-A ufw-logging-deny -j LOG --log-prefix "[UFW BLOCK] "
-A ufw-logging-allow -j LOG --log-prefix "[UFW ALLOW] "
-I ufw-before-logging-input -j LOG --log-prefix "[UFW AUDIT] " -m limit --limit 3/min --limit-burst 10
-I ufw-before-logging-output -j LOG --log-prefix "[UFW AUDIT] " -m limit --limit 3/min --limit-burst 10
-I ufw-before-logging-forward -j LOG --log-prefix "[UFW AUDIT] " -m limit --limit 3/min --limit-burst 10
### END LOGGING ###

### RATE LIMITING ###
-A ufw-user-limit -m limit --limit 3/minute -j LOG --log-prefix "[UFW LIMIT BLOCK] "
-A ufw-user-limit -j REJECT
-A ufw-user-limit-accept -j ACCEPT
### END RATE LIMITING ###
COMMIT
`},
	{"full user.rules", "full", false, "DROP", "ACCEPT", "DROP", `### LOGGING ###
-A ufw-after-logging-input -j LOG --log-prefix "[UFW BLOCK] "
-A ufw-after-logging-output -j LOG --log-prefix "[UFW ALLOW] "
-A ufw-after-logging-forward -j LOG --log-prefix "[UFW BLOCK] "
-A ufw-logging-deny -m conntrack --ctstate INVALID -j LOG --log-prefix "[UFW AUDIT INVALID] "
-A ufw-logging-deny -j LOG --log-prefix "[UFW BLOCK] "
-A ufw-logging-allow -j LOG --log-prefix "[UFW ALLOW] "
-I ufw-before-logging-input -j LOG --log-prefix "[UFW AUDIT] "
-I ufw-before-logging-output -j LOG --log-prefix "[UFW AUDIT] "
-I ufw-before-logging-forward -j LOG --log-prefix "[UFW AUDIT] "
### END LOGGING ###

### RATE LIMITING ###
-A ufw-user-limit -m limit --limit 3/minute -j LOG --log-prefix "[UFW LIMIT BLOCK] "
-A ufw-user-limit -j REJECT
-A ufw-user-limit-accept -j ACCEPT
### END RATE LIMITING ###
COMMIT
`},
	{"off user6.rules", "off", true, "DROP", "ACCEPT", "DROP", `### LOGGING ###
-I ufw6-user-logging-input -j RETURN
-I ufw6-user-logging-output -j RETURN
-I ufw6-user-logging-forward -j RETURN
### END LOGGING ###

### RATE LIMITING ###
-A ufw6-user-limit -j REJECT
-A ufw6-user-limit-accept -j ACCEPT
### END RATE LIMITING ###
COMMIT
`},
	{"medium user6.rules", "medium", true, "DROP", "ACCEPT", "DROP", `### LOGGING ###
-A ufw6-after-logging-input -j LOG --log-prefix "[UFW BLOCK] " -m limit --limit 3/min --limit-burst 10
-A ufw6-after-logging-output -j LOG --log-prefix "[UFW ALLOW] " -m limit --limit 3/min --limit-burst 10
-A ufw6-after-logging-forward -j LOG --log-prefix "[UFW BLOCK] " -m limit --limit 3/min --limit-burst 10
-A ufw6-logging-deny -m conntrack --ctstate INVALID -j LOG --log-prefix "[UFW AUDIT INVALID] " -m limit --limit 3/min --limit-burst 10
-A ufw6-logging-deny -j LOG --log-prefix "[UFW BLOCK] " -m limit --limit 3/min --limit-burst 10
-A ufw6-logging-allow -j LOG --log-prefix "[UFW ALLOW] " -m limit --limit 3/min --limit-burst 10
-I ufw6-before-logging-input -j LOG --log-prefix "[UFW AUDIT] " -m conntrack --ctstate NEW -m limit --limit 3/min --limit-burst 10
-I ufw6-before-logging-output -j LOG --log-prefix "[UFW AUDIT] " -m conntrack --ctstate NEW -m limit --limit 3/min --limit-burst 10
-I ufw6-before-logging-forward -j LOG --log-prefix "[UFW AUDIT] " -m conntrack --ctstate NEW -m limit --limit 3/min --limit-burst 10
### END LOGGING ###

### RATE LIMITING ###
-A ufw6-user-limit -m limit --limit 3/minute -j LOG --log-prefix "[UFW LIMIT BLOCK] "
-A ufw6-user-limit -j REJECT
-A ufw6-user-limit-accept -j ACCEPT
### END RATE LIMITING ###
COMMIT
`},
	{"reject-allow low user.rules", "low", false, "REJECT", "ACCEPT", "ACCEPT", `### LOGGING ###
-A ufw-after-logging-input -j LOG --log-prefix "[UFW BLOCK] " -m limit --limit 3/min --limit-burst 10
-I ufw-logging-deny -m conntrack --ctstate INVALID -j RETURN -m limit --limit 3/min --limit-burst 10
-A ufw-logging-deny -j LOG --log-prefix "[UFW BLOCK] " -m limit --limit 3/min --limit-burst 10
-A ufw-logging-allow -j LOG --log-prefix "[UFW ALLOW] " -m limit --limit 3/min --limit-burst 10
### END LOGGING ###

### RATE LIMITING ###
-A ufw-user-limit -m limit --limit 3/minute -j LOG --log-prefix "[UFW LIMIT BLOCK] "
-A ufw-user-limit -j REJECT
-A ufw-user-limit-accept -j ACCEPT
### END RATE LIMITING ###
COMMIT
`},
	{"reject-allow medium user.rules", "medium", false, "REJECT", "ACCEPT", "ACCEPT", `### LOGGING ###
-A ufw-after-logging-input -j LOG --log-prefix "[UFW BLOCK] " -m limit --limit 3/min --limit-burst 10
-A ufw-after-logging-output -j LOG --log-prefix "[UFW ALLOW] " -m limit --limit 3/min --limit-burst 10
-A ufw-after-logging-forward -j LOG --log-prefix "[UFW ALLOW] " -m limit --limit 3/min --limit-burst 10
-A ufw-logging-deny -m conntrack --ctstate INVALID -j LOG --log-prefix "[UFW AUDIT INVALID] " -m limit --limit 3/min --limit-burst 10
-A ufw-logging-deny -j LOG --log-prefix "[UFW BLOCK] " -m limit --limit 3/min --limit-burst 10
-A ufw-logging-allow -j LOG --log-prefix "[UFW ALLOW] " -m limit --limit 3/min --limit-burst 10
-I ufw-before-logging-input -j LOG --log-prefix "[UFW AUDIT] " -m conntrack --ctstate NEW -m limit --limit 3/min --limit-burst 10
-I ufw-before-logging-output -j LOG --log-prefix "[UFW AUDIT] " -m conntrack --ctstate NEW -m limit --limit 3/min --limit-burst 10
-I ufw-before-logging-forward -j LOG --log-prefix "[UFW AUDIT] " -m conntrack --ctstate NEW -m limit --limit 3/min --limit-burst 10
### END LOGGING ###

### RATE LIMITING ###
-A ufw-user-limit -m limit --limit 3/minute -j LOG --log-prefix "[UFW LIMIT BLOCK] "
-A ufw-user-limit -j REJECT
-A ufw-user-limit-accept -j ACCEPT
### END RATE LIMITING ###
COMMIT
`},
}

func TestFilesLogLevels(t *testing.T) {
	base := predictConfig(t, "ubuntu-24.04", "enabled")
	for _, tc := range filesTestLevels {
		cfg := base
		cfg.LogLevel, cfg.DefaultInput, cfg.DefaultOutput, cfg.DefaultForward = tc.level, tc.input, tc.output, tc.forward
		got, err := Files(cfg)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		path := userRules
		if tc.v6 {
			path = userRules6
		}
		_, section, ok := strings.Cut(string(got[path]), "\n### LOGGING ###\n")
		if !ok || "### LOGGING ###\n"+section != tc.want {
			t.Errorf("%s: logging section\n%s\nwant\n%s", tc.name, "### LOGGING ###\n"+section, tc.want)
		}
	}
}

// TestFilesRuleLines renders tuples the golden scenarios lack and compares
// the rule section with what ufw 0.36.2-6 wrote on ubuntu:24.04 for
// "allow proto tcp from 10.0.0.0/8 port 1000:2000 to any port 80,443",
// "deny out proto udp from any port 53", "reject in log-all to 192.0.2.1
// port 25", "limit in log proto tcp to any port 22", "route deny in on eth0
// out on eth1" and "allow in on eth0 log-all from 2001:db8::/32 to any port
// 8080:8090 proto udp".
func TestFilesRuleLines(t *testing.T) {
	cfg := predictConfig(t, "ubuntu-24.04", "enabled")
	cfg.Rules4, cfg.Rules6 = filesTestTuples(t, filesTestRules4, false), filesTestTuples(t, filesTestRules6, true)
	got, err := Files(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{userRules: filesTestRules4, userRules6: filesTestRules6} {
		if !strings.Contains(string(got[path]), "\n"+want) {
			t.Errorf("%s rendered\n%s\nwant the section\n%s", path, got[path], want)
		}
	}
}

func filesTestTuples(t *testing.T, section string, v6 bool) []model.Tuple {
	t.Helper()
	var out []model.Tuple
	for _, line := range strings.Split(section, "\n") {
		if strings.HasPrefix(line, model.TuplePrefix) {
			tp, err := model.ParseTuple(line, v6)
			if err != nil {
				t.Fatal(err)
			}
			out = append(out, tp)
		}
	}
	return out
}

const filesTestRules4 = `### RULES ###

### tuple ### allow tcp 80,443 0.0.0.0/0 1000:2000 10.0.0.0/8 in
-A ufw-user-input -p tcp -m multiport --dports 80,443 -m multiport --sports 1000:2000 -s 10.0.0.0/8 -j ACCEPT

### tuple ### deny udp any 0.0.0.0/0 53 0.0.0.0/0 out
-A ufw-user-output -p udp --sport 53 -j DROP

### tuple ### reject_log-all any 25 192.0.2.1 any 0.0.0.0/0 in
-A ufw-user-logging-input -p tcp -d 192.0.2.1 --dport 25 -m limit --limit 3/min --limit-burst 10 -j LOG --log-prefix "[UFW BLOCK] "
-A ufw-user-logging-input -p tcp -d 192.0.2.1 --dport 25 -j RETURN
-A ufw-user-input -p tcp -d 192.0.2.1 --dport 25 -j ufw-user-logging-input
-A ufw-user-input -p tcp -d 192.0.2.1 --dport 25 -j REJECT --reject-with tcp-reset
-A ufw-user-logging-input -p udp -d 192.0.2.1 --dport 25 -m limit --limit 3/min --limit-burst 10 -j LOG --log-prefix "[UFW BLOCK] "
-A ufw-user-logging-input -p udp -d 192.0.2.1 --dport 25 -j RETURN
-A ufw-user-input -p udp -d 192.0.2.1 --dport 25 -j ufw-user-logging-input
-A ufw-user-input -p udp -d 192.0.2.1 --dport 25 -j REJECT

### tuple ### limit_log tcp 22 0.0.0.0/0 any 0.0.0.0/0 in
-A ufw-user-logging-input -p tcp --dport 22 -m conntrack --ctstate NEW -m limit --limit 3/min --limit-burst 10 -j LOG --log-prefix "[UFW LIMIT] "
-A ufw-user-logging-input -p tcp --dport 22 -j RETURN
-A ufw-user-input -p tcp --dport 22 -j ufw-user-logging-input
-A ufw-user-input -p tcp --dport 22 -m conntrack --ctstate NEW -m recent --set
-A ufw-user-input -p tcp --dport 22 -m conntrack --ctstate NEW -m recent --update --seconds 30 --hitcount 6 -j ufw-user-limit
-A ufw-user-input -p tcp --dport 22 -j ufw-user-limit-accept

### tuple ### route:deny any any 0.0.0.0/0 any 0.0.0.0/0 in_eth0!out_eth1
-A ufw-user-forward -i eth0 -o eth1 -j DROP

### END RULES ###
`

const filesTestRules6 = `### RULES ###

### tuple ### deny udp any ::/0 53 ::/0 out
-A ufw6-user-output -p udp --sport 53 -j DROP

### tuple ### limit_log tcp 22 ::/0 any ::/0 in
-A ufw6-user-logging-input -p tcp --dport 22 -m conntrack --ctstate NEW -m limit --limit 3/min --limit-burst 10 -j LOG --log-prefix "[UFW LIMIT] "
-A ufw6-user-logging-input -p tcp --dport 22 -j RETURN
-A ufw6-user-input -p tcp --dport 22 -j ufw6-user-logging-input
-A ufw6-user-input -p tcp --dport 22 -m conntrack --ctstate NEW -m recent --set
-A ufw6-user-input -p tcp --dport 22 -m conntrack --ctstate NEW -m recent --update --seconds 30 --hitcount 6 -j ufw6-user-limit
-A ufw6-user-input -p tcp --dport 22 -j ufw6-user-limit-accept

### tuple ### route:deny any any ::/0 any ::/0 in_eth0!out_eth1
-A ufw6-user-forward -i eth0 -o eth1 -j DROP

### tuple ### allow_log-all udp 8080:8090 ::/0 any 2001:db8::/32 in_eth0
-A ufw6-user-logging-input -i eth0 -p udp -m multiport --dports 8080:8090 -s 2001:db8::/32 -m limit --limit 3/min --limit-burst 10 -j LOG --log-prefix "[UFW ALLOW] "
-A ufw6-user-logging-input -i eth0 -p udp -m multiport --dports 8080:8090 -s 2001:db8::/32 -j RETURN
-A ufw6-user-input -i eth0 -p udp -m multiport --dports 8080:8090 -s 2001:db8::/32 -j ufw6-user-logging-input
-A ufw6-user-input -i eth0 -p udp -m multiport --dports 8080:8090 -s 2001:db8::/32 -j ACCEPT

### END RULES ###
`

// TestFilesConfKeys checks that ufw.conf and /etc/default/ufw change only
// in the keys whose value UFW would read differently, written as
// set_default writes them, and that a missing key is appended.
func TestFilesConfKeys(t *testing.T) {
	cfg := predictConfig(t, "debian-13", "enabled")
	cfg.Files = maps.Clone(cfg.Files)
	conf := cfg.Files[ufwConf]
	conf.Data = []byte("# comment\nENABLED=YES\nLOGLEVEL=low")
	cfg.Files[ufwConf] = conf
	defs := cfg.Files[defaultsUFW]
	defs.Data = []byte("IPV6=yes\nDEFAULT_INPUT_POLICY=\"DROP\"\nDEFAULT_OUTPUT_POLICY=ACCEPT\n")
	cfg.Files[defaultsUFW] = defs
	cfg.Enabled, cfg.LogLevel = true, "low"
	cfg.DefaultInput, cfg.DefaultOutput, cfg.DefaultForward = "DROP", "ACCEPT", "DROP"
	got, err := Files(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if string(got[ufwConf]) != "# comment\nENABLED=YES\nLOGLEVEL=low" {
		t.Errorf("unchanged ufw.conf = %q", got[ufwConf])
	}
	if want := "IPV6=yes\nDEFAULT_INPUT_POLICY=\"DROP\"\nDEFAULT_OUTPUT_POLICY=ACCEPT\nDEFAULT_FORWARD_POLICY=\"DROP\"\n"; string(got[defaultsUFW]) != want {
		t.Errorf("/etc/default/ufw = %q, want %q", got[defaultsUFW], want)
	}
	cfg.Enabled, cfg.LogLevel, cfg.DefaultOutput = false, "full", "REJECT"
	if got, err = Files(cfg); err != nil {
		t.Fatal(err)
	}
	if want := "# comment\nENABLED=no\nLOGLEVEL=full\n"; string(got[ufwConf]) != want {
		t.Errorf("ufw.conf = %q, want %q", got[ufwConf], want)
	}
	if want := "IPV6=yes\nDEFAULT_INPUT_POLICY=\"DROP\"\nDEFAULT_OUTPUT_POLICY=\"REJECT\"\nDEFAULT_FORWARD_POLICY=\"DROP\"\n"; string(got[defaultsUFW]) != want {
		t.Errorf("/etc/default/ufw = %q, want %q", got[defaultsUFW], want)
	}
}

func TestFilesRefuses(t *testing.T) {
	base := predictConfig(t, "debian-13", "enabled")
	for name, edit := range map[string]func(*fwstate.Config){
		"log level": func(c *fwstate.Config) { c.LogLevel = "loud" },
		"policy":    func(c *fwstate.Config) { c.DefaultForward = "drop" },
	} {
		cfg := base
		edit(&cfg)
		if _, err := Files(cfg); err == nil || !strings.HasPrefix(err.Error(), "predict: ") {
			t.Errorf("%s: err = %v, want a predict error", name, err)
		}
	}
}
