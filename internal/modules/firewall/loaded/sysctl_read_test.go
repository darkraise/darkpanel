package loaded

import (
	"context"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/platform/netstat"
)

// srValues reads a golden sysctl.txt of key=value lines.
func srValues(t *testing.T, image, scenario string) map[string]string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(ldGoldenDir, image, scenario, "sysctl.txt")) //nolint:gosec // G304: a fixed name under the golden directory
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for line := range strings.Lines(string(data)) {
		k, v, _ := strings.Cut(strings.TrimSpace(line), "=")
		out[k] = v
	}
	return out
}

var srIfaces = []netstat.Interface{
	{Index: 1, Name: "lo", Up: true, Loopback: true},
	{Index: 2, Name: "eth0", Up: true, Running: true},
	{Index: 3, Name: "docker0"},
	{Index: 4, Name: "we:ird"},
}

func TestReadSysctl(t *testing.T) {
	values := srValues(t, "debian-12", "rules")
	values["net/ipv4/conf/docker0/rp_filter"] = "1"
	values["net/ipv4/conf/docker0/accept_source_route"] = "0"
	x := ldGolden(t, "debian-12", "rules")
	files := &ldFiles{sysctl: values}
	r := Reader{
		Interfaces:     func() ([]netstat.Interface, error) { return srIfaces, nil },
		NFTablesLoaded: func() bool { return false },
	}
	st, err := r.read(context.Background(), core.Env{Exec: x, Files: files}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !maps.Equal(st.Sysctl, values) {
		t.Errorf("sysctl:\n got %v\nwant %v", st.Sysctl, values)
	}
	for _, k := range []string{"net/ipv6/conf/lo/forwarding", "net/ipv6/conf/lo/accept_ra", "net/ipv4/conf/lo/rp_filter", "net/ipv4/conf/lo/accept_source_route"} {
		if _, ok := st.Sysctl[k]; !ok {
			t.Errorf("sysctl lacks the loopback key %s", k)
		}
	}
	if want := []string{`interface "we:ird": Darkpanel cannot read its sysctl values`}; !slices.Equal(st.Unsupported, want) {
		t.Errorf("unsupported %q, want %q", st.Unsupported, want)
	}
	if len(files.reads) != 21 || files.reads[0] != "firewall net/ipv4/ip_forward" || files.reads[20] != "firewall net/ipv4/conf/docker0/accept_source_route" {
		t.Errorf("reads %q", files.reads)
	}
}

func TestSysctlKeys(t *testing.T) {
	keys, reasons := sysctlKeys([]netstat.Interface{{Name: "eth0"}, {Name: "all"}, {Name: "lo", Loopback: true}, {Name: "a-very-long-name0"}})
	want := []string{"net/ipv4/ip_forward",
		"net/ipv6/conf/all/forwarding", "net/ipv6/conf/all/accept_ra", "net/ipv4/conf/all/rp_filter", "net/ipv4/conf/all/accept_source_route",
		"net/ipv6/conf/default/forwarding", "net/ipv6/conf/default/accept_ra", "net/ipv4/conf/default/rp_filter", "net/ipv4/conf/default/accept_source_route",
		"net/ipv6/conf/eth0/forwarding", "net/ipv6/conf/eth0/accept_ra", "net/ipv4/conf/eth0/rp_filter", "net/ipv4/conf/eth0/accept_source_route",
		"net/ipv6/conf/lo/forwarding", "net/ipv6/conf/lo/accept_ra", "net/ipv4/conf/lo/rp_filter", "net/ipv4/conf/lo/accept_source_route"}
	if !slices.Equal(keys, want) {
		t.Errorf("keys %q", keys)
	}
	if want := []string{`interface "all": Darkpanel cannot read its sysctl values`, `interface "a-very-long-name0": Darkpanel cannot read its sysctl values`}; !slices.Equal(reasons, want) {
		t.Errorf("reasons %q", reasons)
	}
}

// srFailFiles fails every sysctl read with err.
type srFailFiles struct {
	core.FileWriter
	err error
}

func (f srFailFiles) ReadSysctl(context.Context, string, string) (string, error) { return "", f.err }

func TestReadSysctlErrors(t *testing.T) {
	x := ldGolden(t, "debian-12", "rules")
	denied := srFailFiles{err: errors.New("platform: module firewall does not declare sysctl net/ipv4/ip_forward")}
	_, err := ldReader().read(context.Background(), core.Env{Exec: x, Files: denied}, t.TempDir())
	if err == nil || err.Error() != "loaded: platform: module firewall does not declare sysctl net/ipv4/ip_forward" {
		t.Errorf("err = %v", err)
	}
	gone := srFailFiles{err: syscall.ENOENT}
	st, err := ldReader().read(context.Background(), core.Env{Exec: x, Files: gone}, t.TempDir())
	if err != nil || len(st.Sysctl) != 0 || st.Sysctl == nil {
		t.Errorf("every key missing: %v, %v", st.Sysctl, err)
	}
	r := ldReader()
	r.Interfaces = func() ([]netstat.Interface, error) { return nil, errors.New("netstat: interfaces: boom") }
	if _, err := r.read(context.Background(), ldEnv(x, nil), t.TempDir()); err == nil || err.Error() != "loaded: netstat: interfaces: boom" {
		t.Errorf("err = %v", err)
	}
}
