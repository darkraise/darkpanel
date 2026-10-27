package firewall

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/schema"
	"github.com/darkraise/darkpanel/internal/modules/firewall/model"
)

// ruChange is the change id of the rule tests; rule.add gives the new rule
// this id.
const ruChange = "01J00000000000000000000077"

// ruTuple is the IPv4 tuple UFW writes for an incoming TCP rule to port
// with the given comment.
func ruTuple(port, comment string) string {
	return "### tuple ### allow tcp " + port + " 0.0.0.0/0 any 0.0.0.0/0 in comment=" + hex.EncodeToString([]byte(comment))
}

// ruHost is a fake host whose ufw adds the rule for port 8080 (as change
// ruChange or in a plan) and deletes its rules.
func ruHost(t *testing.T) (*fakeHost, fw, core.Env) {
	t.Helper()
	h := newFakeHost(t)
	h.ufw["reload"] = func(*fhState) int { return 0 }
	for _, id := range []string{ruChange, planRuleID} {
		r := model.Rule{ID: id, Action: model.ActionAllow, Direction: model.DirIn, Proto: model.ProtoTCP, From: model.Any,
			To: model.Any, ToPort: "8080", Family: model.FamilyV4, Comment: "web"}
		h.ufw[strings.Join(model.AddArgs(r), " ")] = func(s *fhState) int {
			s.Rules4 = append(s.Rules4, ruTuple("8080", model.Tag(id, "web")))
			return 0
		}
	}
	h.ufw["delete allow in proto tcp from any to any port 22"] = func(s *fhState) int {
		s.Rules4, s.Rules6 = ruWithout(s.Rules4, fhRule22v4), ruWithout(s.Rules6, fhRule22v6)
		return 0
	}
	h.ufw["delete allow in proto tcp from 10.0.0.0/8 to 0.0.0.0/0 port 3000"] = func(s *fhState) int {
		s.Rules4 = ruWithout(s.Rules4, fhRule3000v4)
		return 0
	}
	f, _ := newFW(h)
	env := h.env()
	env.ChangeID = ruChange
	return h, f, env
}

func ruWithout(lines []string, line string) []string {
	var out []string
	for _, l := range lines {
		if l != line {
			out = append(out, l)
		}
	}
	return out
}

// ruApply plans (without a change id, as the engine does), snapshots,
// applies and verifies def under env.
func ruApply(t *testing.T, env core.Env, def core.ActionDef, raw string) (core.PlanOutcome, ChangeOutput) {
	t.Helper()
	ctx := context.Background()
	if err := schema.NewValidator().ValidateInput(def, json.RawMessage(raw)); err != nil {
		t.Fatalf("input %s: %v", raw, err)
	}
	planEnv := env
	planEnv.ChangeID = ""
	plan, err := def.Handler.Plan(ctx, planEnv, json.RawMessage(raw))
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if _, err := def.Handler.Snapshot(ctx, env, json.RawMessage(raw)); err != nil {
		t.Fatal(err)
	}
	res, err := def.Handler.Apply(ctx, env, json.RawMessage(raw))
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if err := def.Handler.Verify(ctx, env, json.RawMessage(raw), res); err != nil {
		t.Fatalf("verify: %v", err)
	}
	var out ChangeOutput
	if err := json.Unmarshal(res, &out); err != nil {
		t.Fatal(err)
	}
	return plan, out
}

// TestRuleAdd: the new rule gets its change's id as its tag; the plan shows
// it as new.
func TestRuleAdd(t *testing.T) {
	h, f, env := ruHost(t)
	raw := `{"rule":{"action":"allow","direction":"in","proto":"tcp","from":"any","to":"any","toPort":"8080","family":"v4","comment":"web"}}`
	plan, out := ruApply(t, env, f.ruleAdd(), raw)
	if want := []string{"+ rule new: allow in proto tcp from 0.0.0.0/0 to 0.0.0.0/0 port 8080 comment \"web\""}; !reflect.DeepEqual(acDiff(plan), want) {
		t.Errorf("diff %q, want %q", acDiff(plan), want)
	}
	if !reflect.DeepEqual(out.Rules, []string{ruChange}) || out.Snapshot == "" {
		t.Errorf("output %+v, want the new rule's id", out)
	}
	if rules := findRules(h.config(), ruChange); len(rules) != 1 || rules[0].Rule.Comment != "web" || rules[0].Rule.Origin != model.OriginDarkpanel {
		t.Errorf("rules %+v, want the tagged rule", rules)
	}
	s := h.state()
	s.Rules4 = s.Rules4[:2]
	h.setState(s)
	if err := f.ruleAdd().Handler.Verify(context.Background(), env, json.RawMessage(raw), json.RawMessage(`{}`)); errcode.From(err).Code != errcode.FirewallPredictionMismatch {
		t.Errorf("verify without the rule = %v, want a prediction mismatch", err)
	}
}

// TestRuleAddRefuses: a rule with an id, a manual rule, one outside UFW's
// grammar and one UFW already holds are refused.
func TestRuleAddRefuses(t *testing.T) {
	ctx := context.Background()
	h, f, env := ruHost(t)
	env.ChangeID = ""
	h.ufw["allow in proto tcp from any to any port 22 comment darkpanel:"+planRuleID] = func(*fhState) int { return 0 }
	for _, c := range []struct {
		raw, path string
	}{
		{`{"rule":{"id":"01J00000000000000000000005","action":"allow","direction":"in","proto":"tcp","from":"any","to":"any","toPort":"80","family":"v4"}}`, "/rule/id"},
		{`{"rule":{"action":"allow","direction":"in","proto":"tcp","from":"any","to":"any","toPort":"80","family":"v4","origin":"manual"}}`, "/rule/origin"},
		{`{"rule":{"action":"limit","direction":"out","proto":"tcp","from":"any","to":"any","toPort":"80","family":"v4"}}`, "/rule/action"},
		{`{"rule":{"action":"allow","direction":"in","proto":"tcp","from":"any","to":"any","toPort":"22","family":"both"}}`, "/rule"},
	} {
		_, err := f.ruleAdd().Handler.Plan(ctx, env, json.RawMessage(c.raw))
		e := errcode.From(err)
		if e.Code != errcode.InvalidInput || len(e.Fields) == 0 || e.Fields[0].Path != c.path {
			t.Errorf("plan %s = %v, want an invalid input at %s", c.raw, err, c.path)
		}
		if c.path == "/rule" && !strings.Contains(e.Detail, "already holds") {
			t.Errorf("plan %s = %v, want the duplicate refused", c.raw, err)
		}
	}
}

// TestRuleDelete: a Darkpanel rule is deleted by id; a manual rule needs
// manual: true; an unknown id and a rule outside the model are refused.
func TestRuleDelete(t *testing.T) {
	ctx := context.Background()
	h, f, env := ruHost(t)
	plan, out := ruApply(t, env, f.ruleDelete(), `{"id":"`+fhTag+`"}`)
	if len(plan.Diff) != 1 || plan.Diff[0].Op != "-" || !strings.Contains(plan.Diff[0].Text, fhTag) || len(out.Rules) != 0 {
		t.Errorf("diff %q output %+v, want the rule's removal", acDiff(plan), out)
	}
	if len(findRules(h.config(), fhTag)) != 0 {
		t.Error("the rule is still there")
	}

	manual := model.Merge(h.config().Rules4, nil)[0].ID
	_, err := f.ruleDelete().Handler.Plan(ctx, env, json.RawMessage(`{"id":"`+manual+`"}`))
	if errcode.From(err).Code != errcode.FirewallManualRequired {
		t.Errorf("deleting a manual rule = %v, want manual required", err)
	}
	if plan, _ := ruApply(t, env, f.ruleDelete(), `{"id":"`+manual+`","manual":true}`); !strings.Contains(acDiff(plan)[0], "(manual)") {
		t.Errorf("diff %q, want the manual rule marked", acDiff(plan))
	}

	h, f, env = ruHost(t)
	s := h.state()
	s.Rules6 = append(s.Rules6, suRuleRaw)
	h.setState(s)
	raw := model.Merge(nil, []model.Tuple{h.config().Rules6[1]})[0].ID
	for _, id := range []string{"01J00000000000000000000099", raw} {
		_, err := f.ruleDelete().Handler.Plan(ctx, env, json.RawMessage(`{"id":"`+id+`","manual":true}`))
		if e := errcode.From(err); e.Code != errcode.InvalidInput || len(e.Fields) != 1 || e.Fields[0].Path != "/id" {
			t.Errorf("deleting %s = %v, want an invalid input at /id", id, err)
		}
	}
	if err := f.ruleDelete().Handler.Verify(ctx, env, json.RawMessage(`{"id":"`+fhTag+`"}`), json.RawMessage(`{}`)); errcode.From(err).Code != errcode.FirewallPredictionMismatch {
		t.Errorf("verify with the rule present = %v, want a prediction mismatch", err)
	}
}
