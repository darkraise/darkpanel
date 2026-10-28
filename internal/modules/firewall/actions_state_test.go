package firewall

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/schema"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
)

// acHost is a fake host whose ufw sets the enabled state, the defaults and
// the logging level.
func acHost(t *testing.T, enabled bool) (*fakeHost, fw) {
	t.Helper()
	h := newFakeHost(t)
	s := h.state()
	s.Enabled = enabled
	h.setState(s)
	if !enabled {
		h.loaded = fwstate.Loaded{Variant: "nft", V4: fhParse(t, tgStopped), V6: fhParse(t, tgStopped)}
	}
	h.ufw["--force enable"] = func(s *fhState) int { s.Enabled = true; return 0 }
	h.ufw["disable"] = func(s *fhState) int { s.Enabled = false; return 0 }
	h.ufw["reload"] = func(*fhState) int { return 0 }
	for policy, verb := range policyVerbs {
		h.ufw["default "+verb+" incoming"] = func(s *fhState) int { s.Input = policy; return 0 }
		h.ufw["default "+verb+" outgoing"] = func(s *fhState) int { s.Output = policy; return 0 }
		h.ufw["default "+verb+" routed"] = func(s *fhState) int { s.Forward = policy; return 0 }
	}
	for _, level := range []string{"off", "low", "medium", "high", "full"} {
		h.ufw["logging "+level] = func(s *fhState) int { s.LogLevel = level; return 0 }
	}
	f, _ := newFW(h)
	return h, f
}

// acRun plans, snapshots, applies and verifies def with input raw, and
// returns the plan and the ufw vectors the apply ran.
func acRun(t *testing.T, h *fakeHost, def core.ActionDef, raw string) (core.PlanOutcome, []string) {
	t.Helper()
	ctx := context.Background()
	if err := schema.NewValidator().ValidateInput(def, json.RawMessage(raw)); err != nil {
		t.Fatalf("%s input %s: %v", def.Spec.Name, raw, err)
	}
	out, err := def.Handler.Plan(ctx, h.env(), json.RawMessage(raw))
	if err != nil {
		t.Fatalf("%s plan: %v", def.Spec.Name, err)
	}
	if _, err := def.Handler.Snapshot(ctx, h.env(), json.RawMessage(raw)); err != nil {
		t.Fatal(err)
	}
	n := len(h.calls)
	res, err := def.Handler.Apply(ctx, h.env(), json.RawMessage(raw))
	if err != nil {
		t.Fatalf("%s apply: %v", def.Spec.Name, err)
	}
	if err := def.Handler.Verify(ctx, h.env(), json.RawMessage(raw), res); err != nil {
		t.Fatalf("%s verify: %v", def.Spec.Name, err)
	}
	var ran []string
	for _, c := range h.calls[n:] {
		if v, ok := strings.CutPrefix(c, "run /usr/sbin/ufw "); ok {
			ran = append(ran, v)
		}
	}
	return out, ran
}

func acDiff(out core.PlanOutcome) []string {
	var d []string
	for _, l := range out.Diff {
		d = append(d, l.Op+" "+l.Text)
	}
	return d
}

// TestStateActions: enable starts an inactive UFW and disable stops an
// active one; each is a no-op when UFW is already so.
func TestStateActions(t *testing.T) {
	h, f := acHost(t, false)
	out, ran := acRun(t, h, f.stateEnable(), `{}`)
	if !reflect.DeepEqual(acDiff(out), []string{"- UFW disabled", "+ UFW enabled"}) || !reflect.DeepEqual(ran, []string{"--force enable"}) {
		t.Errorf("enable: diff %q ran %q", acDiff(out), ran)
	}
	out, ran = acRun(t, h, f.stateEnable(), `{}`)
	if len(out.Diff) != 0 || len(ran) != 0 {
		t.Errorf("enable again: diff %q ran %q, want nothing", acDiff(out), ran)
	}

	h, f = acHost(t, true)
	out, ran = acRun(t, h, f.stateDisable(), `{}`)
	if !reflect.DeepEqual(acDiff(out), []string{"- UFW enabled", "+ UFW disabled"}) || !reflect.DeepEqual(ran, []string{"disable"}) {
		t.Errorf("disable: diff %q ran %q", acDiff(out), ran)
	}
	if h.state().Enabled {
		t.Error("UFW is still enabled")
	}
}

// TestStateEnableIPv6No: starting UFW with IPV6=no empties user6.rules, so
// enabling a host that holds an IPv6 manual rule needs manual: true.
func TestStateEnableIPv6No(t *testing.T) {
	ctx := context.Background()
	h, f := acHost(t, false)
	s := h.state()
	s.IPv6 = false
	s.Rules6 = append(s.Rules6, fhRule53v6)
	h.setState(s)
	h.ufw["--force enable"] = func(s *fhState) int {
		s.Enabled = true
		if !s.IPv6 {
			s.Rules6 = nil
		}
		return 0
	}
	_, err := f.stateEnable().Handler.Plan(ctx, h.env(), json.RawMessage(`{}`))
	if e := errcode.From(err); e.Code != errcode.FirewallManualRequired || !strings.Contains(e.Detail, "set manual: true") {
		t.Fatalf("enable with IPV6=no and an IPv6 manual rule = %v, want manual required", err)
	}
	out, _ := acRun(t, h, f.stateEnable(), `{"manual":true}`)
	if d := strings.Join(acDiff(out), "\n"); !strings.Contains(d, "(manual)") {
		t.Errorf("diff %q, want the discarded manual rule marked", d)
	}
}

// TestMutationsIPv6NoManual: on an active UFW every change reloads it, and
// a reload with IPV6=no empties user6.rules, so logging.set, defaults.set
// and rule.add on a host that holds an IPv6 manual rule need manual: true.
func TestMutationsIPv6NoManual(t *testing.T) {
	ctx := context.Background()
	h, f, env := ruHost(t)
	env.ChangeID = ""
	s := h.state()
	s.IPv6 = false
	s.Rules6 = append(s.Rules6, fhRule53v6)
	h.setState(s)
	h.ufw["reload"] = func(s *fhState) int {
		if !s.IPv6 {
			s.Rules6 = nil
		}
		return 0
	}
	h.ufw["logging high"] = func(s *fhState) int { s.LogLevel = "high"; return 0 }
	h.ufw["default allow incoming"] = func(s *fhState) int { s.Input = "ACCEPT"; return 0 }
	rule := `"rule":{"action":"allow","direction":"in","proto":"tcp","from":"any","to":"any","toPort":"8080","family":"v4","comment":"web"}`
	for _, c := range []struct {
		def       core.ActionDef
		raw, with string
	}{
		{f.loggingSet(), `{"level":"high"}`, `{"level":"high","manual":true}`},
		{f.defaultsSet(), `{"incoming":"ACCEPT"}`, `{"incoming":"ACCEPT","manual":true}`},
		{f.ruleAdd(), `{` + rule + `}`, `{` + rule + `,"manual":true}`},
	} {
		t.Run(c.def.Spec.Name, func(t *testing.T) {
			_, err := c.def.Handler.Plan(ctx, env, json.RawMessage(c.raw))
			if e := errcode.From(err); e.Code != errcode.FirewallManualRequired || !strings.Contains(e.Detail, "set manual: true") {
				t.Fatalf("plan without manual = %v, want manual required", err)
			}
			if err := schema.NewValidator().ValidateInput(c.def, json.RawMessage(c.with)); err != nil {
				t.Fatalf("input %s: %v", c.with, err)
			}
			out, err := c.def.Handler.Plan(ctx, env, json.RawMessage(c.with))
			if err != nil {
				t.Fatalf("plan with manual = %v", err)
			}
			if d := strings.Join(acDiff(out), "\n"); !strings.Contains(d, "(manual)") {
				t.Errorf("diff %q, want the discarded manual rule marked", d)
			}
		})
	}
}

// TestDefaultsAndLogging: the defaults and the logging level are set for
// what changes, then an active UFW reloads; nothing to change is a no-op;
// a request without any direction is refused.
func TestDefaultsAndLogging(t *testing.T) {
	h, f := acHost(t, true)
	out, ran := acRun(t, h, f.defaultsSet(), `{"incoming":"ACCEPT","outgoing":"ACCEPT","routed":"REJECT"}`)
	wantDiff := []string{"- default incoming DROP", "+ default incoming ACCEPT", "- default routed DROP", "+ default routed REJECT"}
	if !reflect.DeepEqual(acDiff(out), wantDiff) || !reflect.DeepEqual(ran, []string{"default allow incoming", "default reject routed", "reload"}) {
		t.Errorf("defaults: diff %q ran %q", acDiff(out), ran)
	}
	if out, ran = acRun(t, h, f.defaultsSet(), `{"routed":"REJECT"}`); len(out.Diff) != 0 || len(ran) != 0 {
		t.Errorf("defaults again: diff %q ran %q, want nothing", acDiff(out), ran)
	}
	_, err := f.defaultsSet().Handler.Plan(context.Background(), h.env(), json.RawMessage(`{}`))
	if e := errcode.From(err); e.Code != errcode.InvalidInput || len(e.Fields) != 1 || e.Fields[0].Path != "/incoming" {
		t.Errorf("defaults without a direction = %v, want an invalid input", err)
	}
	if err := schema.NewValidator().ValidateInput(f.defaultsSet(), json.RawMessage(`{"incoming":"allow"}`)); err == nil {
		t.Error("the schema accepts the policy allow")
	}

	out, ran = acRun(t, h, f.loggingSet(), `{"level":"high"}`)
	if !reflect.DeepEqual(acDiff(out), []string{"- logging low", "+ logging high"}) || !reflect.DeepEqual(ran, []string{"logging high", "reload"}) {
		t.Errorf("logging: diff %q ran %q", acDiff(out), ran)
	}
	h, f = acHost(t, false)
	if _, ran = acRun(t, h, f.loggingSet(), `{"level":"off"}`); !reflect.DeepEqual(ran, []string{"logging off"}) {
		t.Errorf("logging on an inactive UFW ran %q, want no reload", ran)
	}
	if err := schema.NewValidator().ValidateInput(f.loggingSet(), json.RawMessage(`{"level":"on"}`)); err == nil {
		t.Error("the schema accepts the level on")
	}
}

// TestStateActionsVerify: verify fails when the configuration does not
// show what the action set.
func TestStateActionsVerify(t *testing.T) {
	ctx := context.Background()
	h, f := acHost(t, true)
	for _, c := range []struct {
		def core.ActionDef
		raw string
	}{
		{f.stateEnable(), `{}`}, {f.defaultsSet(), `{"outgoing":"DROP"}`}, {f.loggingSet(), `{"level":"full"}`},
	} {
		s := h.state()
		s.Enabled = false
		h.setState(s)
		err := c.def.Handler.Verify(ctx, h.env(), json.RawMessage(c.raw), json.RawMessage(`{"snapshot":""}`))
		if e := errcode.From(err); e.Code != errcode.FirewallPredictionMismatch || !strings.Contains(e.Detail, "after the change") {
			t.Errorf("%s verify = %v, want the action's own prediction mismatch", c.def.Spec.Name, err)
		}
	}
	s := h.state()
	s.Enabled = true
	h.setState(s)
	err := f.stateDisable().Handler.Verify(ctx, h.env(), json.RawMessage(`{}`), json.RawMessage(`{}`))
	if e := errcode.From(err); e.Code != errcode.FirewallPredictionMismatch || !strings.Contains(e.Detail, "after the change") {
		t.Errorf("disable verify = %v, want the action's own prediction mismatch", err)
	}
}
