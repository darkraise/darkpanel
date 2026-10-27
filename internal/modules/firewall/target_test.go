package firewall

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/modules/firewall/guard"
	"github.com/darkraise/darkpanel/internal/modules/firewall/model"
)

// fwStub is the plan-time guard and IPv6 refusal of a test fw: check
// answers guard (its ID defaulting to firewall.lockout), flows and err,
// recording every input and state; ipv6Refusal answers ipv6Err.
type fwStub struct {
	guard   core.Guard
	flows   []guard.FlowResult
	err     error
	inputs  []guard.Input
	states  []fwstate.Loaded
	ipv6Err error
}

// newFW is a fw over the fake host h with a passing stub guard.
func newFW(h *fakeHost) (fw, *fwStub) {
	s := &fwStub{guard: core.Guard{ID: guard.GuardID, OK: true, Message: "every protected flow passes (1 checked)"}}
	f := fw{
		d: h.deps(),
		ipv6Refusal: func(fwstate.Config, fwstate.Loaded, fwstate.Transition) error {
			return s.ipv6Err
		},
		check: func(_ context.Context, _ core.Env, st fwstate.Loaded, in guard.Input) (core.Guard, []guard.FlowResult, error) {
			s.inputs, s.states = append(s.inputs, in), append(s.states, st)
			if s.err != nil {
				return core.Guard{}, nil, s.err
			}
			g := s.guard
			if g.ID == "" {
				g.ID = guard.GuardID
			}
			return g, s.flows, nil
		},
	}
	return f, s
}

// The fake host's states for the three cases of UFW's chains: never
// started (no chain), stopped (primary chains left) and running.
const (
	tgNeverStarted = "*filter\n:INPUT ACCEPT [0:0]\n:FORWARD ACCEPT [0:0]\n:OUTPUT ACCEPT [0:0]\nCOMMIT\n"
	tgStopped      = "*filter\n:INPUT ACCEPT [0:0]\n:FORWARD ACCEPT [0:0]\n:OUTPUT ACCEPT [0:0]\n:ufw-before-logging-input - [0:0]\nCOMMIT\n"
	tgRunning      = "*filter\n:INPUT DROP [0:0]\n:FORWARD DROP [0:0]\n:OUTPUT ACCEPT [0:0]\n:ufw-before-logging-input - [0:0]\n:ufw-user-input - [0:0]\nCOMMIT\n"
)

// tgHost is a fake host whose ufw edits the enabled state, the defaults
// and rules, and whose predictor appends each transition it predicts to
// the variant, so a prediction's history reads "nft|enable|reload".
type tgHost struct {
	*fakeHost
	f        fw
	stub     *fwStub
	ipv6Args []fwstate.Transition
	sysctlTr []fwstate.Transition
}

func newTGHost(t *testing.T, save string, enabled bool) *tgHost {
	t.Helper()
	h := &tgHost{fakeHost: newFakeHost(t)}
	s := h.state()
	s.Enabled = enabled
	h.setState(s)
	h.loaded = fwstate.Loaded{Variant: "nft", V4: fhParse(t, save), V6: fhParse(t, save)}
	h.ufw["--force enable"] = func(s *fhState) int { s.Enabled = true; return 0 }
	h.ufw["disable"] = func(s *fhState) int { s.Enabled = false; return 0 }
	h.ufw["reload"] = func(*fhState) int { return 0 }
	h.ufw["allow in proto tcp from any to any port 80 comment darkpanel:01J00000000000000000000002"] = func(s *fhState) int {
		s.Rules4 = append(s.Rules4, strings.Replace(fhRule22v4, "22", "80", 1))
		return 0
	}
	h.f, h.stub = newFW(h.fakeHost)
	h.f.d.predict = func(_ fwstate.Config, cur fwstate.Loaded, tr fwstate.Transition) (fwstate.Loaded, error) {
		cur.Variant += "|" + string(tr)
		return cur, nil
	}
	h.f.d.sysctl = func(_ fwstate.Config, cur map[string]string, tr fwstate.Transition) (map[string]string, error) {
		h.sysctlTr = append(h.sysctlTr, tr)
		out := maps.Clone(cur)
		out["net/ipv4/ip_forward"] = "1"
		return out, nil
	}
	h.f.ipv6Refusal = func(_ fwstate.Config, _ fwstate.Loaded, tr fwstate.Transition) error {
		h.ipv6Args = append(h.ipv6Args, tr)
		return h.stub.ipv6Err
	}
	return h
}

// cur is the loaded state as the host's reader returns it, with the live
// sysctl values.
func (h *tgHost) cur() fwstate.Loaded {
	st := h.loaded
	st.Sysctl = maps.Clone(h.sysctl)
	return st
}

func tgAllow80(t *testing.T) ufwOp {
	t.Helper()
	op, err := addRuleOp(model.Rule{ID: "01J00000000000000000000002", Action: model.ActionAllow, Direction: model.DirIn,
		Proto: model.ProtoTCP, From: model.Any, To: model.Any, ToPort: "80", Family: model.FamilyBoth})
	if err != nil {
		t.Fatal(err)
	}
	return op
}

func tgCmds(cmds [][]string) []string {
	var out []string
	for _, c := range cmds {
		out = append(out, strings.Join(c, " "))
	}
	return out
}

var yes, no = true, false

// TestTargetTransitions: the transition, the commands Apply runs after the
// change's own and the predicted loaded state, for each starting point.
func TestTargetTransitions(t *testing.T) {
	cases := []struct {
		name    string
		save    string
		enabled bool
		change  func(t *testing.T) change
		tr      fwstate.Transition
		cmds    []string
		history string
		final   bool
	}{
		{"rule on an active UFW", tgRunning, true, func(t *testing.T) change { return change{ops: []ufwOp{tgAllow80(t)}} },
			fwstate.TransReload, []string{"reload"}, "nft|reload", true},
		{"rule on an inactive UFW", tgStopped, false, func(t *testing.T) change { return change{ops: []ufwOp{tgAllow80(t)}} },
			fwstate.TransNone, nil, "nft|none", false},
		{"enable a stopped UFW", tgStopped, false, func(*testing.T) change { return change{enable: &yes} },
			fwstate.TransEnable, []string{"--force enable"}, "nft|enable", true},
		{"first enable", tgNeverStarted, false, func(*testing.T) change { return change{enable: &yes} },
			fwstate.TransEnable, []string{"--force enable", "reload"}, "nft|enable|reload", true},
		{"enable with UFW's chains loaded", tgRunning, false, func(*testing.T) change { return change{enable: &yes} },
			fwstate.TransEnable, []string{"--force enable", "reload"}, "nft|reload", true},
		{"enable an active UFW", tgRunning, true, func(*testing.T) change { return change{enable: &yes} },
			fwstate.TransNone, nil, "nft|none", true},
		{"disable", tgRunning, true, func(*testing.T) change { return change{enable: &no} },
			fwstate.TransDisable, []string{"disable"}, "nft|disable", false},
		{"repair an active UFW", tgRunning, true, func(*testing.T) change { return change{repair: true} },
			fwstate.TransReload, []string{"reload"}, "nft|reload", true},
		{"repair a running UFW configured off", tgRunning, false, func(*testing.T) change { return change{repair: true} },
			fwstate.TransDisable, []string{"disable"}, "nft|disable", false},
		{"repair a stopped UFW", tgStopped, false, func(*testing.T) change { return change{repair: true} },
			fwstate.TransNone, nil, "nft|none", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newTGHost(t, c.save, c.enabled)
			cfg0 := h.config()
			tg, err := h.f.predictTarget(cfg0, h.cur(), c.change(t))
			if err != nil {
				t.Fatal(err)
			}
			if tg.tr != c.tr || !reflect.DeepEqual(tgCmds(tg.cmds), c.cmds) || tg.pred.Variant != c.history || tg.final.Enabled != c.final {
				t.Errorf("target %s %q %s final enabled %v; want %s %q %s %v", tg.tr, tgCmds(tg.cmds), tg.pred.Variant, tg.final.Enabled,
					c.tr, c.cmds, c.history, c.final)
			}
			if tg.pred.Sysctl["net/ipv4/ip_forward"] != "1" || len(h.sysctlTr) != 1 || h.sysctlTr[0] != c.tr {
				t.Errorf("sysctl %v predicted for %v, want the sysctl prediction for %s", tg.pred.Sysctl, h.sysctlTr, c.tr)
			}
		})
	}
}

// TestTargetCommands: the configuration after the change's commands holds
// their effect, and the IPv6 refusal is asked about the transition.
func TestTargetCommands(t *testing.T) {
	h := newTGHost(t, tgRunning, true)
	tg, err := h.f.predictTarget(h.config(), h.cur(), change{ops: []ufwOp{tgAllow80(t)}})
	if err != nil {
		t.Fatal(err)
	}
	if len(tg.cfgN.Rules4) != 3 || len(tg.final.Rules4) != 3 || !strings.Contains(tg.cfgN.Rules4[2].Raw, "allow tcp 80 ") {
		t.Errorf("rules after the change %+v, want the new rule appended", tg.cfgN.Rules4)
	}
	if !reflect.DeepEqual(h.ipv6Args, []fwstate.Transition{fwstate.TransReload}) {
		t.Errorf("IPv6 refusal asked for %v, want reload", h.ipv6Args)
	}

	h.stub.ipv6Err = errcode.New(errcode.FirewallUnsupportedConfiguration, "chain DOCKER-USER; set IPV6=yes in /etc/default/ufw")
	_, err = h.f.predictTarget(h.config(), h.cur(), change{ops: []ufwOp{tgAllow80(t)}})
	if errcode.From(err).Code != errcode.FirewallUnsupportedConfiguration {
		t.Errorf("predictTarget = %v, want the IPv6 refusal", err)
	}

	h = newTGHost(t, tgRunning, true)
	op := tgAllow80(t)
	op.Args = append([]string{"route"}, op.Args...)
	_, err = h.f.predictTarget(h.config(), h.cur(), change{ops: []ufwOp{op}})
	if e := errcode.From(err); e.Code != errcode.InvalidInput || !strings.Contains(e.Detail, "ufw route allow") {
		t.Errorf("predictTarget = %v, want an invalid input naming the command", err)
	}

	h = newTGHost(t, tgRunning, true)
	refusal := errcode.New(errcode.FirewallUnsupportedConfiguration, "/etc/ufw/sysctl.conf sets net/ipv4/ip_forward to \"2\"")
	h.f.d.sysctl = func(fwstate.Config, map[string]string, fwstate.Transition) (map[string]string, error) {
		return nil, refusal
	}
	if _, err := h.f.predictTarget(h.config(), h.cur(), change{repair: true}); !errors.Is(err, refusal) {
		t.Errorf("predictTarget = %v, want the sysctl refusal", err)
	}
	h = newTGHost(t, tgRunning, true)
	h.f.d.predict = func(fwstate.Config, fwstate.Loaded, fwstate.Transition) (fwstate.Loaded, error) {
		return fwstate.Loaded{}, errors.New("predict: translate: /etc/ufw/before.rules is empty")
	}
	if _, err := h.f.predictTarget(h.config(), h.cur(), change{repair: true}); err == nil || !strings.Contains(err.Error(), "before.rules is empty") {
		t.Errorf("predictTarget = %v, want the prediction's error", err)
	}
}

// TestTargetRestore: a restore's configuration is the snapshot's, its
// transition follows the snapshot's and the current UFW state, and the
// snapshot's sysctl values overlay the prediction for the keys that exist.
func TestTargetRestore(t *testing.T) {
	h := newTGHost(t, tgStopped, false)
	s := h.state()
	s.Enabled, s.Rules4, s.Rules6, s.Input = true, []string{fhRule3000v4}, nil, "REJECT"
	snapCfg := h.config()
	var entries []core.RestoreEntry
	for p, data := range s.files() {
		e := snapCfg.Files[p]
		e.Data = data
		snapCfg.Files[p] = e
	}
	for _, p := range fwstate.StateFiles {
		entries = append(entries, snapCfg.Files[p])
	}
	snap := snapshot{ID: "01J00000000000000000000009", Entries: entries,
		State: snapshotState{Enabled: true, Sysctl: map[string]string{"net/ipv4/conf/eth0/rp_filter": "0", "net/ipv4/conf/eth9/rp_filter": "1"}}}

	tg, err := h.f.predictTarget(h.config(), h.cur(), change{restore: &snap})
	if err != nil {
		t.Fatal(err)
	}
	if tg.tr != fwstate.TransEnable || len(tg.cfgN.Rules4) != 1 || tg.cfgN.DefaultInput != "REJECT" || len(tg.cfgN.Rules6) != 0 {
		t.Errorf("target %s rules %d/%d input %s; want enable with the snapshot's configuration", tg.tr, len(tg.cfgN.Rules4), len(tg.cfgN.Rules6), tg.cfgN.DefaultInput)
	}
	if v, ok := tg.pred.Sysctl["net/ipv4/conf/eth9/rp_filter"]; ok || tg.pred.Sysctl["net/ipv4/conf/eth0/rp_filter"] != "0" || tg.pred.Sysctl["net/ipv4/ip_forward"] != "1" {
		t.Errorf("sysctl %v (eth9 %q), want the snapshot's eth0 value over the prediction and no gone interface", tg.pred.Sysctl, v)
	}

	h.f.d.sysctl = func(_ fwstate.Config, cur map[string]string, _ fwstate.Transition) (map[string]string, error) {
		return cur, nil
	}
	cur := h.cur()
	if _, err := h.f.predictTarget(h.config(), cur, change{restore: &snap}); err != nil {
		t.Fatal(err)
	}
	if cur.Sysctl["net/ipv4/conf/eth0/rp_filter"] != "1" {
		t.Error("the snapshot's sysctl values overwrote the loaded state's")
	}
}
