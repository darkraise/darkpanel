package docker

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

const plRef = "busybox:1.37"

// plImage is a local image whose tag is ref, with one repository digest.
func plImage(ref string) dockerapi.ImageSummary {
	img := dkImage(ref, 4096)
	img.RepoDigests = []string{"busybox@sha256:" + strings.Repeat("a", 64)}
	return img
}

func plShort(id string) string {
	id = strings.TrimPrefix(id, "sha256:")
	return id[:min(12, len(id))]
}

func plInput(image string) json.RawMessage {
	b, err := json.Marshal(ImagePullInput{Image: image})
	if err != nil {
		panic(err)
	}
	return b
}

func plCode(t *testing.T, err error) errcode.Code {
	t.Helper()
	if err == nil {
		t.Fatal("no error")
	}
	return errcode.From(err).Code
}

// plStep is one read of a scripted pull stream.
type plStep struct {
	msg dockerapi.PullMessage
	err error
}

type plStream struct {
	steps  []plStep
	closed *bool
}

func (s *plStream) Next() (dockerapi.PullMessage, error) {
	if len(s.steps) == 0 {
		return dockerapi.PullMessage{}, io.EOF
	}
	st := s.steps[0]
	s.steps = s.steps[1:]
	return st.msg, st.err
}

func (s *plStream) Close() error {
	*s.closed = true
	return nil
}

// plDocker is the fake engine with a scripted pull stream.
type plDocker struct {
	*fakeDocker
	steps  []plStep
	closed bool
}

func (d *plDocker) PullImage(context.Context, string) (pullStream, error) {
	return &plStream{steps: d.steps, closed: &d.closed}, nil
}

func TestPLPullSpec(t *testing.T) {
	h := newDKHost(t)
	def := h.dk().imagePull(newPullHub(h.clock))
	s := def.Spec
	if s.Name != "docker.image.pull" || s.Version != 1 || s.Class != core.ClassCompensable || s.Side != core.SideAgent {
		t.Errorf("spec %+v, want docker.image.pull v1, compensable, agent-side", s)
	}
	if !reflect.DeepEqual(s.LockKeys, []string{"docker"}) || !s.LongRunning || s.ApplyTimeout != 30*time.Minute {
		t.Errorf("spec %+v, want lock key docker, long-running, a 30 minute ApplyTimeout", s)
	}
	if s.CompensatedBy != "" || s.ForwardOnly {
		t.Errorf("spec %+v: the undo handler is the only compensation", s)
	}
	if def.Handler.Plan == nil || def.Handler.Apply == nil || def.Handler.Verify == nil || def.Handler.Observe == nil || def.Handler.Inverse == nil {
		t.Errorf("handler %+v lacks Plan, Apply, Verify, Observe or Inverse", def.Handler)
	}
	if def.InputType != reflect.TypeFor[ImagePullInput]() || def.OutputType != reflect.TypeFor[PulledImage]() {
		t.Errorf("types %v -> %v, want ImagePullInput -> PulledImage", def.InputType, def.OutputType)
	}
}

func TestPLPlanDescribesThePullAndHashesTheLocalImage(t *testing.T) {
	h := newDKHost(t)
	def := h.dk().imagePull(newPullHub(h.clock))
	ctx := context.Background()

	absent, err := def.Handler.Plan(ctx, h.env(""), plInput(plRef))
	if err != nil {
		t.Fatal(err)
	}
	wantAbsent := []core.DiffLine{
		{Op: "+", Text: "pull image busybox:1.37 from its registry"},
		{Op: " ", Text: "image busybox:1.37 is not present locally"},
		{Op: " ", Text: "undo changes nothing: a pulled or re-pointed tag is not reverted"},
	}
	if !reflect.DeepEqual(absent.Diff, wantAbsent) {
		t.Errorf("diff\n got %+v\nwant %+v", absent.Diff, wantAbsent)
	}
	if absent.Confirmation != "" || absent.StateHash == "" {
		t.Errorf("plan %+v, want no confirmation and a state hash", absent)
	}
	again, err := def.Handler.Plan(ctx, h.env(""), plInput(plRef))
	if err != nil || again.StateHash != absent.StateHash {
		t.Errorf("a second plan of the same state hashed %q, %v; want %q", again.StateHash, err, absent.StateHash)
	}

	img := plImage(plRef)
	h.docker.putImage(img)
	present, err := def.Handler.Plan(ctx, h.env(""), plInput(plRef))
	if err != nil {
		t.Fatal(err)
	}
	wantPresent := []core.DiffLine{
		{Op: "+", Text: "pull image busybox:1.37 from its registry"},
		{Op: " ", Text: "image busybox:1.37 is local as " + plShort(img.ID) + "; a moved tag is re-pointed to the pulled image"},
		{Op: " ", Text: "undo changes nothing: a pulled or re-pointed tag is not reverted"},
	}
	if !reflect.DeepEqual(present.Diff, wantPresent) {
		t.Errorf("diff\n got %+v\nwant %+v", present.Diff, wantPresent)
	}
	if present.StateHash == absent.StateHash {
		t.Error("the state hash does not change when the local image appears")
	}
}

func TestPLPlanRefusals(t *testing.T) {
	for _, ref := range []string{"", " busybox", "bad ref", "busybox:", "a..b", "x?y", "x#y", "x%y", "a\tb"} {
		h := newDKHost(t)
		def := h.dk().imagePull(newPullHub(h.clock))
		_, err := def.Handler.Plan(context.Background(), h.env(""), plInput(ref))
		if got := plCode(t, err); got != errcode.InvalidInput {
			t.Errorf("reference %q: code %s, want %s", ref, got, errcode.InvalidInput)
		}
		if calls := h.docker.calls(); len(calls) != 0 {
			t.Errorf("reference %q reached the engine: %v", ref, calls)
		}
	}

	h := newDKHost(t)
	def := h.dk().imagePull(newPullHub(h.clock))
	h.docker.fail("InspectImage", dockerapi.ErrUnavailable)
	_, err := def.Handler.Plan(context.Background(), h.env(""), plInput(plRef))
	if got := plCode(t, err); got != errcode.DockerEngineUnavailable {
		t.Errorf("unreachable engine: code %s, want %s", got, errcode.DockerEngineUnavailable)
	}
}

func TestPLApplyPullsPublishesProgressAndReportsTheImage(t *testing.T) {
	h := newDKHost(t)
	hub := newPullHub(h.clock)
	def := h.dk().imagePull(hub)
	img := plImage(plRef)
	h.docker.putImage(img)
	h.docker.pulls = map[string][]dockerapi.PullMessage{plRef: {
		{Status: "Pulling from library/busybox", ID: "1.37"},
		{Status: "Downloading", ID: "abc", Progress: "[=>]", ProgressDetail: &dockerapi.ProgressDetail{Current: 10, Total: 100}},
		{Status: "Status: Downloaded newer image for busybox:1.37"},
	}}

	raw, err := def.Handler.Apply(context.Background(), h.env("chg1"), plInput(plRef))
	if err != nil {
		t.Fatal(err)
	}
	var out PulledImage
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if want := (PulledImage{Image: plRef, ID: img.ID, Digests: img.RepoDigests}); !reflect.DeepEqual(out, want) {
		t.Errorf("output %+v, want %+v", out, want)
	}
	if got := h.docker.calls(); !slices.Equal(got, []string{"PullImage", "InspectImage"}) {
		t.Errorf("engine calls %v, want PullImage then InspectImage", got)
	}
	if effects := h.journal.effects(); len(effects) != 0 {
		t.Errorf("a pull journaled %d effects, want none", len(effects))
	}

	ch, stop := hub.subscribe(context.Background(), "chg1")
	defer stop()
	want := []PullEvent{
		{Status: "Pulling from library/busybox", ID: "1.37"},
		{Status: "Downloading", ID: "abc", Progress: "[=>]", Current: 10, Total: 100},
		{Status: "Status: Downloaded newer image for busybox:1.37"},
	}
	if got := plDrain(t, ch); !reflect.DeepEqual(got, want) {
		t.Errorf("events\n got %+v\nwant %+v", got, want)
	}
}

func TestPLApplyRefusalsFinishTheChange(t *testing.T) {
	for name, c := range map[string]struct {
		err  error
		want errcode.Code
	}{
		"registry refuses": {&dockerapi.APIError{StatusCode: 404, Message: "pull access denied"}, errcode.NotFound},
		"engine down":      {dockerapi.ErrUnavailable, errcode.DockerEngineUnavailable},
	} {
		t.Run(name, func(t *testing.T) {
			h := newDKHost(t)
			hub := newPullHub(h.clock)
			def := h.dk().imagePull(hub)
			h.docker.fail("PullImage", c.err)
			_, err := def.Handler.Apply(context.Background(), h.env("chg1"), plInput(plRef))
			if got := plCode(t, err); got != c.want {
				t.Errorf("code %s, want %s", got, c.want)
			}
			ch, stop := hub.subscribe(context.Background(), "chg1")
			defer stop()
			if got := plDrain(t, ch); len(got) != 0 {
				t.Errorf("events %v, want none from a pull that never started", plStatuses(got))
			}
		})
	}
}

func TestPLApplyReportsAFailureInsideTheStream(t *testing.T) {
	h := newDKHost(t)
	hub := newPullHub(h.clock)
	k := h.dk()
	d := &plDocker{fakeDocker: h.docker, steps: []plStep{
		{msg: dockerapi.PullMessage{Status: "Pulling from library/busybox", ID: "1.37"}},
		{msg: dockerapi.PullMessage{Status: "Downloading"}, err: errors.New("dockerapi: pull: unauthorized")},
	}}
	k.d.docker = d
	_, err := k.imagePull(hub).Handler.Apply(context.Background(), h.env("chg1"), plInput(plRef))
	if err == nil || !strings.Contains(err.Error(), "unauthorized") {
		t.Fatalf("Apply = %v, want an error naming the registry's message", err)
	}
	if !d.closed {
		t.Error("the pull stream was not closed")
	}
	ch, stop := hub.subscribe(context.Background(), "chg1")
	defer stop()
	want := []PullEvent{
		{Status: "Pulling from library/busybox", ID: "1.37"},
		{Status: "Downloading", Error: "dockerapi: pull: unauthorized"},
	}
	if got := plDrain(t, ch); !reflect.DeepEqual(got, want) {
		t.Errorf("events\n got %+v\nwant %+v", got, want)
	}
}

func TestPLVerifyNeedsTheImageLocally(t *testing.T) {
	h := newDKHost(t)
	def := h.dk().imagePull(newPullHub(h.clock))
	ctx := context.Background()
	err := def.Handler.Verify(ctx, h.env("chg1"), plInput(plRef), json.RawMessage(`{}`))
	if got := plCode(t, err); got != errcode.NotFound {
		t.Errorf("absent image: code %s, want %s", got, errcode.NotFound)
	}
	h.docker.putImage(plImage(plRef))
	if err := def.Handler.Verify(ctx, h.env("chg1"), plInput(plRef), json.RawMessage(`{}`)); err != nil {
		t.Errorf("present image: %v", err)
	}
}

func TestPLUndoChangesNothing(t *testing.T) {
	h := newDKHost(t)
	def := h.dk().imagePull(newPullHub(h.clock))
	res, err := def.Handler.Inverse(context.Background(), h.env("chg1"), core.Effect{ChangeID: "chg1"})
	if err != nil || res != (core.InverseResult{}) {
		t.Errorf("Inverse = %+v, %v; want an empty result", res, err)
	}
	if calls := h.docker.calls(); len(calls) != 0 {
		t.Errorf("the undo reached the engine: %v", calls)
	}
}

func TestPLStreamDefinition(t *testing.T) {
	h := newDKHost(t)
	def := h.dk().pullStreamDef(newPullHub(h.clock))
	if def.Name != "pull" || def.ParamsType != reflect.TypeFor[PullParams]() || def.EventType != reflect.TypeFor[PullEvent]() {
		t.Errorf("stream %q %v -> %v, want pull, PullParams -> PullEvent", def.Name, def.ParamsType, def.EventType)
	}
}

func TestPLStreamEmitsTheChangesEventsUntilItFinishes(t *testing.T) {
	h := newDKHost(t)
	hub := newPullHub(h.clock)
	def := h.dk().pullStreamDef(hub)
	hub.publish("c1", PullEvent{Status: "a"})
	hub.publish("c1", PullEvent{Status: "b", Current: 1, Total: 2})
	hub.finish("c1")

	var got []PullEvent
	err := def.Run(context.Background(), h.env(""), json.RawMessage(`{"changeId":"c1"}`), func(e any) error {
		got = append(got, e.(PullEvent))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := []PullEvent{{Status: "a"}, {Status: "b", Current: 1, Total: 2}}; !reflect.DeepEqual(got, want) {
		t.Errorf("events %+v, want %+v", got, want)
	}
}

func TestPLStreamEndsWithTheReader(t *testing.T) {
	h := newDKHost(t)
	hub := newPullHub(h.clock)
	def := h.dk().pullStreamDef(hub)

	gone := errors.New("reader gone")
	hub.publish("c1", PullEvent{Status: "a"})
	err := def.Run(context.Background(), h.env(""), json.RawMessage(`{"changeId":"c1"}`), func(any) error { return gone })
	if !errors.Is(err, gone) {
		t.Errorf("Run = %v, want the emit error", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- def.Run(ctx, h.env(""), json.RawMessage(`{"changeId":"c2"}`), func(any) error { return nil })
	}()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Run = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after its context ended")
	}
}

func TestPLStreamRefusesAMissingChangeId(t *testing.T) {
	h := newDKHost(t)
	def := h.dk().pullStreamDef(newPullHub(h.clock))
	for _, params := range []string{`{}`, `{"changeId":""}`} {
		err := def.Run(context.Background(), h.env(""), json.RawMessage(params), func(any) error { return nil })
		if got := plCode(t, err); got != errcode.InvalidInput {
			t.Errorf("params %s: code %s, want %s", params, got, errcode.InvalidInput)
		}
	}
}
