package dockerfw

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/modules/firewall/model"
	"github.com/darkraise/darkpanel/internal/modules/firewall/predict"
)

// roULID is the n-th 26-character ULID of these tests.
func roULID(n int) string { return fmt.Sprintf("01J%023d", n) }

// roRule is a route rule to 172.17.0.2 port 80 out on docker0 as Realize
// makes it: no id, labelled for container rule ctr.
func roRule(action model.Action, from string, ctr int) model.Rule {
	return model.Rule{
		Action: action, Direction: model.DirRoute, Proto: model.ProtoTCP, From: from,
		To: "172.17.0.2", ToPort: "80", OutIface: "docker0", Family: model.FamilyV4,
		Comment: LabelPrefix + roULID(ctr),
	}
}

// roRule6 is roRule for the IPv6 address of the container on br-0123456789ab.
func roRule6(action model.Action, from string, ctr int) model.Rule {
	r := roRule(action, from, ctr)
	r.To, r.OutIface, r.Family = "fd00:dead::2", "br-0123456789ab", model.FamilyV6
	return r
}

// roHave is r as UFW holds it once written with the tag id: the shape
// wave 1's status lists.
func roHave(r model.Rule, id int) model.Rule {
	r.ID, r.Origin, r.Log = roULID(id), model.OriginDarkpanel, model.LogOff
	return r
}

// roManual is a manual incoming allow of one TCP port, IPv4 only.
func roManual(port string) model.Rule {
	return model.Rule{
		ID: "manual-" + strings.Repeat("0", 16-len(port)) + port, Action: model.ActionAllow, Direction: model.DirIn,
		Proto: model.ProtoTCP, From: model.Any, To: model.Any, ToPort: port, Family: model.FamilyV4,
		Origin: model.OriginManual, Log: model.LogOff, Comment: "manual " + port,
	}
}

// roOther is a Darkpanel rule that is not a container rule.
func roOther() model.Rule {
	return roHave(model.Rule{
		Action: model.ActionAllow, Direction: model.DirIn, Proto: model.ProtoTCP, From: model.Any,
		To: model.Any, ToPort: "2222", Family: model.FamilyV4, Comment: "ssh",
	}, 900)
}

func roDel(id int) RuleOp { return RuleOp{Delete: roULID(id)} }

// roAdd is the operation that adds r with the new id n at position insert.
func roAdd(r model.Rule, n, insert int) RuleOp {
	r = r.Normalize()
	r.ID = roULID(n)
	return RuleOp{Add: &r, Insert: insert}
}

// roShow prints operations with their rules dereferenced.
func roShow(ops []RuleOp) string {
	var parts []string
	for _, op := range ops {
		if op.Add != nil {
			parts = append(parts, fmt.Sprintf("add@%d %+v", op.Insert, *op.Add))
		} else {
			parts = append(parts, "delete "+op.Delete)
		}
	}
	return strings.Join(parts, "\n")
}

// roSeq returns a newID function that hands out roULID(first), then the
// next numbers, and counts its calls in *calls.
func roSeq(first int, calls *int) func() string {
	return func() string {
		*calls++
		return roULID(first + *calls - 1)
	}
}

// roReplay runs the rules of current, then ops, through predict.Edit, which
// numbers and orders rules as UFW 0.36 does, and returns the rules UFW
// would then hold.
func roReplay(t *testing.T, current []model.Rule, ops []RuleOp) []model.Rule {
	t.Helper()
	empty := []byte("*filter\nCOMMIT\n")
	cfg := fwstate.Config{
		IPv6: true, LogLevel: "low", DefaultInput: "DROP", DefaultOutput: "ACCEPT", DefaultForward: "DROP",
		Files: map[string]core.RestoreEntry{
			"/etc/ufw/user.rules":  {Path: "/etc/ufw/user.rules", Present: true, Data: empty},
			"/etc/ufw/user6.rules": {Path: "/etc/ufw/user6.rules", Present: true, Data: empty},
		},
	}
	run := func(args []string) {
		t.Helper()
		next, err := predict.Edit(cfg, args)
		if err != nil {
			t.Fatalf("ufw %s: %v", strings.Join(args, " "), err)
		}
		cfg = next
	}
	byID := map[string]model.Rule{}
	for _, r := range current {
		byID[r.ID] = r
		run(model.AddArgs(r))
	}
	for _, op := range ops {
		switch {
		case op.Delete != "":
			r, ok := byID[op.Delete]
			if !ok {
				t.Fatalf("delete of %s, which current does not hold", op.Delete)
			}
			run(model.DeleteArgs(r))
		case op.Add != nil && op.Insert > 0:
			run(model.InsertArgs(op.Insert, *op.Add))
		case op.Add != nil:
			run(model.AddArgs(*op.Add))
		default:
			t.Fatal("an operation that neither deletes nor adds")
		}
	}
	return model.Merge(cfg.Rules4, cfg.Rules6)
}

// roLabels lists the labels of the container rules of one family, in order.
func roLabels(rules []model.Rule, fam model.Family) []string {
	var out []string
	for _, r := range rules {
		if _, ok := RuleOf(r.Comment); ok && r.Origin == model.OriginDarkpanel && r.Family == fam {
			out = append(out, r.Comment)
		}
	}
	return out
}

// roOthers lists the comments of the rules that are not container rules.
func roOthers(rules []model.Rule) []string {
	var out []string
	for _, r := range rules {
		if _, ok := RuleOf(r.Comment); !ok || r.Origin != model.OriginDarkpanel {
			out = append(out, r.Comment)
		}
	}
	return out
}

func TestRoRuleOps(t *testing.T) {
	allow := roRule(model.ActionAllow, model.Any, 1)
	deny := roRule(model.ActionDeny, "203.0.113.0/24", 3)
	other := roRule(model.ActionAllow, model.Any, 5)
	other.To = "172.17.0.3"
	tests := []struct {
		name    string
		current []model.Rule
		desired []model.Rule
		want    []RuleOp
		// labels lists the container rules of each family UFW must hold after
		// the operations, in order; nil derives them from desired.
		labels map[model.Family][]string
	}{
		{
			name:    "adds append when UFW holds no container rule",
			current: []model.Rule{roManual("22")},
			desired: []model.Rule{allow, deny},
			want:    []RuleOp{roAdd(allow, 500, 0), roAdd(deny, 501, 0)},
		},
		{
			name:    "a rule equal but for its id is kept",
			current: []model.Rule{roHave(allow, 101), roHave(deny, 103)},
			desired: []model.Rule{allow, deny},
		},
		{
			name:    "a changed rule is deleted and added again",
			current: []model.Rule{roHave(allow, 101)},
			desired: []model.Rule{other},
			want:    []RuleOp{roDel(101), roAdd(other, 500, 0)},
		},
		{
			name:    "a rule no desired rule equals is deleted",
			current: []model.Rule{roHave(allow, 101), roHave(deny, 103)},
			desired: []model.Rule{allow},
			want:    []RuleOp{roDel(103)},
		},
		{
			name:    "nothing desired deletes every container rule and nothing else",
			current: []model.Rule{roOther(), roHave(allow, 101), roManual("22"), roHave(deny, 103)},
			desired: nil,
			want:    []RuleOp{roDel(101), roDel(103)},
		},
		{
			name:    "a new rule goes right after the desired rule before it",
			current: []model.Rule{roManual("22"), roHave(allow, 101), roManual("80"), roHave(deny, 103)},
			desired: []model.Rule{allow, other, deny},
			want:    []RuleOp{roAdd(other, 500, 3)},
		},
		{
			name:    "a new first rule goes before the first kept rule",
			current: []model.Rule{roManual("22"), roHave(allow, 101)},
			desired: []model.Rule{other, allow},
			want:    []RuleOp{roAdd(other, 500, 2)},
		},
		{
			name:    "a kept rule that is out of order is deleted and added after its predecessor",
			current: []model.Rule{roHave(deny, 103), roHave(allow, 101)},
			desired: []model.Rule{allow, deny},
			want:    []RuleOp{roDel(103), roAdd(deny, 500, 0)},
		},
		{
			name: "an IPv6 rule is numbered after every IPv4 rule",
			current: []model.Rule{
				roManual("22"), roHave(allow, 101),
				roHave(roRule6(model.ActionAllow, model.Any, 7), 107),
			},
			desired: []model.Rule{
				allow, roRule6(model.ActionDeny, "2001:db8::/32", 9), roRule6(model.ActionAllow, model.Any, 7),
			},
			want: []RuleOp{roAdd(roRule6(model.ActionDeny, "2001:db8::/32", 9), 500, 3)},
		},
		{
			name:    "an IPv4 rule inserted ahead of IPv6 rules keeps its IPv4 number",
			current: []model.Rule{roManual("22"), roHave(roRule6(model.ActionAllow, model.Any, 7), 107)},
			desired: []model.Rule{allow, roRule6(model.ActionAllow, model.Any, 7)},
			want:    []RuleOp{roAdd(allow, 500, 0)},
		},
		{
			name:    "an insert is numbered after the deletes before it have run",
			current: []model.Rule{roManual("22"), roHave(allow, 101), roHave(deny, 103)},
			desired: []model.Rule{other, deny},
			want:    []RuleOp{roDel(101), roAdd(other, 500, 2)},
		},
		{
			name: "an IPv6 insert counts the IPv4 rules left after the deletes",
			current: []model.Rule{
				roManual("22"), roHave(allow, 101),
				roHave(roRule6(model.ActionAllow, model.Any, 7), 107),
			},
			desired: []model.Rule{roRule6(model.ActionDeny, "2001:db8::/32", 9), roRule6(model.ActionAllow, model.Any, 7)},
			want:    []RuleOp{roDel(101), roAdd(roRule6(model.ActionDeny, "2001:db8::/32", 9), 500, 2)},
		},
		{
			name:    "two desired rules that differ only in label collapse to the first",
			current: nil,
			desired: []model.Rule{allow, roRule(model.ActionAllow, model.Any, 2)},
			want:    []RuleOp{roAdd(allow, 500, 0)},
			labels:  map[model.Family][]string{model.FamilyV4: {allow.Comment}},
		},
		{
			name:    "a manual rule and a manual-looking label are never touched",
			current: []model.Rule{roManual("22"), {ID: "manual-00000000000000aa", Action: model.ActionAllow, Direction: model.DirIn, Proto: model.ProtoTCP, From: model.Any, To: model.Any, ToPort: "81", Family: model.FamilyV4, Origin: model.OriginManual, Log: model.LogOff, Comment: allow.Comment}},
			desired: []model.Rule{deny},
			want:    []RuleOp{roAdd(deny, 500, 0)},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			got, err := RuleOps(tt.current, tt.desired, roSeq(500, &calls))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) && (len(got) != 0 || len(tt.want) != 0) {
				t.Fatalf("operations:\n%s\nwant:\n%s", roShow(got), roShow(tt.want))
			}
			adds := 0
			for _, op := range got {
				if op.Add != nil {
					adds++
				}
			}
			if calls != adds {
				t.Errorf("newID called %d times for %d adds", calls, adds)
			}
			rules := roReplay(t, tt.current, got)
			for _, fam := range []model.Family{model.FamilyV4, model.FamilyV6} {
				want := tt.labels[fam]
				if tt.labels == nil {
					for _, d := range tt.desired {
						if d.Family == fam {
							want = append(want, d.Comment)
						}
					}
				}
				if have := roLabels(rules, fam); !slices.Equal(have, want) {
					t.Errorf("UFW holds %s container rules %q after the operations, want %q", fam, have, want)
				}
			}
			if have, want := roOthers(rules), roOthers(tt.current); !slices.Equal(have, want) {
				t.Errorf("rules that are not container rules are %q after the operations, want %q", have, want)
			}
		})
	}
}

func TestRoRuleOpsRefuses(t *testing.T) {
	allow := roRule(model.ActionAllow, model.Any, 1)
	noLabel := allow
	noLabel.Comment = "web"
	both := model.Rule{Action: model.ActionAllow, Direction: model.DirIn, Proto: model.ProtoTCP, From: model.Any, To: model.Any, ToPort: "80", Family: model.FamilyBoth, Comment: LabelPrefix + roULID(1)}
	manualTwin := roManual("80")
	manualTwin.Direction, manualTwin.Proto = model.DirRoute, model.ProtoTCP
	manualTwin.To, manualTwin.ToPort, manualTwin.OutIface = "172.17.0.2", "80", "docker0"
	manualTwin.Comment = "by hand"
	raw := roHave(allow, 101)
	raw.Raw = "### tuple ### route:allow tcp 80 172.17.0.2 any 0.0.0.0/0 out_docker0"
	tests := []struct {
		name    string
		current []model.Rule
		desired []model.Rule
		newID   func() string
		want    string
	}{
		{
			name: "a desired rule without a container label", desired: []model.Rule{noLabel},
			newID: func() string { return roULID(500) }, want: "dockerfw: desired rule",
		},
		{
			name: "a desired rule for both families", desired: []model.Rule{both},
			newID: func() string { return roULID(500) }, want: "dockerfw: desired rule",
		},
		{
			name: "an id that is not a ULID", desired: []model.Rule{allow},
			newID: func() string { return "not-a-ulid" }, want: "dockerfw: ",
		},
		{
			name: "a desired rule that would take over a manual rule", current: []model.Rule{manualTwin},
			desired: []model.Rule{allow}, newID: func() string { return roULID(500) },
			want: manualTwin.ID,
		},
		{
			name: "a container rule outside the model", current: []model.Rule{raw},
			desired: nil, newID: func() string { return roULID(500) }, want: roULID(101),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ops, err := RuleOps(tt.current, tt.desired, tt.newID)
			if err == nil {
				t.Fatalf("no error, operations:\n%s", roShow(ops))
			}
			if !strings.HasPrefix(err.Error(), "dockerfw: ") || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error %q, want the prefix \"dockerfw: \" and %q", err, tt.want)
			}
			if len(ops) != 0 {
				t.Errorf("an error came with operations:\n%s", roShow(ops))
			}
		})
	}
}
