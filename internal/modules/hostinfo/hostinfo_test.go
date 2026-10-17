package hostinfo

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"runtime"
	"testing"
	"testing/fstest"
	"time"

	"github.com/darkraise/darkpanel/internal/buildinfo"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
)

var testNow = time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)

func fixtureFS(t *testing.T, osName, arch string) fstest.MapFS {
	t.Helper()
	return fstest.MapFS{
		"etc/os-release":            {Data: readFixture(t, "os-release", osName)},
		"proc/cpuinfo":              {Data: readFixture(t, "proc-"+arch, "cpuinfo")},
		"proc/meminfo":              {Data: readFixture(t, "proc-"+arch, "meminfo")},
		"proc/loadavg":              {Data: []byte("0.08 0.03 0.01 1/123 4567\n")},
		"proc/uptime":               {Data: []byte("3600.50 7000.00\n")},
		"proc/sys/kernel/osrelease": {Data: []byte("6.8.0-45-generic\n")},
	}
}

func fixtureSource(fsys fstest.MapFS) Source {
	return Source{
		FS:       fsys,
		Hostname: func() (string, error) { return "vps-1", nil },
		Statfs: func(path string) (uint64, uint64, error) {
			if path != "/" {
				return 0, 0, errors.New("statfs: unexpected path " + path)
			}
			return 50 << 30, 20 << 30, nil
		},
		Version: func() string { return "v0.1.0" },
	}
}

func readSummary(t *testing.T, m core.Module) (json.RawMessage, error) {
	t.Helper()
	res := m.Manifest().Resources
	if len(res) != 1 {
		t.Fatalf("resources = %d, want 1", len(res))
	}
	return res[0].Read(context.Background(), core.Env{Clock: coretest.NewFakeClock(testNow)})
}

func TestSummaryPerOSAndArch(t *testing.T) {
	oses := map[string]OS{
		"ubuntu-22.04": {ID: "ubuntu", VersionID: "22.04", PrettyName: "Ubuntu 22.04.5 LTS"},
		"ubuntu-24.04": {ID: "ubuntu", VersionID: "24.04", PrettyName: "Ubuntu 24.04.3 LTS"},
		"debian-12":    {ID: "debian", VersionID: "12", PrettyName: "Debian GNU/Linux 12 (bookworm)"},
		"debian-13":    {ID: "debian", VersionID: "13", PrettyName: "Debian GNU/Linux 13 (trixie)"},
	}
	type archWant struct {
		cpu  CPU
		mem  Memory
		swap Swap
	}
	arches := map[string]archWant{
		"amd64": {
			CPU{Model: "Intel(R) Xeon(R) Platinum 8259CL CPU @ 2.50GHz", LogicalCount: 2},
			Memory{TotalBytes: 8007412 << 10, AvailableBytes: 7421576 << 10},
			Swap{TotalBytes: 2097148 << 10, FreeBytes: 2097148 << 10},
		},
		"arm64": {
			CPU{Model: "0x41/0xd0c", LogicalCount: 2},
			Memory{TotalBytes: 3924604 << 10, AvailableBytes: 3512876 << 10},
			Swap{},
		},
	}
	for osName, wantOS := range oses {
		for arch, w := range arches {
			t.Run(osName+"-"+arch, func(t *testing.T) {
				raw, err := readSummary(t, New(fixtureSource(fixtureFS(t, osName, arch))))
				if err != nil {
					t.Fatalf("Read: %v", err)
				}
				var got Summary
				if err := json.Unmarshal(raw, &got); err != nil {
					t.Fatal(err)
				}
				want := Summary{
					Hostname: "vps-1", OS: wantOS, Kernel: "6.8.0-45-generic", Arch: runtime.GOARCH,
					UptimeSeconds: 3600, BootTime: testNow.Add(-time.Hour),
					CPU: w.cpu, Memory: w.mem, Swap: w.swap,
					Load:   Load{One: 0.08, Five: 0.03, Fifteen: 0.01},
					RootFS: RootFS{TotalBytes: 50 << 30, AvailableBytes: 20 << 30}, DarkpanelVersion: "v0.1.0",
				}
				if !reflect.DeepEqual(got, want) {
					t.Errorf("summary =\n%+v\nwant\n%+v", got, want)
				}
			})
		}
	}
}

func TestSummaryJSONFieldNames(t *testing.T) {
	raw, err := readSummary(t, New(fixtureSource(fixtureFS(t, "debian-13", "arm64"))))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"hostname":"vps-1","os":{"id":"debian","versionId":"13","prettyName":"Debian GNU/Linux 13 (trixie)"},` +
		`"kernel":"6.8.0-45-generic","arch":"` + runtime.GOARCH + `","uptimeSeconds":3600,"bootTime":"2026-10-17T11:00:00Z",` +
		`"cpu":{"model":"0x41/0xd0c","logicalCount":2},"memory":{"totalBytes":4018794496,"availableBytes":3597185024},` +
		`"swap":{"totalBytes":0,"freeBytes":0},"load":{"one":0.08,"five":0.03,"fifteen":0.01},` +
		`"rootFs":{"totalBytes":53687091200,"availableBytes":21474836480},"darkpanelVersion":"v0.1.0"}`
	if string(raw) != want {
		t.Errorf("summary JSON =\n%s\nwant\n%s", raw, want)
	}
}

func TestSummaryFailsOnMissingSource(t *testing.T) {
	for _, name := range []string{"etc/os-release", "proc/cpuinfo", "proc/meminfo", "proc/loadavg", "proc/uptime", "proc/sys/kernel/osrelease"} {
		fsys := fixtureFS(t, "debian-12", "amd64")
		delete(fsys, name)
		if _, err := readSummary(t, New(fixtureSource(fsys))); err == nil {
			t.Errorf("Read without %s succeeded, want an error", name)
		}
	}
	src := fixtureSource(fixtureFS(t, "debian-12", "amd64"))
	src.Statfs = func(string) (uint64, uint64, error) { return 0, 0, errors.New("statfs failed") }
	if _, err := readSummary(t, New(src)); err == nil {
		t.Error("Read with a failing statfs succeeded, want an error")
	}
}

func TestManifest(t *testing.T) {
	m := Module().Manifest()
	if m.ID != ID || m.Version != 1 || !m.DefaultEnabled || len(m.Actions) != 0 || len(m.Migrations) != 0 {
		t.Errorf("manifest = %+v, want id hostinfo, version 1, enabled by default, no actions or migrations", m)
	}
	if len(m.Resources) != 1 || m.Resources[0].Name != "hostinfo.summary" || m.Resources[0].OutputType != reflect.TypeFor[Summary]() {
		t.Errorf("resources = %+v, want hostinfo.summary returning Summary", m.Resources)
	}
	if _, err := core.NewRegistry(Module()); err != nil {
		t.Errorf("NewRegistry: %v", err)
	}
}

func TestPreflightReadsMeminfo(t *testing.T) {
	fsys := fixtureFS(t, "ubuntu-24.04", "amd64")
	checks := New(fixtureSource(fsys)).Manifest().Preflight
	if len(checks) != 1 || checks[0].ID != "meminfo" {
		t.Fatalf("preflight = %+v, want one check meminfo", checks)
	}
	if err := checks[0].Run(context.Background(), core.Env{}); err != nil {
		t.Errorf("preflight with meminfo: %v", err)
	}
	delete(fsys, "proc/meminfo")
	if err := checks[0].Run(context.Background(), core.Env{}); err == nil {
		t.Error("preflight without meminfo succeeded, want an error")
	}
}

func TestProductionModuleReadsThisHost(t *testing.T) {
	m := Module()
	raw, err := m.Manifest().Resources[0].Read(context.Background(), core.Env{Clock: core.SystemClock{}})
	if err != nil {
		t.Fatalf("Read on this host: %v", err)
	}
	var s Summary
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	if s.Hostname == "" || s.Kernel == "" || s.CPU.LogicalCount < 1 || s.Memory.TotalBytes == 0 ||
		s.RootFS.TotalBytes == 0 || s.DarkpanelVersion != buildinfo.Version {
		t.Errorf("summary of this host = %+v, want every field filled", s)
	}
	if err := m.Manifest().Preflight[0].Run(context.Background(), core.Env{}); err != nil {
		t.Errorf("preflight on this host: %v", err)
	}
}
