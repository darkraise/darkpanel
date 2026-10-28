package firewall

import (
	"context"
	"errors"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules/firewall/conf"
	"github.com/darkraise/darkpanel/internal/modules/firewall/eval"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/modules/firewall/guard"
	"github.com/darkraise/darkpanel/internal/modules/firewall/loaded"
	"github.com/darkraise/darkpanel/internal/modules/firewall/predict"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
	"github.com/darkraise/darkpanel/internal/platform/netstat"
	"github.com/darkraise/darkpanel/internal/platform/tslocal"
)

// checkFunc runs the lockout guard on st: guard.Check, or a test's stand-in.
type checkFunc func(ctx context.Context, env core.Env, st fwstate.Loaded, in guard.Input) (core.Guard, []guard.FlowResult, error)

// fw is everything the firewall's handlers use: the host-facing functions
// of deps and, for plans, the IPV6=no refusal and the lockout guard.
type fw struct {
	d           deps
	ipv6Refusal func(cfg fwstate.Config, cur fwstate.Loaded, tr fwstate.Transition) error
	check       checkFunc
}

// productionFW is the firewall over this host.
func productionFW() fw {
	return fw{
		d: deps{
			root:       "/",
			readConfig: conf.Read,
			readLoaded: loaded.Reader{}.Read,
			predict:    predict.Predict,
			edit:       predict.Edit,
			files:      predict.Files,
			compare:    loaded.Compare,
			sysctl:     loaded.PredictSysctl,
			lockout:    lockoutVia(guard.Check),

			docker:      newDockerClient(dockerapi.DefaultSocket),
			readVariant: loaded.Reader{}.ReadVariant,
			sockets:     netstat.Sockets,
			interfaces:  netstat.Interfaces,
			routeGet:    netstat.RouteGet,
			shieldsUp:   shieldsUpVia(tslocal.New(tslocal.DefaultSocket).Prefs),
		},
		ipv6Refusal: predict.IPv6Refusal,
		check:       guard.Check,
	}
}

// lockoutVia is the lockout guard of a revert's connectivity exception
// (deps.lockout): check evaluates, with the real evaluator, the flows of
// the snapshot's guard context and of the host's live facts. A revert has
// no apply-time refresh and no retired exposure.
func lockoutVia(check checkFunc) func(ctx context.Context, env core.Env, st fwstate.Loaded, gc *core.GuardContext) (bool, string, error) {
	return func(ctx context.Context, env core.Env, st fwstate.Loaded, gc *core.GuardContext) (bool, string, error) {
		g, _, err := check(ctx, env, st, guard.Input{Plan: gc, Eval: eval.Evaluate})
		if err != nil {
			return false, "", err
		}
		return g.OK, g.Message, nil
	}
}

// dockerClient is the Docker client as the firewall's dockerAPI.
type dockerClient struct{ *dockerapi.Client }

// newDockerClient returns the client of the daemon on socket; it opens no
// connection until the first call.
func newDockerClient(socket string) dockerAPI {
	return dockerClient{dockerapi.New(socket)}
}

// Events opens the daemon's event stream. On an error it returns a nil
// interface rather than the client's nil stream, which inside a
// dockerEvents would compare non-nil.
func (c dockerClient) Events(ctx context.Context, o dockerapi.EventsOptions) (dockerEvents, error) {
	s, err := c.Client.Events(ctx, o)
	if err != nil {
		return nil, err
	}
	return s, nil
}

// shieldsUpVia reads Tailscale's shields-up preference through prefs. A
// tailscaled that cannot be reached is not an error: shields-up is then
// unknown.
func shieldsUpVia(prefs func(ctx context.Context) (*tslocal.Prefs, error)) func(ctx context.Context) (on, known bool, err error) {
	return func(ctx context.Context) (bool, bool, error) {
		p, err := prefs(ctx)
		if errors.Is(err, tslocal.ErrUnavailable) {
			return false, false, nil
		}
		if err != nil {
			return false, false, err
		}
		return p.ShieldsUp, true, nil
	}
}
