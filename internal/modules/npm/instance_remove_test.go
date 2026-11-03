package npm

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/schema"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

// rmModule registers one action, so the registry's own checks run on it.
type rmModule struct{ def core.ActionDef }

func (m rmModule) Manifest() core.Manifest {
	return core.Manifest{ID: ID, Version: 1, Actions: []core.ActionDef{m.def}}
}

var rmCountSQL = map[string]string{
	"npm_instances":   "SELECT count(*) FROM npm_instances",
	"npm_attachments": "SELECT count(*) FROM npm_attachments",
	"npm_watch":       "SELECT count(*) FROM npm_watch",
}

func rmCount(t *testing.T, h *thHost, table string) int {
	t.Helper()
	var n int
	if err := h.db.Read().QueryRowContext(context.Background(), rmCountSQL[table]).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func rmShort(id string) string { return id[:min(len(id), 12)] }

// rmRefused asserts err is a definite refusal with the given code and returns it.
func rmRefused(t *testing.T, err error, code errcode.Code) *errcode.Error {
	t.Helper()
	var coded *errcode.Error
	if !errors.As(err, &coded) || coded.Code != code {
		t.Fatalf("error %v, want code %s", err, code)
	}
	if !core.IsDefinite(err) {
		t.Errorf("error %v is not marked definite", err)
	}
	return coded
}

// rmDeployed seeds a deployed instance and makes sure its container, network
// and both volumes exist, are labelled darkpanel.module=npm and the container
// runs, as a finished deploy leaves them.
func rmDeployed(t *testing.T) (*thHost, thInstance) {
	t.Helper()
	h := newTHHost(t)
	inst := h.deployed()
	labels := map[string]string{core.LabelModule: ID}
	nw, ok := h.docker.network(networkName)
	if !ok {
		nw = thNetwork(networkName)
	}
	nw.Labels = labels
	h.docker.putNetwork(nw)
	for _, name := range []string{dataVolume, certVolume} {
		v, ok := h.docker.volume(name)
		if !ok {
			v = dockerapi.Volume{Name: name, Driver: "local"}
		}
		v.Labels = labels
		h.docker.putVolume(v)
	}
	c, ok := h.docker.container(inst.ContainerID)
	if !ok {
		t.Fatal("deployed() left no container")
	}
	if strings.TrimPrefix(c.Name, "/") != containerName {
		t.Fatalf("deployed container is named %q, want %q", c.Name, containerName)
	}
	if c.Config.Labels == nil {
		c.Config.Labels = map[string]string{}
	}
	c.Config.Labels[core.LabelModule] = ID
	c.State.Running, c.State.Status = true, "running"
	h.docker.putContainer(c)
	return h, inst
}

func rmStop(t *testing.T, h *thHost, inst thInstance) {
	t.Helper()
	c, _ := h.docker.container(inst.ContainerID)
	c.State.Running, c.State.Status = false, "exited"
	h.docker.putContainer(c)
}

func rmDropContainer(t *testing.T, h *thHost, inst thInstance) {
	t.Helper()
	err := h.docker.RemoveContainer(context.Background(), inst.ContainerID, dockerapi.RemoveContainerOptions{Force: true})
	if err != nil {
		t.Fatal(err)
	}
}

// rmReserve records an object an exposure reserves.
func rmReserve(t *testing.T, h *thHost, _ thInstance) {
	t.Helper()
	ctx := context.Background()
	err := h.db.Write(ctx, func(tx *sql.Tx) error {
		return addReserved(ctx, tx, reservedRow{Kind: "proxy_host", ObjectID: 7, Owner: "npm.admin.expose", Exposure: "npm.example.com", ChangeID: "c0"})
	})
	if err != nil {
		t.Fatal(err)
	}
}

// rmWeb puts a second container on the NPM network, mounting the data volume.
func rmWeb(onNetwork, mountsData bool) func(t *testing.T, h *thHost, inst thInstance) {
	return func(t *testing.T, h *thHost, _ thInstance) {
		t.Helper()
		nw, _ := h.docker.network(networkName)
		h.docker.putContainer(thContainer("web", func(c *dockerapi.ContainerJSON) {
			if onNetwork {
				c.NetworkSettings.Networks = map[string]*dockerapi.EndpointSettings{networkName: {NetworkID: nw.ID}}
			}
			if mountsData {
				c.Mounts = []dockerapi.MountPoint{{Type: "volume", Name: dataVolume, Destination: "/data"}}
			}
		}))
	}
}

// rmUnlabelVolume drops the module label from the volume name.
func rmUnlabelVolume(name string) func(t *testing.T, h *thHost, inst thInstance) {
	return func(_ *testing.T, h *thHost, _ thInstance) {
		v, _ := h.docker.volume(name)
		delete(v.Labels, core.LabelModule)
		h.docker.putVolume(v)
	}
}

// rmEngineDown makes the next call of the Engine method op find the Engine unreachable.
func rmEngineDown(op string) func(t *testing.T, h *thHost, inst thInstance) {
	return func(_ *testing.T, h *thHost, _ thInstance) {
		h.docker.Fail(op, dockerapi.ErrUnavailable)
	}
}

var (
	rmVolumeDiff =[]core.DiffLine{
		{Op: "-", Text: "volume darkpanel-npm-data"},
		{Op: "-", Text: "volume darkpanel-npm-letsencrypt"},
		{Op: " ", Text: "the proxy hosts, access lists, certificates and users in the volumes are deleted and cannot be restored"},
	}
	rmTailDiff = []core.DiffLine{
		{Op: "-", Text: "network darkpanel-npm"},
		{Op: "-", Text: "the stored admin password and the attachment claims of this instance"},
	}
)

func rmContainerDiff(inst thInstance, killed bool) []core.DiffLine {
	d := []core.DiffLine{{Op: "-", Text: "container darkpanel-npm (" + rmShort(inst.ContainerID) + ")"}}
	if killed {
		d = append(d, core.DiffLine{Op: " ", Text: "the running container is killed first"})
	}
	return d
}

func TestRmPlan(t *testing.T) {
	ctx := context.Background()
	accepted := []struct {
		name  string
		setup func(t *testing.T, h *thHost, inst thInstance)
		diff  func(inst thInstance) []core.DiffLine
	}{
		{name: "running instance", diff: func(inst thInstance) []core.DiffLine {
			return slices.Concat(rmContainerDiff(inst, true), rmVolumeDiff, rmTailDiff)
		}},
		{name: "stopped container", setup: rmStop, diff: func(inst thInstance) []core.DiffLine {
			return slices.Concat(rmContainerDiff(inst, false), rmVolumeDiff, rmTailDiff)
		}},
		{name: "container already removed", setup: rmDropContainer, diff: func(thInstance) []core.DiffLine {
			return slices.Concat(rmVolumeDiff, rmTailDiff)
		}},
	}
	for _, tt := range accepted {
		t.Run(tt.name, func(t *testing.T) {
			h, inst := rmDeployed(t)
			if tt.setup != nil {
				tt.setup(t, h, inst)
			}
			before := len(h.docker.calls())
			out, err := h.nm().instanceRemove().Handler.Plan(ctx, h.env(""), json.RawMessage(`{}`))
			if err != nil {
				t.Fatal(err)
			}
			if want := tt.diff(inst); !slices.Equal(out.Diff, want) {
				t.Errorf("diff %v, want %v", out.Diff, want)
			}
			if out.Confirmation != "darkpanel-npm" {
				t.Errorf("confirmation %q, want darkpanel-npm", out.Confirmation)
			}
			if out.StateHash == "" {
				t.Error("empty state hash")
			}
			for _, call := range []string{"RemoveContainer", "RemoveVolume", "RemoveNetwork"} {
				if slices.Contains(h.docker.calls()[before:], call) {
					t.Errorf("a plan called %s", call)
				}
			}
		})
	}

	refusals := []struct {
		name  string
		fresh bool
		setup func(t *testing.T, h *thHost, inst thInstance)
		code  errcode.Code
		has   string
		msg   string
	}{
		{name: "no instance", fresh: true, code: errcode.NotFound, has: "no Nginx Proxy Manager instance"},
		{name: "an exposure reserves an object", setup: rmReserve, code: errcode.NPMReserved, has: "npm.example.com"},
		{name: "another container uses the network", setup: rmWeb(true, false), code: errcode.DockerInUse, has: "web"},
		{name: "another container mounts a volume", setup: rmWeb(false, true), code: errcode.DockerInUse, has: "web"},
		{name: "container not labelled", setup: func(_ *testing.T, h *thHost, inst thInstance) {
			c, _ := h.docker.container(inst.ContainerID)
			delete(c.Config.Labels, core.LabelModule)
			h.docker.putContainer(c)
		}, code: errcode.InvalidInput, has: "darkpanel.module"},
		{name: "engine unavailable", setup: func(_ *testing.T, h *thHost, _ thInstance) {
			h.docker.Fail("InspectContainer", dockerapi.ErrUnavailable)
		}, code: errcode.DockerEngineUnavailable},
		{name: "another container is attached under the network's name with another id", setup: func(_ *testing.T, h *thHost, _ thInstance) {
			h.docker.putContainer(thContainer("web", func(c *dockerapi.ContainerJSON) {
				c.NetworkSettings.Networks = map[string]*dockerapi.EndpointSettings{networkName: {NetworkID: thID("network:other")}}
			}))
		}, code: errcode.DockerInUse, has: "network darkpanel-npm is used by container web"},
		{name: "network not labelled", setup: func(_ *testing.T, h *thHost, _ thInstance) {
			nw, _ := h.docker.network(networkName)
			delete(nw.Labels, core.LabelModule)
			h.docker.putNetwork(nw)
		}, code: errcode.InvalidInput, has: "network darkpanel-npm is not labelled darkpanel.module=npm"},
		{name: "data volume not labelled", setup: rmUnlabelVolume(dataVolume), code: errcode.InvalidInput,
			has: "volume darkpanel-npm-data is not labelled darkpanel.module=npm"},
		{name: "certificate volume not labelled", setup: rmUnlabelVolume(certVolume), code: errcode.InvalidInput,
			has: "volume darkpanel-npm-letsencrypt is not labelled darkpanel.module=npm"},
		{name: "engine unavailable while inspecting the network", setup: rmEngineDown("InspectNetwork"),
			code: errcode.DockerEngineUnavailable, msg: "inspect network darkpanel-npm"},
		{name: "engine unavailable while inspecting a volume", setup: rmEngineDown("InspectVolume"),
			code: errcode.DockerEngineUnavailable, msg: "inspect volume darkpanel-npm-data"},
		{name: "engine unavailable while listing the containers", setup: rmEngineDown("ListContainers"),
			code: errcode.DockerEngineUnavailable, msg: "list containers"},
	}
	for _, tt := range refusals {
		t.Run(tt.name, func(t *testing.T) {
			var h *thHost
			var inst thInstance
			if tt.fresh {
				h = newTHHost(t)
			} else {
				h, inst = rmDeployed(t)
			}
			if tt.setup != nil {
				tt.setup(t, h, inst)
			}
			_, err := h.nm().instanceRemove().Handler.Plan(ctx, h.env(""), json.RawMessage(`{}`))
			coded := rmRefused(t, err, tt.code)
			if !strings.Contains(coded.Detail, tt.has) {
				t.Errorf("detail %q does not hold %q", coded.Detail, tt.has)
			}
			if !strings.Contains(err.Error(), tt.msg) {
				t.Errorf("error %q does not name the failed read %q", err, tt.msg)
			}
			for _, call := range []string{"RemoveContainer", "RemoveVolume", "RemoveNetwork"} {
				if slices.Contains(h.docker.calls(), call) {
					t.Errorf("a refused plan called %s", call)
				}
			}
		})
	}
}

// TestRmPlanStateHash: the hash is stable while the instance is, and changes
// when the container stops, so a plan made before a stop is refused.
func TestRmPlanStateHash(t *testing.T) {
	ctx := context.Background()
	h, inst := rmDeployed(t)
	def := h.nm().instanceRemove()
	plan := func() string {
		t.Helper()
		out, err := def.Handler.Plan(ctx, h.env(""), json.RawMessage(`{}`))
		if err != nil || out.StateHash == "" {
			t.Fatalf("plan: hash %q, error %v", out.StateHash, err)
		}
		return out.StateHash
	}
	first := plan()
	if again := plan(); again != first {
		t.Errorf("a second plan hashes %s, want %s", again, first)
	}
	rmStop(t, h, inst)
	if stopped := plan(); stopped == first {
		t.Error("the hash did not change when the container stopped")
	}
}

func TestRmApply(t *testing.T) {
	ctx := context.Background()
	apply := func(h *thHost, change string) (json.RawMessage, error) {
		h.addChange(change, "npm.instance.remove")
		return h.nm().instanceRemove().Handler.Apply(ctx, h.env(change), json.RawMessage(`{}`))
	}

	t.Run("removes the instance and its data", func(t *testing.T) {
		h, inst := rmDeployed(t)
		nw, _ := h.docker.network(networkName)
		err := h.db.Write(ctx, func(tx *sql.Tx) error {
			if err := addClaim(ctx, tx, claimRow{Network: "appnet", Claimant: "expose:a.example.com", ChangeID: "c0"}); err != nil {
				return err
			}
			return addWatch(ctx, tx, "proxy_host", "a.example.com", "c0", 1)
		})
		if err != nil {
			t.Fatal(err)
		}
		raw, err := apply(h, "c1")
		if err != nil {
			t.Fatal(err)
		}
		var out Removed
		if err := json.Unmarshal(raw, &out); err != nil || out != (Removed{ContainerID: inst.ContainerID}) {
			t.Errorf("output %s (%v), want container %s", raw, err, inst.ContainerID)
		}
		if _, ok := h.docker.container(inst.ContainerID); ok {
			t.Error("the container is still there")
		}
		for _, v := range []string{dataVolume, certVolume} {
			if _, ok := h.docker.volume(v); ok {
				t.Errorf("volume %s is still there", v)
			}
		}
		if _, ok := h.docker.network(networkName); ok {
			t.Error("the network is still there")
		}
		for _, table := range []string{"npm_instances", "npm_attachments", "npm_watch"} {
			if n := rmCount(t, h, table); n != 0 {
				t.Errorf("%s holds %d rows, want 0", table, n)
			}
		}
		want := []string{"npm.container:" + inst.ContainerID, "npm.volume:" + dataVolume, "npm.volume:" + certVolume, "npm.network:" + nw.ID}
		effects := h.journal.effects()
		if len(effects) != len(want) {
			t.Fatalf("%d effects, want %d: %+v", len(effects), len(want), effects)
		}
		for i, e := range effects {
			if e.Kind != core.EffectDelete || e.Resource != want[i] || e.State != core.EffectMade || e.Local || e.CommitWait != commitWait {
				t.Errorf("effect %d is %+v, want a made non-local delete of %s with commit wait %s", i, e, want[i], commitWait)
			}
			if string(e.Before.State) == "null" {
				t.Errorf("effect %d journals the absent state as its before-state", i)
			}
			if e.Post == nil || string(e.Post.State) != "null" {
				t.Errorf("effect %d post %+v, want the absent state null", i, e.Post)
			}
		}
	})

	t.Run("the daemon refuses a volume removal", func(t *testing.T) {
		h, _ := rmDeployed(t)
		h.docker.Fail("RemoveVolume", &dockerapi.APIError{StatusCode: 409, Message: "volume is in use"})
		_, err := apply(h, "c1")
		if err == nil || !core.IsDefinite(err) {
			t.Fatalf("error %v, want a definite refusal", err)
		}
		effects := h.journal.effects()
		if len(effects) != 2 || effects[0].State != core.EffectMade || effects[1].State != core.EffectSkipped {
			t.Fatalf("effects %+v, want the container made and the volume skipped", effects)
		}
		if n := rmCount(t, h, "npm_instances"); n != 1 {
			t.Errorf("npm_instances holds %d rows, want the instance kept", n)
		}
	})

	t.Run("the daemon fails on the network", func(t *testing.T) {
		h, _ := rmDeployed(t)
		h.docker.Fail("RemoveNetwork", &dockerapi.APIError{StatusCode: 500, Message: "boom"})
		_, err := apply(h, "c1")
		if err == nil || core.IsDefinite(err) {
			t.Fatalf("error %v, want an uncertain failure", err)
		}
		effects := h.journal.effects()
		if len(effects) != 4 || effects[3].State != core.EffectIntended {
			t.Fatalf("effects %+v, want four with the network left intended", effects)
		}
		if n := rmCount(t, h, "npm_instances"); n != 1 {
			t.Errorf("npm_instances holds %d rows, want the instance kept", n)
		}
	})

	t.Run("refused again at apply", func(t *testing.T) {
		h, inst := rmDeployed(t)
		rmReserve(t, h, inst)
		_, err := apply(h, "c1")
		_ = rmRefused(t, err, errcode.NPMReserved)
		if len(h.journal.effects()) != 0 || slices.Contains(h.docker.calls(), "RemoveContainer") {
			t.Error("apply journaled an effect or removed the container")
		}
	})

	t.Run("resumes after the container is gone", func(t *testing.T) {
		h, inst := rmDeployed(t)
		rmDropContainer(t, h, inst)
		raw, err := apply(h, "c1")
		if err != nil {
			t.Fatal(err)
		}
		var out Removed
		if err := json.Unmarshal(raw, &out); err != nil || out.ContainerID != inst.ContainerID {
			t.Errorf("output %s (%v), want the stored container %s", raw, err, inst.ContainerID)
		}
		if n := len(h.journal.effects()); n != 3 {
			t.Errorf("%d effects, want 3 (two volumes and the network)", n)
		}
		if n := rmCount(t, h, "npm_instances"); n != 0 {
			t.Errorf("npm_instances holds %d rows, want 0", n)
		}
	})
}

func TestRmVerify(t *testing.T) {
	ctx := context.Background()
	h, _ := rmDeployed(t)
	def := h.nm().instanceRemove()
	verify := func() error {
		return def.Handler.Verify(ctx, h.env("c1"), json.RawMessage(`{}`), json.RawMessage(`{}`))
	}
	if verify() == nil {
		t.Error("verify passed while the instance is deployed")
	}
	h.addChange("c1", "npm.instance.remove")
	if _, err := def.Handler.Apply(ctx, h.env("c1"), json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	if err := verify(); err != nil {
		t.Errorf("verify after the removal: %v", err)
	}
	h.docker.putVolume(dockerapi.Volume{Name: dataVolume, Driver: "local"})
	if verify() == nil {
		t.Error("verify passed with a volume of the instance back")
	}
}

// TestRmObserve: a settle finds the container's before-state while it is there
// and the absent state once it is removed.
func TestRmObserve(t *testing.T) {
	ctx := context.Background()
	h, _ := rmDeployed(t)
	def := h.nm().instanceRemove()
	h.addChange("c1", "npm.instance.remove")
	h.docker.Fail("RemoveContainer", &dockerapi.APIError{StatusCode: 500, Message: "boom"})
	if _, err := def.Handler.Apply(ctx, h.env("c1"), json.RawMessage(`{}`)); err == nil {
		t.Fatal("apply succeeded")
	}
	e := h.journal.effects()[0]
	obs, err := def.Handler.Observe(ctx, h.env("c1"), e)
	if err != nil || string(obs.Current.State) != string(e.Before.State) {
		t.Errorf("observation %s (%v), want the journaled before-state %s", obs.Current.State, err, e.Before.State)
	}
	h.addChange("c2", "npm.instance.remove")
	if _, err := def.Handler.Apply(ctx, h.env("c2"), json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	obs, err = def.Handler.Observe(ctx, h.env("c2"), e)
	if err != nil || string(obs.Current.State) != "null" {
		t.Errorf("observation %s (%v), want null", obs.Current.State, err)
	}
}

func TestRmDefinition(t *testing.T) {
	h := newTHHost(t)
	def := h.nm().instanceRemove()
	s := def.Spec
	if s.Name != "npm.instance.remove" || s.Version != 1 || s.Class != core.ClassIrreversible || s.Side != core.SideAgent ||
		!slices.Equal(s.LockKeys, []string{"npm"}) {
		t.Errorf("spec %+v", s)
	}
	if def.Handler.Inverse != nil || def.Handler.Observe == nil {
		t.Errorf("inverse %v observe %v, want no inverse and an observer", def.Handler.Inverse != nil, def.Handler.Observe != nil)
	}
	if _, err := core.NewRegistry(rmModule{def}); err != nil {
		t.Errorf("the registry refuses the action: %v", err)
	}
	v := schema.NewValidator()
	for input, field := range map[string]string{`{}`: "", `{"x":1}`: "/x"} {
		err := v.ValidateInput(def, json.RawMessage(input))
		var coded *errcode.Error
		switch {
		case field == "" && err != nil:
			t.Errorf("input %s: %v, want it accepted", input, err)
		case field != "" && (!errors.As(err, &coded) || !slices.ContainsFunc(coded.Fields, func(f errcode.FieldError) bool { return f.Path == field })):
			t.Errorf("input %s: %v, want a refusal naming %s", input, err, field)
		}
	}
}
