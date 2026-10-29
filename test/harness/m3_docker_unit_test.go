//go:build harness

package harness

import (
	"net/netip"
	"slices"
	"strings"
	"testing"
)

func TestM3dxRows(t *testing.T) {
	const view = `{"rows":[
	  {"proto":"tcp","addr":"0.0.0.0","port":22,"family":"v4","process":"sshd","classes":[
	    {"class":"internet","verdict":"open","decidedBy":"ufw-user-input#3"},
	    {"class":"private","range":"10.0.0.0/8","verdict":"open"},
	    {"class":"tailnet","verdict":"open","note":"host firewall; tailnet policy not evaluated"},
	    {"class":"localhost","verdict":"open"}]},
	  {"proto":"tcp","addr":"0.0.0.0","port":8080,"family":"v4","container":{"name":"web","containerPort":80,"proxied":false},"classes":[
	    {"class":"private","range":"10.0.0.0/8","verdict":"open"},
	    {"class":"internet","verdict":"partial","allowed":[
	      {"sources":["203.0.113.0/24","198.51.100.7/32"],"decidedBy":"dp-b-76d26265fc2a#2"},
	      {"sources":["198.51.100.7/32","192.0.2.0/24"],"decidedBy":"dp-b-76d26265fc2a#3"}]}]},
	  {"proto":"udp","addr":"::","port":8053,"family":"v6","classes":[
	    {"class":"internet","verdict":"closed","decidedBy":"bound to loopback"}]}]}`
	got, err := m3dxRows([]byte(view))
	if err != nil {
		t.Fatal(err)
	}
	want := []m3dRow{
		{Proto: "tcp", Addr: "0.0.0.0", Family: "v4", Port: 22, Verdict: "open"},
		{Proto: "tcp", Addr: "0.0.0.0", Family: "v4", Port: 8080, Verdict: "partial", Allowed: []string{"203.0.113.0/24", "198.51.100.7/32", "192.0.2.0/24"}},
		{Proto: "udp", Addr: "::", Family: "v6", Port: 8053, Verdict: "closed"},
	}
	if len(got) != len(want) {
		t.Fatalf("m3dxRows = %+v, want %+v", got, want)
	}
	for i := range want {
		g, w := got[i], want[i]
		if g.Proto != w.Proto || g.Addr != w.Addr || g.Family != w.Family || g.Port != w.Port || g.Verdict != w.Verdict || !slices.Equal(g.Allowed, w.Allowed) {
			t.Errorf("row %d = %+v, want %+v", i, g, w)
		}
	}

	bad := []struct{ name, in, want string }{
		{"not JSON", `{"rows":`, "exposure view"},
		{"no rows", `{"rows":[]}`, "no rows"},
		{"no internet class", `{"rows":[{"proto":"tcp","addr":"0.0.0.0","port":22,"family":"v4","classes":[{"class":"private","verdict":"open"}]}]}`, "internet"},
		{"unknown verdict", `{"rows":[{"proto":"tcp","addr":"0.0.0.0","port":22,"family":"v4","classes":[{"class":"internet","verdict":"maybe"}]}]}`, "maybe"},
		{"protocol", `{"rows":[{"proto":"sctp","addr":"0.0.0.0","port":22,"family":"v4","classes":[{"class":"internet","verdict":"open"}]}]}`, "sctp"},
		{"port zero", `{"rows":[{"proto":"tcp","addr":"0.0.0.0","port":0,"family":"v4","classes":[{"class":"internet","verdict":"open"}]}]}`, "port"},
	}
	for _, c := range bad {
		if _, err := m3dxRows([]byte(c.in)); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: error %v, want one naming %q", c.name, err, c.want)
		}
	}
}

func TestM3dxMismatch(t *testing.T) {
	row := func(proto string, port uint16, verdict string) m3dRow {
		return m3dRow{Proto: proto, Addr: "0.0.0.0", Family: "v4", Port: port, Verdict: verdict}
	}
	cases := []struct {
		name    string
		rows    []m3dRow
		scanned []PortState
		proto   string
		want    []string
	}{
		{"TCP matches exactly",
			[]m3dRow{row("tcp", 22, "open"), row("tcp", 8080, "closed"), row("tcp", 9090, "filtered")},
			[]PortState{{22, "tcp", "open"}, {8080, "tcp", "closed"}, {9090, "tcp", "filtered"}}, "tcp", nil},
		{"TCP differences",
			[]m3dRow{row("tcp", 22, "open"), row("tcp", 8080, "closed"), row("tcp", 9090, "filtered"), row("tcp", 7000, "open")},
			[]PortState{{22, "tcp", "closed"}, {8080, "tcp", "open"}, {9090, "tcp", "closed"}, {7000, "tcp", "open"}}, "tcp",
			[]string{
				"tcp 0.0.0.0:22 (v4): verdict open, scanned closed",
				"tcp 0.0.0.0:8080 (v4): verdict closed, scanned open",
				"tcp 0.0.0.0:9090 (v4): verdict filtered, scanned closed",
			}},
		{"TCP partial and unknown rows are not compared",
			[]m3dRow{row("tcp", 7000, "partial"), row("tcp", 7001, "unknown")},
			[]PortState{{7000, "tcp", "closed"}, {7001, "tcp", "open"}}, "tcp", nil},
		{"a port missing from the scan",
			[]m3dRow{row("tcp", 5000, "open")},
			[]PortState{{22, "tcp", "open"}}, "tcp",
			[]string{"tcp 0.0.0.0:5000 (v4): verdict open, not in the scan"}},
		{"only the protocol asked for is compared",
			[]m3dRow{row("tcp", 53, "open"), row("udp", 53, "closed")},
			[]PortState{{53, "tcp", "closed"}, {53, "udp", "closed"}}, "udp", nil},
		{"UDP open must not be scanned closed",
			[]m3dRow{row("udp", 53, "open"), row("udp", 54, "open"), row("udp", 55, "open")},
			[]PortState{{53, "udp", "closed"}, {54, "udp", "open|filtered"}, {55, "udp", "open"}}, "udp",
			[]string{"udp 0.0.0.0:53 (v4): verdict open, scanned closed"}},
		{"UDP closed and filtered must not be answered",
			[]m3dRow{row("udp", 53, "closed"), row("udp", 54, "closed"), row("udp", 55, "filtered"), row("udp", 56, "filtered"), row("udp", 57, "filtered")},
			[]PortState{{53, "udp", "open"}, {54, "udp", "closed"}, {55, "udp", "open"}, {56, "udp", "open|filtered"}, {57, "udp", "closed"}}, "udp",
			[]string{
				"udp 0.0.0.0:53 (v4): verdict closed, scanned open",
				"udp 0.0.0.0:55 (v4): verdict filtered, scanned open",
			}},
		{"an IPv6 address is bracketed",
			[]m3dRow{{Proto: "tcp", Addr: "::", Family: "v6", Port: 443, Verdict: "closed"}},
			[]PortState{{443, "tcp", "open"}}, "tcp",
			[]string{"tcp [::]:443 (v6): verdict closed, scanned open"}},
	}
	for _, c := range cases {
		if got := m3dxMismatch(c.rows, c.scanned, c.proto); !slices.Equal(got, c.want) {
			t.Errorf("%s: m3dxMismatch =\n%q\nwant\n%q", c.name, got, c.want)
		}
	}
}

func TestM3dxCommands(t *testing.T) {
	const image = "ghcr.io/letsencrypt/pebble:2.10.1@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	cases := []struct{ name, got, want string }{
		{"shell leaves plain words and quotes the rest", m3dxShell("plain-1.2:3", "a b", "it's", "", "{{.State.Status}}"),
			`plain-1.2:3 'a b' 'it'\''s' '' '{{.State.Status}}'`},
		{"nmap", m3dxNmapCommand(ScanOpts{Family: "-6", Ports: []uint16{443}, Target: "2001:db8:d0::10"}),
			"sudo nmap -6 -Pn -n -sS -p443 --min-rate 2000 --max-retries 2 --max-rtt-timeout 500ms -oG - 2001:db8:d0::10"},
		{"add an IPv4 source", m3dxAddrCmd(netip.MustParsePrefix("203.0.113.9/32"), "eth1", true),
			"sudo ip addr add 203.0.113.9/32 dev eth1"},
		{"remove an IPv4 source", m3dxAddrCmd(netip.MustParsePrefix("203.0.113.9/32"), "eth1", false),
			"sudo ip addr del 203.0.113.9/32 dev eth1"},
		{"add an IPv6 source", m3dxAddrCmd(netip.MustParsePrefix("2001:db8::5/128"), "eth1", true),
			"sudo ip addr add 2001:db8::5/128 dev eth1 nodad preferred_lft 0"},
		{"remove an IPv6 source", m3dxAddrCmd(netip.MustParsePrefix("2001:db8::5/128"), "eth1", false),
			"sudo ip addr del 2001:db8::5/128 dev eth1"},
		{"run on a network", m3dxRunCmd(image, "dp-m3d-web", "custom", []string{"8080:14000", "127.0.0.1:8081:14000"}),
			"sudo docker run -d --name dp-m3d-web --network custom -p 8080:14000 -p 127.0.0.1:8081:14000 " + image},
		{"run on the default network", m3dxRunCmd(image, "dp-m3d-udp", "", []string{"8053:8053/udp"}),
			"sudo docker run -d --name dp-m3d-udp -p 8053:8053/udp " + image},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s:\n got %s\nwant %s", c.name, c.got, c.want)
		}
	}
}

func TestM3dxRunning(t *testing.T) {
	if err := m3dxRunning("running"); err != nil {
		t.Errorf("m3dxRunning(running) = %v", err)
	}
	for _, status := range []string{"created", "exited", ""} {
		if err := m3dxRunning(status); err == nil || !strings.Contains(err.Error(), `"`+status+`"`) {
			t.Errorf("m3dxRunning(%q) = %v, want an error naming the status", status, err)
		}
	}
}

func TestM3dxImage(t *testing.T) {
	for name, prefix := range map[string]string{
		"pebble":              "ghcr.io/letsencrypt/pebble:",
		"pebble-challtestsrv": "ghcr.io/letsencrypt/pebble-challtestsrv:",
	} {
		ref, err := m3dxImage(name)
		if err != nil || !strings.HasPrefix(ref, prefix) || !strings.Contains(ref, "@sha256:") {
			t.Errorf("m3dxImage(%s) = %q, %v, want a digest-pinned reference under %s", name, ref, err, prefix)
		}
	}
	if ref, err := m3dxImage("nginx"); err == nil {
		t.Errorf("m3dxImage(nginx) = %q, want an error", ref)
	}
}
