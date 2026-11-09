package netstat

import (
	"net/netip"
	"slices"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

// w11b02Msg is an RTM_NEWRULE payload: struct fib_rule_hdr, then attrs.
func w11b02Msg(family, dstLen, srcLen, table, action uint8, attrs ...[]byte) []byte {
	b := rtMsg(family, dstLen, srcLen, 0)
	b[4], b[7] = table, action
	for _, a := range attrs {
		b = append(b, a...)
	}
	return b
}

func TestW11b02ParseRule(t *testing.T) {
	const act = unix.FR_ACT_TO_TBL
	addr := func(s string) []byte { return netip.MustParseAddr(s).AsSlice() }
	span := []byte{1, 0, 2, 0}
	for _, tt := range []struct {
		name string
		m    []byte
		want Rule
	}{
		{"the main rule", w11b02Msg(unix.AF_INET, 0, 0, unix.RT_TABLE_MAIN, act,
			nlAttr(unix.FRA_PRIORITY, nlUint32(32766)), nlAttr(unix.FRA_TABLE, nlUint32(unix.RT_TABLE_MAIN)),
			nlAttr(unix.FRA_SUPPRESS_PREFIXLEN, nlUint32(0xffffffff)), nlAttr(unix.FRA_PROTOCOL, []byte{2})),
			Rule{Family: IPv4, Priority: 32766, Table: unix.RT_TABLE_MAIN, Action: act}},
		{"priority 0 has no priority attribute", w11b02Msg(unix.AF_INET, 0, 0, unix.RT_TABLE_LOCAL, act),
			Rule{Family: IPv4, Table: unix.RT_TABLE_LOCAL, Action: act}},
		{"a table above 255 comes from the attribute", w11b02Msg(unix.AF_INET, 0, 0, unix.RT_TABLE_COMPAT, act,
			nlAttr(unix.FRA_TABLE, nlUint32(1000))),
			Rule{Family: IPv4, Table: 1000, Action: act}},
		{"source and destination prefixes", w11b02Msg(unix.AF_INET, 24, 16, 200, act,
			nlAttr(unix.FRA_DST, addr("198.41.192.0")), nlAttr(unix.FRA_SRC, addr("45.33.0.0"))),
			Rule{Family: IPv4, Table: 200, Action: act,
				Dst: netip.MustParsePrefix("198.41.192.0/24"), Src: netip.MustParsePrefix("45.33.0.0/16")}},
		{"an IPv6 prefix", w11b02Msg(unix.AF_INET6, 32, 0, 200, act, nlAttr(unix.FRA_DST, addr("2606:4700::"))),
			Rule{Family: IPv6, Table: 200, Action: act, Dst: netip.MustParsePrefix("2606:4700::/32")}},
		{"a mark", w11b02Msg(unix.AF_INET, 0, 0, 200, act, nlAttr(unix.FRA_FWMARK, nlUint32(0x80000))),
			Rule{Family: IPv4, Table: 200, Action: act, Mark: true}},
		{"a mask without a mark", w11b02Msg(unix.AF_INET, 0, 0, 200, act, nlAttr(unix.FRA_FWMASK, nlUint32(0xff0000))),
			Rule{Family: IPv4, Table: 200, Action: act, Mark: true}},
		{"protocol and ports", w11b02Msg(unix.AF_INET, 0, 0, 200, act, nlAttr(unix.FRA_IP_PROTO, []byte{unix.IPPROTO_TCP}),
			nlAttr(unix.FRA_SPORT_RANGE, span), nlAttr(unix.FRA_DPORT_RANGE, span)),
			Rule{Family: IPv4, Table: 200, Action: act, Proto: unix.IPPROTO_TCP, SrcPort: true, DstPort: true}},
		{"a uid range", w11b02Msg(unix.AF_INET, 0, 0, 200, act, nlAttr(unix.FRA_UID_RANGE, make([]byte, 8))),
			Rule{Family: IPv4, Table: 200, Action: act, UID: true}},
		{"a tos selector", func() []byte { m := w11b02Msg(unix.AF_INET, 0, 0, 200, act); m[3] = 0x10; return m }(),
			Rule{Family: IPv4, Table: 200, Action: act, TOS: true}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseRule(tt.m)
			if err != nil || got != tt.want {
				t.Errorf("parseRule = %+v, %v; want %+v", got, err, tt.want)
			}
		})
	}
}

func TestW11b02ParseRuleRefusesMalformedMessages(t *testing.T) {
	for _, tt := range []struct {
		name string
		m    []byte
	}{
		{"shorter than the header", make([]byte, 4)},
		{"an attribute past the end", append(rtMsg(unix.AF_INET, 0, 0, 0), nlAttr(unix.FRA_TABLE, nlUint32(1))[:6]...)},
		{"a prefix longer than the family", w11b02Msg(unix.AF_INET, 40, 0, 200, 1, nlAttr(unix.FRA_DST, []byte{8, 8, 8, 8}))},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := parseRule(tt.m); err == nil || !strings.Contains(err.Error(), "netstat: rule") {
				t.Errorf("parseRule = %v, want an error naming the rule", err)
			}
		})
	}
}

func TestW11b02RulesRetriesAnInterruptedDump(t *testing.T) {
	calls := 0
	got, err := rulesFrom(IPv4, func(uint16, uint16, []byte) ([][]byte, error) {
		calls++
		if calls == 1 {
			return nil, errDumpInterrupted
		}
		return [][]byte{w11b02Msg(unix.AF_INET, 0, 0, 200, unix.FR_ACT_TO_TBL)}, nil
	})
	want := []Rule{{Family: IPv4, Table: 200, Action: unix.FR_ACT_TO_TBL}}
	if err != nil || calls != 2 || !slices.Equal(got, want) {
		t.Fatalf("rulesFrom = %+v, %v after %d calls, want %+v after 2", got, err, calls, want)
	}
}

func TestW11b02RulesLive(t *testing.T) {
	rules, err := Rules(IPv4)
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(rules, func(r Rule) bool { return r.Priority == 32766 && r.Table == unix.RT_TABLE_MAIN })
	if i < 0 {
		t.Fatalf("no main rule at priority 32766 in %+v", rules)
	}
	if want := (Rule{Family: IPv4, Priority: 32766, Table: unix.RT_TABLE_MAIN, Action: unix.FR_ACT_TO_TBL}); rules[i] != want {
		t.Errorf("main rule = %+v, want %+v: it selects nothing", rules[i], want)
	}
	if _, err := Rules(IPv6); err != nil {
		t.Fatal(err)
	}
}

func TestW11b02RulesOfAFamilyWithoutPolicyRouting(t *testing.T) {
	got, err := rulesFrom(IPv6, func(uint16, uint16, []byte) ([][]byte, error) {
		return nil, unix.EAFNOSUPPORT
	})
	if err != nil || len(got) != 0 {
		t.Fatalf("rulesFrom = %+v, %v; want no rules and no error", got, err)
	}
}
