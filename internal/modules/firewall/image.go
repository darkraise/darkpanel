package firewall

import (
	"bytes"
	"fmt"
	"slices"
	"strings"

	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/platform/netfilter"
)

// beforeImage is a repair snapshot's loaded-state before-image (spec 2.3):
// per family, every UFW and Darkpanel chain of every table and the
// built-in INPUT, FORWARD and OUTPUT chains of the filter table, with
// their policies and rules as *-save printed them.
type beforeImage struct {
	V4 []imageTable `json:"v4,omitempty"`
	V6 []imageTable `json:"v6,omitempty"`
}

// imageTable is one table's chains in a before-image.
type imageTable struct {
	Name   string       `json:"name"`
	Chains []imageChain `json:"chains"`
}

// imageChain is one chain in a before-image.
type imageChain struct {
	Name    string      `json:"name"`
	Builtin bool        `json:"builtin,omitempty"`
	Policy  string      `json:"policy,omitempty"`
	Rules   []imageRule `json:"rules"`
}

// imageRule is one rule of an imageChain. Key is taken when the image is,
// from the rule in its whole table, so a jump compares the same way it did
// in the loaded state.
type imageRule struct {
	Text string `json:"text"`           // the -A line as *-save printed it
	Key  string `json:"key"`            // netfilter.Key of the rule in its family
	Own  bool   `json:"own,omitempty"`  // a bare jump to a UFW or Darkpanel chain
	Jump string `json:"jump,omitempty"` // the chain of its table the rule jumps or goes to
}

// chainRef names one chain of a loaded state.
type chainRef struct {
	Family fwstate.Family
	Table  string
	Chain  string
}

func (c chainRef) String() string { return fmt.Sprintf("%s %s/%s", c.Family, c.Table, c.Chain) }

// imagePlan is what loading a before-image does to the current loaded state.
type imagePlan struct {
	Restore4, Restore6 []byte     // *-restore -n input per family; nil when the family loads nothing
	Excluded           []chainRef // chains left as they are: see skippedChains
}

// builtinChains are the built-in chains a before-image holds, of the filter
// table only: UFW hooks its chains there, and other tables' built-in chains
// belong to Docker, tailscaled and other programs.
var builtinChains = []string{"INPUT", "FORWARD", "OUTPUT"}

// firewallChain reports whether a chain is UFW's or Darkpanel's (spec 2.3).
func firewallChain(name string) bool {
	return strings.HasPrefix(name, "ufw-") || strings.HasPrefix(name, "ufw6-") || name == "DOCKER-USER" ||
		strings.HasPrefix(name, "darkpanel-") || strings.HasPrefix(name, "dp-b-")
}

// ownJump reports whether r is a bare jump, with no matches, to a UFW or
// Darkpanel chain. A jump with matches is another program's rule even when
// it targets such a chain.
func ownJump(r netfilter.Rule, ipv6 bool) bool {
	return firewallChain(r.Target.Name) && netfilter.Key(r, ipv6) == "-j "+r.Target.Name
}

// imageHolds reports whether a before-image holds chain c of table.
func imageHolds(table string, c netfilter.Chain) bool {
	return firewallChain(c.Name) || (table == "filter" && c.Builtin && slices.Contains(builtinChains, c.Name))
}

// takeImage takes the before-image of st.
func takeImage(st fwstate.Loaded) *beforeImage {
	return &beforeImage{V4: imageTables(st.V4, false), V6: imageTables(st.V6, true)}
}

func imageTables(rs *netfilter.Ruleset, ipv6 bool) []imageTable {
	if rs == nil {
		return nil
	}
	var out []imageTable
	for _, t := range rs.Tables {
		it := imageTable{Name: t.Name}
		for _, c := range t.Chains {
			if !imageHolds(t.Name, c) {
				continue
			}
			ic := imageChain{Name: c.Name, Builtin: c.Builtin, Policy: c.Policy, Rules: []imageRule{}}
			for _, r := range c.Rules {
				ir := imageRule{Text: r.Text, Key: netfilter.Key(r, ipv6), Own: ownJump(r, ipv6)}
				switch {
				case r.Target.Chain:
					ir.Jump = r.Target.Name
				case r.Goto != "" && t.Chain(r.Goto) != nil:
					ir.Jump = r.Goto
				}
				ic.Rules = append(ic.Rules, ir)
			}
			it.Chains = append(it.Chains, ic)
		}
		if len(it.Chains) > 0 {
			out = append(out, it)
		}
	}
	return out
}

// planImage decides how to load img over the current loaded state st. A
// user chain is always loaded: *-restore -n flushes and refills a chain it
// declares. A built-in chain is not flushed by a declaration, so it is
// flushed explicitly, and only when its rules other than bare jumps to UFW
// and Darkpanel chains equal the image's; otherwise it is left as it is and
// listed in Excluded, so no other program's rule is overwritten. A chain
// that jumps to a chain the load would leave missing is excluded too. A family
// the current state lacks is left out with all its built-in chains
// excluded.
func planImage(img *beforeImage, st fwstate.Loaded) imagePlan {
	var p imagePlan
	p.Restore4 = planFamily(img.V4, st.V4, fwstate.V4, false, &p.Excluded)
	p.Restore6 = planFamily(img.V6, st.V6, fwstate.V6, true, &p.Excluded)
	return p
}

func planFamily(tables []imageTable, cur *netfilter.Ruleset, fam fwstate.Family, ipv6 bool, excluded *[]chainRef) []byte {
	if len(tables) == 0 {
		return nil
	}
	if cur == nil {
		for _, t := range tables {
			for _, c := range t.Chains {
				*excluded = append(*excluded, chainRef{fam, t.Name, c.Name})
			}
		}
		return nil
	}
	var b bytes.Buffer
	for _, t := range tables {
		skip := skippedChains(t, cur, ipv6)
		var decl, flush, rules []string
		for _, c := range t.Chains {
			if skip[c.Name] {
				*excluded = append(*excluded, chainRef{fam, t.Name, c.Name})
				continue
			}
			if c.Builtin {
				decl = append(decl, fmt.Sprintf(":%s %s [0:0]", c.Name, c.Policy))
				flush = append(flush, "-F "+c.Name)
			} else {
				decl = append(decl, fmt.Sprintf(":%s - [0:0]", c.Name))
			}
			for _, r := range c.Rules {
				rules = append(rules, r.Text)
			}
		}
		if len(decl) == 0 {
			continue
		}
		b.WriteString("*" + t.Name + "\n")
		for _, l := range slices.Concat(decl, flush, rules) {
			b.WriteString(l + "\n")
		}
		b.WriteString("COMMIT\n")
	}
	if b.Len() == 0 {
		return nil
	}
	return b.Bytes()
}

// skippedChains returns the chains of t the load leaves out: a built-in
// chain whose other rules changed, and, repeated until nothing changes, any
// chain with a rule that jumps or goes to a chain neither loaded nor
// current, because *-restore -n rejects the whole table for such a rule.
func skippedChains(t imageTable, cur *netfilter.Ruleset, ipv6 bool) map[string]bool {
	skip := map[string]bool{}
	loaded := map[string]bool{}
	for _, c := range t.Chains {
		if c.Builtin && !foreignEqual(c, cur, t.Name, ipv6) {
			skip[c.Name] = true
		}
		loaded[c.Name] = true
	}
	for changed := true; changed; {
		changed = false
		for _, c := range t.Chains {
			if skip[c.Name] {
				continue
			}
			for _, r := range c.Rules {
				if j := r.Jump; j != "" && (!loaded[j] || skip[j]) && chainOf(cur, t.Name, j) == nil {
					skip[c.Name], changed = true, true
					break
				}
			}
		}
	}
	return skip
}

// foreignEqual reports whether the current built-in chain's rules other
// than bare jumps to UFW and Darkpanel chains equal, in order, the image's.
func foreignEqual(c imageChain, cur *netfilter.Ruleset, table string, ipv6 bool) bool {
	var want []string
	for _, r := range c.Rules {
		if !r.Own {
			want = append(want, r.Key)
		}
	}
	var got []string
	if ch := chainOf(cur, table, c.Name); ch != nil {
		for _, r := range ch.Rules {
			if !ownJump(r, ipv6) {
				got = append(got, netfilter.Key(r, ipv6))
			}
		}
	}
	return slices.Equal(want, got)
}

func chainOf(rs *netfilter.Ruleset, table, chain string) *netfilter.Chain {
	if rs == nil {
		return nil
	}
	t := rs.Table(table)
	if t == nil {
		return nil
	}
	return t.Chain(chain)
}

// imageMismatch lists the chains of img, except those excluded, whose
// policy or rules in st differ from the image. A chain of st that the image
// lacks, created after the image was taken, is not listed: *-restore -n
// cannot remove a chain, and once the image's built-in chains are loaded
// nothing jumps to it. A built-in chain that keeps a jump to it is in
// excluded, which fails the restore on its own.
func imageMismatch(img *beforeImage, st fwstate.Loaded, excluded []chainRef) []chainRef {
	var out []chainRef
	check := func(tables []imageTable, cur *netfilter.Ruleset, fam fwstate.Family, ipv6 bool) {
		for _, t := range tables {
			for _, c := range t.Chains {
				ref := chainRef{fam, t.Name, c.Name}
				if slices.Contains(excluded, ref) {
					continue
				}
				ch := chainOf(cur, t.Name, c.Name)
				if ch == nil || (c.Builtin && ch.Policy != c.Policy) || len(ch.Rules) != len(c.Rules) {
					out = append(out, ref)
					continue
				}
				for i, r := range ch.Rules {
					if netfilter.Key(r, ipv6) != c.Rules[i].Key {
						out = append(out, ref)
						break
					}
				}
			}
		}
	}
	check(img.V4, st.V4, fwstate.V4, false)
	check(img.V6, st.V6, fwstate.V6, true)
	return out
}

// restoreProgram is the *-restore program of the iptables variant UFW uses
// for a family.
func restoreProgram(fam fwstate.Family, variant string) string {
	name := "iptables"
	if fam == fwstate.V6 {
		name = "ip6tables"
	}
	return "/usr/sbin/" + name + "-" + variant + "-restore"
}
