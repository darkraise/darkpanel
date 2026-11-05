//go:build harness

package harness

import (
	"testing"
)

// FuzzParsePorts feeds arbitrary text to ParsePorts and m3dParsePortEntry: every port they accept is from 1 to
// 65535, its protocol is tcp or udp and its state is not empty, and a refusal returns no ports.
func FuzzParsePorts(f *testing.F) {
	f.Add("# Nmap 7.95 scan initiated\nHost: 198.18.0.10 ()\tStatus: Up\n" +
		"Host: 198.18.0.10 ()\tPorts: 22/open/tcp//ssh///, 8080/closed/tcp//http-proxy///, 9090/filtered/tcp//websm///\tIgnored State: filtered (65532)\n# Nmap done\n")
	f.Add("Host: 198.18.0.10 ()\tPorts: 53/open|filtered/udp//domain///, 8053/open/udp//unknown///\tIgnored State: closed (1)\n")
	f.Add("Host: 2001:db8:d0::10 ()\tPorts: 443/open/tcp//https///\tIgnored State: filtered (1)\n")
	f.Add("Host: 198.18.0.10 ()\tIgnored State: filtered (65535)\n")
	f.Add("Host: 198.18.0.10 ()\tPorts: 0/open/tcp////\n")
	f.Add("Host: 198.18.0.10 ()\tPorts: 70000/open/tcp////\n")
	f.Add("Host: 198.18.0.10 ()\tPorts: 22/open/sctp//ssh///\n")
	f.Add("Host: 198.18.0.10 ()\tPorts: 22//tcp//ssh///\n")
	f.Add("Host: 198.18.0.10 ()\tPorts: 22/open\n")
	f.Add("22/open/tcp//ssh///")
	f.Add("")
	f.Fuzz(func(t *testing.T, in string) {
		ports, err := ParsePorts(in)
		if err != nil && ports != nil {
			t.Fatalf("ParsePorts(%q) returned %v with the error %v", fz9Head(in), ports, err)
		}
		for _, p := range ports {
			fz9CheckPort(t, in, p)
		}
		if p, err := m3dParsePortEntry(in); err == nil {
			fz9CheckPort(t, in, p)
		}
	})
}

func fz9CheckPort(t *testing.T, in string, p PortState) {
	t.Helper()
	if p.Port == 0 || (p.Proto != "tcp" && p.Proto != "udp") || p.State == "" {
		t.Fatalf("an input %q gave the port entry %+v, want a port from 1 to 65535, tcp or udp, and a state", fz9Head(in), p)
	}
}

func fz9Head(s string) string {
	if len(s) > 64 {
		return s[:64]
	}
	return s
}
