package hostinfo

import (
	"os"
	"path/filepath"
	"testing"
)

func readFixture(t *testing.T, path ...string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(append([]string{"testdata"}, path...)...))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParseOSRelease(t *testing.T) {
	tests := []struct {
		file string
		want osRelease
	}{
		{"ubuntu-22.04", osRelease{ID: "ubuntu", VersionID: "22.04", PrettyName: "Ubuntu 22.04.5 LTS"}},
		{"ubuntu-24.04", osRelease{ID: "ubuntu", VersionID: "24.04", PrettyName: "Ubuntu 24.04.3 LTS"}},
		{"debian-12", osRelease{ID: "debian", VersionID: "12", PrettyName: "Debian GNU/Linux 12 (bookworm)"}},
		{"debian-13", osRelease{ID: "debian", VersionID: "13", PrettyName: "Debian GNU/Linux 13 (trixie)"}},
	}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			if got := parseOSRelease(readFixture(t, "os-release", tt.file)); got != tt.want {
				t.Errorf("parseOSRelease = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestParseOSReleaseSyntax(t *testing.T) {
	tests := []struct {
		name, in string
		want     osRelease
	}{
		{"empty file takes the os-release defaults", "", osRelease{ID: "linux", PrettyName: "Linux"}},
		{"single quotes, comments and blank lines", "# comment\n\nID='alpine'\nPRETTY_NAME='Alpine Linux'\n", osRelease{ID: "alpine", PrettyName: "Alpine Linux"}},
		{"escaped quote in a double-quoted value", `PRETTY_NAME="Say \"hi\""` + "\nID=x\n", osRelease{ID: "x", PrettyName: `Say "hi"`}},
		{"line without = is skipped", "garbage\nID=debian\n", osRelease{ID: "debian", PrettyName: "Linux"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseOSRelease([]byte(tt.in)); got != tt.want {
				t.Errorf("parseOSRelease = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestParseCPUInfo(t *testing.T) {
	tests := []struct {
		name string
		in   []byte
		want cpuInfo
	}{
		{"amd64", readFixture(t, "proc-amd64", "cpuinfo"), cpuInfo{Model: "Intel(R) Xeon(R) Platinum 8259CL CPU @ 2.50GHz", LogicalCount: 2}},
		{"arm64 implementer and part", readFixture(t, "proc-arm64", "cpuinfo"), cpuInfo{Model: "0x41/0xd0c", LogicalCount: 2}},
		{"Hardware line", []byte("processor\t: 0\nHardware\t: BCM2835\nCPU implementer\t: 0x41\nCPU part\t: 0xd08\n"), cpuInfo{Model: "BCM2835", LogicalCount: 1}},
		{"model name wins over Hardware", []byte("processor\t: 0\nmodel name\t: ARMv7 Processor rev 4 (v7l)\nHardware\t: BCM2835\n"), cpuInfo{Model: "ARMv7 Processor rev 4 (v7l)", LogicalCount: 1}},
		{"nothing known", []byte("processor\t: 0\nprocessor\t: 1\nprocessor\t: 2\n"), cpuInfo{Model: "unknown", LogicalCount: 3}},
		{"empty", nil, cpuInfo{Model: "unknown", LogicalCount: 0}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseCPUInfo(tt.in); got != tt.want {
				t.Errorf("parseCPUInfo = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestParseMemInfo(t *testing.T) {
	tests := []struct {
		arch string
		want memInfo
	}{
		{"amd64", memInfo{TotalBytes: 8007412 * 1024, AvailableBytes: 7421576 * 1024, SwapTotalBytes: 2097148 * 1024, SwapFreeBytes: 2097148 * 1024}},
		{"arm64", memInfo{TotalBytes: 3924604 * 1024, AvailableBytes: 3512876 * 1024}},
	}
	for _, tt := range tests {
		t.Run(tt.arch, func(t *testing.T) {
			got, err := parseMemInfo(readFixture(t, "proc-"+tt.arch, "meminfo"))
			if err != nil || got != tt.want {
				t.Errorf("parseMemInfo = %+v, %v; want %+v", got, err, tt.want)
			}
		})
	}
	for _, in := range []string{"", "MemFree: 1 kB\n", "MemTotal: x kB\nMemAvailable: 1 kB\n", "MemTotal: 1 kB\n", "MemTotal:0 00\nMemAvailable:1 00000000000000", "MemTotal: 2 kB\nMemAvailable: 1 kB\nSwapTotal: 0 kB\nSwapFree: 1 kB\n"} {
		if _, err := parseMemInfo([]byte(in)); err == nil {
			t.Errorf("parseMemInfo(%q) succeeded, want an error", in)
		}
	}
}

func TestParseLoadAvg(t *testing.T) {
	got, err := parseLoadAvg([]byte("0.08 0.03 1.50 1/123 4567\n"))
	if want := (loadAvg{One: 0.08, Five: 0.03, Fifteen: 1.5}); err != nil || got != want {
		t.Errorf("parseLoadAvg = %+v, %v; want %+v", got, err, want)
	}
	for _, in := range []string{"", "0.1 0.2", "a b c 1/2 3", "NaN 0 0 1/1 1", "-1 0 0 1/1 1"} {
		if _, err := parseLoadAvg([]byte(in)); err == nil {
			t.Errorf("parseLoadAvg(%q) succeeded, want an error", in)
		}
	}
}

func TestParseUptime(t *testing.T) {
	got, err := parseUptime([]byte("122610.97 1469212.45\n"))
	if err != nil || got != 122610 {
		t.Errorf("parseUptime = %d, %v; want 122610", got, err)
	}
	for _, in := range []string{"", "x 1", "-5 1", "Inf 1", "1e300 1"} {
		if _, err := parseUptime([]byte(in)); err == nil {
			t.Errorf("parseUptime(%q) succeeded, want an error", in)
		}
	}
}

func FuzzParseOSRelease(f *testing.F) {
	f.Add([]byte("ID=debian\nPRETTY_NAME=\"Debian\"\n"))
	f.Add([]byte(`PRETTY_NAME="a\"b\\"` + "\n"))
	f.Fuzz(func(t *testing.T, in []byte) {
		if r := parseOSRelease(in); r.ID == "" || r.PrettyName == "" {
			t.Errorf("parseOSRelease(%q) = %+v, want ID and PrettyName set", in, r)
		}
	})
}

func FuzzParseCPUInfo(f *testing.F) {
	f.Add([]byte("processor\t: 0\nmodel name\t: x\n"))
	f.Add([]byte("processor\t: 0\nCPU implementer\t: 0x41\nCPU part\t: 0xd0c\n"))
	f.Fuzz(func(t *testing.T, in []byte) {
		if c := parseCPUInfo(in); c.Model == "" || c.LogicalCount < 0 {
			t.Errorf("parseCPUInfo(%q) = %+v", in, c)
		}
	})
}

func FuzzParseMemInfo(f *testing.F) {
	f.Add([]byte("MemTotal: 2 kB\nMemAvailable: 1 kB\nSwapTotal: 0 kB\nSwapFree: 0 kB\n"))
	f.Fuzz(func(t *testing.T, in []byte) {
		if m, err := parseMemInfo(in); err == nil && (m.AvailableBytes > m.TotalBytes || m.SwapFreeBytes > m.SwapTotalBytes) {
			t.Errorf("parseMemInfo(%q) = %+v, more available than total", in, m)
		}
	})
}

func FuzzParseLoadAvg(f *testing.F) {
	f.Add([]byte("0.08 0.03 0.01 1/123 4567\n"))
	f.Fuzz(func(t *testing.T, in []byte) {
		if l, err := parseLoadAvg(in); err == nil && (l.One < 0 || l.Five < 0 || l.Fifteen < 0) {
			t.Errorf("parseLoadAvg(%q) = %+v", in, l)
		}
	})
}

func FuzzParseUptime(f *testing.F) {
	f.Add([]byte("122610.97 1469212.45\n"))
	f.Fuzz(func(t *testing.T, in []byte) {
		if s, err := parseUptime(in); err == nil && s < 0 {
			t.Errorf("parseUptime(%q) = %d", in, s)
		}
	})
}
