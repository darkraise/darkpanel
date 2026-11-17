package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var conventionalSubject = regexp.MustCompile(`^([a-z]+)(\([^)]*\))?(!)?: `)

// commitKind is what one commit message contributes to the next version.
type commitKind struct {
	typ      string
	breaking bool
	ships    bool
}

// parseCommit reads a commit message as a Conventional Commit. A subject that
// is not conventional, other than git's own `Revert "..."`, never ships, even
// with a breaking-change footer: only typed commits are counted.
func parseCommit(msg string) commitKind {
	subject, body, _ := strings.Cut(msg, "\n")
	var k commitKind
	if m := conventionalSubject.FindStringSubmatch(subject); m != nil {
		k.typ = m[1]
		k.breaking = m[3] == "!"
	} else if strings.HasPrefix(subject, `Revert "`) {
		k.typ = "revert"
	} else {
		return k
	}
	for line := range strings.SplitSeq(body, "\n") {
		if strings.HasPrefix(line, "BREAKING CHANGE:") || strings.HasPrefix(line, "BREAKING-CHANGE:") {
			k.breaking = true
		}
	}
	switch k.typ {
	case "feat", "fix", "perf", "refactor", "revert":
		k.ships = true
	default:
		k.ships = k.breaking
	}
	return k
}

// nextVersion returns the version a release of messages takes after current,
// or "" when none of them ships. A pre-release stays on its line and only
// moves its last numeric identifier, whatever the commits: leaving a
// pre-release line is a human decision.
func nextVersion(current version, messages []string) (string, error) {
	var ships, breaking, feat bool
	for _, msg := range messages {
		k := parseCommit(msg)
		if !k.ships {
			continue
		}
		ships = true
		breaking = breaking || k.breaking
		feat = feat || k.typ == "feat"
	}
	if !ships {
		return "", nil
	}
	if len(current.prerelease) > 0 {
		pre := current.prerelease
		for i := len(pre) - 1; i >= 0; i-- {
			if !isDigits(pre[i]) {
				continue
			}
			n, err := strconv.ParseUint(pre[i], 10, 64)
			if err != nil {
				return "", fmt.Errorf("%s: %w", current.text, err)
			}
			next := append([]string(nil), pre...)
			next[i] = strconv.FormatUint(n+1, 10)
			c := current.core
			return fmt.Sprintf("%d.%d.%d-%s", c[0], c[1], c[2], strings.Join(next, ".")), nil
		}
		return "", fmt.Errorf("pre-release %s has no numeric identifier to increment", current.text)
	}
	c := current.core
	switch {
	case breaking:
		return fmt.Sprintf("%d.0.0", c[0]+1), nil
	case feat:
		return fmt.Sprintf("%d.%d.0", c[0], c[1]+1), nil
	}
	return fmt.Sprintf("%d.%d.%d", c[0], c[1], c[2]+1), nil
}
