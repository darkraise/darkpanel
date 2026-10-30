package docker

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/docker/dstate"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

const crCreated = "2026-10-01T00:00:00Z"

// crDocker records the timeouts the stop and restart calls are given.
type crDocker struct {
	*fakeDocker
	timeouts []time.Duration
}

func (d *crDocker) StopContainer(ctx context.Context, id string, timeout time.Duration) (bool, error) {
	d.timeouts = append(d.timeouts, timeout)
	return d.fakeDocker.StopContainer(ctx, id, timeout)
}

func (d *crDocker) RestartContainer(ctx context.Context, id string, timeout time.Duration) error {
	d.timeouts = append(d.timeouts, timeout)
	return d.fakeDocker.RestartContainer(ctx, id, timeout)
}

// crModule registers the three actions the way the module's manifest will.
type crModule struct{ k dk }

func (m crModule) Manifest() core.Manifest {
	return core.Manifest{ID: ID, Version: 1, Actions: []core.ActionDef{m.k.containerStart(), m.k.containerStop(), m.k.containerRestart()}}
}

func crDef(k dk, action string) core.ActionDef {
	switch action {
	case "docker.container.start":
		return k.containerStart()
	case "docker.container.stop":
		return k.containerStop()
	}
	return k.containerRestart()
}

// crSetup holds a container "web", running or stopped, behind a dk whose engine is a crDocker.
func crSetup(t *testing.T, running bool, opts ...func(*dockerapi.ContainerJSON)) (*dkHost, dk, *crDocker) {
	t.Helper()
	h := newDKHost(t)
	if running {
		opts = append(opts, dkRunning)
	}
	h.docker.put(dkContainer("web", opts...))
	d := &crDocker{fakeDocker: h.docker}
	k := h.dk()
	k.d.docker = d
	return h, k, d
}

func crIn(ref string) json.RawMessage { return json.RawMessage(`{"container":"` + ref + `"}`) }

// crSame compares two State documents as JSON values.
func crSame(t *testing.T, a, b json.RawMessage) bool {
	t.Helper()
	var va, vb any
	if err := json.Unmarshal(a, &va); err != nil {
		t.Fatalf("state %s: %v", a, err)
	}
	if err := json.Unmarshal(b, &vb); err != nil {
		t.Fatalf("state %s: %v", b, err)
	}
	return reflect.DeepEqual(va, vb)
}

func crState(t *testing.T, raw json.RawMessage) ContainerState {
	t.Helper()
	var s ContainerState
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestCrActionDefinitions(t *testing.T) {
	k := newDKHost(t).dk()
	if _, err := core.NewRegistry(crModule{k: k}); err != nil {
		t.Fatalf("the actions do not register: %v", err)
	}
	for _, name := range []string{"docker.container.start", "docker.container.stop", "docker.container.restart"} {
		def := crDef(k, name)
		s, h := def.Spec, def.Handler
		if s.Name != name || s.Version != 1 || s.Class != core.ClassCompensable || s.Side != core.SideAgent ||
			!slices.Equal(s.LockKeys, []string{lockKey}) || s.LongRunning {
			t.Errorf("%s: spec %+v, want a synchronous compensable agent action locking %q", name, s, lockKey)
		}
		if def.InputType != reflect.TypeFor[ContainerInput]() || def.OutputType != reflect.TypeFor[ContainerState]() {
			t.Errorf("%s: input %v and output %v, want ContainerInput and ContainerState", name, def.InputType, def.OutputType)
		}
		if h.Plan == nil || h.Apply == nil || h.Verify == nil || h.Observe == nil || h.Inverse == nil {
			t.Errorf("%s: handler lacks Plan, Apply, Verify, Observe or Inverse", name)
		}
	}
}

func TestCrPlan(t *testing.T) {
	head := core.DiffLine{Op: " ", Text: "container web (" + dkID("web")[:12] + ")"}
	line := func(op, text string) core.DiffLine { return core.DiffLine{Op: op, Text: text} }
	tests := []struct {
		name, action string
		running      bool
		want         []core.DiffLine
	}{
		{"start a stopped container", "docker.container.start", false, []core.DiffLine{head, line("-", "state: stopped"), line("+", "state: running")}},
		{"start a running container", "docker.container.start", true, []core.DiffLine{head, line(" ", "state: running (already running)")}},
		{"stop a running container", "docker.container.stop", true, []core.DiffLine{head, line("-", "state: running"), line("+", "state: stopped")}},
		{"stop a stopped container", "docker.container.stop", false, []core.DiffLine{head, line(" ", "state: stopped (already stopped)")}},
		{"restart a running container", "docker.container.restart", true, []core.DiffLine{head, line(" ", "state: running"), line("+", "restart: stop with a 10 s timeout, then start")}},
		{"restart a stopped container", "docker.container.restart", false, []core.DiffLine{head, line("-", "state: stopped"), line("+", "state: running")}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, k, _ := crSetup(t, tc.running)
			out, err := crDef(k, tc.action).Handler.Plan(context.Background(), h.env(""), crIn("web"))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(out.Diff, tc.want) || len(out.StateHash) != 64 || out.Confirmation != "" || len(out.Guards) != 0 {
				t.Errorf("plan %+v, want diff %v, a 64-digit state hash, no confirmation and no guards", out, tc.want)
			}
		})
	}
}

func TestCrPlanStateHash(t *testing.T) {
	ctx := context.Background()
	hash := func(running bool, opts ...func(*dockerapi.ContainerJSON)) string {
		h, k, _ := crSetup(t, running, opts...)
		out, err := k.containerStart().Handler.Plan(ctx, h.env(""), crIn("web"))
		if err != nil {
			t.Fatal(err)
		}
		return out.StateHash
	}
	stopped, running := hash(false), hash(true)
	if stopped == running {
		t.Error("a stopped and a running container have the same state hash")
	}
	if again := hash(true, func(c *dockerapi.ContainerJSON) { c.State.StartedAt = "2026-10-02T00:00:00Z" }); again != running {
		t.Error("a later start time changed the state hash, want identity and running flag only")
	}
	if other := hash(true, func(c *dockerapi.ContainerJSON) { c.ID = dkID("recreated") }); other == running {
		t.Error("a recreated container (another id) has the same state hash")
	}
}

func TestCrRefusals(t *testing.T) {
	owned := dkLabels(map[string]string{core.LabelModule: "npm"})
	tests := []struct {
		name  string
		ref   string
		setup func(h *dkHost)
		opts  []func(*dockerapi.ContainerJSON)
		code  errcode.Code
	}{
		{"empty container", "", nil, nil, errcode.InvalidInput},
		{"unknown container", "ghost", nil, nil, errcode.NotFound},
		{"owned container", "web", nil, []func(*dockerapi.ContainerJSON){owned}, errcode.DockerOwned},
		{"engine unavailable", "web", func(h *dkHost) { h.docker.fail("InspectContainer", dockerapi.ErrUnavailable) }, nil, errcode.DockerEngineUnavailable},
	}
	for _, tc := range tests {
		for _, phase := range []string{"plan", "apply"} {
			t.Run(tc.name+" at "+phase, func(t *testing.T) {
				h, k, _ := crSetup(t, false, tc.opts...)
				if tc.setup != nil {
					tc.setup(h)
				}
				def := crDef(k, "docker.container.start")
				var err error
				if phase == "plan" {
					_, err = def.Handler.Plan(context.Background(), h.env(""), crIn(tc.ref))
				} else {
					_, err = def.Handler.Apply(context.Background(), h.env("c1"), crIn(tc.ref))
				}
				if e := errcode.From(err); e.Code != tc.code || !core.IsDefinite(err) {
					t.Errorf("err = %v (definite %v), want %s marked definite", err, core.IsDefinite(err), tc.code)
				}
				if tc.ref == "" {
					if e := errcode.From(err); len(e.Fields) != 1 || e.Fields[0].Path != "/container" {
						t.Errorf("fields %+v, want one at /container", e.Fields)
					}
				}
				if got := h.journal.effects(); len(got) != 0 || slices.Contains(h.docker.calls(), "StartContainer") {
					t.Errorf("effects %v and calls %v, want nothing journaled and no start", got, h.docker.calls())
				}
			})
		}
	}
}

func TestCrApply(t *testing.T) {
	tests := []struct {
		name, action  string
		running       bool
		wantRunning   bool
		wantBefore    bool // the running flag journaled as the state an undo restores
		call          string
		wantTimeouts  []time.Duration
		wantNoCallsOf []string
	}{
		{"start a stopped container", "docker.container.start", false, true, false, "StartContainer", nil, []string{"StopContainer", "RestartContainer"}},
		{"start a running container", "docker.container.start", true, true, true, "StartContainer", nil, []string{"StopContainer", "RestartContainer"}},
		{"stop a running container", "docker.container.stop", true, false, true, "StopContainer", []time.Duration{stopTimeout}, []string{"StartContainer", "RestartContainer"}},
		{"stop a stopped container", "docker.container.stop", false, false, false, "StopContainer", []time.Duration{stopTimeout}, []string{"StartContainer", "RestartContainer"}},
		{"restart a running container", "docker.container.restart", true, true, true, "RestartContainer", []time.Duration{stopTimeout}, []string{"StartContainer", "StopContainer"}},
		{"restart a stopped container", "docker.container.restart", false, true, true, "RestartContainer", []time.Duration{stopTimeout}, []string{"StartContainer", "StopContainer"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, k, d := crSetup(t, tc.running)
			h.clock.Advance(time.Minute)
			raw, err := crDef(k, tc.action).Handler.Apply(context.Background(), h.env("c1"), crIn("web"))
			if err != nil {
				t.Fatal(err)
			}
			if got, want := crState(t, raw), (ContainerState{ID: dkID("web"), Name: "web", Running: tc.wantRunning}); got != want {
				t.Errorf("output %+v, want %+v", got, want)
			}
			c, _ := h.docker.container(dkID("web"))
			if c.State.Running != tc.wantRunning {
				t.Errorf("container running = %v, want %v", c.State.Running, tc.wantRunning)
			}
			calls := h.docker.calls()
			if !slices.Contains(calls, tc.call) {
				t.Errorf("engine calls %v, want %s", calls, tc.call)
			}
			for _, other := range tc.wantNoCallsOf {
				if slices.Contains(calls, other) {
					t.Errorf("engine calls %v include %s", calls, other)
				}
			}
			if !slices.Equal(d.timeouts, tc.wantTimeouts) {
				t.Errorf("stop timeouts %v, want %v", d.timeouts, tc.wantTimeouts)
			}

			effects := h.journal.effects()
			if len(effects) != 1 {
				t.Fatalf("%d effects journaled, want 1", len(effects))
			}
			e := effects[0]
			if e.Kind != core.EffectModify || e.Resource != containerResource(dkID("web")) || e.Local || e.CommitWait != commitWait || e.State != core.EffectMade {
				t.Errorf("effect %+v, want a made, non-local modify of the container with the Engine's commit wait", e)
			}
			before, _ := dstate.ContainerRunning(dkID("web"), crCreated, tc.wantBefore)
			after, _ := dstate.ContainerRun(c, dstate.Recorded)
			if !crSame(t, e.Before.State, before) {
				t.Errorf("before %s, want %s", e.Before.State, before)
			}
			if e.Post == nil || !crSame(t, e.Post.State, after) {
				t.Errorf("post %+v, want the recorded state %s", e.Post, after)
			}
		})
	}
}

func TestCrApplyFindsTheContainerByID(t *testing.T) {
	h, k, _ := crSetup(t, false)
	raw, err := k.containerStart().Handler.Apply(context.Background(), h.env("c1"), crIn(dkID("web")))
	if got := crState(t, raw); err != nil || got.Name != "web" || !got.Running {
		t.Errorf("output %+v (%v), want web running", got, err)
	}
	if effects := h.journal.effects(); len(effects) != 1 || effects[0].Resource != containerResource(dkID("web")) {
		t.Errorf("effects %+v, want one on the container's full id", effects)
	}
}

func TestCrApplyJournalsBeforeTheCall(t *testing.T) {
	t.Run("the daemon refuses", func(t *testing.T) {
		h, k, _ := crSetup(t, false)
		h.docker.fail("StartContainer", &dockerapi.APIError{StatusCode: 400, Message: "bad request"})
		_, err := k.containerStart().Handler.Apply(context.Background(), h.env("c1"), crIn("web"))
		effects := h.journal.effects()
		if !core.IsDefinite(err) || len(effects) != 1 || effects[0].State != core.EffectSkipped {
			t.Errorf("err = %v (definite %v), effects %+v; want a definite refusal and a skipped effect", err, core.IsDefinite(err), effects)
		}
	})
	t.Run("the call fails without an answer", func(t *testing.T) {
		h, k, _ := crSetup(t, false)
		h.docker.fail("StartContainer", errors.New("connection reset"))
		_, err := k.containerStart().Handler.Apply(context.Background(), h.env("c1"), crIn("web"))
		effects := h.journal.effects()
		if err == nil || core.IsDefinite(err) || len(effects) != 1 || effects[0].State != core.EffectIntended {
			t.Errorf("err = %v (definite %v), effects %+v; want an uncertain error and an intended effect", err, core.IsDefinite(err), effects)
		}
	})
}

func TestCrVerify(t *testing.T) {
	tests := []struct {
		action  string
		running bool // the state Verify requires
	}{
		{"docker.container.start", true},
		{"docker.container.restart", true},
		{"docker.container.stop", false},
	}
	for _, tc := range tests {
		t.Run(tc.action, func(t *testing.T) {
			h, k, _ := crSetup(t, !tc.running)
			def := crDef(k, tc.action)
			ctx := context.Background()
			out, err := def.Handler.Apply(ctx, h.env("c1"), crIn("web"))
			if err != nil {
				t.Fatal(err)
			}
			if err := def.Handler.Verify(ctx, h.env("c1"), crIn("web"), out); err != nil {
				t.Errorf("verify after apply: %v", err)
			}
			if err := def.Handler.Verify(ctx, h.env("c1"), crIn("web"), json.RawMessage(`{}`)); err != nil {
				t.Errorf("verify without the output, as recovery calls it: %v", err)
			}
			c, _ := h.docker.container(dkID("web"))
			c.State.Running = !tc.running
			h.docker.put(c)
			if err := def.Handler.Verify(ctx, h.env("c1"), crIn("web"), out); err == nil {
				t.Error("verify passed with the container in the other state, want an error")
			}
		})
	}
}

// TestCrUndo applies each action, then plays the engine's undo of the
// journaled effect: compare with the recorded state, run the inverse, compare
// with the state the undo restores.
func TestCrUndo(t *testing.T) {
	tests := []struct {
		name, action string
		running      bool
		wantRunning  bool // after the undo
		noop         bool // the inverse changes nothing
	}{
		{"undo a start of a stopped container", "docker.container.start", false, false, false},
		{"undo a start of a running container", "docker.container.start", true, true, true},
		{"undo a stop of a running container", "docker.container.stop", true, true, false},
		{"undo a stop of a stopped container", "docker.container.stop", false, false, true},
		{"undo a restart of a running container", "docker.container.restart", true, true, true},
		{"undo a restart of a stopped container", "docker.container.restart", false, true, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			h, k, _ := crSetup(t, tc.running)
			def := crDef(k, tc.action)
			if _, err := def.Handler.Apply(ctx, h.env("c1"), crIn("web")); err != nil {
				t.Fatal(err)
			}
			e := h.journal.effects()[0]

			now, err := def.Handler.Observe(ctx, h.env("c1"), e)
			if err != nil || !crSame(t, now.Current.State, e.Post.State) {
				t.Fatalf("observed %s (%v) before the undo, want the recorded state %s", now.Current.State, err, e.Post.State)
			}

			h.clock.Advance(time.Minute)
			applied, _ := h.docker.container(dkID("web"))
			e.State, e.UndoTo = core.EffectUndoing, &e.Before
			if _, err := def.Handler.Inverse(ctx, h.env("c1"), e); err != nil {
				t.Fatal(err)
			}
			undone, _ := h.docker.container(dkID("web"))
			if undone.State.Running != tc.wantRunning {
				t.Errorf("running = %v after the undo, want %v", undone.State.Running, tc.wantRunning)
			}
			if tc.noop && !reflect.DeepEqual(applied, undone) {
				t.Errorf("the inverse changed the container: %+v -> %+v", applied.State, undone.State)
			}
			after, err := def.Handler.Observe(ctx, h.env("c1"), e)
			if err != nil || !crSame(t, after.Current.State, e.Before.State) {
				t.Errorf("observed %s (%v) after the undo, want the journaled before-state %s", after.Current.State, err, e.Before.State)
			}
		})
	}
}

func TestCrObserveSeesALaterOperation(t *testing.T) {
	ctx := context.Background()
	h, k, _ := crSetup(t, false)
	def := k.containerStart()
	if _, err := def.Handler.Apply(ctx, h.env("c1"), crIn("web")); err != nil {
		t.Fatal(err)
	}
	e := h.journal.effects()[0]
	h.clock.Advance(time.Minute)
	if err := h.docker.RestartContainer(ctx, "web", stopTimeout); err != nil {
		t.Fatal(err)
	}
	obs, err := def.Handler.Observe(ctx, h.env("c1"), e)
	if err != nil || crSame(t, obs.Current.State, e.Post.State) {
		t.Errorf("observed %s (%v) after a later restart, want a state different from the recorded %s", obs.Current.State, err, e.Post.State)
	}
}

func TestCrInverseRefusals(t *testing.T) {
	ctx := context.Background()
	setup := func(t *testing.T) (*dkHost, core.ActionDef, core.Effect) {
		t.Helper()
		h, k, _ := crSetup(t, false)
		def := k.containerStart()
		if _, err := def.Handler.Apply(ctx, h.env("c1"), crIn("web")); err != nil {
			t.Fatal(err)
		}
		e := h.journal.effects()[0]
		e.State, e.UndoTo = core.EffectUndoing, &e.Before
		return h, def, e
	}
	t.Run("the container is gone", func(t *testing.T) {
		h, def, e := setup(t)
		if err := h.docker.RemoveContainer(ctx, "web", dockerapi.RemoveContainerOptions{Force: true}); err != nil {
			t.Fatal(err)
		}
		_, err := def.Handler.Inverse(ctx, h.env("c1"), e)
		if c := errcode.From(err).Code; c != errcode.NotFound || !core.IsDefinite(err) {
			t.Errorf("err = %v (definite %v), want %s marked definite", err, core.IsDefinite(err), errcode.NotFound)
		}
	})
	t.Run("the daemon refuses", func(t *testing.T) {
		h, def, e := setup(t)
		h.docker.fail("StopContainer", &dockerapi.APIError{StatusCode: 409, Message: "conflict"})
		if _, err := def.Handler.Inverse(ctx, h.env("c1"), e); !core.IsDefinite(err) {
			t.Errorf("err = %v, want a definite refusal", err)
		}
	})
	t.Run("the call fails without an answer", func(t *testing.T) {
		h, def, e := setup(t)
		h.docker.fail("StopContainer", errors.New("connection reset"))
		if _, err := def.Handler.Inverse(ctx, h.env("c1"), e); err == nil || core.IsDefinite(err) {
			t.Errorf("err = %v, want an error that is not definite", err)
		}
	})
}
