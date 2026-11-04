package panel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/schema"
	"github.com/darkraise/darkpanel/internal/modules/docker/dstate"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

func nwApplyRemove(t *testing.T, d *fdDocker, j *nwJournal) (NetworkRemoveOutput, error) {
	t.Helper()
	raw, err := networkRemoveAction(nwDeps(d)).Handler.Apply(context.Background(), nwEnv(j), nwJSON(t, NetworkRemoveInput{}))
	if err != nil {
		return NetworkRemoveOutput{}, err
	}
	var out NetworkRemoveOutput
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out, nil
}

// TestNwRemovePlan: the plan removes the panel network, plans an empty diff when it is already gone, and refuses a
// network of that name that the panel module did not make.
func TestNwRemovePlan(t *testing.T) {
	plan := func(d *fdDocker) (core.PlanOutcome, error) {
		return networkRemoveAction(nwDeps(d)).Handler.Plan(context.Background(), nwEnv(nil), nwJSON(t, NetworkRemoveInput{}))
	}
	t.Run("present", func(t *testing.T) {
		d := fdNewDocker()
		d.AddNetwork(nwPanelNet("172.31.255.248/29"))
		out, err := plan(d)
		if err != nil {
			t.Fatal(err)
		}
		if want := []core.DiffLine{{Op: "-", Text: "network darkpanel-panel"}}; !slices.Equal(out.Diff, want) || out.StateHash == "" {
			t.Errorf("diff %v and hash %q, want %v and a hash", out.Diff, out.StateHash, want)
		}
		if len(d.Removed) != 0 {
			t.Error("a plan removed a network")
		}
	})
	t.Run("absent", func(t *testing.T) {
		out, err := plan(fdNewDocker())
		if err != nil || len(out.Diff) != 0 || out.StateHash == "" {
			t.Errorf("diff %v, hash %q, error %v; want an empty diff and a hash", out.Diff, out.StateHash, err)
		}
	})
	t.Run("state hash", func(t *testing.T) {
		d := fdNewDocker()
		absent, _ := plan(d)
		d.AddNetwork(nwPanelNet("172.31.255.248/29"))
		present, _ := plan(d)
		again, _ := plan(d)
		if present.StateHash != again.StateHash || present.StateHash == absent.StateHash {
			t.Errorf("hashes absent %s, present %s, again %s; want present stable and different from absent", absent.StateHash, present.StateHash, again.StateHash)
		}
		nwAttach(d, NetworkName, "web")
		if used, _ := plan(d); used.StateHash == present.StateHash {
			t.Error("the hash did not change when a container joined the network")
		}
	})
	refusals := []struct {
		name  string
		setup func(d *fdDocker)
		code  errcode.Code
	}{
		{name: "no label", setup: func(d *fdDocker) { d.AddNetwork(nwNet(NetworkName, nil, "10.9.0.0/24")) }, code: errcode.DockerOwned},
		{name: "label of another module", setup: func(d *fdDocker) {
			d.AddNetwork(nwNet(NetworkName, map[string]string{core.LabelModule: "npm"}, "10.9.0.0/24"))
		}, code: errcode.DockerOwned},
		{name: "engine unavailable", setup: func(d *fdDocker) { d.Fail("InspectNetwork", dockerapi.ErrUnavailable) }, code: errcode.DockerEngineUnavailable},
	}
	for _, tt := range refusals {
		t.Run(tt.name, func(t *testing.T) {
			d := fdNewDocker()
			tt.setup(d)
			_, err := plan(d)
			_ = nwRefusal(t, err, tt.code)
		})
	}
}

// TestNwRemoveApply: apply journals the deletion with the network's state before the call, removes the network and
// records it made; a network already gone is removed without an effect; apply converges when run again.
func TestNwRemoveApply(t *testing.T) {
	ctx := context.Background()
	t.Run("present", func(t *testing.T) {
		d := fdNewDocker()
		n := nwPanelNet("172.31.255.248/29")
		n.ID = "n1"
		d.AddNetwork(n)
		before, err := d.InspectNetwork(ctx, "n1")
		if err != nil {
			t.Fatal(err)
		}
		beforeDoc, err := dstate.NetworkCreated(&before)
		if err != nil {
			t.Fatal(err)
		}
		j := &nwJournal{}
		removedAtIntend := -1
		j.onIntend = func() { removedAtIntend = len(d.Removed) }
		out, err := nwApplyRemove(t, d, j)
		if err != nil {
			t.Fatal(err)
		}
		if out != (NetworkRemoveOutput{Network: "darkpanel-panel", Removed: true}) || !slices.Equal(d.Removed, []string{"darkpanel-panel"}) || removedAtIntend != 0 {
			t.Errorf("output %+v, removed %v, removed at intend %d", out, d.Removed, removedAtIntend)
		}
		if len(j.list) != 1 {
			t.Fatalf("%d effects journaled, want 1", len(j.list))
		}
		e := j.list[0]
		if e.State != core.EffectMade || e.Kind != core.EffectDelete || e.Resource != "panel.network:n1" || e.Recovery != nil ||
			!bytes.Equal(e.Before.State, beforeDoc) || e.CommitWait != 2*time.Minute || e.Local || e.Post == nil || string(e.Post.State) != "null" {
			t.Errorf("effect %+v, want a made deletion of panel.network:n1 from %s to null", e, beforeDoc)
		}
		if err := networkRemoveAction(nwDeps(d)).Handler.Verify(ctx, nwEnv(j), nwJSON(t, NetworkRemoveInput{}), nwJSON(t, out)); err != nil {
			t.Errorf("verify after apply: %v", err)
		}
		again, err := nwApplyRemove(t, d, &nwJournal{})
		if err != nil || again != (NetworkRemoveOutput{Network: "darkpanel-panel"}) || len(d.Removed) != 1 {
			t.Errorf("second apply: %+v, %v, removed %v; want removed false and no second removal", again, err, d.Removed)
		}
	})
	t.Run("absent", func(t *testing.T) {
		d := fdNewDocker()
		j := &nwJournal{}
		out, err := nwApplyRemove(t, d, j)
		if err != nil || out != (NetworkRemoveOutput{Network: "darkpanel-panel"}) || len(j.list) != 0 || len(d.Removed) != 0 {
			t.Errorf("output %+v, %v, effects %d, removed %v; want removed false and nothing journaled", out, err, len(j.list), d.Removed)
		}
	})
	t.Run("gone during the call", func(t *testing.T) {
		d := fdNewDocker()
		d.AddNetwork(nwPanelNet("172.31.255.248/29"))
		d.Fail("RemoveNetwork", &dockerapi.APIError{StatusCode: http.StatusNotFound, Message: "no such network"})
		j := &nwJournal{}
		out, err := nwApplyRemove(t, d, j)
		if err != nil || !out.Removed || !slices.Equal(nwStates(j), []core.EffectState{core.EffectMade}) {
			t.Errorf("output %+v, %v, effects %v; want removed and the effect made", out, err, nwStates(j))
		}
	})
	t.Run("container attached", func(t *testing.T) {
		d := fdNewDocker()
		d.AddNetwork(nwPanelNet("172.31.255.248/29"))
		nwAttach(d, NetworkName, "web")
		j := &nwJournal{}
		_, err := nwApplyRemove(t, d, j)
		if err == nil || !core.IsDefinite(err) || !slices.Equal(nwStates(j), []core.EffectState{core.EffectSkipped}) {
			t.Errorf("error %v, effects %v; want a definite refusal and a skipped effect", err, nwStates(j))
		}
		if _, err := d.InspectNetwork(ctx, NetworkName); err != nil {
			t.Errorf("the network is gone: %v", err)
		}
	})
	t.Run("failed by the Engine", func(t *testing.T) {
		d := fdNewDocker()
		d.AddNetwork(nwPanelNet("172.31.255.248/29"))
		d.Fail("RemoveNetwork", &dockerapi.APIError{StatusCode: http.StatusInternalServerError, Message: "boom"})
		j := &nwJournal{}
		_, err := nwApplyRemove(t, d, j)
		if err == nil || core.IsDefinite(err) || !slices.Equal(nwStates(j), []core.EffectState{core.EffectIntended}) {
			t.Errorf("error %v, effects %v; want a failure that is not definite and an intended effect", err, nwStates(j))
		}
	})
	t.Run("not the panel's network", func(t *testing.T) {
		d := fdNewDocker()
		d.AddNetwork(nwNet(NetworkName, nil, "10.9.0.0/24"))
		j := &nwJournal{}
		_, err := nwApplyRemove(t, d, j)
		_ = nwRefusal(t, err, errcode.DockerOwned)
		if len(j.list) != 0 || len(d.Removed) != 0 {
			t.Errorf("effects %d, removed %v for a network the module does not own", len(j.list), d.Removed)
		}
	})
}

// TestNwRemoveVerify: verify passes only when the network is gone.
func TestNwRemoveVerify(t *testing.T) {
	verify := func(d *fdDocker) error {
		return networkRemoveAction(nwDeps(d)).Handler.Verify(context.Background(), nwEnv(&nwJournal{}),
			nwJSON(t, NetworkRemoveInput{}), nwJSON(t, NetworkRemoveOutput{}))
	}
	d := fdNewDocker()
	if err := verify(d); err != nil {
		t.Errorf("verify with the network gone: %v", err)
	}
	d.AddNetwork(nwPanelNet("172.31.255.248/29"))
	if err := verify(d); err == nil || !strings.HasPrefix(err.Error(), "panel:") {
		t.Errorf("verify with the network present = %v, want a panel: error", err)
	}
	d.Fail("InspectNetwork", dockerapi.ErrUnavailable)
	if err := verify(d); !errors.Is(err, dockerapi.ErrUnavailable) {
		t.Errorf("verify with the Engine down = %v, want the Engine's error", err)
	}
}

// TestNwRemoveObserve: the observer reads a deleted network's resource by id, as null once the network is gone.
func TestNwRemoveObserve(t *testing.T) {
	ctx := context.Background()
	d := fdNewDocker()
	n := nwPanelNet("172.31.255.248/29")
	n.ID = "n1"
	d.AddNetwork(n)
	e := core.Effect{Kind: core.EffectDelete, Resource: "panel.network:n1", State: core.EffectIntended}
	def := networkRemoveAction(nwDeps(d))
	obs, err := def.Handler.Observe(ctx, nwEnv(nil), e)
	if err != nil || obs.Matches != 1 || string(obs.Current.State) == "null" || !strings.Contains(string(obs.Current.State), `"id":"n1"`) {
		t.Errorf("observation before removal %+v, %v", obs, err)
	}
	if err := d.RemoveNetwork(ctx, "n1"); err != nil {
		t.Fatal(err)
	}
	obs, err = def.Handler.Observe(ctx, nwEnv(nil), e)
	if err != nil || obs.Matches != 1 || string(obs.Current.State) != "null" {
		t.Errorf("observation after removal %+v, %v; want the null state", obs, err)
	}
}

// TestNwRemoveDeclaration: the action is a compensable agent action compensated by panel.network.create, converging,
// without an undo of its own, and the registry resolves its compensating action only when both are registered.
func TestNwRemoveDeclaration(t *testing.T) {
	d := nwDeps(fdNewDocker())
	def := networkRemoveAction(d)
	s := def.Spec
	if s.Name != "panel.network.remove" || s.Version != 1 || s.Class != core.ClassCompensable || s.Side != core.SideAgent ||
		!slices.Equal(s.LockKeys, []string{"panel"}) || s.MinScope != core.ScopeAdmin || !s.Converges || s.CompensatedBy != "panel.network.create" || s.ForwardOnly {
		t.Errorf("spec %+v", s)
	}
	if def.Handler.Inverse != nil || def.Handler.Observe == nil {
		t.Error("the action has an undo of its own or no observer")
	}
	if _, err := schema.Input(def.InputType); err != nil {
		t.Error(err)
	}
	if _, err := schema.Output(def.OutputType); err != nil {
		t.Error(err)
	}
	if _, err := core.NewRegistry(nwModule{defs: []core.ActionDef{networkCreateAction(d), def}}); err != nil {
		t.Errorf("registry with both actions: %v", err)
	}
	if _, err := core.NewRegistry(nwModule{defs: []core.ActionDef{def}}); err == nil || !strings.Contains(err.Error(), "panel.network.create") {
		t.Errorf("registry with the removal alone = %v, want its compensating action reported missing", err)
	}
}
