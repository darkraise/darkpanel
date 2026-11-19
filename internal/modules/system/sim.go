// Package system is the host system module: packages and updates, automatic
// updates, reboots and Docker Engine (spec 4).
package system

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// The refusals of ParseSim; a plan that meets either is refused with
// system.sim_unparsed.
var (
	ErrSimUnknownLine   = errors.New("system: apt simulation has an action line this parser does not know")
	ErrSimCountMismatch = errors.New("system: apt simulation's action lines do not match its summary line")
)

// SimOp is one action line of an apt simulation. Kind is Inst, Conf, Remv
// or Purg; From is the installed version, To the version apt would install,
// and Origins the archives apt names for it, such as
// "Ubuntu:22.04/jammy-security".
type SimOp struct {
	Kind, Name, From, To string
	Origins              []string
}

// Sim is a parsed apt simulation: its action lines and its summary counts.
type Sim struct {
	Ops                                       []SimOp
	Upgraded, Installed, Removed, NotUpgraded int
}

var (
	simSummary = regexp.MustCompile(`^(\d+) upgraded, (\d+) newly installed, (\d+) to remove and (\d+) not upgraded\.$`)
	simAction  = regexp.MustCompile(`^[A-Z][a-z]{3} `)
)

// ParseSim parses the output of apt-get -s. It knows the action lines Inst,
// Conf, Remv and Purg; another capitalised four-letter action word is
// ErrSimUnknownLine, and other prose is ignored. The summary line is
// required, once, and its upgraded, newly installed and to-remove counts
// must equal the parsed sets, or ErrSimCountMismatch.
func ParseSim(out []byte) (Sim, error) {
	var sim Sim
	summaries := 0
	for line := range strings.Lines(string(out)) {
		line = strings.TrimRight(line, " \r\n")
		if m := simSummary.FindStringSubmatch(line); m != nil {
			summaries++
			sim.Upgraded, _ = strconv.Atoi(m[1])
			sim.Installed, _ = strconv.Atoi(m[2])
			sim.Removed, _ = strconv.Atoi(m[3])
			sim.NotUpgraded, _ = strconv.Atoi(m[4])
			continue
		}
		if !simAction.MatchString(line) {
			continue
		}
		op, ok := parseSimOp(line)
		if !ok {
			return Sim{}, fmt.Errorf("%w: %q", ErrSimUnknownLine, line)
		}
		sim.Ops = append(sim.Ops, op)
	}
	switch {
	case summaries == 0:
		return Sim{}, fmt.Errorf("%w: no summary line", ErrSimCountMismatch)
	case summaries > 1:
		return Sim{}, fmt.Errorf("%w: %d summary lines", ErrSimCountMismatch, summaries)
	}
	if up, in, rm := len(sim.Upgrades()), len(sim.Installs()), len(sim.Removals()); up != sim.Upgraded || in != sim.Installed || rm != sim.Removed {
		return Sim{}, fmt.Errorf("%w: parsed %d upgraded, %d installed, %d removed; the summary says %d, %d, %d",
			ErrSimCountMismatch, up, in, rm, sim.Upgraded, sim.Installed, sim.Removed)
	}
	return sim, nil
}

// parseSimOp parses "Kind name [from] (to …)": [from] and (to …) are each
// optional, as apt prints them.
func parseSimOp(line string) (SimOp, bool) {
	kind, rest, _ := strings.Cut(line, " ")
	if !slices.Contains([]string{"Inst", "Conf", "Remv", "Purg"}, kind) {
		return SimOp{}, false
	}
	name, rest, _ := strings.Cut(rest, " ")
	if name == "" {
		return SimOp{}, false
	}
	op := SimOp{Kind: kind, Name: name}
	if from, ok := strings.CutPrefix(rest, "["); ok {
		op.From, rest, _ = strings.Cut(from, "]")
		rest = strings.TrimLeft(rest, " ")
	}
	if to, ok := strings.CutPrefix(rest, "("); ok {
		op.To, _, _ = strings.Cut(to, " ")
		op.To = strings.TrimSuffix(op.To, ")")
		op.Origins = simOrigins(to)
	}
	return op, true
}

// simOrigins reads the archives of "(<to> <origin>, <origin> [<arch>])".
func simOrigins(paren string) []string {
	inner, _, _ := strings.Cut(paren, ")")
	_, rest, _ := strings.Cut(inner, " ")
	if i := strings.LastIndex(rest, "["); i >= 0 {
		rest = rest[:i]
	}
	var origins []string
	for o := range strings.SplitSeq(rest, ",") {
		if o = strings.TrimSpace(o); o != "" {
			origins = append(origins, o)
		}
	}
	return origins
}

// Installs is the sorted names of the packages apt would newly install.
func (s Sim) Installs() []string {
	return s.names(func(op SimOp) bool { return op.Kind == "Inst" && op.From == "" })
}

// Upgrades is the sorted names of the installed packages apt would replace
// with another version.
func (s Sim) Upgrades() []string {
	return s.names(func(op SimOp) bool { return op.Kind == "Inst" && op.From != "" })
}

// Removals is the sorted names of the packages apt would remove or purge.
func (s Sim) Removals() []string {
	return s.names(func(op SimOp) bool { return op.Kind == "Remv" || op.Kind == "Purg" })
}

func (s Sim) names(keep func(SimOp) bool) []string {
	names := []string{}
	for _, op := range s.Ops {
		if keep(op) {
			names = append(names, op.Name)
		}
	}
	slices.Sort(names)
	return names
}
