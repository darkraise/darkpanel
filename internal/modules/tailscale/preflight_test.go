package tailscale

import (
	"context"
	"errors"
	"io/fs"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
)

func thPreflight(t *testing.T, info osInfo, osErr error, arch string) error {
	t.Helper()
	tt := ts{d: deps{osRelease: func() (osInfo, error) { return info, osErr }, arch: arch}}
	checks := tt.preflight()
	if len(checks) != 1 || checks[0].ID != "tailscale.os" || checks[0].Run == nil {
		t.Fatalf("preflight = %+v, want the one check tailscale.os", checks)
	}
	return checks[0].Run(context.Background(), core.Env{})
}

func TestTHPreflightPasses(t *testing.T) {
	for _, c := range []struct {
		info osInfo
		arch string
	}{
		{osInfo{"ubuntu", "jammy"}, "amd64"}, {osInfo{"ubuntu", "noble"}, "arm64"},
		{osInfo{"debian", "bookworm"}, "amd64"}, {osInfo{"debian", "trixie"}, "arm64"},
	} {
		if err := thPreflight(t, c.info, nil, c.arch); err != nil {
			t.Errorf("%+v on %s: %v", c.info, c.arch, err)
		}
	}
}

// TestTHPreflightFails: each reason names what was found, and starts with the package name. The check reads
// only the operating system, never tailscaled.
func TestTHPreflightFails(t *testing.T) {
	for name, c := range map[string]struct {
		info osInfo
		arch string
		want []string
	}{
		"ubuntu too old":               {osInfo{"ubuntu", "focal"}, "amd64", []string{"ubuntu", "focal"}},
		"debian too old":               {osInfo{"debian", "bullseye"}, "amd64", []string{"debian", "bullseye"}},
		"codename of the other distro": {osInfo{"ubuntu", "bookworm"}, "amd64", []string{"ubuntu", "bookworm"}},
		"another distribution":         {osInfo{"alpine", ""}, "amd64", []string{"alpine"}},
		"no codename":                  {osInfo{"debian", ""}, "amd64", []string{"debian"}},
		"32-bit x86":                   {osInfo{"ubuntu", "noble"}, "386", []string{"386", "amd64", "arm64"}},
		"32-bit arm":                   {osInfo{"debian", "bookworm"}, "arm", []string{"arm"}},
	} {
		err := thPreflight(t, c.info, nil, c.arch)
		if err == nil || !strings.HasPrefix(err.Error(), "tailscale:") {
			t.Errorf("%s: error %v, want one starting with tailscale:", name, err)
			continue
		}
		for _, part := range c.want {
			if !strings.Contains(err.Error(), part) {
				t.Errorf("%s: error %q lacks %q", name, err, part)
			}
		}
	}
	if err := thPreflight(t, osInfo{}, fs.ErrNotExist, "amd64"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("an unreadable os-release gave %v, want the read error", err)
	}
}
