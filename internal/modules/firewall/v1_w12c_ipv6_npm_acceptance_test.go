package firewall

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/modules/firewall/guard"
	"github.com/darkraise/darkpanel/internal/modules/firewall/model"
)

// The four ids of a deploy-public v2 style batch: ports 80 and 443, IPv4 and IPv6.
const (
	qa12cID80V4  = "01JW12C0000000000000000V80"
	qa12cID443V4 = "01JW12C0000000000000000V43"
	qa12cID80V6  = "01JW12C0000000000000000W80"
	qa12cID443V6 = "01JW12C0000000000000000W43"

	qa12cManualAllow4   = "### tuple ### allow tcp 443 0.0.0.0/0 any 0.0.0.0/0 in"
	qa12cManualAllow6   = "### tuple ### allow tcp 443 ::/0 any ::/0 in"
	qa12cManualDeny4    = "### tuple ### deny tcp 443 0.0.0.0/0 any 0.0.0.0/0 in"
	qa12cManualComment4 = qa12cManualAllow4 + " comment=" + "776562" // "web"
)

type qa12cPort struct {
	id, port string
	family   model.Family
}

var qa12cPorts = []qa12cPort{
	{qa12cID80V4, "80", model.FamilyV4}, {qa12cID443V4, "443", model.FamilyV4},
	{qa12cID80V6, "80", model.FamilyV6}, {qa12cID443V6, "443", model.FamilyV6},
}

func qa12cTuple(p qa12cPort, comment string) string {
	all := "0.0.0.0/0"
	if p.family == model.FamilyV6 {
		all = "::/0"
	}
	line := "### tuple ### allow tcp " + p.port + " " + all + " any " + all + " in"
	if comment != "" {
		line += " comment=" + hex.EncodeToString([]byte(comment))
	}
	return line
}

func qa12cRuleJSON(p qa12cPort) string {
	return `{"id":"` + p.id + `","action":"allow","direction":"in","proto":"tcp","from":"any","to":"any","toPort":"` +
		p.port + `","family":"` + string(p.family) + `"}`
}

// qa12cBatch is the batch of the ports in ps, labelled "npm", with the extra members.
func qa12cBatch(ps []qa12cPort, extra string) string {
	var rs []string
	for _, p := range ps {
		rs = append(rs, qa12cRuleJSON(p))
	}
	return `{"label":"npm","add":[` + strings.Join(rs, ",") + `]` + extra + `}`
}

// qa12cSameMatch reports whether two tuple lines match the same traffic: UFW's set_rule replaces a rule whatever its
// action and comment.
func qa12cSameMatch(a, b string) bool {
	key := func(line string) string {
		var keep []string
		for i, f := range strings.Fields(line) {
			if i != 3 && !strings.HasPrefix(f, "comment=") {
				keep = append(keep, f)
			}
		}
		return strings.Join(keep, " ")
	}
	return key(a) == key(b)
}

// qa12cHost is a host whose UFW holds rules4 and rules6 and adds a tagged rule as UFW does: it replaces a rule of the
// same match, whatever its action and comment, else appends.
func qa12cHost(t *testing.T, rules4, rules6 []string) (*fakeHost, fw, core.Env) {
	t.Helper()
	h := newFakeHost(t)
	h.ufw["reload"] = func(*fhState) int { return 0 }
	h.setState(fhState{Enabled: true, LogLevel: "low", IPv6: true, Input: "DROP", Output: "ACCEPT", Forward: "DROP",
		Rules4: rules4, Rules6: rules6})
	for _, p := range qa12cPorts {
		r := model.Rule{ID: p.id, Action: model.ActionAllow, Direction: model.DirIn, Proto: model.ProtoTCP, From: model.Any,
			To: model.Any, ToPort: p.port, Family: p.family, Comment: "npm"}
		h.ufw[strings.Join(model.AddArgs(r), " ")] = func(s *fhState) int {
			lines := &s.Rules4
			if p.family == model.FamilyV6 {
				lines = &s.Rules6
			}
			for i, l := range *lines {
				if qa12cSameMatch(l, qa12cTuple(p, "")) {
					(*lines)[i] = qa12cTuple(p, model.Tag(p.id, "npm"))
					return 0
				}
			}
			*lines = append(*lines, qa12cTuple(p, model.Tag(p.id, "npm")))
			return 0
		}
	}
	f, _ := newFW(h)
	env := h.env()
	env.ChangeID = ruChange
	return h, f, env
}

func qa12cPlan(t *testing.T, f fw, env core.Env, raw string) (core.PlanOutcome, error) {
	t.Helper()
	env.ChangeID = ""
	return f.rulesApply().Handler.Plan(context.Background(), env, json.RawMessage(raw))
}

// qa12cManualID is the id of the manual rule to port 443 on h.
func qa12cManualID(t *testing.T, h *fakeHost) string {
	t.Helper()
	cfg := h.config()
	for _, r := range model.Merge(cfg.Rules4, cfg.Rules6) {
		if r.Origin == model.OriginManual && r.ToPort == "443" {
			return r.ID
		}
	}
	t.Fatal("the host holds no manual rule to port 443")
	return ""
}

func TestA3_FourPresentAllowancesPlanNoRule(t *testing.T) {
	var r4, r6 []string
	for _, p := range qa12cPorts {
		if p.family == model.FamilyV6 {
			r6 = append(r6, qa12cTuple(p, model.Tag(p.id, "npm")))
		} else {
			r4 = append(r4, qa12cTuple(p, model.Tag(p.id, "npm")))
		}
	}
	h, f, env := qa12cHost(t, r4, r6)
	n := len(h.calls)
	out, err := qa12cPlan(t, f, env, qa12cBatch(qa12cPorts, `,"keepManual":true`))
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Diff) != 0 {
		t.Errorf("diff %q, want no added rule", acDiff(out))
	}
	for _, c := range h.calls[n:] {
		if strings.HasPrefix(c, "run /usr/sbin/ufw") {
			t.Errorf("planning ran %s", c)
		}
	}
}

func TestA21_KeepManualLeavesAManualRuleAndNamesIt(t *testing.T) {
	h, f, env := qa12cHost(t, []string{qa12cManualComment4}, nil)
	manual := qa12cManualID(t, h)
	raw := qa12cBatch(qa12cPorts[1:2], `,"keepManual":true`)
	out, res := ruApply(t, env, f.rulesApply(), raw)

	named := false
	for _, l := range out.Diff {
		if l.Op != " " && (strings.Contains(l.Text, "443") || strings.Contains(l.Text, manual)) {
			t.Errorf("diff line %q %q changes the manual rule or adds port 443", l.Op, l.Text)
		}
		if l.Op == " " && strings.Contains(l.Text, manual) {
			named = true
		}
	}
	if !named {
		t.Errorf("diff %q, want a context line naming manual rule %s", acDiff(out), manual)
	}
	if got := h.state().Rules4; !slices.Equal(got, []string{qa12cManualComment4}) {
		t.Errorf("IPv4 rules after the change %q, want the manual rule with its comment, unchanged", got)
	}
	if len(res.Rules) != 0 {
		t.Errorf("output rules %v, want none: the added rule was left out", res.Rules)
	}
}

func TestA23_ApplyAndPostChangeCheckAcceptALeftOutRule(t *testing.T) {
	h, f, env := qa12cHost(t, []string{qa12cManualAllow4}, nil)
	// ruApply plans, applies and verifies, and fails the test on an error of any of them.
	_, res := ruApply(t, env, f.rulesApply(), qa12cBatch(qa12cPorts[0:2], `,"keepManual":true`))
	if !slices.Equal(res.Rules, []string{qa12cID80V4}) {
		t.Errorf("output rules %v, want only %s: the 443 rule was left out", res.Rules, qa12cID443V4)
	}
	if got := h.state().Rules4; !slices.Equal(got, []string{qa12cManualAllow4, qa12cTuple(qa12cPorts[0], model.Tag(qa12cID80V4, "npm"))}) {
		t.Errorf("IPv4 rules after the change %q, want the manual rule and the port 80 rule", got)
	}
}

func TestA24_WithoutKeepManualTheBatchIsRefusedAndADenyStaysRefused(t *testing.T) {
	t.Run("without keepManual", func(t *testing.T) {
		_, f, env := qa12cHost(t, []string{qa12cManualComment4}, nil)
		_, err := qa12cPlan(t, f, env, qa12cBatch(qa12cPorts[1:2], ""))
		if e := errcode.From(err); e.Code != errcode.FirewallManualRequired {
			t.Errorf("plan error %v, want firewall.manual_required", err)
		}
	})
	t.Run("a manual deny with keepManual", func(t *testing.T) {
		_, f, env := qa12cHost(t, []string{qa12cManualDeny4}, nil)
		_, err := qa12cPlan(t, f, env, qa12cBatch(qa12cPorts[1:2], `,"keepManual":true`))
		if e := errcode.From(err); e.Code != errcode.FirewallManualRequired {
			t.Errorf("plan error %v, want firewall.manual_required", err)
		}
	})
}

func TestA25_ManualAllowOf443LeavesThePort80RulesOnly(t *testing.T) {
	h, f, env := qa12cHost(t, []string{qa12cManualAllow4}, []string{qa12cManualAllow6})
	manual := qa12cManualID(t, h)
	out, res := ruApply(t, env, f.rulesApply(), qa12cBatch(qa12cPorts, `,"keepManual":true`))

	var added []string
	named := false
	for _, l := range out.Diff {
		switch {
		case l.Op == "+":
			added = append(added, l.Text)
		case l.Op == " " && strings.Contains(l.Text, manual):
			named = true
		}
	}
	if len(added) != 2 || !strings.Contains(added[0]+added[1], qa12cID80V4) || !strings.Contains(added[0]+added[1], qa12cID80V6) {
		t.Errorf("added lines %q, want exactly the two port 80 rules", added)
	}
	for _, a := range added {
		if !strings.Contains(a, "port 80") {
			t.Errorf("added line %q, want a port 80 rule", a)
		}
	}
	if !named {
		t.Errorf("diff %q, want a context line naming the manual rule %s", acDiff(out), manual)
	}
	slices.Sort(res.Rules)
	if want := []string{qa12cID80V4, qa12cID80V6}; !slices.Equal(res.Rules, want) {
		t.Errorf("output rules %v, want %v", res.Rules, want)
	}
}

func TestA26_OutOfSyncHostIsRefusedAtTheGuardUntilItIsRepaired(t *testing.T) {
	h, f, env := qa12cHost(t, nil, nil)
	h.diffs = []fwstate.ChainDiff{{Family: fwstate.V4, Table: "filter", Chain: "ufw-user-input", OnlyB: []string{"-p tcp -m tcp --dport 22 -j ACCEPT"}}}
	for _, overrides := range []string{"", `,"overrides":["` + guard.GuardID + `"]`} {
		out, err := qa12cPlan(t, f, env, qa12cBatch(qa12cPorts, `,"keepManual":true`+overrides))
		if err != nil {
			t.Fatalf("plan: %v", err)
		}
		if len(out.Guards) != 1 || out.Guards[0].ID != guard.GuardID || out.Guards[0].OK || out.Guards[0].Overridden {
			t.Errorf("overrides %q: guards %+v, want the failing lockout guard, not overridden", overrides, out.Guards)
		}
	}
	h.diffs = nil
	out, err := qa12cPlan(t, f, env, qa12cBatch(qa12cPorts, `,"keepManual":true`))
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Guards) != 1 || !out.Guards[0].OK {
		t.Errorf("after the repair guards %+v, want the guard to pass", out.Guards)
	}
}
