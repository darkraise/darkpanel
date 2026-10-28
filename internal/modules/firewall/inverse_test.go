package firewall

import (
	"context"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/modules/firewall/model"
)

// ivTestForward runs op forward on h and returns its made effect.
func ivTestForward(t *testing.T, h *fakeHost, op ufwOp) core.Effect {
	t.Helper()
	h.journal.effects = nil
	if err := runUfw(context.Background(), h.env(), h.deps(), op); err != nil {
		t.Fatal(err)
	}
	e := efTestOnly(t, h)
	if e.State != core.EffectMade {
		t.Fatalf("forward effect is %s", e.State)
	}
	return e
}

func ivTestInsert(lines []string, i int, line string) []string {
	return slices.Insert(slices.Clone(lines), i, line)
}

func TestInverseDeletesAnAddedRule(t *testing.T) {
	ctx := context.Background()
	h := newFakeHost(t)
	snap := rsTestSnap(t, h, false)
	v4, v6 := efTestLines()
	op, err := addRuleOp(efTestRule)
	if err != nil {
		t.Fatal(err)
	}
	h.ufw[strings.Join(op.Args, " ")] = func(s *fhState) int {
		s.Rules4, s.Rules6 = append(s.Rules4, v4), append(s.Rules6, v6)
		return 0
	}
	e := ivTestForward(t, h, op)
	del := strings.Join(model.DeleteArgs(efTestRule), " ")
	h.ufw[del] = func(s *fhState) int {
		s.Rules4, s.Rules6 = slices.DeleteFunc(s.Rules4, func(l string) bool { return l == v4 }), slices.DeleteFunc(s.Rules6, func(l string) bool { return l == v6 })
		return 0
	}
	if err := inverse(ctx, h.env(), h.deps(), snap, e); err != nil {
		t.Fatal(err)
	}
	if got := fileHashes(h.config().Files); !reflect.DeepEqual(got, snap.Hashes) {
		t.Error("the files differ from before the rule was added")
	}
	if !slices.Contains(h.calls, "run /usr/sbin/ufw "+del) {
		t.Errorf("calls %v lack the delete", h.calls)
	}
}

func TestInverseReinsertsADeletedRule(t *testing.T) {
	v4, v6 := efTestLines()
	for _, c := range []struct {
		name                 string
		rules4, rules6       []string
		id                   string
		wantInsert           []string
		removeAt4, removeAt6 int
	}{
		{
			name: "both families in the middle", id: efTestID,
			rules4: []string{fhRule22v4, v4, fhRule3000v4}, rules6: []string{fhRule22v6, v6, fhRule53v6},
			removeAt4: 1, removeAt6: 1,
			wantInsert: []string{
				"insert 2 allow in proto tcp from 0.0.0.0/0 to 0.0.0.0/0 port 8080 comment darkpanel:" + efTestID,
				"insert 5 allow in proto tcp from ::/0 to ::/0 port 8080 comment darkpanel:" + efTestID,
			},
		},
		{
			name: "both families at the end", id: efTestID,
			rules4: []string{fhRule22v4, v4}, rules6: []string{fhRule22v6, v6},
			removeAt4: 1, removeAt6: 1,
			wantInsert: []string{
				"allow in proto tcp from 0.0.0.0/0 to 0.0.0.0/0 port 8080 comment darkpanel:" + efTestID,
				"allow in proto tcp from ::/0 to ::/0 port 8080 comment darkpanel:" + efTestID,
			},
		},
		{
			name: "IPv6 manual rule first", id: "",
			rules4: []string{fhRule22v4}, rules6: []string{fhRule53v6, fhRule22v6},
			removeAt4: -1, removeAt6: 0,
			wantInsert: []string{"insert 2 deny in proto udp from 2001:db8::/32 to ::/0 port 53"},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			ctx := context.Background()
			h := newFakeHost(t)
			s := h.state()
			s.Rules4, s.Rules6 = c.rules4, c.rules6
			h.setState(s)
			snap := rsTestSnap(t, h, false)
			id := c.id
			if id == "" {
				id = obTestID(t, nil, []string{fhRule53v6})
			}
			r := findRules(h.config(), id)[0].Rule
			op := deleteRuleOp(r)
			removed4, removed6 := "", ""
			h.ufw[strings.Join(op.Args, " ")] = func(s *fhState) int {
				if c.removeAt4 >= 0 {
					removed4 = s.Rules4[c.removeAt4]
					s.Rules4 = slices.Delete(s.Rules4, c.removeAt4, c.removeAt4+1)
				}
				removed6 = s.Rules6[c.removeAt6]
				s.Rules6 = slices.Delete(s.Rules6, c.removeAt6, c.removeAt6+1)
				return 0
			}
			e := ivTestForward(t, h, op)
			// The scripted ufw puts an inserted IPv4 line at its 1-based
			// position, an IPv6 one at its position after the IPv4 rules,
			// and an added one at the end, as ufw does.
			for _, args := range c.wantInsert {
				v6half := strings.Contains(args, "::/0") || strings.Contains(args, "2001:db8")
				line := removed4
				if v6half {
					line = removed6
				}
				fields := strings.Fields(args)
				h.ufw[args] = func(s *fhState) int {
					if fields[0] != "insert" {
						if v6half {
							s.Rules6 = append(s.Rules6, line)
						} else {
							s.Rules4 = append(s.Rules4, line)
						}
						return 0
					}
					pos := 0
					for _, ch := range fields[1] {
						pos = pos*10 + int(ch-'0')
					}
					if v6half {
						s.Rules6 = ivTestInsert(s.Rules6, pos-len(s.Rules4)-1, line)
					} else {
						s.Rules4 = ivTestInsert(s.Rules4, pos-1, line)
					}
					return 0
				}
			}
			h.calls = nil
			if err := inverse(ctx, h.env(), h.deps(), snap, e); err != nil {
				t.Fatal(err)
			}
			var runs []string
			for _, call := range rsTestRuns(h) {
				runs = append(runs, strings.TrimPrefix(call, "run /usr/sbin/ufw "))
			}
			if !reflect.DeepEqual(runs, c.wantInsert) {
				t.Errorf("ufw ran %q, want %q", runs, c.wantInsert)
			}
			if got := fileHashes(h.config().Files); !reflect.DeepEqual(got, snap.Hashes) {
				t.Error("the files differ from before the rule was deleted")
			}
		})
	}
}

// TestInverseReinsertKeepsTheSnapshotOrder: the snapshot holds A, B, R, C,
// the change deletes R and an operator then inserts M first. The undo puts
// R after B, its nearest predecessor still there, not at its old absolute
// place 3, which would put it before B.
func TestInverseReinsertKeepsTheSnapshotOrder(t *testing.T) {
	const (
		a = "### tuple ### allow tcp 80 0.0.0.0/0 any 0.0.0.0/0 in"
		b = "### tuple ### allow tcp 81 0.0.0.0/0 any 0.0.0.0/0 in"
		r = "### tuple ### deny tcp 82 0.0.0.0/0 any 0.0.0.0/0 in"
		c = "### tuple ### allow tcp 83 0.0.0.0/0 any 0.0.0.0/0 in"
		m = "### tuple ### allow tcp 84 0.0.0.0/0 any 0.0.0.0/0 in"
	)
	for _, tc := range []struct {
		name       string
		edit, want []string
	}{
		{"insert first", []string{m, a, b, c}, []string{m, a, b, r, c}},
		{"predecessor deleted", []string{m, a, c}, []string{m, a, r, c}},
		{"no predecessor left", []string{m, c}, []string{r, m, c}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			h := newFakeHost(t)
			s := h.state()
			s.Rules4, s.Rules6 = []string{a, b, r, c}, nil
			h.setState(s)
			snap := rsTestSnap(t, h, false)
			rule := findRules(h.config(), obTestID(t, []string{r}, nil))[0].Rule
			op := deleteRuleOp(rule)
			h.ufw[strings.Join(op.Args, " ")] = func(s *fhState) int {
				s.Rules4 = slices.DeleteFunc(s.Rules4, func(l string) bool { return l == r })
				return 0
			}
			e := ivTestForward(t, h, op)
			s = h.state()
			s.Rules4 = tc.edit
			h.setState(s)
			for pos := 1; pos <= len(tc.edit); pos++ {
				h.ufw[strings.Join(model.InsertArgs(pos, rule), " ")] = func(s *fhState) int {
					s.Rules4 = ivTestInsert(s.Rules4, pos-1, r)
					return 0
				}
			}
			if err := inverse(ctx, h.env(), h.deps(), snap, e); err != nil {
				t.Fatal(err)
			}
			if got := h.state().Rules4; !slices.Equal(got, tc.want) {
				t.Errorf("rules after the undo = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestInverseFailsWhenUfwSkipsTheInsert(t *testing.T) {
	ctx := context.Background()
	h := newFakeHost(t)
	snap := rsTestSnap(t, h, false)
	op := deleteRuleOp(findRules(h.config(), fhTag)[0].Rule)
	h.ufw[strings.Join(op.Args, " ")] = func(s *fhState) int {
		s.Rules4, s.Rules6 = s.Rules4[1:], s.Rules6[1:]
		return 0
	}
	e := ivTestForward(t, h, op)
	// ufw answers "Skipping inserting existing rule" and "Skipping adding
	// existing rule" with exit 0.
	h.ufw["insert 1 allow in proto tcp from 0.0.0.0/0 to 0.0.0.0/0 port 22 comment darkpanel:"+fhTag] = func(*fhState) int { return 0 }
	h.ufw["allow in proto tcp from ::/0 to ::/0 port 22 comment darkpanel:"+fhTag] = func(*fhState) int { return 0 }
	if err := inverse(ctx, h.env(), h.deps(), snap, e); err == nil || !strings.Contains(err.Error(), "left") {
		t.Errorf("inverse = %v, want an error naming what it left", err)
	}
	h.ufw["insert 1 allow in proto tcp from 0.0.0.0/0 to 0.0.0.0/0 port 22 comment darkpanel:"+fhTag] = func(*fhState) int { return 1 }
	if err := inverse(ctx, h.env(), h.deps(), snap, e); err == nil || !strings.Contains(err.Error(), "exited 1") {
		t.Errorf("inverse = %v, want ufw's failure", err)
	}
}

func TestInverseSetsValuesBack(t *testing.T) {
	ctx := context.Background()
	h := newFakeHost(t)
	snap := rsTestSnap(t, h, false)
	def, _ := defaultOp("incoming", "ACCEPT")
	logging, _ := loggingOp("full")
	for _, c := range []struct {
		forward    ufwOp
		edit, undo func(s *fhState)
		undoArgs   string
	}{
		{def, func(s *fhState) { s.Input = "ACCEPT" }, func(s *fhState) { s.Input = "DROP" }, "default deny incoming"},
		{logging, func(s *fhState) { s.LogLevel = "full" }, func(s *fhState) { s.LogLevel = "low" }, "logging low"},
		// UFW's start applies its sysctl file.
		{enableOp(false), func(s *fhState) { s.Enabled = false }, func(s *fhState) { s.Enabled = true; h.sysctl["net/ipv4/ip_forward"] = "1" }, "--force enable"},
	} {
		edit, undo := c.edit, c.undo
		h.ufw[strings.Join(c.forward.Args, " ")] = func(s *fhState) int { edit(s); return 0 }
		h.ufw[c.undoArgs] = func(s *fhState) int { undo(s); return 0 }
		e := ivTestForward(t, h, c.forward)
		h.calls = nil
		if err := inverse(ctx, h.env(), h.deps(), snap, e); err != nil {
			t.Fatalf("undo %s: %v", c.undoArgs, err)
		}
		if h.calls[0] != "run /usr/sbin/ufw "+c.undoArgs {
			t.Errorf("undo ran %v, want %q first", h.calls, c.undoArgs)
		}
		wroteSysctl := slices.Contains(h.calls, "sysctl net/ipv4/ip_forward=0")
		if wroteSysctl != (c.forward.Resource == resourceEnabled) {
			t.Errorf("undo of %s wrote the sysctl values back: %v", c.forward.Resource, wroteSysctl)
		}
	}
	if got := fileHashes(h.config().Files); !reflect.DeepEqual(got, snap.Hashes) {
		t.Error("the files differ from the snapshot after every undo")
	}
}

func TestInverseFileAndSysctl(t *testing.T) {
	ctx := context.Background()
	h := newFakeHost(t)
	snap := rsTestSnap(t, h, false)
	entry := h.config().Files["/etc/ufw/after.rules"]
	entry.Data = []byte("*filter\n# edited\nCOMMIT\n")
	h.journal.effects = nil
	if err := writeFileEffect(ctx, h.env(), h.deps(), entry); err != nil {
		t.Fatal(err)
	}
	fileEffect := efTestOnly(t, h)
	if err := inverse(ctx, h.env(), h.deps(), snap, fileEffect); err != nil {
		t.Fatal(err)
	}
	if got := fileHash(h.config().Files[entry.Path]); got != snap.Hashes[entry.Path] {
		t.Errorf("after.rules = %s, want the snapshot's", got)
	}
	other := fileEffect
	other.Before = core.StateDoc{State: mustJSON("sha256:00")}
	if err := inverse(ctx, h.env(), h.deps(), snap, other); err == nil {
		t.Error("a file effect whose before the snapshot does not hold was undone")
	}
	h.journal.effects = nil
	if err := writeSysctlEffect(ctx, h.env(), h.deps(), "net/ipv4/conf/default/rp_filter", "2"); err != nil {
		t.Fatal(err)
	}
	if err := inverse(ctx, h.env(), h.deps(), snap, efTestOnly(t, h)); err != nil {
		t.Fatal(err)
	}
	if h.sysctl["net/ipv4/conf/default/rp_filter"] != "1" {
		t.Errorf("rp_filter = %s after the undo, want 1", h.sysctl["net/ipv4/conf/default/rp_filter"])
	}
}

func TestInverseRefusesWhatItCannotUndo(t *testing.T) {
	ctx := context.Background()
	h := newFakeHost(t)
	snap := rsTestSnap(t, h, false)
	for name, e := range map[string]core.Effect{
		"unknown noun":       {Seq: 1, Kind: core.EffectModify, Resource: "firewall.thing:x"},
		"added without post": {Seq: 1, Kind: core.EffectCreate, Resource: ruleResource(fhTag)},
		"doubled rule": {Seq: 1, Kind: core.EffectCreate, Resource: ruleResource(fhTag),
			Post: &core.StateDoc{State: mustJSON(ruleState{Matches: 2})}},
		"application rule": {Seq: 1, Kind: core.EffectDelete, Resource: ruleResource("manual-0000000000000000"),
			Before: core.StateDoc{State: mustJSON(ruleState{V4: "### tuple ### allow any any 0.0.0.0/0 any 0.0.0.0/0 OpenSSH - in"}), Version: mustJSON(ufwVersion{Pos4: 1})}},
	} {
		if err := inverse(ctx, h.env(), h.deps(), snap, e); err == nil {
			t.Errorf("%s: inverse succeeded", name)
		}
	}
	if slices.ContainsFunc(h.calls, func(c string) bool { return strings.HasPrefix(c, "run ") }) {
		t.Errorf("a refused undo ran %v", h.calls)
	}
}

func TestInverseKeepsAManualSysctlEdit(t *testing.T) {
	ctx := context.Background()
	h := newFakeHost(t)
	s := h.state()
	s.Enabled = false
	h.setState(s)
	snap := rsTestSnap(t, h, false)
	h.ufw["--force enable"] = func(s *fhState) int {
		s.Enabled = true
		h.sysctl["net/ipv4/ip_forward"] = "1"
		return 0
	}
	h.ufw["disable"] = func(s *fhState) int { s.Enabled = false; return 0 }
	e := ivTestForward(t, h, enableOp(true))
	if h.sysctl["net/ipv4/ip_forward"] != "1" {
		t.Fatal("UFW's start left net/ipv4/ip_forward as it was")
	}
	// The snapshot's and the predicted value are both 2.
	h.sysctl["net/ipv4/conf/all/rp_filter"] = "0"
	d := h.deps()
	var trs []fwstate.Transition
	d.sysctl = func(_ fwstate.Config, cur map[string]string, tr fwstate.Transition) (map[string]string, error) {
		trs = append(trs, tr)
		out := maps.Clone(cur)
		if tr == fwstate.TransEnable {
			out["net/ipv4/ip_forward"] = "1"
		}
		return out, nil
	}
	if err := inverse(ctx, h.env(), d, snap, e); err != nil {
		t.Fatal(err)
	}
	if got, want := h.sysctl["net/ipv4/ip_forward"], snap.State.Sysctl["net/ipv4/ip_forward"]; got != want || want != "0" {
		t.Errorf("net/ipv4/ip_forward = %s after the undo, want the snapshot's %s", got, want)
	}
	if got := h.sysctl["net/ipv4/conf/all/rp_filter"]; got != "0" {
		t.Errorf("net/ipv4/conf/all/rp_filter = %s after the undo, want the manual 0", got)
	}
	if !reflect.DeepEqual(trs, []fwstate.Transition{fwstate.TransEnable}) {
		t.Errorf("predicted transitions %v, want the forward enable", trs)
	}
}
