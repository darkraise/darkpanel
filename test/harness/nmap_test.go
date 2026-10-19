//go:build harness

package harness

import (
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
