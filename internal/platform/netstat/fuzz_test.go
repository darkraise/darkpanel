package netstat

import (
	"encoding/binary"
	"net/netip"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

// fz5Head returns at most the first 64 bytes of s, for a failure message.
func fz5Head(s string) string { return s[:min(len(s), 64)] }

// fz5HasFamily reports whether a is an address of the table family f. An
// IPv6 table prints an IPv4-mapped peer as an IPv6 address.
func fz5HasFamily(a netip.Addr, f Family) bool {
	if f == IPv6 {
		return a.Is6()
	}
	return a.Is4()
}

// FuzzParseSocketLine feeds one /proc/net socket-table line, and each of its
// fields as an address:port, to parseEntry and parseAddrPort for either
// family and either protocol: neither may panic, and an accepted entry or
// address is of the family that was asked for (R74).
func FuzzParseSocketLine(f *testing.F) {
	for _, table := range []struct {
		text    string
		v6, udp bool
	}{{skTCP, false, false}, {skTCP6, true, false}, {skUDP, false, true}} {
		for _, line := range strings.Split(table.text, "\n")[1:] {
			if strings.TrimSpace(line) != "" {
				f.Add(line, table.v6, table.udp)
			}
		}
	}
	f.Fuzz(func(t *testing.T, line string, v6, udp bool) {
		family, proto := IPv4, ProtoTCP
		if v6 {
			family = IPv6
		}
		if udp {
			proto = ProtoUDP
		}
		fields := strings.Fields(line)
		if s, err := parseEntry(fields, proto, family); err == nil {
			if s.Family != family || s.Proto != proto || !fz5HasFamily(s.Local.Addr(), family) || !fz5HasFamily(s.Remote.Addr(), family) {
				t.Fatalf("entry %+v of %q is not of family %d and protocol %s", s, fz5Head(line), family, proto)
			}
			if udp {
				switch s.State {
				case StateEstablished, StateClose, StateUnknown:
				default:
					t.Fatalf("UDP state %q of %q", s.State, fz5Head(line))
				}
			}
		}
		for _, field := range append([]string{line}, fields...) {
			if ap, err := parseAddrPort(field, family); err == nil && !fz5HasFamily(ap.Addr(), family) {
				t.Fatalf("address %q read as %v is not of family %d", fz5Head(field), ap, family)
			}
		}
	})
}

// fz5CheckRoute fails when parseRoute accepts m with a prefix length other
// than the header's rtm_dst_len.
func fz5CheckRoute(t *testing.T, m []byte) {
	t.Helper()
	r, err := parseRoute(m)
	if err != nil {
		return
	}
	if r.Dst.Bits() != int(m[1]) {
		t.Fatalf("route of %q has prefix length %d, header says %d", fz5Head(string(m)), r.Dst.Bits(), m[1])
	}
}

// FuzzParseNetlink feeds one datagram to parseNetlink, and its messages and
// the datagram itself to parseRoute: none may panic, every returned message
// is no longer than the input and an error returns no messages, and an
// accepted route's prefix length is the header's rtm_dst_len (R74).
func FuzzParseNetlink(f *testing.F) {
	route4 := rtRoute(unix.AF_INET, 24, unix.RT_TABLE_MAIN, RouteProtoKernel, unix.RTN_UNICAST,
		nlAttr(unix.RTA_DST, netip.MustParseAddr("192.0.2.0").AsSlice()), nlAttr(unix.RTA_OIF, nlUint32(2)))
	route6 := rtRoute(unix.AF_INET6, 64, unix.RT_TABLE_MAIN, RouteProtoRA, unix.RTN_UNICAST,
		nlAttr(unix.RTA_DST, netip.MustParseAddr("2001:db8::").AsSlice()), nlAttr(unix.RTA_GATEWAY, netip.MustParseAddr("fe80::1").AsSlice()))
	f.Add(append(rtMessage(unix.RTM_NEWROUTE, 7, route4), rtMessage(unix.NLMSG_DONE, 7, nlUint32(0))...))
	f.Add(rtMessage(unix.RTM_NEWROUTE, 7, route6))
	f.Add(rtMessage(unix.NLMSG_ERROR, 7, nlUint32(1<<32-101)))
	f.Add(rtMessage(unix.RTM_NEWROUTE, 7, append(rtRoute(unix.AF_INET, 0, 254, 0, 1), 0xff, 0, 1, 0)))
	f.Add(rtMessage(unix.RTM_NEWROUTE, 7, rtRoute(unix.AF_INET, 33, 254, 0, 1)))
	f.Add([]byte{1, 2, 3})
	f.Add(route4)
	f.Fuzz(func(t *testing.T, b []byte) {
		var seq uint32
		if len(b) >= unix.SizeofNlMsghdr {
			seq = binary.NativeEndian.Uint32(b[8:])
		}
		msgs, _, err := parseNetlink(b, seq)
		if err != nil && msgs != nil {
			t.Fatalf("messages returned with error %v for %q", err, fz5Head(string(b)))
		}
		for _, m := range msgs {
			if len(m) > len(b) {
				t.Fatalf("message of %d bytes in a datagram of %d: %q", len(m), len(b), fz5Head(string(b)))
			}
			fz5CheckRoute(t, m)
		}
		fz5CheckRoute(t, b)
	})
}
