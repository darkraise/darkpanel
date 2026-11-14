package npm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/schema"
	"github.com/darkraise/darkpanel/internal/modules/firewall"
)

const qa12cHost = "h9"

var qa12cULID = regexp.MustCompile(`^[0-9A-HJKMNP-TV-Z]{26}$`)

// qa12cDef returns the declaration of action name at version from the npm module's manifest.
func qa12cDef(t *testing.T, name string, version int) core.ActionDef {
	t.Helper()
	for _, a := range Module().Manifest().Actions {
		if a.Spec.Name == name && a.Spec.Version == version {
			return a
		}
	}
	t.Fatalf("%s v%d is not declared by the npm module", name, version)
	return core.ActionDef{}
}

// qa12cIn is the input of deploy-public as the operator gives it; v1 takes no firewall and no ipv6.
func qa12cIn(integration, fw, ipv6 *bool) map[string]any {
	in := map[string]any{"hostId": qa12cHost, "acmeEmail": "ops@example.com"}
	for k, v := range map[string]*bool{"integration": integration, "firewall": fw, "ipv6": ipv6} {
		if v != nil {
			in[k] = *v
		}
	}
	return in
}

func qa12cB(v bool) *bool { return &v }

func qa12cPlanSteps(t *testing.T, version int, in map[string]any) []core.Step {
	t.Helper()
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	steps, err := qa12cDef(t, "npm.instance.deploy-public", version).Handler.Steps(context.Background(), core.Env{}, raw)
	if err != nil {
		t.Fatalf("steps of v%d for %s: %v", version, raw, err)
	}
	return steps
}

func qa12cStepIDs(steps []core.Step) []string {
	var ids []string
	for _, s := range steps {
		ids = append(ids, s.ID)
	}
	return ids
}

// qa12cObject decodes raw as a JSON object.
func qa12cObject(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("%s: %v", raw, err)
	}
	return m
}

// qa12cOutput runs the composite's output handler as the engine would after every step ran: each step answers {}
// except deploy, which answers a container.
func qa12cOutput(t *testing.T, version int, in map[string]any, steps []core.Step) DeployPublicOutput {
	t.Helper()
	outs := map[string]json.RawMessage{}
	for _, s := range steps {
		outs[s.ID] = json.RawMessage(`{}`)
	}
	outs["deploy"] = json.RawMessage(`{"containerId":"c0ffee","image":"jc21/nginx-proxy-manager:2.13.5","adminEmail":"ops@example.com"}`)
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	out, err := qa12cDef(t, "npm.instance.deploy-public", version).Handler.Output(context.Background(), core.Env{}, raw, outs)
	if err != nil {
		t.Fatal(err)
	}
	var got DeployPublicOutput
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if got.ContainerID != "c0ffee" {
		t.Errorf("output %+v, want the deployed container", got)
	}
	return got
}

// qa12cCheckInput resolves the check step's input against the composite's input.
func qa12cCheckInput(t *testing.T, steps []core.Step, in map[string]any) map[string]any {
	t.Helper()
	if len(steps) == 0 || steps[0].ID != "check" || steps[0].Action.String() != "npm.instance.check/v1" {
		t.Fatalf("steps %v, want the check step first", qa12cStepIDs(steps))
	}
	return qa12cObject(t, cxResolve(t, steps[0].Input, in, nil))
}

type qa12cRule struct{ port, family string }

// qa12cPortsRules checks the shape of a ports step that applies rules and returns its batch, the rule ids in order
// and the (port, family) of each rule.
func qa12cPortsRules(t *testing.T, steps []core.Step) (map[string]any, []string, []qa12cRule) {
	t.Helper()
	last := steps[len(steps)-1]
	if last.ID != "ports" || last.Action.String() != "firewall.rules.apply/v1" || last.Host != qa12cHost {
		t.Fatalf("last step %+v, want ports: firewall.rules.apply/v1 on host %s", last, qa12cHost)
	}
	batch := qa12cObject(t, last.Input)
	if batch["keepManual"] != true {
		t.Errorf("ports input %s, want keepManual true", last.Input)
	}
	adds, _ := batch["add"].([]any)
	var ids []string
	var rules []qa12cRule
	for _, a := range adds {
		r, _ := a.(map[string]any)
		id, _ := r["id"].(string)
		if !qa12cULID.MatchString(id) {
			t.Errorf("rule %v, want a ULID id", r)
		}
		fam, _ := r["family"].(string)
		wild := map[string]string{"v4": "0.0.0.0/0", "v6": "::/0"}[fam]
		for _, k := range []string{"from", "to"} {
			if v, _ := r[k].(string); v != "any" && v != wild {
				t.Errorf("rule %v: %s is %q, want any address", r, k, v)
			}
		}
		if r["action"] != "allow" || r["direction"] != "in" || r["proto"] != "tcp" {
			t.Errorf("rule %v, want an incoming allow of TCP", r)
		}
		if c, ok := r["comment"]; ok && c != "" {
			t.Errorf("rule %v carries a comment of its own", r)
		}
		if o, ok := r["origin"]; ok && o != "darkpanel" {
			t.Errorf("rule %v has origin %v, want a Darkpanel rule", r, o)
		}
		port, _ := r["toPort"].(string)
		ids = append(ids, id)
		rules = append(rules, qa12cRule{port, fam})
	}
	def := firewallRulesApplyDef(t)
	if err := schema.NewValidator().ValidateInput(def, last.Input); err != nil {
		t.Errorf("ports input %s is invalid for firewall.rules.apply: %v", last.Input, err)
	}
	return batch, ids, rules
}

func firewallRulesApplyDef(t *testing.T) core.ActionDef {
	t.Helper()
	for _, a := range firewall.Module().Manifest().Actions {
		if a.Spec.Name == "firewall.rules.apply" && a.Spec.Version == 1 {
			return a
		}
	}
	t.Fatal("firewall.rules.apply is not declared")
	return core.ActionDef{}
}

func TestA1_DeployPublicV2PortsStepWithIntegrationOff(t *testing.T) {
	in := qa12cIn(qa12cB(false), qa12cB(true), qa12cB(true))
	steps := qa12cPlanSteps(t, 2, in)
	if got, want := qa12cStepIDs(steps), []string{"check", "deploy", "ports"}; !slices.Equal(got, want) {
		t.Fatalf("steps %v, want %v", got, want)
	}
	_, ids, rules := qa12cPortsRules(t, steps)
	want := []qa12cRule{{"80", "v4"}, {"443", "v4"}, {"80", "v6"}, {"443", "v6"}}
	if len(rules) != 4 {
		t.Fatalf("ports step adds %d rules %v, want 4", len(rules), rules)
	}
	for _, w := range want {
		if !slices.Contains(rules, w) {
			t.Errorf("rules %v lack port %s for %s", rules, w.port, w.family)
		}
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			t.Errorf("rule id %s is used twice", id)
		}
		seen[id] = true
	}
	if out := qa12cOutput(t, 2, in, steps); !out.PortsOpened {
		t.Errorf("output %+v, want portsOpened true", out)
	}
}

func TestA2_DeployPublicV2IntegrationOnKeepsV1Steps(t *testing.T) {
	for _, tc := range []struct{ fw, ipv6 bool }{{true, true}, {true, false}, {false, false}} {
		t.Run(fmt.Sprintf("firewall %v ipv6 %v", tc.fw, tc.ipv6), func(t *testing.T) {
			in := qa12cIn(qa12cB(true), qa12cB(tc.fw), qa12cB(tc.ipv6))
			steps := qa12cPlanSteps(t, 2, in)
			if got, want := qa12cStepIDs(steps), []string{"check", "deploy", "ports"}; !slices.Equal(got, want) {
				t.Fatalf("steps %v, want %v", got, want)
			}
			for _, s := range steps {
				if s.Action.Name == "firewall.rules.apply" {
					t.Errorf("step %s is %s, want no firewall.rules.apply step with the integration on", s.ID, s.Action)
				}
			}
			ports := steps[2]
			if ports.Action.String() != "firewall.containers.apply/v1" || ports.Host != qa12cHost {
				t.Fatalf("last step %+v, want firewall.containers.apply/v1 on host %s", ports, qa12cHost)
			}
			body := qa12cObject(t, ports.Input)
			ops, _ := body["ops"].([]any)
			got := map[float64]bool{}
			for _, o := range ops {
				op, _ := o.(map[string]any)
				b, _ := op["binding"].(map[string]any)
				if op["op"] != "allow" || b["selector"] != "darkpanel-npm" || b["proto"] != "tcp" {
					t.Errorf("op %v, want an allow of a TCP port of darkpanel-npm", op)
				}
				p, _ := b["hostPort"].(float64)
				got[p] = true
			}
			if len(ops) != 2 || !got[80] || !got[443] {
				t.Errorf("ops %v, want allows of 80 and 443", ops)
			}
			check := qa12cCheckInput(t, steps, in)
			if check["integration"] != true || check["firewall"] != tc.fw || check["ipv6"] != tc.ipv6 || check["hostId"] != qa12cHost {
				t.Errorf("check input %v, want hostId, integration true, firewall %v, ipv6 %v", check, tc.fw, tc.ipv6)
			}
			if out := qa12cOutput(t, 2, in, steps); !out.PortsOpened {
				t.Errorf("output %+v, want portsOpened true", out)
			}
		})
	}
}

// A3 (the plan side): the plan of v2 names the same four fixed ids on every call, whatever the host's own state, so
// a host that already holds them plans no added rule; the firewall side is pinned in the firewall package's
// acceptance file.
func TestA3_DeployPublicV2RuleIdsAreFixed(t *testing.T) {
	var first []string
	for i, host := range []string{"h9", "h9", "another-host"} {
		in := qa12cIn(qa12cB(false), qa12cB(true), qa12cB(true))
		in["hostId"] = host
		in["acmeEmail"] = fmt.Sprintf("op%d@example.com", i)
		raw, _ := json.Marshal(in)
		ss, err := qa12cDef(t, "npm.instance.deploy-public", 2).Handler.Steps(context.Background(), core.Env{}, raw)
		if err != nil {
			t.Fatal(err)
		}
		last := ss[len(ss)-1]
		batch := qa12cObject(t, last.Input)
		adds, _ := batch["add"].([]any)
		var ids []string
		for _, a := range adds {
			ids = append(ids, a.(map[string]any)["id"].(string))
		}
		if len(ids) != 4 {
			t.Fatalf("ids %v, want four", ids)
		}
		if first == nil {
			first = ids
		} else if !slices.Equal(first, ids) {
			t.Errorf("ids %v, want %v on every call and host", ids, first)
		}
	}
}

func TestA4_DeployPublicV2PlanAndApplyBuildTheSameSteps(t *testing.T) {
	for _, tc := range []struct{ integration, fw, ipv6 bool }{{false, true, true}, {false, true, false}, {false, false, false}, {true, true, true}} {
		in := qa12cIn(qa12cB(tc.integration), qa12cB(tc.fw), qa12cB(tc.ipv6))
		raw, _ := json.Marshal(in)
		var first []byte
		for i := range 4 {
			// A fresh module per call: nothing a call mints may survive in the module.
			var def core.ActionDef
			for _, a := range Module().Manifest().Actions {
				if a.Spec.Name == "npm.instance.deploy-public" && a.Spec.Version == 2 {
					def = a
				}
			}
			steps, err := def.Handler.Steps(context.Background(), core.Env{}, raw)
			if err != nil {
				t.Fatal(err)
			}
			enc, err := json.Marshal(steps)
			if err != nil {
				t.Fatal(err)
			}
			if i == 0 {
				first = enc
			} else if string(enc) != string(first) {
				t.Errorf("call %d built\n%s\nwant what the first call built\n%s", i, enc, first)
			}
		}
	}
}

func TestA17_DeployPublicV2FirewallUnsupportedKeepsV1Steps(t *testing.T) {
	for _, ipv6 := range []bool{false, true} {
		in := qa12cIn(qa12cB(false), qa12cB(false), qa12cB(ipv6))
		steps := qa12cPlanSteps(t, 2, in)
		if got, want := qa12cStepIDs(steps), []string{"check", "deploy"}; !slices.Equal(got, want) {
			t.Errorf("ipv6 %v: steps %v, want v1's list %v", ipv6, got, want)
		}
		for _, s := range steps {
			if strings.HasPrefix(s.Action.Name, "firewall.") {
				t.Errorf("ipv6 %v: step %s is %s, want no firewall step", ipv6, s.ID, s.Action)
			}
		}
		if out := qa12cOutput(t, 2, in, steps); out.PortsOpened {
			t.Errorf("ipv6 %v: output %+v, want portsOpened false", ipv6, out)
		}
	}
}

func TestA18_DeployPublicV2WithoutIPv6AddsTheIPv4RulesOnly(t *testing.T) {
	in := qa12cIn(qa12cB(false), qa12cB(true), qa12cB(false))
	steps := qa12cPlanSteps(t, 2, in)
	_, ids, rules := qa12cPortsRules(t, steps)
	if len(ids) != 2 || len(rules) != 2 || !slices.Contains(rules, qa12cRule{"80", "v4"}) || !slices.Contains(rules, qa12cRule{"443", "v4"}) {
		t.Errorf("rules %v, want ports 80 and 443 for IPv4 only", rules)
	}
}

// qa12cAgent answers the controller's resource reads of one host.
type qa12cAgent struct {
	supported, ipv6, integration bool
	statusErr                    error
}

func (a qa12cAgent) fake() *coretest.FakeAgent {
	return &coretest.FakeAgent{
		ResourceFunc: func(_ context.Context, _ core.Principal, name string) (json.RawMessage, error) {
			switch name {
			case "firewall.docker":
				return json.Marshal(map[string]any{
					"available": true, "version": "29.9.1", "driver": "iptables", "rawProtection": true,
					"supported": true, "enabled": a.integration, "health": "ok",
				})
			case "firewall.status":
				if a.statusErr != nil {
					return nil, a.statusErr
				}
				return json.Marshal(map[string]any{"supported": a.supported, "configured": map[string]any{"ipv6": a.ipv6}})
			}
			return nil, fmt.Errorf("qa12cAgent: no resource %s", name)
		},
	}
}

func TestA19_CheckStepRefusesAHostThatChangedItsFirewall(t *testing.T) {
	check := qa12cDef(t, "npm.instance.check", 1)
	for _, tc := range []struct {
		name         string
		planned      [2]bool // firewall, ipv6 as planned
		host         [2]bool // as the host is at apply
		wantRefusal  bool
		wantInDetail string
	}{
		{"nothing changed with both on", [2]bool{true, true}, [2]bool{true, true}, false, ""},
		{"nothing changed with both off", [2]bool{false, false}, [2]bool{false, false}, false, ""},
		{"firewall support was lost", [2]bool{true, true}, [2]bool{false, true}, true, "firewall"},
		{"firewall support appeared", [2]bool{false, false}, [2]bool{true, false}, true, "firewall"},
		{"ipv6 was turned off", [2]bool{true, true}, [2]bool{true, false}, true, "ipv6"},
		{"ipv6 was turned on", [2]bool{true, false}, [2]bool{true, true}, true, "ipv6"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := qa12cIn(qa12cB(false), qa12cB(tc.planned[0]), qa12cB(tc.planned[1]))
			steps := qa12cPlanSteps(t, 2, in)
			ci := qa12cCheckInput(t, steps, in)
			raw, _ := json.Marshal(ci)

			h := newTHHost(t)
			h.agents = map[string]*coretest.FakeAgent{qa12cHost: qa12cAgent{supported: tc.host[0], ipv6: tc.host[1]}.fake()}
			_, out, err := check.Handler.Reveal(context.Background(), h.ctlEnv(""), raw)
			if !tc.wantRefusal {
				if err != nil {
					t.Fatalf("check refused an unchanged host: %v", err)
				}
				var res map[string]any
				if err := json.Unmarshal(out, &res); err != nil || res["integration"] != false {
					t.Errorf("check answered %s, %v", out, err)
				}
				return
			}
			var coded *errcode.Error
			if !errors.As(err, &coded) || coded.Code != errcode.PlanStateChanged {
				t.Fatalf("check error %v, want plan.state_changed", err)
			}
			if !strings.Contains(strings.ToLower(coded.Detail), tc.wantInDetail) {
				t.Errorf("detail %q, want it to name %s", coded.Detail, tc.wantInDetail)
			}
		})
	}
}

func TestA20_DeployPublicV1IsUnchanged(t *testing.T) {
	for _, integration := range []bool{false, true} {
		t.Run(fmt.Sprintf("integration %v", integration), func(t *testing.T) {
			in := qa12cIn(qa12cB(integration), nil, nil)
			steps := qa12cPlanSteps(t, 1, in)
			want := []string{"check", "deploy"}
			if integration {
				want = append(want, "ports")
			}
			if got := qa12cStepIDs(steps); !slices.Equal(got, want) {
				t.Fatalf("steps %v, want %v", got, want)
			}
			check := qa12cCheckInput(t, steps, in)
			if !reflect.DeepEqual(check, map[string]any{"hostId": qa12cHost, "integration": integration}) {
				t.Errorf("check input %v, want hostId and integration only", check)
			}
			deploy := steps[1]
			if deploy.Action.String() != "npm.instance.deploy/v1" || deploy.Host != qa12cHost || !slices.Equal(deploy.After, []string{"check"}) {
				t.Errorf("deploy step %+v", deploy)
			}
			if integration {
				ports := steps[2]
				if ports.Action.String() != "firewall.containers.apply/v1" || ports.Host != qa12cHost || !slices.Equal(ports.After, []string{"deploy"}) {
					t.Errorf("ports step %+v, want firewall.containers.apply/v1 on host %s after deploy", ports, qa12cHost)
				}
				body := qa12cObject(t, ports.Input)
				if ops, _ := body["ops"].([]any); len(ops) != 2 {
					t.Errorf("ports input %s, want two allows", ports.Input)
				}
			}
			if out := qa12cOutput(t, 1, in, steps); out.PortsOpened != integration {
				t.Errorf("output %+v, want portsOpened %v", out, integration)
			}
			if def := qa12cDef(t, "npm.instance.deploy-public", 1); def.Spec.Version != 1 {
				t.Errorf("v1 spec %+v", def.Spec)
			}
		})
	}
}
