package core

import "testing"

func TestDefaultScope(t *testing.T) {
	tests := []struct {
		class Class
		side  Side
		want  Scope
	}{
		{ClassRead, SideAgent, ScopeRead},
		{ClassRead, SideController, ScopeRead},
		{ClassRevertible, SideAgent, ScopeOperate},
		{ClassCompensable, SideAgent, ScopeOperate},
		{ClassIrreversible, SideAgent, ScopeOperate},
		{ClassCompensable, SideController, ScopeAdmin},
		{ClassIrreversible, SideController, ScopeAdmin},
		{ClassComposite, SideController, ScopeAdmin},
		{Class("magic"), SideAgent, ScopeAdmin},
		{ClassCompensable, Side("browser"), ScopeAdmin},
	}
	for _, tt := range tests {
		if got := DefaultScope(tt.class, tt.side); got != tt.want {
			t.Errorf("DefaultScope(%s, %s) = %s, want %s", tt.class, tt.side, got, tt.want)
		}
	}
}

func TestRequiredScope(t *testing.T) {
	tests := []struct {
		name string
		spec ActionSpec
		want Scope
	}{
		{"agent read raised to admin", ActionSpec{Class: ClassRead, Side: SideAgent, MinScope: ScopeAdmin}, ScopeAdmin},
		{"agent compensable raised to admin", ActionSpec{Class: ClassCompensable, Side: SideAgent, MinScope: ScopeAdmin}, ScopeAdmin},
		{"agent compensable without MinScope", ActionSpec{Class: ClassCompensable, Side: SideAgent}, ScopeOperate},
		{"MinScope below the default", ActionSpec{Class: ClassIrreversible, Side: SideController, MinScope: ScopeRead}, ScopeAdmin},
		{"unknown MinScope", ActionSpec{Class: ClassRead, Side: SideAgent, MinScope: "root"}, Scope("root")},
	}
	for _, tt := range tests {
		if got := RequiredScope(tt.spec); got != tt.want {
			t.Errorf("%s: RequiredScope = %s, want %s", tt.name, got, tt.want)
		}
	}
}
