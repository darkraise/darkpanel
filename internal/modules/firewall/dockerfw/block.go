package dockerfw

import (
	"bytes"
	"fmt"
	"net/netip"
	"slices"
	"strconv"
	"strings"

	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/platform/netfilter"
)

const (
	// ChainDockerUser is the chain Docker evaluates before its own forwarding
	// rules and leaves to the administrator.
	ChainDockerUser = "DOCKER-USER"
	// ChainBindings dispatches a DNAT-ed packet to its binding's chain.
	ChainBindings = "darkpanel-docker-bindings"
	// ChainDeny logs and drops what the block refuses.
	ChainDeny = "darkpanel-docker-deny"
	// BlockID names the block in the marker lines "# BEGIN DARKPANEL docker"
	// and "# END DARKPANEL docker".
	BlockID = "docker"
	// LogPrefix is the prefix of the deny chain's log lines; its trailing
	// space is part of the prefix.
	LogPrefix = "[DARKPANEL DOCKER DENY] "
)

// BindingLine is one dispatch line of the bindings chain: traffic DNAT-ed to
// a container's address and port, from the host address and port a binding
// publishes, jumps to the binding's chain.
type BindingLine struct {
	Proto         string
	ContainerAddr netip.Addr
	ContainerPort uint16
	// HostAddr is the address the client connected to; the zero value omits
	// the match, so the line holds for every host address.
	HostAddr netip.Addr
	HostPort uint16
	// Chain is the binding's dp-b-<id> chain.
	Chain string
}

// BindingChain is the source policy of one binding.
type BindingChain struct {
	Name string
	// Deny holds the sources refused before any allow, in order.
	Deny []netip.Prefix
	// Allow holds the sources let through, in order; empty allows every
	// source.
	Allow []netip.Prefix
}

// BlockInput is everything Render needs to write the block.
type BlockInput struct {
	// CustomBridges are the bridge names that are neither docker0 nor
	// br-<id>, sorted.
	CustomBridges []string
	Settings      Settings
	Lines         []BindingLine
	Chains        []BindingChain
}

// blockV4Dests are the private destinations the IPv4 block gates.
var blockV4Dests = []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"}

// blockFamilyOf returns the family of an address.
func blockFamilyOf(a netip.Addr) fwstate.Family {
	if a.Is4() {
		return fwstate.V4
	}
	return fwstate.V6
}

// Render returns the filter section of the block for one family, from
// "*filter" to "COMMIT". An entry belongs to the family of its address;
// entries of the other family are left out.
func Render(in BlockInput, f fwstate.Family) []byte {
	user, everyone, dests := "ufw-user-forward", "0.0.0.0/0", blockV4Dests
	if f == fwstate.V6 {
		user, everyone, dests = "ufw6-user-forward", "::/0", []string{"fc00::/7"}
	}

	var lines []BindingLine
	chainOf := map[string]bool{}
	for _, l := range in.Lines {
		if blockFamilyOf(l.ContainerAddr) == f {
			lines = append(lines, l)
			chainOf[l.Chain] = true
		}
	}
	var chains []BindingChain
	for _, c := range in.Chains {
		if chainOf[c.Name] {
			chains = append(chains, c)
		}
	}

	var b bytes.Buffer
	w := func(format string, args ...any) {
		fmt.Fprintf(&b, format, args...)
		b.WriteByte('\n')
	}
	du := "-A " + ChainDockerUser

	w("*filter")
	w(":%s - [0:0]", user)
	w(":%s - [0:0]", ChainDockerUser)
	w(":%s - [0:0]", ChainBindings)
	w(":%s - [0:0]", ChainDeny)
	for _, c := range chains {
		w(":%s - [0:0]", c.Name)
	}

	w("%s -m conntrack --ctstate RELATED,ESTABLISHED -j RETURN", du)
	w("%s -i docker0 -j RETURN", du)
	w("%s -i br-+ -j RETURN", du)
	for _, br := range in.CustomBridges {
		w("%s -i %s -j RETURN", du, br)
	}
	for _, p := range in.Settings.PrivateSources {
		if blockFamilyOf(p.Addr()) == f {
			w("%s -s %s -j RETURN", du, p)
		}
	}
	for _, e := range in.Settings.ForwardExemptions {
		if e.Family() != f {
			continue
		}
		var s strings.Builder
		s.WriteString(du)
		if e.InInterface != "" {
			s.WriteString(" -i " + e.InInterface)
		}
		s.WriteString(" -m conntrack --ctstate DNAT --ctorigdst " + e.OrigDestination.String())
		if e.OrigPort != 0 {
			s.WriteString(" --ctorigdstport " + strconv.Itoa(int(e.OrigPort)))
		}
		s.WriteString(" -p " + e.Proto + " -d " + e.Destination.String())
		if e.Port != 0 {
			s.WriteString(" --dport " + strconv.Itoa(int(e.Port)))
		}
		s.WriteString(" -j RETURN")
		w("%s", s.String())
	}
	w("%s -m conntrack --ctstate DNAT -j %s", du, ChainBindings)
	w("%s -o docker0 -j %s", du, ChainDeny)
	w("%s -o br-+ -j %s", du, ChainDeny)
	for _, br := range in.CustomBridges {
		w("%s -o %s -j %s", du, br, ChainDeny)
	}
	for _, d := range dests {
		w("%s -d %s -j %s", du, d, ChainDeny)
	}
	w("%s -j RETURN", du)

	for _, l := range lines {
		var s strings.Builder
		fmt.Fprintf(&s, "-A %s -p %s -d %s --dport %d -m conntrack", ChainBindings, l.Proto, l.ContainerAddr, l.ContainerPort)
		if l.HostAddr.IsValid() {
			s.WriteString(" --ctorigdst " + l.HostAddr.String())
		}
		fmt.Fprintf(&s, " --ctorigdstport %d -j %s", l.HostPort, l.Chain)
		w("%s", s.String())
	}
	w("-A %s -j %s", ChainBindings, ChainDeny)

	for _, c := range chains {
		for _, p := range c.Deny {
			if blockFamilyOf(p.Addr()) == f {
				w("-A %s -s %s -j %s", c.Name, p, ChainDeny)
			}
		}
		if len(c.Allow) == 0 {
			w("-A %s -s %s -j %s", c.Name, everyone, user)
		}
		for _, p := range c.Allow {
			if blockFamilyOf(p.Addr()) == f {
				w("-A %s -s %s -j %s", c.Name, p, user)
			}
		}
		w("-A %s -j %s", c.Name, ChainDeny)
	}

	w("-A %s -m limit --limit 3/min -j LOG --log-prefix \"%s\"", ChainDeny, LogPrefix)
	w("-A %s -j DROP", ChainDeny)
	w("COMMIT")
	return b.Bytes()
}

// blockSpan is a block's begin and end marker line indexes.
type blockSpan struct{ begin, end int }

const (
	blockBeginPrefix = "# BEGIN DARKPANEL "
	blockEndPrefix   = "# END DARKPANEL "
)

func blockSplitLines(content []byte) [][]byte {
	lines := bytes.SplitAfter(content, []byte("\n"))
	if len(lines[len(lines)-1]) == 0 {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// blockScan reads every marker line of any block id and returns each block's
// span. A file with nested, duplicated or unterminated blocks, or with a
// marker line ending in whitespace (a carriage return included, which would
// otherwise read as a different id), is refused rather than repaired.
func blockScan(lines [][]byte) (map[string]blockSpan, error) {
	spans := map[string]blockSpan{}
	open, openAt := "", -1
	for i, raw := range lines {
		line := strings.TrimSuffix(string(raw), "\n")
		isBegin := strings.HasPrefix(line, blockBeginPrefix)
		isEnd := strings.HasPrefix(line, blockEndPrefix)
		if (isBegin || isEnd) && strings.TrimRight(line, " \t\r") != line {
			return nil, fmt.Errorf("dockerfw: line %d: marker line ends in whitespace", i+1)
		}
		switch {
		case isBegin:
			id := strings.TrimPrefix(line, blockBeginPrefix)
			if open != "" {
				return nil, fmt.Errorf("dockerfw: line %d: block %s begins inside block %s", i+1, id, open)
			}
			if _, dup := spans[id]; dup {
				return nil, fmt.Errorf("dockerfw: line %d: block %s appears twice", i+1, id)
			}
			open, openAt = id, i
		case isEnd:
			id := strings.TrimPrefix(line, blockEndPrefix)
			if id != open {
				return nil, fmt.Errorf("dockerfw: line %d: end of block %s without its beginning", i+1, id)
			}
			spans[id] = blockSpan{openAt, i}
			open = ""
		}
	}
	if open != "" {
		return nil, fmt.Errorf("dockerfw: block %s has no end marker", open)
	}
	return spans, nil
}

// BlockOf returns the lines between the block's markers, newlines kept, and
// true; a file with no block gives nil, false, nil. A file with duplicated,
// nested or unterminated markers is an error.
func BlockOf(file []byte) (block []byte, ok bool, err error) {
	lines := blockSplitLines(file)
	spans, err := blockScan(lines)
	if err != nil {
		return nil, false, err
	}
	s, ok := spans[BlockID]
	if !ok {
		return nil, false, nil
	}
	return bytes.Join(lines[s.begin+1:s.end], nil), true, nil
}

// WithBlock returns file with the marked block set to block: the block's
// lines are replaced in place, or the marked block is appended when the file
// has none. Everything else stays byte for byte.
func WithBlock(file, block []byte) ([]byte, error) {
	lines := blockSplitLines(file)
	spans, err := blockScan(lines)
	if err != nil {
		return nil, err
	}
	var wrapped bytes.Buffer
	wrapped.WriteString(blockBeginPrefix + BlockID + "\n")
	wrapped.Write(block)
	if len(block) > 0 && !bytes.HasSuffix(block, []byte("\n")) {
		wrapped.WriteByte('\n')
	}
	wrapped.WriteString(blockEndPrefix + BlockID + "\n")

	var out bytes.Buffer
	if s, ok := spans[BlockID]; ok {
		out.Write(bytes.Join(lines[:s.begin], nil))
		out.Write(wrapped.Bytes())
		out.Write(bytes.Join(lines[s.end+1:], nil))
	} else {
		out.Write(file)
		if len(file) > 0 && !bytes.HasSuffix(file, []byte("\n")) {
			out.WriteByte('\n')
		}
		out.Write(wrapped.Bytes())
	}
	// A block holding marker lines would leave a file the scan refuses.
	if _, err := blockScan(blockSplitLines(out.Bytes())); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// WithoutBlock returns file without the marked block, markers included; a
// file with no block is returned as it is.
func WithoutBlock(file []byte) ([]byte, error) {
	lines := blockSplitLines(file)
	spans, err := blockScan(lines)
	if err != nil {
		return nil, err
	}
	s, ok := spans[BlockID]
	if !ok {
		return file, nil
	}
	return append(bytes.Join(lines[:s.begin], nil), bytes.Join(lines[s.end+1:], nil)...), nil
}

// Owned reports whether Darkpanel owns the chain: DOCKER-USER, which the
// block fills, and every chain it declares.
func Owned(chain string) bool {
	return chain == ChainDockerUser || strings.HasPrefix(chain, "darkpanel-") || strings.HasPrefix(chain, "dp-b-")
}

// BlockRules parses the block as iptables-restore would load it.
func BlockRules(block []byte) (*netfilter.Ruleset, error) {
	return netfilter.ParseRestore(bytes.NewReader(block))
}

// blockReturnKey is the key of an unconditional -j RETURN.
const blockReturnKey = "-j RETURN"

// Foreign returns, in chain order, the rules of chain that match no known
// rule and are not an unconditional RETURN, which Docker's own DOCKER-USER
// holds. Rules compare by netfilter.Key as a set, so a second copy of a known
// rule is not foreign. A nil chain has none.
func Foreign(chain *netfilter.Chain, known []netfilter.Rule, f fwstate.Family) []netfilter.Rule {
	if chain == nil {
		return nil
	}
	v6 := f == fwstate.V6
	keys := make([]string, 0, len(known))
	for _, k := range known {
		keys = append(keys, netfilter.Key(k, v6))
	}
	var out []netfilter.Rule
	for _, r := range chain.Rules {
		key := netfilter.Key(r, v6)
		if key == blockReturnKey || slices.Contains(keys, key) {
			continue
		}
		out = append(out, r)
	}
	return out
}
