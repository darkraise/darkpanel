package panel

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/schema"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

const nwTunnel = "0123abcd-0123-4abc-8def-0123456789ab"

// nwConnector is the connector container of nwTunnel, running and labelled darkpanel.module=cloudflare.
func nwConnector() dockerapi.ContainerJSON {
	return dockerapi.ContainerJSON{
		ID: "ctr1", Name: "/darkpanel-cloudflared-" + nwTunnel,
		State:  dockerapi.ContainerState{Status: "running", Running: true},
		Config: dockerapi.ContainerConfig{Labels: map[string]string{core.LabelModule: "cloudflare"}},
	}
}

// TestNwConnectorCheck: the check passes for a running connector of the tunnel that the cloudflare module made, and
// refuses every other case with cloudflare.connector_missing, a bad tunnel with request.invalid_input and an unreachable
// Engine with docker.engine_unavailable.
func TestNwConnectorCheck(t *testing.T) {
	tests := []struct {
		name   string
		mod    func(c *dockerapi.ContainerJSON)
		setup  func(d *fdDocker)
		tunnel string       // "" means nwTunnel
		empty  bool         // the input names no tunnel at all
		absent bool         // the container does not exist
		code   errcode.Code // "" when the check passes
		has    string
		field  string
	}{
		{name: "running"},
		{name: "container missing", mod: nil, setup: func(d *fdDocker) {
			d.Fail("InspectContainer", &dockerapi.APIError{StatusCode: 404, Message: "No such container"})
		},
			code: errcode.CloudflareConnectorMissing, has: "does not exist"},
		{name: "another tunnel's connector", tunnel: "11111111-1111-4111-8111-111111111111", code: errcode.CloudflareConnectorMissing, has: "does not exist"},
		{name: "no label", mod: func(c *dockerapi.ContainerJSON) { c.Config.Labels = nil }, code: errcode.CloudflareConnectorMissing, has: "darkpanel.module=cloudflare"},
		{name: "label of another module", mod: func(c *dockerapi.ContainerJSON) { c.Config.Labels = map[string]string{core.LabelModule: "npm"} },
			code: errcode.CloudflareConnectorMissing, has: "darkpanel.module=cloudflare"},
		{name: "stopped", mod: func(c *dockerapi.ContainerJSON) { c.State = dockerapi.ContainerState{Status: "exited"} },
			code: errcode.CloudflareConnectorMissing, has: "exited"},
		{name: "restarting", mod: func(c *dockerapi.ContainerJSON) { c.State = dockerapi.ContainerState{Status: "restarting"} },
			code: errcode.CloudflareConnectorMissing, has: "restarting"},
		{name: "tunnel not a UUID", tunnel: "my-tunnel", code: errcode.InvalidInput, field: "/tunnel"},
		{name: "empty tunnel", empty: true, code: errcode.InvalidInput, field: "/tunnel"},
		{name: "engine unavailable", setup: func(d *fdDocker) { d.Fail("InspectContainer", dockerapi.ErrUnavailable) }, code: errcode.DockerEngineUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := fdNewDocker()
			c := nwConnector()
			if tt.mod != nil {
				tt.mod(&c)
			}
			if !tt.absent {
				d.AddContainer(c)
			}
			if tt.setup != nil {
				tt.setup(d)
			}
			tunnel := nwTunnel
			switch {
			case tt.empty:
				tunnel = ""
			case tt.tunnel != "":
				tunnel = tt.tunnel
			}
			def := connectorCheckAction(nwDeps(d))
			identity, raw, err := def.Handler.Reveal(context.Background(), nwEnv(nil), nwJSON(t, ConnectorCheckInput{Tunnel: tunnel}))
			if tt.code == "" {
				var out ConnectorCheckOutput
				if err != nil || json.Unmarshal(raw, &out) != nil || out != (ConnectorCheckOutput{Container: "darkpanel-cloudflared-" + nwTunnel, State: "running"}) {
					t.Fatalf("output %s, error %v; want the running connector", raw, err)
				}
				if !strings.Contains(identity, nwTunnel) {
					t.Errorf("identity %q does not name the tunnel", identity)
				}
				return
			}
			coded := nwRefusal(t, err, tt.code)
			if tt.has != "" && !strings.Contains(coded.Detail, tt.has) {
				t.Errorf("detail %q does not hold %q", coded.Detail, tt.has)
			}
			if tt.field != "" && (len(coded.Fields) != 1 || coded.Fields[0].Path != tt.field) {
				t.Errorf("fields %v, want one at %s", coded.Fields, tt.field)
			}
		})
	}
}

// TestNwConnectorCheckDeclaration: the check is an internal read on the agent for admins, without locks, and its
// schemas reflect.
func TestNwConnectorCheckDeclaration(t *testing.T) {
	def := connectorCheckAction(nwDeps(fdNewDocker()))
	s := def.Spec
	if s.Name != "panel.connector.check" || s.Version != 1 || s.Class != core.ClassRead || s.Side != core.SideAgent || !s.Internal ||
		s.MinScope != core.ScopeAdmin || len(s.LockKeys) != 0 {
		t.Errorf("spec %+v", s)
	}
	if def.Handler.Reveal == nil || def.Handler.Read != nil {
		t.Error("the check is not an internal read")
	}
	if _, err := schema.Input(def.InputType); err != nil {
		t.Error(err)
	}
	if _, err := schema.Output(def.OutputType); err != nil {
		t.Error(err)
	}
	if _, err := core.NewRegistry(nwModule{defs: []core.ActionDef{def}}); err != nil {
		t.Errorf("registry: %v", err)
	}
}
