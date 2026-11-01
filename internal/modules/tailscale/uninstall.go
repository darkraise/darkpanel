package tailscale

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/platform"
)

// uninstallAptTimeout bounds the uninstall's apt commands: the runner gives a command whose context has no
// deadline only 30 seconds, and apt-get remove can take longer.
const uninstallAptTimeout = 5 * time.Minute

// UninstallOptions declares the option package (spec 1.11): keep leaves the tailscale package, remove takes it
// out when Darkpanel installed it.
func (m module) UninstallOptions() []core.UninstallOption {
	return []core.UninstallOption{{
		Key: "package", Values: []string{"keep", "remove"}, Default: "keep",
		Help: "remove the tailscale package if Darkpanel installed it",
	}}
}

// Uninstall with package=remove removes the tailscale package of the newest install Darkpanel recorded and not
// yet removed, releasing its hold first, then records the install as removed; with keep, or no such install, it
// changes nothing, so a tailscaled the owner installed is left. It keys on the install row and the host, so it
// succeeds when run again where it already ran. keepData does not change it: the module's tables go with the
// agent database, and the package is not Darkpanel's data.
func (m module) Uninstall(ctx context.Context, env core.Env, _ bool, opts map[string]string) error {
	switch v := opts["package"]; v {
	case "keep":
		return nil
	case "remove":
	default:
		return fmt.Errorf("tailscale: uninstall option package=%q is not keep or remove", v)
	}
	changeID, _, ok, err := liveInstall(ctx, env.DB.Read())
	if err != nil {
		return fmt.Errorf("tailscale: uninstall: %w", err)
	}
	if !ok {
		return nil
	}
	version, err := m.t.installedVersion(ctx, env)
	if err != nil {
		return err
	}
	// An empty version means the package is already gone: removed by hand, or by an earlier run that stopped
	// before recording the removal.
	if version != "" {
		aptCtx, cancel := context.WithTimeout(ctx, uninstallAptTimeout)
		defer cancel()
		held, err := m.t.held(aptCtx, env)
		if err != nil {
			return err
		}
		if held {
			if _, err := run(aptCtx, env, "/usr/bin/apt-mark", "unhold", "tailscale"); err != nil {
				return err
			}
		}
		if _, err := run(aptCtx, env, "/usr/bin/apt-get", platform.AptRemoveTailscale()...); err != nil {
			if !held {
				return err
			}
			// The row stays live so that running the uninstall again retries, which needs the host as it was. A
			// remove that ran out of time leaves aptCtx expired, so the hold is put back under a bound of its own.
			holdCtx, cancelHold := context.WithTimeout(ctx, uninstallAptTimeout)
			defer cancelHold()
			_, holdErr := run(holdCtx, env, "/usr/bin/apt-mark", "hold", "tailscale")
			return errors.Join(err, holdErr)
		}
	}
	return setInstallRemoved(ctx, env.DB, changeID, env.Clock.Now())
}
