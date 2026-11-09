package netstat

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net/netip"

	"golang.org/x/sys/unix"
)

// Rule is one entry of the kernel's policy routing rule table; a non-zero
// selector field means the rule requires a packet to match that selector.
type Rule struct {
	Family   Family
	Priority uint32
	Table    uint32
	Action   uint8        // FR_ACT_*
	Src, Dst netip.Prefix // zero when the rule has no prefix
	Mark     bool         // FRA_FWMARK or FRA_FWMASK present
	Proto    uint8        // FRA_IP_PROTO, 0 when absent
	SrcPort  bool         // FRA_SPORT_RANGE present
	DstPort  bool         // FRA_DPORT_RANGE present
	UID      bool         // FRA_UID_RANGE present
	TOS      bool         // the header's tos byte is non-zero
}

// Rules lists every policy routing rule for one family.
func Rules(family Family) ([]Rule, error) {
	return rulesFrom(family, netlinkRequest)
}

func rulesFrom(family Family, request netlinkDump) ([]Rule, error) {
	af := uint8(unix.AF_INET)
	if family == IPv6 {
		af = unix.AF_INET6
	}
	// struct fib_rule_hdr has the layout of struct rtmsg.
	msgs, err := retryDump(func() ([][]byte, error) {
		return request(unix.RTM_GETRULE, unix.NLM_F_DUMP, rtMsg(af, 0, 0, 0))
	})
	if errors.Is(err, unix.EAFNOSUPPORT) {
		// The kernel answers EAFNOSUPPORT for a family with no policy
		// routing, such as IPv6 disabled at boot, and that family has no
		// rules.
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("netstat: rule dump: %w", err)
	}
	out := make([]Rule, 0, len(msgs))
	for _, m := range msgs {
		r, err := parseRule(m)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

// parseRule decodes an RTM_NEWRULE payload: struct fib_rule_hdr, then
// attributes.
func parseRule(m []byte) (Rule, error) {
	if len(m) < unix.SizeofRtMsg {
		return Rule{}, fmt.Errorf("netstat: rule message of %d bytes", len(m))
	}
	family := IPv4
	if m[0] == unix.AF_INET6 {
		family = IPv6
	}
	dstLen, srcLen := int(m[1]), int(m[2])
	r := Rule{
		Family: family,
		Table:  uint32(m[4]),
		Action: m[7],
		TOS:    m[3] != 0,
	}
	attrs := m[unix.SizeofRtMsg:]
	for len(attrs) >= unix.SizeofRtAttr {
		l := int(binary.NativeEndian.Uint16(attrs[0:]))
		typ := binary.NativeEndian.Uint16(attrs[2:])
		if l < unix.SizeofRtAttr || l > len(attrs) {
			return Rule{}, fmt.Errorf("netstat: rule attribute of length %d in %d bytes", l, len(attrs))
		}
		v := attrs[unix.SizeofRtAttr:l]
		switch typ {
		case unix.FRA_DST:
			p, err := rulePrefix(v, dstLen)
			if err != nil {
				return Rule{}, err
			}
			r.Dst = p
		case unix.FRA_SRC:
			p, err := rulePrefix(v, srcLen)
			if err != nil {
				return Rule{}, err
			}
			r.Src = p
		case unix.FRA_PRIORITY:
			if len(v) == 4 {
				r.Priority = binary.NativeEndian.Uint32(v)
			}
		case unix.FRA_TABLE:
			if len(v) == 4 {
				r.Table = binary.NativeEndian.Uint32(v)
			}
		case unix.FRA_FWMARK, unix.FRA_FWMASK:
			r.Mark = true
		case unix.FRA_IP_PROTO:
			if len(v) == 1 {
				r.Proto = v[0]
			}
		case unix.FRA_SPORT_RANGE:
			r.SrcPort = true
		case unix.FRA_DPORT_RANGE:
			r.DstPort = true
		case unix.FRA_UID_RANGE:
			r.UID = true
		}
		attrs = attrs[min(nlAlign(l), len(attrs)):]
	}
	return r, nil
}

func rulePrefix(b []byte, bits int) (netip.Prefix, error) {
	addr, _ := netip.AddrFromSlice(b)
	p, err := addr.Prefix(bits)
	if err != nil {
		return netip.Prefix{}, fmt.Errorf("netstat: rule prefix %s/%d: %w", addr, bits, err)
	}
	return p, nil
}
