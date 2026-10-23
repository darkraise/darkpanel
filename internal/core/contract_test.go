package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"reflect"
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

// declGuardedInput is an action input with guard support.
type declGuardedInput struct {
	Guarded
	Name string `json:"name"`
}

func TestGuardedJSON(t *testing.T) {
	tests := []struct {
		name string
		in   declGuardedInput
		want string
	}{
		{"filled", declGuardedInput{
			Guarded: Guarded{
				Guard:     &GuardContext{Client: &Peer{Addr: netip.MustParseAddr("192.0.2.1"), Port: 50000}},
				Overrides: []string{"x.lockout"},
			},
			Name: "n",
		}, `{"guard":{"client":{"addr":"192.0.2.1","port":50000}},"overrides":["x.lockout"],"name":"n"}`},
		{"zero", declGuardedInput{Name: "n"}, `{"name":"n"}`},
	}
	for _, tt := range tests {
		b, err := json.Marshal(tt.in)
		if err != nil || string(b) != tt.want {
			t.Errorf("%s: json.Marshal = %s, %v; want %s", tt.name, b, err, tt.want)
		}
	}
}

func TestGuardContextJSONNames(t *testing.T) {
	g := GuardContext{
		Client: &Peer{Addr: netip.MustParseAddr("2001:db8::7"), Port: 41000},
		Via: &ProxyPath{
			ExposureID:   "e1",
			Kind:         "npm",
			ProxyAddr:    netip.MustParseAddr("172.18.0.2"),
			ListenerAddr: netip.MustParseAddrPort("172.18.0.1:8443"),
			Public:       []netip.AddrPort{netip.MustParseAddrPort("203.0.113.5:443"), netip.MustParseAddrPort("[2001:db8::1]:443")},
		},
		PanelListen: []netip.AddrPort{netip.MustParseAddrPort("0.0.0.0:8443")},
		AgentListen: []netip.AddrPort{netip.MustParseAddrPort("[::]:7443")},
		AgentPeers:  []netip.Addr{netip.MustParseAddr("198.51.100.4"), netip.MustParseAddr("2001:db8::2")},
	}
	b, err := json.Marshal(g)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"client":{"addr":"2001:db8::7","port":41000},` +
		`"via":{"exposureId":"e1","kind":"npm","proxyAddr":"172.18.0.2","listenerAddr":"172.18.0.1:8443",` +
		`"public":["203.0.113.5:443","[2001:db8::1]:443"]},` +
		`"panelListen":["0.0.0.0:8443"],"agentListen":["[::]:7443"],"agentPeers":["198.51.100.4","2001:db8::2"]}`
	if string(b) != want {
		t.Errorf("json.Marshal = %s\nwant %s", b, want)
	}
	var back GuardContext
	if err := json.Unmarshal(b, &back); err != nil || !reflect.DeepEqual(back, g) {
		t.Errorf("json.Unmarshal = %+v, %v; want %+v", back, err, g)
	}
}

func TestStreamInfoJSON(t *testing.T) {
	b, err := json.Marshal(StreamInfo{Module: "docker", Name: "logs", SchemaHash: "ab"})
	if want := `{"module":"docker","name":"logs","schemaHash":"ab"}`; err != nil || string(b) != want {
		t.Errorf("json.Marshal = %s, %v; want %s", b, err, want)
	}
}

func TestGuardSerialisesOverrideFields(t *testing.T) {
	b, err := json.Marshal(Guard{ID: "a", OK: true, Overridable: true, Overridden: true})
	if want := `{"id":"a","ok":true,"message":"","overridable":true,"overridden":true}`; err != nil || string(b) != want {
		t.Errorf("json.Marshal = %s, %v; want %s", b, err, want)
	}
}
