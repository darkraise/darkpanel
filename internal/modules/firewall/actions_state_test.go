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
		if errcode.From(err).Code != errcode.FirewallPredictionMismatch {
			t.Errorf("%s verify = %v, want a prediction mismatch", c.def.Spec.Name, err)
		}
	}
	s := h.state()
	s.Enabled = true
	h.setState(s)
	if err := f.stateDisable().Handler.Verify(ctx, h.env(), json.RawMessage(`{}`), json.RawMessage(`{}`)); errcode.From(err).Code != errcode.FirewallPredictionMismatch {
		t.Errorf("disable verify = %v, want a prediction mismatch", err)
	}
}
