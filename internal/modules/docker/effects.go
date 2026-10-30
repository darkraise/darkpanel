package docker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

// The resource nouns of the docker effects. A resource is "<noun>:<identity>";
// a network or volume creation not yet named is "<noun>:?<name>".
const (
	nounContainer = "docker.container"
	nounEndpoint  = "docker.endpoint"
	nounNetwork   = "docker.network"
	nounVolume    = "docker.volume"
)

func containerResource(id string) string { return nounContainer + ":" + id }

func endpointResource(containerID, networkID string) string {
	return nounEndpoint + ":" + containerID + "@" + networkID
}

func networkResource(id string) string { return nounNetwork + ":" + id }

func volumeResource(name string) string { return nounVolume + ":" + name }

// identityOf returns the part of a resource after its noun.
func identityOf(resource string) string {
	_, identity, _ := strings.Cut(resource, ":")
	return identity
}

// dockerEffect is the effect of one Engine call. The post-state is not known
// before the call, so it has no After.
func dockerEffect(kind core.EffectKind, resource string, before json.RawMessage) core.Effect {
	e := core.Effect{
		Kind:       kind,
		Resource:   resource,
		CommitWait: commitWait,
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

// run journals one Engine call: it records e as intended, makes the call, and
// records it made with the Recorded post-state of the resource do names (or
// e.Resource when do names none). A 4xx answer means the daemon refused before
// changing anything, so the effect is skipped and the error is Definite; any
// other failure leaves the effect intended for the engine to settle as
// uncertain.
func (k dk) run(ctx context.Context, env core.Env, e core.Effect, do func(ctx context.Context) (resource string, err error)) error {
	seq, err := env.Effects.Intend(ctx, e, nil)
	if err != nil {
		return fmt.Errorf("docker: journal %s: %w", e.Resource, err)
	}
	resource, err := do(ctx)
	if err != nil {
		var refused *dockerapi.APIError
		if !errors.As(err, &refused) || refused.StatusCode < http.StatusBadRequest || refused.StatusCode >= http.StatusInternalServerError {
			return err
		}
		if skipErr := env.Effects.Skipped(ctx, seq); skipErr != nil {
			return errors.Join(err, fmt.Errorf("docker: journal %s: %w", e.Resource, skipErr))
		}
		return core.Definite(err)
	}
	if resource == "" {
		resource = e.Resource
	}
	if strings.HasPrefix(identityOf(resource), "?") {
		return fmt.Errorf("docker: the call that made %s did not name its object", resource)
	}
	made := e
	made.Resource = resource
	made.State = core.EffectIntended
	obs, err := k.observe(ctx, env, made)
	if err != nil {
		return fmt.Errorf("docker: %s is unsettled: %w", resource, err)
	}
	if err := env.Effects.Made(ctx, seq, resource, obs.Current); err != nil {
		return fmt.Errorf("docker: journal %s: %w", resource, err)
	}
	return nil
}

// engineError maps the dockerapi errors an action must tell its caller about
// to coded errors and returns any other error unchanged. The input stays the
// cause, so a Definite input stays Definite.
func engineError(kind, name string, err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, dockerapi.ErrUnavailable):
		return errcode.Wrap(errcode.DockerEngineUnavailable, err, "the Docker Engine is not reachable")
	case dockerapi.IsNotFound(err):
		return errcode.Wrap(errcode.NotFound, err, kind+" "+name+" not found")
	case errors.Is(err, dockerapi.ErrEmptyID):
		return errcode.Wrap(errcode.InvalidInput, err, kind+" name or id is empty")
	default:
		return err
	}
}
