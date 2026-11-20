package system

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/platform"
)

const (
	aptGet    = "/usr/bin/apt-get"
	dpkgQuery = "/usr/bin/dpkg-query"
)

// Applied is what a mutating apt run changed, from the installed packages
// before and after it; never from apt's progress output.
type Applied struct {
	Installed []string `json:"installed"`
	Upgraded  []string `json:"upgraded"`
	Removed   []string `json:"removed"`
}

// aptRun runs the mutating vector of verb and returns what it changed and
// its output. A failed run is an errcode error with the code classifyApt
// gives and, as detail, the failing package and the last 40 lines.
func aptRun(ctx context.Context, env core.Env, verb string, names []string) (Applied, []byte, error) {
	before, err := installedVersions(ctx, env)
	if err != nil {
		return Applied{}, nil, err
	}
	out, err := aptExec(ctx, env, platform.SystemAptRun(verb, names))
	if err != nil {
		return Applied{}, out, err
	}
	after, err := installedVersions(ctx, env)
	if err != nil {
		return Applied{}, out, err
	}
	return diffInstalled(before, after), out, nil
}

// aptPlan runs the lock probe, then the plan simulation of verb. A root
// simulation takes no lock and ignores an interrupted dpkg, so the probe is
// what refuses a busy or interrupted apt; when it fails no simulation runs.
// A plan's probe gives up after 10 seconds; a task's (wait) after the
// mutating run's 120.
func aptPlan(ctx context.Context, env core.Env, verb string, names []string, wait bool) (Sim, error) {
	probe := platform.SystemAptCheck()
	if wait {
		probe = platform.SystemAptCheckWait()
	}
	if _, err := aptExec(ctx, env, probe); err != nil {
		return Sim{}, err
	}
	return aptSimulate(ctx, env, platform.SystemAptPlan(verb, names))
}

// aptResim is the task's own simulation just before its run. It runs no
// probe: apt-get check also fails on the broken dependencies a fix-install
// re-simulation must run on, and the run's 120-second lock wait covers a
// busy apt.
func aptResim(ctx context.Context, env core.Env, verb string, names []string) (Sim, error) {
	return aptSimulate(ctx, env, platform.SystemAptResim(verb, names))
}

// aptRead is a resource read's simulation, which takes no lock.
func aptRead(ctx context.Context, env core.Env, verb string, opts, names []string) (Sim, error) {
	return aptSimulate(ctx, env, platform.SystemAptRead(verb, opts, names))
}

func aptSimulate(ctx context.Context, env core.Env, args []string) (Sim, error) {
	out, err := aptExec(ctx, env, args)
	if err != nil {
		return Sim{}, err
	}
	sim, err := ParseSim(out)
	if err != nil {
		return Sim{}, errcode.Wrap(errcode.SystemSimUnparsed, err, "apt printed a simulation Darkpanel cannot read")
	}
	return sim, nil
}

// aptExec runs apt-get with args and returns its combined output; a
// non-zero exit is aptFailure.
func aptExec(ctx context.Context, env core.Env, args []string) ([]byte, error) {
	res, err := env.Exec.Run(ctx, core.Cmd{Path: aptGet, Args: args})
	out := slices.Concat(res.Stdout, res.Stderr)
	if err != nil {
		return out, fmt.Errorf("system: run apt-get: %w", err)
	}
	if res.ExitCode != 0 {
		return out, aptFailure(out)
	}
	return out, nil
}

// aptFailure is the errcode error of a failed apt or dpkg run's output.
func aptFailure(out []byte) error {
	pkg, tail := failureDetail(out)
	head := "apt failed"
	if pkg != "" {
		head += " on " + pkg
	}
	return errcode.New(classifyApt(out), head+":\n"+strings.Join(tail, "\n"))
}

// installedPackage is one line of platform.DpkgQueryFormat.
type installedPackage struct {
	Name, Version, Arch, Status, Essential, Priority, Summary string
}

// dpkgPackages lists every package dpkg knows, in any state.
func dpkgPackages(ctx context.Context, env core.Env) ([]installedPackage, error) {
	res, err := env.Exec.Run(ctx, core.Cmd{Path: dpkgQuery, Args: platform.SystemDpkgQuery()})
	if err != nil {
		return nil, fmt.Errorf("system: run dpkg-query: %w", err)
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("system: dpkg-query exited %d: %s", res.ExitCode, strings.TrimSpace(string(res.Stderr)))
	}
	var pkgs []installedPackage
	for line := range strings.Lines(string(res.Stdout)) {
		f := strings.SplitN(strings.TrimRight(line, "\n"), "\t", 7)
		if len(f) != 7 {
			return nil, fmt.Errorf("system: dpkg-query printed a line of %d fields", len(f))
		}
		pkgs = append(pkgs, installedPackage{f[0], f[1], f[2], f[3], f[4], f[5], f[6]})
	}
	return pkgs, nil
}

// installedVersions maps each installed package to its version. A package
// installed for several architectures appears once.
func installedVersions(ctx context.Context, env core.Env) (map[string]string, error) {
	pkgs, err := dpkgPackages(ctx, env)
	if err != nil {
		return nil, err
	}
	v := map[string]string{}
	for _, p := range pkgs {
		if strings.HasSuffix(p.Status, " installed") {
			v[p.Name] = p.Version
		}
	}
	return v, nil
}

func diffInstalled(before, after map[string]string) Applied {
	a := Applied{Installed: []string{}, Upgraded: []string{}, Removed: []string{}}
	for name, v := range after {
		switch old, ok := before[name]; {
		case !ok:
			a.Installed = append(a.Installed, name)
		case old != v:
			a.Upgraded = append(a.Upgraded, name)
		}
	}
	for name := range before {
		if _, ok := after[name]; !ok {
			a.Removed = append(a.Removed, name)
		}
	}
	slices.Sort(a.Installed)
	slices.Sort(a.Upgraded)
	slices.Sort(a.Removed)
	return a
}
