// Package fail2ban is the intrusion prevention module (M11 spec section 4): fail2ban jails and bans.
package fail2ban

import (
	"bytes"
	"context"
	"fmt"
	"net/netip"
	"os"
	"slices"
	"strings"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/platform"
)

// ID is the module id.
const ID = "fail2ban"

// GuardSelfBan is the guard of a recidive jail that could ban the caller.
const GuardSelfBan = ID + ".self_ban"

// lockKey serialises the module's own changes; aptLockKey is the system
// module's apt key, so an install never runs beside another apt run.
const (
	lockKey    = ID
	aptLockKey = "system.apt"
)

// The units the module reads: fail2ban's own, CrowdSec's, whose presence
// keeps Darkpanel from installing fail2ban, and the controller's, whose
// presence makes this the controller's own host.
const (
	fail2banUnit   = "fail2ban.service"
	crowdsecUnit   = "crowdsec.service"
	controllerUnit = "darkpanel-controller.service"
)

// The directories install creates before fail2ban's package does.
const (
	etcDir  = "/etc/fail2ban"
	jailDir = "/etc/fail2ban/jail.d"
)

// deps is what the module reaches outside core.Env; tests replace it.
type deps struct {
	readFile func(name string) ([]byte, error)
}

type module struct{ d deps }

// Module returns the fail2ban module.
func Module() core.Module { return module{d: deps{readFile: os.ReadFile}} }

// Manifest declares the module.
func (m module) Manifest() core.Manifest {
	state := []string{core.UnitOpState}
	return core.Manifest{
		ID: ID, Version: 1, DefaultEnabled: true,
		Actions: slices.Concat(m.reads(), m.packageActions(), []core.ActionDef{m.settingsAction()}, m.banActions()),
		OwnedPaths: []core.OwnedPath{
			{Path: etcDir, Kind: core.OwnedDir},
			{Path: jailDir, Kind: core.OwnedDir},
			{Path: SettingsFile, Kind: core.OwnedFile},
		},
		ExternalUnits: []core.ExternalUnit{
			{Name: fail2banUnit, Ops: state}, {Name: crowdsecUnit, Ops: state}, {Name: controllerUnit, Ops: state},
		},
	}
}

// loaded reports whether systemd knows unit: its package is installed.
func loaded(ctx context.Context, env core.Env, unit string) (bool, error) {
	st, err := env.Units.State(ctx, unit)
	if err != nil {
		return false, fmt.Errorf("fail2ban: read %s: %w", unit, err)
	}
	return st.LoadState == "loaded", nil
}

// client runs fail2ban-client with args and returns its standard output; a
// non-zero exit is an error carrying its message.
func client(ctx context.Context, env core.Env, args []string) ([]byte, error) {
	res, err := env.Exec.Run(ctx, core.Cmd{Path: platform.Fail2banClientPath, Args: args})
	if err != nil {
		return nil, fmt.Errorf("fail2ban: run fail2ban-client %s: %w", strings.Join(args, " "), err)
	}
	if res.ExitCode != 0 {
		msg := strings.TrimSpace(string(bytes.TrimSpace(slices.Concat(res.Stderr, res.Stdout))))
		return nil, fmt.Errorf("fail2ban: fail2ban-client %s exited %d: %s", strings.Join(args, " "), res.ExitCode, lastLines(msg, 10))
	}
	return res.Stdout, nil
}

// lastLines is the last n lines of s.
func lastLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// callerAddr is the client address of the principal, as the controller saw
// it ("<addr>" or "<client> via <proxy>"); false for the agent socket and
// system principals.
func callerAddr(p core.Principal) (netip.Addr, bool) {
	f := strings.Fields(p.Source)
	if len(f) == 0 {
		return netip.Addr{}, false
	}
	a, err := netip.ParseAddr(f[0])
	if err != nil {
		return netip.Addr{}, false
	}
	return a.Unmap(), true
}

// invalid is a Definite invalid_input refusal of the field at path.
func invalid(path, msg string) error {
	return core.Definite(&errcode.Error{Code: errcode.InvalidInput, Detail: msg, Fields: []errcode.FieldError{{Path: path, Message: msg}}})
}

// requireInstalled refuses a change on a host without fail2ban.
func requireInstalled(ctx context.Context, env core.Env) error {
	ok, err := loaded(ctx, env, fail2banUnit)
	if err != nil {
		return err
	}
	if !ok {
		return core.Definite(errcode.New(errcode.ModuleUnavailable, "fail2ban is not installed on this host"))
	}
	return nil
}
