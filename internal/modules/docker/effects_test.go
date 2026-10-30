package docker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/docker/dstate"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

// TestEFResources: the resource strings of the four docker effect nouns.
func TestEFResources(t *testing.T) {
	for got, want := range map[string]string{
		containerResource("c1"):      "docker.container:c1",
		endpointResource("c1", "n1"): "docker.endpoint:c1@n1",
		networkResource("n1"):        "docker.network:n1",
		networkResource("?web"):      "docker.network:?web",
		volumeResource("data"):       "docker.volume:data",
		volumeResource("?data"):      "docker.volume:?data",
	} {
		if got != want {
			t.Errorf("resource = %q, want %q", got, want)
		}
	}
}

// TestEFDockerEffect: an Engine call is not Local and the daemon may still
// commit it for the commit window; a creation names its object by a
// recovery name and starts from the null state, whatever Before is passed.
func TestEFDockerEffect(t *testing.T) {
	before := json.RawMessage(`{"id":"c1","created":"x","running":false}`)
	mod := dockerEffect(core.EffectModify, containerResource("c1"), before)
	if mod.Kind != core.EffectModify || mod.Resource != "docker.container:c1" || mod.Local || mod.InverseLocal ||
		mod.CommitWait != 2*time.Minute || mod.Recovery != nil || string(mod.Before.State) != string(before) || mod.After != nil {
		t.Errorf("modify effect = %+v", mod)
	}
	for _, resource := range []string{networkResource("?web"), volumeResource("?web")} {
		c := dockerEffect(core.EffectCreate, resource, before)
		if c.Kind != core.EffectCreate || c.Resource != resource || c.Local || c.CommitWait != 2*time.Minute ||
			c.Recovery == nil || *c.Recovery != (core.RecoveryID{Kind: "name", Value: "web"}) || string(c.Before.State) != "null" {
			t.Errorf("creation effect of %s = %+v (recovery %+v)", resource, c, c.Recovery)
		}
	}
	if del := dockerEffect(core.EffectDelete, networkResource("n1"), before); del.Recovery != nil {
		t.Errorf("a deletion carries a recovery identity: %+v", del.Recovery)
	}
}

// TestEFEngineError: the dockerapi errors an action must tell its caller
// about become coded errors; any other error passes unchanged, and a mapped
// error keeps the Definite mark of the error it replaces.
func TestEFEngineError(t *testing.T) {
	unavailable := fmt.Errorf("dockerapi: GET /containers/x/json: %w", dockerapi.ErrUnavailable)
	notFound := &dockerapi.APIError{StatusCode: 404, Message: "No such container"}
	empty := fmt.Errorf("dockerapi: InspectContainer: %w", dockerapi.ErrEmptyID)
	for name, c := range map[string]struct {
		in     error
		code   errcode.Code
		detail string
	}{
		"unavailable":  {unavailable, errcode.DockerEngineUnavailable, ""},
		"not found":    {notFound, errcode.NotFound, "container web not found"},
		"empty id":     {empty, errcode.InvalidInput, ""},
		"definite 404": {core.Definite(notFound), errcode.NotFound, "container web not found"},
	} {
		got := engineError("container", "web", c.in)
		var coded *errcode.Error
		if !errors.As(got, &coded) || coded.Code != c.code || (c.detail != "" && coded.Detail != c.detail) {
			t.Errorf("%s: engineError = %#v, want code %s", name, got, c.code)
			continue
		}
		if !errors.Is(got, c.in) {
			t.Errorf("%s: the mapped error lost its cause", name)
		}
		if core.IsDefinite(got) != core.IsDefinite(c.in) {
			t.Errorf("%s: Definite = %v, want %v", name, core.IsDefinite(got), core.IsDefinite(c.in))
		}
	}
	if got := engineError("container", "web", nil); got != nil {
		t.Errorf("engineError(nil) = %v", got)
	}
	for _, other := range []error{&dockerapi.APIError{StatusCode: 409, Message: "in use"}, &dockerapi.APIError{StatusCode: 500}, errors.New("boom")} {
		got := engineError("container", "web", other)
		var coded *errcode.Error
		if !errors.Is(got, other) || errors.As(got, &coded) {
			t.Errorf("engineError(%v) = %v, want it unchanged", other, got)
		}
	}
}

// efStart is a call that starts container c and names it.
func efStart(h *dkHost, id string) func(context.Context) (string, error) {
	return func(ctx context.Context) (string, error) {
		_, err := h.docker.StartContainer(ctx, id)
		return containerResource(id), err
	}
}

// TestEFRunModify: run journals the effect before the call and records it
// made with the Recorded state the container is in afterwards.
func TestEFRunModify(t *testing.T) {
	ctx := context.Background()
	h := newDKHost(t)
	c := dkContainer("web")
	h.docker.put(c)
	before := efMust(dstate.ContainerRunning(c.ID, c.Created, false))
	e := dockerEffect(core.EffectModify, containerResource(c.ID), before)

	var intended []core.Effect
	do := func(ctx context.Context) (string, error) {
		intended = h.journal.effects()
		return efStart(h, c.ID)(ctx)
	}
	if err := h.dk().run(ctx, h.env("c1"), e, do); err != nil {
		t.Fatal(err)
	}
	if len(intended) != 1 || intended[0].State != core.EffectIntended {
		t.Fatalf("effects when the call ran: %+v, want one intended", intended)
	}
	got := h.journal.effects()
	if len(got) != 1 || got[0].State != core.EffectMade || got[0].Resource != containerResource(c.ID) || got[0].Post == nil {
		t.Fatalf("effects = %+v, want one made", got)
	}
	after, err := h.docker.InspectContainer(ctx, c.ID)
	if err != nil || !after.State.Running {
		t.Fatalf("container after the call: %+v, %v", after.State, err)
	}
	efSame(t, "Post", got[0].Post.State, efMust(dstate.ContainerRun(after, dstate.Recorded)))
	efSame(t, "Before", got[0].Before.State, before)
	if got[0].Local || got[0].CommitWait != 2*time.Minute {
		t.Errorf("effect = %+v, want not Local with a two-minute commit window", got[0])
	}
}

// TestEFRunCreation: a creation is journaled under its recovery name and
// made under the id the call returned, with the created object's state.
func TestEFRunCreation(t *testing.T) {
	ctx := context.Background()
	h := newDKHost(t)
	e := dockerEffect(core.EffectCreate, networkResource("?web"), nil)
	var id string
	do := func(ctx context.Context) (string, error) {
		var err error
		id, err = h.docker.CreateNetwork(ctx, dockerapi.NetworkCreate{Name: "web"})
		return networkResource(id), err
	}
	if err := h.dk().run(ctx, h.env("c1"), e, do); err != nil {
		t.Fatal(err)
	}
	got := h.journal.effects()
	if len(got) != 1 || got[0].State != core.EffectMade || got[0].Resource != networkResource(id) || got[0].Recovery == nil {
		t.Fatalf("effects = %+v, want one made under docker.network:%s", got, id)
	}
	n, err := h.docker.InspectNetwork(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	efSame(t, "Post", got[0].Post.State, efMust(dstate.NetworkCreated(&n)))
}

// TestEFRunFailures: a 4xx answer from the daemon changed nothing, so the
// effect is skipped and the error is Definite; any other failure leaves the
// effect intended and the error plain, because the call may still commit.
func TestEFRunFailures(t *testing.T) {
	for name, c := range map[string]struct {
		err      error
		definite bool
		state    core.EffectState
	}{
		"409 conflict": {&dockerapi.APIError{StatusCode: 409, Message: "in use"}, true, core.EffectSkipped},
		"404":          {&dockerapi.APIError{StatusCode: 404, Message: "gone"}, true, core.EffectSkipped},
		"wrapped 400":  {fmt.Errorf("dockerapi: %w", &dockerapi.APIError{StatusCode: 400}), true, core.EffectSkipped},
		"500":          {&dockerapi.APIError{StatusCode: 500, Message: "boom"}, false, core.EffectIntended},
		"reset":        {errors.New("read: connection reset by peer"), false, core.EffectIntended},
		"deadline":     {context.DeadlineExceeded, false, core.EffectIntended},
	} {
		h := newDKHost(t)
		e := dockerEffect(core.EffectModify, containerResource("c1"), json.RawMessage(`{}`))
		err := h.dk().run(context.Background(), h.env("c1"), e, func(context.Context) (string, error) { return "", c.err })
		if !errors.Is(err, c.err) || core.IsDefinite(err) != c.definite {
			t.Errorf("%s: run = %v (definite %v), want %v (definite %v)", name, err, core.IsDefinite(err), c.err, c.definite)
		}
		if got := h.journal.effects(); len(got) != 1 || got[0].State != c.state {
			t.Errorf("%s: effects = %+v, want one %s", name, got, c.state)
		}
	}
}

// efNoJournal is a journal whose Intend fails.
type efNoJournal struct{ core.EffectJournal }

func (efNoJournal) Intend(context.Context, core.Effect, []byte) (int, error) {
	return 0, errors.New("journal is full")
}

// TestEFRunNeedsAJournaledIntent: when the intent cannot be recorded the call
// is never made.
func TestEFRunNeedsAJournaledIntent(t *testing.T) {
	h := newDKHost(t)
	env := h.env("c1")
	env.Effects = efNoJournal{env.Effects}
	called := false
	e := dockerEffect(core.EffectModify, containerResource("c1"), json.RawMessage(`{}`))
	err := h.dk().run(context.Background(), env, e, func(context.Context) (string, error) { called = true; return "", nil })
	if err == nil || called || !strings.HasPrefix(err.Error(), "docker: ") || !strings.Contains(err.Error(), "journal is full") {
		t.Errorf("run = %v, call made %v; want a docker: error and no call", err, called)
	}
}

// TestEFRunUnsettled: a call that succeeded but whose resulting state cannot
// be read, or whose creation was not named, leaves the effect intended with
// an error, so the engine treats it as uncertain.
func TestEFRunUnsettled(t *testing.T) {
	ctx := context.Background()
	h := newDKHost(t)
	c := dkContainer("web")
	h.docker.put(c)
	k := h.dk()
	k.d.docker = efAPI{dockerAPI: h.docker, inspectErr: fmt.Errorf("dockerapi: GET: %w", dockerapi.ErrUnavailable)}
	e := dockerEffect(core.EffectModify, containerResource(c.ID), json.RawMessage(`{}`))
	if err := k.run(ctx, h.env("c1"), e, efStart(h, c.ID)); err == nil || !strings.HasPrefix(err.Error(), "docker: ") {
		t.Errorf("unreadable post-state: run = %v", err)
	}
	if got := h.journal.effects(); len(got) != 1 || got[0].State != core.EffectIntended {
		t.Errorf("effects = %+v, want one intended", got)
	}

	h2 := newDKHost(t)
	create := dockerEffect(core.EffectCreate, networkResource("?web"), nil)
	err := h2.dk().run(ctx, h2.env("c1"), create, func(context.Context) (string, error) { return "", nil })
	if err == nil || !strings.HasPrefix(err.Error(), "docker: ") {
		t.Errorf("unnamed creation: run = %v", err)
	}
	if got := h2.journal.effects(); len(got) != 1 || got[0].State != core.EffectIntended {
		t.Errorf("effects = %+v, want one intended", got)
	}
}
