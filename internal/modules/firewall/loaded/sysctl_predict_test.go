package loaded

import (
	"errors"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/firewall/conf"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
)

const spGoldenDir = "../testdata/golden"

var spImages = []string{"ubuntu-22.04", "ubuntu-24.04", "debian-12", "debian-13"}

// spValues reads a golden sysctl.txt of key=value lines.
func spValues(t *testing.T, path string) map[string]string {
	t.Helper()
	data, err := os.ReadFile(path) //nolint:gosec // G304: a fixed name under the golden directory
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for line := range strings.Lines(string(data)) {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			t.Fatalf("%s: line %q", path, line)
		}
		out[k] = v
	}
	return out
}

func TestPredictSysctlGolden(t *testing.T) {
	for _, img := range spImages {
		cfg, err := conf.Read(filepath.Join(spGoldenDir, img, "sysctl", "files"))
		if err != nil {
			t.Fatal(err)
		}
		before := spValues(t, filepath.Join(spGoldenDir, img, "rules", "sysctl.txt"))
		after := spValues(t, filepath.Join(spGoldenDir, img, "sysctl", "sysctl.txt"))
		if maps.Equal(before, after) {
			t.Fatalf("%s: the scenario changes nothing", img)
		}
		for _, tr := range []fwstate.Transition{fwstate.TransEnable, fwstate.TransReload} {
			got, err := PredictSysctl(cfg, before, tr)
			if err != nil {
				t.Fatalf("%s %s: %v", img, tr, err)
			}
			if !maps.Equal(got, after) {
				t.Errorf("%s %s:\n got %v\nwant %v", img, tr, got, after)
			}
		}
		for _, tr := range []fwstate.Transition{fwstate.TransNone, fwstate.TransDisable} {
			got, err := PredictSysctl(cfg, before, tr)
			if err != nil || !maps.Equal(got, before) {
				t.Errorf("%s %s: %v, %v; want the values unchanged", img, tr, got, err)
			}
		}
		cfg.Enabled = false
		if got, err := PredictSysctl(cfg, before, fwstate.TransReload); err != nil || !maps.Equal(got, before) {
			t.Errorf("%s: reload while disabled changed %v, %v", img, got, err)
		}
	}
}

// spCur is a host with one interface, eth0, and the loopback interface.
func spCur() map[string]string {
	cur := map[string]string{"net/ipv4/ip_forward": "0"}
	for _, d := range []string{"all", "default", "eth0", "lo"} {
		cur["net/ipv6/conf/"+d+"/forwarding"] = "0"
		cur["net/ipv6/conf/"+d+"/accept_ra"] = "1"
		cur["net/ipv4/conf/"+d+"/rp_filter"] = "2"
		cur["net/ipv4/conf/"+d+"/accept_source_route"] = "0"
	}
	return cur
}

func spConfig(lines ...string) fwstate.Config {
	cfg := fwstate.Config{Enabled: true, SysctlFile: "/etc/ufw/sysctl.conf"}
	for _, l := range lines {
		k, v, _ := strings.Cut(l, "=")
		cfg.Sysctl = append(cfg.Sysctl, fwstate.SysctlSetting{Key: k, Value: v})
	}
	return cfg
}

func TestPredictSysctlEffects(t *testing.T) {
	cases := []struct {
		name   string
		lines  []string
		before map[string]string // changes to spCur before the transition
		want   map[string]string // changes to spCur expected after it
	}{
		{"ipv4 forwarding resets no supported key", []string{"net/ipv4/ip_forward=1"},
			map[string]string{"net/ipv4/conf/eth0/rp_filter": "1", "net/ipv4/conf/eth0/accept_source_route": "1"},
			map[string]string{"net/ipv4/ip_forward": "1", "net/ipv4/conf/eth0/rp_filter": "1", "net/ipv4/conf/eth0/accept_source_route": "1"}},
		{"ipv6 all forwarding sets every interface", []string{"net/ipv6/conf/all/forwarding=1"}, nil,
			map[string]string{"net/ipv6/conf/all/forwarding": "1", "net/ipv6/conf/default/forwarding": "1",
				"net/ipv6/conf/eth0/forwarding": "1", "net/ipv6/conf/lo/forwarding": "1"}},
		{"ipv6 all forwarding resets an interface without a change", []string{"net/ipv6/conf/all/forwarding=0"},
			map[string]string{"net/ipv6/conf/eth0/forwarding": "1"}, map[string]string{"net/ipv6/conf/eth0/forwarding": "0"}},
		{"file order", []string{"net/ipv6/conf/all/forwarding=1", "net/ipv6/conf/eth0/forwarding=0"}, nil,
			map[string]string{"net/ipv6/conf/all/forwarding": "1", "net/ipv6/conf/default/forwarding": "1",
				"net/ipv6/conf/eth0/forwarding": "0", "net/ipv6/conf/lo/forwarding": "1"}},
		{"ipv6 defaults do not spread", []string{"net/ipv6/conf/default/forwarding=1", "net/ipv6/conf/default/accept_ra=0", "net/ipv6/conf/all/accept_ra=2"}, nil,
			map[string]string{"net/ipv6/conf/default/forwarding": "1", "net/ipv6/conf/default/accept_ra": "0", "net/ipv6/conf/all/accept_ra": "2"}},
		{"missing keys and other keys are skipped", []string{"net/ipv6/conf/eth9/forwarding=1", "net/ipv4/conf/all/accept_redirects=0", "net/ipv4/icmp_echo_ignore_all=1"}, nil, nil},
		{"ipv4 all values do not spread", []string{"net/ipv4/conf/all/rp_filter=1", "net/ipv4/conf/all/accept_source_route=1"}, nil,
			map[string]string{"net/ipv4/conf/all/rp_filter": "1", "net/ipv4/conf/all/accept_source_route": "1"}},
		{"ipv4 default leaves explicit interfaces", []string{"net/ipv4/conf/default/rp_filter=1"},
			map[string]string{"net/ipv4/conf/eth0/rp_filter": "0", "net/ipv4/conf/lo/rp_filter": "1"},
			map[string]string{"net/ipv4/conf/default/rp_filter": "1", "net/ipv4/conf/eth0/rp_filter": "0", "net/ipv4/conf/lo/rp_filter": "1"}},
		{"ipv4 default after the interfaces", []string{"net/ipv4/conf/eth0/accept_source_route=0", "net/ipv4/conf/lo/accept_source_route=0", "net/ipv4/conf/default/accept_source_route=1"}, nil,
			map[string]string{"net/ipv4/conf/default/accept_source_route": "1"}},
		{"ipv4 default unchanged", []string{"net/ipv4/conf/default/rp_filter=2"}, nil, nil},
	}
	for _, c := range cases {
		cur := spCur()
		maps.Copy(cur, c.before)
		want, kept := maps.Clone(cur), maps.Clone(cur)
		maps.Copy(want, c.want)
		got, err := PredictSysctl(spConfig(c.lines...), cur, fwstate.TransEnable)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if !maps.Equal(got, want) {
			t.Errorf("%s:\n got %v\nwant %v", c.name, got, want)
		}
		if !maps.Equal(cur, kept) {
			t.Errorf("%s: the current values were changed", c.name)
		}
	}
}

func TestPredictSysctlRefusals(t *testing.T) {
	cases := map[string]string{
		"net/ipv4/ip_forward=2":            `/etc/ufw/sysctl.conf sets net/ipv4/ip_forward to "2"; Darkpanel supports only values matching ^[01]$`,
		"net/ipv6/conf/eth0/accept_ra=3":   `/etc/ufw/sysctl.conf sets net/ipv6/conf/eth0/accept_ra to "3"; Darkpanel supports only values matching ^[012]$`,
		"net/ipv6/conf/eth9/forwarding=on": `/etc/ufw/sysctl.conf sets net/ipv6/conf/eth9/forwarding to "on"; Darkpanel supports only values matching ^[01]$`,
		"net/ipv4/conf/default/rp_filter=1": "/etc/ufw/sysctl.conf sets net/ipv4/conf/default/rp_filter to 1, which the kernel may also copy to " +
			"net/ipv4/conf/eth0/rp_filter; Darkpanel cannot predict that, so set the interface's own value earlier in the file",
	}
	for line, want := range cases {
		_, err := PredictSysctl(spConfig(line), spCur(), fwstate.TransReload)
		var ce *errcode.Error
		if !errors.As(err, &ce) || ce.Code != errcode.FirewallUnsupportedConfiguration || ce.Detail != want {
			t.Errorf("%s: err = %v, want %q", line, err, want)
		}
	}
}

func TestPredictSysctlNilValues(t *testing.T) {
	got, err := PredictSysctl(spConfig("net/ipv4/ip_forward=1"), nil, fwstate.TransEnable)
	if err != nil || got == nil || len(got) != 0 {
		t.Errorf("got %v, %v; want an empty map", got, err)
	}
}
