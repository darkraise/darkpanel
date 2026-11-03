package cloudflare

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io/fs"
	"path"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/schema"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

const (
	cpTunnel = "5c1d2f3a-0b4e-4c6d-8e7f-9a0b1c2d3e4f"
	cpOther  = "7a8b9c0d-1e2f-4a3b-8c4d-5e6f7a8b9c0d"
)

// cpModule registers the actions it holds, so the registry's own checks run on them.
type cpModule struct{ defs []core.ActionDef }

func (m cpModule) Manifest() core.Manifest {
	return core.Manifest{ID: ID, Version: 1, Actions: m.defs}
}

func cpJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// cpCF is the host's cf with agentDeps.readFile serving the files the host's FileWriter holds: connector.run observes
// its token file through readFile, which h.cf() leaves nil.
func cpCF(h *agHost) cf {
	c := h.cf()
	c.a.readFile = func(name string) ([]byte, error) {
		f, ok := h.files.file(path.Dir(name), path.Base(name))
		if !ok {
			return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
		}
		return f.Data, nil
	}
	return c
}

// cpDeploy runs cloudflare.connector.run for tunnel on the host and answers the connector it started.
func cpDeploy(t *testing.T, h *agHost, tunnel string) Connector {
	t.Helper()
	in := cpJSON(t, ConnectorRunInput{Tunnel: tunnel, Token: agToken(tunnel)})
	raw, err := cpCF(h).connectorRun().Handler.Apply(context.Background(), h.env("run-"+tunnel[:8]), in)
	if err != nil {
		t.Fatalf("connector.run for %s: %v", tunnel, err)
	}
	var c Connector
	if err := json.Unmarshal(raw, &c); err != nil || c.ContainerID == "" {
		t.Fatalf("connector.run output %s (%v), want a connector with a container id", raw, err)
	}
	return c
}

// cpClaim records an attachment claim on the connector of tunnel.
func cpClaim(t *testing.T, h *agHost, tunnel, network, claimant string) {
	t.Helper()
	ctx := context.Background()
	err := h.env("").DB.Write(ctx, func(tx *sql.Tx) error {
		return insertClaim(ctx, tx, claimRow{TunnelID: tunnel, Network: network, Claimant: claimant, ChangeID: "c0"})
	})
	if err != nil {
		t.Fatal(err)
	}
}

// cpDropContainer removes the connector's container from the Engine and leaves its record and file.
func cpDropContainer(t *testing.T, h *agHost, c Connector) {
	t.Helper()
	err := cpCF(h).a.docker.RemoveContainer(context.Background(), c.ContainerID, dockerapi.RemoveContainerOptions{Force: true})
	if err != nil {
		t.Fatal(err)
	}
}

func cpRowStored(t *testing.T, h *agHost, tunnel string) bool {
	t.Helper()
	_, ok, err := loadConnector(context.Background(), h.env("").DB.Read(), tunnel)
	if err != nil {
		t.Fatal(err)
	}
	return ok
}

func cpEngineHas(t *testing.T, h *agHost, tunnel string) bool {
	t.Helper()
	_, err := cpCF(h).a.docker.InspectContainer(context.Background(), connectorName(tunnel))
	switch {
	case err == nil:
		return true
	case dockerapi.IsNotFound(err):
		return false
	}
	t.Fatal(err)
	return false
}

func cpFileStored(h *agHost, tunnel string) bool {
	_, ok := h.files.file(tokenDir, tokenFileName(tunnel))
	return ok
}

func cpPlan(t *testing.T, h *agHost, tunnel string) (core.PlanOutcome, error) {
	t.Helper()
	return cpCF(h).connectorStop().Handler.Plan(context.Background(), h.env(""), cpJSON(t, ConnectorStopInput{Tunnel: tunnel}))
}

func cpApply(t *testing.T, h *agHost, change, tunnel string) (ConnectorStopped, error) {
	t.Helper()
	raw, err := cpCF(h).connectorStop().Handler.Apply(context.Background(), h.env(change), cpJSON(t, ConnectorStopInput{Tunnel: tunnel}))
	if err != nil {
		return ConnectorStopped{}, err
	}
	var out ConnectorStopped
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("output %s: %v", raw, err)
	}
	return out, nil
}

// cpRefused fails the test unless err is a Definite refusal with the code, and answers it.
func cpRefused(t *testing.T, err error, code errcode.Code) *errcode.Error {
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

// cpDiff is the plan of stopping the running connector c of tunnel.
func cpDiff(c Connector, tunnel string) []core.DiffLine {
	return []core.DiffLine{
		{Op: "-", Text: "container " + connectorName(tunnel) + " (" + c.ContainerID[:min(len(c.ContainerID), 12)] + ")"},
		{Op: "-", Text: "token file " + tokenDir + "/" + tokenFileName(tunnel)},
		{Op: "-", Text: "the stored connector record of tunnel " + tunnel},
	}
}

// TestCpDefinition: the stop is a compensable agent action under the module's lock, compensated by the run it
// removes, with no undo of its own.
func TestCpDefinition(t *testing.T) {
	h := newAGHost(t)
	def := cpCF(h).connectorStop()
	s := def.Spec
	if s.Name != "cloudflare.connector.stop" || s.Version != 1 || s.Summary == "" || s.Class != core.ClassCompensable || s.Side != core.SideAgent ||
		s.CompensatedBy != "cloudflare.connector.run" || !s.Converges || !slices.Equal(s.LockKeys, []string{"cloudflare"}) {
		t.Errorf("spec %+v, want version 1, a summary, a compensable agent action compensated by cloudflare.connector.run, converging, lock cloudflare", s)
	}
	if def.Handler.Inverse != nil {
		t.Error("the stop has an inverse, want CompensatedBy as its one compensation")
	}
	if _, err := core.NewRegistry(cpModule{[]core.ActionDef{cpCF(h).connectorRun(), def}}); err != nil {
		t.Errorf("the registry refuses the action: %v", err)
	}
	v := schema.NewValidator()
	for input, field := range map[string]string{`{"tunnel":"` + cpTunnel + `"}`: "", `{"tunnel":"` + cpTunnel + `","y":1}`: "/y"} {
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

// TestCpPlan: the plan lists what the stop removes, changes nothing, and is empty once nothing is left. A claim on
// another tunnel's connector does not hold this one.
func TestCpPlan(t *testing.T) {
	accepted := []struct {
		name  string
		setup func(t *testing.T, h *agHost, c Connector)
		diff  func(c Connector) []core.DiffLine
	}{
		{"a running connector", nil, func(c Connector) []core.DiffLine { return cpDiff(c, cpTunnel) }},
		{"the container is gone from the Engine", cpDropContainer, func(c Connector) []core.DiffLine { return cpDiff(c, cpTunnel)[1:] }},
		{"another tunnel's connector holds a claim", func(t *testing.T, h *agHost, _ Connector) {
			cpDeploy(t, h, cpOther)
			cpClaim(t, h, cpOther, "appnet", "expose:other.example.com")
		}, func(c Connector) []core.DiffLine { return cpDiff(c, cpTunnel) }},
	}
	for _, tt := range accepted {
		t.Run(tt.name, func(t *testing.T) {
			h := newAGHost(t)
			c := cpDeploy(t, h, cpTunnel)
			if tt.setup != nil {
				tt.setup(t, h, c)
			}
			before := len(h.docker.calls())
			out, err := cpPlan(t, h, cpTunnel)
			if err != nil {
				t.Fatal(err)
			}
			if want := tt.diff(c); !slices.Equal(out.Diff, want) {
				t.Errorf("diff %v, want %v", out.Diff, want)
			}
			if out.StateHash == "" {
				t.Error("empty state hash")
			}
			for _, line := range out.Diff {
				if strings.Contains(line.Text, agToken(cpTunnel)) {
					t.Errorf("diff line %q shows the token", line.Text)
				}
			}
			for _, call := range []string{"RemoveContainer", "StopContainer"} {
				if slices.Contains(h.docker.calls()[before:], call) {
					t.Errorf("a plan called %s", call)
				}
			}
		})
	}

	t.Run("nothing to stop", func(t *testing.T) {
		h := newAGHost(t)
		out, err := cpPlan(t, h, cpTunnel)
		if err != nil {
			t.Fatal(err)
		}
		if len(out.Diff) != 0 || out.StateHash == "" {
			t.Errorf("diff %v and hash %q, want an empty diff and a hash", out.Diff, out.StateHash)
		}
	})

	refusals := []struct {
		name   string
		tunnel string
		setup  func(t *testing.T, h *agHost, c Connector)
		code   errcode.Code
		path   string
		has    []string
	}{
		{"a name instead of a tunnel id", "web", nil, errcode.InvalidInput, "/tunnel", nil},
		{"no tunnel id", "", nil, errcode.InvalidInput, "/tunnel", nil},
		{"a tunnel id in upper case", strings.ToUpper(cpTunnel), nil, errcode.InvalidInput, "/tunnel", nil},
		{"the connector holds claims", cpTunnel, func(t *testing.T, h *agHost, _ Connector) {
			cpClaim(t, h, cpTunnel, "appnet", "expose:app.example.com")
			cpClaim(t, h, cpTunnel, "backend", "expose:api.example.com")
		}, errcode.DockerInUse, "", []string{"expose:app.example.com", "appnet", "expose:api.example.com", "backend"}},
		{"the Engine is unavailable", cpTunnel, func(_ *testing.T, h *agHost, _ Connector) {
			h.docker.Fail("InspectContainer", dockerapi.ErrUnavailable)
		}, errcode.DockerEngineUnavailable, "", nil},
	}
	for _, tt := range refusals {
		t.Run(tt.name, func(t *testing.T) {
			h := newAGHost(t)
			c := cpDeploy(t, h, cpTunnel)
			if tt.setup != nil {
				tt.setup(t, h, c)
			}
			_, err := cpPlan(t, h, tt.tunnel)
			coded := cpRefused(t, err, tt.code)
			if tt.path != "" && !slices.ContainsFunc(coded.Fields, func(f errcode.FieldError) bool { return f.Path == tt.path }) {
				t.Errorf("fields %+v, want a refusal of %s", coded.Fields, tt.path)
			}
			for _, want := range tt.has {
				if !strings.Contains(coded.Detail, want) {
					t.Errorf("detail %q does not hold %q", coded.Detail, want)
				}
			}
			if slices.Contains(h.docker.calls(), "RemoveContainer") {
				t.Error("a refused plan removed the container")
			}
		})
	}
}

// TestCpPlanStateHash: the hash is stable while the connector is and changes when its container disappears or the
// connector is gone, so a plan made before the change is refused at apply.
func TestCpPlanStateHash(t *testing.T) {
	h := newAGHost(t)
	c := cpDeploy(t, h, cpTunnel)
	hash := func(h *agHost) string {
		t.Helper()
		out, err := cpPlan(t, h, cpTunnel)
		if err != nil || out.StateHash == "" {
			t.Fatalf("plan: hash %q, error %v", out.StateHash, err)
		}
		return out.StateHash
	}
	first := hash(h)
	if again := hash(h); again != first {
		t.Errorf("a second plan hashes %s, want %s", again, first)
	}
	if empty := hash(newAGHost(t)); empty == first {
		t.Error("the hash of a host without the connector equals the hash of one with it")
	}
	cpDropContainer(t, h, c)
	if dropped := hash(h); dropped == first {
		t.Error("the hash did not change when the container disappeared")
	}
}

func TestCpApply(t *testing.T) {
	t.Run("removes the container, the file and the record of that tunnel only", func(t *testing.T) {
		h := newAGHost(t)
		cpDeploy(t, h, cpTunnel)
		cpDeploy(t, h, cpOther)
		if !cpEngineHas(t, h, cpTunnel) || !cpFileStored(h, cpTunnel) || !cpRowStored(t, h, cpTunnel) {
			t.Fatal("the connector run left no container, file or record to remove")
		}
		out, err := cpApply(t, h, "stop1", cpTunnel)
		if err != nil {
			t.Fatal(err)
		}
		if out != (ConnectorStopped{Tunnel: cpTunnel, Removed: true}) {
			t.Errorf("output %+v, want tunnel %s removed", out, cpTunnel)
		}
		if cpEngineHas(t, h, cpTunnel) || cpFileStored(h, cpTunnel) || cpRowStored(t, h, cpTunnel) {
			t.Error("the container, the file or the record of the stopped tunnel is still there")
		}
		if !cpEngineHas(t, h, cpOther) || !cpFileStored(h, cpOther) || !cpRowStored(t, h, cpOther) {
			t.Error("the stop removed part of another tunnel's connector")
		}
		plan, err := cpPlan(t, h, cpTunnel)
		if err != nil || len(plan.Diff) != 0 {
			t.Errorf("plan after the stop: diff %v, error %v, want an empty diff", plan.Diff, err)
		}
	})

	t.Run("a repeated stop converges", func(t *testing.T) {
		h := newAGHost(t)
		cpDeploy(t, h, cpTunnel)
		if _, err := cpApply(t, h, "stop1", cpTunnel); err != nil {
			t.Fatal(err)
		}
		out, err := cpApply(t, h, "stop2", cpTunnel)
		if err != nil {
			t.Fatalf("the second stop: %v", err)
		}
		if out != (ConnectorStopped{Tunnel: cpTunnel, Removed: false}) {
			t.Errorf("output %+v, want nothing removed", out)
		}
	})

	t.Run("finishes a stop whose container is already gone", func(t *testing.T) {
		h := newAGHost(t)
		c := cpDeploy(t, h, cpTunnel)
		cpDropContainer(t, h, c)
		out, err := cpApply(t, h, "stop1", cpTunnel)
		if err != nil {
			t.Fatal(err)
		}
		if !out.Removed || cpFileStored(h, cpTunnel) || cpRowStored(t, h, cpTunnel) {
			t.Errorf("output %+v, file stored %v, record stored %v, want the file and the record removed", out, cpFileStored(h, cpTunnel), cpRowStored(t, h, cpTunnel))
		}
	})

	t.Run("refused again at apply while a claim is held", func(t *testing.T) {
		h := newAGHost(t)
		cpDeploy(t, h, cpTunnel)
		cpClaim(t, h, cpTunnel, "appnet", "expose:app.example.com")
		_, err := cpApply(t, h, "stop1", cpTunnel)
		coded := cpRefused(t, err, errcode.DockerInUse)
		if !strings.Contains(coded.Detail, "expose:app.example.com") {
			t.Errorf("detail %q does not name the claimant", coded.Detail)
		}
		if slices.Contains(h.docker.calls(), "RemoveContainer") || !cpEngineHas(t, h, cpTunnel) || !cpFileStored(h, cpTunnel) || !cpRowStored(t, h, cpTunnel) {
			t.Error("a refused apply removed part of the connector")
		}
	})

	t.Run("an Engine failure keeps the record and a repeat finishes", func(t *testing.T) {
		h := newAGHost(t)
		cpDeploy(t, h, cpTunnel)
		h.docker.Fail("RemoveContainer", &dockerapi.APIError{StatusCode: 500, Message: "boom"})
		if _, err := cpApply(t, h, "stop1", cpTunnel); err == nil {
			t.Fatal("the stop succeeded although the Engine failed")
		}
		if !cpRowStored(t, h, cpTunnel) {
			t.Error("the record is gone although the container was not removed")
		}
		out, err := cpApply(t, h, "stop2", cpTunnel)
		if err != nil || !out.Removed {
			t.Fatalf("the repeated stop: %+v, %v", out, err)
		}
		if cpEngineHas(t, h, cpTunnel) || cpFileStored(h, cpTunnel) || cpRowStored(t, h, cpTunnel) {
			t.Error("the repeated stop left the container, the file or the record")
		}
	})
}

// TestCpVerify: the stop passes only when neither the container nor the record is left.
func TestCpVerify(t *testing.T) {
	ctx := context.Background()
	verify := func(t *testing.T, h *agHost) error {
		t.Helper()
		in := cpJSON(t, ConnectorStopInput{Tunnel: cpTunnel})
		return cpCF(h).connectorStop().Handler.Verify(ctx, h.env("stop1"), in, cpJSON(t, ConnectorStopped{Tunnel: cpTunnel, Removed: true}))
	}

	t.Run("a stopped connector", func(t *testing.T) {
		h := newAGHost(t)
		cpDeploy(t, h, cpTunnel)
		if verify(t, h) == nil {
			t.Error("verify passed while the connector runs")
		}
		if _, err := cpApply(t, h, "stop1", cpTunnel); err != nil {
			t.Fatal(err)
		}
		if err := verify(t, h); err != nil {
			t.Errorf("verify after the stop: %v", err)
		}
	})

	t.Run("the record is left", func(t *testing.T) {
		h := newAGHost(t)
		c := cpDeploy(t, h, cpTunnel)
		cpDropContainer(t, h, c)
		if err := verify(t, h); err == nil || !strings.Contains(err.Error(), "connector record") {
			t.Errorf("verify: %v, want an error naming the connector record", err)
		}
	})

	t.Run("the container is left", func(t *testing.T) {
		h := newAGHost(t)
		cpDeploy(t, h, cpTunnel)
		err := h.env("").DB.Write(ctx, func(tx *sql.Tx) error { return deleteConnector(ctx, tx, cpTunnel) })
		if err != nil {
			t.Fatal(err)
		}
		if err := verify(t, h); err == nil || !strings.Contains(err.Error(), "container "+connectorName(cpTunnel)) {
			t.Errorf("verify: %v, want an error naming the container", err)
		}
	})
}
