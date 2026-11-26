package loaded

import (
	"bufio"
	"bytes"
	"net/netip"
	"regexp"
	"strings"
)

// f2bTable is the table fail2ban's nftables actions create.
var f2bTable = nftTable{Family: "inet", Name: "f2b-table"}

// f2bChainHeader is the only base chain declaration fail2ban's actions
// print on the five supported OS (recorded by the harness, spec 4).
const f2bChainHeader = "type filter hook input priority filter - 1; policy accept;"

// F2BNeighbour is fail2ban's table read as a known neighbour of UFW: it
// only rejects or drops addresses fail2ban banned.
type F2BNeighbour struct {
	Banned int // distinct banned addresses across its sets
}

const (
	f2bJail    = `[A-Za-z0-9_-]{1,64}`
	f2bPort    = `[0-9]{1,5}(?:-[0-9]{1,5})?`
	f2bProto   = `(?:tcp|udp|sctp|dccp)`
	f2bICMP    = `(?:port-unreachable|host-unreachable|net-unreachable|prot-unreachable|admin-prohibited|host-prohibited|net-prohibited|no-route|addr-unreachable)`
	f2bVerdict = `drop|reject|reject with icmp ` + f2bICMP + `|reject with icmpv6 ` + f2bICMP + `|reject with icmpx ` + f2bICMP + `|reject with tcp reset`
)

var (
	f2bSetOpen = regexp.MustCompile(`^set (addr6?-set-` + f2bJail + `) \{$`)
	// f2bRule is a rule of nftables-multiport (a port match) or of
	// nftables-allports (a protocol match), with an optional counter.
	f2bRule = regexp.MustCompile(`^(?:(?:tcp|udp) dport (?:` + f2bPort + `|\{ ` + f2bPort + `(?:, ` + f2bPort + `)* \})` +
		`|meta l4proto (?:` + f2bProto + `|\{ ` + f2bProto + `(?:, ` + f2bProto + `)* \}))` +
		` (ip6?) saddr @(addr6?-set-` + f2bJail + `)(?: counter packets [0-9]+ bytes [0-9]+)? (?:` + f2bVerdict + `)$`)
)

// parseF2B reads `nft list table inet f2b-table`. It reports true only for
// exactly the shape fail2ban's nftables-multiport and nftables-allports
// actions create: sets addr-set-<jail> of ipv4_addr and addr6-set-<jail> of
// ipv6_addr holding addresses, and one base chain f2b-chain with the
// recorded declaration whose rules each match a set's family and drop or
// reject. Anything else is false, and the table stays foreign.
func parseF2B(out []byte) (F2BNeighbour, bool) {
	const (
		top = iota
		table
		set
		elements
		chain
		done
	)
	var (
		state    = top
		setName  string
		setTyped bool // the set's type line was read
		setElems bool // the set's elements line was read
		header   bool // the chain's declaration was read
		sets     = map[string]bool{}
		chainSet bool
		banned   = map[netip.Addr]bool{}
	)
	addElements := func(list string) bool {
		for _, e := range strings.Split(list, ",") {
			e = strings.TrimSpace(e)
			if e == "" {
				continue
			}
			a, err := netip.ParseAddr(e)
			if err != nil || a.Zone() != "" || a.Is4() == strings.HasPrefix(setName, "addr6-") {
				return false
			}
			banned[a] = true
		}
		return true
	}
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 0, 64<<10), maxNFTLine)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		ok := true
		switch state {
		case top:
			ok = line == "table inet f2b-table {"
			state = table
		case table:
			m := f2bSetOpen.FindStringSubmatch(line)
			switch {
			case line == "}":
				state = done
			case m != nil && !sets[m[1]] && !chainSet:
				setName, setTyped, setElems, state = m[1], false, false, set
			case line == "chain f2b-chain {" && !chainSet:
				chainSet, state = true, chain
			default:
				ok = false
			}
		case set:
			want := "type ipv4_addr"
			if strings.HasPrefix(setName, "addr6-") {
				want = "type ipv6_addr"
			}
			list, isElements := strings.CutPrefix(line, "elements = { ")
			switch {
			case !setTyped:
				ok, setTyped = line == want, true
			case line == "}":
				sets[setName], state = true, table
			case isElements && !setElems:
				setElems = true
				list, closed := strings.CutSuffix(list, " }")
				ok = addElements(list)
				if !closed {
					state = elements
				}
			default:
				ok = false
			}
		case elements:
			list, closed := strings.CutSuffix(line, " }")
			ok = addElements(list)
			if closed {
				state = set
			}
		case chain:
			m := f2bRule.FindStringSubmatch(line)
			switch {
			case !header:
				ok, header = line == f2bChainHeader, true
			case line == "}":
				state = table
			default:
				ok = m != nil && sets[m[2]] && (m[1] == "ip6") == strings.HasPrefix(m[2], "addr6-")
			}
		default:
			ok = false
		}
		if !ok {
			return F2BNeighbour{}, false
		}
	}
	if sc.Err() != nil || state != done || !header {
		return F2BNeighbour{}, false
	}
	return F2BNeighbour{Banned: len(banned)}, true
}
