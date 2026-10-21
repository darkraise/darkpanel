package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core/errcode"
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

func TestActionRefText(t *testing.T) {
	for _, ref := range []ActionRef{{"core.update.apply", 1}, {"tailnet.device.set-routes", 12}} {
		text, err := ref.MarshalText()
		if err != nil || string(text) != ref.String() {
			t.Errorf("%v.MarshalText() = %q, %v; want %q", ref, text, err, ref.String())
		}
		var got ActionRef
		if err := got.UnmarshalText(text); err != nil || got != ref {
			t.Errorf("UnmarshalText(%q) = %v, %v; want %v", text, got, err, ref)
		}
	}
	for _, text := range []string{"x", "x/v0", "x/1", "/v1", "a/b/vx", ""} {
		var got ActionRef
		err := got.UnmarshalText([]byte(text))
		if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("%q", text)) {
			t.Errorf("UnmarshalText(%q) = %v, %v; want an error naming the text", text, got, err)
		}
	}
}

func TestStepCarriesTheActionAsOneString(t *testing.T) {
	step := Step{ID: "join", Host: "h1", Action: ActionRef{"tailscale.node.join", 3}, Input: json.RawMessage(`{}`)}
	b, err := json.Marshal(step)
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"id":"join","host":"h1","action":"tailscale.node.join/v3","input":{}}`; string(b) != want {
		t.Errorf("json.Marshal(step) = %s, want %s", b, want)
	}
	var back Step
	if err := json.Unmarshal(b, &back); err != nil || back.Action != step.Action {
		t.Errorf("json.Unmarshal = %+v, %v; want action %v", back, err, step.Action)
	}
	if err := json.Unmarshal([]byte(`{"action":"tailscale.node.join"}`), &back); err == nil {
		t.Error("json.Unmarshal accepted an action without a version")
	}
}

func TestDefinite(t *testing.T) {
	plain := errors.New("409 conflict")
	if Definite(nil) != nil {
		t.Error("Definite(nil) is not nil")
	}
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"plain", plain, false},
		{"marked", Definite(plain), true},
		{"wrapped after marking", fmt.Errorf("create: %w", Definite(plain)), true},
	}
	for _, tt := range tests {
		if got := IsDefinite(tt.err); got != tt.want {
			t.Errorf("%s: IsDefinite = %v, want %v", tt.name, got, tt.want)
		}
	}
	if marked := Definite(plain); !errors.Is(marked, plain) || marked.Error() != plain.Error() {
		t.Errorf("Definite(err) = %v; want it to read as and wrap err", marked)
	}
	coded := errcode.New(errcode.InvalidInput, "the name is taken")
	if got := errcode.From(Definite(coded)); got != coded || got.Code != errcode.InvalidInput {
		t.Errorf("errcode.From(Definite(coded)) = %v, want the original %s error", got, errcode.InvalidInput)
	}
}
