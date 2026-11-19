package system

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// restartingPackages restart the Docker daemon, and with it every running
// container, when they upgrade.
var restartingPackages = []string{"docker-ce", "containerd.io"}

// planSet plans a package change: it checks the names, simulates verb and
// refuses a simulation that removes a protected package not in exempt. The
// diff lists installs, upgrades and, in their own group, removals; the
// state hash covers the simulated set. The caller sets the confirmation.
func (m module) planSet(ctx context.Context, env core.Env, verb string, names []string, exempt []string) (core.PlanOutcome, Sim, error) {
	sim, err := m.simulateSet(ctx, env, verb, names, exempt)
	if err != nil {
		return core.PlanOutcome{}, Sim{}, err
	}
	diff := setDiff(sim)
	if line, ok := m.restartLine(ctx, sim); ok {
		diff = append(diff, line)
	}
	sum := sha256.Sum256([]byte(setKey(sim)))
	return core.PlanOutcome{Diff: diff, StateHash: hex.EncodeToString(sum[:])}, sim, nil
}

// simulateSet is planSet without the diff: the checks and the simulation. A
// task calls it to learn the set its confirmed plan showed, which the
// engine's state-hash recheck has just found unchanged.
func (m module) simulateSet(ctx context.Context, env core.Env, verb string, names []string, exempt []string) (Sim, error) {
	if len(names) > 0 {
		kind := nameInstalled
		if verb == "install" {
			kind = nameAvailable
		}
		if err := validateNames(ctx, env, names, kind); err != nil {
			return Sim{}, err
		}
	}
	sim, err := aptPlan(ctx, env, verb, names)
	if err != nil {
		return Sim{}, err
	}
	protected, err := m.protectedRemovals(ctx, env, sim.Removals())
	if err != nil {
		return Sim{}, err
	}
	protected = withoutExempt(protected, exempt)
	if len(protected) > 0 {
		list := make([]string, len(protected))
		for i, p := range protected {
			list[i] = p.Name + " (" + p.Reason + ")"
		}
		return Sim{}, errcode.New(errcode.SystemProtectedPackage, "apt would remove protected packages: "+strings.Join(list, ", "))
	}
	return sim, nil
}

// withoutExempt drops from protected the packages exempt names.
func withoutExempt(protected []Protected, exempt []string) []Protected {
	return slices.DeleteFunc(protected, func(p Protected) bool {
		base, _, _ := strings.Cut(p.Name, ":")
		return slices.Contains(exempt, base)
	})
}

// runSet runs verb once the task's own re-simulation, which waits for a busy
// apt as the run does, still shows the planned set; a different set refuses
// with system.plan_stale before anything changes. It drops the updates cache
// on every exit.
func runSet(ctx context.Context, env core.Env, verb string, names []string, planned Sim) (Applied, error) {
	defer invalidateUpdates(env.HostID)
	now, err := aptResim(ctx, env, verb, names)
	if err != nil {
		return Applied{}, core.Definite(err)
	}
	if setKey(now) != setKey(planned) {
		return Applied{}, core.Definite(errcode.New(errcode.SystemPlanStale, staleDetail(planned, now)))
	}
	applied, _, err := aptRun(ctx, env, verb, names)
	if err != nil {
		if errcode.From(err).Code == errcode.SystemAptBusy {
			// apt gave up waiting for the lock before changing anything.
			return Applied{}, core.Definite(err)
		}
		return applied, err
	}
	return applied, nil
}

// setOps are the simulation's installs, upgrades and removals, one string
// each, sorted; Conf lines follow from the Inst lines and are left out.
func setOps(sim Sim) []string {
	var ops []string
	for _, op := range sim.Ops {
		switch op.Kind {
		case "Inst":
			ops = append(ops, "Inst "+op.Name+" "+op.From+" "+op.To)
		case "Remv", "Purg":
			ops = append(ops, op.Kind+" "+op.Name+" "+op.From)
		}
	}
	slices.Sort(ops)
	return slices.Compact(ops)
}

func setKey(sim Sim) string { return strings.Join(setOps(sim), "\n") }

func staleDetail(planned, now Sim) string {
	was, is := setOps(planned), setOps(now)
	var added, dropped []string
	for _, op := range is {
		if !slices.Contains(was, op) {
			added = append(added, op)
		}
	}
	for _, op := range was {
		if !slices.Contains(is, op) {
			dropped = append(dropped, op)
		}
	}
	return fmt.Sprintf("the packages apt would change are no longer the planned ones (now also: %s; no longer: %s); plan the change again",
		listOrNone(added), listOrNone(dropped))
}

func listOrNone(l []string) string {
	if len(l) == 0 {
		return "none"
	}
	return strings.Join(l, ", ")
}

// setDiff lists the simulation's installs and upgrades, then its removals as
// their own group.
func setDiff(sim Sim) []core.DiffLine {
	ops := slices.Clone(sim.Ops)
	slices.SortStableFunc(ops, func(a, b SimOp) int { return cmp.Compare(a.Name, b.Name) })
	var adds, removes []core.DiffLine
	for _, op := range ops {
		switch {
		case op.Kind == "Inst" && op.From == "":
			adds = append(adds, core.DiffLine{Op: "+", Text: "Install: " + op.Name + " " + op.To})
		case op.Kind == "Inst":
			adds = append(adds, core.DiffLine{Op: "+", Text: "Upgrade: " + op.Name + " " + op.From + " → " + op.To})
		case op.Kind == "Remv":
			removes = append(removes, core.DiffLine{Op: "-", Text: "Remove: " + op.Name + " " + op.From})
		case op.Kind == "Purg":
			removes = append(removes, core.DiffLine{Op: "-", Text: "Remove: " + op.Name + " " + op.From + ", and purge its configuration"})
		}
	}
	if len(adds)+len(removes) == 0 {
		return []core.DiffLine{{Op: " ", Text: "apt has nothing to change"}}
	}
	return append(adds, removes...)
}

// restartLine is the number of running containers an upgrade of the Docker
// daemon restarts; none when nothing restarts it or the daemon does not
// answer.
func (m module) restartLine(ctx context.Context, sim Sim) (core.DiffLine, bool) {
	if !slices.ContainsFunc(sim.Upgrades(), func(n string) bool { return slices.Contains(restartingPackages, n) }) {
		return core.DiffLine{}, false
	}
	info, err := m.d.docker.Info(ctx)
	if err != nil {
		return core.DiffLine{}, false
	}
	text := fmt.Sprintf("%d running containers will restart", info.ContainersRunning)
	if info.ContainersRunning == 1 {
		text = "1 running container will restart"
	}
	return core.DiffLine{Op: " ", Text: text}, true
}
