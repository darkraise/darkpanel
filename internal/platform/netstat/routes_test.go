package netstat

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net/netip"
	"reflect"
	"slices"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

// rtMessage frames payload as one netlink message of type typ.
func rtMessage(typ uint16, seq uint32, payload []byte) []byte {
	b := make([]byte, unix.SizeofNlMsghdr, unix.SizeofNlMsghdr+len(payload))
	binary.NativeEndian.PutUint32(b[0:], uint32(unix.SizeofNlMsghdr+len(payload))) //nolint:gosec // G115: test payloads are a few bytes
	binary.NativeEndian.PutUint16(b[4:], typ)
	binary.NativeEndian.PutUint32(b[8:], seq)
	b = append(b, payload...)
	return append(b, make([]byte, nlAlign(len(b))-len(b))...)
}

// rtRoute builds an RTM_NEWROUTE payload.
func rtRoute(family, dstLen, table, proto, typ uint8, attrs ...[]byte) []byte {
	b := rtMsg(family, dstLen, 0, 0)
	b[4], b[5], b[7] = table, proto, typ
	for _, a := range attrs {
		b = append(b, a...)
	}
	return b
}

func TestParseRoute(t *testing.T) {
	// The header's table byte and the attribute differ so that ignoring the
	// attribute, which carries tables above 255, fails the comparison.
	m := rtRoute(unix.AF_INET6, 0, 252, RouteProtoRA, unix.RTN_UNICAST,
		nlAttr(unix.RTA_TABLE, nlUint32(1000)),
		nlAttr(unix.RTA_GATEWAY, netip.MustParseAddr("fe80::1").AsSlice()),
		nlAttr(unix.RTA_OIF, nlUint32(2)),
		nlAttr(unix.RTA_PRIORITY, nlUint32(1024)),
		nlAttr(unix.RTA_CACHEINFO, make([]byte, 32)),
	)
	r, err := parseRoute(m)
	if err != nil {
		t.Fatal(err)
	}
	want := Route{
		Type: "unicast", Table: 1000, Protocol: RouteProtoRA, Dst: netip.MustParsePrefix("::/0"),
		Gateway: netip.MustParseAddr("fe80::1"), OutIndex: 2, Priority: 1024,
	}
	if r != want {
		t.Fatalf("route = %+v, want %+v", r, want)
	}

	m = rtRoute(unix.AF_INET, 32, unix.RT_TABLE_LOCAL, RouteProtoKernel, unix.RTN_LOCAL,
		nlAttr(unix.RTA_DST, netip.MustParseAddr("127.0.0.1").AsSlice()),
		nlAttr(unix.RTA_PREFSRC, netip.MustParseAddr("127.0.0.1").AsSlice()),
	)
	if r, err = parseRoute(m); err != nil || r.Type != "local" || r.Dst != netip.MustParsePrefix("127.0.0.1/32") ||
		r.Table != unix.RT_TABLE_LOCAL || r.PrefSrc != netip.MustParseAddr("127.0.0.1") {
		t.Fatalf("lookup answer = %+v, %v", r, err)
	}

	for name, bad := range map[string][]byte{
		"short header":   {2, 0, 0},
		"attr too long":  append(rtRoute(unix.AF_INET, 0, 254, 0, 1), 0xff, 0, 1, 0),
		"attr too short": append(rtRoute(unix.AF_INET, 0, 254, 0, 1), 2, 0, 1, 0),
		"dst length":     rtRoute(unix.AF_INET, 33, 254, 0, 1),
	} {
		if _, err := parseRoute(bad); err == nil {
			t.Errorf("%s: parsed", name)
		}
	}
}

func TestParseNetlink(t *testing.T) {
	a := rtMessage(unix.RTM_NEWROUTE, 1, []byte{1, 2, 3})
	b := rtMessage(unix.RTM_NEWROUTE, 1, []byte{4, 5, 6, 7})
	msgs, done, err := parseNetlink(append(a, b...), 1)
	if err != nil || done || len(msgs) != 2 || !slices.Equal(msgs[0], []byte{1, 2, 3}) || !slices.Equal(msgs[1], []byte{4, 5, 6, 7}) {
		t.Fatalf("messages = %v, done %v, %v", msgs, done, err)
	}
	if _, done, err := parseNetlink(rtMessage(unix.NLMSG_DONE, 1, []byte{0, 0, 0, 0}), 1); err != nil || !done {
		t.Fatalf("done = %v, %v", done, err)
	}
	code := -int32(unix.ENETUNREACH)
	errMsg := binary.NativeEndian.AppendUint32(nil, uint32(code)) //nolint:gosec // G115: encodes the negative errno the kernel sends
	if _, _, err := parseNetlink(rtMessage(unix.NLMSG_ERROR, 1, errMsg), 1); !errors.Is(err, unix.ENETUNREACH) {
		t.Fatalf("error message = %v", err)
	}
	if _, done, err := parseNetlink(rtMessage(unix.NLMSG_ERROR, 1, make([]byte, 20)), 1); err != nil || !done {
		t.Fatalf("acknowledgement = %v, %v", done, err)
	}
	for name, bad := range map[string][]byte{
		"short":       {1, 0, 0},
		"overlong":    rtMessage(unix.RTM_NEWROUTE, 1, []byte{1})[:12+4],
		"wrong seq":   rtMessage(unix.RTM_NEWROUTE, 9, []byte{1}),
		"short error": rtMessage(unix.NLMSG_ERROR, 1, []byte{1}),
	} {
		if _, _, err := parseNetlink(bad, 1); err == nil {
			t.Errorf("%s: parsed", name)
		}
	}
}

func TestParseNetlinkDoneStatus(t *testing.T) {
	code := -int32(unix.EINTR)
	status := binary.NativeEndian.AppendUint32(nil, uint32(code)) //nolint:gosec // G115: encodes the negative errno the kernel sends
	msgs, done, err := parseNetlink(append(rtMessage(unix.RTM_NEWROUTE, 1, []byte{1}), rtMessage(unix.NLMSG_DONE, 1, status)...), 1)
	if !errors.Is(err, unix.EINTR) || done || msgs != nil {
		t.Fatalf("a dump that ended with an errno = %v, done %v, messages %v; want the errno and no partial table", err, done, msgs)
	}
}

func TestParseNetlinkDumpInterrupted(t *testing.T) {
	for name, typ := range map[string]uint16{"data": unix.RTM_NEWROUTE, "done": unix.NLMSG_DONE} {
		m := rtMessage(typ, 1, []byte{0, 0, 0, 0})
		binary.NativeEndian.PutUint16(m[6:], unix.NLM_F_MULTI|unix.NLM_F_DUMP_INTR)
		if _, _, err := parseNetlink(m, 1); !errors.Is(err, errDumpInterrupted) {
			t.Errorf("%s message flagged as an inconsistent dump: err = %v", name, err)
		}
	}
}

func TestFromKernel(t *testing.T) {
	if !fromKernel(&unix.SockaddrNetlink{Family: unix.AF_NETLINK}) {
		t.Error("the kernel's port id 0 was refused")
	}
	for name, from := range map[string]unix.Sockaddr{
		"another process": &unix.SockaddrNetlink{Family: unix.AF_NETLINK, Pid: 4242},
		"unix address":    &unix.SockaddrUnix{Name: "/tmp/x"},
		"no address":      nil,
	} {
		if fromKernel(from) {
			t.Errorf("%s was accepted as the kernel", name)
		}
	}
}

func TestRouteLookupResult(t *testing.T) {
	dst := netip.MustParseAddr("192.0.2.1")
	for _, errno := range []unix.Errno{unix.ENETUNREACH, unix.EHOSTUNREACH, unix.EACCES} {
		_, err := routeLookupResult(dst, nil, fmt.Errorf("netstat: wrapped: %w", errno))
		if !errors.Is(err, ErrNoRoute) || !errors.Is(err, errno) {
			t.Errorf("%v: err = %v, want ErrNoRoute wrapping the errno", errno, err)
		}
	}
	if _, err := routeLookupResult(dst, nil, unix.EPERM); err == nil || errors.Is(err, ErrNoRoute) || !errors.Is(err, unix.EPERM) {
		t.Errorf("EPERM err = %v, want a plain failure", err)
	}
	one := rtRoute(unix.AF_INET, 32, 254, RouteProtoKernel, unix.RTN_UNICAST, nlAttr(unix.RTA_DST, dst.AsSlice()))
	for _, msgs := range [][][]byte{nil, {one, one}} {
		if _, err := routeLookupResult(dst, msgs, nil); err == nil || !strings.Contains(err.Error(), "answered") {
			t.Errorf("%d messages: err = %v", len(msgs), err)
		}
	}
	r, err := routeLookupResult(dst, [][]byte{one}, nil)
	if err != nil || r.Dst != netip.MustParsePrefix("192.0.2.1/32") {
		t.Fatalf("one message = %+v, %v", r, err)
	}
}

func TestRouteGetRefusals(t *testing.T) {
	if _, err := RouteGet(RouteQuery{}); err == nil || !strings.Contains(err.Error(), "without a destination") {
		t.Fatalf("empty query err = %v", err)
	}
	q := RouteQuery{Dst: netip.MustParseAddr("192.0.2.1"), Src: netip.MustParseAddr("2001:db8::1")}
	if _, err := RouteGet(q); err == nil || !strings.Contains(err.Error(), "mixes address families") {
		t.Fatalf("mixed families err = %v", err)
	}
	q = RouteQuery{Dst: netip.MustParseAddr("192.0.2.1"), OIF: "no-such-if0"}
	if _, err := RouteGet(q); err == nil || !strings.Contains(err.Error(), "no-such-if0") {
		t.Fatalf("unknown interface err = %v", err)
	}
}

func TestRouteRequest(t *testing.T) {
	q := RouteQuery{Dst: netip.MustParseAddr("::ffff:192.0.2.1"), Src: netip.MustParseAddr("192.0.2.2"), Mark: 0x40000}
	b, err := routeRequest(q, 7, 9)
	if err != nil {
		t.Fatal(err)
	}
	if b[0] != unix.AF_INET || b[1] != 32 || b[2] != 32 || binary.NativeEndian.Uint32(b[8:]) != unix.RTM_F_LOOKUP_TABLE {
		t.Fatalf("rtmsg = %v", b[:unix.SizeofRtMsg])
	}
	got := map[uint16][]byte{}
	for a := b[unix.SizeofRtMsg:]; len(a) > 0; {
		l := int(binary.NativeEndian.Uint16(a[0:]))
		got[binary.NativeEndian.Uint16(a[2:])] = a[unix.SizeofRtAttr:l]
		a = a[nlAlign(l):]
	}
	want := map[uint16][]byte{
		unix.RTA_DST:  {192, 0, 2, 1},
		unix.RTA_SRC:  {192, 0, 2, 2},
		unix.RTA_IIF:  nlUint32(7),
		unix.RTA_OIF:  nlUint32(9),
		unix.RTA_MARK: nlUint32(0x40000),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("attributes = %v, want %v", got, want)
	}
	b, err = routeRequest(RouteQuery{Dst: netip.MustParseAddr("2001:db8::1")}, 0, 0)
	if err != nil || b[0] != unix.AF_INET6 || b[1] != 128 || b[2] != 0 || len(b) != unix.SizeofRtMsg+unix.SizeofRtAttr+16 {
		t.Fatalf("IPv6 request = %v, %v", b, err)
	}
}

// The live tests ask the running kernel about loopback, which every Linux
// host and container has.
func TestRouteGetLive(t *testing.T) {
	r, err := RouteGet(RouteQuery{Dst: netip.MustParseAddr("127.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	if r.Type != "local" || r.OutIface != "lo" || r.Dst != netip.MustParsePrefix("127.0.0.1/32") {
		t.Fatalf("route to 127.0.0.1 = %+v", r)
	}
	back, err := RouteGet(RouteQuery{Dst: netip.MustParseAddr("::ffff:127.0.0.1"), Src: netip.MustParseAddr("127.0.0.1")})
	if err != nil || back.OutIface != "lo" {
		t.Fatalf("reverse lookup from the local address = %+v, %v", back, err)
	}
	if !lrHasIPv6Loopback(t) {
		return
	}
	r6, err := RouteGet(RouteQuery{Dst: netip.MustParseAddr("::1")})
	if err != nil || r6.Type != "local" || r6.OutIface != "lo" {
		t.Fatalf("route to ::1 = %+v, %v", r6, err)
	}
}

// TestRouteGetIncomingLive looks up an incoming flow: a packet from a
// documentation address arriving on the default route's interface for that
// interface's own address is delivered locally. It uses the default route's
// interface so reverse-path filtering accepts the source.
func TestRouteGetIncomingLive(t *testing.T) {
	remote := netip.MustParseAddr("192.0.2.10")
	out, err := RouteGet(RouteQuery{Dst: remote})
	if errors.Is(err, ErrNoRoute) {
		t.Skip("no IPv4 default route")
	}
	if err != nil {
		t.Fatal(err)
	}
	ifs, err := Interfaces()
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range ifs {
		if i.Name != out.OutIface {
			continue
		}
		for _, a := range i.Addrs {
			if !a.Addr().Is4() {
				continue
			}
			in, err := RouteGet(RouteQuery{Dst: a.Addr(), Src: remote, IIF: i.Name})
			if err != nil || in.Type != "local" || in.OutIface != "lo" {
				t.Fatalf("incoming %s on %s = %+v, %v", a.Addr(), i.Name, in, err)
			}
			return
		}
	}
	t.Skipf("default route interface %q has no IPv4 address", out.OutIface)
}

func TestRoutesAndInterfacesLive(t *testing.T) {
	routes, err := Routes(IPv4)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range routes {
		if r.Type == "local" && r.Table == unix.RT_TABLE_LOCAL && r.Dst == netip.MustParsePrefix("127.0.0.0/8") && r.OutIface == "lo" {
			found = true
		}
	}
	if !found {
		t.Fatalf("no local 127.0.0.0/8 route on lo in %+v", routes)
	}
	if _, err := Routes(IPv6); err != nil {
		t.Fatal(err)
	}
	ifs, err := Interfaces()
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range ifs {
		if i.Name == "lo" {
			if !i.Loopback || !i.Up || !slices.Contains(i.Addrs, netip.MustParsePrefix("127.0.0.1/8")) {
				t.Fatalf("lo = %+v", i)
			}
			return
		}
	}
	t.Fatalf("no lo in %+v", ifs)
}

func lrHasIPv6Loopback(t *testing.T) bool {
	t.Helper()
	ifs, err := Interfaces()
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range ifs {
		if i.Name == "lo" && slices.Contains(i.Addrs, netip.MustParsePrefix("::1/128")) {
			return true
		}
	}
	return false
}
