//go:build harness

package harness

import (
	"net/netip"
	"slices"
	"testing"
)

func TestOpenPorts(t *testing.T) {
	const header = "# Nmap 7.95 scan initiated Mon Oct 19 03:00:00 2026 as: nmap -Pn -n -sS -p- -oG - 198.18.0.10\n"
	const footer = "# Nmap done at Mon Oct 19 03:01:40 2026 -- 1 IP address (1 host up) scanned in 100.00 seconds\n"
	cases := []struct {
		name, host, out string
		want            []string
		wantErr         bool
	}{
		{"ssh only", "198.18.0.10",
			"Host: 198.18.0.10 ()\tStatus: Up\nHost: 198.18.0.10 ()\tPorts: 22/open/tcp//ssh///\tIgnored State: filtered (65534)\n",
			[]string{"22/tcp"}, false},
		{"two open", "198.18.0.10",
			"Host: 198.18.0.10 ()\tPorts: 22/open/tcp//ssh///, 8080/open/tcp//http-proxy///\tIgnored State: filtered (65533)\n",
			[]string{"22/tcp", "8080/tcp"}, false},
		{"closed is not open", "198.18.0.10",
			"Host: 198.18.0.10 ()\tPorts: 22/open/tcp//ssh///, 25/closed/tcp//smtp///\tIgnored State: filtered (65533)\n",
			[]string{"22/tcp"}, false},
		{"everything filtered", "198.18.0.10",
			"Host: 198.18.0.10 ()\tStatus: Up\nHost: 198.18.0.10 ()\tIgnored State: filtered (65535)\n",
			[]string{}, false},
		{"IPv6", "2001:db8:d0::10",
			"Host: 2001:db8:d0::10 ()\tPorts: 22/open/tcp//ssh///\tIgnored State: filtered (65534)\n",
			[]string{"22/tcp"}, false},
		{"status line only", "198.18.0.10", "Host: 198.18.0.10 ()\tStatus: Up\n", nil, true},
		{"another host", "198.18.0.10", "Host: 198.18.0.11 ()\tPorts: 22/open/tcp//ssh///\n", nil, true},
		{"no output", "198.18.0.10", "", nil, true},
	}
	for _, c := range cases {
		got, err := OpenPorts(header+c.out+footer, c.host)
		if (err != nil) != c.wantErr {
			t.Errorf("%s: error %v, want error %v", c.name, err, c.wantErr)
			continue
		}
		if !c.wantErr && !slices.Equal(got, c.want) {
			t.Errorf("%s: open ports %v, want %v", c.name, got, c.want)
		}
	}
}

func TestM3dNmapArgs(t *testing.T) {
	timing := []string{"--min-rate", "2000", "--max-retries", "2", "--max-rtt-timeout", "500ms", "-oG", "-"}
	want := func(head []string, target string) []string {
		return slices.Concat(head, timing, []string{target})
	}
	cases := []struct {
		name string
		opts ScanOpts
		want []string
	}{
		{"every TCP port", ScanOpts{Family: "-4", Target: "198.18.0.10"},
			want([]string{"-4", "-Pn", "-n", "-sS", "-p-"}, "198.18.0.10")},
		{"UDP ports from a source port", ScanOpts{Family: "-6", UDP: true, Ports: []uint16{8053, 53}, SourcePort: 53, Target: "2001:db8:d0::10"},
			want([]string{"-6", "-Pn", "-n", "-sU", "-p8053,53", "--source-port", "53"}, "2001:db8:d0::10")},
		{"spoofed source", ScanOpts{Family: "-4", Ports: []uint16{8080}, Source: netip.MustParseAddr("198.19.0.5"), Iface: "eth1", Target: "198.18.0.10"},
			want([]string{"-4", "-Pn", "-n", "-sS", "-p8080", "-S", "198.19.0.5", "-e", "eth1"}, "198.18.0.10")},
		{"interface without a source, no family", ScanOpts{Ports: []uint16{22, 80}, Iface: "eth1", Target: "198.18.0.10"},
			want([]string{"-Pn", "-n", "-sS", "-p22,80", "-e", "eth1"}, "198.18.0.10")},
		{"a UDP payload", ScanOpts{Family: "-4", UDP: true, Ports: []uint16{18304}, Payload: []byte{0x12, 0x34, 0xab}, Target: "198.18.0.10"},
			want([]string{"-4", "-Pn", "-n", "-sU", "-p18304", "--data", "1234ab"}, "198.18.0.10")},
		{"every option", ScanOpts{Family: "-6", UDP: true, Ports: []uint16{53}, SourcePort: 53, Source: netip.MustParseAddr("2001:db8:d0::99"), Iface: "eth1", Target: "2001:db8:d0::10"},
			want([]string{"-6", "-Pn", "-n", "-sU", "-p53", "--source-port", "53", "-S", "2001:db8:d0::99", "-e", "eth1"}, "2001:db8:d0::10")},
	}
	for _, c := range cases {
		if got := NmapArgs(c.opts); !slices.Equal(got, c.want) {
			t.Errorf("%s: NmapArgs = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestM3dParsePorts(t *testing.T) {
	cases := []struct {
		name, out string
		want      []PortState
		wantErr   bool
	}{
		{"TCP states", "# Nmap 7.95 scan initiated\nHost: 198.18.0.10 ()\tStatus: Up\n" +
			"Host: 198.18.0.10 ()\tPorts: 22/open/tcp//ssh///, 8080/closed/tcp//http-proxy///, 9090/filtered/tcp//websm///\tIgnored State: filtered (65532)\n# Nmap done\n",
			[]PortState{{22, "tcp", "open"}, {8080, "tcp", "closed"}, {9090, "tcp", "filtered"}}, false},
		{"UDP states", "Host: 198.18.0.10 ()\tPorts: 53/open|filtered/udp//domain///, 8053/open/udp//unknown///, 5353/closed/udp//zeroconf///\tIgnored State: closed (1)\n",
			[]PortState{{53, "udp", "open|filtered"}, {8053, "udp", "open"}, {5353, "udp", "closed"}}, false},
		{"TCP and UDP in one report", "Host: 198.18.0.10 ()\tPorts: 22/open/tcp//ssh///, 53/open|filtered/udp//domain///\n",
			[]PortState{{22, "tcp", "open"}, {53, "udp", "open|filtered"}}, false},
		{"IPv6 host", "Host: 2001:db8:d0::10 ()\tPorts: 443/open/tcp//https///\tIgnored State: filtered (1)\n",
			[]PortState{{443, "tcp", "open"}}, false},
		{"nothing but an ignored state", "Host: 198.18.0.10 ()\tStatus: Up\nHost: 198.18.0.10 ()\tIgnored State: filtered (65535)\n",
			nil, false},
		{"status line only", "Host: 198.18.0.10 ()\tStatus: Up\n", nil, true},
		{"no output", "", nil, true},
		{"port is not a number", "Host: 198.18.0.10 ()\tPorts: ssh/open/tcp//ssh///\n", nil, true},
		{"port zero", "Host: 198.18.0.10 ()\tPorts: 0/open/tcp////\n", nil, true},
		{"port above 65535", "Host: 198.18.0.10 ()\tPorts: 70000/open/tcp////\n", nil, true},
		{"protocol other than tcp and udp", "Host: 198.18.0.10 ()\tPorts: 22/open/sctp//ssh///\n", nil, true},
		{"entry without a state", "Host: 198.18.0.10 ()\tPorts: 22//tcp//ssh///\n", nil, true},
		{"truncated entry", "Host: 198.18.0.10 ()\tPorts: 22/open\n", nil, true},
	}
	for _, c := range cases {
		got, err := ParsePorts(c.out)
		if (err != nil) != c.wantErr {
			t.Errorf("%s: error %v, want error %v", c.name, err, c.wantErr)
			continue
		}
		if !c.wantErr && !slices.Equal(got, c.want) {
			t.Errorf("%s: ports %v, want %v", c.name, got, c.want)
		}
	}
}
