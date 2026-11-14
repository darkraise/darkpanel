package firewall

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/schema"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
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
		"delete allow in proto tcp from any to any port 22"}
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

// TestRulesApplyBatchSize: a batch adds and deletes at most maxBatch rules
// each; the schema and the plan both refuse a longer list.
func TestRulesApplyBatchSize(t *testing.T) {
	_, f, env := apHost(t)
	def := f.rulesApply()
	ids := make([]string, maxBatch+1)
	for i := range ids {
		ids[i] = fmt.Sprintf("01J000000000000000000%05d", i)
	}
	rules := make([]string, len(ids))
	for i, id := range ids {
		rules[i] = `{"id":"` + id + `","action":"allow","direction":"in","proto":"tcp","from":"any","to":"any","toPort":"8080","family":"v4"}`
	}
	for _, c := range []struct{ raw, path string }{
		{`{"add":[` + strings.Join(rules, ",") + `]}`, "/add"},
		{`{"delete":["` + strings.Join(ids, `","`) + `"]}`, "/delete"},
	} {
		if err := schema.NewValidator().ValidateInput(def, json.RawMessage(c.raw)); err == nil {
			t.Errorf("the schema accepts %d items at %s", len(ids), c.path)
		}
		_, err := def.Handler.Plan(context.Background(), env, json.RawMessage(c.raw))
		if e := errcode.From(err); e.Code != errcode.InvalidInput || len(e.Fields) != 1 || e.Fields[0].Path != c.path {
			t.Errorf("plan with %d items at %s = %v, want an invalid input there", len(ids), c.path, err)
		}
	}
	if err := schema.NewValidator().ValidateInput(def, json.RawMessage(`{"delete":["`+strings.Join(ids[:maxBatch], `","`)+`"]}`)); err != nil {
		t.Errorf("the schema refuses %d deletes: %v", maxBatch, err)
	}
}

// TestRulesApplyLabel: the label is checked once, at /label, whether the
// batch adds rules or only deletes them.
func TestRulesApplyLabel(t *testing.T) {
	ctx := context.Background()
	_, f, env := apHost(t)
	rule := `{"id":"` + apID1 + `","action":"allow","direction":"in","proto":"tcp","from":"any","to":"any","toPort":"8080","family":"v4"}`
	for _, c := range []struct{ name, raw string }{
		{"add", `{"label":" npm","add":[` + rule + `]}`},
		{"delete only", `{"label":"npm ","delete":["` + fhTag + `"]}`},
		{"too long", `{"label":"` + strings.Repeat("n", 65) + `","add":[` + rule + `]}`},
	} {
		_, err := f.rulesApply().Handler.Plan(ctx, env, json.RawMessage(c.raw))
		if e := errcode.From(err); e.Code != errcode.InvalidInput || len(e.Fields) != 1 || e.Fields[0].Path != "/label" {
			t.Errorf("%s: plan = %v, want one invalid input at /label", c.name, err)
		}
	}
	if _, err := f.rulesApply().Handler.Plan(ctx, env, json.RawMessage(`{"label":"npm: web.1","delete":["`+fhTag+`"]}`)); err != nil {
		t.Errorf("plan with a valid label = %v", err)
	}
}

// The NPM port rule ids (Contracts, NPM port rules), as literals: this task
// runs before Task 4 adds them.
const (
	kmID80V4  = "0CGS78T46E9GTMW1GWTK2TV0T5"
	kmID443V4 = "0GCPW66GP51CA48ZNYH48M71YX"
	kmID80V6  = "0QSMKDEJA5YXDMQM1MNS2FXN05"
	kmID443V6 = "0XZS5RBT87XEQW3BMX0WTC6YQW"

	kmManual4 = "### tuple ### allow tcp 443 0.0.0.0/0 any 0.0.0.0/0 in"
	kmManual6 = "### tuple ### allow tcp 443 ::/0 any ::/0 in"
	kmDeny4   = "### tuple ### deny tcp 443 0.0.0.0/0 any 0.0.0.0/0 in"
)

var kmRules = []struct {
	id, port string
	fam      model.Family
}{{kmID80V4, "80", model.FamilyV4}, {kmID443V4, "443", model.FamilyV4}, {kmID80V6, "80", model.FamilyV6}, {kmID443V6, "443", model.FamilyV6}}

// kmTuple is the tuple UFW writes in fam's file for an incoming TCP allow to
// port from anywhere with the given comment.
func kmTuple(port string, fam model.Family, comment string) string {
	all := "0.0.0.0/0"
	if fam == model.FamilyV6 {
		all = "::/0"
	}
	return "### tuple ### allow tcp " + port + " " + all + " any " + all + " in comment=" + hex.EncodeToString([]byte(comment))
}

func kmRule(id, port string, fam model.Family) string {
	return `{"id":"` + id + `","action":"allow","direction":"in","proto":"tcp","from":"any","to":"any","toPort":"` + port + `","family":"` + string(fam) + `"}`
}

// kmHost is ruHost with only rules4 and rules6 in UFW's files and IPv6 on,
// whose ufw adds each NPM port rule with label "npm" as UFW does: the tagged
// tuple replaces an uncommented tuple of the same port in its family (a
// manual allow or deny, which UFW's set_rule replaces), else it is appended.
func kmHost(t *testing.T, rules4, rules6 []string) (*fakeHost, fw, core.Env) {
	t.Helper()
	h, f, env := ruHost(t)
	h.setState(fhState{Enabled: true, LogLevel: "low", IPv6: true, Input: "DROP", Output: "ACCEPT", Forward: "DROP", Rules4: rules4, Rules6: rules6})
	for _, k := range kmRules {
		r := model.Rule{ID: k.id, Action: model.ActionAllow, Direction: model.DirIn, Proto: model.ProtoTCP, From: model.Any,
			To: model.Any, ToPort: k.port, Family: k.fam, Comment: "npm"}
		h.ufw[strings.Join(model.AddArgs(r), " ")] = func(s *fhState) int {
			lines := &s.Rules4
			if k.fam == model.FamilyV6 {
				lines = &s.Rules6
			}
			tagged := kmTuple(k.port, k.fam, model.Tag(k.id, "npm"))
			for i, l := range *lines {
				if strings.Contains(l, " tcp "+k.port+" ") && !strings.Contains(l, "comment=") {
					(*lines)[i] = tagged
					return 0
				}
			}
			*lines = append(*lines, tagged)
			return 0
		}
	}
	env.ChangeID = ruChange
	return h, f, env
}

// kmManualID is the id model.Merge gives the manual rule to port 443.
func kmManualID(t *testing.T, h *fakeHost) string {
	t.Helper()
	cfg := h.config()
	for _, r := range model.Merge(cfg.Rules4, cfg.Rules6) {
		if r.Origin == model.OriginManual && r.ToPort == "443" {
			return r.ID
		}
	}
	t.Fatal("no manual rule to port 443")
	return ""
}

func TestW12cKeepManual(t *testing.T) {
	ctx := context.Background()
	batch := `{"label":"npm","add":[` + kmRule(kmID80V4, "80", model.FamilyV4) + `,` + kmRule(kmID443V4, "443", model.FamilyV4) + `,` + kmRule(kmID443V6, "443", model.FamilyV6) + `]`
	plan := func(t *testing.T, f fw, env core.Env, raw string) (core.PlanOutcome, error) {
		t.Helper()
		env.ChangeID = ""
		return f.rulesApply().Handler.Plan(ctx, env, json.RawMessage(raw))
	}

	t.Run("without keepManual the add replaces the manual rule and is refused", func(t *testing.T) {
		_, f, env := kmHost(t, []string{kmManual4}, []string{kmManual6})
		_, err := plan(t, f, env, batch+`}`)
		if errcode.From(err).Code != errcode.FirewallManualRequired {
			t.Fatalf("plan = %v, want manual required", err)
		}
	})
	t.Run("keepManual leaves the held rules out and names the manual rule", func(t *testing.T) {
		h, f, env := kmHost(t, []string{kmManual4}, []string{kmManual6})
		manual := kmManualID(t, h)
		out, res := ruApply(t, env, f.rulesApply(), batch+`,"keepManual":true}`)
		want := []string{
			"+ rule " + kmID80V4 + ": allow in proto tcp from 0.0.0.0/0 to 0.0.0.0/0 port 80 comment \"npm\"",
			"  rule " + kmID443V4 + " left out: manual rule " + manual + " already allows it",
			"  rule " + kmID443V6 + " left out: manual rule " + manual + " already allows it",
		}
		if !reflect.DeepEqual(acDiff(out), want) {
			t.Errorf("diff\n%q\nwant\n%q", acDiff(out), want)
		}
		if !slices.Equal(res.Rules, []string{kmID80V4}) {
			t.Errorf("output rules %v, want only %s", res.Rules, kmID80V4)
		}
		if got := kmManualID(t, h); got != manual {
			t.Errorf("manual rule after the change %s, want %s unchanged", got, manual)
		}
		if n := len(findRules(h.config(), kmID443V4)) + len(findRules(h.config(), kmID443V6)); n != 0 {
			t.Errorf("%d held rules were added", n)
		}
	})
	t.Run("a manual deny of the same tuple is still refused", func(t *testing.T) {
		_, f, env := kmHost(t, []string{kmDeny4}, nil)
		_, err := plan(t, f, env, `{"label":"npm","add":[`+kmRule(kmID443V4, "443", model.FamilyV4)+`],"keepManual":true}`)
		if errcode.From(err).Code != errcode.FirewallManualRequired {
			t.Fatalf("plan = %v, want manual required", err)
		}
	})
	t.Run("a manual rule of one family does not hold a rule of the other", func(t *testing.T) {
		_, f, env := kmHost(t, []string{kmManual4}, nil)
		out, err := plan(t, f, env, `{"label":"npm","add":[`+kmRule(kmID443V6, "443", model.FamilyV6)+`],"keepManual":true}`)
		if err != nil {
			t.Fatal(err)
		}
		if diff := strings.Join(acDiff(out), "\n"); strings.Contains(diff, "left out") || !strings.Contains(diff, "+ rule "+kmID443V6+": ") {
			t.Errorf("diff\n%s\nwants the IPv6 rule added", diff)
		}
	})
	t.Run("the four rules already present plan no change (A3)", func(t *testing.T) {
		var rules4, rules6 []string
		var add []string
		for _, k := range kmRules {
			tuple := kmTuple(k.port, k.fam, model.Tag(k.id, "npm"))
			if k.fam == model.FamilyV6 {
				rules6 = append(rules6, tuple)
			} else {
				rules4 = append(rules4, tuple)
			}
			add = append(add, kmRule(k.id, k.port, k.fam))
		}
		_, f, env := kmHost(t, rules4, rules6)
		out, err := plan(t, f, env, `{"label":"npm","add":[`+strings.Join(add, ",")+`],"keepManual":true}`)
		if err != nil {
			t.Fatal(err)
		}
		if len(out.Diff) != 0 {
			t.Errorf("diff %q, want none", acDiff(out))
		}
	})
}

// TestW12cKeepManualOutOfSync: keepManual does not open the out-of-sync
// path; the plan's guard is still the hard refusal (A26).
func TestW12cKeepManualOutOfSync(t *testing.T) {
	h, f, env := kmHost(t, []string{kmManual4}, nil)
	h.diffs = []fwstate.ChainDiff{{Family: fwstate.V4, Table: "filter", Chain: "ufw-user-input", OnlyB: []string{"-p tcp -m tcp --dport 22 -j ACCEPT"}}}
	env.ChangeID = ""
	raw := `{"label":"npm","add":[` + kmRule(kmID80V4, "80", model.FamilyV4) + `,` + kmRule(kmID443V4, "443", model.FamilyV4) + `],"keepManual":true}`
	out, err := f.rulesApply().Handler.Plan(context.Background(), env, json.RawMessage(raw))
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Guards) != 1 || out.Guards[0].OK || out.Guards[0].Overridden || !strings.Contains(out.Guards[0].Message, "out of sync") {
		t.Errorf("guards %+v, want the hard out-of-sync refusal", out.Guards)
	}
}
