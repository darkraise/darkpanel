package dockerfw

import (
	"encoding/json"
	"net/netip"
	"slices"
	"strings"
	"testing"
)

const bdULID = "01J00000000000000000000001"

func bdRule(edit ...func(*ContainerRule)) ContainerRule {
	r := ContainerRule{ID: bdULID, Binding: bdBinding(), Kind: Allow}
	for _, e := range edit {
		e(&r)
	}
	return r
}

func TestBdRuleValidate(t *testing.T) {
	deny := func(from string) func(*ContainerRule) {
		return func(r *ContainerRule) {
			r.Kind = Deny
			if from != "" {
				r.From = netip.MustParsePrefix(from)
			}
		}
	}
	cases := []struct {
		name    string
		private []string // nil: the default privateSources
		edit    func(*ContainerRule)
		paths   []string
		msg     string // a substring of the first field's message
	}{
		{"an allowance from any source", nil, func(*ContainerRule) {}, nil, ""},
		{"an allowance from a range", nil, func(r *ContainerRule) { r.From = netip.MustParsePrefix("203.0.113.0/24") }, nil, ""},
		{"an allowance from inside a private range", nil, func(r *ContainerRule) { r.From = netip.MustParsePrefix("10.1.0.0/16") }, nil, ""},
		{"a deny", nil, deny("203.0.113.0/24"), nil, ""},
		{"an IPv6 deny", nil, deny("2001:db8::/32"), nil, ""},
		{"a deny of every IPv4 source", nil, deny("0.0.0.0/0"), nil, ""},
		{"a deny that contains a private range", nil, deny("10.0.0.0/7"), nil, ""},
		{"a deny outside a custom private range", []string{"198.51.100.0/24"}, deny("203.0.113.0/24"), nil, ""},
		{"a deny on one host address", nil, func(r *ContainerRule) {
			r.Kind, r.From = Deny, netip.MustParsePrefix("203.0.113.0/24")
			r.Binding.HostAddr = netip.MustParseAddr("192.0.2.10")
		}, nil, ""},
		{"a deny without a source", nil, deny(""), []string{"/from"}, "source"},
		{"an unmasked allowance", nil, func(r *ContainerRule) { r.From = netip.MustParsePrefix("203.0.113.5/24") }, []string{"/from"}, "203.0.113.0/24"},
		{"an unmasked deny", nil, deny("203.0.113.5/24"), []string{"/from"}, "203.0.113.0/24"},
		{"an invalid prefix", nil, func(r *ContainerRule) { r.From = netip.PrefixFrom(netip.MustParseAddr("203.0.113.0"), 99) }, []string{"/from"}, ""},
		{"a deny inside 10.0.0.0/8", nil, deny("10.1.0.0/16"), []string{"/from"}, "10.0.0.0/8"},
		{"a deny equal to a private range", nil, deny("100.64.0.0/10"), []string{"/from"}, "100.64.0.0/10"},
		{"a deny inside 192.168.0.0/16", nil, deny("192.168.1.0/24"), []string{"/from"}, "192.168.0.0/16"},
		{"a deny inside fc00::/7", nil, deny("fd00::/8"), []string{"/from"}, "fc00::/7"},
		{"a deny equal to fe80::/10", nil, deny("fe80::/10"), []string{"/from"}, "fe80::/10"},
		{"a deny inside a custom private range", []string{"198.51.100.0/24"}, deny("198.51.100.128/25"), []string{"/from"}, "198.51.100.0/24"},
		{"no kind", nil, func(r *ContainerRule) { r.Kind = "" }, []string{"/kind"}, ""},
		{"an unknown kind", nil, func(r *ContainerRule) { r.Kind = "block" }, []string{"/kind"}, ""},
		{"a binding without a selector", nil, func(r *ContainerRule) { r.Binding.Selector = "" }, []string{"/binding/selector"}, ""},
		{"a binding on port 0", nil, func(r *ContainerRule) { r.Binding.HostPort = 0 }, []string{"/binding/hostPort"}, ""},
		{"an unspecified IPv4 host address", nil, func(r *ContainerRule) { r.Binding.HostAddr = netip.MustParseAddr("0.0.0.0") }, []string{"/binding/hostAddr"}, ""},
		{"an unspecified IPv6 host address", nil, func(r *ContainerRule) { r.Binding.HostAddr = netip.MustParseAddr("::") }, []string{"/binding/hostAddr"}, ""},
		{"every part", nil, func(r *ContainerRule) {
			r.Binding.Selector, r.Kind = "", "block"
			r.From = netip.MustParsePrefix("203.0.113.5/24")
		}, []string{"/binding/selector", "/kind", "/from"}, ""},
		{"a deny with a bad selector and no source", nil, func(r *ContainerRule) {
			r.Binding.Selector, r.Kind = "", Deny
		}, []string{"/binding/selector", "/from"}, ""},
	}
	for _, c := range cases {
		s := DefaultSettings()
		if c.private != nil {
			s.PrivateSources = nil
			for _, p := range c.private {
				s.PrivateSources = append(s.PrivateSources, netip.MustParsePrefix(p))
			}
		}
		err := bdRule(c.edit).Validate(s)
		if c.paths == nil {
			if err != nil {
				t.Errorf("%s: Validate() = %v, want nil", c.name, err)
			}
			continue
		}
		fields := bdFields(t, err)
		var got []string
		for _, f := range fields {
			got = append(got, f.Path)
		}
		if !slices.Equal(got, c.paths) {
			t.Errorf("%s: field paths = %q, want %q", c.name, got, c.paths)
			continue
		}
		if !strings.Contains(fields[0].Message, c.msg) {
			t.Errorf("%s: message %q, want it to contain %q", c.name, fields[0].Message, c.msg)
		}
	}
}

func TestBdRuleOf(t *testing.T) {
	if LabelPrefix != "ctr " {
		t.Errorf("LabelPrefix = %q, want %q", LabelPrefix, "ctr ")
	}
	if id, ok := RuleOf(LabelPrefix + bdULID); !ok || id != bdULID {
		t.Errorf("RuleOf(%q) = %q, %t, want %q, true", LabelPrefix+bdULID, id, ok, bdULID)
	}
	for _, label := range []string{
		"",
		"ctr",
		"ctr ",
		"ctr 01J0000000000000000000000",
		"ctr 01J000000000000000000000001",
		"ctr 01j00000000000000000000001",
		"ctr 01J0000000000000000000000I",
		"ctr  01J00000000000000000000001",
		"ctr 01J00000000000000000000001 ",
		"ctr 01J00000000000000000000001 extra",
		"CTR 01J00000000000000000000001",
		"manual 01J00000000000000000000001",
		bdULID,
	} {
		if id, ok := RuleOf(label); ok || id != "" {
			t.Errorf("RuleOf(%q) = %q, %t, want no rule", label, id, ok)
		}
	}
}

func TestBdRuleJSON(t *testing.T) {
	cases := []struct {
		rule ContainerRule
		json string
	}{
		{bdRule(), `{"id":"01J00000000000000000000001","binding":{"selector":"web","hostPort":8080,"proto":"tcp"},"kind":"allow"}`},
		{bdRule(func(r *ContainerRule) {
			r.ID, r.Kind, r.From = "01J00000000000000000000002", Deny, netip.MustParsePrefix("203.0.113.0/24")
			r.Binding.HostAddr = netip.MustParseAddr("192.0.2.10")
		}), `{"id":"01J00000000000000000000002","binding":{"selector":"web","hostAddr":"192.0.2.10","hostPort":8080,"proto":"tcp"},"kind":"deny","from":"203.0.113.0/24"}`},
	}
	for _, c := range cases {
		b, err := json.Marshal(c.rule)
		if err != nil || string(b) != c.json {
			t.Errorf("Marshal = %s, %v, want %s", b, err, c.json)
		}
		var back ContainerRule
		if err := json.Unmarshal([]byte(c.json), &back); err != nil || back != c.rule {
			t.Errorf("Unmarshal(%s) = %+v, %v, want %+v", c.json, back, err, c.rule)
		}
	}
}
