package core

import "testing"

func TestAccessOn(t *testing.T) {
	cases := []struct {
		name   string
		access Access
		host   string
		want   Scope
	}{
		{"host grant above all", Access{All: ScopeRead, Hosts: map[string]Scope{"h1": ScopeOperate}}, "h1", ScopeOperate},
		{"all grant elsewhere", Access{All: ScopeRead, Hosts: map[string]Scope{"h1": ScopeOperate}}, "h2", ScopeRead},
		{"all grant above host grant", Access{All: ScopeOperate, Hosts: map[string]Scope{"h1": ScopeRead}}, "h1", ScopeOperate},
		{"outside the grants", Access{Hosts: map[string]Scope{"h1": ScopeRead}}, "h2", ""},
		{"no grants", Access{}, "h1", ""},
		{"admin", Access{Admin: true}, "x", ScopeAdmin},
		{"admin ignores the ceiling", Access{Admin: true, Ceil: map[string]Scope{"c": ScopeRead}}, "c", ScopeAdmin},
		{"ceiling lowers all", Access{All: ScopeOperate, Ceil: map[string]Scope{"c": ScopeRead}}, "c", ScopeRead},
		{"ceiling lowers a host grant", Access{Hosts: map[string]Scope{"c": ScopeOperate}, Ceil: map[string]Scope{"c": ScopeRead}}, "c", ScopeRead},
		{"ceiling elsewhere", Access{All: ScopeOperate, Ceil: map[string]Scope{"c": ScopeRead}}, "h", ScopeOperate},
		{"ceiling grants nothing", Access{Ceil: map[string]Scope{"c": ScopeRead}}, "c", ""},
	}
	for _, c := range cases {
		if got := c.access.On(c.host); got != c.want {
			t.Errorf("%s: On(%q) = %q, want %q", c.name, c.host, got, c.want)
		}
	}
}

func TestAccessAllHosts(t *testing.T) {
	ceil := map[string]Scope{"c": ScopeRead}
	cases := []struct {
		name   string
		access Access
		need   Scope
		want   bool
	}{
		{"admin, admin", Access{Admin: true}, ScopeAdmin, true},
		{"admin under a ceiling", Access{Admin: true, Ceil: ceil}, ScopeOperate, true},
		{"operate, read", Access{All: ScopeOperate}, ScopeRead, true},
		{"operate, operate", Access{All: ScopeOperate}, ScopeOperate, true},
		{"operate, admin", Access{All: ScopeOperate}, ScopeAdmin, false},
		{"capped operate, read", Access{All: ScopeOperate, Ceil: ceil}, ScopeRead, true},
		{"capped operate, operate", Access{All: ScopeOperate, Ceil: ceil}, ScopeOperate, false},
		{"host grants only, read", Access{Hosts: map[string]Scope{"h1": ScopeOperate}}, ScopeRead, false},
		{"nothing, read", Access{}, ScopeRead, false},
	}
	for _, c := range cases {
		if got := c.access.AllHosts(c.need); got != c.want {
			t.Errorf("%s: AllHosts(%q) = %v, want %v", c.name, c.need, got, c.want)
		}
	}
}
