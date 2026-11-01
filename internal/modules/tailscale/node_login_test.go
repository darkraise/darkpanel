package tailscale

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/tailscale/link"
	"github.com/darkraise/darkpanel/internal/platform/tslocal"
)

const liKey = "tskey-auth-kABCDEF1CNTRL-0123456789abcdefghij"

func liStatus(state string) *tslocal.Status {
	st := &tslocal.Status{BackendState: state}
	if state != "NeedsLogin" && state != "NoState" {
		st.Self = &tslocal.PeerStatus{ID: "n1", HostName: "host-1", DNSName: "host-1.tail1234.ts.net."}
		st.TailscaleIPs = []netip.Addr{netip.MustParseAddr("fd7a:115c:a1e0::7"), netip.MustParseAddr("100.64.0.7")}
	}
	return st
}

// liFlow makes the fake tailscaled behave as the real one does around a
// login: Start moves the node to Starting, which becomes Running at the
// status read numbered after+1 since Start (never when after is negative);
// Logout moves the node to NeedsLogin.
type liFlow struct {
	h       *tsHost
	after   int
	started bool
	polls   int
}

type liLocal struct {
	localAPI
	f *liFlow
}

func (l liLocal) Start(ctx context.Context, o tslocal.StartOptions) error {
	if err := l.localAPI.Start(ctx, o); err != nil {
		return err
	}
	l.f.started = true
	l.f.h.local.Status = liStatus("Starting")
	return nil
}

func (l liLocal) Status(ctx context.Context) (*tslocal.Status, error) {
	if l.f.started {
		l.f.polls++
		if l.f.after >= 0 && l.f.polls == l.f.after+1 {
			l.f.h.local.Status = liStatus("Running")
		}
	}
	return l.localAPI.Status(ctx)
}

func (l liLocal) Logout(ctx context.Context) error {
	if err := l.localAPI.Logout(ctx); err != nil {
		return err
	}
	l.f.h.local.Status = liStatus("NeedsLogin")
	return nil
}

// liSetup is a node in the backend state, behind a ts whose tailscaled
// follows the flow (after: the status reads a login takes) and whose host
// name is host-1.
func liSetup(t *testing.T, state string, prefs tslocal.Prefs, after int) (*tsHost, ts) {
	t.Helper()
	h := newTSHost(t)
	h.local.Status = liStatus(state)
	h.local.Prefs = &prefs
	if h.local.Errs == nil {
		h.local.Errs = map[string]error{}
	}
	if h.units.States == nil {
		h.units.States = map[string]core.UnitState{}
	}
	h.units.States[tailscaledUnit] = core.UnitState{LoadState: "loaded", ActiveState: "active", SubState: "running"}
	h.units.States[controllerUnit] = core.UnitState{LoadState: "not-found", ActiveState: "inactive"}
	f := &liFlow{h: h, after: after}
	tt := h.ts()
	inner := tt.d.local
	tt.d.local = func() localAPI { return liLocal{localAPI: inner(), f: f} }
	tt.d.hostname = func() (string, error) { return "host-1", nil }
	return h, tt
}

func liEffects(t *testing.T, h *tsHost) []core.Effect {
	t.Helper()
	effects, err := h.env("c1").Effects.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return effects
}

// liSame compares two JSON documents as values.
func liSame(t *testing.T, a, b []byte) bool {
	t.Helper()
	var va, vb any
	if err := json.Unmarshal(a, &va); err != nil {
		t.Fatalf("document %s: %v", a, err)
	}
	if err := json.Unmarshal(b, &vb); err != nil {
		t.Fatalf("document %s: %v", b, err)
	}
	return reflect.DeepEqual(va, vb)
}

// liDrive runs call while advancing the host's clock whenever it waits.
func liDrive(t *testing.T, h *tsHost, call func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		call()
	}()
	for deadline := time.Now().Add(10 * time.Second); ; {
		select {
		case <-done:
			return
		default:
		}
		if h.clock.Pending() > 0 {
			h.clock.Advance(nodeLoginPoll)
		} else {
			time.Sleep(time.Millisecond)
		}
		if time.Now().After(deadline) {
			t.Fatal("the call did not finish")
		}
	}
}

func liLoginIn(extra string) json.RawMessage {
	return json.RawMessage(`{"authKey":"` + liKey + `"` + extra + `}`)
}

func TestLiActionDefinitions(t *testing.T) {
	_, tt := liSetup(t, "Running", tslocal.Prefs{}, 0)
	login, logout := tt.nodeLogin(), tt.nodeLogout()
	for _, tc := range []struct {
		def   core.ActionDef
		name  string
		class core.Class
		in    reflect.Type
		out   reflect.Type
	}{
		{login, "tailscale.node.login", core.ClassCompensable, reflect.TypeFor[LoginInput](), reflect.TypeFor[LoginResult]()},
		{logout, "tailscale.node.logout", core.ClassIrreversible, reflect.TypeFor[LogoutInput](), reflect.TypeFor[LogoutResult]()},
	} {
		s := tc.def.Spec
		if s.Name != tc.name || s.Version != 1 || s.Class != tc.class || s.Side != core.SideAgent || !slices.Equal(s.LockKeys, []string{lockKey}) ||
			s.Available == nil || s.Task || s.LongRunning || s.Internal {
			t.Errorf("%s: spec %+v, want a synchronous %s agent action locking %q with an availability check", tc.name, s, tc.class, lockKey)
		}
		if want := []core.GuardSpec{{ID: link.GuardID, Overridable: true}}; !reflect.DeepEqual(s.Guards, want) {
			t.Errorf("%s: guards %+v, want %+v", tc.name, s.Guards, want)
		}
		if tc.def.InputType != tc.in || tc.def.OutputType != tc.out {
			t.Errorf("%s: input %v and output %v, want %v and %v", tc.name, tc.def.InputType, tc.def.OutputType, tc.in, tc.out)
		}
		if h := tc.def.Handler; h.Plan == nil || h.Apply == nil || h.Verify == nil || h.Snapshot != nil || h.Revert != nil {
			t.Errorf("%s: handler lacks Plan, Apply or Verify, or has a Snapshot or Revert", tc.name)
		}
	}
	if h := login.Handler; h.Observe == nil || h.Inverse == nil {
		t.Error("login lacks Observe or Inverse, the undo handler of a compensable action")
	}
	if h := logout.Handler; h.Observe != nil || h.Inverse != nil {
		t.Error("logout has an Observe or Inverse, but an irreversible action journals nothing")
	}
}

func TestLiLoginPlan(t *testing.T) {
	line := func(op, text string) core.DiffLine { return core.DiffLine{Op: op, Text: text} }
	tests := []struct {
		name  string
		state string
		prefs tslocal.Prefs
		in    string
		want  []core.DiffLine
		guard bool
	}{
		{"a logged out node", "NeedsLogin", tslocal.Prefs{}, ``,
			[]core.DiffLine{line("-", "node: logged out"), line("+", "node: logged in with an auth key")}, true},
		{"hostname and tags", "NeedsLogin", tslocal.Prefs{}, `,"hostname":"web-1","advertiseTags":["tag:server","tag:web"]`,
			[]core.DiffLine{line("-", "node: logged out"), line("+", "node: logged in with an auth key"),
				line("+", "hostname: web-1"), line("+", "advertiseTags: tag:server, tag:web")}, true},
		{"a login again", "Running", tslocal.Prefs{WantRunning: true}, `,"relogin":true`,
			[]core.DiffLine{line("-", "node: logged in as host-1.tail1234.ts.net"), line("+", "node: logged in again with an auth key")}, true},
		{"a login again while Tailscale SSH may be the access path", "Running", tslocal.Prefs{WantRunning: true, RunSSH: true}, `,"relogin":true`,
			[]core.DiffLine{line("-", "node: logged in as host-1.tail1234.ts.net"), line("+", "node: logged in again with an auth key")}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, tt := liSetup(t, tc.state, tc.prefs, 0)
			out, err := tt.nodeLogin().Handler.Plan(context.Background(), h.env(""), liLoginIn(tc.in))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(out.Diff, tc.want) {
				t.Errorf("diff %+v, want %+v", out.Diff, tc.want)
			}
			if len(out.Guards) != 1 || out.Guards[0].ID != link.GuardID || out.Guards[0].OK != tc.guard {
				t.Errorf("guards %+v, want %s with OK %v", out.Guards, link.GuardID, tc.guard)
			}
			if b, _ := json.Marshal(out); len(out.StateHash) != 64 || out.Confirmation != "" || strings.Contains(string(b), liKey) {
				t.Errorf("plan %s, want a 64-digit hash, no confirmation and no auth key", b)
			}
			if len(h.local.Starts) != 0 {
				t.Error("plan logged the node in")
			}
		})
	}
}

func TestLiLoginRefusals(t *testing.T) {
	tests := []struct {
		name, state, in string
		code            errcode.Code
		path            string
		planOnly        bool // the engine plans again before it applies, so apply does not repeat a state check
	}{
		{"no auth key", "NeedsLogin", `{}`, errcode.InvalidInput, "/authKey", false},
		{"an auth key with a space", "NeedsLogin", `{"authKey":"tskey-auth-a b"}`, errcode.InvalidInput, "/authKey", false},
		{"a host name with a dot", "NeedsLogin", `{"authKey":"` + liKey + `","hostname":"web.1"}`, errcode.InvalidInput, "/hostname", false},
		{"a tag without the prefix", "NeedsLogin", `{"authKey":"` + liKey + `","advertiseTags":["tag:ok","server"]}`, errcode.InvalidInput, "/advertiseTags/1", false},
		{"a node that is logged in", "Running", `{"authKey":"` + liKey + `"}`, errcode.InvalidInput, "/relogin", true},
		{"a node whose backend has not started", "NoState", `{"authKey":"` + liKey + `"}`, errcode.TailscaleUnavailable, "", false},
	}
	for _, tc := range tests {
		for _, phase := range []string{"plan", "apply"} {
			if phase == "apply" && tc.planOnly {
				continue
			}
			t.Run(tc.name+" at "+phase, func(t *testing.T) {
				h, tt := liSetup(t, tc.state, tslocal.Prefs{}, 0)
				def, ctx, raw := tt.nodeLogin(), context.Background(), json.RawMessage(tc.in)
				var err error
				if phase == "plan" {
					_, err = def.Handler.Plan(ctx, h.env(""), raw)
				} else {
					_, err = def.Handler.Apply(ctx, h.env("c1"), raw)
				}
				e := errcode.From(err)
				if e.Code != tc.code || !core.IsDefinite(err) {
					t.Fatalf("err = %v (definite %v), want %s marked definite", err, core.IsDefinite(err), tc.code)
				}
				if tc.path != "" && (len(e.Fields) != 1 || e.Fields[0].Path != tc.path) {
					t.Errorf("fields %+v, want one at %q", e.Fields, tc.path)
				}
				if len(liEffects(t, h)) != 0 || len(h.local.Starts) != 0 {
					t.Errorf("effects %v and starts %v, want none", liEffects(t, h), h.local.Starts)
				}
			})
		}
	}
}

func TestLiLoginApply(t *testing.T) {
	old := tslocal.Prefs{LoggedOut: true, ShieldsUp: true, Hostname: "old", AdvertiseTags: []string{"tag:old"}}
	tests := []struct {
		name     string
		in       string
		hostname string
		tags     []string
	}{
		{"with a host name and tags", `,"hostname":"web-1","advertiseTags":["tag:server","tag:web"]`, "web-1", []string{"tag:server", "tag:web"}},
		{"keeping the host name and tags", ``, "old", []string{"tag:old"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, tt := liSetup(t, "NeedsLogin", old, 0)
			raw, err := tt.nodeLogin().Handler.Apply(context.Background(), h.env("c1"), liLoginIn(tc.in))
			if err != nil {
				t.Fatal(err)
			}
			if len(h.local.Starts) != 1 || h.local.Starts[0].AuthKey != liKey || h.local.Starts[0].UpdatePrefs == nil {
				t.Fatalf("starts %+v, want one with the auth key and the preferences", h.local.Starts)
			}
			if h.local.LoginInteractives != 1 {
				t.Errorf("%d login-interactive calls, want one after Start: Start alone does not log in a node without a node key", h.local.LoginInteractives)
			}
			p := h.local.Starts[0].UpdatePrefs
			if !p.WantRunning || p.LoggedOut || !p.ShieldsUp || p.Hostname != tc.hostname || !slices.Equal(p.AdvertiseTags, tc.tags) {
				t.Errorf("start preferences %+v, want running and logged in, shields up kept, host name %q, tags %v", *p, tc.hostname, tc.tags)
			}
			var res LoginResult
			if err := json.Unmarshal(raw, &res); err != nil {
				t.Fatal(err)
			}
			if want := (LoginResult{BackendState: "Running", DNSName: "host-1.tail1234.ts.net", Addresses: []string{"100.64.0.7", "fd7a:115c:a1e0::7"}}); !reflect.DeepEqual(res, want) {
				t.Errorf("output %+v, want %+v", res, want)
			}
			effects := liEffects(t, h)
			if len(effects) != 1 {
				t.Fatalf("%d effects journaled, want 1", len(effects))
			}
			e := effects[0]
			if e.Kind != core.EffectModify || e.Resource != "tailscale.node:self" || e.Local || e.CommitWait != 2*time.Minute || e.State != core.EffectMade {
				t.Errorf("effect %+v, want a made, non-local modify of tailscale.node:self with a 2 minute commit wait", e)
			}
			if !liSame(t, e.Before.State, []byte(`{"loggedIn":false}`)) || e.After == nil || !liSame(t, e.After.State, []byte(`{"loggedIn":true}`)) ||
				e.Post == nil || !liSame(t, e.Post.State, []byte(`{"loggedIn":true}`)) || !liSame(t, e.Post.Version, []byte(`{"backendState":"Running"}`)) {
				t.Errorf("effect documents before %s, after %+v, post %+v", e.Before.State, e.After, e.Post)
			}
			if b, _ := json.Marshal(effects); strings.Contains(string(b)+string(raw), liKey) {
				t.Error("the auth key reached an effect or the output")
			}
		})
	}
}

func TestLiLoginWaitsForTheNode(t *testing.T) {
	h, tt := liSetup(t, "NeedsLogin", tslocal.Prefs{}, 3)
	start := h.clock.Now()
	var res json.RawMessage
	var err error
	liDrive(t, h, func() { res, err = tt.nodeLogin().Handler.Apply(context.Background(), h.env("c1"), liLoginIn("")) })
	if err != nil {
		t.Fatal(err)
	}
	if got := h.clock.Now().Sub(start); got != 3*nodeLoginPoll {
		t.Errorf("waited %s, want three polls of %s", got, nodeLoginPoll)
	}
	if !strings.Contains(string(res), `"backendState":"Running"`) {
		t.Errorf("output %s, want the Running backend", res)
	}
}

func TestLiLoginGivesUpWaiting(t *testing.T) {
	h, tt := liSetup(t, "NeedsLogin", tslocal.Prefs{}, -1)
	start := h.clock.Now()
	var err error
	liDrive(t, h, func() { _, err = tt.nodeLogin().Handler.Apply(context.Background(), h.env("c1"), liLoginIn("")) })
	if err == nil || core.IsDefinite(err) || !strings.Contains(err.Error(), "did not log in within 20s") || !strings.Contains(err.Error(), "Starting") {
		t.Fatalf("err = %v, want an uncertain error naming the 20 s wait and the backend state", err)
	}
	if got := h.clock.Now().Sub(start); got != 20*time.Second {
		t.Errorf("waited %s, want 20s", got)
	}
	if effects := liEffects(t, h); len(effects) != 1 || effects[0].State != core.EffectIntended {
		t.Errorf("effects %+v, want one still intended: the login may yet complete", effects)
	}
}

// liPollLocal answers the status reads after Start from poll, numbered from 1.
type liPollLocal struct {
	localAPI
	started bool
	reads   int
	poll    func(n int) (*tslocal.Status, error)
}

func (l *liPollLocal) Start(ctx context.Context, o tslocal.StartOptions) error {
	if err := l.localAPI.Start(ctx, o); err != nil {
		return err
	}
	l.started = true
	return nil
}

func (l *liPollLocal) Status(ctx context.Context) (*tslocal.Status, error) {
	if !l.started {
		return l.localAPI.Status(ctx)
	}
	l.reads++
	return l.poll(l.reads)
}

// liPollSetup is a logged-out node whose status reads after Start come from poll.
func liPollSetup(t *testing.T, poll func(n int) (*tslocal.Status, error)) (*tsHost, ts, *liPollLocal) {
	t.Helper()
	h, tt := liSetup(t, "NeedsLogin", tslocal.Prefs{}, -1)
	l := &liPollLocal{localAPI: tt.d.local(), poll: poll}
	tt.d.local = func() localAPI { return l }
	return h, tt, l
}

func TestLiLoginSettlesAwaitingMachineAuth(t *testing.T) {
	h, tt, _ := liPollSetup(t, func(n int) (*tslocal.Status, error) {
		if n < 2 {
			return liStatus("Starting"), nil
		}
		return liStatus("NeedsMachineAuth"), nil
	})
	var res json.RawMessage
	var err error
	liDrive(t, h, func() { res, err = tt.nodeLogin().Handler.Apply(context.Background(), h.env("c1"), liLoginIn("")) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(res), `"backendState":"NeedsMachineAuth"`) {
		t.Errorf("output %s, want the NeedsMachineAuth backend: the login waits for an admin's approval", res)
	}
	effects := liEffects(t, h)
	if len(effects) != 1 || effects[0].State != core.EffectMade || effects[0].Post == nil ||
		!liSame(t, effects[0].Post.Version, []byte(`{"backendState":"NeedsMachineAuth"}`)) {
		t.Errorf("effects %+v, want one made with the NeedsMachineAuth backend", effects)
	}
}

func TestLiLoginStatusFailsWhilePolling(t *testing.T) {
	h, tt, _ := liPollSetup(t, func(n int) (*tslocal.Status, error) {
		if n < 2 {
			return liStatus("Starting"), nil
		}
		return nil, errors.New("connection reset")
	})
	var err error
	liDrive(t, h, func() { _, err = tt.nodeLogin().Handler.Apply(context.Background(), h.env("c1"), liLoginIn("")) })
	if err == nil || core.IsDefinite(err) || !strings.Contains(err.Error(), "connection reset") {
		t.Fatalf("err = %v, want an uncertain error carrying the failed read", err)
	}
	if effects := liEffects(t, h); len(effects) != 1 || effects[0].State != core.EffectIntended {
		t.Errorf("effects %+v, want one still intended: the login may yet complete", effects)
	}
}

func TestLiLoginStopsWhenTheContextEnds(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h, tt, l := liPollSetup(t, func(int) (*tslocal.Status, error) {
		cancel()
		return liStatus("Starting"), nil
	})
	_, err := tt.nodeLogin().Handler.Apply(ctx, h.env("c1"), liLoginIn(""))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if l.reads != 1 {
		t.Errorf("%d status reads after Start, want 1: the poll stops once the context ends", l.reads)
	}
	if effects := liEffects(t, h); len(effects) != 1 || effects[0].State != core.EffectIntended {
		t.Errorf("effects %+v, want one still intended", effects)
	}
}

func TestLiLoginApplyRefusals(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		code     errcode.Code // "" for an error that is not coded
		definite bool
		state    core.EffectState
	}{
		{"tailscaled refuses the key", &tslocal.APIError{StatusCode: 400, Message: "invalid key " + liKey}, errcode.InvalidInput, true, core.EffectSkipped},
		{"tailscaled is not reachable", fmt.Errorf("dial: %w", tslocal.ErrUnavailable), errcode.TailscaleUnavailable, true, core.EffectSkipped},
		{"tailscaled fails", &tslocal.APIError{StatusCode: 500, Message: "boom " + liKey}, "", false, core.EffectIntended},
		{"the call fails without an answer", errors.New("post failed for " + liKey), "", false, core.EffectIntended},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, tt := liSetup(t, "NeedsLogin", tslocal.Prefs{}, 0)
			h.local.Errs["Start"] = tc.err
			_, err := tt.nodeLogin().Handler.Apply(context.Background(), h.env("c1"), liLoginIn(""))
			if err == nil || core.IsDefinite(err) != tc.definite {
				t.Fatalf("err = %v, want an error with definite %v", err, tc.definite)
			}
			var coded *errcode.Error
			if tc.code != "" && (!errors.As(err, &coded) || coded.Code != tc.code) {
				t.Errorf("err = %v, want code %s", err, tc.code)
			}
			if strings.Contains(err.Error(), liKey) || (coded != nil && strings.Contains(coded.Detail, liKey)) {
				t.Errorf("the error carries the auth key: %v", err)
			}
			if effects := liEffects(t, h); len(effects) != 1 || effects[0].State != tc.state {
				t.Errorf("effects %+v, want one %s: the effect is journaled before the call", effects, tc.state)
			}
		})
	}
}

func TestLiLoginInteractiveFails(t *testing.T) {
	for _, cause := range []error{
		&tslocal.APIError{StatusCode: 403, Message: "login access denied for " + liKey},
		fmt.Errorf("dial: %w", tslocal.ErrUnavailable),
		errors.New("connection reset after " + liKey),
	} {
		h, tt := liSetup(t, "NeedsLogin", tslocal.Prefs{}, 0)
		h.local.Errs["LoginInteractive"] = cause
		_, err := tt.nodeLogin().Handler.Apply(context.Background(), h.env("c1"), liLoginIn(""))
		if err == nil || core.IsDefinite(err) || !strings.HasPrefix(err.Error(), "tailscale: login:") || strings.Contains(err.Error(), liKey) {
			t.Errorf("cause %v: err = %v, want an uncertain tailscale: login: error without the auth key", cause, err)
		}
		if len(h.local.Starts) != 1 || h.local.LoginInteractives != 1 {
			t.Errorf("cause %v: %d starts and %d login-interactive calls, want one each", cause, len(h.local.Starts), h.local.LoginInteractives)
		}
		if effects := liEffects(t, h); len(effects) != 1 || effects[0].State != core.EffectIntended {
			t.Errorf("cause %v: effects %+v, want one still intended: Start already replaced the preferences", cause, effects)
		}
	}
}

func TestLiLoginVerify(t *testing.T) {
	ctx := context.Background()
	h, tt := liSetup(t, "NeedsLogin", tslocal.Prefs{}, 0)
	def, in := tt.nodeLogin(), liLoginIn("")
	out, err := def.Handler.Apply(ctx, h.env("c1"), in)
	if err != nil {
		t.Fatal(err)
	}
	if err := def.Handler.Verify(ctx, h.env("c1"), in, out); err != nil {
		t.Errorf("verify after apply: %v", err)
	}
	if err := def.Handler.Verify(ctx, h.env("c1"), in, json.RawMessage(`{}`)); err != nil {
		t.Errorf("verify without the output, as recovery calls it: %v", err)
	}
	h.local.Status = liStatus("NeedsMachineAuth")
	if err := def.Handler.Verify(ctx, h.env("c1"), in, out); err != nil {
		t.Errorf("verify while the node waits for approval: %v", err)
	}
	for _, state := range []string{"NeedsLogin", "Starting", "Stopped"} {
		h.local.Status = liStatus(state)
		if err := def.Handler.Verify(ctx, h.env("c1"), in, out); err == nil {
			t.Errorf("verify passed in backend state %s, want an error", state)
		}
	}
	h.local.Errs["Status"] = errors.New("boom")
	if err := def.Handler.Verify(ctx, h.env("c1"), in, out); err == nil {
		t.Error("verify passed with tailscaled silent, want an error")
	}
}

// TestLiLoginUndo applies the login, then plays the engine's undo of the
// journaled effect: compare with the recorded state, run the inverse, compare
// with the state the undo restores.
func TestLiLoginUndo(t *testing.T) {
	tests := []struct {
		name    string
		state   string
		in      string
		logouts int
		want    string // the State the node has after the undo
	}{
		{"a node that was logged out is logged out again", "NeedsLogin", ``, 1, `{"loggedIn":false}`},
		{"a node that was logged in stays logged in", "Running", `,"relogin":true`, 0, `{"loggedIn":true}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			h, tt := liSetup(t, tc.state, tslocal.Prefs{WantRunning: true}, 0)
			def := tt.nodeLogin()
			if _, err := def.Handler.Apply(ctx, h.env("c1"), liLoginIn(tc.in)); err != nil {
				t.Fatal(err)
			}
			e := liEffects(t, h)[0]
			now, err := def.Handler.Observe(ctx, h.env("c1"), e)
			if err != nil || !liSame(t, now.Current.State, e.Post.State) || now.Matches != 1 || !now.Exclusive {
				t.Fatalf("observed %+v (%v) before the undo, want the recorded state %s", now, err, e.Post.State)
			}
			e.State, e.UndoTo = core.EffectUndoing, &core.StateDoc{State: e.Before.State}
			res, err := def.Handler.Inverse(ctx, h.env("c1"), e)
			if err != nil || res.Resource != "" {
				t.Fatalf("inverse = %+v, %v", res, err)
			}
			if h.local.Logouts != tc.logouts {
				t.Errorf("%d logouts, want %d", h.local.Logouts, tc.logouts)
			}
			after, err := def.Handler.Observe(ctx, h.env("c1"), e)
			if err != nil || !liSame(t, after.Current.State, []byte(tc.want)) || !liSame(t, after.Current.State, e.UndoTo.State) {
				t.Errorf("observed %s (%v) after the undo, want %s, the state the undo leaves", after.Current.State, err, tc.want)
			}
		})
	}
}

func TestLiLoginInverseRefusals(t *testing.T) {
	loggedOut := core.Effect{Seq: 1, Resource: "tailscale.node:self", Before: core.StateDoc{State: json.RawMessage(`{"loggedIn":false}`)}}
	tests := []struct {
		name     string
		effect   core.Effect
		err      error
		code     errcode.Code
		definite bool
	}{
		{"tailscaled refuses", loggedOut, &tslocal.APIError{StatusCode: 409, Message: "busy"}, errcode.InvalidInput, true},
		{"tailscaled is not reachable", loggedOut, fmt.Errorf("dial: %w", tslocal.ErrUnavailable), errcode.TailscaleUnavailable, true},
		{"the call fails without an answer", loggedOut, errors.New("connection reset"), "", false},
		{"a before-state without the login flag", core.Effect{Seq: 1, Before: core.StateDoc{State: json.RawMessage(`{}`)}}, nil, "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, tt := liSetup(t, "Running", tslocal.Prefs{}, 0)
			h.local.Errs["Logout"] = tc.err
			_, err := tt.nodeLogin().Handler.Inverse(context.Background(), h.env("c1"), tc.effect)
			if err == nil || core.IsDefinite(err) != tc.definite {
				t.Fatalf("err = %v, want an error with definite %v", err, tc.definite)
			}
			var coded *errcode.Error
			if tc.code != "" && (!errors.As(err, &coded) || coded.Code != tc.code) {
				t.Errorf("err = %v, want code %s", err, tc.code)
			}
		})
	}
}

func TestLiObserveRefusals(t *testing.T) {
	h, tt := liSetup(t, "Running", tslocal.Prefs{}, 0)
	def := tt.nodeLogin()
	if _, err := def.Handler.Observe(context.Background(), h.env("c1"), core.Effect{Resource: "tailscale.prefs:node"}); err == nil {
		t.Error("observed another resource, want an error")
	}
	h.local.Errs["Status"] = errors.New("boom")
	if _, err := def.Handler.Observe(context.Background(), h.env("c1"), core.Effect{Resource: "tailscale.node:self"}); err == nil {
		t.Error("observed with tailscaled silent, want an error")
	}
}

func TestLiLogoutPlan(t *testing.T) {
	h, tt := liSetup(t, "Running", tslocal.Prefs{WantRunning: true}, 0)
	out, err := tt.nodeLogout().Handler.Plan(context.Background(), h.env(""), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	want := []core.DiffLine{{Op: "-", Text: "node: logged in as host-1.tail1234.ts.net"}, {Op: "+", Text: "node: logged out"}}
	if !reflect.DeepEqual(out.Diff, want) || out.Confirmation != "host-1" || len(out.StateHash) != 64 {
		t.Errorf("plan %+v, want diff %v, the host name as the confirmation and a 64-digit hash", out, want)
	}
	if len(out.Guards) != 1 || out.Guards[0].ID != link.GuardID || !out.Guards[0].OK {
		t.Errorf("guards %+v, want %s passing", out.Guards, link.GuardID)
	}
	if h.local.Logouts != 0 {
		t.Error("plan logged the node out")
	}
}

func TestLiLogoutPlanGuard(t *testing.T) {
	h, tt := liSetup(t, "Running", tslocal.Prefs{WantRunning: true, RunSSH: true}, 0)
	out, err := tt.nodeLogout().Handler.Plan(context.Background(), h.env(""), json.RawMessage(`{}`))
	if err != nil || len(out.Guards) != 1 || out.Guards[0].ID != link.GuardID || out.Guards[0].OK || out.Guards[0].Message == "" {
		t.Errorf("plan %+v (%v), want the guard failing with a message while Tailscale SSH may be the access path", out.Guards, err)
	}
}

func TestLiLogoutRefusals(t *testing.T) {
	tests := []struct {
		name, state string
		code        errcode.Code
	}{
		{"a node that is logged out", "NeedsLogin", errcode.InvalidInput},
		{"a node whose backend has not started", "NoState", errcode.TailscaleUnavailable},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, tt := liSetup(t, tc.state, tslocal.Prefs{}, 0)
			_, err := tt.nodeLogout().Handler.Plan(context.Background(), h.env(""), json.RawMessage(`{}`))
			if e := errcode.From(err); e.Code != tc.code || !core.IsDefinite(err) {
				t.Errorf("err = %v (definite %v), want %s marked definite", err, core.IsDefinite(err), tc.code)
			}
		})
	}
}

func TestLiLogoutApplyAndVerify(t *testing.T) {
	ctx := context.Background()
	h, tt := liSetup(t, "Running", tslocal.Prefs{WantRunning: true}, 0)
	def := tt.nodeLogout()
	in := json.RawMessage(`{}`)
	if err := def.Handler.Verify(ctx, h.env("c1"), in, json.RawMessage(`{}`)); err == nil {
		t.Error("verify passed on a node that is still logged in, want an error")
	}
	out, err := def.Handler.Apply(ctx, h.env("c1"), in)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != `{"backendState":"NeedsLogin"}` || h.local.Logouts != 1 {
		t.Errorf("output %s after %d logouts, want the NeedsLogin backend after one", out, h.local.Logouts)
	}
	if err := def.Handler.Verify(ctx, h.env("c1"), in, out); err != nil {
		t.Errorf("verify after apply: %v", err)
	}
	if err := def.Handler.Verify(ctx, h.env("c1"), in, json.RawMessage(`{}`)); err != nil {
		t.Errorf("verify without the output, as recovery calls it: %v", err)
	}
	if effects := liEffects(t, h); len(effects) != 0 {
		t.Errorf("effects %+v, want none: a logout is irreversible and journals nothing", effects)
	}
}

func TestLiLogoutApplyRefusals(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		code     errcode.Code
		definite bool
	}{
		{"tailscaled refuses", &tslocal.APIError{StatusCode: 403, Message: "no"}, errcode.InvalidInput, true},
		{"tailscaled is not reachable", fmt.Errorf("dial: %w", tslocal.ErrUnavailable), errcode.TailscaleUnavailable, true},
		{"the call fails without an answer", errors.New("connection reset"), "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, tt := liSetup(t, "Running", tslocal.Prefs{}, 0)
			h.local.Errs["Logout"] = tc.err
			_, err := tt.nodeLogout().Handler.Apply(context.Background(), h.env("c1"), json.RawMessage(`{}`))
			var coded *errcode.Error
			if err == nil || core.IsDefinite(err) != tc.definite || (tc.code != "" && (!errors.As(err, &coded) || coded.Code != tc.code)) {
				t.Errorf("err = %v (definite %v), want code %q with definite %v", err, core.IsDefinite(err), tc.code, tc.definite)
			}
		})
	}
}
