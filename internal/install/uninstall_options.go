package install

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// optionKey is the pattern of an uninstall option's key (spec 1.11).
var optionKey = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)

// moduleOptions checks the uninstall options each core.Uninstaller of mods
// declares, then the --module-option values in given. It returns every
// Uninstaller's options with each value filled, the given one or the
// default, and the plan's lines, one per module and option in order. A
// value for a module that is not compiled in or has no uninstall step, an
// undeclared key and a value outside the option's values are
// request.invalid_input; a module that declares a broken option is a plain
// error naming the module, because the binary is at fault, not the operator.
func moduleOptions(mods []core.Module, given map[string]map[string]string) (map[string]map[string]string, []string, error) {
	compiled := map[string]bool{}
	declared := map[string]map[string]core.UninstallOption{}
	for _, m := range mods {
		id := m.Manifest().ID
		compiled[id] = true
		un, ok := m.(core.Uninstaller)
		if !ok {
			continue
		}
		opts := map[string]core.UninstallOption{}
		for _, o := range un.UninstallOptions() {
			if err := checkOption(o); err != nil {
				return nil, nil, fmt.Errorf("install: module %s: %w", id, err)
			}
			if _, dup := opts[o.Key]; dup {
				return nil, nil, fmt.Errorf("install: module %s: the uninstall option %s is declared twice", id, o.Key)
			}
			opts[o.Key] = o
		}
		declared[id] = opts
	}
	filled := map[string]map[string]string{}
	for id, opts := range declared {
		filled[id] = map[string]string{}
		for key, o := range opts {
			filled[id][key] = o.Default
		}
	}
	for _, id := range slices.Sorted(maps.Keys(given)) {
		opts, ok := declared[id]
		switch {
		case !compiled[id]:
			return nil, nil, errcode.New(errcode.InvalidInput, fmt.Sprintf("--module-option: module %s is not compiled into this binary", id))
		case !ok:
			return nil, nil, errcode.New(errcode.InvalidInput, fmt.Sprintf("--module-option: module %s has no uninstall step", id))
		}
		for _, key := range slices.Sorted(maps.Keys(given[id])) {
			o, ok := opts[key]
			if !ok {
				return nil, nil, errcode.New(errcode.InvalidInput, fmt.Sprintf("--module-option: module %s has no uninstall option %s", id, key))
			}
			v := given[id][key]
			if !slices.Contains(o.Values, v) {
				return nil, nil, errcode.New(errcode.InvalidInput, fmt.Sprintf("--module-option %s.%s=%s: the value must be one of %s",
					id, key, v, strings.Join(o.Values, ", ")))
			}
			filled[id][key] = v
		}
	}
	var lines []string
	for _, id := range slices.Sorted(maps.Keys(declared)) {
		for _, key := range slices.Sorted(maps.Keys(declared[id])) {
			o := declared[id][key]
			lines = append(lines, fmt.Sprintf("%s.%s=%s (default %s; %s)", id, key, filled[id][key], o.Default, o.Help))
		}
	}
	return filled, lines, nil
}

// checkOption refuses a declared option that breaks the rules of
// core.UninstallOption.
func checkOption(o core.UninstallOption) error {
	switch {
	case !optionKey.MatchString(o.Key):
		return fmt.Errorf("the uninstall option key %q does not match %s", o.Key, optionKey)
	case len(o.Values) == 0:
		return fmt.Errorf("the uninstall option %s has no values", o.Key)
	case !slices.Contains(o.Values, o.Default):
		return fmt.Errorf("the default %q of the uninstall option %s is not one of its values", o.Default, o.Key)
	case strings.ContainsAny(o.Help, "\r\n"):
		return fmt.Errorf("the help of the uninstall option %s is more than one line", o.Key)
	}
	return nil
}
