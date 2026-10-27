package firewall

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/modules/firewall/model"
)

func obTestTuples(t *testing.T, v6 bool, lines ...string) []model.Tuple {
	t.Helper()
	var out []model.Tuple
	for _, l := range lines {
		tu, err := model.ParseTuple(l, v6)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, tu)
	}
	return out
}

// obTestID is the id model.Merge gives a rule of the given tuples.
func obTestID(t *testing.T, v4, v6 []string) string {
	t.Helper()
	return model.Merge(obTestTuples(t, false, v4...), obTestTuples(t, true, v6...))[0].ID
}

func obTestObserve(t *testing.T, h *fakeHost, kind core.EffectKind, resource string) core.Observation {
	t.Helper()
	obs, err := observe(context.Background(), h.env(), h.deps(), core.Effect{Kind: kind, Resource: resource})
	if err != nil {
		t.Fatal(err)
	}
	return obs
}

func obTestVersion(t *testing.T, h *fakeHost, pos4, pos6 int) string {
	t.Helper()
	return string(mustJSON(ufwVersion{Files: ufwHashes(h.config().Files), Pos4: pos4, Pos6: pos6}))
}

func TestRuleGroupsAgreeWithMerge(t *testing.T) {
	a4 := "### tuple ### allow tcp 80 0.0.0.0/0 any 0.0.0.0/0 in"
	a6 := "### tuple ### allow tcp 80 ::/0 any ::/0 in"
	b4 := "### tuple ### allow tcp 443 0.0.0.0/0 any 0.0.0.0/0 in"
	b6 := "### tuple ### allow tcp 443 ::/0 any ::/0 in"
	for name, c := range map[string]struct{ v4, v6 []string }{
		"in order":      {[]string{a4, b4, fhRule3000v4}, []string{a6, b6, fhRule53v6}},
		"out of order":  {[]string{a4, b4}, []string{fhRule53v6, b6, a6}},
		"halves apart":  {[]string{fhRule3000v4, a4}, []string{a6}},
		"duplicates":    {[]string{a4, a4}, []string{a6}},
		"only v6":       {nil, []string{a6, fhRule53v6}},
		"nothing":       {nil, nil},
		"tagged halves": {[]string{fhRule22v4, fhRule3000v4}, []string{fhRule22v6}},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := fwstate.Config{Rules4: obTestTuples(t, false, c.v4...), Rules6: obTestTuples(t, true, c.v6...)}
			groups := ruleGroups(cfg)
			var rules []model.Rule
			for _, g := range groups {
				rules = append(rules, g.Rule)
				if g.I4 >= 0 && g.I6 >= 0 && len(model.Merge(cfg.Rules4[g.I4:g.I4+1], cfg.Rules6[g.I6:g.I6+1])) != 1 {
					t.Errorf("group %+v pairs tuples that are not one rule", g)
				}
			}
			if want := model.Merge(cfg.Rules4, cfg.Rules6); len(rules) != len(want) || (len(want) > 0 && !reflect.DeepEqual(rules, want)) {
				t.Errorf("rules = %+v\nwant %+v", rules, want)
			}
		})
	}
}

func TestObserveRule(t *testing.T) {
	h := newFakeHost(t)
	obs := obTestObserve(t, h, core.EffectDelete, ruleResource(fhTag))
	wantState := fmt.Sprintf(`{"v4":%q,"v6":%q}`, fhRule22v4, fhRule22v6)
	if string(obs.Current.State) != wantState {
		t.Errorf("state = %s, want %s", obs.Current.State, wantState)
	}
	if got, want := string(obs.Current.Version), obTestVersion(t, h, 1, 1); got != want {
		t.Errorf("version = %s, want %s", got, want)
	}
	if obs.Matches != 1 || !obs.Exclusive || obs.Resource != "" {
		t.Errorf("observation = %+v", obs)
	}
	manual := obTestID(t, []string{fhRule3000v4}, nil)
	obs = obTestObserve(t, h, core.EffectDelete, ruleResource(manual))
	if want := fmt.Sprintf(`{"v4":%q}`, fhRule3000v4); string(obs.Current.State) != want {
		t.Errorf("manual rule state = %s, want %s", obs.Current.State, want)
	}
	if got, want := string(obs.Current.Version), obTestVersion(t, h, 2, 0); got != want {
		t.Errorf("manual rule version = %s, want %s", got, want)
	}
	obs = obTestObserve(t, h, core.EffectDelete, ruleResource("01J00000000000000000000999"))
	if string(obs.Current.State) != "null" || obs.Matches != 1 {
		t.Errorf("absent rule = %+v", obs)
	}
	if got, want := string(obs.Current.Version), obTestVersion(t, h, 0, 0); got != want {
		t.Errorf("absent rule version = %s, want %s", got, want)
	}
}

func TestObserveRuleCreation(t *testing.T) {
	h := newFakeHost(t)
	pending := nounRule + ":?" + fhTag
	obs := obTestObserve(t, h, core.EffectCreate, pending)
	if obs.Matches != 1 || obs.Resource != ruleResource(fhTag) || len(obs.Candidates) != 0 {
		t.Errorf("one match = %+v", obs)
	}
	absent := obTestObserve(t, h, core.EffectCreate, nounRule+":?01J00000000000000000000999")
	if absent.Matches != 0 || absent.Resource != "" || string(absent.Current.State) != "null" {
		t.Errorf("no match = %+v", absent)
	}
	s := h.state()
	s.Rules4 = append(s.Rules4, fhRule22v4)
	h.setState(s)
	dup := obTestObserve(t, h, core.EffectCreate, pending)
	if dup.Matches != 2 || dup.Exclusive || dup.Resource != "" || len(dup.Candidates) != 2 {
		t.Fatalf("two matches = %+v", dup)
	}
	if want := fmt.Sprintf(`{"v4":%q}`, fhRule22v4); string(dup.Candidates[1].State.State) != want {
		t.Errorf("second candidate = %s, want %s", dup.Candidates[1].State.State, want)
	}
	if got, want := string(dup.Candidates[1].State.Version), obTestVersion(t, h, 3, 0); got != want {
		t.Errorf("second candidate version = %s, want %s", got, want)
	}
	if string(dup.Current.State) != `{"matches":2}` {
		t.Errorf("current of two matches = %s", dup.Current.State)
	}
	if del := obTestObserve(t, h, core.EffectDelete, ruleResource(fhTag)); del.Matches != 1 || string(del.Current.State) != `{"matches":2}` {
		t.Errorf("a deletion of a doubled id = %+v", del)
	}
}

func TestObserveDefaultsLoggingAndState(t *testing.T) {
	h := newFakeHost(t)
	for resource, want := range map[string]string{
		defaultResource("incoming"): `"DROP"`, defaultResource("outgoing"): `"ACCEPT"`, defaultResource("routed"): `"DROP"`,
		resourceLogging: `"low"`, resourceEnabled: `true`,
	} {
		obs := obTestObserve(t, h, core.EffectModify, resource)
		if string(obs.Current.State) != want {
			t.Errorf("%s = %s, want %s", resource, obs.Current.State, want)
		}
		if got, want := string(obs.Current.Version), obTestVersion(t, h, 0, 0); got != want {
			t.Errorf("%s version = %s, want %s", resource, got, want)
		}
	}
	s := h.state()
	s.Enabled, s.LogLevel, s.Output = false, "off", "REJECT"
	h.setState(s)
	for resource, want := range map[string]string{
		defaultResource("outgoing"): `"REJECT"`, resourceLogging: `"off"`, resourceEnabled: `false`,
	} {
		if obs := obTestObserve(t, h, core.EffectModify, resource); string(obs.Current.State) != want {
			t.Errorf("after the edit %s = %s, want %s", resource, obs.Current.State, want)
		}
	}
}

func TestObserveFileAndSysctl(t *testing.T) {
	h := newFakeHost(t)
	cfg := h.config()
	obs := obTestObserve(t, h, core.EffectModify, fileResource("/etc/ufw/before.rules"))
	if want := string(mustJSON(fileHash(cfg.Files["/etc/ufw/before.rules"]))); string(obs.Current.State) != want || obs.Current.Version != nil {
		t.Errorf("file = %+v, want state %s", obs.Current, want)
	}
	if err := os.Remove(filepath.Join(h.root, "/etc/ufw/after6.rules")); err != nil {
		t.Fatal(err)
	}
	if obs := obTestObserve(t, h, core.EffectModify, fileResource("/etc/ufw/after6.rules")); string(obs.Current.State) != `"absent"` {
		t.Errorf("absent file = %s", obs.Current.State)
	}
	if obs := obTestObserve(t, h, core.EffectModify, sysctlResource("net/ipv4/conf/eth0/rp_filter")); string(obs.Current.State) != `"1"` {
		t.Errorf("sysctl = %s", obs.Current.State)
	}
	if obs := obTestObserve(t, h, core.EffectModify, sysctlResource("net/ipv4/conf/eth9/rp_filter")); string(obs.Current.State) != "null" {
		t.Errorf("sysctl of a vanished interface = %s", obs.Current.State)
	}
}

func TestObserveRefusesUnknownResources(t *testing.T) {
	h := newFakeHost(t)
	for _, resource := range []string{
		"firewall.rule", "firewall.rule:", "firewall.thing:x", "firewall.default:sideways",
		"firewall.logging:other", "firewall.state:other", "firewall.file:/etc/passwd",
	} {
		if _, err := observe(context.Background(), h.env(), h.deps(), core.Effect{Kind: core.EffectModify, Resource: resource}); err == nil {
			t.Errorf("observe(%q) succeeded", resource)
		}
	}
	if err := os.Remove(filepath.Join(h.root, "/etc/ufw/ufw.conf")); err != nil {
		t.Fatal(err)
	}
	if _, err := observe(context.Background(), h.env(), h.deps(), core.Effect{Kind: core.EffectModify, Resource: resourceLogging}); err == nil {
		t.Error("observe succeeded without a readable configuration")
	}
}

func TestStateShapesRoundTrip(t *testing.T) {
	var rs ruleState
	if err := json.Unmarshal([]byte(`{"v4":"a","v6":"b"}`), &rs); err != nil || rs != (ruleState{V4: "a", V6: "b"}) {
		t.Errorf("ruleState = %+v, %v", rs, err)
	}
	var v ufwVersion
	if err := json.Unmarshal(mustJSON(ufwVersion{Files: map[string]string{"/etc/ufw/user.rules": "absent"}, Pos4: 2}), &v); err != nil || v.Pos4 != 2 || v.Files["/etc/ufw/user.rules"] != "absent" {
		t.Errorf("ufwVersion = %+v, %v", v, err)
	}
}
