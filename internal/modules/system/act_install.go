package system

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// installNoun is the noun of an install's effect, whose identity is the
// installed set, comma-joined and sorted.
const installNoun = ID + ".packages"

// InstallInput is the input of system.packages.install.
type InstallInput struct {
	Names []string `json:"names" jsonschema_description:"The packages to install, as the configured sources name them."`
}

func (m module) installAction() core.ActionDef {
	return core.DefineTask[InstallInput, Applied](core.ActionSpec{
		Name: ID + ".packages.install", Version: 1,
		Summary:      "Install named packages; undo removes exactly the packages it installed.",
		Class:        core.ClassCompensable,
		Side:         core.SideAgent,
		LockKeys:     []string{aptLockKey},
		ApplyTimeout: 30 * time.Minute,
		Rechecks:     []string{"docker"},
	}, installHandler{m: m})
}

type installHandler struct{ m module }

func (h installHandler) Plan(ctx context.Context, env core.Env, in InstallInput) (core.PlanOutcome, error) {
	if len(in.Names) == 0 {
		return core.PlanOutcome{}, errcode.New(errcode.InvalidInput, "list the packages to install")
	}
	out, sim, err := h.m.planSet(ctx, env, "install", in.Names, nil)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	if len(sim.Removals()) > 0 {
		out.Diff = append(out.Diff, core.DiffLine{Op: " ", Text: "Undo does not reinstall the removed packages."})
	}
	return out, nil
}

// Task journals one effect for the set the plan installs, so the undo
// removes that set and nothing it upgraded or removed.
func (h installHandler) Task(ctx context.Context, env core.Env, in InstallInput) (Applied, error) {
	defer invalidateUpdates(env.HostID)
	if len(in.Names) == 0 {
		return Applied{}, core.Definite(errcode.New(errcode.InvalidInput, "list the packages to install"))
	}
	planned, err := h.m.simulateSet(ctx, env, "install", in.Names, nil)
	if err != nil {
		return Applied{}, core.Definite(err)
	}
	before, err := installedVersions(ctx, env)
	if err != nil {
		return Applied{}, core.Definite(err)
	}
	// The effect names packages by name, so another architecture's copy of
	// a package already installed is left out: its undo would remove the
	// copy that was there before.
	var installs []string
	for _, n := range planned.Installs() {
		if base, _, _ := strings.Cut(n, ":"); before[base] == "" {
			installs = append(installs, n)
		}
	}
	if len(installs) == 0 {
		return runSet(ctx, env, "install", in.Names, planned)
	}
	seq, err := env.Effects.Intend(ctx, installEffect(installs), nil)
	if err != nil {
		return Applied{}, err
	}
	applied, err := runSet(ctx, env, "install", in.Names, planned)
	if err != nil {
		if core.IsDefinite(err) {
			if serr := env.Effects.Skipped(ctx, seq); serr != nil {
				return Applied{}, serr
			}
		}
		// Otherwise the effect stays intended and the engine settles it
		// through Observe.
		return applied, err
	}
	set := installs
	if len(applied.Installed) > 0 {
		set = applied.Installed
	}
	post, err := installState(ctx, env, set)
	if err != nil {
		return applied, err
	}
	if err := env.Effects.Made(ctx, seq, installResource(set), post); err != nil {
		return applied, err
	}
	return applied, nil
}

// Verify checks the named packages are installed, and the reported sets.
func (h installHandler) Verify(ctx context.Context, env core.Env, in InstallInput, out Applied) error {
	versions, err := installedVersions(ctx, env)
	if err != nil {
		return err
	}
	for _, n := range in.Names {
		if _, ok := versions[n]; !ok {
			return fmt.Errorf("system: %s is not installed", n)
		}
	}
	return verifyApplied(ctx, env, out)
}

// installEffect is the effect of installing set: none of it was there, and
// some of it is after.
func installEffect(set []string) core.Effect {
	return core.Effect{
		Kind: core.EffectModify, Resource: installResource(set), Local: true, InverseLocal: true,
		Before: core.StateDoc{State: json.RawMessage(`{"present":false}`)},
		After:  &core.StateDoc{State: json.RawMessage(`{"present":true}`)},
	}
}

// installResource names set by package: apt qualifies another
// architecture's package ("libfoo:i386"), dpkg-query, which observes it,
// does not.
func installResource(set []string) string {
	s := make([]string, len(set))
	for i, n := range set {
		s[i], _, _ = strings.Cut(n, ":")
	}
	slices.Sort(s)
	return installNoun + ":" + strings.Join(slices.Compact(s), ",")
}

// installSet is the package set an install effect names.
func installSet(e core.Effect) ([]string, error) {
	noun, identity, _ := strings.Cut(e.Resource, ":")
	set := strings.Split(identity, ",")
	if noun != installNoun || slices.ContainsFunc(set, func(n string) bool { return !namePattern.MatchString(n) }) {
		return nil, fmt.Errorf("system: %q is not a package set system.packages.install makes", e.Resource)
	}
	return set, nil
}

// installState is whether any package of set is installed; the versions of
// those that are go in Version, which no undo compares.
func installState(ctx context.Context, env core.Env, set []string) (core.StateDoc, error) {
	present, err := presentOf(ctx, env, set)
	if err != nil {
		return core.StateDoc{}, err
	}
	state := json.RawMessage(`{"present":false}`)
	if len(present) > 0 {
		state = json.RawMessage(`{"present":true}`)
	}
	version, err := json.Marshal(present)
	if err != nil {
		return core.StateDoc{}, err
	}
	return core.StateDoc{State: state, Version: version}, nil
}

// presentOf maps the installed packages of set to their versions.
func presentOf(ctx context.Context, env core.Env, set []string) (map[string]string, error) {
	versions, err := installedVersions(ctx, env)
	if err != nil {
		return nil, err
	}
	present := map[string]string{}
	for _, n := range set {
		if v, ok := versions[n]; ok {
			present[n] = v
		}
	}
	return present, nil
}

func (h installHandler) Observe(ctx context.Context, env core.Env, e core.Effect) (core.Observation, error) {
	set, err := installSet(e)
	if err != nil {
		return core.Observation{}, err
	}
	cur, err := installState(ctx, env, set)
	if err != nil {
		return core.Observation{}, err
	}
	return core.Observation{Current: cur, Resource: e.Resource, Matches: 1, Exclusive: true}, nil
}

// Inverse removes the recorded packages still installed. It refuses, having
// changed nothing, when removing them would remove anything more or a
// protected package: something installed since depends on them.
func (h installHandler) Inverse(ctx context.Context, env core.Env, e core.Effect) (core.InverseResult, error) {
	defer invalidateUpdates(env.HostID)
	set, err := installSet(e)
	if err != nil {
		return core.InverseResult{}, core.Definite(err)
	}
	present, err := presentOf(ctx, env, set)
	if err != nil {
		return core.InverseResult{}, core.Definite(err)
	}
	if len(present) == 0 {
		return core.InverseResult{}, nil
	}
	names := slices.Sorted(func(yield func(string) bool) {
		for n := range present {
			if !yield(n) {
				return
			}
		}
	})
	sim, err := aptPlan(ctx, env, "remove", names)
	if err != nil {
		return core.InverseResult{}, core.Definite(err)
	}
	if err := h.m.undoRemovalRefusal(ctx, env, names, sim, nil); err != nil {
		return core.InverseResult{}, core.Definite(err)
	}
	if _, err := runSet(ctx, env, "remove", names, sim); err != nil {
		return core.InverseResult{}, err
	}
	return core.InverseResult{}, nil
}

// undoRemovalRefusal refuses an undo's removal of names when sim, apt's simulation of it, also removes another
// package, or a protected one not in exempt: something installed since depends on them.
func (m module) undoRemovalRefusal(ctx context.Context, env core.Env, names []string, sim Sim, exempt []string) error {
	var extra []string
	for _, r := range sim.Removals() {
		base, _, _ := strings.Cut(r, ":")
		if !slices.Contains(names, base) {
			extra = append(extra, r)
		}
	}
	protected, err := m.protectedRemovals(ctx, env, sim.Removals())
	if err != nil {
		return err
	}
	protected = withoutExempt(protected, exempt)
	if len(extra) == 0 && len(protected) == 0 {
		return nil
	}
	why := make([]string, 0, len(extra)+len(protected))
	why = append(why, extra...)
	for _, p := range protected {
		why = append(why, p.Name+" ("+p.Reason+")")
	}
	return errcode.New(errcode.SystemRemovalRefused,
		"removing the installed packages would also remove "+strings.Join(why, ", ")+"; remove them by hand if that is intended")
}
