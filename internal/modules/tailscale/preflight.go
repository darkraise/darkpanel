package tailscale

import (
	"context"
	"fmt"
	"slices"

	"github.com/darkraise/darkpanel/internal/core"
)

// supportedReleases are the codenames of each distribution Tailscale's stable
// track publishes packages for.
var supportedReleases = map[string][]string{
	"ubuntu": {"jammy", "noble"},
	"debian": {"bookworm", "trixie"},
}

// preflight is the module's preflight: it checks only the operating system
// and the architecture, never tailscaled, so the module is available on a host
// before tailscale is installed.
func (t ts) preflight() []core.PreflightCheck {
	return []core.PreflightCheck{{
		ID: ID + ".os",
		Run: func(context.Context, core.Env) error {
			info, err := t.d.osRelease()
			if err != nil {
				return err
			}
			if !slices.Contains(supportedReleases[info.ID], info.Codename) {
				return fmt.Errorf("tailscale: the operating system %q release %q is not supported; Ubuntu jammy or noble, or Debian bookworm or trixie, is needed", info.ID, info.Codename)
			}
			if t.d.arch != "amd64" && t.d.arch != "arm64" {
				return fmt.Errorf("tailscale: the architecture %s is not supported; amd64 or arm64 is needed", t.d.arch)
			}
			return nil
		},
	}}
}
