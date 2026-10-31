//go:build harness

package harness

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core/errcode"
)

const m4dSecret = "s3cr3t-m4-value"

// m4dReady waits until the agent's docker.engine resource reports a running
// Engine whose API is at least 1.44.
func m4dReady(ctx context.Context, t *testing.T, f *fleet) {
	var eng struct {
		Available  bool   `json:"available"`
		Reason     string `json:"reason"`
		APIVersion string `json:"apiVersion"`
	}
	err := eventually(ctx, 2*time.Minute, func(ctx context.Context) error {
		if err := f.api.Resource(ctx, f.agentID, "docker.engine", &eng); err != nil {
			return err
		}
		if !eng.Available {
			return errors.New("the engine is unavailable: " + eng.Reason)
		}
		if !m4xAtLeast(eng.APIVersion, "1.44") {
			return errors.New("the engine's API is " + eng.APIVersion + ", want 1.44 or later")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("docker.engine of host %s: %v", f.agentID, err)
	}
}

// m4dRun starts the pinned pebble image as the container name with extra
// docker run arguments, and removes the container when the test ends.
func m4dRun(ctx context.Context, t *testing.T, f *fleet, name string, extra ...string) {
	t.Helper()
	ref, err := m3dxImage("pebble")
	if err != nil {
		t.Fatal(err)
	}
	args := append([]string{"run", "-d", "--name", name}, extra...)
	m4Docker(ctx, t, f.agent, append(args, ref)...)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		_, _ = f.agent.Run(ctx, "sudo docker rm -f "+m3dxShell(name))
	})
	m3dWaitRunning(ctx, t, f.agent, name)
}

// m4dRunning reports whether the container name runs, by the Docker CLI on
// the agent VM.
func m4dRunning(ctx context.Context, t *testing.T, f *fleet, name string) bool {
	t.Helper()
	return strings.TrimSpace(m4Docker(ctx, t, f.agent, "inspect", "-f", "{{.State.Running}}", name)) == "true"
}

// m4dStartedAt is the container's State.StartedAt as the Docker CLI reports it.
func m4dStartedAt(ctx context.Context, t *testing.T, f *fleet, name string) string {
	t.Helper()
	return strings.TrimSpace(m4Docker(ctx, t, f.agent, "inspect", "-f", "{{.State.StartedAt}}", name))
}

// m4dAct applies the container action name on container and waits for it.
func m4dAct(ctx context.Context, t *testing.T, f *fleet, action, container string) Change {
	t.Helper()
	return m4ApplyAsync(ctx, t, f.api, f.agentID, action, map[string]any{"container": container})
}

// m4dRefused requires that planning action with input is refused with code.
func m4dRefused(ctx context.Context, t *testing.T, f *fleet, action string, input any, code errcode.Code) {
	t.Helper()
	_, err := m4Plan(ctx, f.api, f.agentID, action, input)
	var ae *APIError
	if !errors.As(err, &ae) || ae.Code != string(code) {
		t.Errorf("plan %s: %v, want an API error with code %s", action, err, code)
	}
}

// m4dResources: every resource lists the objects the Docker CLI created.
func m4dResources(ctx context.Context, t *testing.T, f *fleet) {
	m4dRun(ctx, t, f, "m4-res")
	err := eventually(ctx, time.Minute, func(ctx context.Context) error {
		var raw json.RawMessage
		if err := f.api.Resource(ctx, f.agentID, "docker.containers", &raw); err != nil {
			return err
		}
		cs, err := m4xContainers(raw)
		if err != nil {
			return err
		}
		c, ok := m4xFindContainer(cs, "m4-res")
		if !ok || c.State != "running" {
			return errors.New("docker.containers does not list m4-res as running")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var images struct {
		Images []struct {
			ID         string   `json:"id"`
			Containers []string `json:"containers"`
		} `json:"images"`
	}
	if err := f.api.Resource(ctx, f.agentID, "docker.images", &images); err != nil {
		t.Fatal(err)
	}
	used := false
	for _, im := range images.Images {
		used = used || slices.Contains(im.Containers, "m4-res")
	}
	if !used {
		t.Errorf("docker.images lists no image used by m4-res: %+v", images.Images)
	}
	var networks struct {
		Networks []struct {
			Name string `json:"name"`
		} `json:"networks"`
	}
	if err := f.api.Resource(ctx, f.agentID, "docker.networks", &networks); err != nil {
		t.Fatal(err)
	}
	if len(networks.Networks) == 0 || !strings.Contains(m3JSON(networks), `"bridge"`) {
		t.Errorf("docker.networks does not list the bridge network: %+v", networks)
	}
	if err := f.api.Resource(ctx, f.agentID, "docker.volumes", &json.RawMessage{}); err != nil {
		t.Error(err)
	}
}

// m4dLifecycle: start, stop and restart act on the Engine, and the undo of
// the newest change restores what its journal recorded.
func m4dLifecycle(ctx context.Context, t *testing.T, f *fleet) {
	const name = "m4-life"
	m4dRun(ctx, t, f, name)

	stop := m4dAct(ctx, t, f, "docker.container.stop", name)
	if m4dRunning(ctx, t, f, name) {
		t.Fatal("the container still runs after docker.container.stop")
	}
	if err := m4Undo(ctx, f.api, stop.ID); err != nil {
		t.Fatal(err)
	}
	if !m4dRunning(ctx, t, f, name) {
		t.Error("the undo of stop did not start the container that was running before")
	}

	m4dAct(ctx, t, f, "docker.container.stop", name)
	start := m4dAct(ctx, t, f, "docker.container.start", name)
	if !m4dRunning(ctx, t, f, name) {
		t.Fatal("the container does not run after docker.container.start")
	}
	if err := m4Undo(ctx, f.api, start.ID); err != nil {
		t.Fatal(err)
	}
	if m4dRunning(ctx, t, f, name) {
		t.Error("the undo of start did not stop the container that was stopped before")
	}

	m4dAct(ctx, t, f, "docker.container.start", name)
	before := m4dStartedAt(ctx, t, f, name)
	restart := m4dAct(ctx, t, f, "docker.container.restart", name)
	if after := m4dStartedAt(ctx, t, f, name); after == before {
		t.Errorf("StartedAt is still %s after docker.container.restart", before)
	}
	if err := m4Undo(ctx, f.api, restart.ID); err != nil {
		t.Fatal(err)
	}
	if !m4dRunning(ctx, t, f, name) {
		t.Error("the undo of restart left the container stopped")
	}
}

// m4dUndoConflict: a container restarted by hand after docker.container.start
// is not stopped by the undo, which becomes a conflict for the operator.
func m4dUndoConflict(ctx context.Context, t *testing.T, f *fleet) {
	const name = "m4-conflict"
	m4dRun(ctx, t, f, name)
	m4dAct(ctx, t, f, "docker.container.stop", name)
	start := m4dAct(ctx, t, f, "docker.container.start", name)
	m4Docker(ctx, t, f.agent, "restart", name)

	err := m4Undo(ctx, f.api, start.ID)
	var ue *m4UndoError
	if !errors.As(err, &ue) || ue.Change.State != "needs_repair" {
		t.Fatalf("undo of start after a manual restart: %v, want an undo that ended needs_repair", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if err := m4Release(ctx, f.api, start.ID, "M4 scenario cleanup"); err != nil {
			t.Errorf("release %s: %v", start.ID, err)
		}
	})
	effs, err := m4Effects(ctx, f.api, start.ID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range effs {
		found = found || (e.State == "unresolved" && e.Reason == "conflict")
	}
	if !found {
		t.Errorf("the effects of %s are %+v, want one unresolved with reason conflict", start.ID, effs)
	}
	if !m4dRunning(ctx, t, f, name) {
		t.Error("the refused undo stopped the container")
	}
}

// m4dInspect: inspect redacts every environment value, reveal-env returns
// them, and the logs and stats streams deliver events.
func m4dInspect(ctx context.Context, t *testing.T, f *fleet) {
	const name = "m4-env"
	m4dRun(ctx, t, f, name, "-e", "M4_SECRET="+m4dSecret, "-e", "M4_PLAIN=1")
	in := map[string]any{"container": name}

	var raw json.RawMessage
	if err := f.api.Read(ctx, f.agentID, "docker.container.inspect/v1", in, &raw); err != nil {
		t.Fatal(err)
	}
	var detail struct {
		Env []struct{ Name, Value string } `json:"env"`
	}
	if err := json.Unmarshal(raw, &detail); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), m4dSecret) {
		t.Error("docker.container.inspect returned the secret value")
	}
	redacted := false
	for _, e := range detail.Env {
		redacted = redacted || (e.Name == "M4_SECRET" && e.Value == "[redacted]")
	}
	if !redacted {
		t.Errorf("inspect lists the environment %+v, want M4_SECRET with the value [redacted]", detail.Env)
	}

	var revealed struct {
		Env []struct{ Name, Value string } `json:"env"`
	}
	if err := m4Reveal(ctx, f.api, f.agentID, "docker.container.reveal-env", in, &revealed); err != nil {
		t.Fatal(err)
	}
	got := ""
	for _, e := range revealed.Env {
		if e.Name == "M4_SECRET" {
			got = e.Value
		}
	}
	if got != m4dSecret {
		t.Errorf("reveal-env returned %q for M4_SECRET, want the value", got)
	}

	err := eventually(ctx, time.Minute, func(ctx context.Context) error {
		out, err := f.agent.Run(ctx, "sudo docker logs "+m3dxShell(name))
		if err == nil && strings.TrimSpace(out) == "" {
			err = errors.New("the container has logged nothing yet")
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	lines, err := m4Stream(ctx, f.api, f.agentID, "docker", "logs", url.Values{"container": {name}, "tail": {"all"}}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(lines[0]), m4dSecret) {
		t.Error("the logs stream returned the secret value")
	}
	if _, err := m4Stream(ctx, f.api, f.agentID, "docker", "stats", url.Values{"container": {name}}, 2); err != nil {
		t.Error(err)
	}
}

// m4dRemove: removing a container asks for its name, and refuses another.
func m4dRemove(ctx context.Context, t *testing.T, f *fleet) {
	const name = "m4-rm"
	m4dRun(ctx, t, f, name)
	m4Docker(ctx, t, f.agent, "stop", name)
	in := map[string]any{"container": name}

	p, err := m4Plan(ctx, f.api, f.agentID, "docker.container.remove", in)
	if err != nil {
		t.Fatal(err)
	}
	if p.Confirmation != name {
		t.Errorf("the plan asks for the confirmation %q, want the container name %q", p.Confirmation, name)
	}
	path, _ := actionPath(f.agentID, "docker.container.remove/v1", "apply")
	err = f.api.do(ctx, "POST", path, map[string]any{"planId": p.PlanID, "confirmation": "wrong"}, nil)
	var ae *APIError
	if !errors.As(err, &ae) || ae.Code != string(errcode.PlanConfirmationNeeded) {
		t.Errorf("apply with a wrong confirmation: %v, want code %s", err, errcode.PlanConfirmationNeeded)
	}
	m4ApplyAsync(ctx, t, f.api, f.agentID, "docker.container.remove", in)
	if out := strings.TrimSpace(m4Docker(ctx, t, f.agent, "ps", "-a", "-q", "--filter", "name=^"+name+"$")); out != "" {
		t.Errorf("container %s still exists after docker.container.remove: %s", name, out)
	}
}

// m4dImages: a pull runs as a long change with a progress stream, remove
// asks for the reference, and prune lists before it removes.
func m4dImages(ctx context.Context, t *testing.T, f *fleet) {
	ref, err := m3dxImage("pebble-challtestsrv")
	if err != nil {
		t.Fatal(err)
	}
	ch := m4ApplyAsync(ctx, t, f.api, f.agentID, "docker.image.pull", map[string]any{"image": ref})
	var pulled struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(ch.Output, &pulled); err != nil || pulled.ID == "" {
		t.Fatalf("the pull's output is %s (%v), want the image id", ch.Output, err)
	}
	if _, err := m4Stream(ctx, f.api, f.agentID, "docker", "pull", url.Values{"changeId": {ch.ID}}, 1); err != nil {
		t.Errorf("the pull stream of change %s: %v", ch.ID, err)
	}
	m4Docker(ctx, t, f.agent, "image", "inspect", ref)

	m4dRefused(ctx, t, f, "docker.image.remove", map[string]any{"image": ""}, errcode.InvalidInput)
	p, err := m4Plan(ctx, f.api, f.agentID, "docker.image.remove", map[string]any{"image": ref})
	if err != nil {
		t.Fatal(err)
	}
	if p.Confirmation != ref {
		t.Errorf("remove asks for the confirmation %q, want the reference as given", p.Confirmation)
	}
	m4ApplyAsync(ctx, t, f.api, f.agentID, "docker.image.remove", map[string]any{"image": ref})
	if _, err := f.agent.Run(ctx, "sudo docker image inspect "+m3dxShell(ref)); err == nil {
		t.Error("the image is still present after docker.image.remove")
	}
	prune, err := m4Plan(ctx, f.api, f.agentID, "docker.image.prune", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if prune.Confirmation != "prune" {
		t.Errorf("prune asks for the confirmation %q, want prune", prune.Confirmation)
	}
}

// m4dNetworksVolumes: create and remove for both kinds, the undo of a create,
// connect and disconnect, and the refusal to remove a network in use.
func m4dNetworksVolumes(ctx context.Context, t *testing.T, f *fleet) {
	nw := map[string]any{"name": "m4-net"}
	created := m4ApplyAsync(ctx, t, f.api, f.agentID, "docker.network.create", nw)
	m4Docker(ctx, t, f.agent, "network", "inspect", "m4-net")
	if err := m4Undo(ctx, f.api, created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.agent.Run(ctx, "sudo docker network inspect m4-net"); err == nil {
		t.Error("the undo of network.create left the network")
	}
	m4ApplyAsync(ctx, t, f.api, f.agentID, "docker.network.create", nw)
	t.Cleanup(func() { _, _ = f.agent.Run(context.Background(), "sudo docker network rm m4-net") })

	m4dRun(ctx, t, f, "m4-nc")
	link := map[string]any{"network": "m4-net", "container": "m4-nc"}
	m4ApplyAsync(ctx, t, f.api, f.agentID, "docker.network.connect", link)
	if out := m4Docker(ctx, t, f.agent, "inspect", "-f", "{{json .NetworkSettings.Networks}}", "m4-nc"); !strings.Contains(out, `"m4-net"`) {
		t.Errorf("m4-nc is not attached to m4-net after docker.network.connect: %s", out)
	}
	m4dRefused(ctx, t, f, "docker.network.remove", map[string]any{"network": "m4-net"}, errcode.DockerInUse)
	m4ApplyAsync(ctx, t, f.api, f.agentID, "docker.network.disconnect", link)
	m4ApplyAsync(ctx, t, f.api, f.agentID, "docker.network.remove", map[string]any{"network": "m4-net"})
	if _, err := f.agent.Run(ctx, "sudo docker network inspect m4-net"); err == nil {
		t.Error("the network is still present after docker.network.remove")
	}

	vol := map[string]any{"name": "m4-vol"}
	vc := m4ApplyAsync(ctx, t, f.api, f.agentID, "docker.volume.create", vol)
	m4Docker(ctx, t, f.agent, "volume", "inspect", "m4-vol")
	if err := m4Undo(ctx, f.api, vc.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.agent.Run(ctx, "sudo docker volume inspect m4-vol"); err == nil {
		t.Error("the undo of volume.create left the volume")
	}
	m4ApplyAsync(ctx, t, f.api, f.agentID, "docker.volume.create", vol)
	m4ApplyAsync(ctx, t, f.api, f.agentID, "docker.volume.remove", map[string]any{"volume": "m4-vol"})
	if _, err := f.agent.Run(ctx, "sudo docker volume inspect m4-vol"); err == nil {
		t.Error("the volume is still present after docker.volume.remove")
	}
}

// m4dOwned: actions on a container labelled darkpanel.module are refused,
// and its environment is never revealed.
func m4dOwned(ctx context.Context, t *testing.T, f *fleet) {
	const name = "m4-owned"
	m4dRun(ctx, t, f, name, "--label", "darkpanel.module=cloudflare", "-e", "M4_SECRET="+m4dSecret)
	in := map[string]any{"container": name}
	for _, action := range []string{"docker.container.stop", "docker.container.restart"} {
		m4dRefused(ctx, t, f, action, in, errcode.DockerOwned)
	}
	var out json.RawMessage
	err := m4Reveal(ctx, f.api, f.agentID, "docker.container.reveal-env", in, &out)
	var ae *APIError
	if !errors.As(err, &ae) || ae.Code != string(errcode.DockerOwned) {
		t.Errorf("reveal-env of an owned container: %v, want code %s", err, errcode.DockerOwned)
	}
	if !m4dRunning(ctx, t, f, name) {
		t.Error("the owned container stopped")
	}
}
