package firewall

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
)

type w11c11FailRestore struct {
	*deeHost
	program string
	failed  bool
}

func (r *w11c11FailRestore) Run(ctx context.Context, cmd core.Cmd) (core.CmdResult, error) {
	if !r.failed && strings.HasSuffix(cmd.Path, r.program) {
		r.failed = true
		return core.CmdResult{ExitCode: 1, Stderr: []byte("restore: line 2 failed\n")}, nil
	}
	return r.deeHost.Run(ctx, cmd)
}

func TestW11c11FailedRestoreRevertsTheEnable(t *testing.T) {
	h := deeNewHost(t)
	exec := &w11c11FailRestore{deeHost: h, program: "/iptables-nft-restore"}
	_, cv, err := deePlanApply(t, deeEngineWith(t, h, exec), "firewall.docker.enable", `{}`)
	if !exec.failed || cv.State != core.StateReverted {
		t.Fatalf("enable = %+v, %v (restore failed: %v); want reverted", cv, err, exec.failed)
	}
	if owned := deeOwnedChains(h); len(owned) != 0 {
		t.Errorf("the revert left the chains %v loaded", owned)
	}
}

// IPv4 has loaded when the IPv6 restore fails, so the one effect covering both
// families matches neither its before-state nor its after-state. An unresolved
// effect blocks the whole revert (spec 1.2), so the IPv4 chains stay loaded.
func TestW11c11FailedIPv6RestoreLeavesTheChangeForRepair(t *testing.T) {
	h := deeNewHost(t)
	exec := &w11c11FailRestore{deeHost: h, program: "/ip6tables-nft-restore"}
	eng := deeEngineWith(t, h, exec)
	_, cv, err := deePlanApply(t, eng, "firewall.docker.enable", `{}`)
	if err != nil || !exec.failed || cv.State != core.StateNeedsRepair {
		t.Fatalf("enable = %+v, %v (restore failed: %v); want needs_repair", cv, err, exec.failed)
	}
	if cv.Error == nil || cv.Error.Code != string(errcode.NeedsRepair) || cv.Error.Cause != string(errcode.ChangeRevertFailed) {
		t.Errorf("change error = %+v, want core.needs_repair caused by change.revert_failed", cv.Error)
	}
	effects, err := eng.Effects(context.Background(), cv.ID)
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(effects, func(e core.EffectView) bool { return e.Resource == blockChainsResource })
	if i < 0 || effects[i].State != core.EffectUnresolved || effects[i].Reason != core.ReasonAmbiguous {
		t.Errorf("effects = %+v, want %s unresolved as ambiguous", effects, blockChainsResource)
	}
	if !deChainsLoaded(h.loaded.V4) || deChainsLoaded(h.loaded.V6) {
		t.Errorf("owned chains = %v, want the IPv4 chains kept and no IPv6 chains", deeOwnedChains(h))
	}
}

func TestW11c11BlockInputsSkipIPv6WhenOff(t *testing.T) {
	h := deeNewHost(t)
	deeConfirm(t, deeEngine(t, h), "firewall.docker.enable", `{}`)
	cfg := h.config()
	cfg.IPv6 = false
	inputs, err := blockChainInputs(cfg, h.loaded)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := inputs[fwstate.V4]; !ok {
		t.Error("no IPv4 input")
	}
	if _, ok := inputs[fwstate.V6]; ok {
		t.Error("an IPv6 input with IPv6 off")
	}
}

func TestW11c11ByInversesRevertKeepsAForeignDockerUserRule(t *testing.T) {
	ctx := context.Background()
	h := deeNewHost(t)
	eng := deeEngine(t, h)
	deeConfirm(t, eng, "firewall.docker.enable", `{}`)
	_, cv, err := deePlanApply(t, eng, "firewall.container.allow", `{"binding":`+deeBinding+`}`)
	if err != nil || cv.State != core.StatePending {
		t.Fatalf("allow = %+v, %v; want pending", cv, err)
	}
	if res, err := h.Run(ctx, core.Cmd{Path: ufwPath, Args: strings.Fields("allow in proto tcp from any to any port 8098")}); err != nil || res.ExitCode != 0 {
		t.Fatalf("the manual ufw allow = %+v, %v", res, err)
	}
	h.addForeign(t, deeForeign)
	if cv, err = eng.Revert(ctx, eeUser, cv.ID); err != nil || cv.State != core.StateReverted {
		t.Fatalf("revert = %+v, %v; want reverted", cv, err)
	}
	if got := deeTexts(h, fwstate.V4, "DOCKER-USER"); !slices.Contains(got, deeForeign) {
		t.Errorf("DOCKER-USER = %q, want the foreign rule %q kept", got, deeForeign)
	}
}
