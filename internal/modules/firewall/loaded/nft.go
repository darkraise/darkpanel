package loaded

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"strings"
)

// maxNFTLine bounds one line of nft output; nft prints far shorter ones.
const maxNFTLine = 1 << 20

// nftTable is one "table <family> <name>" line of `nft list tables`.
type nftTable struct{ Family, Name string }

// nftChain is one chain of `nft list table <family> <name>`.
type nftChain struct {
	Name   string
	Base   bool   // declared with "type ... hook ..."
	Hook   string // the hook of a base chain: "input", "forward", "ingress", ...
	Policy string // a base chain's policy as printed ("accept", "drop"); "" when none is printed
	Rules  int    // statements in the chain; a statement nft prints over several lines counts once
}

// parseNFTTables reads `nft list tables`. A line that is not exactly
// "table <family> <name>" is returned in bad, so the caller can report what
// it could not read instead of skipping a table.
func parseNFTTables(out []byte) (tables []nftTable, bad []string) {
	for line := range strings.Lines(string(out)) {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		f := strings.Fields(line)
		if len(f) != 3 || f[0] != "table" {
			bad = append(bad, line)
			continue
		}
		tables = append(tables, nftTable{Family: f[1], Name: f[2]})
	}
	return tables, bad
}

// parseNFTTable reads the chains of one table from `nft list table`
// output. It reads only what deciding a foreign hook needs: each chain's
// name, whether it is a base chain with its hook and policy, and how many
// statements it holds. Sets, maps, flowtables and other objects are
// skipped. Output it cannot follow (unbalanced braces, an unterminated
// string, text outside the table block) is an error; no input panics.
func parseNFTTable(out []byte) ([]nftChain, error) {
	var (
		chains []nftChain
		cur    *nftChain // the chain whose block is open
		depth  int       // braces open before the current line
		seen   bool      // the table block has been opened
		lineNo int
	)
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 0, 64<<10), maxNFTLine)
	for sc.Scan() {
		lineNo++
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		opens, closes, err := nftBraces(line)
		if err != nil {
			return nil, fmt.Errorf("loaded: nft line %d: %w", lineNo, err)
		}
		switch {
		case depth == 0:
			if seen || !strings.HasPrefix(line, "table ") || opens != 1 || closes != 0 {
				return nil, fmt.Errorf("loaded: nft line %d: want one table block, got %q", lineNo, truncate(line))
			}
			seen = true
		case depth == 1 && strings.HasPrefix(line, "chain ") && opens == 1 && closes == 0:
			name := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(line, "chain "), "{"))
			chains = append(chains, nftChain{Name: strings.Trim(name, `"`)})
			cur = &chains[len(chains)-1]
		case depth == 2 && cur != nil && line != "}":
			nftStatement(cur, line)
		}
		depth += opens - closes
		if depth < 0 {
			return nil, fmt.Errorf("loaded: nft line %d: unbalanced braces", lineNo)
		}
		if depth <= 1 {
			cur = nil
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("loaded: nft output: %w", err)
	}
	if !seen || depth != 0 {
		return nil, errors.New("loaded: nft output: the table block is missing or not closed")
	}
	return chains, nil
}

// nftStatement reads one line at the top level of a chain block.
func nftStatement(c *nftChain, line string) {
	switch {
	case strings.HasPrefix(line, "type ") && strings.Contains(line, " hook "):
		c.Base = true
		c.Hook = nftWordAfter(line, "hook")
		if p := nftWordAfter(line, "policy"); p != "" {
			c.Policy = p
		}
	case strings.HasPrefix(line, "policy ") && c.Base:
		c.Policy = nftWordAfter(line, "policy")
	case strings.HasPrefix(line, "comment "), strings.HasPrefix(line, "devices "):
		// A chain's comment and a netdev chain's device list declare the
		// chain; they are not statements.
	default:
		c.Rules++
	}
}

// nftWordAfter returns the word after the first word key of line, without
// a trailing semicolon; "" when key is not followed by a word.
func nftWordAfter(line, key string) string {
	f := strings.Fields(line)
	for i := 0; i+1 < len(f); i++ {
		if f[i] == key {
			return strings.TrimSuffix(f[i+1], ";")
		}
	}
	return ""
}

// nftBraces counts the braces of line outside double-quoted strings.
func nftBraces(line string) (opens, closes int, err error) {
	quoted := false
	for i := 0; i < len(line); i++ {
		switch c := line[i]; {
		case c == '\\' && quoted:
			i++
		case c == '"':
			quoted = !quoted
		case c == '{' && !quoted:
			opens++
		case c == '}' && !quoted:
			closes++
		}
	}
	if quoted {
		return 0, 0, errors.New("unterminated string")
	}
	return opens, closes, nil
}
