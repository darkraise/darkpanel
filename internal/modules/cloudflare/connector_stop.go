package cloudflare

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

// connectorStop is cloudflare.connector.stop: it removes the connector of one tunnel from this host, its container,
// its token file and its record, unless an exposure still has it attached to a network.
func (c cf) connectorStop() core.ActionDef {
	return core.DefineMutating[ConnectorStopInput, ConnectorStopped](core.ActionSpec{
		Name:          ID + ".connector.stop",
		Summary:       "Stop the cloudflared connector of a tunnel and remove its container, token file and record from this host.",
		Version:       1,
		Class:         core.ClassCompensable,
		Side:          core.SideAgent,
		LockKeys:      []string{"cloudflare"},
		CompensatedBy: ID + ".connector.run",
		Converges:     true,
	}, connectorStopHandler{c: c})
}

// connectorStopHandler is the handler of cloudflare.connector.stop.
type connectorStopHandler struct{ c cf }

// connectorStopTarget is what a stop finds of the tunnel's connector.
type connectorStopTarget struct {
	row         connectorRow
	stored      bool   // the connector's row exists
	containerID string // "" when the container does not exist
	running     bool
}

// inspect reads what the stop removes and refuses a bad tunnel id, a connector an exposure still claims or a container
// of the connector's name the module did not label. It only reads, so every failure is Definite.
func (h connectorStopHandler) inspect(ctx context.Context, env core.Env, tunnel string) (connectorStopTarget, error) {
	if p := tunnelIDProblem(tunnel); p != "" {
		return connectorStopTarget{}, core.Definite(&errcode.Error{
			Code: errcode.InvalidInput, Detail: p, Fields: []errcode.FieldError{{Path: "/tunnel", Message: p}},
		})
	}
	claims, err := connectorClaims(ctx, env.DB.Read(), tunnel, "")
	if err != nil {
		return connectorStopTarget{}, core.Definite(err)
	}
	if len(claims) > 0 {
		held := make([]string, len(claims))
		for i, cl := range claims {
			held[i] = cl.Claimant + " on " + cl.Network
		}
		return connectorStopTarget{}, core.Definite(errcode.New(errcode.DockerInUse, fmt.Sprintf(
			"connector %s is attached for %s; remove those exposures first", connectorName(tunnel), strings.Join(held, ", "))))
	}
	var t connectorStopTarget
	if t.row, t.stored, err = loadConnector(ctx, env.DB.Read(), tunnel); err != nil {
		return connectorStopTarget{}, core.Definite(err)
	}
	name := connectorName(tunnel)
	ctr, err := h.c.a.docker.InspectContainer(ctx, name)
	switch {
	case dockerapi.IsNotFound(err):
	case err != nil:
		return connectorStopTarget{}, core.Definite(fmt.Errorf("cloudflare: inspect container %s: %w", name, engineCoded(err)))
	default:
		if err := foreignConnector(ctr, name); err != nil {
			return connectorStopTarget{}, err
		}
		t.containerID, t.running = ctr.ID, ctr.State.Running
	}
	return t, nil
}

// Plan lists what is left of the connector to remove. The hash covers the row and the container found, so a plan made
// before the container disappears or is recreated is refused.
func (h connectorStopHandler) Plan(ctx context.Context, env core.Env, in ConnectorStopInput) (core.PlanOutcome, error) {
	t, err := h.inspect(ctx, env, in.Tunnel)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	diff := []core.DiffLine{}
	if t.containerID != "" {
		short := t.containerID[:min(len(t.containerID), 12)]
		diff = append(diff, core.DiffLine{Op: "-", Text: "container " + connectorName(in.Tunnel) + " (" + short + ")"})
	}
	if t.stored {
		diff = append(diff,
			core.DiffLine{Op: "-", Text: "token file " + tokenDir + "/" + tokenFileName(in.Tunnel)},
			core.DiffLine{Op: "-", Text: "the stored connector record of tunnel " + in.Tunnel},
		)
	}
	sum := sha256.Sum256([]byte(strings.Join([]string{
		strconv.FormatBool(t.stored), t.row.ContainerID, t.containerID, strconv.FormatBool(t.running),
	}, "\n")))
	return core.PlanOutcome{Diff: diff, Guards: []core.Guard{}, StateHash: hex.EncodeToString(sum[:])}, nil
}

// Apply checks the connector again and removes it. A failed removal is not Definite: part of it may have happened, and
// the record stays until the container is gone, so a repeated stop finishes it.
func (h connectorStopHandler) Apply(ctx context.Context, env core.Env, in ConnectorStopInput) (ConnectorStopped, error) {
	if _, err := h.inspect(ctx, env, in.Tunnel); err != nil {
		return ConnectorStopped{}, err
	}
	removed, err := removeConnector(ctx, env, h.c, in.Tunnel)
	if err != nil {
		return ConnectorStopped{}, err
	}
	return ConnectorStopped{Tunnel: in.Tunnel, Removed: removed}, nil
}

// Verify checks from the host alone that neither the connector's container nor its record is left.
func (h connectorStopHandler) Verify(ctx context.Context, env core.Env, in ConnectorStopInput, _ ConnectorStopped) error {
	name := connectorName(in.Tunnel)
	var left []string
	_, err := h.c.a.docker.InspectContainer(ctx, name)
	switch {
	case err == nil:
		left = append(left, "container "+name)
	case !dockerapi.IsNotFound(err):
		return fmt.Errorf("cloudflare: inspect container %s: %w", name, engineCoded(err))
	}
	_, ok, err := loadConnector(ctx, env.DB.Read(), in.Tunnel)
	if err != nil {
		return err
	}
	if ok {
		left = append(left, "the connector record")
	}
	if len(left) > 0 {
		return fmt.Errorf("cloudflare: the stop left %s", strings.Join(left, ", "))
	}
	return nil
}
