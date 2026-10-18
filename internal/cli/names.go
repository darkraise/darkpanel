package cli

import (
	"errors"
	"fmt"
	"slices"
	"strings"

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
// starts with a reserved top-level word.
func entries(reg *core.Registry, reserved []string) ([]entry, error) {
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
	if err := checkEntries(es, reserved); err != nil {
		return nil, err
	}
	return es, nil
}

// checkEntries refuses entries whose commands would clash.
func checkEntries(es []entry, reserved []string) error {
	var errs []error
	owners := map[string]string{}
	for _, e := range es {
		p := strings.Join(e.Path, " ")
		if slices.Contains(reserved, e.Path[0]) {
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
