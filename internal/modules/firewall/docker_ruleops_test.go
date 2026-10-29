package firewall

import (
	"encoding/hex"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/oklog/ulid/v2"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules/firewall/dockerfw"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/modules/firewall/model"
)

// droULID is the n-th 26-character ULID of these tests.
func droULID(n int) string { return fmt.Sprintf("01J%023d", n) }

// droSeq returns a newID function that hands out droULID(first), then the
// next numbers.
func droSeq(first int) func() string {
	n := first - 1
	return func() string {
		n++
		return droULID(n)
	}
}

// droRouteTuple is the tuple line UFW writes in user.rules for a route rule
// out on docker0 to 172.17.0.2 port 80, tagged id with label; an empty from
// is any source.
func droRouteTuple(action, from, id, label string) string {
	if from == "" {
		from = "0.0.0.0/0"
	}
	return fmt.Sprintf("### tuple ### route:%s tcp 80 172.17.0.2 any %s out_docker0 comment=%s",
		action, from, hex.EncodeToString([]byte(model.Tag(id, label))))
}

// droConfig is a configuration whose user.rules holds the given tuple
// lines.
func droConfig(t *testing.T, v4 ...string) fwstate.Config {
	t.Helper()
	cfg := fwstate.Config{IPv6: true, DefaultInput: "DROP"}
	for _, line := range v4 {
		tp, err := model.ParseTuple(line, false)
		if err != nil {
			t.Fatal(err)
		}
		cfg.Rules4 = append(cfg.Rules4, tp)
	}
	return cfg
}

// droRoute is the rule Realize wants for a route allow or deny out on
// docker0 to 172.17.0.2 port 80, labelled for container rule row: no id.
func droRoute(action model.Action, from, row string) model.Rule {
	return model.Rule{
		Action: action, Direction: model.DirRoute, Proto: model.ProtoTCP, From: from, To: "172.17.0.2", ToPort: "80",
		OutIface: "docker0", Family: model.FamilyV4, Log: model.LogOff, Comment: dockerfw.LabelPrefix + row,
		Origin: model.OriginDarkpanel,
	}
}

// droWithID is r as containerRuleOps must add it: with its new id.
func droWithID(r model.Rule, id string) model.Rule {
	r.ID = id
	return r
}

// TestDroNewULID: newULID mints a fresh valid ULID on every call, carrying
// the time of the env's clock.
func TestDroNewULID(t *testing.T) {
	env := newFakeHost(t).env()
	newID := newULID(env)
	seen := map[string]bool{}
	for range 5 {
		id := newID()
		parsed, err := ulid.ParseStrict(id)
		if err != nil {
			t.Fatalf("newID() = %q: %v", id, err)
		}
		if got, want := parsed.Time(), ulid.Timestamp(env.Clock.Now()); got != want {
			t.Errorf("ULID %s carries time %d, want the clock's %d", id, got, want)
		}
		if seen[id] {
			t.Errorf("ULID %s was minted twice", id)
		}
		seen[id] = true
	}
}

// TestDroContainerRuleOps: dockerfw.RuleOps' operations become ufw
// operations that a change can journal: a delete is wave 1's delete of the
// current rule with that id, an add that appends is wave 1's add, and an
// add at a position is the same effect through "ufw insert".
func TestDroContainerRuleOps(t *testing.T) {
	const (
		row1, row2, row3 = "01J00000000000000000000R01", "01J00000000000000000000R02", "01J00000000000000000000R03"
		tag1, tag2       = "01J00000000000000000000T01", "01J00000000000000000000T02"
	)
	manual := fhRule3000v4
	allow1 := droRouteTuple("allow", "", tag1, dockerfw.LabelPrefix+row1)
	deny2 := droRouteTuple("deny", "203.0.113.0/24", tag2, dockerfw.LabelPrefix+row2)
	cfg := droConfig(t, manual, allow1, deny2)
	current := model.Merge(cfg.Rules4, cfg.Rules6)
	if len(current) != 3 || current[1].ID != tag1 || current[2].ID != tag2 {
		t.Fatalf("the fixture's rules are %+v, want the manual rule and the two tagged ones", current)
	}
	wantDelete := func(r model.Rule) ufwOp {
		return ufwOp{Kind: core.EffectDelete, Resource: ruleResource(r.ID), Args: model.DeleteArgs(r)}
	}
	wantAdd := func(r model.Rule) ufwOp {
		return ufwOp{Kind: core.EffectCreate, Resource: ruleResource(r.ID), Args: model.AddArgs(r)}
	}
	wantInsert := func(pos int, r model.Rule) ufwOp {
		return ufwOp{Kind: core.EffectCreate, Resource: ruleResource(r.ID), Args: model.InsertArgs(pos, r)}
	}
	kept := []model.Rule{droRoute(model.ActionAllow, model.Any, row1), droRoute(model.ActionDeny, "203.0.113.0/24", row2)}
	fresh := droRoute(model.ActionAllow, "198.51.100.0/24", row3)

	tests := []struct {
		name    string
		cfg     fwstate.Config
		desired []model.Rule
		want    []ufwOp
	}{
		{"rules the host holds already need no operation", cfg, kept, nil},
		{"nothing desired deletes every container rule and keeps the manual one", cfg, nil,
			[]ufwOp{wantDelete(current[1]), wantDelete(current[2])}},
		{"a missing rule at the end is added", droConfig(t, manual), kept[:1],
			[]ufwOp{wantAdd(droWithID(kept[0], droULID(500)))}},
		{"a missing rule before a kept one is inserted at its position", droConfig(t, manual, allow1),
			[]model.Rule{fresh, kept[0]},
			[]ufwOp{wantInsert(2, droWithID(fresh, droULID(500)))}},
		{"a changed rule is deleted and added again", droConfig(t, allow1), []model.Rule{fresh},
			[]ufwOp{wantDelete(current[1]), wantAdd(droWithID(fresh, droULID(500)))}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := containerRuleOps(tt.cfg, tt.desired, droSeq(500))
			if err != nil {
				t.Fatal(err)
			}
			if len(got) == 0 && len(tt.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("operations\n%+v\nwant\n%+v", got, tt.want)
			}
		})
	}
}

// TestDroContainerRuleOpsError: a failure of dockerfw.RuleOps, here an id
// that is not a ULID, is returned as a firewall error and no operation
// comes with it.
func TestDroContainerRuleOpsError(t *testing.T) {
	ops, err := containerRuleOps(droConfig(t, fhRule3000v4), []model.Rule{droRoute(model.ActionAllow, model.Any, droULID(1))},
		func() string { return "not-a-ulid" })
	if err == nil || !strings.HasPrefix(err.Error(), "firewall:") || !strings.Contains(err.Error(), "dockerfw:") {
		t.Errorf("containerRuleOps = %v, want a firewall error that wraps the dockerfw one", err)
	}
	if len(ops) != 0 {
		t.Errorf("an error came with operations %+v", ops)
	}
}
