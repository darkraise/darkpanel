package netstat

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/netip"

	"golang.org/x/sys/unix"
)

// ErrNoRoute is a lookup the kernel answers with an unreachable,
// prohibited or missing route.
var ErrNoRoute = errors.New("netstat: no route")

// The route protocols (RTPROT_*) callers test for.
const (
	RouteProtoKernel uint8 = 2
	RouteProtoBoot   uint8 = 3
	RouteProtoStatic uint8 = 4
	RouteProtoRA     uint8 = 9 // learned from IPv6 router advertisements
	RouteProtoDHCP   uint8 = 16
)

// routeTypes are the kernel's route types (RTN_*), by number.
var routeTypes = []string{
	"unspec", "unicast", "local", "broadcast", "anycast", "multicast",
	"blackhole", "unreachable", "prohibit", "throw", "nat", "xresolve",
}

// RouteQuery is one route lookup, as `ip route get` makes it: the kernel
// applies its policy rules and answers the route a packet would take.
type RouteQuery struct {
	Dst  netip.Addr // required
	Src  netip.Addr // optional: the packet's source address
	IIF  string     // optional: look up as for a packet received on this interface
	OIF  string     // optional: only routes through this interface
	Mark uint32     // optional: the packet's firewall mark
}

// Route is a route entry, or the answer to a lookup (whose Dst is the
// looked-up address as a host prefix). A multipath route has no OutIface.
type Route struct {
	Type string
	// Table is the entry's table, or the table a lookup matched. While no
	// policy rule beyond the defaults exists the kernel merges the IPv4
	// local table into main, and a lookup then reports main for local
	// routes too.
	Table    uint32
	Protocol uint8
	Dst      netip.Prefix
	Gateway  netip.Addr
	PrefSrc  netip.Addr
	OutIndex int
	OutIface string
	Priority uint32
}

// RouteGet looks up the route for q.
func RouteGet(q RouteQuery) (Route, error) {
	if !q.Dst.IsValid() {
		return Route{}, errors.New("netstat: route lookup without a destination")
	}
	var index [2]uint32
	for i, name := range []string{q.IIF, q.OIF} {
		if name == "" {
			continue
		}
		ifc, err := net.InterfaceByName(name)
		if err != nil {
			return Route{}, fmt.Errorf("netstat: interface %s: %w", name, err)
		}
		index[i] = uint32(ifc.Index) //nolint:gosec // G115: the kernel numbers interfaces from 1 as int32
	}
	body, err := routeRequest(q, index[0], index[1])
	if err != nil {
		return Route{}, err
	}
	msgs, err := netlinkRequest(unix.RTM_GETROUTE, 0, body)
	if err != nil {
		var errno unix.Errno
		if errors.As(err, &errno) && (errno == unix.ENETUNREACH || errno == unix.EHOSTUNREACH || errno == unix.EACCES) {
			return Route{}, fmt.Errorf("%w to %s: %w", ErrNoRoute, q.Dst, errno)
		}
		return Route{}, err
	}
	if len(msgs) != 1 {
		return Route{}, fmt.Errorf("netstat: route lookup answered %d messages", len(msgs))
	}
	r, err := parseRoute(msgs[0])
	if err != nil {
		return Route{}, err
	}
	r.OutIface = ifaceName(r.OutIndex, nil)
	return r, nil
}

// routeRequest encodes q as an RTM_GETROUTE body: struct rtmsg, then the
// destination, source, input and output interface indexes (0: none) and
// mark as attributes.
func routeRequest(q RouteQuery, iif, oif uint32) ([]byte, error) {
	dst, src := q.Dst.Unmap(), q.Src.Unmap()
	family, bits := familyOf(dst)
	attrs := nlAttr(unix.RTA_DST, dst.AsSlice())
	srcLen := uint8(0)
	if src.IsValid() {
		if src.Is4() != dst.Is4() {
			return nil, errors.New("netstat: route lookup mixes address families")
		}
		attrs = append(attrs, nlAttr(unix.RTA_SRC, src.AsSlice())...)
		srcLen = bits
	}
	if iif != 0 {
		attrs = append(attrs, nlAttr(unix.RTA_IIF, nlUint32(iif))...)
	}
	if oif != 0 {
		attrs = append(attrs, nlAttr(unix.RTA_OIF, nlUint32(oif))...)
	}
	if q.Mark != 0 {
		attrs = append(attrs, nlAttr(unix.RTA_MARK, nlUint32(q.Mark))...)
	}
	return append(rtMsg(family, bits, srcLen, unix.RTM_F_LOOKUP_TABLE), attrs...), nil
}

// Routes lists every route of every table for one family.
func Routes(family Family) ([]Route, error) {
	af := uint8(unix.AF_INET)
	if family == IPv6 {
		af = unix.AF_INET6
	}
	msgs, err := netlinkRequest(unix.RTM_GETROUTE, unix.NLM_F_DUMP, rtMsg(af, 0, 0, 0))
	if err != nil {
		return nil, err
	}
	names := map[int]string{}
	if ifs, err := net.Interfaces(); err == nil {
		for _, ifc := range ifs {
			names[ifc.Index] = ifc.Name
		}
	}
	out := make([]Route, 0, len(msgs))
	for _, m := range msgs {
		r, err := parseRoute(m)
		if err != nil {
			return nil, err
		}
		r.OutIface = ifaceName(r.OutIndex, names)
		out = append(out, r)
	}
	return out, nil
}

// Interface is a network interface with its addresses.
type Interface struct {
	Index    int
	Name     string
	Up       bool // administratively up (IFF_UP)
	Running  bool // operationally up (IFF_RUNNING)
	Loopback bool
	Addrs    []netip.Prefix
}

// Interfaces lists the host's interfaces. The standard library reads them
// through rtnetlink.
func Interfaces() ([]Interface, error) {
	ifs, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("netstat: interfaces: %w", err)
	}
	out := make([]Interface, 0, len(ifs))
	for _, ifc := range ifs {
		addrs, err := ifc.Addrs()
		if err != nil {
			return nil, fmt.Errorf("netstat: addresses of %s: %w", ifc.Name, err)
		}
		i := Interface{
			Index:    ifc.Index,
			Name:     ifc.Name,
			Up:       ifc.Flags&net.FlagUp != 0,
			Running:  ifc.Flags&net.FlagRunning != 0,
			Loopback: ifc.Flags&net.FlagLoopback != 0,
		}
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			addr, ok := netip.AddrFromSlice(ipn.IP)
			if !ok {
				continue
			}
			ones, _ := ipn.Mask.Size()
			i.Addrs = append(i.Addrs, netip.PrefixFrom(addr.Unmap(), ones))
		}
		out = append(out, i)
	}
	return out, nil
}

func ifaceName(index int, names map[int]string) string {
	if index == 0 {
		return ""
	}
	if names != nil {
		return names[index]
	}
	if ifc, err := net.InterfaceByIndex(index); err == nil {
		return ifc.Name
	}
	return ""
}

func familyOf(a netip.Addr) (family, bits uint8) {
	if a.Is4() {
		return unix.AF_INET, 32
	}
	return unix.AF_INET6, 128
}

// rtMsg encodes struct rtmsg: family, dst_len, src_len, tos, table,
// protocol, scope, type, then the 32-bit flags.
func rtMsg(family, dstLen, srcLen uint8, flags uint32) []byte {
	b := make([]byte, unix.SizeofRtMsg)
	b[0], b[1], b[2] = family, dstLen, srcLen
	binary.NativeEndian.PutUint32(b[8:], flags)
	return b
}

func nlUint32(v uint32) []byte {
	return binary.NativeEndian.AppendUint32(nil, v)
}

// nlAttr encodes one attribute, padded to 4 bytes.
func nlAttr(typ uint16, payload []byte) []byte {
	n := unix.SizeofRtAttr + len(payload)
	b := make([]byte, nlAlign(n))
	binary.NativeEndian.PutUint16(b[0:], uint16(n)) //nolint:gosec // G115: an attribute holds a few bytes
	binary.NativeEndian.PutUint16(b[2:], typ)
	copy(b[unix.SizeofRtAttr:], payload)
	return b
}

func nlAlign(n int) int { return (n + 3) &^ 3 }

// parseRoute decodes an RTM_NEWROUTE payload: struct rtmsg, then
// attributes.
func parseRoute(m []byte) (Route, error) {
	if len(m) < unix.SizeofRtMsg {
		return Route{}, fmt.Errorf("netstat: route message of %d bytes", len(m))
	}
	family, dstLen := m[0], int(m[1])
	r := Route{Table: uint32(m[4]), Protocol: m[5], Type: "unknown"}
	if int(m[7]) < len(routeTypes) {
		r.Type = routeTypes[m[7]]
	}
	var dst netip.Addr
	attrs := m[unix.SizeofRtMsg:]
	for len(attrs) >= unix.SizeofRtAttr {
		l := int(binary.NativeEndian.Uint16(attrs[0:]))
		typ := binary.NativeEndian.Uint16(attrs[2:])
		if l < unix.SizeofRtAttr || l > len(attrs) {
			return Route{}, fmt.Errorf("netstat: route attribute of length %d in %d bytes", l, len(attrs))
		}
		v := attrs[unix.SizeofRtAttr:l]
		switch typ {
		case unix.RTA_DST:
			dst, _ = netip.AddrFromSlice(v)
		case unix.RTA_GATEWAY:
			r.Gateway, _ = netip.AddrFromSlice(v)
		case unix.RTA_PREFSRC:
			r.PrefSrc, _ = netip.AddrFromSlice(v)
		case unix.RTA_OIF:
			if len(v) == 4 {
				r.OutIndex = int(binary.NativeEndian.Uint32(v))
			}
		case unix.RTA_PRIORITY:
			if len(v) == 4 {
				r.Priority = binary.NativeEndian.Uint32(v)
			}
		case unix.RTA_TABLE:
			if len(v) == 4 {
				r.Table = binary.NativeEndian.Uint32(v)
			}
		}
		attrs = attrs[min(nlAlign(l), len(attrs)):]
	}
	if !dst.IsValid() {
		dst = netip.IPv4Unspecified()
		if family == unix.AF_INET6 {
			dst = netip.IPv6Unspecified()
		}
	}
	p, err := dst.Prefix(dstLen)
	if err != nil {
		return Route{}, fmt.Errorf("netstat: route destination %s/%d: %w", dst, dstLen, err)
	}
	r.Dst = p
	return r, nil
}

// netlinkRequest sends one rtnetlink request and returns the payloads of
// its answer: one message, or every message of a dump. An error message
// is returned as its errno.
func netlinkRequest(typ, flags uint16, body []byte) ([][]byte, error) {
	fd, err := unix.Socket(unix.AF_NETLINK, unix.SOCK_RAW|unix.SOCK_CLOEXEC, unix.NETLINK_ROUTE)
	if err != nil {
		return nil, fmt.Errorf("netstat: netlink socket: %w", err)
	}
	defer func() { _ = unix.Close(fd) }()
	tv := unix.Timeval{Sec: 5}
	if err := unix.SetsockoptTimeval(fd, unix.SOL_SOCKET, unix.SO_RCVTIMEO, &tv); err != nil {
		return nil, fmt.Errorf("netstat: netlink timeout: %w", err)
	}
	if err := unix.Bind(fd, &unix.SockaddrNetlink{Family: unix.AF_NETLINK}); err != nil {
		return nil, fmt.Errorf("netstat: netlink bind: %w", err)
	}
	const seq = 1
	msg := make([]byte, unix.SizeofNlMsghdr, unix.SizeofNlMsghdr+len(body))
	binary.NativeEndian.PutUint32(msg[0:], uint32(unix.SizeofNlMsghdr+len(body))) //nolint:gosec // G115: a request is well under 64 KiB
	binary.NativeEndian.PutUint16(msg[4:], typ)
	binary.NativeEndian.PutUint16(msg[6:], unix.NLM_F_REQUEST|flags)
	binary.NativeEndian.PutUint32(msg[8:], seq)
	msg = append(msg, body...)
	if err := unix.Sendto(fd, msg, 0, &unix.SockaddrNetlink{Family: unix.AF_NETLINK}); err != nil {
		return nil, fmt.Errorf("netstat: netlink send: %w", err)
	}
	dump := flags&unix.NLM_F_DUMP != 0
	var out [][]byte
	buf := make([]byte, 1<<16)
	for {
		n, _, err := unix.Recvfrom(fd, buf, 0)
		if err != nil {
			return nil, fmt.Errorf("netstat: netlink receive: %w", err)
		}
		msgs, done, err := parseNetlink(buf[:n], seq)
		if err != nil {
			return nil, err
		}
		out = append(out, msgs...)
		if done || (!dump && len(out) > 0) {
			return out, nil
		}
	}
}

// parseNetlink splits one datagram into message payloads. done reports a
// NLMSG_DONE or a zero error (an acknowledgement); a non-zero error is
// returned as its errno.
func parseNetlink(b []byte, seq uint32) (msgs [][]byte, done bool, err error) {
	for len(b) > 0 {
		if len(b) < unix.SizeofNlMsghdr {
			return nil, false, fmt.Errorf("netstat: netlink header of %d bytes", len(b))
		}
		l := int(binary.NativeEndian.Uint32(b[0:]))
		typ := binary.NativeEndian.Uint16(b[4:])
		if l < unix.SizeofNlMsghdr || l > len(b) {
			return nil, false, fmt.Errorf("netstat: netlink message of length %d in %d bytes", l, len(b))
		}
		payload := b[unix.SizeofNlMsghdr:l]
		if got := binary.NativeEndian.Uint32(b[8:]); got != seq {
			return nil, false, fmt.Errorf("netstat: netlink answer with sequence %d, want %d", got, seq)
		}
		switch typ {
		case unix.NLMSG_DONE:
			return msgs, true, nil
		case unix.NLMSG_ERROR:
			if len(payload) < 4 {
				return nil, false, errors.New("netstat: truncated netlink error")
			}
			if code := int32(binary.NativeEndian.Uint32(payload)); code != 0 { //nolint:gosec // G115: the error field is a signed errno
				return nil, false, unix.Errno(-code) //nolint:gosec // G115: the kernel sends a negative errno
			}
			return msgs, true, nil
		default:
			msgs = append(msgs, append([]byte(nil), payload...))
		}
		b = b[min(nlAlign(l), len(b)):]
	}
	return msgs, false, nil
}
