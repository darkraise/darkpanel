package dockerfw

import (
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
