// Package netfilter parses iptables-save and ip6tables-save output (spec
// 1.8) into tables, chains and rules. A rule using a match, target or
// option outside the parser's contract is kept as an opaque rule: its
// Unsupported reasons are set and the evaluator (2.3) treats it as
// unsupported. Output that is not save output at all is an error.
package netfilter

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"slices"
	"strings"
)

var knownTables = []string{"filter", "nat", "raw", "mangle", "security"}

// ErrNoTables is input that holds neither a table block nor an
// incompatible-table note: an empty or truncated capture, never an empty
// firewall, which save output always shows as tables.
var ErrNoTables = errors.New("netfilter: no tables in the input")

// Tables returns the tables the parser accepts, in the order the firewall
// module reads them. The slice is a copy, so callers cannot change what
// Parse accepts.
func Tables() []string { return slices.Clone(knownTables) }

// Ruleset is one save output.
type Ruleset struct {
	Tables []Table
	// LegacyPresent is set when iptables-nft-save warned that iptables-legacy
	// tables exist as well: rules in them are not in this ruleset, so it may
	// be incomplete.
	LegacyPresent bool
}

// Table is one *<name> ... COMMIT block.
type Table struct {
	Name string
	// Incompatible is iptables-nft-save's note that the table holds
	// rules or base chains it cannot show ("# Table `filter' is
	// incompatible, use 'nft' tool."), "" when none. A table with only
	// the note has no chains.
	Incompatible string
	Chains       []Chain
}

// Chain is a declared chain with its rules in order.
type Chain struct {
	Name    string
	Builtin bool   // declared with a policy
	Policy  string // ACCEPT or DROP for a built-in chain, "" otherwise
	Rules   []Rule
}

// Rule is one -A line.
type Rule struct {
	Line   int    // 1-based line number in the input
	Text   string // the line as saved
	Source AddrMatch
	Dest   AddrMatch
	In     NameMatch // -i; a trailing "+" is iptables' wildcard, kept as saved
	Out    NameMatch // -o
	Proto  NameMatch // -p, as saved ("tcp", "ipv6-icmp", a number)
	// Matches are the -m modules in order, each with its options.
	Matches []Match
	// Target is the -j target; Name is "" for a rule without one.
	Target Target
	// Unsupported lists why the rule is opaque; empty for a rule fully
	// inside the contract.
	Unsupported []string
}

// Opaque reports whether the rule uses anything outside the contract.
func (r Rule) Opaque() bool { return len(r.Unsupported) > 0 }

// AddrMatch is -s or -d. Prefix is invalid (the zero value) when the rule
// has no such option.
type AddrMatch struct {
	Prefix netip.Prefix
	Neg    bool
}

// NameMatch is -i, -o or -p. Name is "" when the rule has no such option.
type NameMatch struct {
	Name string
	Neg  bool
}

// Match is one -m module.
type Match struct {
	Module  string
	Options []Option
}

// Target is the -j target. Chain reports a jump to a chain declared in
// the same table.
type Target struct {
	Name    string
	Chain   bool
	Options []Option
}

// Option is one --name option with its values, without the dashes.
type Option struct {
	Name   string
	Neg    bool
	Values []string
}

// Table returns the named table, or nil.
func (rs *Ruleset) Table(name string) *Table {
	for i := range rs.Tables {
		if rs.Tables[i].Name == name {
			return &rs.Tables[i]
		}
	}
	return nil
}

// Chain returns the named chain, or nil.
func (t *Table) Chain(name string) *Chain {
	for i := range t.Chains {
		if t.Chains[i].Name == name {
			return &t.Chains[i]
		}
	}
	return nil
}

// maxLine bounds one line; iptables limits a rule far below it.
const maxLine = 1 << 20

// Parse reads save output. Comment lines are skipped except the
// incompatible-table notes, which are attached to their table, and the
// legacy-table warning, which sets Ruleset.LegacyPresent. Input with neither
// a table block nor an incompatible-table note is ErrNoTables, so an empty
// or truncated capture is never mistaken for an empty firewall.
func Parse(r io.Reader) (*Ruleset, error) {
	p := parser{notes: map[string]string{}}
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
	if p.table != nil {
		return nil, fmt.Errorf("netfilter: table %s has no COMMIT", p.table.Name)
	}
	// A note that came after its table's block belongs to that table; a table
	// with no block at all is kept as a note-only table.
	for _, name := range knownTables {
		note, ok := p.notes[name]
		if !ok {
			continue
		}
		if t := p.rs.Table(name); t != nil {
			t.Incompatible = note
			continue
		}
		p.rs.Tables = append(p.rs.Tables, Table{Name: name, Incompatible: note})
	}
	if len(p.rs.Tables) == 0 {
		return nil, ErrNoTables
	}
	return &p.rs, nil
}

type parser struct {
	rs    Ruleset
	table *Table
	line  int
	notes map[string]string // incompatible notes of tables not yet opened
}

func (p *parser) parseLine(line string) error {
	switch {
	case line == "":
		return nil
	case strings.HasPrefix(line, "#"):
		p.note(line)
		return nil
	case strings.HasPrefix(line, "*"):
		return p.openTable(line[1:])
	case line == "COMMIT":
		if p.table == nil {
			return errors.New("COMMIT outside a table")
		}
		p.rs.Tables = append(p.rs.Tables, *p.table)
		p.table = nil
		return nil
	case strings.HasPrefix(line, ":"):
		return p.declareChain(line[1:])
	case strings.HasPrefix(line, "-A "):
		return p.addRule(line)
	}
	return fmt.Errorf("unexpected line %q", truncate(line))
}

// note records iptables-nft-save's incompatible-table and legacy-table
// comments.
func (p *parser) note(line string) {
	if strings.HasPrefix(line, "# Warning: iptables-legacy tables present") {
		p.rs.LegacyPresent = true
		return
	}
	name, ok := strings.CutPrefix(line, "# Table `")
	if !ok {
		return
	}
	name, rest, ok := strings.Cut(name, "'")
	if !ok || !strings.HasPrefix(rest, " is incompatible") && !strings.HasPrefix(rest, " contains incompatible") {
		return
	}
	p.notes[name] = strings.TrimPrefix(line, "# ")
}

func (p *parser) openTable(name string) error {
	if p.table != nil {
		return fmt.Errorf("table %s opened inside table %s", name, p.table.Name)
	}
	if !slices.Contains(knownTables, name) {
		return fmt.Errorf("unknown table %q", truncate(name))
	}
	if p.rs.Table(name) != nil {
		return fmt.Errorf("table %s appears twice", name)
	}
	p.table = &Table{Name: name, Incompatible: p.notes[name]}
	delete(p.notes, name)
	return nil
}

func (p *parser) declareChain(decl string) error {
	if p.table == nil {
		return errors.New("chain declared outside a table")
	}
	f := strings.Fields(decl)
	if len(f) < 2 || len(f) > 3 {
		return fmt.Errorf("malformed chain declaration %q", truncate(decl))
	}
	if p.table.Chain(f[0]) != nil {
		return fmt.Errorf("chain %s declared twice", f[0])
	}
	c := Chain{Name: f[0]}
	switch f[1] {
	case "-":
	case "ACCEPT", "DROP":
		c.Builtin, c.Policy = true, f[1]
	default:
		return fmt.Errorf("chain %s has policy %q", f[0], truncate(f[1]))
	}
	p.table.Chains = append(p.table.Chains, c)
	return nil
}

func (p *parser) addRule(line string) error {
	if p.table == nil {
		return errors.New("rule outside a table")
	}
	toks, err := tokenize(line)
	if err != nil {
		return err
	}
	if len(toks) < 2 || toks[1].quoted {
		return errors.New("-A without a chain")
	}
	c := p.table.Chain(toks[1].text)
	if c == nil {
		return fmt.Errorf("rule for undeclared chain %q", truncate(toks[1].text))
	}
	rule, err := parseRule(toks[2:])
	if err != nil {
		return err
	}
	rule.Line, rule.Text = p.line, line
	if rule.Target.Name != "" && p.table.Chain(rule.Target.Name) != nil {
		rule.Target.Chain = true
	}
	classify(&rule)
	c.Rules = append(c.Rules, rule)
	return nil
}

type token struct {
	text   string
	quoted bool
}

// tokenize splits a line as iptables-restore does: whitespace separates
// tokens, and a double-quoted token may hold spaces with \" \\ and \'
// escaped. A quoted token is always a value.
func tokenize(line string) ([]token, error) {
	var out []token
	for i := 0; i < len(line); {
		switch line[i] {
		case ' ', '\t':
			i++
		case '"':
			var b strings.Builder
			i++
			for {
				if i >= len(line) {
					return nil, errors.New("unterminated quote")
				}
				if line[i] == '"' {
					i++
					break
				}
				if line[i] == '\\' && i+1 < len(line) {
					i++
				}
				b.WriteByte(line[i])
				i++
			}
			out = append(out, token{text: b.String(), quoted: true})
		default:
			j := i
			for j < len(line) && line[j] != ' ' && line[j] != '\t' {
				j++
			}
			out = append(out, token{text: line[i:j]})
			i = j
		}
	}
	return out, nil
}

// optionName returns a token's option name without dashes, or "" when the
// token is a value.
func optionName(t token) string {
	if t.quoted || len(t.text) < 2 || t.text[0] != '-' {
		return ""
	}
	return strings.TrimLeft(t.text, "-")
}

// topLevel maps every spelling of a top-level option to its short form.
// These are never a module's or target's option, wherever they appear.
var topLevel = map[string]string{
	"s": "s", "source": "s", "d": "d", "destination": "d",
	"i": "i", "in-interface": "i", "o": "o", "out-interface": "o",
	"p": "p", "protocol": "p", "m": "m", "match": "m",
	"j": "j", "jump": "j", "g": "g", "goto": "g", "f": "f", "fragment": "f",
}

// parseRule reads the tokens after "-A <chain>". Options of a -m module
// or the -j target follow it until the next top-level option.
func parseRule(toks []token) (Rule, error) {
	var r Rule
	var cur *[]Option // the module's or target's options being read
	seen := map[string]bool{}
	for i := 0; i < len(toks); {
		neg := false
		if !toks[i].quoted && toks[i].text == "!" {
			neg = true
			i++
			if i == len(toks) || optionName(toks[i]) == "" {
				return Rule{}, errors.New("! not followed by an option")
			}
		}
		name := optionName(toks[i])
		if name == "" {
			return Rule{}, fmt.Errorf("value %q without an option", truncate(toks[i].text))
		}
		i++
		var vals []string
		for i < len(toks) && optionName(toks[i]) == "" && (toks[i].quoted || toks[i].text != "!") {
			vals = append(vals, toks[i].text)
			i++
		}
		short, top := topLevel[name]
		if !top {
			if cur == nil {
				r.Unsupported = append(r.Unsupported, "option --"+name)
				continue
			}
			*cur = append(*cur, Option{Name: name, Neg: neg, Values: vals})
			continue
		}
		if short != "m" && seen[short] {
			r.Unsupported = append(r.Unsupported, "repeated -"+short)
		}
		seen[short] = true
		switch short {
		case "m", "j":
			if len(vals) != 1 || neg {
				return Rule{}, fmt.Errorf("-%s needs one name", short)
			}
			if short == "m" {
				r.Matches = append(r.Matches, Match{Module: vals[0]})
				cur = &r.Matches[len(r.Matches)-1].Options
			} else {
				r.Target.Name = vals[0]
				cur = &r.Target.Options
			}
			continue
		case "g", "f":
			r.Unsupported = append(r.Unsupported, map[string]string{"g": "goto", "f": "fragment match"}[short])
			cur = nil
			continue
		}
		if len(vals) != 1 {
			return Rule{}, fmt.Errorf("-%s needs one value", short)
		}
		cur = nil
		switch short {
		case "s", "d":
			pfx, err := netip.ParsePrefix(vals[0])
			if err != nil {
				r.Unsupported = append(r.Unsupported, fmt.Sprintf("address %q", truncate(vals[0])))
				continue
			}
			am := AddrMatch{Prefix: pfx, Neg: neg}
			if short == "s" {
				r.Source = am
			} else {
				r.Dest = am
			}
		case "i":
			r.In = NameMatch{Name: vals[0], Neg: neg}
		case "o":
			r.Out = NameMatch{Name: vals[0], Neg: neg}
		case "p":
			r.Proto = NameMatch{Name: vals[0], Neg: neg}
		}
	}
	return r, nil
}

func truncate(s string) string {
	if len(s) > 64 {
		return s[:64] + "..."
	}
	return s
}
