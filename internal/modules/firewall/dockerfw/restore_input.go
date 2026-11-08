package dockerfw

import (
	"fmt"
	"slices"
	"strings"

	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/platform/netfilter"
)

// Layout records where the foreign DOCKER-USER rules of one family sit
// relative to the block's lines, so they can be put back around the block.
type Layout struct {
	Before      []string `json:"before"`      // foreign rules' save text, in order, placed before the block's first line
	After       []string `json:"after"`       // after its last line
	Interleaved bool     `json:"interleaved"` // some foreign rule sat between block lines; all are in Before
	Order       []string `json:"order"`       // the chain as it was, when Interleaved
}

// LayoutOf reads where the foreign rules of chain sit around the rules in
// known. Every rule that is not foreign counts as a block line, including an
// unconditional RETURN, which is Docker's default or the block's last line. A
// nil chain gives the zero Layout.
func LayoutOf(chain *netfilter.Chain, known []netfilter.Rule, f fwstate.Family) Layout {
	if chain == nil {
		return Layout{}
	}
	foreign := Foreign(chain, known, f)
	isForeign := make([]bool, len(chain.Rules))
	next := 0
	for i, r := range chain.Rules {
		if next < len(foreign) && foreign[next].Line == r.Line && foreign[next].Text == r.Text {
			isForeign[i] = true
			next++
		}
	}

	first, last := -1, -1
	for i, fr := range isForeign {
		if fr {
			continue
		}
		if first < 0 {
			first = i
		}
		last = i
	}

	var l Layout
	for i, r := range chain.Rules {
		if !isForeign[i] {
			continue
		}
		switch {
		case first < 0 || i < first:
			l.Before = append(l.Before, r.Text)
		case i > last:
			l.After = append(l.After, r.Text)
		default:
			l.Interleaved = true
		}
	}
	if l.Interleaved {
		l.Before, l.After = nil, nil
		for i, r := range chain.Rules {
			l.Order = append(l.Order, r.Text)
			if isForeign[i] {
				l.Before = append(l.Before, r.Text)
			}
		}
	}
	return l
}

// DockerUserInput builds the iptables-restore -n input that loads block, the
// DOCKER-USER lines in save syntax, with the foreign rules of l around it.
// The chain declaration flushes DOCKER-USER, so the chain ends up exactly as
// the lines say. A nil or empty block loads Docker's default RETURN.
func DockerUserInput(block []string, l Layout) []byte {
	if len(block) == 0 {
		block = []string{"-A " + ChainDockerUser + " -j RETURN"}
	}
	var b strings.Builder
	b.WriteString("*filter\n:" + ChainDockerUser + " - [0:0]\n")
	for _, group := range [][]string{l.Before, block, l.After} {
		for _, line := range group {
			b.WriteString(line + "\n")
		}
	}
	b.WriteString("COMMIT\n")
	return []byte(b.String())
}

// CleanupInput builds the iptables-restore -n input that leaves DOCKER-USER
// holding only a RETURN and removes every other chain Darkpanel owns in the
// filter table of loaded. Every chain is flushed before any is deleted, so no
// chain goes while another still jumps to it.
func CleanupInput(loaded *netfilter.Ruleset) []byte {
	var b strings.Builder
	b.WriteString("*filter\n:" + ChainDockerUser + " - [0:0]\n-A " + ChainDockerUser + " -j RETURN\n")
	var owned []string
	if loaded != nil {
		if t := loaded.Table("filter"); t != nil {
			for _, c := range t.Chains {
				if Owned(c.Name) && c.Name != ChainDockerUser {
					owned = append(owned, c.Name)
				}
			}
		}
	}
	for _, name := range owned {
		b.WriteString("-F " + name + "\n")
	}
	for _, name := range owned {
		b.WriteString("-X " + name + "\n")
	}
	b.WriteString("COMMIT\n")
	return []byte(b.String())
}

// ChainsInput builds the iptables-restore -n input that loads block into the
// filter table and removes any stale Darkpanel-owned chains present in loaded.
// It never declares ufw-user-forward or ufw6-user-forward, even though the
// block in after.rules declares them so a fresh UFW start can resolve jumps to
// them before user.rules is loaded; declaring them in an iptables-restore -n
// input on a running firewall would flush UFW's loaded route rules.
func ChainsInput(block []byte, loaded *netfilter.Ruleset) []byte {
	var declared []string
	declaredSet := map[string]bool{}
	var rules []netfilter.Rule
	if rs, err := BlockRules(block); err == nil {
		if ft := rs.Table("filter"); ft != nil {
			for _, c := range ft.Chains {
				rules = append(rules, c.Rules...)
			}
		}
	}
	slices.SortFunc(rules, func(a, b netfilter.Rule) int { return a.Line - b.Line })
	for _, raw := range strings.Split(string(block), "\n") {
		line := strings.TrimRight(raw, " \t\r")
		if strings.HasPrefix(line, ":") {
			if f := strings.Fields(line[1:]); len(f) > 0 && !declaredSet[f[0]] {
				declaredSet[f[0]] = true
				declared = append(declared, f[0])
			}
		}
	}

	var b strings.Builder
	b.WriteString("*filter\n:" + ChainDockerUser + " - [0:0]\n")
	for _, name := range declared {
		if name != ChainDockerUser && Owned(name) {
			b.WriteString(":" + name + " - [0:0]\n")
		}
	}
	for _, r := range rules {
		b.WriteString(r.Text + "\n")
	}

	if loaded != nil {
		if ft := loaded.Table("filter"); ft != nil {
			var stale []string
			deleting := map[string]bool{}
			for _, c := range ft.Chains {
				if c.Name != ChainDockerUser && Owned(c.Name) && !declaredSet[c.Name] {
					stale = append(stale, c.Name)
					deleting[c.Name] = true
				}
			}
			slices.Sort(stale)
			for _, name := range stale {
				b.WriteString("-F " + name + "\n")
			}
			for _, name := range stale {
				b.WriteString("-X " + name + "\n")
			}
			if !declaredSet[ChainDockerUser] {
				ref := false
				for _, c := range ft.Chains {
					if c.Name == ChainDockerUser || deleting[c.Name] {
						continue
					}
					for _, r := range c.Rules {
						if r.Target.Name == ChainDockerUser || r.Goto == ChainDockerUser {
							ref = true
							break
						}
					}
					if ref {
						break
					}
				}
				if !ref {
					b.WriteString("-X " + ChainDockerUser + "\n")
				}
			}
		}
	}

	b.WriteString("COMMIT\n")
	return []byte(b.String())
}

// ApplyChains returns the ruleset that iptables-restore -n of input leaves,
// starting from a copy of rs without modifying rs.
func ApplyChains(rs *netfilter.Ruleset, input []byte) (*netfilter.Ruleset, error) {
	if rs == nil {
		return nil, fmt.Errorf("dockerfw: nil ruleset")
	}
	type chainModel struct {
		name    string
		builtin bool
		policy  string
		rules   []string
	}
	type tableModel struct {
		name         string
		incompatible string
		chains       []chainModel
	}
	var tables []tableModel
	for _, t := range rs.Tables {
		tm := tableModel{name: t.Name, incompatible: t.Incompatible}
		for _, c := range t.Chains {
			cm := chainModel{name: c.Name, builtin: c.Builtin, policy: c.Policy}
			for _, r := range c.Rules {
				cm.rules = append(cm.rules, r.Text)
			}
			tm.chains = append(tm.chains, cm)
		}
		tables = append(tables, tm)
	}

	cur := -1
	for _, raw := range strings.Split(string(input), "\n") {
		line := strings.TrimRight(raw, " \t\r")
		f := strings.Fields(line)
		if len(f) == 0 || strings.HasPrefix(line, "#") {
			continue
		}
		var chains *[]chainModel
		if cur >= 0 {
			chains = &tables[cur].chains
		}
		find := func(name string) int {
			if chains == nil {
				return -1
			}
			return slices.IndexFunc(*chains, func(c chainModel) bool { return c.name == name })
		}
		switch {
		case strings.HasPrefix(line, "*"):
			name := line[1:]
			cur = slices.IndexFunc(tables, func(t tableModel) bool { return t.name == name })
			if cur < 0 {
				tables = append(tables, tableModel{name: name})
				cur = len(tables) - 1
			}
		case line == "COMMIT":
			if cur < 0 {
				return nil, fmt.Errorf("dockerfw: COMMIT outside a table")
			}
			cur = -1
		case chains == nil:
			return nil, fmt.Errorf("dockerfw: %q outside a table", line)
		case strings.HasPrefix(line, ":") && len(f) >= 2:
			name, policy := f[0][1:], f[1]
			switch i := find(name); {
			case i < 0 && policy == "-":
				*chains = append(*chains, chainModel{name: name})
			case i < 0:
				*chains = append(*chains, chainModel{name: name, builtin: true, policy: policy})
			case !(*chains)[i].builtin:
				(*chains)[i].rules = nil
			case policy != "-":
				(*chains)[i].policy = policy
			}
		case f[0] == "-A" && len(f) >= 2:
			i := find(f[1])
			if i < 0 {
				return nil, fmt.Errorf("dockerfw: -A: no chain %s", f[1])
			}
			(*chains)[i].rules = append((*chains)[i].rules, line)
		case f[0] == "-F" && len(f) == 2:
			i := find(f[1])
			if i < 0 {
				return nil, fmt.Errorf("dockerfw: -F: no chain %s", f[1])
			}
			(*chains)[i].rules = nil
		case f[0] == "-X" && len(f) == 2:
			i := find(f[1])
			if i < 0 || (*chains)[i].builtin {
				return nil, fmt.Errorf("dockerfw: -X: %s is not a user chain", f[1])
			}
			if len((*chains)[i].rules) > 0 {
				return nil, fmt.Errorf("dockerfw: -X: chain %s is not empty", f[1])
			}
			for _, c := range *chains {
				for _, r := range c.rules {
					if ruleJumpsTo(r, f[1]) {
						return nil, fmt.Errorf("dockerfw: -X: chain %s is still referenced by %q", f[1], r)
					}
				}
			}
			*chains = slices.Delete(*chains, i, i+1)
		default:
			return nil, fmt.Errorf("dockerfw: unexpected line %q", line)
		}
	}
	if cur >= 0 {
		return nil, fmt.Errorf("dockerfw: table %s has no COMMIT", tables[cur].name)
	}

	var b strings.Builder
	if rs.LegacyPresent {
		b.WriteString("# Warning: iptables-legacy tables present\n")
	}
	for _, t := range tables {
		if t.incompatible != "" {
			b.WriteString("# " + t.incompatible + "\n")
		}
		if len(t.chains) == 0 && t.incompatible != "" {
			continue
		}
		b.WriteString("*" + t.name + "\n")
		for _, c := range t.chains {
			policy := "-"
			if c.builtin {
				policy = c.policy
			}
			fmt.Fprintf(&b, ":%s %s [0:0]\n", c.name, policy)
		}
		for _, c := range t.chains {
			for _, r := range c.rules {
				b.WriteString(r + "\n")
			}
		}
		b.WriteString("COMMIT\n")
	}
	parsed, err := netfilter.ParseRestore(strings.NewReader(b.String()))
	if err != nil {
		return nil, fmt.Errorf("dockerfw: %w", err)
	}
	parsed.LegacyPresent = rs.LegacyPresent
	for _, t := range tables {
		if t.incompatible != "" {
			if pt := parsed.Table(t.name); pt != nil {
				pt.Incompatible = t.incompatible
			} else {
				parsed.Tables = append(parsed.Tables, netfilter.Table{Name: t.name, Incompatible: t.incompatible})
			}
		}
	}
	return parsed, nil
}

// ruleJumpsTo reports whether rule line jumps or goes to chain.
func ruleJumpsTo(line, chain string) bool {
	f := strings.Fields(line)
	for i := 0; i+1 < len(f); i++ {
		if (f[i] == "-j" || f[i] == "-g") && f[i+1] == chain {
			return true
		}
	}
	return false
}
