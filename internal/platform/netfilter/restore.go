package netfilter

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
)

// builtinChains are each table's built-in chains.
var builtinChains = map[string][]string{
	"filter":   {"INPUT", "FORWARD", "OUTPUT"},
	"nat":      {"PREROUTING", "INPUT", "OUTPUT", "POSTROUTING"},
	"mangle":   {"PREROUTING", "INPUT", "FORWARD", "OUTPUT", "POSTROUTING"},
	"raw":      {"PREROUTING", "OUTPUT"},
	"security": {"INPUT", "FORWARD", "OUTPUT"},
}

// protoNames maps the protocol numbers and aliases iptables accepts for the
// protocols UFW, Docker and tailscaled use to the names iptables-save
// prints.
var protoNames = map[string]string{
	"1": "icmp", "2": "igmp", "6": "tcp", "17": "udp", "41": "ipv6", "47": "gre", "50": "esp", "51": "ah",
	"58": "ipv6-icmp", "icmpv6": "ipv6-icmp", "icmp6": "ipv6-icmp",
}

// canonicalProto is the protocol name iptables-save prints for p.
func canonicalProto(p string) string {
	p = strings.ToLower(p)
	if name, ok := protoNames[p]; ok {
		return name
	}
	return p
}

// implicitModules are the modules iptables loads by itself for an option of
// a rule's protocol given without -m, by canonical protocol name.
var implicitModules = map[string]string{"tcp": "tcp", "udp": "udp", "icmp": "icmp", "ipv6-icmp": "icmp6"}

// restoreOwner is the extension an option of a restore-syntax rule belongs
// to. iptables merges the options of every loaded extension, so the option
// goes to the current extension when it defines it, else to the latest
// loaded module or the target that does; failing those, an option of the
// rule's protocol module loads that module, as iptables does for -p tcp,
// udp, icmp and icmpv6 without -m. An option nothing defines stays with the
// current extension, for classify to report.
func (r *Rule) restoreOwner(cur int, name string) int {
	if r.defines(cur, name) {
		return cur
	}
	for i := len(r.Matches) - 1; i >= 0; i-- {
		if r.defines(i, name) {
			return i
		}
	}
	if r.defines(extTarget, name) {
		return extTarget
	}
	m := implicitModules[r.Proto.Name]
	if m == "" || r.Proto.Neg || slices.ContainsFunc(r.Matches, func(x Match) bool { return x.Module == m }) {
		return cur
	}
	if _, ok := modules[m][name]; !ok {
		return cur
	}
	r.Matches = append(r.Matches, Match{Module: m})
	return len(r.Matches) - 1
}

// defines reports whether the extension ext of r takes the option name in
// the contract.
func (r *Rule) defines(ext int, name string) bool {
	var known arity
	switch {
	case ext == extTarget && r.Target.Name != "":
		known = targets[r.Target.Name]
	case ext >= 0:
		known = modules[r.Matches[ext].Module]
	}
	_, ok := known[name]
	return ok
}

// ParseRestore reads iptables-restore input, as UFW's rules files and the
// firewall predictor's output are written, into the structure Parse gives
// save output: the ruleset `*-restore --noflush` would load into empty
// tables. A table may appear in several blocks, which continue it; a
// declared chain that exists already is flushed when it is a user chain and
// takes the declared policy when it is built-in; -A appends and -I inserts
// at its position (1 when none); a rule for a chain the input never
// declares creates that chain, built-in when the table has a built-in
// chain of that name, with Policy "". Rules are read as iptables-restore
// reads them (parseRule's restore syntax), and a jump resolves to a chain
// the input declares or appends to anywhere, so the files of one load are
// passed as one reader, in load order, each ending in a newline. Input
// without a table is ErrNoTables.
func ParseRestore(r io.Reader) (*Ruleset, error) {
	p := restoreParser{cur: -1}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), maxLine)
	for sc.Scan() {
		p.line++
		if err := p.parseLine(strings.TrimRight(sc.Text(), " \t\r")); err != nil {
			return nil, fmt.Errorf("netfilter: line %d: %w", p.line, err)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("netfilter: %w", err)
	}
	if p.cur >= 0 {
		return nil, fmt.Errorf("netfilter: table %s has no COMMIT", p.rs.Tables[p.cur].Name)
	}
	if len(p.rs.Tables) == 0 {
		return nil, ErrNoTables
	}
	for ti := range p.rs.Tables {
		t := &p.rs.Tables[ti]
		for ci := range t.Chains {
			for ri := range t.Chains[ci].Rules {
				rule := &t.Chains[ci].Rules[ri]
				rule.Target.Chain = rule.Target.Name != "" && t.Chain(rule.Target.Name) != nil
				classify(rule)
			}
		}
	}
	return &p.rs, nil
}

type restoreParser struct {
	rs   Ruleset
	cur  int // index of the open table in rs.Tables; -1 outside a block
	line int
}

func (p *restoreParser) parseLine(line string) error {
	switch {
	case line == "" || strings.HasPrefix(line, "#"):
		return nil
	case strings.HasPrefix(line, "*"):
		return p.openTable(line[1:])
	case line == "COMMIT":
		if p.cur < 0 {
			return errors.New("COMMIT outside a table")
		}
		p.cur = -1
		return nil
	case strings.HasPrefix(line, ":"):
		return p.declareChain(line[1:])
	case line == "-A" || line == "-I" || strings.HasPrefix(line, "-A ") || strings.HasPrefix(line, "-I "):
		return p.addRule(line)
	}
	return fmt.Errorf("unexpected line %q", truncate(line))
}

func (p *restoreParser) openTable(name string) error {
	if p.cur >= 0 {
		return fmt.Errorf("table %s opened inside table %s", name, p.rs.Tables[p.cur].Name)
	}
	if !slices.Contains(knownTables, name) {
		return fmt.Errorf("unknown table %q", truncate(name))
	}
	p.cur = slices.IndexFunc(p.rs.Tables, func(t Table) bool { return t.Name == name })
	if p.cur < 0 {
		p.rs.Tables = append(p.rs.Tables, Table{Name: name})
		p.cur = len(p.rs.Tables) - 1
	}
	return nil
}

// chain returns the open table's chain name, creating it when absent.
func (p *restoreParser) chain(name string) *Chain {
	t := &p.rs.Tables[p.cur]
	if c := t.Chain(name); c != nil {
		return c
	}
	t.Chains = append(t.Chains, Chain{Name: name, Builtin: slices.Contains(builtinChains[t.Name], name)})
	return &t.Chains[len(t.Chains)-1]
}

func (p *restoreParser) declareChain(decl string) error {
	if p.cur < 0 {
		return errors.New("chain declared outside a table")
	}
	f := strings.Fields(decl)
	if len(f) < 2 || len(f) > 3 {
		return fmt.Errorf("malformed chain declaration %q", truncate(decl))
	}
	if f[1] != "-" && f[1] != "ACCEPT" && f[1] != "DROP" {
		return fmt.Errorf("chain %s has policy %q", f[0], truncate(f[1]))
	}
	builtin := slices.Contains(builtinChains[p.rs.Tables[p.cur].Name], f[0])
	if f[1] != "-" && !builtin {
		return fmt.Errorf("chain %s is not built-in, so it has no policy", f[0])
	}
	c := p.chain(f[0])
	switch {
	case !builtin:
		c.Rules = nil
	case f[1] != "-":
		c.Policy = f[1]
	}
	return nil
}

func (p *restoreParser) addRule(line string) error {
	if p.cur < 0 {
		return errors.New("rule outside a table")
	}
	toks, err := tokenize(line)
	if err != nil {
		return err
	}
	cmd := toks[0].text
	if len(toks) < 2 || toks[1].quoted {
		return fmt.Errorf("%s without a chain", cmd)
	}
	rest, pos := toks[2:], 1
	if cmd == "-I" && len(rest) > 0 && !rest[0].quoted {
		if n, err := strconv.Atoi(rest[0].text); err == nil {
			rest, pos = rest[1:], n
		}
	}
	rule, err := parseRule(rest, true)
	if err != nil {
		return err
	}
	rule.Line, rule.Text = p.line, line
	c := p.chain(toks[1].text)
	if cmd == "-A" {
		c.Rules = append(c.Rules, rule)
		return nil
	}
	if pos < 1 || pos > len(c.Rules)+1 {
		return fmt.Errorf("insert at %d into chain %s of %d rules", pos, c.Name, len(c.Rules))
	}
	c.Rules = slices.Insert(c.Rules, pos-1, rule)
	return nil
}
