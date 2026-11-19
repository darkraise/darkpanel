package system

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"runtime"
	"slices"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/platform"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

// ID is the module id.
const ID = "system"

// supportedReleases are the release codenames of each supported
// distribution (README "Supported hosts").
var supportedReleases = map[string][]string{
	"ubuntu": {"jammy", "noble", "resolute"},
	"debian": {"bookworm", "trixie"},
}

// dockerAPI is the part of the Docker Engine API the module reads.
type dockerAPI interface {
	Info(ctx context.Context) (dockerapi.SystemInfo, error)
	ListContainers(ctx context.Context, opts dockerapi.ListContainersOptions) ([]dockerapi.ContainerSummary, error)
	ListVolumes(ctx context.Context, filters dockerapi.Filters) ([]dockerapi.Volume, error)
}

// deps is what the module reaches outside core.Env; tests replace each.
type deps struct {
	readFile      func(name string) ([]byte, error)
	listDir       func(name string) ([]string, error)
	modTime       func(name string) (time.Time, error)
	stat          func(name string) (mode fs.FileMode, uid, gid int, err error)
	kernelRelease func() (string, error)
	hostname      func() (string, error)
	arch          string
	docker        dockerAPI
	reboot        platform.Reboot
	purge         platform.Purge
	// keyring replaces the embedded Docker repository keyring when not nil.
	keyring []byte
	// firewallIntegration reports whether the firewall's Docker integration
	// is on; nil until the Docker install reads it.
	firewallIntegration func(ctx context.Context, env core.Env) (bool, error)
}

type module struct{ d deps }

// Module returns the system module with the host's dependencies.
func Module() core.Module { return module{d: productionDeps()} }

func productionDeps() deps {
	return deps{
		readFile: os.ReadFile,
		listDir:  listFiles,
		modTime: func(name string) (time.Time, error) {
			st, err := os.Stat(name)
			if err != nil {
				return time.Time{}, err
			}
			return st.ModTime(), nil
		},
		stat:          statOwner,
		kernelRelease: unameRelease,
		hostname:      os.Hostname,
		arch:          runtime.GOARCH,
		docker:        dockerapi.New(dockerapi.DefaultSocket),
		reboot:        platform.NewReboot(),
		purge:         platform.NewPurge(),
	}
}

// listFiles lists the names of the entries of dir that are not directories.
func listFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	return names, nil
}

// statOwner reads name's permission bits and owner, not following a link.
func statOwner(name string) (fs.FileMode, int, int, error) {
	var st unix.Stat_t
	if err := unix.Lstat(name, &st); err != nil {
		return 0, 0, 0, err
	}
	return fs.FileMode(st.Mode & 0o777), int(st.Uid), int(st.Gid), nil
}

func unameRelease() (string, error) {
	var u unix.Utsname
	if err := unix.Uname(&u); err != nil {
		return "", fmt.Errorf("system: read the kernel release: %w", err)
	}
	return unix.ByteSliceToString(u.Release[:]), nil
}

// Manifest declares the module. It is enabled by default; preflight checks
// only the operating system and architecture.
func (m module) Manifest() core.Manifest {
	return core.Manifest{
		ID:             ID,
		Version:        1,
		DefaultEnabled: true,
		Preflight:      []core.PreflightCheck{{ID: "os", Run: m.checkOS}},
		Resources:      []core.ResourceDef{m.updatesResource(), m.packagesResource(), m.rebootResource(), m.autoupdatesResource(), m.dockerResource()},
		Actions:        []core.ActionDef{m.searchAction(), m.refreshAction(), m.upgradeAction(), m.installAction(), m.removeAction(), m.repairAction(), m.autoupdatesSetAction(), m.rebootAction(), m.engineInstallAction(), m.engineUpdateAction(), m.dockerCheckAction(), m.dockerInstallAction()},
		OwnedPaths: []core.OwnedPath{
			{Path: failedSourcesDir, Kind: core.OwnedManagedDir, Pattern: `^failed-sources$`},
			{Path: autoUpgradesFile, Kind: core.OwnedRestore},
			{Path: dockerKeyringPath, Kind: core.OwnedFile},
			{Path: dockerListPath, Kind: core.OwnedFile},
		},
	}
}

// hostname is the host's name, the phrase an irreversible action asks for.
func (m module) hostname() (string, error) {
	h, err := m.d.hostname()
	if err != nil {
		return "", fmt.Errorf("system: read the host name: %w", err)
	}
	return h, nil
}

func (m module) checkOS(context.Context, core.Env) error {
	info, err := readOSRelease(m.d.readFile)
	if err != nil {
		return err
	}
	if !slices.Contains(supportedReleases[info.ID], info.Codename) {
		return fmt.Errorf("system: the operating system %q release %q is not supported; Ubuntu jammy, noble or resolute, or Debian bookworm or trixie, is needed", info.ID, info.Codename)
	}
	if m.d.arch != "amd64" && m.d.arch != "arm64" {
		return fmt.Errorf("system: the architecture %s is not supported; amd64 or arm64 is needed", m.d.arch)
	}
	return nil
}

type osInfo struct{ ID, Codename string }

// readOSRelease reads the distribution id and release codename of
// /etc/os-release, as the tailscale module's preflight does.
func readOSRelease(read func(name string) ([]byte, error)) (osInfo, error) {
	b, err := read("/etc/os-release")
	if err != nil {
		return osInfo{}, fmt.Errorf("system: read the operating system release: %w", err)
	}
	var info osInfo
	for line := range strings.Lines(string(b)) {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok || strings.HasPrefix(key, "#") {
			continue
		}
		value = strings.TrimSpace(value)
		if len(value) >= 2 && (value[0] == '"' || value[0] == '\'') && value[len(value)-1] == value[0] {
			value = value[1 : len(value)-1]
		}
		switch strings.TrimSpace(key) {
		case "ID":
			info.ID = value
		case "VERSION_CODENAME":
			info.Codename = value
		}
	}
	return info, nil
}
