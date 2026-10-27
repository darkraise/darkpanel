package firewall

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/firewall/model"
)

// The ids a caller of rules.apply supplies.
const (
	apID1 = "01J00000000000000000000011"
	apID2 = "01J00000000000000000000012"
)

// apHost is a fake host whose ufw adds the batch's rules (ports 8080 and
// 8443, label "npm") and deletes the both-family rule fhTag.
func apHost(t *testing.T) (*fakeHost, fw, core.Env) {
	t.Helper()
	h, f, env := ruHost(t)
	for _, c := range []struct{ id, port string }{{apID1, "8080"}, {apID2, "8443"}} {
		r := model.Rule{ID: c.id, Action: model.ActionAllow, Direction: model.DirIn, Proto: model.ProtoTCP, From: model.Any,
			To: model.Any, ToPort: c.port, Family: model.FamilyV4, Comment: "npm"}
		h.ufw[strings.Join(model.AddArgs(r), " ")] = func(s *fhState) int {
			s.Rules4 = append(s.Rules4, ruTuple(c.port, model.Tag(c.id, "npm")))
			return 0
		}
	}
	return h, f, env
}

const apInput = `{"label":"npm","add":[` +
	`{"id":"` + apID1 + `","action":"allow","direction":"in","proto":"tcp","from":"any","to":"any","toPort":"8080","family":"v4"},` +
	`{"id":"` + apID2 + `","action":"allow","direction":"in","proto":"tcp","from":"any","to":"any","toPort":"8443","family":"v4"}],` +
	`"delete":["` + fhTag + `"]}`

// TestRulesApply: the batch adds its rules with the caller's ids and the
// label, then deletes; run again with the same input it plans an empty
// diff and runs nothing.
func TestRulesApply(t *testing.T) {
	h, f, env := apHost(t)
	def := f.rulesApply()
	if !def.Spec.Converges {
		t.Error("rules.apply does not declare Converges")
	}
	n := len(h.calls)
	plan, out := ruApply(t, env, def, apInput)
	want := []string{
		"- rule " + fhTag + ": allow in proto tcp from any to any port 22",
		"+ rule " + apID1 + ": allow in proto tcp from 0.0.0.0/0 to 0.0.0.0/0 port 8080 comment \"npm\"",
		"+ rule " + apID2 + ": allow in proto tcp from 0.0.0.0/0 to 0.0.0.0/0 port 8443 comment \"npm\"",
	}
	if !reflect.DeepEqual(acDiff(plan), want) {
		t.Errorf("diff\n%q\nwant\n%q", acDiff(plan), want)
	}
	var ran []string
	for _, c := range h.calls[n:] {
		if v, ok := strings.CutPrefix(c, "run /usr/sbin/ufw "); ok {
			ran = append(ran, v)
		}
	}
	wantRan := []string{"allow in proto tcp from 0.0.0.0/0 to 0.0.0.0/0 port 8080 comment darkpanel:" + apID1 + " npm",
		"allow in proto tcp from 0.0.0.0/0 to 0.0.0.0/0 port 8443 comment darkpanel:" + apID2 + " npm",
		"delete allow in proto tcp from any to any port 22", "reload"}
	if !reflect.DeepEqual(ran, wantRan) {
		t.Errorf("ran\n%q\nwant\n%q", ran, wantRan)
	}
	if !reflect.DeepEqual(out.Rules, []string{apID1, apID2}) {
		t.Errorf("output rules %v, want the batch's ids", out.Rules)
	}

	n = len(h.calls)
	plan, _ = ruApply(t, env, def, apInput)
	if len(plan.Diff) != 0 {
		t.Errorf("second plan diff %q, want none", acDiff(plan))
	}
	for _, c := range h.calls[n:] {
		if strings.HasPrefix(c, "run /usr/sbin/ufw") {
			t.Errorf("the second apply ran %s", c)
		}
	}

	conflict := strings.Replace(apInput, `"toPort":"8443"`, `"toPort":"9443"`, 1)
	_, err := def.Handler.Plan(context.Background(), env, json.RawMessage(conflict))
	if e := errcode.From(err); e.Code != errcode.InvalidInput || len(e.Fields) != 1 || e.Fields[0].Path != "/add/1" {
		t.Errorf("plan with another rule under an existing id = %v, want an invalid input at /add/1", err)
	}
}

// TestRulesApplyRefuses: the batch's own mistakes are invalid input with
// the field that holds them; deleting a manual rule needs manual: true.
func TestRulesApplyRefuses(t *testing.T) {
	ctx := context.Background()
	h, f, env := apHost(t)
	rule := func(extra string) string {
		return `{"action":"allow","direction":"in","proto":"tcp","from":"any","to":"any","toPort":"8080","family":"v4"` + extra + `}`
	}
	for _, c := range []struct{ raw, path string }{
		{`{"add":[` + rule(``) + `]}`, "/add/0/id"},
		{`{"add":[` + rule(`,"id":"manual-0123456789abcdef"`) + `]}`, "/add/0/id"},
		{`{"add":[` + rule(`,"id":"`+apID1+`","comment":"x"`) + `]}`, "/add/0/comment"},
		{`{"add":[` + rule(`,"id":"`+apID1+`","origin":"manual"`) + `]}`, "/add/0/origin"},
		{`{"add":[` + rule(`,"id":"`+apID1+`"`) + `,` + rule(`,"id":"`+apID1+`"`) + `]}`, "/add/1/id"},
		{`{"add":[` + rule(`,"id":"`+apID1+`"`) + `],"delete":["` + apID1 + `"]}`, "/delete/0"},
		{`{"delete":["nope"]}`, "/delete/0"},
		{`{"add":[` + rule(`,"id":"`+apID1+`","toPort":"0"`) + `]}`, "/add/0/toPort"},
	} {
		_, err := f.rulesApply().Handler.Plan(ctx, env, json.RawMessage(c.raw))
		if e := errcode.From(err); e.Code != errcode.InvalidInput || len(e.Fields) == 0 || e.Fields[0].Path != c.path {
			t.Errorf("plan %s = %v, want an invalid input at %s", c.raw, err, c.path)
		}
	}
	manual := model.Merge(h.config().Rules4, nil)[1].ID
	_, err := f.rulesApply().Handler.Plan(ctx, env, json.RawMessage(`{"delete":["`+manual+`"]}`))
	if errcode.From(err).Code != errcode.FirewallManualRequired {
		t.Errorf("deleting a manual rule = %v, want manual required", err)
	}
	if _, err := f.rulesApply().Handler.Plan(ctx, env, json.RawMessage(`{"delete":["`+manual+`"],"manual":true}`)); err != nil {
		t.Errorf("deleting a manual rule with manual: %v", err)
	}
	err = f.rulesApply().Handler.Verify(ctx, env, json.RawMessage(apInput), json.RawMessage(`{}`))
	if errcode.From(err).Code != errcode.FirewallPredictionMismatch {
		t.Errorf("verify before the batch = %v, want a prediction mismatch", err)
	}
}
