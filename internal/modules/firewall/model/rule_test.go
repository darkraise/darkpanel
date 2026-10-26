package model

import (
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/schema"
)

const ruleTestID = "01J00000000000000000000000"

// ruleTestBase is a valid rule each case of TestRuleValidate edits.
func ruleTestBase() Rule {
	return Rule{ID: ruleTestID, Action: ActionAllow, Direction: DirIn, Proto: ProtoTCP,
		From: Any, To: Any, ToPort: "22", Family: FamilyBoth, Log: LogOff, Origin: OriginDarkpanel}
}

func TestRuleValidate(t *testing.T) {
	cases := []struct {
		name  string
		edit  func(r *Rule)
		paths []string // nil: valid
	}{
		{"spec example ssh", func(*Rule) {}, nil},
		{"spec example v6 udp range", func(r *Rule) {
			r.Action, r.Proto, r.From, r.ToPort, r.Family = ActionDeny, ProtoUDP, "2001:db8::/32", "5000:5100", FamilyV6
		}, nil},
		{"spec example route", func(r *Rule) {
			r.Direction, r.To, r.ToPort, r.OutIface, r.Family = DirRoute, "172.17.0.2", "80", "docker0", FamilyV4
		}, nil},
		{"empty id, log and origin", func(r *Rule) { r.ID, r.Log, r.Origin = "", "", "" }, nil},
		{"manual id", func(r *Rule) { r.ID, r.Origin = "manual-0123456789abcdef", OriginManual }, nil},
		{"port list at 15 slots", func(r *Rule) { r.ToPort = "1:2,3:4,5:6,7:8,9:10,11:12,13:14,15" }, nil},
		{"port list over 15 slots", func(r *Rule) { r.ToPort = "1:2,3:4,5:6,7:8,9:10,11:12,13:14,15:16" }, []string{"/toPort"}},
		{"both ports", func(r *Rule) { r.FromPort = "1024:2048" }, nil},
		{"single port with any", func(r *Rule) { r.Proto = ProtoAny }, nil},
		{"list with any", func(r *Rule) { r.Proto, r.ToPort = ProtoAny, "80,443" }, []string{"/toPort"}},
		{"range with any", func(r *Rule) { r.Proto, r.FromPort = ProtoAny, "1:2" }, []string{"/fromPort"}},
		{"port-less protocol with port", func(r *Rule) { r.Proto = ProtoESP }, []string{"/toPort"}},
		{"port-less protocol", func(r *Rule) { r.Proto, r.ToPort = ProtoGRE, "" }, nil},
		{"ipv6 protocol on v4", func(r *Rule) { r.Proto, r.ToPort, r.Family = ProtoIPv6, "", FamilyV4 }, nil},
		{"ipv6 protocol on both", func(r *Rule) { r.Proto, r.ToPort = ProtoIPv6, "" }, []string{"/proto"}},
		{"igmp on v6", func(r *Rule) { r.Proto, r.ToPort, r.Family = ProtoIGMP, "", FamilyV6 }, []string{"/proto"}},
		{"port zero", func(r *Rule) { r.ToPort = "0" }, []string{"/toPort"}},
		{"port too high", func(r *Rule) { r.ToPort = "65536" }, []string{"/toPort"}},
		{"leading zero", func(r *Rule) { r.ToPort = "022" }, []string{"/toPort"}},
		{"reversed range", func(r *Rule) { r.ToPort = "100:90" }, []string{"/toPort"}},
		{"empty range", func(r *Rule) { r.ToPort = "90:90" }, []string{"/toPort"}},
		{"service name", func(r *Rule) { r.ToPort = "ssh" }, []string{"/toPort"}},
		{"trailing comma", func(r *Rule) { r.ToPort = "22," }, []string{"/toPort"}},
		{"unsorted list", func(r *Rule) { r.ToPort = "443,80" }, []string{"/toPort"}},
		{"v4 address on v6", func(r *Rule) { r.From, r.Family = "10.0.0.0/8", FamilyV6 }, []string{"/from"}},
		{"address on both", func(r *Rule) { r.To = "10.0.0.1" }, []string{"/family"}},
		{"bad address", func(r *Rule) { r.From, r.Family = "10.0.0.300", FamilyV4 }, []string{"/from"}},
		{"host bits", func(r *Rule) { r.From, r.Family = "10.0.0.1/8", FamilyV4 }, []string{"/from"}},
		{"host prefix length", func(r *Rule) { r.To, r.Family = "10.0.0.1/32", FamilyV4 }, []string{"/to"}},
		{"zero prefix", func(r *Rule) { r.To, r.Family = "0.0.0.0/0", FamilyV4 }, []string{"/to"}},
		{"zone", func(r *Rule) { r.To, r.Family = "fe80::1%eth0", FamilyV6 }, []string{"/to"}},
		{"long v6 form", func(r *Rule) { r.To, r.Family = "2001:0db8::1", FamilyV6 }, []string{"/to"}},
		{"limit out", func(r *Rule) { r.Action, r.Direction = ActionLimit, DirOut }, []string{"/action"}},
		{"limit route", func(r *Rule) { r.Action, r.Direction = ActionLimit, DirRoute }, []string{"/action"}},
		{"in-interface on out", func(r *Rule) { r.Direction, r.InIface = DirOut, "eth0" }, []string{"/inIface"}},
		{"out-interface on in", func(r *Rule) { r.OutIface = "eth0" }, []string{"/outIface"}},
		{"both interfaces on route", func(r *Rule) { r.Direction, r.InIface, r.OutIface = DirRoute, "eth0", "eth1+" }, nil},
		{"interface too long", func(r *Rule) { r.InIface = "abcdefghijklmnop" }, []string{"/inIface"}},
		{"interface with colon", func(r *Rule) { r.InIface = "eth0:1" }, []string{"/inIface"}},
		{"interface leading dash", func(r *Rule) { r.InIface = "-eth0" }, []string{"/inIface"}},
		{"comment ok", func(r *Rule) { r.Comment = "ssh from office 10.0.0.0/8" }, nil},
		{"comment quote", func(r *Rule) { r.Comment = "it's" }, []string{"/comment"}},
		{"comment too long", func(r *Rule) { r.Comment = strings.Repeat("a", 65) }, []string{"/comment"}},
		{"comment edge space", func(r *Rule) { r.Comment = "ssh " }, []string{"/comment"}},
		{"bad action", func(r *Rule) { r.Action = "accept" }, []string{"/action"}},
		{"bad direction", func(r *Rule) { r.Direction = "forward" }, []string{"/direction"}},
		{"bad proto", func(r *Rule) { r.Proto = "icmp" }, []string{"/proto"}},
		{"bad family", func(r *Rule) { r.Family = "v5" }, []string{"/family"}},
		{"bad log", func(r *Rule) { r.Log = "all" }, []string{"/log"}},
		{"bad origin", func(r *Rule) { r.Origin = "docker" }, []string{"/origin"}},
		{"bad id", func(r *Rule) { r.ID = "01j00000000000000000000000" }, []string{"/id"}},
		{"manual id on darkpanel rule", func(r *Rule) { r.ID = "manual-0123456789abcdef" }, []string{"/id"}},
		{"raw", func(r *Rule) { r.Raw = "### tuple ### allow any any 0.0.0.0/0 any 0.0.0.0/0 in" }, []string{"/raw"}},
		{"several", func(r *Rule) { r.Proto, r.Family = "icmp", "v5" }, []string{"/proto", "/family"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := ruleTestBase()
			c.edit(&r)
			err := r.Validate()
			if c.paths == nil {
				if err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}
				return
			}
			var e *errcode.Error
			if !errors.As(err, &e) || e.Code != errcode.InvalidInput {
				t.Fatalf("Validate() = %v, want %s", err, errcode.InvalidInput)
			}
			var got []string
			for _, f := range e.Fields {
				if f.Message == "" {
					t.Errorf("field %s has no message", f.Path)
				}
				got = append(got, f.Path)
			}
			if !slices.Equal(got, c.paths) {
				t.Fatalf("field paths = %q, want %q (%v)", got, c.paths, e.Fields)
			}
		})
	}
}

func TestRuleNormalize(t *testing.T) {
	in := Rule{Action: ActionAllow, Direction: DirIn, Proto: ProtoTCP, From: "10.1.2.3/8", FromPort: "8000:8100,443,80",
		To: "2001:0db8:0:0::1/128", ToPort: "22", Family: FamilyV4}
	want := Rule{Action: ActionAllow, Direction: DirIn, Proto: ProtoTCP, From: "10.0.0.0/8", FromPort: "80,443,8000:8100",
		To: "2001:db8::1", ToPort: "22", Family: FamilyV4, Log: LogOff, Origin: OriginDarkpanel}
	if got := in.Normalize(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Normalize() = %+v, want %+v", got, want)
	}
	zero := Rule{From: "::/0", To: "0.0.0.0/0", ToPort: "x,1", Log: LogAll, Origin: OriginManual}
	wantZero := Rule{From: Any, To: Any, ToPort: "x,1", Log: LogAll, Origin: OriginManual}
	if got := zero.Normalize(); !reflect.DeepEqual(got, wantZero) {
		t.Fatalf("Normalize() = %+v, want %+v", got, wantZero)
	}
	if got := sortPorts("90:100,90,1000,100"); got != "90,90:100,100,1000" {
		t.Fatalf("sortPorts = %q", got)
	}
}

func TestRuleSchema(t *testing.T) {
	raw, err := schema.Input(reflect.TypeFor[Rule]())
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Defs map[string]struct {
			Required   []string `json:"required"`
			Properties map[string]struct {
				Enum     []string `json:"enum"`
				Pattern  string   `json:"pattern"`
				ReadOnly bool     `json:"readOnly"`
			} `json:"properties"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	def, ok := doc.Defs["Rule"]
	if !ok {
		t.Fatalf("schema has no Rule definition: %s", raw)
	}
	if want := []string{"action", "direction", "proto", "from", "to", "family"}; !slices.Equal(def.Required, want) {
		t.Errorf("required = %q, want %q", def.Required, want)
	}
	enums := map[string][]string{
		"action": {"allow", "deny", "limit", "reject"}, "direction": {"in", "out", "route"},
		"proto": ruleTestStrings(Protos()), "family": {"v4", "v6", "both"}, "log": {"off", "log", "log-all"},
		"origin": {"darkpanel", "manual"},
	}
	for name, want := range enums {
		if got := def.Properties[name].Enum; !slices.Equal(got, want) {
			t.Errorf("%s enum = %q, want %q", name, got, want)
		}
	}
	patterns := map[string]string{"id": idPattern, "from": addrPattern, "to": addrPattern, "fromPort": portPattern,
		"toPort": portPattern, "inIface": ifacePattern, "outIface": ifacePattern, "comment": commentPattern}
	for name, want := range patterns {
		if got := def.Properties[name].Pattern; got != want {
			t.Errorf("%s pattern = %q, want %q", name, got, want)
		}
	}
	if !def.Properties["raw"].ReadOnly {
		t.Error("raw is not readOnly")
	}

	v := schema.NewValidator()
	ok1 := `{"action":"allow","direction":"in","proto":"tcp","from":"any","to":"any","toPort":"22","family":"both"}`
	if err := v.ValidateType(reflect.TypeFor[Rule](), json.RawMessage(ok1)); err != nil {
		t.Fatalf("schema refused a valid rule: %v", err)
	}
	for _, bad := range []string{
		`{"action":"accept","direction":"in","proto":"tcp","from":"any","to":"any","family":"both"}`,
		`{"action":"allow","direction":"in","proto":"icmp","from":"any","to":"any","family":"both"}`,
		`{"action":"allow","direction":"in","proto":"tcp","from":"any","to":"any","toPort":"0","family":"both"}`,
		`{"action":"allow","direction":"in","proto":"tcp","from":"any","to":"any","inIface":"-i","family":"both"}`,
		`{"action":"allow","direction":"in","proto":"tcp","from":"--x","to":"any","family":"both"}`,
		`{"action":"allow","direction":"in","proto":"tcp","from":"any","to":"any","comment":"a'b","family":"both"}`,
		`{"action":"allow","direction":"in","proto":"tcp","from":"any","family":"both"}`,
	} {
		if err := v.ValidateType(reflect.TypeFor[Rule](), json.RawMessage(bad)); err == nil {
			t.Errorf("schema accepted %s", bad)
		}
	}
}

func ruleTestStrings(ps []Proto) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = string(p)
	}
	return out
}

func TestTag(t *testing.T) {
	if got := Tag(ruleTestID, ""); got != "darkpanel:"+ruleTestID {
		t.Errorf("Tag without label = %q", got)
	}
	if got := Tag(ruleTestID, "ssh from office"); got != "darkpanel:"+ruleTestID+" ssh from office" {
		t.Errorf("Tag with label = %q", got)
	}
	cases := []struct {
		comment, id, label string
		ok                 bool
	}{
		{"darkpanel:" + ruleTestID, ruleTestID, "", true},
		{"darkpanel:" + ruleTestID + " ssh", ruleTestID, "ssh", true},
		{"darkpanel:" + ruleTestID + "  two", ruleTestID, " two", true},
		{"darkpanel:" + ruleTestID + "x", "", "", false},
		{"darkpanel:01J0000000000000000000000", "", "", false},
		{"darkpanel:01J0000000000000000000000U", "", "", false},
		{"Darkpanel:" + ruleTestID, "", "", false},
		{"hand rule", "", "", false},
		{"", "", "", false},
	}
	for _, c := range cases {
		id, label, ok := ParseTag(c.comment)
		if id != c.id || label != c.label || ok != c.ok {
			t.Errorf("ParseTag(%q) = %q, %q, %v; want %q, %q, %v", c.comment, id, label, ok, c.id, c.label, c.ok)
		}
	}
}
