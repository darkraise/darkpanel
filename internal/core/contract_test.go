package core

import (
	"testing"
	"time"
)

func TestScopeAllows(t *testing.T) {
	tests := []struct {
		have, need Scope
		want       bool
	}{
		{ScopeRead, ScopeRead, true},
		{ScopeRead, ScopeOperate, false},
		{ScopeOperate, ScopeRead, true},
		{ScopeOperate, ScopeAdmin, false},
		{ScopeAdmin, ScopeOperate, true},
		{Scope(""), ScopeRead, false},
		{Scope("root"), ScopeRead, false},
		{ScopeAdmin, Scope(""), false},
		{ScopeRead, Scope("root"), false},
	}
	for _, tt := range tests {
		if got := tt.have.Allows(tt.need); got != tt.want {
			t.Errorf("%q.Allows(%q) = %v, want %v", tt.have, tt.need, got, tt.want)
		}
	}
}

func TestParseVersion(t *testing.T) {
	for in, want := range map[string]int{"v1": 1, "v2": 2, "v10": 10} {
		got, err := ParseVersion(in)
		if err != nil || got != want {
			t.Errorf("ParseVersion(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, in := range []string{"", "1", "v", "v0", "v-1", "v01", "V1", "v1x"} {
		if _, err := ParseVersion(in); err == nil {
			t.Errorf("ParseVersion(%q) succeeded, want an error", in)
		}
	}
}

func TestActionRefString(t *testing.T) {
	if got := (ActionRef{"core.token.create", 1}).String(); got != "core.token.create/v1" {
		t.Errorf("String() = %q", got)
	}
}

func TestSystemClock(t *testing.T) {
	var c Clock = SystemClock{}
	if d := time.Since(c.Now()); d < 0 || d > time.Minute {
		t.Errorf("Now() is %v away from time.Now()", d)
	}
	fired := make(chan struct{})
	c.AfterFunc(time.Millisecond, func() { close(fired) })
	select {
	case <-fired:
	case <-time.After(5 * time.Second):
		t.Fatal("AfterFunc did not fire within 5s")
	}
	stopped := c.AfterFunc(time.Hour, func() { t.Error("stopped timer fired") })
	if !stopped.Stop() {
		t.Error("Stop() = false for a pending timer")
	}
}
