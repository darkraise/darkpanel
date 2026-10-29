package firewall

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/netip"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/schema"
	"github.com/darkraise/darkpanel/internal/modules/firewall/dockerfw"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/modules/firewall/guard"
	"github.com/darkraise/darkpanel/internal/modules/firewall/model"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

const (
	ctrAfter4 = "/etc/ufw/after.rules"
	ctrAfter6 = "/etc/ufw/after6.rules"
	ctrRow1   = "01J00000000000000000000R01"
	ctrRow2   = "01J00000000000000000000R02"
	ctrRow9   = "01J00000000000000000000R09"
	ctrTag1   = "01J00000000000000000000T01"
)

var ctrULIDRe = regexp.MustCompile(`^[0-9A-HJKMNP-TV-Z]{26}$`)

// ctrBinding is web's published port 8080 on every host address.
func ctrBinding() dockerfw.Binding {
	return dockerfw.Binding{Selector: "web", HostPort: 8080, Proto: "tcp"}
}

func ctrAllowRow(id string, b dockerfw.Binding) dockerfw.ContainerRule {
	return dockerfw.ContainerRule{ID: id, Binding: b, Kind: dockerfw.Allow}
}

// ctrFacts is one running container, web, publishing 8080 to its port 80
// on every IPv4 address, on the default bridge.
func ctrFacts() dockerfw.Facts {
	return dockerfw.Facts{
		Containers: []dockerfw.Container{{
			ID: "id-web", Name: "web",
			Ports:     []dockerfw.Published{{HostAddr: netip.MustParseAddr("0.0.0.0"), HostPort: 8080, ContainerPort: 80, Proto: "tcp"}},
			Endpoints: []dockerfw.Endpoint{{Network: "bridge", Bridge: "docker0", Addr4: netip.MustParseAddr("172.17.0.2")}},
		}},
		Networks: []dockerfw.Network{{ID: "id-bridge", Name: "bridge", Driver: "bridge", Bridge: "docker0",
			Subnets: []netip.Prefix{netip.MustParsePrefix("172.17.0.0/16")}}},
	}
}

// ctrState is a Docker 29.9.1 that answers, with the integration off, the
// default settings, no facts and no stored row; edit changes it before the
// block is realized.
func ctrState(edit func(*dockerState)) dockerState {
	st := dockerState{
		available: true,
		det: dockerfw.Detection{Version: "29.9.1", Driver: "iptables", DockerVariant: "nft", UFWVariant: "nft",
			RawProtection: true},
		cfg: dockerConfig{Settings: dockerfw.DefaultSettings()},
	}
	if edit != nil {
		edit(&st)
	}
	st.realized = dockerfw.Realize(st.rows, st.facts, st.cfg.Settings)
	return st
}

// ctrFiles is a configuration with both after files, each carrying the
// block of blocks, and the given user.rules tuples.
func ctrFiles(t *testing.T, blocks map[string][]byte, v4 ...string) fwstate.Config {
	t.Helper()
	cfg := fwstate.Config{IPv6: true, DefaultInput: "DROP", Files: map[string]core.RestoreEntry{}}
	for _, p := range []string{ctrAfter4, ctrAfter6} {
		data := []byte("*filter\n# after\nCOMMIT\n")
		if b, ok := blocks[p]; ok {
			var err error
			if data, err = dockerfw.WithBlock(data, b); err != nil {
				t.Fatal(err)
			}
		}
		cfg.Files[p] = core.RestoreEntry{Path: p, Present: true, Mode: 0o640, Data: data}
	}
	for _, line := range v4 {
		tp, err := model.ParseTuple(line, false)
		if err != nil {
			t.Fatal(err)
		}
		cfg.Rules4 = append(cfg.Rules4, tp)
	}
	return cfg
}

// ctrTuple is the tuple UFW writes for a route allow out on docker0 to
// 172.17.0.2 port 80, tagged id with label.
func ctrTuple(id, label string) string {
	return "### tuple ### route:allow tcp 80 172.17.0.2 any 0.0.0.0/0 out_docker0 comment=" +
		hex.EncodeToString([]byte(model.Tag(id, label)))
}

func ctrTokens(args []string, tokens ...string) bool {
	i := 0
	for _, a := range args {
		if i < len(tokens) && a == tokens[i] {
			i++
		}
	}
	return i == len(tokens)
}

func ctrEmpty(c change) bool {
	return c.flag == nil && c.sources == nil && c.enable == nil && c.restore == nil && !c.repair && !c.cleanup && !c.reload &&
		len(c.ops) == 0 && len(c.files) == 0 && len(c.rowsAdd) == 0 && len(c.rowsDel) == 0
}

// ctrPath is the first field path of an invalid-input refusal.
func ctrPath(t *testing.T, err error) string {
	t.Helper()
	e := errcode.From(err)
	if err == nil || e.Code != errcode.InvalidInput || len(e.Fields) == 0 {
		t.Fatalf("error %v, want an invalid input with a field", err)
	}
	return e.Fields[0].Path
}

func ctrOp(op string, b dockerfw.Binding, from string) ContainerOp {
	o := ContainerOp{Op: op, Binding: &b}
	if from != "" {
		o.From = netip.MustParsePrefix(from)
	}
	return o
}

// TestCtrChangeSingle: with the integration off, each action changes the
// stored rows only: an allowance or deny is one new row with a fresh ULID,
// a removal deletes the row; a rule that is stored already changes nothing;
// every invalid request is refused at the field that is wrong.
func TestCtrChangeSingle(t *testing.T) {
	env := newFakeHost(t).env()
	cfg := ctrFiles(t, nil)
	b := ctrBinding()
	other := dockerfw.Binding{Selector: "db", HostPort: 5432, Proto: "tcp"}
	st := ctrState(func(s *dockerState) {
		s.rows = []dockerfw.ContainerRule{ctrAllowRow(ctrRow1, b), ctrAllowRow(ctrRow9, other)}
	})
	add := func(t *testing.T, st dockerState, op ContainerOp) dockerfw.ContainerRule {
		t.Helper()
		c, err := containerChange(env, cfg, st, []ContainerOp{op}, false)
		if err != nil {
			t.Fatal(err)
		}
		if c.flag != nil || len(c.ops) != 0 || len(c.files) != 0 || c.reload || c.cleanup || len(c.rowsDel) != 0 || len(c.rowsAdd) != 1 {
			t.Fatalf("change %+v, want one new row and nothing else", c)
		}
		if !ctrULIDRe.MatchString(c.rowsAdd[0].ID) {
			t.Errorf("row id %q is not a ULID", c.rowsAdd[0].ID)
		}
		return c.rowsAdd[0]
	}

	t.Run("an allowance for every source", func(t *testing.T) {
		r := add(t, ctrState(nil), ctrOp("allow", b, ""))
		if r.Kind != dockerfw.Allow || r.Binding != b || r.From.IsValid() {
			t.Errorf("row %+v, want an allowance of %+v for any source", r, b)
		}
	})
	t.Run("an allowance for a range", func(t *testing.T) {
		r := add(t, ctrState(nil), ctrOp("allow", b, "198.51.100.0/24"))
		if r.Kind != dockerfw.Allow || r.From != netip.MustParsePrefix("198.51.100.0/24") {
			t.Errorf("row %+v, want an allowance from 198.51.100.0/24", r)
		}
	})
	t.Run("a deny on an allowed binding", func(t *testing.T) {
		r := add(t, st, ctrOp("deny", b, "203.0.113.0/24"))
		if r.Kind != dockerfw.Deny || r.Binding != b || r.From != netip.MustParsePrefix("203.0.113.0/24") {
			t.Errorf("row %+v, want a deny of 203.0.113.0/24 on %+v", r, b)
		}
	})
	t.Run("a removal", func(t *testing.T) {
		c, err := containerChange(env, cfg, st, []ContainerOp{{Op: "remove", ID: ctrRow9}}, false)
		if err != nil || !slices.Equal(c.rowsDel, []string{ctrRow9}) || len(c.rowsAdd) != 0 || len(c.ops) != 0 || len(c.files) != 0 {
			t.Errorf("change %+v, %v; want only the deletion of %s", c, err, ctrRow9)
		}
	})
	t.Run("a rule that is stored already", func(t *testing.T) {
		c, err := containerChange(env, cfg, st, []ContainerOp{ctrOp("allow", b, "")}, false)
		if err != nil || !ctrEmpty(c) {
			t.Errorf("change %+v, %v; want no change", c, err)
		}
	})
	t.Run("an empty batch", func(t *testing.T) {
		c, err := containerChange(env, cfg, st, nil, true)
		if err != nil || !ctrEmpty(c) {
			t.Errorf("change %+v, %v; want no change", c, err)
		}
	})

	refused := []struct {
		name string
		op   ContainerOp
		path string
	}{
		{"a deny on a binding nobody allows", ctrOp("deny", dockerfw.Binding{Selector: "web", HostPort: 8081, Proto: "tcp"}, "203.0.113.0/24"), "/binding"},
		{"a deny that could never match", ctrOp("deny", b, "10.1.0.0/16"), "/from"},
		{"a deny without a source", ctrOp("deny", b, ""), "/from"},
		{"a selector that is no name", ctrOp("allow", dockerfw.Binding{Selector: "-web", HostPort: 8080, Proto: "tcp"}, ""), "/binding/selector"},
		{"a protocol that is neither", ctrOp("allow", dockerfw.Binding{Selector: "web", HostPort: 8080, Proto: "icmp"}, ""), "/binding/proto"},
		{"no binding", ContainerOp{Op: "allow"}, "/binding"},
		{"a removal of no row", ContainerOp{Op: "remove", ID: ctrRow2}, "/id"},
		{"a removal without an id", ContainerOp{Op: "remove"}, "/id"},
		{"an operation that is none", ctrOp("toggle", b, ""), "/op"},
	}
	for _, tt := range refused {
		t.Run(tt.name, func(t *testing.T) {
			c, err := containerChange(env, cfg, st, []ContainerOp{tt.op}, false)
			if got := ctrPath(t, err); got != tt.path {
				t.Errorf("refused at %s, want %s", got, tt.path)
			}
			if !ctrEmpty(c) {
				t.Errorf("a refusal came with the change %+v", c)
			}
		})
	}
}

// TestCtrChangeBatch: a batch applies its operations in order to the stored
// rows, so a deny may follow the allowance it needs, and a refusal names the
// operation by its index.
func TestCtrChangeBatch(t *testing.T) {
	env := newFakeHost(t).env()
	cfg := ctrFiles(t, nil)
	b := ctrBinding()
	other := dockerfw.Binding{Selector: "db", HostPort: 5432, Proto: "tcp"}
	st := ctrState(func(s *dockerState) { s.rows = []dockerfw.ContainerRule{ctrAllowRow(ctrRow9, other)} })

	c, err := containerChange(env, cfg, st, []ContainerOp{
		ctrOp("allow", b, ""), ctrOp("deny", b, "203.0.113.0/24"), {Op: "remove", ID: ctrRow9},
	}, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.rowsAdd) != 2 || c.rowsAdd[0].Kind != dockerfw.Allow || c.rowsAdd[1].Kind != dockerfw.Deny ||
		c.rowsAdd[0].ID == c.rowsAdd[1].ID || !slices.Equal(c.rowsDel, []string{ctrRow9}) {
		t.Errorf("change %+v, want the allowance and the deny added in order and %s deleted", c, ctrRow9)
	}

	refused := []struct {
		name string
		ops  []ContainerOp
		path string
	}{
		{"a deny that could never match", []ContainerOp{ctrOp("allow", b, ""), ctrOp("deny", b, "10.1.0.0/16")}, "/ops/1/from"},
		{"a deny before its allowance", []ContainerOp{ctrOp("deny", b, "203.0.113.0/24"), ctrOp("allow", b, "")}, "/ops/0/binding"},
		{"a removal twice", []ContainerOp{{Op: "remove", ID: ctrRow9}, {Op: "remove", ID: ctrRow9}}, "/ops/1/id"},
		{"a removal of no row", []ContainerOp{ctrOp("allow", b, ""), {Op: "remove", ID: ctrRow2}}, "/ops/1/id"},
		{"no binding", []ContainerOp{ctrOp("allow", b, ""), ctrOp("allow", b, "198.51.100.0/24"), {Op: "deny"}}, "/ops/2/binding"},
		{"an operation that is none", []ContainerOp{{Op: "toggle"}}, "/ops/0/op"},
	}
	for _, tt := range refused {
		t.Run(tt.name, func(t *testing.T) {
			c, err := containerChange(env, cfg, st, tt.ops, true)
			if got := ctrPath(t, err); got != tt.path {
				t.Errorf("refused at %s, want %s", got, tt.path)
			}
			if !ctrEmpty(c) {
				t.Errorf("a refusal came with the change %+v", c)
			}
		})
	}
}

// TestCtrChangeIntegrationOn: with the integration on, the rows' realization
// follows in the same change: the block files of both families, a reload
// and the tagged UFW rules the new rows need; a change that leaves the
// realization as it is writes nothing, and the integration needs Docker to
// be reachable.
func TestCtrChangeIntegrationOn(t *testing.T) {
	env := newFakeHost(t).env()
	b := ctrBinding()
	on := func(s *dockerState) { s.cfg.Enabled, s.facts = true, ctrFacts() }

	t.Run("an allowance is realized", func(t *testing.T) {
		st := ctrState(on)
		cfg := ctrFiles(t, nil)
		c, err := containerChange(env, cfg, st, []ContainerOp{ctrOp("allow", b, "")}, false)
		if err != nil {
			t.Fatal(err)
		}
		if len(c.rowsAdd) != 1 || c.flag != nil || !c.reload || c.cleanup || len(c.rowsDel) != 0 {
			t.Fatalf("change %+v, want the new row, the block files and a reload", c)
		}
		for _, p := range []string{ctrAfter4, ctrAfter6} {
			if _, ok, err := dockerfw.BlockOf(c.files[p]); err != nil || !ok {
				t.Fatalf("%s: block %v, %v", p, ok, err)
			}
		}
		if !bytes.Contains(c.files[ctrAfter4], []byte(b.Chain())) {
			t.Errorf("after.rules lacks the binding chain %s:\n%s", b.Chain(), c.files[ctrAfter4])
		}
		if len(c.ops) != 1 || c.ops[0].Kind != core.EffectCreate ||
			!ctrTokens(c.ops[0].Args, "route", "allow", "docker0", "172.17.0.2", "80", "comment") {
			t.Fatalf("operations %+v, want the route allow to 172.17.0.2 port 80 out on docker0", c.ops)
		}
		args := c.ops[0].Args
		id, label, ok := model.ParseTag(args[len(args)-1])
		if !ok || !ctrULIDRe.MatchString(id) || label != dockerfw.LabelPrefix+c.rowsAdd[0].ID {
			t.Errorf("rule tag %q, want darkpanel:<ULID> labelled for the new row %s", args[len(args)-1], c.rowsAdd[0].ID)
		}
	})

	t.Run("a removal takes the realization away", func(t *testing.T) {
		before := ctrState(func(s *dockerState) {
			on(s)
			s.rows = []dockerfw.ContainerRule{ctrAllowRow(ctrRow1, b)}
		})
		cfg := ctrFiles(t, nil)
		files, err := blockFiles(cfg, before, true)
		if err != nil {
			t.Fatal(err)
		}
		cfg = ctrFiles(t, nil, ctrTuple(ctrTag1, dockerfw.LabelPrefix+ctrRow1))
		for p, data := range files {
			cfg.Files[p] = core.RestoreEntry{Path: p, Present: true, Mode: 0o640, Data: data}
		}
		c, err := containerChange(env, cfg, before, []ContainerOp{{Op: "remove", ID: ctrRow1}}, false)
		if err != nil {
			t.Fatal(err)
		}
		current := model.Merge(cfg.Rules4, cfg.Rules6)
		wantOps := []ufwOp{{Kind: core.EffectDelete, Resource: ruleResource(ctrTag1), Args: model.DeleteArgs(current[0])}}
		if !slices.Equal(c.rowsDel, []string{ctrRow1}) || !c.reload || len(c.ops) != 1 || c.ops[0].Resource != wantOps[0].Resource ||
			!slices.Equal(c.ops[0].Args, wantOps[0].Args) || c.ops[0].Kind != core.EffectDelete {
			t.Errorf("change %+v, want the row deleted, its UFW rule deleted and the block rewritten", c)
		}
		if bytes.Contains(c.files[ctrAfter4], []byte(b.Chain())) {
			t.Errorf("after.rules still has the binding chain %s", b.Chain())
		}
	})

	t.Run("nothing changes when the realization already holds", func(t *testing.T) {
		st := ctrState(func(s *dockerState) {
			on(s)
			s.rows = []dockerfw.ContainerRule{ctrAllowRow(ctrRow1, b)}
		})
		files, err := blockFiles(ctrFiles(t, nil), st, true)
		if err != nil {
			t.Fatal(err)
		}
		cfg := ctrFiles(t, nil, ctrTuple(ctrTag1, dockerfw.LabelPrefix+ctrRow1))
		for p, data := range files {
			cfg.Files[p] = core.RestoreEntry{Path: p, Present: true, Mode: 0o640, Data: data}
		}
		c, err := containerChange(env, cfg, st, nil, true)
		if err != nil || !ctrEmpty(c) {
			t.Errorf("change %+v, %v; want no change", c, err)
		}
	})

	t.Run("a change that restores a lost realization is made", func(t *testing.T) {
		st := ctrState(func(s *dockerState) {
			on(s)
			s.rows = []dockerfw.ContainerRule{ctrAllowRow(ctrRow1, b)}
		})
		c, err := containerChange(env, ctrFiles(t, nil), st, nil, true)
		if err != nil || len(c.files) != 2 || !c.reload || len(c.ops) != 1 || len(c.rowsAdd) != 0 {
			t.Errorf("change %+v, %v; want the block and the rule written with no row change", c, err)
		}
	})

	t.Run("an unreachable Docker is refused", func(t *testing.T) {
		st := ctrState(func(s *dockerState) {
			s.cfg.Enabled, s.available, s.reason = true, false, "the Docker daemon is not reachable"
		})
		c, err := containerChange(env, ctrFiles(t, nil), st, []ContainerOp{ctrOp("allow", b, "")}, false)
		if e := errcode.From(err); err == nil || e.Code != errcode.FirewallUnsupportedIntegration || !strings.Contains(e.Detail, "not reachable") {
			t.Errorf("containerChange = %v, want firewall.unsupported_integration saying Docker is not reachable", err)
		}
		if !ctrEmpty(c) {
			t.Errorf("a refusal came with the change %+v", c)
		}
	})

	t.Run("an unreachable Docker does not matter while the integration is off", func(t *testing.T) {
		st := ctrState(func(s *dockerState) { s.available, s.reason = false, "the Docker daemon is not reachable" })
		c, err := containerChange(env, ctrFiles(t, nil), st, []ContainerOp{ctrOp("allow", b, "")}, false)
		if err != nil || len(c.rowsAdd) != 1 || len(c.files) != 0 || len(c.ops) != 0 {
			t.Errorf("change %+v, %v; want the row alone", c, err)
		}
	})
}

// ctrHost is a fake host with a Docker daemon that answers and no
// container, and the firewall over both.
func ctrHost(t *testing.T) (*fakeHost, fw, *fakeDocker) {
	t.Helper()
	h := newFakeHost(t)
	d := newFakeDocker()
	h.ufw["reload"] = func(*fhState) int { return 0 }
	f, _ := newFW(h)
	f.d = fdDeps(h, d)
	return h, f, d
}

func ctrRows(t *testing.T, h *fakeHost) []dockerfw.ContainerRule {
	t.Helper()
	rows, err := listContainerRules(context.Background(), h.db)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

// ctrFind returns the stored row of the kind and binding with the source.
func ctrFind(t *testing.T, h *fakeHost, kind dockerfw.Kind, selector, from string) dockerfw.ContainerRule {
	t.Helper()
	for _, r := range ctrRows(t, h) {
		if r.Kind == kind && r.Binding.Selector == selector && (r.From.String() == from || (from == "" && !r.From.IsValid())) {
			return r
		}
	}
	t.Fatalf("no stored %s row of %s from %q in %+v", kind, selector, from, ctrRows(t, h))
	return dockerfw.ContainerRule{}
}

// ctrLines are the container rule lines of a plan's diff.
func ctrLines(out core.PlanOutcome) []string {
	var lines []string
	for _, l := range out.Diff {
		if strings.HasPrefix(l.Text, "container rule") {
			lines = append(lines, l.Op+" "+l.Text)
		}
	}
	return lines
}

// ctrCreated counts the journaled creations of container rows made.
func ctrCreated(h *fakeHost) int {
	n := 0
	for _, e := range h.journal.effects {
		if e.Kind == core.EffectCreate && e.State == core.EffectMade && strings.HasPrefix(e.Resource, "firewall.container:") {
			n++
		}
	}
	return n
}

// TestCtrActions: the four actions through the engine's pipeline with the
// integration off: each plan lists its rule lines, each apply stores or
// deletes the rows as journaled effects without any ufw command, and verify
// finds them.
func TestCtrActions(t *testing.T) {
	h, f, _ := ctrHost(t)
	web := `{"selector":"web","hostPort":8080,"proto":"tcp"}`
	shop := `{"selector":"shop/api","hostAddr":"192.0.2.1","hostPort":9090,"proto":"tcp"}`
	step := func(def core.ActionDef, raw string, wantLines ...string) {
		t.Helper()
		out, ran := acRun(t, h, def, raw)
		if got := ctrLines(out); !slices.Equal(got, wantLines) {
			t.Errorf("%s %s: lines %q, want %q", def.Spec.Name, raw, got, wantLines)
		}
		if len(ran) != 0 {
			t.Errorf("%s ran ufw %q, want no command", def.Spec.Name, ran)
		}
	}

	step(f.containerAllow(), `{"binding":`+web+`}`, "+ container rule + web all:8080/tcp allow any")
	allow := ctrFind(t, h, dockerfw.Allow, "web", "")
	if allow.Binding != ctrBinding() || ctrCreated(h) != 1 {
		t.Errorf("row %+v with %d creations journaled, want the allowance and one creation", allow, ctrCreated(h))
	}
	var created *core.Effect
	for i, e := range h.journal.effects {
		if e.Resource == "firewall.container:"+allow.ID {
			created = &h.journal.effects[i]
		}
	}
	if created == nil || created.Kind != core.EffectCreate || created.State != core.EffectMade || !created.Local || !created.InverseLocal {
		t.Errorf("effect %+v, want the made, local creation of firewall.container:%s", created, allow.ID)
	}

	step(f.containerAllow(), `{"binding":`+web+`}`)
	if n := len(ctrRows(t, h)); n != 1 || ctrCreated(h) != 1 {
		t.Errorf("%d rows and %d creations after the same allowance again, want one and one", n, ctrCreated(h))
	}

	step(f.containerDeny(), `{"binding":`+web+`,"from":"203.0.113.0/24"}`, "+ container rule + web all:8080/tcp deny 203.0.113.0/24")
	deny := ctrFind(t, h, dockerfw.Deny, "web", "203.0.113.0/24")

	step(f.containersApply(), `{"ops":[{"op":"allow","binding":`+shop+`,"from":"198.51.100.0/24"},{"op":"remove","id":"`+deny.ID+`"}]}`,
		"+ container rule + shop/api 192.0.2.1:9090/tcp allow 198.51.100.0/24",
		"- container rule - web all:8080/tcp deny 203.0.113.0/24")
	shopRow := ctrFind(t, h, dockerfw.Allow, "shop/api", "198.51.100.0/24")
	if rows := ctrRows(t, h); len(rows) != 2 || shopRow.Binding.HostAddr != netip.MustParseAddr("192.0.2.1") {
		t.Errorf("rows %+v, want web's allowance and the shop/api allowance", rows)
	}

	step(f.containersApply(), `{"ops":[]}`)
	step(f.containerRemove(), `{"id":"`+shopRow.ID+`"}`, "- container rule - shop/api 192.0.2.1:9090/tcp allow 198.51.100.0/24")
	if rows := ctrRows(t, h); len(rows) != 1 || rows[0].ID != allow.ID {
		t.Errorf("rows %+v, want web's allowance only", rows)
	}
}

// TestCtrPlanRefusals: the plan refuses what the builder refuses, at the
// same fields, and an integration that is on needs a reachable Docker.
func TestCtrPlanRefusals(t *testing.T) {
	ctx := context.Background()
	h, f, d := ctrHost(t)
	web := `{"selector":"web","hostPort":8080,"proto":"tcp"}`
	plan := func(def core.ActionDef, raw string) error {
		_, err := def.Handler.Plan(ctx, h.env(), json.RawMessage(raw))
		return err
	}
	if got := ctrPath(t, plan(f.containerDeny(), `{"binding":`+web+`,"from":"203.0.113.0/24"}`)); got != "/binding" {
		t.Errorf("deny without an allowance refused at %s, want /binding", got)
	}
	if got := ctrPath(t, plan(f.containerRemove(), `{"id":"`+ctrRow2+`"}`)); got != "/id" {
		t.Errorf("removal of no row refused at %s, want /id", got)
	}
	if got := ctrPath(t, plan(f.containersApply(), `{"ops":[{"op":"allow","binding":`+web+`},{"op":"deny","binding":`+web+`,"from":"10.1.0.0/16"}]}`)); got != "/ops/1/from" {
		t.Errorf("batch refused at %s, want /ops/1/from", got)
	}

	err := h.db.Write(ctx, func(tx *sql.Tx) error {
		return writeDockerConfig(ctx, tx, dockerConfig{Enabled: true, Settings: dockerfw.DefaultSettings()})
	})
	if err != nil {
		t.Fatal(err)
	}
	d.err = fmt.Errorf("dial unix /var/run/docker.sock: %w", dockerapi.ErrUnavailable)
	if e := errcode.From(plan(f.containerAllow(), `{"binding":`+web+`}`)); e.Code != errcode.FirewallUnsupportedIntegration {
		t.Errorf("allow with the integration on and Docker down = %v, want firewall.unsupported_integration", e)
	}
}

// TestCtrVerify: verify finds the stored rows the action stored or removed
// and refuses when they are not as it left them, before it compares the
// loaded state.
func TestCtrVerify(t *testing.T) {
	ctx := context.Background()
	h, f, _ := ctrHost(t)
	web := `{"selector":"web","hostPort":8080,"proto":"tcp"}`
	allowRaw := `{"binding":` + web + `}`
	verify := func(def core.ActionDef, raw string) error {
		return def.Handler.Verify(ctx, h.env(), json.RawMessage(raw), json.RawMessage(`{}`))
	}
	wantMismatch := func(name string, err error) {
		t.Helper()
		if e := errcode.From(err); e.Code != errcode.FirewallPredictionMismatch || !strings.Contains(e.Detail, "after the change") {
			t.Errorf("%s verify = %v, want the action's own prediction mismatch", name, err)
		}
	}
	wantMismatch("allow with no stored row", verify(f.containerAllow(), allowRaw))

	acRun(t, h, f.containerAllow(), allowRaw)
	allow := ctrFind(t, h, dockerfw.Allow, "web", "")
	if err := verify(f.containerAllow(), allowRaw); err != nil {
		t.Errorf("allow verify with the row stored = %v, want nil", err)
	}
	wantMismatch("remove of a stored row", verify(f.containerRemove(), `{"id":"`+allow.ID+`"}`))
	wantMismatch("deny with no stored row", verify(f.containerDeny(), `{"binding":`+web+`,"from":"203.0.113.0/24"}`))
	wantMismatch("apply whose removal left the row", verify(f.containersApply(), `{"ops":[{"op":"remove","id":"`+allow.ID+`"}]}`))
	if err := verify(f.containersApply(), `{"ops":[{"op":"allow","binding":`+web+`}]}`); err != nil {
		t.Errorf("apply verify with its allowance stored = %v, want nil", err)
	}

	err := h.db.Write(ctx, func(tx *sql.Tx) error { return deleteContainerRule(ctx, tx, allow.ID) })
	if err != nil {
		t.Fatal(err)
	}
	if err := verify(f.containerRemove(), `{"id":"`+allow.ID+`"}`); err != nil {
		t.Errorf("remove verify with the row gone = %v, want nil", err)
	}
	wantMismatch("allow after the row went", verify(f.containerAllow(), allowRaw))
}

// TestCtrActionDefinitions: the four actions are the revertible, agent-side,
// firewall-locked mutations of every firewall change with the lockout guard;
// their schemas take a binding, and a deny needs its source.
func TestCtrActionDefinitions(t *testing.T) {
	_, f, _ := ctrHost(t)
	defs := map[string]core.ActionDef{
		"firewall.container.allow": f.containerAllow(), "firewall.container.deny": f.containerDeny(),
		"firewall.container.remove": f.containerRemove(), "firewall.containers.apply": f.containersApply(),
	}
	for name, def := range defs {
		s := def.Spec
		if s.Name != name || s.Version != 1 || s.Class != core.ClassRevertible || s.Side != core.SideAgent ||
			!slices.Equal(s.LockKeys, []string{"firewall"}) || s.RepairedBy != "firewall.state.repair" ||
			len(s.Guards) != 1 || s.Guards[0].ID != guard.GuardID || !s.Guards[0].Overridable {
			t.Errorf("%s spec %+v", name, s)
		}
	}
	web := `{"selector":"web","hostPort":8080,"proto":"tcp"}`
	for _, c := range []struct {
		name string
		raw  string
		ok   bool
	}{
		{"firewall.container.allow", `{"binding":` + web + `}`, true},
		{"firewall.container.allow", `{"binding":` + web + `,"from":"198.51.100.0/24"}`, true},
		{"firewall.container.allow", `{"binding":` + web + `,"from":"198.51.100.0"}`, false},
		{"firewall.container.allow", `{}`, false},
		{"firewall.container.deny", `{"binding":` + web + `,"from":"203.0.113.0/24"}`, true},
		{"firewall.container.deny", `{"binding":` + web + `}`, false},
		{"firewall.container.remove", `{"id":"` + ctrRow1 + `"}`, true},
		{"firewall.container.remove", `{}`, false},
		{"firewall.containers.apply", `{"ops":[]}`, true},
		{"firewall.containers.apply", `{"ops":[{"op":"remove","id":"` + ctrRow1 + `"},{"op":"allow","binding":` + web + `}]}`, true},
		{"firewall.containers.apply", `{"ops":[{"op":"toggle"}]}`, false},
		{"firewall.containers.apply", `{"ops":[{"op":"allow","binding":` + web + `,"manual":true}]}`, false},
	} {
		err := schema.NewValidator().ValidateInput(defs[c.name], json.RawMessage(c.raw))
		if (err == nil) != c.ok {
			t.Errorf("%s input %s: error %v, want ok=%v", c.name, c.raw, err, c.ok)
		}
	}
}
