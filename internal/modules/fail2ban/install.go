package fail2ban

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/platform"
)

const (
	aptGet    = "/usr/bin/apt-get"
	dpkgQuery = "/usr/bin/dpkg-query"
)

// packageTimeout bounds an install or remove, apt's lock wait included.
const packageTimeout = 15 * time.Minute

// Packages is the answer of fail2ban.package.install and .remove.
type Packages struct {
	Packages []string `json:"packages"`
}

func (m module) packageActions() []core.ActionDef {
	spec := func(verb, summary, compensatedBy string) core.ActionSpec {
		return core.ActionSpec{
			Name: ID + ".package." + verb, Version: 1, Summary: summary,
			Class: core.ClassCompensable, Side: core.SideAgent, LockKeys: []string{aptLockKey, lockKey},
			MinScope: core.ScopeAdmin, ApplyTimeout: packageTimeout, CompensatedBy: ID + ".package." + compensatedBy,
			Rechecks: []string{"firewall"},
		}
	}
	return []core.ActionDef{
		core.DefineTask[struct{}, Packages](spec("install",
			"Install fail2ban and python3-systemd with Darkpanel's settings written first.", "remove"), installHandler{m: m}),
		core.DefineTask[struct{}, Packages](spec("remove",
			"Remove fail2ban and python3-systemd; Darkpanel's settings file stays.", "install"), removeHandler{}),
	}
}

// refuseCrowdSec refuses an install on a host where CrowdSec is installed:
// Darkpanel then stays hands-off.
func refuseCrowdSec(ctx context.Context, env core.Env) error {
	on, err := loaded(ctx, env, crowdsecUnit)
	if err != nil {
		return err
	}
	if on {
		return core.Definite(errcode.New(errcode.ModuleUnavailable, "CrowdSec is installed on this host"))
	}
	return nil
}

type installHandler struct{ m module }

func (h installHandler) Plan(ctx context.Context, env core.Env, _ struct{}) (core.PlanOutcome, error) {
	if err := refuseCrowdSec(ctx, env); err != nil {
		return core.PlanOutcome{}, err
	}
	if ok, err := loaded(ctx, env, fail2banUnit); err != nil || ok {
		if err == nil {
			err = invalid("", "fail2ban is already installed")
		}
		return core.PlanOutcome{}, err
	}
	if _, err := apt(ctx, env, platform.SystemAptCheck()); err != nil {
		return core.PlanOutcome{}, err
	}
	diff := []core.DiffLine{}
	cur, err := h.m.readSettingsFile()
	if err != nil {
		return core.PlanOutcome{}, err
	}
	if cur == nil {
		diff = append(diff, core.DiffLine{Op: "+", Text: "Write " + SettingsFile + ": nftables ban actions, the sshd jail on, bans of 10 minutes after 5 failures"})
	} else {
		diff = append(diff, core.DiffLine{Op: " ", Text: "Keep the settings in " + SettingsFile})
	}
	diff = append(diff, core.DiffLine{Op: "+", Text: "Install " + strings.Join(platform.Fail2banPackages, " and ")})
	return core.PlanOutcome{Diff: diff, StateHash: "absent"}, nil
}

// Task writes the settings file before the package exists, so fail2ban
// never starts with its package's own ban action, then installs.
func (h installHandler) Task(ctx context.Context, env core.Env, _ struct{}) (Packages, error) {
	if err := refuseCrowdSec(ctx, env); err != nil {
		return Packages{}, err
	}
	cur, err := h.m.readSettingsFile()
	if err != nil {
		return Packages{}, core.Definite(err)
	}
	if cur == nil {
		if err := h.writeDefaults(ctx, env); err != nil {
			return Packages{}, err
		}
	}
	if _, err := apt(ctx, env, platform.SystemAptCheckWait()); err != nil {
		return Packages{}, err
	}
	if _, err := apt(ctx, env, platform.SystemAptRun("install", platform.Fail2banPackages)); err != nil {
		return Packages{}, err
	}
	return Packages{Packages: slices.Clone(platform.Fail2banPackages)}, nil
}

func (h installHandler) writeDefaults(ctx context.Context, env core.Env) error {
	mk, ok := env.Files.(core.DirMaker)
	if !ok {
		return errors.New("fail2ban: the file provider cannot create directories")
	}
	for _, dir := range []string{etcDir, jailDir} {
		if err := mk.MakeDir(ctx, ID, dir); err != nil {
			return err
		}
	}
	s := defaultSettings
	s.IgnoreIP = fixedIgnore(env)
	data, _, err := settingsHandler(h).desired(ctx, env, s)
	if err != nil {
		return err
	}
	return env.Files.WriteFile(ctx, ID, SettingsFile, data, 0o644)
}

func (installHandler) Verify(ctx context.Context, env core.Env, _ struct{}, _ Packages) error {
	missing, err := notInstalled(ctx, env)
	if err != nil {
		return err
	}
	if len(missing) > 0 {
		return fmt.Errorf("fail2ban: %s not installed", strings.Join(missing, ", "))
	}
	return nil
}

type removeHandler struct{}

// removals are the packages apt's simulation of the removal removes; it
// refuses one that would remove anything else.
func removals(ctx context.Context, env core.Env, sim []string) error {
	out, err := apt(ctx, env, sim)
	if err != nil {
		return err
	}
	var other []string
	for line := range strings.Lines(string(out)) {
		f := strings.Fields(line)
		if len(f) >= 2 && f[0] == "Remv" && !slices.Contains(platform.Fail2banPackages, f[1]) {
			other = append(other, f[1])
		}
	}
	if len(other) > 0 {
		return core.Definite(errcode.New(errcode.SystemRemovalRefused,
			"removing fail2ban would also remove "+strings.Join(other, ", ")+"; remove it by hand"))
	}
	return nil
}

func (removeHandler) Plan(ctx context.Context, env core.Env, _ struct{}) (core.PlanOutcome, error) {
	if err := requireInstalled(ctx, env); err != nil {
		return core.PlanOutcome{}, err
	}
	if _, err := apt(ctx, env, platform.SystemAptCheck()); err != nil {
		return core.PlanOutcome{}, err
	}
	if err := removals(ctx, env, platform.SystemAptPlan("remove", platform.Fail2banPackages)); err != nil {
		return core.PlanOutcome{}, err
	}
	return core.PlanOutcome{StateHash: "installed", Diff: []core.DiffLine{
		{Op: "-", Text: "Remove " + strings.Join(platform.Fail2banPackages, " and ") + "; every ban ends"},
		{Op: " ", Text: "Keep " + SettingsFile},
	}}, nil
}

func (removeHandler) Task(ctx context.Context, env core.Env, _ struct{}) (Packages, error) {
	if err := removals(ctx, env, platform.SystemAptResim("remove", platform.Fail2banPackages)); err != nil {
		return Packages{}, err
	}
	if _, err := apt(ctx, env, platform.SystemAptRun("remove", platform.Fail2banPackages)); err != nil {
		return Packages{}, err
	}
	return Packages{Packages: slices.Clone(platform.Fail2banPackages)}, nil
}

func (removeHandler) Verify(ctx context.Context, env core.Env, _ struct{}, _ Packages) error {
	missing, err := notInstalled(ctx, env)
	if err != nil {
		return err
	}
	if len(missing) < len(platform.Fail2banPackages) {
		return errors.New("fail2ban: the packages are still installed")
	}
	return nil
}

// apt runs apt-get with args and returns its output; a non-zero exit is an
// error with its last lines.
func apt(ctx context.Context, env core.Env, args []string) ([]byte, error) {
	res, err := env.Exec.Run(ctx, core.Cmd{Path: aptGet, Args: args})
	out := slices.Concat(res.Stdout, res.Stderr)
	if err != nil {
		return out, fmt.Errorf("fail2ban: run apt-get: %w", err)
	}
	if res.ExitCode != 0 {
		return out, fmt.Errorf("fail2ban: apt-get exited %d:\n%s", res.ExitCode, lastLines(strings.TrimSpace(string(out)), 20))
	}
	return out, nil
}

// notInstalled lists the packages of platform.Fail2banPackages dpkg does
// not report installed.
func notInstalled(ctx context.Context, env core.Env) ([]string, error) {
	res, err := env.Exec.Run(ctx, core.Cmd{Path: dpkgQuery, Args: platform.SystemDpkgQuery()})
	if err != nil {
		return nil, fmt.Errorf("fail2ban: run dpkg-query: %w", err)
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("fail2ban: dpkg-query exited %d: %s", res.ExitCode, strings.TrimSpace(string(res.Stderr)))
	}
	installed := map[string]bool{}
	for line := range strings.Lines(string(res.Stdout)) {
		f := strings.Split(line, "\t")
		if len(f) >= 4 && f[3] == "install ok installed" {
			name, _, _ := strings.Cut(f[0], ":")
			installed[name] = true
		}
	}
	var missing []string
	for _, p := range platform.Fail2banPackages {
		if !installed[p] {
			missing = append(missing, p)
		}
	}
	return missing, nil
}
