package firewall

import (
	"context"
	"fmt"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/modules/firewall/model"
	"github.com/darkraise/darkpanel/internal/platform/netfilter"
)

// Status is the output of resource firewall.status (v1, spec 2.2): the
// configured and loaded state, every rule with its origin, and whether UFW
// is active, in sync and supported.
type Status struct {
	Supported   bool              `json:"supported"`
	Unsupported []string          `json:"unsupported"` // why the host is an unsupported configuration (spec 2.1); empty when supported
	Active      bool              `json:"active"`      // UFW's chains are loaded
	InSync      bool              `json:"inSync"`      // the loaded state is what the configuration predicts; false when unsupported
	OutOfSync   []ChainDifference `json:"outOfSync"`   // the chains that differ
	SyncError   string            `json:"syncError,omitempty"`
	Configured  Configured        `json:"configured"`
	Loaded      LoadedState       `json:"loaded"`
	// Rules are UFW's rules in its order (model.Merge): Darkpanel's carry
	// origin darkpanel, the others manual; a manual rule the model cannot
	// express carries only its id, origin and raw tuple, and is read-only.
	Rules []model.Rule `json:"rules"`
}

// Configured is UFW's configured state (spec 2.1).
type Configured struct {
	Enabled    bool     `json:"enabled"` // ENABLED=yes in ufw.conf
	LogLevel   string   `json:"logLevel"`
	IPv6       bool     `json:"ipv6"`
	Defaults   Defaults `json:"defaults"`
	SysctlFile string   `json:"sysctlFile,omitempty"` // IPT_SYSCTL
}

// Defaults are UFW's default policies, as /etc/default/ufw spells them
// (ACCEPT, DROP or REJECT).
type Defaults struct {
	Incoming string `json:"incoming"`
	Outgoing string `json:"outgoing"`
	Routed   string `json:"routed"`
}

// LoadedState is the loaded state (spec 2.1) in summary.
type LoadedState struct {
	Variant string            `json:"variant"` // nft or legacy
	IPv6    bool              `json:"ipv6"`    // ip6tables is present
	Sysctl  map[string]string `json:"sysctl"`  // the supported keys' live values
	Chains  []LoadedChain     `json:"chains"`
}

// LoadedChain is one loaded chain.
type LoadedChain struct {
	Family string `json:"family"`
	Table  string `json:"table"`
	Chain  string `json:"chain"`
	Policy string `json:"policy,omitempty"` // built-in chains only
	Rules  int    `json:"rules"`
}

// ufwRunning reports whether UFW's user chains are loaded: UFW is running.
func ufwRunning(st fwstate.Loaded) bool { return chainOf(st.V4, "filter", "ufw-user-input") != nil }

// ufwNeverStarted reports whether not even UFW's primary chains are loaded:
// UFW has not started since boot (a stop keeps them, empty).
func ufwNeverStarted(st fwstate.Loaded) bool {
	return chainOf(st.V4, "filter", "ufw-before-logging-input") == nil
}

// status reads resource firewall.status.
func (f fw) status(ctx context.Context, env core.Env) (Status, error) {
	cfg, err := f.d.readConfig(f.d.root)
	if err != nil {
		return Status{}, fmt.Errorf("firewall: status: %w", err)
	}
	cur, err := f.d.readLoaded(ctx, env)
	if err != nil {
		return Status{}, fmt.Errorf("firewall: status: %w", err)
	}
	st := Status{
		Unsupported: hostUnsupported(cfg, cur),
		Active:      ufwRunning(cur),
		OutOfSync:   []ChainDifference{},
		Configured: Configured{
			Enabled: cfg.Enabled, LogLevel: cfg.LogLevel, IPv6: cfg.IPv6, SysctlFile: cfg.SysctlFile,
			Defaults: Defaults{Incoming: cfg.DefaultInput, Outgoing: cfg.DefaultOutput, Routed: cfg.DefaultForward},
		},
		Loaded: LoadedState{Variant: cur.Variant, IPv6: cur.V6 != nil, Sysctl: cur.Sysctl, Chains: loadedChains(cur)},
		Rules:  model.Merge(cfg.Rules4, cfg.Rules6),
	}
	if st.Rules == nil {
		st.Rules = []model.Rule{}
	}
	if st.Loaded.Sysctl == nil {
		st.Loaded.Sysctl = map[string]string{}
	}
	st.Supported = len(st.Unsupported) == 0
	if !st.Supported {
		return st, nil
	}
	diffs, err := f.syncDiffs(cfg, cur)
	if err != nil {
		st.SyncError = err.Error()
		return st, nil
	}
	st.InSync, st.OutOfSync = len(diffs) == 0, chainDifferences(diffs)
	return st, nil
}

// loadedChains lists every chain of both families in save order.
func loadedChains(st fwstate.Loaded) []LoadedChain {
	out := []LoadedChain{}
	for _, f := range []struct {
		fam fwstate.Family
		rs  *netfilter.Ruleset
	}{{fwstate.V4, st.V4}, {fwstate.V6, st.V6}} {
		if f.rs == nil {
			continue
		}
		for _, t := range f.rs.Tables {
			for _, c := range t.Chains {
				out = append(out, LoadedChain{Family: string(f.fam), Table: t.Name, Chain: c.Name, Policy: c.Policy, Rules: len(c.Rules)})
			}
		}
	}
	return out
}
