package firewall

import (
	"context"
	"fmt"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
)

// StateInput is the input of firewall.state.enable and
// firewall.state.disable (v1).
type StateInput struct {
	core.Guarded
}

// DefaultsInput is the input of firewall.defaults.set (v1): the default
// policies to set, as /etc/default/ufw spells them; an omitted direction
// keeps its policy, and at least one is set.
type DefaultsInput struct {
	core.Guarded
	Incoming string `json:"incoming,omitempty" jsonschema:"enum=ACCEPT,enum=DROP,enum=REJECT" jsonschema_description:"Default policy for incoming traffic."`
	Outgoing string `json:"outgoing,omitempty" jsonschema:"enum=ACCEPT,enum=DROP,enum=REJECT" jsonschema_description:"Default policy for outgoing traffic."`
	Routed   string `json:"routed,omitempty" jsonschema:"enum=ACCEPT,enum=DROP,enum=REJECT" jsonschema_description:"Default policy for routed (forwarded) traffic."`
}

// LoggingInput is the input of firewall.logging.set (v1).
type LoggingInput struct {
	core.Guarded
	Level string `json:"level" jsonschema:"enum=off,enum=low,enum=medium,enum=high,enum=full" jsonschema_description:"UFW's logging level."`
}

// mismatch is a verify failure: the configuration does not show what the
// change made.
func mismatch(format string, args ...any) error {
	return errcode.New(errcode.FirewallPredictionMismatch, fmt.Sprintf(format, args...))
}

// stateEnable is firewall.state.enable: start UFW (spec 2.3, transition
// enable), a no-op when it is enabled.
func (f fw) stateEnable() core.ActionDef {
	return f.stateAction("firewall.state.enable", "Enable UFW.", true)
}

// stateDisable is firewall.state.disable: stop UFW, a no-op when it is
// disabled.
func (f fw) stateDisable() core.ActionDef {
	return f.stateAction("firewall.state.disable", "Disable UFW.", false)
}

func (f fw) stateAction(name, summary string, enable bool) core.ActionDef {
	return defineMutation(core.ActionSpec{Name: name, Summary: summary}, mutation[StateInput]{
		f:       f,
		guarded: func(in StateInput) core.Guarded { return in.Guarded },
		build: func(context.Context, core.Env, StateInput) buildFunc {
			return func(fwstate.Config, fwstate.Loaded) (change, error) { return change{enable: &enable}, nil }
		},
		check: func(_ context.Context, _ core.Env, _ StateInput, cfg fwstate.Config) error {
			if cfg.Enabled != enable {
				return mismatch("UFW's ENABLED is %s after the change", yesNo(cfg.Enabled))
			}
			return nil
		},
	})
}

// defaultsSet is firewall.defaults.set: set the default policies that
// differ.
func (f fw) defaultsSet() core.ActionDef {
	dirs := func(in DefaultsInput) [][2]string {
		return [][2]string{{"incoming", in.Incoming}, {"outgoing", in.Outgoing}, {"routed", in.Routed}}
	}
	return defineMutation(core.ActionSpec{Name: "firewall.defaults.set", Summary: "Set UFW's default policies."}, mutation[DefaultsInput]{
		f:       f,
		guarded: func(in DefaultsInput) core.Guarded { return in.Guarded },
		build: func(_ context.Context, _ core.Env, in DefaultsInput) buildFunc {
			return func(cfg fwstate.Config, _ fwstate.Loaded) (change, error) {
				if in.Incoming == "" && in.Outgoing == "" && in.Routed == "" {
					return change{}, &errcode.Error{Code: errcode.InvalidInput, Detail: "set at least one default policy",
						Fields: []errcode.FieldError{{Path: "/incoming", Message: "set incoming, outgoing or routed"}}}
				}
				var c change
				for _, d := range dirs(in) {
					cur, err := policyOf(cfg, d[0])
					if err != nil {
						return change{}, err
					}
					if d[1] == "" || d[1] == cur {
						continue
					}
					op, err := defaultOp(d[0], d[1])
					if err != nil {
						return change{}, err
					}
					c.ops = append(c.ops, op)
				}
				return c, nil
			}
		},
		check: func(_ context.Context, _ core.Env, in DefaultsInput, cfg fwstate.Config) error {
			for _, d := range dirs(in) {
				if cur, err := policyOf(cfg, d[0]); err != nil || (d[1] != "" && cur != d[1]) {
					return mismatch("the default %s policy is %s after the change, not %s", d[0], cur, d[1])
				}
			}
			return nil
		},
	})
}

// loggingSet is firewall.logging.set: set UFW's logging level.
func (f fw) loggingSet() core.ActionDef {
	return defineMutation(core.ActionSpec{Name: "firewall.logging.set", Summary: "Set UFW's logging level."}, mutation[LoggingInput]{
		f:       f,
		guarded: func(in LoggingInput) core.Guarded { return in.Guarded },
		build: func(_ context.Context, _ core.Env, in LoggingInput) buildFunc {
			return func(cfg fwstate.Config, _ fwstate.Loaded) (change, error) {
				if cfg.LogLevel == in.Level {
					return change{}, nil
				}
				op, err := loggingOp(in.Level)
				if err != nil {
					return change{}, errcode.New(errcode.InvalidInput, err.Error())
				}
				return change{ops: []ufwOp{op}}, nil
			}
		},
		check: func(_ context.Context, _ core.Env, in LoggingInput, cfg fwstate.Config) error {
			if cfg.LogLevel != in.Level {
				return mismatch("the logging level is %s after the change, not %s", cfg.LogLevel, in.Level)
			}
			return nil
		},
	})
}
