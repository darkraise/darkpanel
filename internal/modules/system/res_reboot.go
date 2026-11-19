package system

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"slices"
	"strings"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/platform"
)

const (
	rebootRequired     = "/run/reboot-required"
	rebootRequiredPkgs = "/run/reboot-required.pkgs"
	procVersion        = "/proc/version"
	needrestartBin     = "/usr/sbin/needrestart"
	aptConfig          = "/usr/bin/apt-config"
)

// debianKernelVersion is the package version a Debian kernel prints in
// /proc/version: "... Debian 6.1.76-1 (2024-02-01)".
var debianKernelVersion = regexp.MustCompile(`\bDebian (\S+)`)

// RebootState is the reboot resource.
type RebootState struct {
	Required bool         `json:"required"`
	Packages []string     `json:"packages"`
	Kernel   *KernelState `json:"kernel" jsonschema:"nullable"` // set only when the kernel check decides a reboot
	Services []string     `json:"services"`
}

// KernelState is the running kernel's package version and the newest one
// installed.
type KernelState struct {
	Running string `json:"running"`
	Newest  string `json:"newest"`
}

// AutoUpdates is the autoupdates resource.
type AutoUpdates struct {
	Installed bool `json:"installed"`
	Enabled   bool `json:"enabled"`
}

func (m module) rebootResource() core.ResourceDef {
	return core.DefineResource(ID+".reboot", m.reboot)
}

func (m module) autoupdatesResource() core.ResourceDef {
	return core.DefineResource(ID+".autoupdates", m.autoupdates)
}

// reboot reports a pending reboot: /run/reboot-required when the host writes
// it, else a newer installed kernel than the running one.
func (m module) reboot(ctx context.Context, env core.Env) (RebootState, error) {
	out := RebootState{Packages: []string{}}
	_, err := m.d.readFile(rebootRequired)
	switch {
	case err == nil:
		out.Required = true
		pkgs, err := m.d.readFile(rebootRequiredPkgs)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return RebootState{}, fmt.Errorf("system: read %s: %w", rebootRequiredPkgs, err)
		}
		for line := range strings.Lines(string(pkgs)) {
			if p := strings.TrimSpace(line); p != "" && !slices.Contains(out.Packages, p) {
				out.Packages = append(out.Packages, p)
			}
		}
		slices.Sort(out.Packages)
	case errors.Is(err, fs.ErrNotExist):
		k, err := m.kernelCheck(ctx, env)
		if err != nil {
			return RebootState{}, err
		}
		if k != nil {
			out.Required, out.Kernel = true, k
		}
	default:
		return RebootState{}, fmt.Errorf("system: read %s: %w", rebootRequired, err)
	}
	if out.Services, err = restartServices(ctx, env); err != nil {
		return RebootState{}, err
	}
	return out, nil
}

// kernelCheck compares the running kernel's package version with the newest
// installed linux-image-* version; nil when no newer one is installed or the
// running version is unknown.
func (m module) kernelCheck(ctx context.Context, env core.Env) (*KernelState, error) {
	info, err := readOSRelease(m.d.readFile)
	if err != nil {
		return nil, err
	}
	versions, err := installedVersions(ctx, env)
	if err != nil {
		return nil, err
	}
	var running string
	if info.ID == "debian" {
		b, err := m.d.readFile(procVersion)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("system: read %s: %w", procVersion, err)
		}
		if mt := debianKernelVersion.FindSubmatch(b); mt != nil {
			running = string(mt[1])
		}
	} else {
		release, err := m.d.kernelRelease()
		if err != nil {
			return nil, err
		}
		running = versions["linux-image-"+release]
	}
	if running == "" {
		return nil, nil
	}
	newest := running
	for name, v := range versions {
		if strings.HasPrefix(name, "linux-image-") && debVersionCompare(v, newest) > 0 {
			newest = v
		}
	}
	if newest == running {
		return nil, nil
	}
	return &KernelState{Running: running, Newest: newest}, nil
}

// restartServices lists the services needrestart says run outdated code; []
// when needrestart is not installed.
func restartServices(ctx context.Context, env core.Env) ([]string, error) {
	res, err := env.Exec.Run(ctx, core.Cmd{Path: needrestartBin, Args: platform.SystemNeedrestart()})
	if errors.Is(err, fs.ErrNotExist) {
		return []string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("system: run needrestart: %w", err)
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("system: needrestart exited %d: %s", res.ExitCode, strings.TrimSpace(string(res.Stderr)))
	}
	services := []string{}
	for line := range strings.Lines(string(res.Stdout)) {
		if s, ok := strings.CutPrefix(strings.TrimSpace(line), "NEEDRESTART-SVC:"); ok {
			if s = strings.TrimSpace(s); s != "" {
				services = append(services, s)
			}
		}
	}
	slices.Sort(services)
	return slices.Compact(services), nil
}

// autoupdates reports whether unattended-upgrades is installed and both
// periodic apt settings it needs are on.
func (m module) autoupdates(ctx context.Context, env core.Env) (AutoUpdates, error) {
	versions, err := installedVersions(ctx, env)
	if err != nil {
		return AutoUpdates{}, err
	}
	dump, err := runOK(ctx, env, aptConfig, platform.SystemAptConfigDump())
	if err != nil {
		return AutoUpdates{}, err
	}
	conf := map[string]string{}
	for line := range strings.Lines(string(dump)) {
		key, value, ok := strings.Cut(strings.TrimSpace(line), " ")
		if ok {
			conf[key] = strings.Trim(strings.TrimSuffix(value, ";"), `"`)
		}
	}
	_, installed := versions["unattended-upgrades"]
	return AutoUpdates{
		Installed: installed,
		Enabled:   conf["APT::Periodic::Update-Package-Lists"] == "1" && conf["APT::Periodic::Unattended-Upgrade"] == "1",
	}, nil
}
