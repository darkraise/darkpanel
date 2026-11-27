package services

import (
	"net/netip"
	"testing"

	"github.com/darkraise/darkpanel/internal/platform/netstat"
)

func TestLiveAgentOnTailnet(t *testing.T) {
	const pid = 4242
	tcp := func(local, remote string, state netstat.State, pids ...int) netstat.Socket {
		s := netstat.Socket{Proto: netstat.ProtoTCP, Local: netip.MustParseAddrPort(local), State: state}
		if remote != "" {
			s.Remote = netip.MustParseAddrPort(remote)
		}
		for _, p := range pids {
			s.Processes = append(s.Processes, netstat.Process{PID: p})
		}
		return s
	}
	for _, c := range []struct {
		name  string
		socks []netstat.Socket
		want  bool
	}{
		{"agent dials from a tailnet address", []netstat.Socket{
			tcp("100.64.1.2:41000", "100.64.1.9:443", netstat.StateEstablished, pid)}, true},
		{"another process dials from a tailnet address", []netstat.Socket{
			tcp("100.64.1.2:41000", "100.64.1.9:443", netstat.StateEstablished, pid+1)}, false},
		{"agent dials from a LAN address", []netstat.Socket{
			tcp("192.168.1.5:41000", "192.168.1.9:443", netstat.StateEstablished, pid)}, false},
		{"agent accepts on a listening port", []netstat.Socket{
			tcp("100.64.1.2:9443", "", netstat.StateListen, pid),
			tcp("100.64.1.2:9443", "100.64.1.9:50000", netstat.StateEstablished, pid)}, false},
		{"agent dials from a tailnet IPv6 address", []netstat.Socket{
			tcp("[fd7a:115c:a1e0::1]:41000", "[fd7a:115c:a1e0::9]:443", netstat.StateEstablished, pid)}, true},
	} {
		got, err := agentOnTailnet(func() ([]netstat.Socket, error) { return c.socks, nil }, pid)
		if err != nil || got != c.want {
			t.Errorf("%s: agentOnTailnet = %v, %v, want %v", c.name, got, err, c.want)
		}
	}
}
