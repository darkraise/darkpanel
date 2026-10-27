package firewall

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/modules/firewall/model"
)

// suRuleRaw is a manual rule the model cannot express (UFW keeps the IPv6
// host bits of 2001:db8::1/32).
const suRuleRaw = "### tuple ### deny any any ::/0 any 2001:db8::1/32 in"

func suStatus(t *testing.T, h *fakeHost) Status {
	t.Helper()
	f := fw{d: h.deps()}
	st, err := f.status(context.Background(), h.env())
	if err != nil {
		t.Fatal(err)
	}
	return st
}

// TestStatus: the configured and loaded state, every rule with its origin
// (an unrepresentable manual rule read-only with its tuple), active, in
// sync and supported.
func TestStatus(t *testing.T) {
	h := newFakeHost(t)
	s := h.state()
	s.Rules6 = append(s.Rules6, suRuleRaw)
	h.setState(s)
	st := suStatus(t, h)

	if !st.Supported || len(st.Unsupported) != 0 || !st.Active || !st.InSync || len(st.OutOfSync) != 0 || st.SyncError != "" {
		t.Errorf("flags %+v, want supported, active and in sync", st)
	}
	wantCfg := Configured{Enabled: true, LogLevel: "low", IPv6: true, Defaults: Defaults{Incoming: "DROP", Outgoing: "ACCEPT", Routed: "DROP"},
		SysctlFile: "/etc/ufw/sysctl.conf"}
	if st.Configured != wantCfg {
		t.Errorf("configured %+v, want %+v", st.Configured, wantCfg)
	}
	if len(st.Rules) != 3 {
		t.Fatalf("rules %+v, want 3", st.Rules)
	}
	if r := st.Rules[0]; r.ID != fhTag || r.Family != model.FamilyBoth || r.Origin != model.OriginDarkpanel || r.ToPort != "22" {
		t.Errorf("rule 1 %+v, want the both-family Darkpanel rule", r)
	}
	if r := st.Rules[1]; r.Origin != model.OriginManual || r.From != "10.0.0.0/8" || r.Raw != "" {
		t.Errorf("rule 2 %+v, want the IPv4 manual rule", r)
	}
	if r := st.Rules[2]; r.Origin != model.OriginManual || r.Raw != suRuleRaw {
		t.Errorf("rule 3 %+v, want the unrepresentable manual rule with its tuple", r)
	}
	if st.Loaded.Variant != "nft" || !st.Loaded.IPv6 || !reflect.DeepEqual(st.Loaded.Sysctl, h.sysctl) {
		t.Errorf("loaded %+v", st.Loaded)
	}
	wantChains := []LoadedChain{
		{Family: "v4", Table: "filter", Chain: "INPUT", Policy: "DROP", Rules: 1},
		{Family: "v4", Table: "filter", Chain: "FORWARD", Policy: "DROP", Rules: 0},
		{Family: "v4", Table: "filter", Chain: "OUTPUT", Policy: "ACCEPT", Rules: 0},
		{Family: "v4", Table: "filter", Chain: "ufw-user-input", Rules: 1},
		{Family: "v6", Table: "filter", Chain: "INPUT", Policy: "DROP", Rules: 1},
		{Family: "v6", Table: "filter", Chain: "FORWARD", Policy: "DROP", Rules: 0},
		{Family: "v6", Table: "filter", Chain: "OUTPUT", Policy: "ACCEPT", Rules: 0},
		{Family: "v6", Table: "filter", Chain: "ufw-user-input", Rules: 1},
	}
	if !reflect.DeepEqual(st.Loaded.Chains, wantChains) {
		t.Errorf("chains %+v, want %+v", st.Loaded.Chains, wantChains)
	}
	if len(h.keeps) != 1 || !h.keeps[0](fwstate.V4, "filter", "INPUT") || h.keeps[0](fwstate.V4, "filter", "DOCKER") {
		t.Error("the in-sync comparison does not cover the filter built-ins and UFW's chains only")
	}
	b, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"unsupported":[]`, `"outOfSync":[]`, `"inSync":true`, `"active":true`} {
		if !strings.Contains(string(b), key) {
			t.Errorf("JSON %s lacks %s", b, key)
		}
	}
}

// TestStatusOutOfSyncAndUnsupported: differing chains are listed; an
// unsupported host is not compared; a failed comparison is reported, not
// an error; an unreadable state is.
func TestStatusOutOfSyncAndUnsupported(t *testing.T) {
	h := newFakeHost(t)
	h.diffs = []fwstate.ChainDiff{{Family: fwstate.V4, Table: "filter", Chain: "ufw-user-input", OnlyB: []string{"-p tcp -m tcp --dport 22 -j ACCEPT"}}}
	st := suStatus(t, h)
	want := []ChainDifference{{Family: "v4", Table: "filter", Chain: "ufw-user-input", OnlyLoaded: []string{"-p tcp -m tcp --dport 22 -j ACCEPT"}}}
	if st.InSync || !reflect.DeepEqual(st.OutOfSync, want) {
		t.Errorf("in sync %v, out of sync %+v; want %+v", st.InSync, st.OutOfSync, want)
	}

	h = newFakeHost(t)
	h.loaded.Unsupported = []string{"iptables-legacy table filter chain INPUT holds 1 rule(s) outside the nft variant UFW uses"}
	h.writeFile("/etc/default/ufw", []byte(strings.Replace(string(h.state().files()["/etc/default/ufw"]), "MANAGE_BUILTINS=no", "MANAGE_BUILTINS=yes", 1)))
	st = suStatus(t, h)
	if st.Supported || st.InSync || len(h.keeps) != 0 || len(st.Unsupported) != 2 ||
		!strings.Contains(st.Unsupported[0], "MANAGE_BUILTINS") || !strings.Contains(st.Unsupported[1], "iptables-legacy") {
		t.Errorf("status %+v (compared %d times), want unsupported with the configuration's reason first, not compared", st, len(h.keeps))
	}

	h = newFakeHost(t)
	h.predictErr = errors.New("predict: translate: /etc/ufw/after.rules is empty")
	if st := suStatus(t, h); st.InSync || !strings.Contains(st.SyncError, "after.rules is empty") || !st.Supported {
		t.Errorf("status %+v, want not in sync with the prediction's error", st)
	}

	h = newFakeHost(t)
	h.loadErr = errors.New("loaded: /usr/sbin/iptables is not installed")
	if _, err := (fw{d: h.deps()}).status(context.Background(), h.env()); err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Errorf("status = %v, want the reader's error", err)
	}
}

// TestUFWChainsLoaded: UFW is running when its user chains are loaded, and
// never started when not even its primary chains are.
func TestUFWChainsLoaded(t *testing.T) {
	st := fwstate.Loaded{V4: fhParse(t, "*filter\n:INPUT ACCEPT [0:0]\n:ufw-before-logging-input - [0:0]\nCOMMIT\n")}
	if ufwRunning(st) || ufwNeverStarted(st) {
		t.Error("a stopped UFW reads as running or never started")
	}
	st.V4 = fhParse(t, fhSave)
	if !ufwRunning(st) || !ufwNeverStarted(st) {
		t.Error("the fake host's state: want running, with no primary chain")
	}
	if ufwRunning(fwstate.Loaded{}) || !ufwNeverStarted(fwstate.Loaded{}) {
		t.Error("no ruleset reads as running")
	}
}
