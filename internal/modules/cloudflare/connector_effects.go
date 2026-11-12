package cloudflare

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/docker/dstate"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

// The resource nouns of the connector effects. A resource is "<noun>:<identity>"; a creation not yet named is
// "<noun>:?<recovery value>".
const (
	nounTokenFile = "cloudflare.tokenfile"
	nounContainer = "cloudflare.container"
	nounConnector = "cloudflare.connector"
)

const (
	// engineCommitWait is how long the Engine may still commit a call the module no longer waits for.
	engineCommitWait = 2 * time.Minute
	// connectorStopGrace is the stop grace of the undo of a connector's start.
	connectorStopGrace = 30 * time.Second
)

// resourceOf is the resource of the object of noun that identity names.
func resourceOf(noun, identity string) string { return noun + ":" + identity }

// identityOf returns the part of a resource after its noun.
func identityOf(resource string) string {
	_, identity, _ := strings.Cut(resource, ":")
	return identity
}

// connectorEffect is the effect of one call. A creation expects the null state and is recovered by the name its "?"
// identity carries. The effect is not Local and has no After; the caller of a local effect sets both.
func connectorEffect(kind core.EffectKind, resource string, before json.RawMessage, wait time.Duration) core.Effect {
	e := core.Effect{
		Kind:       kind,
		Resource:   resource,
		CommitWait: wait,
		Before:     core.StateDoc{State: before},
	}
	if kind == core.EffectCreate {
		// The engine refuses a creation whose Before is not the null state.
		e.Before.State = json.RawMessage("null")
		if name, ok := strings.CutPrefix(identityOf(resource), "?"); ok {
			e.Recovery = &core.RecoveryID{Kind: "name", Value: name}
		}
	}
	return e
}

// refused reports whether err is an answer that proves the call committed nothing: a 4xx of the Engine.
func refused(err error) bool {
	return dockerapi.IsClientError(err)
}

// journal journals one call: it records e as intended, makes the call, and records it made with the state observe
// reads of the resource do names (or of e.Resource when do names none). A refusal (refused, or an error do marked
// Definite because a precondition failed before anything changed) skips the effect and returns a Definite error; any
// other failure leaves the effect intended for the engine to settle as uncertain.
func (c cf) journal(ctx context.Context, env core.Env, e core.Effect, observe func(context.Context, core.Env, core.Effect) (core.Observation, error),
	do func(ctx context.Context) (resource string, err error)) error {
	seq, err := env.Effects.Intend(ctx, e, nil)
	if err != nil {
		return fmt.Errorf("cloudflare: journal %s: %w", e.Resource, err)
	}
	resource, err := do(ctx)
	if err != nil {
		if !refused(err) && !core.IsDefinite(err) {
			return err
		}
		if skipErr := env.Effects.Skipped(ctx, seq); skipErr != nil {
			return errors.Join(err, fmt.Errorf("cloudflare: journal %s: %w", e.Resource, skipErr))
		}
		return core.Definite(err)
	}
	if resource == "" {
		resource = e.Resource
	}
	if strings.HasPrefix(identityOf(resource), "?") {
		return fmt.Errorf("cloudflare: the call that made %s did not name its object", resource)
	}
	made := e
	made.Resource = resource
	made.State = core.EffectIntended
	obs, err := observe(ctx, env, made)
	if err != nil {
		return fmt.Errorf("cloudflare: %s is unsettled: %w", resource, err)
	}
	if err := env.Effects.Made(ctx, seq, resource, obs.Current); err != nil {
		return fmt.Errorf("cloudflare: journal %s: %w", resource, err)
	}
	return nil
}

// nullState is the state of an object that does not exist.
func nullState() core.StateDoc { return core.StateDoc{State: json.RawMessage("null")} }

// named is the observation of an object its identity names.
func named(state json.RawMessage) core.Observation {
	return core.Observation{Current: core.StateDoc{State: state}, Matches: 1, Exclusive: true}
}

// effPhaseOf is the form a container is compared in. A creation is always compared in the restorable form: the module
// starts a container it created, and the recorded form of the creation would then no longer match. A modification is
// compared in the recorded form until it is being or was undone.
func effPhaseOf(e core.Effect) dstate.Phase {
	if e.Kind == core.EffectCreate || e.State == core.EffectUndoing || e.State == core.EffectUndone {
		return dstate.Restored
	}
	return dstate.Recorded
}

// effCreationOf splits a resource into its noun and the name its identity carries; creation is true for a "?"
// identity.
func effCreationOf(resource string) (noun, name string, creation bool, err error) {
	noun, identity, ok := strings.Cut(resource, ":")
	if !ok {
		return "", "", false, fmt.Errorf("cloudflare: %q is not a cloudflare resource", resource)
	}
	name, creation = strings.CutPrefix(identity, "?")
	if name == "" {
		return "", "", false, fmt.Errorf("cloudflare: %q names no object", resource)
	}
	return noun, name, creation, nil
}

// effTokenFileDoc is the state of a token file. It holds the file's size, never its content.
type effTokenFileDoc struct {
	Name string `json:"name"`
	Size int    `json:"size"`
}

// effConnectorDoc is the state of a connector row. The sealed token stays out of it.
type effConnectorDoc struct {
	Tunnel      string `json:"tunnel"`
	ContainerID string `json:"containerId"`
	ChangeID    string `json:"changeId"`
}

// effTokenFileState renders the token file of the tunnel holding size bytes.
func effTokenFileState(tunnel string, size int) (json.RawMessage, error) {
	doc, err := json.Marshal(effTokenFileDoc{Name: tokenFileName(tunnel), Size: size})
	if err != nil {
		return nil, fmt.Errorf("cloudflare: render the token file of tunnel %s: %w", tunnel, err)
	}
	return doc, nil
}

// effConnectorState renders the connector row r.
func effConnectorState(r connectorRow) (json.RawMessage, error) {
	doc, err := json.Marshal(effConnectorDoc{Tunnel: r.TunnelID, ContainerID: r.ContainerID, ChangeID: r.ChangeID})
	if err != nil {
		return nil, fmt.Errorf("cloudflare: render the connector of tunnel %s: %w", r.TunnelID, err)
	}
	return doc, nil
}

// effObservation is the observation of an object whose state is doc, nil when it does not exist. A creation that
// found its object names it as resource.
func effObservation(doc json.RawMessage, resource string, creation bool) core.Observation {
	switch {
	case doc == nil && creation:
		return core.Observation{Current: nullState()}
	case doc == nil:
		return named(nullState().State)
	}
	obs := named(doc)
	if creation {
		obs.Resource = resource
	}
	return obs
}

// Observe reads the object of one effect of the connector: the token file, the connector row or the container. It
// only reads.
func (h connectorRunHandler) Observe(ctx context.Context, env core.Env, e core.Effect) (core.Observation, error) {
	noun, name, creation, err := effCreationOf(e.Resource)
	if err != nil {
		return core.Observation{}, err
	}
	switch noun {
	case nounTokenFile:
		doc, err := h.c.effTokenFile(name)
		if err != nil {
			return core.Observation{}, err
		}
		return effObservation(doc, resourceOf(nounTokenFile, name), creation), nil
	case nounConnector:
		row, ok, err := loadConnector(ctx, env.DB.Read(), name)
		if err != nil {
			return core.Observation{}, err
		}
		if !ok {
			return effObservation(nil, "", creation), nil
		}
		doc, err := effConnectorState(row)
		if err != nil {
			return core.Observation{}, err
		}
		return effObservation(doc, resourceOf(nounConnector, name), creation), nil
	case nounContainer:
		return h.c.effContainer(ctx, name, creation, effPhaseOf(e))
	default:
		return core.Observation{}, fmt.Errorf("cloudflare: %q is not an object of a connector", e.Resource)
	}
}

// effTokenFile renders the token file of the tunnel, or returns nil when there is none. The tunnel is checked so that
// a resource can only ever name a file of the token directory.
func (c cf) effTokenFile(tunnel string) (json.RawMessage, error) {
	if p := tunnelIDProblem(tunnel); p != "" {
		return nil, fmt.Errorf("cloudflare: token file of %q: %s", tunnel, p)
	}
	data, err := c.a.readFile(tokenDir + "/" + tokenFileName(tunnel))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("cloudflare: read the token file of tunnel %s: %w", tunnel, err)
	}
	return effTokenFileState(tunnel, len(data))
}

// effContainer reads a container by its id, or a creation's container by its name, which Docker keeps unique.
func (c cf) effContainer(ctx context.Context, name string, creation bool, phase dstate.Phase) (core.Observation, error) {
	ctr, err := c.a.docker.InspectContainer(ctx, name)
	if dockerapi.IsNotFound(err) {
		return effObservation(nil, "", creation), nil
	}
	if err != nil {
		return core.Observation{}, fmt.Errorf("cloudflare: inspect container %s: %w", name, engineCoded(err))
	}
	if creation && ctr.Config.Labels[core.LabelModule] != ID {
		// The run refuses a name already taken, so an unlabelled container of the name is not its creation and must not
		// be adopted, and later undone, as one.
		return effObservation(nil, "", true), nil
	}
	doc, err := dstate.ContainerRun(ctr, phase)
	if err != nil {
		return core.Observation{}, fmt.Errorf("cloudflare: render container %s: %w", name, err)
	}
	return effObservation(doc, resourceOf(nounContainer, ctr.ID), creation), nil
}

// Inverse undoes one effect of the connector's run. An object already gone is undone; an Engine refusal changed
// nothing. A container the module did not label is refused, not removed.
func (h connectorRunHandler) Inverse(ctx context.Context, env core.Env, e core.Effect) (core.InverseResult, error) {
	noun, name, creation, err := effCreationOf(e.Resource)
	if err != nil {
		return core.InverseResult{}, err
	}
	if creation {
		return core.InverseResult{}, fmt.Errorf("cloudflare: %s names no made object", e.Resource)
	}
	docker := h.c.a.docker
	var what string
	switch {
	case noun == nounTokenFile && e.Kind == core.EffectCreate:
		if p := tunnelIDProblem(name); p != "" {
			return core.InverseResult{}, fmt.Errorf("cloudflare: token file of %q: %s", name, p)
		}
		if err := env.Files.RemoveManaged(ctx, ID, tokenDir, tokenFileName(name)); err != nil {
			return core.InverseResult{}, fmt.Errorf("cloudflare: remove the token file of tunnel %s: %w", name, err)
		}
		return core.InverseResult{}, nil
	case noun == nounConnector && e.Kind == core.EffectCreate:
		return core.InverseResult{}, env.DB.Write(ctx, func(tx *sql.Tx) error {
			row, ok, err := loadConnector(ctx, tx, name)
			if err != nil || !ok {
				return err
			}
			if row.ChangeID != e.ChangeID {
				return core.Definite(errcode.New(errcode.InvalidInput,
					fmt.Sprintf("the connector of tunnel %s was deployed by change %s, not by change %s", name, row.ChangeID, e.ChangeID)))
			}
			return deleteConnector(ctx, tx, name)
		})
	case noun == nounContainer && e.Kind == core.EffectModify:
		what = "stop container " + name
		_, err = docker.StopContainer(ctx, name, connectorStopGrace)
	case noun == nounContainer && e.Kind == core.EffectCreate:
		what = "remove container " + name
		var ctr dockerapi.ContainerJSON
		if ctr, err = docker.InspectContainer(ctx, name); err == nil {
			if err := foreignConnector(ctr, name); err != nil {
				return core.InverseResult{}, err
			}
			err = docker.RemoveContainer(ctx, name, dockerapi.RemoveContainerOptions{Force: true})
		}
	default:
		return core.InverseResult{}, fmt.Errorf("cloudflare: %s %s is not an effect of a connector's run", e.Kind, e.Resource)
	}
	switch {
	case err == nil, dockerapi.IsNotFound(err):
		return core.InverseResult{}, nil
	case refused(err):
		return core.InverseResult{}, core.Definite(fmt.Errorf("cloudflare: %s: %w", what, err))
	default:
		return core.InverseResult{}, fmt.Errorf("cloudflare: %s: %w", what, engineCoded(err))
	}
}
