package firewall

import (
	"reflect"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules/firewall/model"
)

// dgRule80v4 is a Darkpanel IPv4 rule tagged 01J00000000000000000000002
// with the label "web".
const dgRule80v4 = "### tuple ### allow tcp 80 0.0.0.0/0 any 0.0.0.0/0 in comment=6461726b70616e656c3a30314a303030303030303030303030303030303030303030303220776562"

// dgRuleRaw is a manual rule the model cannot express (UFW keeps the IPv6
// host bits of 2001:db8::1/32).
const dgRuleRaw = "### tuple ### deny any any ::/0 any 2001:db8::1/32 in"

func dgLogical(t *testing.T, h *fakeHost, s fhState, sysctl map[string]string) logical {
	t.Helper()
	h.setState(s)
	return logical{cfg: h.config(), sysctl: sysctl}
}

// TestStateDiff: every change from one logical state to another is a line
// (R39): the UFW state, each removed and added rule with manual rules
// marked, a reordering, each default policy, the logging level, IPv6 and
// each sysctl value; no change is no line.
func TestStateDiff(t *testing.T) {
	h := newFakeHost(t)
	base := h.state()
	a := dgLogical(t, h, base, map[string]string{"net/ipv4/ip_forward": "0", "net/ipv4/conf/eth0/rp_filter": "1"})
	if got := stateDiff(a, a); len(got) != 0 {
		t.Errorf("diff of a state with itself = %+v, want none", got)
	}

	next := base
	next.Enabled, next.IPv6, next.LogLevel, next.Input, next.Forward = false, false, "medium", "ACCEPT", "REJECT"
	next.Rules4 = []string{fhRule3000v4, fhRule22v4, dgRule80v4}
	b := dgLogical(t, h, next, map[string]string{"net/ipv4/ip_forward": "1", "net/ipv6/conf/all/forwarding": "0"})
	want := []core.DiffLine{
		{Op: "-", Text: "UFW enabled"},
		{Op: "+", Text: "UFW disabled"},
		{Op: "+", Text: `rule 01J00000000000000000000002: allow in proto tcp from 0.0.0.0/0 to 0.0.0.0/0 port 80 comment "web"`},
		{Op: " ", Text: "rules reordered: manual-" + manualOf(t, a, 1) + ", 01J00000000000000000000001"},
		{Op: "-", Text: "default incoming DROP"},
		{Op: "+", Text: "default incoming ACCEPT"},
		{Op: "-", Text: "default routed DROP"},
		{Op: "+", Text: "default routed REJECT"},
		{Op: "-", Text: "logging low"},
		{Op: "+", Text: "logging medium"},
		{Op: "-", Text: "IPV6 yes"},
		{Op: "+", Text: "IPV6 no"},
		{Op: "-", Text: "sysctl net/ipv4/conf/eth0/rp_filter=1"},
		{Op: "-", Text: "sysctl net/ipv4/ip_forward=0"},
		{Op: "+", Text: "sysctl net/ipv4/ip_forward=1"},
		{Op: "+", Text: "sysctl net/ipv6/conf/all/forwarding=0"},
	}
	got := stateDiff(a, b)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("diff\n%+v\nwant\n%+v", got, want)
	}
}

// manualOf returns the hex digits of the id of a's rule i, which must be a
// manual rule.
func manualOf(t *testing.T, a logical, i int) string {
	t.Helper()
	r := model.Merge(a.cfg.Rules4, a.cfg.Rules6)[i]
	if r.Origin != model.OriginManual {
		t.Fatalf("rule %d is %s", i, r.Origin)
	}
	return r.ID[len("manual-"):]
}

// TestStateDiffManual: manual rules are marked and listed by
// manualChanges; an unrepresentable one shows its raw tuple; a new rule of
// a plan shows as new.
func TestStateDiffManual(t *testing.T) {
	h := newFakeHost(t)
	base := h.state()
	a := dgLogical(t, h, base, nil)
	next := base
	next.Rules4 = []string{fhRule22v4}
	next.Rules6 = []string{fhRule22v6, dgRuleRaw}
	b := dgLogical(t, h, next, nil)
	manual := model.Merge(a.cfg.Rules4, a.cfg.Rules6)[1]
	raw := model.Merge(b.cfg.Rules4, b.cfg.Rules6)[1]
	want := []core.DiffLine{
		{Op: "-", Text: "rule " + manual.ID + " (manual): allow in proto tcp from 10.0.0.0/8 to 0.0.0.0/0 port 3000"},
		{Op: "+", Text: "rule " + raw.ID + " (manual, read-only): " + dgRuleRaw},
	}
	if got := stateDiff(a, b); !reflect.DeepEqual(got, want) {
		t.Errorf("diff\n%+v\nwant\n%+v", got, want)
	}
	if got := manualChanges(a, b); !reflect.DeepEqual(got, []string{manual.ID, raw.ID}) {
		t.Errorf("manualChanges = %v, want %v", got, []string{manual.ID, raw.ID})
	}
	if got := manualChanges(a, a); got != nil {
		t.Errorf("manualChanges of no change = %v", got)
	}

	r := model.Rule{ID: planRuleID, Action: model.ActionDeny, Direction: model.DirIn, Proto: model.ProtoUDP, From: "2001:db8::/32",
		To: model.Any, ToPort: "5000:5100", Family: model.FamilyV6, Comment: "lab"}
	if got, want := ruleText(r), `rule new: deny in proto udp from 2001:db8::/32 to ::/0 port 5000:5100 comment "lab"`; got != want {
		t.Errorf("ruleText = %q, want %q", got, want)
	}
}
