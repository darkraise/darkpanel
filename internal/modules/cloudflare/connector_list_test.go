package cloudflare

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

const (
	clTunnelA = "0a000000-0000-4000-8000-00000000000a"
	clTunnelB = "0b000000-0000-4000-8000-00000000000b"
	clTunnelC = "0c000000-0000-4000-8000-00000000000c"
)

// clSeed records a connector row for tunnel with its token sealed as connector.run seals it.
func clSeed(t *testing.T, h *agHost, tunnel, containerID string) {
	t.Helper()
	ctx := context.Background()
	env := h.env("")
	sealed, err := env.Sealer.Seal(connectorSealAD(tunnel), []byte(agToken(tunnel)))
	if err != nil {
		t.Fatal(err)
	}
	err = env.DB.Write(ctx, func(tx *sql.Tx) error {
		return insertConnector(ctx, tx, connectorRow{TunnelID: tunnel, ContainerID: containerID, TokenCT: sealed, ChangeID: "c0", CreatedAt: env.Clock.Now()})
	})
	if err != nil {
		t.Fatal(err)
	}
}

// clState sets the container's state as the Engine reports it.
func clState(status string) func(*dockerapi.ContainerJSON) {
	return func(c *dockerapi.ContainerJSON) { c.State.Status, c.State.Running = status, status == "running" }
}

func clRead(t *testing.T, h *agHost) (json.RawMessage, error) {
	t.Helper()
	return h.cf().connectorList().Handler.Read(context.Background(), h.env(""), json.RawMessage(`{}`))
}

func TestClSpec(t *testing.T) {
	def := newAGHost(t).cf().connectorList()
	s := def.Spec
	if s.Name != "cloudflare.connector.list" || s.Version != 1 || s.Class != core.ClassRead || s.Side != core.SideAgent ||
		len(s.LockKeys) != 0 || s.Summary == "" || def.Handler.Read == nil {
		t.Errorf("spec %+v", s)
	}
	if core.RequiredScope(s) != core.ScopeRead {
		t.Errorf("scope %s, want read", core.RequiredScope(s))
	}
}

func TestClListReportsEveryRowWithItsContainerState(t *testing.T) {
	h := newAGHost(t)
	a := agContainer(connectorName(clTunnelA), clState("running"))
	b := agContainer(connectorName(clTunnelB), clState("exited"))
	stranger := agContainer("darkpanel-cloudflared-0d000000-0000-4000-8000-00000000000d", clState("running"))
	// A container with connector C's name that is not the container of C's row.
	impostor := agContainer(connectorName(clTunnelC), clState("running"))
	for _, c := range []dockerapi.ContainerJSON{a, b, stranger, impostor} {
		h.docker.putContainer(c)
	}
	clSeed(t, h, clTunnelB, b.ID)
	clSeed(t, h, clTunnelC, "the-container-that-was-removed")
	clSeed(t, h, clTunnelA, a.ID)

	raw, err := clRead(t, h)
	if err != nil {
		t.Fatal(err)
	}
	var got ConnectorList
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	want := []Connector{
		{Tunnel: clTunnelA, Container: connectorName(clTunnelA), ContainerID: a.ID, State: "running"},
		{Tunnel: clTunnelB, Container: connectorName(clTunnelB), ContainerID: b.ID, State: "exited"},
		{Tunnel: clTunnelC, Container: connectorName(clTunnelC), ContainerID: "the-container-that-was-removed", State: "missing"},
	}
	if !slices.Equal(got.Connectors, want) {
		t.Errorf("connectors %+v, want %+v", got.Connectors, want)
	}
	for _, c := range h.docker.calls() {
		if c != "InspectContainer" {
			t.Errorf("the read called %s, want only InspectContainer", c)
		}
	}
	for _, tunnel := range []string{clTunnelA, clTunnelB, clTunnelC} {
		if strings.Contains(string(raw), agToken(tunnel)) {
			t.Errorf("the answer carries the token of %s", tunnel)
		}
	}
}

func TestClListWithoutRowsIsAnEmptyList(t *testing.T) {
	raw, err := clRead(t, newAGHost(t))
	if err != nil {
		t.Fatal(err)
	}
	var members map[string]json.RawMessage
	if err := json.Unmarshal(raw, &members); err != nil || string(members["connectors"]) != "[]" {
		t.Errorf("answer %s (%v), want connectors to be an empty list", raw, err)
	}
}

func TestClListFailsWhenTheEngineCannotAnswer(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		code errcode.Code // "" for an error that need carry no code
	}{
		{"the Engine is down", dockerapi.ErrUnavailable, errcode.DockerEngineUnavailable},
		{"the Engine fails", &dockerapi.APIError{StatusCode: http.StatusInternalServerError, Message: "boom"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newAGHost(t)
			c := agContainer(connectorName(clTunnelA), clState("running"))
			h.docker.putContainer(c)
			clSeed(t, h, clTunnelA, c.ID)
			h.docker.Fail("InspectContainer", tc.err)
			raw, err := clRead(t, h)
			if err == nil || len(raw) != 0 {
				t.Fatalf("answer %s, error %v; want an error and no answer", raw, err)
			}
			if tc.code != "" && errcode.From(err).Code != tc.code {
				t.Errorf("error %v, want code %s", err, tc.code)
			}
		})
	}
}
