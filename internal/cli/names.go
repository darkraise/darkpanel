package cli

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/darkraise/darkpanel/internal/core"
)

// handWritten are the top-level commands that no generated command may
// take: those this package writes, cobra's own, and those the assembly
// passes in Options.Commands (spec section 15).
var handWritten = []string{"serve", "version", "change", "controller", "help", "completion"}

// CommandPath is the words after "darkpanel" of the command generated for
// an action or resource name (spec section 15): action <module>.<noun>.<verb>
// is [<module> <noun> <verb>], resource <module>.<resource> is
// [<module> <resource> get], and module "core" drops its segment.
func CommandPath(name string) []string {
	words := strings.Split(name, ".")
	if len(words) == 2 {
		words = append(words, "get")
	}
	if words[0] == core.ModuleIDCore {
		words = words[1:]
	}
	return words
}

// entry is one generated command: an action at its highest version, or a
// resource.
type entry struct {
	Name     string
	Path     []string
	Action   *core.ActionDef
	Resource *core.ResourceDef
}

// entries returns the generated commands of reg sorted by path, or an error
// when two of them share a path, one's path is a group of another's, or one
// not in adopted starts with a reserved top-level word.
func entries(reg *core.Registry, reserved []string, adopted map[string]bool) ([]entry, error) {
	latest := map[string]core.ActionDef{}
	for _, a := range reg.Actions() {
		latest[a.Spec.Name] = a // sorted by name and version, so the last one wins
	}
	var es []entry
	for name, a := range latest {
		es = append(es, entry{Name: name, Path: CommandPath(name), Action: &a})
	}
	for _, r := range reg.Resources() {
		es = append(es, entry{Name: r.Name, Path: CommandPath(r.Name), Resource: &r})
	}
	slices.SortFunc(es, func(a, b entry) int { return slices.Compare(a.Path, b.Path) })
	if err := checkEntries(es, reserved, adopted); err != nil {
		return nil, err
	}
	return es, nil
}

// checkEntries refuses entries whose commands would clash; an adopted entry
// may start with a reserved word, because its hand-written group takes it.
func checkEntries(es []entry, reserved []string, adopted map[string]bool) error {
	var errs []error
	owners := map[string]string{}
	for _, e := range es {
		p := strings.Join(e.Path, " ")
		if slices.Contains(reserved, e.Path[0]) && !adopted[e.Name] {
			errs = append(errs, fmt.Errorf("%s: command \"darkpanel %s\" clashes with the hand-written command %q", e.Name, p, e.Path[0]))
		}
		if other, ok := owners[p]; ok {
			errs = append(errs, fmt.Errorf("%s and %s both map to \"darkpanel %s\"", other, e.Name, p))
		}
		owners[p] = e.Name
	}
	for _, e := range es {
		for i := 1; i < len(e.Path); i++ {
			if other, ok := owners[strings.Join(e.Path[:i], " ")]; ok {
				errs = append(errs, fmt.Errorf("\"darkpanel %s\" is the command of %s and is also a command group of %s",
					strings.Join(e.Path[:i], " "), other, e.Name))
			}
		}
	}
	return errors.Join(errs...)
}

// adopted is the set of every action name some hand-written group adopts.
func adopted(adopt map[string][]string) map[string]bool {
	out := map[string]bool{}
	for _, names := range adopt {
		for _, name := range names {
			out[name] = true
		}
	}
	return out
}

// checkHooks refuses an Adopt or LocalFallbacks entry that names no
// hand-written group, no registered action, an action outside its group, an
// adopted command a hand-written one already takes, or a fallback for an
// action the agent does not run.
func checkHooks(opts Options) error {
	sides := map[string]core.Side{}
	for _, a := range opts.Registry.Actions() {
		sides[a.Spec.Name] = a.Spec.Side
	}
	groups := map[string]*cobra.Command{}
	for _, c := range opts.Commands {
		groups[c.Name()] = c
	}
	var errs []error
	for _, name := range slices.Sorted(maps.Keys(opts.Adopt)) {
		g, ok := groups[name]
		if !ok {
			errs = append(errs, fmt.Errorf("adopting group %q is not a hand-written command", name))
			continue
		}
		taken := map[string]bool{}
		for _, c := range g.Commands() {
			taken[c.Name()] = true
		}
		for _, action := range opts.Adopt[name] {
			path := CommandPath(action)
			switch {
			case sides[action] == "":
				errs = append(errs, fmt.Errorf("%s adopts %s, which is not a registered action", name, action))
			case path[0] != name:
				errs = append(errs, fmt.Errorf("%s adopts %s, whose command \"darkpanel %s\" is not under it",
					name, action, strings.Join(path, " ")))
			case taken[path[1]]:
				errs = append(errs, fmt.Errorf("%s would replace the hand-written command \"darkpanel %s %s\"",
					action, name, path[1]))
			}
		}
	}
	for _, action := range slices.Sorted(maps.Keys(opts.LocalFallbacks)) {
		if sides[action] != core.SideAgent {
			errs = append(errs, fmt.Errorf("local fallback %s is not a registered agent-side action", action))
		}
	}
	return errors.Join(errs...)
}
