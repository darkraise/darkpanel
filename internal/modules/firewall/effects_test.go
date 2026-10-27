package firewall

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/modules/firewall/model"
)

const efTestID = "01J00000000000000000000002"

// efTestRule is a both-family Darkpanel rule allowing TCP 8080, and
// efTestLines the tuples UFW writes for it.
var efTestRule = model.Rule{ID: efTestID, Action: model.ActionAllow, Direction: model.DirIn, Proto: model.ProtoTCP,
	From: model.Any, To: model.Any, ToPort: "8080", Family: model.FamilyBoth, Log: model.LogOff, Origin: model.OriginDarkpanel}

func efTestLines() (v4, v6 string) {
	c := " comment=" + hex.EncodeToString([]byte(model.Tag(efTestID, "")))
	return "### tuple ### allow tcp 8080 0.0.0.0/0 any 0.0.0.0/0 in" + c, "### tuple ### allow tcp 8080 ::/0 any ::/0 in" + c
}

func efTestRun(t *testing.T, h *fakeHost, op ufwOp) error {
	t.Helper()
	return runUfw(context.Background(), h.env(), h.deps(), op)
}

func efTestOnly(t *testing.T, h *fakeHost) core.Effect {
	t.Helper()
	if len(h.journal.effects) != 1 {
		t.Fatalf("%d effects journaled, want 1: %+v", len(h.journal.effects), h.journal.effects)
	}
	return h.journal.effects[0]
}

func TestRunUfwAddsARule(t *testing.T) {
	h := newFakeHost(t)
	v4, v6 := efTestLines()
	op, err := addRuleOp(efTestRule)
	if err != nil {
		t.Fatal(err)
	}
	h.ufw[strings.Join(op.Args, " ")] = func(s *fhState) int {
		s.Rules4, s.Rules6 = append(s.Rules4, v4), append(s.Rules6, v6)
		return 0
	}
	if err := efTestRun(t, h, op); err != nil {
		t.Fatal(err)
	}
	e := efTestOnly(t, h)
	if e.Kind != core.EffectCreate || e.State != core.EffectMade || e.Resource != ruleResource(efTestID) ||
		e.Recovery == nil || *e.Recovery != (core.RecoveryID{Kind: "marker", Value: efTestID}) || !e.Local || !e.InverseLocal {
		t.Errorf("effect = %+v", e)
	}
	if string(e.Before.State) != "null" {
		t.Errorf("before = %s, want null", e.Before.State)
	}
	wantState := fmt.Sprintf(`{"v4":%q,"v6":%q}`, v4, v6)
	if string(e.After.State) != wantState || string(e.Post.State) != wantState {
		t.Errorf("after %s post %s, want %s", e.After.State, e.Post.State, wantState)
	}
	if want := obTestVersion(t, h, 3, 2); string(e.After.Version) != want || string(e.Post.Version) != want {
		t.Errorf("after version %s post version %s, want %s", e.After.Version, e.Post.Version, want)
	}
	if want := "run /usr/sbin/ufw " + strings.Join(op.Args, " "); !slices.Contains(h.calls, want) {
		t.Errorf("calls %v lack %q", h.calls, want)
	}
	h.journal.effects = nil
	if err := efTestRun(t, h, op); err == nil || len(h.journal.effects) != 0 {
		t.Errorf("adding an existing id = %v with %d effects", err, len(h.journal.effects))
	}
	manual := efTestRule
	manual.Origin = model.OriginManual
	if _, err := addRuleOp(manual); err == nil {
		t.Error("a manual rule became an add operation")
	}
}

func TestRunUfwDeletesARule(t *testing.T) {
	h := newFakeHost(t)
	r := findRules(h.config(), fhTag)[0].Rule
	op := deleteRuleOp(r)
	h.ufw[strings.Join(op.Args, " ")] = func(s *fhState) int {
		s.Rules4, s.Rules6 = s.Rules4[1:], s.Rules6[1:]
		return 0
	}
	before := obTestVersion(t, h, 1, 1)
	if err := efTestRun(t, h, op); err != nil {
		t.Fatal(err)
	}
	e := efTestOnly(t, h)
	if e.Kind != core.EffectDelete || e.State != core.EffectMade || e.Resource != ruleResource(fhTag) || e.Recovery != nil {
		t.Errorf("effect = %+v", e)
	}
	if want := fmt.Sprintf(`{"v4":%q,"v6":%q}`, fhRule22v4, fhRule22v6); string(e.Before.State) != want || string(e.Before.Version) != before {
		t.Errorf("before = %s %s, want %s %s", e.Before.State, e.Before.Version, want, before)
	}
	if string(e.After.State) != "null" || string(e.Post.State) != "null" {
		t.Errorf("after %s post %s, want null", e.After.State, e.Post.State)
	}
}

func TestRunUfwOutcomes(t *testing.T) {
	v4, v6 := efTestLines()
	for _, c := range []struct {
		name      string
		script    func(s *fhState) int
		runErr    error
		failOn    string
		wantErr   bool
		wantState core.EffectState
		wantNone  bool
	}{
		{name: "fails, nothing changed", script: func(*fhState) int { return 1 }, wantErr: true, wantState: core.EffectSkipped},
		{name: "succeeds, nothing changed", script: func(*fhState) int { return 0 }, wantState: core.EffectSkipped},
		{name: "refused before starting", runErr: errors.New("argument vector not allowed"), wantErr: true, wantState: core.EffectSkipped},
		{name: "fails halfway", script: func(s *fhState) int { s.Rules4 = append(s.Rules4, v4); return 1 }, wantErr: true, wantState: core.EffectIntended},
		{name: "succeeds halfway", script: func(s *fhState) int { s.Rules4 = append(s.Rules4, v4); return 0 }, wantErr: true, wantState: core.EffectIntended},
		{name: "fails after the change", script: func(s *fhState) int {
			s.Rules4, s.Rules6 = append(s.Rules4, v4), append(s.Rules6, v6)
			return 1
		}, wantErr: true, wantState: core.EffectIntended},
		{name: "journal refuses the intent", script: func(*fhState) int { return 0 }, failOn: "intend", wantErr: true, wantNone: true},
		{name: "journal refuses made", script: func(s *fhState) int {
			s.Rules4, s.Rules6 = append(s.Rules4, v4), append(s.Rules6, v6)
			return 0
		}, failOn: "made", wantErr: true, wantState: core.EffectIntended},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := newFakeHost(t)
			op, err := addRuleOp(efTestRule)
			if err != nil {
				t.Fatal(err)
			}
			key := strings.Join(op.Args, " ")
			h.ufw[key] = func(s *fhState) int {
				s.Rules4, s.Rules6 = append(s.Rules4, v4), append(s.Rules6, v6)
				return 0
			}
			h.journal.failOn = c.failOn
			if c.runErr != nil {
				h.runErr[ufwPath+" "+key] = c.runErr
			}
			// The prediction uses the scripted vector, so the script under
			// test replaces it only for the run itself.
			d := h.deps()
			predict := d.edit
			d.edit = func(cfg fwstate.Config, args []string) (fwstate.Config, error) {
				next, err := predict(cfg, args)
				if c.script != nil {
					h.ufw[key] = c.script
				}
				return next, err
			}
			err = runUfw(context.Background(), h.env(), d, op)
			if (err != nil) != c.wantErr {
				t.Errorf("runUfw = %v, want error %v", err, c.wantErr)
			}
			if c.wantNone {
				if len(h.journal.effects) != 0 {
					t.Errorf("effects %+v, want none", h.journal.effects)
				}
				if slices.ContainsFunc(h.calls, func(s string) bool { return strings.HasPrefix(s, "run ") }) {
					t.Errorf("ufw ran without a journaled intent: %v", h.calls)
				}
				return
			}
			if e := efTestOnly(t, h); e.State != c.wantState {
				t.Errorf("effect state = %s, want %s", e.State, c.wantState)
			}
		})
	}
}

func TestRunUfwDefaultsLoggingAndEnable(t *testing.T) {
	h := newFakeHost(t)
	def, err := defaultOp("outgoing", "DROP")
	if err != nil {
		t.Fatal(err)
	}
	logging, err := loggingOp("medium")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		op            ufwOp
		args          string
		edit          func(s *fhState)
		before, after string
	}{
		{def, "default deny outgoing", func(s *fhState) { s.Output = "DROP" }, `"ACCEPT"`, `"DROP"`},
		{logging, "logging medium", func(s *fhState) { s.LogLevel = "medium" }, `"low"`, `"medium"`},
		{enableOp(false), "disable", func(s *fhState) { s.Enabled = false }, `true`, `false`},
		{enableOp(true), "--force enable", func(s *fhState) { s.Enabled = true }, `false`, `true`},
	} {
		if got := strings.Join(c.op.Args, " "); got != c.args {
			t.Errorf("%s args = %q, want %q", c.op.Resource, got, c.args)
		}
		h.journal.effects = nil
		edit := c.edit
		h.ufw[c.args] = func(s *fhState) int { edit(s); return 0 }
		if err := efTestRun(t, h, c.op); err != nil {
			t.Fatalf("%s: %v", c.args, err)
		}
		e := efTestOnly(t, h)
		if e.Kind != core.EffectModify || e.State != core.EffectMade || string(e.Before.State) != c.before || string(e.Post.State) != c.after {
			t.Errorf("%s effect = %s %s before %s post %s", c.args, e.Kind, e.State, e.Before.State, e.Post.State)
		}
	}
	for _, bad := range [][2]string{{"outgoing", "allow"}, {"sideways", "DROP"}} {
		if _, err := defaultOp(bad[0], bad[1]); err == nil {
			t.Errorf("defaultOp(%q, %q) succeeded", bad[0], bad[1])
		}
	}
	if _, err := loggingOp("loud"); err == nil {
		t.Error("loggingOp(loud) succeeded")
	}
}

func TestWriteFileAndSysctlEffects(t *testing.T) {
	ctx := context.Background()
	h := newFakeHost(t)
	entry := h.config().Files["/etc/ufw/before.rules"]
	old := fileHash(entry)
	entry.Data = []byte("*filter\n# changed\nCOMMIT\n")
	if err := writeFileEffect(ctx, h.env(), h.deps(), entry); err != nil {
		t.Fatal(err)
	}
	e := efTestOnly(t, h)
	if e.State != core.EffectMade || e.Resource != fileResource(entry.Path) ||
		string(e.Before.State) != fmt.Sprintf("%q", old) || string(e.Post.State) != fmt.Sprintf("%q", fileHash(entry)) {
		t.Errorf("file effect = %+v", e)
	}
	h.journal.effects = nil
	if err := writeSysctlEffect(ctx, h.env(), h.deps(), "net/ipv4/conf/eth0/rp_filter", "2"); err != nil {
		t.Fatal(err)
	}
	e = efTestOnly(t, h)
	if e.State != core.EffectMade || string(e.Before.State) != `"1"` || string(e.Post.State) != `"2"` || h.sysctl["net/ipv4/conf/eth0/rp_filter"] != "2" {
		t.Errorf("sysctl effect = %+v", e)
	}
	h.journal.effects = nil
	h.sysctlFail["net/ipv4/ip_forward"] = errors.New("read-only")
	if err := writeSysctlEffect(ctx, h.env(), h.deps(), "net/ipv4/ip_forward", "1"); err == nil {
		t.Error("a failed sysctl write succeeded")
	}
	if e := efTestOnly(t, h); e.State != core.EffectSkipped {
		t.Errorf("failed sysctl write left the effect %s, want skipped", e.State)
	}
	h.journal.effects = nil
	if err := writeSysctlEffect(ctx, h.env(), h.deps(), "net/ipv4/conf/eth9/rp_filter", "1"); err == nil || len(h.journal.effects) != 0 {
		t.Errorf("writing a missing key = %v with %d effects", err, len(h.journal.effects))
	}
}

func TestJSONEqual(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{`{"a":1,"b":[true,null]}`, ` { "b" : [ true , null ] , "a" : 1.0 } `, true},
		{`null`, ``, true},
		{`"1"`, `1`, false},
		{`{"a":1}`, `{"a":1,"b":2}`, false},
		{`{`, `{`, false},
	} {
		if got := jsonEqual(json.RawMessage(c.a), json.RawMessage(c.b)); got != c.want {
			t.Errorf("jsonEqual(%s, %s) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}
