package eval

import (
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
)

// docker28Save reads one family's capture from testdata/docker28.
func docker28Save(t *testing.T, fam fwstate.Family) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "docker28", string(fam)+".save")) //nolint:gosec // G304: the path is built from constants of this test.
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// docker28Route routes the capture's default bridge, 172.18.0.0/16, out of
// docker0, and this host's 192.0.2.10 locally.
func docker28Route(p fwstate.Packet) (string, string, error) {
	switch {
	case netip.MustParsePrefix("172.18.0.0/16").Contains(p.Dst) && p.Path == "output":
		return "output", "docker0", nil
	case netip.MustParsePrefix("172.18.0.0/16").Contains(p.Dst):
		return "forward", "docker0", nil
	case p.Dst == netip.MustParseAddr("192.0.2.10"):
		return "input", "", nil
	}
	return "", "", errors.New("no route")
}

func TestDocker28(t *testing.T) {
	v4 := docker28Save(t, fwstate.V4)
	if !strings.Contains(v4, "--to-destination 172.18.0.2:80") {
		t.Fatal("the capture does not publish 172.18.0.2:80; rerun capture.sh")
	}
	userDrop := strings.Replace(v4, "-A FORWARD -j DOCKER-USER\n",
		"-A FORWARD -j DOCKER-USER\n-A DOCKER-USER -s 203.0.113.0/24 -j DROP\n", 1)
	published := func(p *fwstate.Packet) { p.Dst, p.DstPort = netip.MustParseAddr("192.0.2.10"), 8080 }
	tests := []struct {
		name    string
		save    string
		edit    func(p *fwstate.Packet)
		verdict fwstate.Verdict
		trace   []string
		reason  string
	}{
		{
			name:    "a published port is reachable from outside through DNAT and DOCKER-USER",
			save:    v4,
			edit:    published,
			verdict: fwstate.Accept,
			trace: []string{"nat/PREROUTING#1", "nat/DOCKER#2", "filter/FORWARD#1", "filter/FORWARD#2",
				"filter/DOCKER-FORWARD#1", "filter/DOCKER-FORWARD#2", "filter/DOCKER-FORWARD#3",
				"filter/DOCKER-BRIDGE#1", "filter/DOCKER#1"},
		},
		{
			name:    "a DOCKER-USER rule decides before Docker's own chains",
			save:    userDrop,
			edit:    published,
			verdict: fwstate.Drop,
			trace:   []string{"nat/PREROUTING#1", "nat/DOCKER#2", "filter/FORWARD#1", "filter/DOCKER-USER#1"},
			reason:  "filter/DOCKER-USER#1 drops",
		},
		{
			name: "direct access to the container from outside is dropped in raw",
			save: v4,
			edit: func(p *fwstate.Packet) {
				p.Path, p.OutIface, p.Dst, p.DstPort = "forward", "docker0", netip.MustParseAddr("172.18.0.2"), 80
			},
			verdict: fwstate.Drop,
			trace:   []string{"raw/PREROUTING#1"},
		},
		{
			name: "the container reaches the internet, masqueraded",
			save: v4,
			edit: func(p *fwstate.Packet) {
				p.Path, p.InIface, p.OutIface = "forward", "docker0", "eth0"
				p.Src, p.Dst, p.DstPort = netip.MustParseAddr("172.18.0.2"), netip.MustParseAddr("198.51.100.2"), 443
			},
			verdict: fwstate.Accept,
			trace: []string{"filter/FORWARD#1", "filter/FORWARD#2", "filter/DOCKER-FORWARD#1",
				"filter/DOCKER-FORWARD#2", "filter/DOCKER-ISOLATION-STAGE-1#1", "filter/DOCKER-FORWARD#3",
				"filter/DOCKER-FORWARD#4", "nat/POSTROUTING#1"},
		},
		{
			name: "a local connection to the published port is DNATed in nat OUTPUT",
			save: v4,
			edit: func(p *fwstate.Packet) {
				p.Path, p.InIface, p.OutIface = "output", "", "lo"
				p.Src, p.Dst, p.DstPort = netip.MustParseAddr("192.0.2.10"), netip.MustParseAddr("192.0.2.10"), 8080
			},
			verdict: fwstate.Accept,
			trace:   []string{"nat/OUTPUT#1", "nat/DOCKER#2"},
		},
		{
			name: "an IPv6 connection to the published port reaches the host, where docker-proxy serves it",
			save: docker28Save(t, fwstate.V6),
			edit: func(p *fwstate.Packet) {
				p.Family, p.DstPort = fwstate.V6, 8080
				p.Src, p.Dst = netip.MustParseAddr("2001:db8:1::7"), netip.MustParseAddr("2001:db8::10")
			},
			verdict: fwstate.Accept,
			trace:   []string{"nat/PREROUTING#1"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := evalPacket()
			tt.edit(&p)
			evalCheck(t, Evaluate(evalRuleset(t, tt.save), p, docker28Route), tt.verdict, tt.trace, tt.reason)
		})
	}
}
