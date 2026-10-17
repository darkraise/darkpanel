package hostinfo

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"runtime"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/darkraise/darkpanel/internal/buildinfo"
	"github.com/darkraise/darkpanel/internal/core"
)

// ID is the module id.
const ID = "hostinfo"

// Summary is the output of resource hostinfo.summary (v1).
type Summary struct {
	Hostname         string    `json:"hostname"`
	OS               OS        `json:"os"`
	Kernel           string    `json:"kernel"`
	Arch             string    `json:"arch"`
	UptimeSeconds    int64     `json:"uptimeSeconds"`
	BootTime         time.Time `json:"bootTime"`
	CPU              CPU       `json:"cpu"`
	Memory           Memory    `json:"memory"`
	Swap             Swap      `json:"swap"`
	Load             Load      `json:"load"`
	RootFS           RootFS    `json:"rootFs"`
	DarkpanelVersion string    `json:"darkpanelVersion"`
}

// OS is the distribution from /etc/os-release.
type OS struct {
	ID         string `json:"id"`
	VersionID  string `json:"versionId"`
	PrettyName string `json:"prettyName"`
}

// CPU describes the processors.
type CPU struct {
	Model        string `json:"model"`
	LogicalCount int    `json:"logicalCount"`
}

// Memory is physical memory in bytes.
type Memory struct {
	TotalBytes     uint64 `json:"totalBytes"`
	AvailableBytes uint64 `json:"availableBytes"`
}

// Swap is swap space in bytes.
type Swap struct {
	TotalBytes uint64 `json:"totalBytes"`
	FreeBytes  uint64 `json:"freeBytes"`
}

// Load is the 1, 5 and 15 minute load averages.
type Load struct {
	One     float64 `json:"one"`
	Five    float64 `json:"five"`
	Fifteen float64 `json:"fifteen"`
}

// RootFS is the size of the filesystem mounted at / in bytes.
type RootFS struct {
	TotalBytes     uint64 `json:"totalBytes"`
	AvailableBytes uint64 `json:"availableBytes"`
}

// Source is where the module reads the host from; tests pass fixtures. FS is
// rooted at /, so paths read from it have no leading slash.
type Source struct {
	FS       fs.FS
	Hostname func() (string, error)
	Statfs   func(path string) (totalBytes, availableBytes uint64, err error)
	Version  func() string
}

// New returns the module reading from src.
func New(src Source) core.Module { return module{src: src} }

// Module returns the module reading from this host.
func Module() core.Module {
	return New(Source{
		FS:       os.DirFS("/"),
		Hostname: os.Hostname,
		Statfs:   statfs,
		Version:  func() string { return buildinfo.Version },
	})
}

func statfs(path string) (uint64, uint64, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return 0, 0, fmt.Errorf("statfs %s: %w", path, err)
	}
	bsize := uint64(st.Bsize) //nolint:gosec // G115: the kernel reports a positive block size
	return st.Blocks * bsize, st.Bavail * bsize, nil
}

type module struct {
	src Source
}

// Manifest declares the module: one resource, no actions, no migrations.
func (m module) Manifest() core.Manifest {
	return core.Manifest{
		ID:             ID,
		Version:        1,
		DefaultEnabled: true,
		Resources:      []core.ResourceDef{core.DefineResource(ID+".summary", m.summary)},
		Preflight:      []core.PreflightCheck{{ID: "meminfo", Run: m.checkMeminfo}},
	}
}

func (m module) checkMeminfo(context.Context, core.Env) error {
	if _, err := fs.ReadFile(m.src.FS, "proc/meminfo"); err != nil {
		return fmt.Errorf("cannot read /proc/meminfo: %w", err)
	}
	return nil
}

func (m module) read(name string) ([]byte, error) {
	b, err := fs.ReadFile(m.src.FS, name)
	if err != nil {
		return nil, fmt.Errorf("hostinfo: read /%s: %w", name, err)
	}
	return b, nil
}

func (m module) summary(_ context.Context, env core.Env) (Summary, error) {
	var s Summary
	var err error
	if s.Hostname, err = m.src.Hostname(); err != nil {
		return Summary{}, fmt.Errorf("hostinfo: hostname: %w", err)
	}
	files := map[string][]byte{}
	for _, name := range []string{"etc/os-release", "proc/sys/kernel/osrelease", "proc/uptime", "proc/cpuinfo", "proc/meminfo", "proc/loadavg"} {
		if files[name], err = m.read(name); err != nil {
			return Summary{}, err
		}
	}
	s.OS = OS(parseOSRelease(files["etc/os-release"]))
	s.Kernel = strings.TrimSpace(string(files["proc/sys/kernel/osrelease"]))
	s.Arch = runtime.GOARCH
	if s.UptimeSeconds, err = parseUptime(files["proc/uptime"]); err != nil {
		return Summary{}, fmt.Errorf("hostinfo: %w", err)
	}
	s.BootTime = env.Clock.Now().UTC().Add(-time.Duration(s.UptimeSeconds) * time.Second).Truncate(time.Second)
	s.CPU = CPU(parseCPUInfo(files["proc/cpuinfo"]))
	mem, err := parseMemInfo(files["proc/meminfo"])
	if err != nil {
		return Summary{}, fmt.Errorf("hostinfo: %w", err)
	}
	s.Memory = Memory{TotalBytes: mem.TotalBytes, AvailableBytes: mem.AvailableBytes}
	s.Swap = Swap{TotalBytes: mem.SwapTotalBytes, FreeBytes: mem.SwapFreeBytes}
	load, err := parseLoadAvg(files["proc/loadavg"])
	if err != nil {
		return Summary{}, fmt.Errorf("hostinfo: %w", err)
	}
	s.Load = Load(load)
	if s.RootFS.TotalBytes, s.RootFS.AvailableBytes, err = m.src.Statfs("/"); err != nil {
		return Summary{}, fmt.Errorf("hostinfo: %w", err)
	}
	s.DarkpanelVersion = m.src.Version()
	return s, nil
}
