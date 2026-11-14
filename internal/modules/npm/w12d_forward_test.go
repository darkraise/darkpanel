package npm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/npm/npmapi"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

// w12dListingAPI answers ProxyHosts through list.
type w12dListingAPI struct {
	npmAPI
	list func(ctx context.Context) ([]npmapi.ProxyHost, error)
}

func (a w12dListingAPI) ProxyHosts(ctx context.Context) ([]npmapi.ProxyHost, error) {
	return a.list(ctx)
}

// w12dSlowDocker is the test Engine whose container inspections under a deadline wait for it, as an Engine that does
// not answer in time; inspections without a deadline answer at once.
type w12dSlowDocker struct{ *thDocker }

func (d w12dSlowDocker) InspectContainer(ctx context.Context, id string) (dockerapi.ContainerJSON, error) {
	if _, ok := ctx.Deadline(); ok {
		<-ctx.Done()
		return dockerapi.ContainerJSON{}, fmt.Errorf("dockerapi: GET /containers/%s/json: %w", id, &url.Error{Op: "Get", URL: "http://docker/containers/" + id + "/json", Err: ctx.Err()})
	}
	return d.thDocker.InspectContainer(ctx, id)
}

// TestW12dCancelledListingIsNotUnhealthy: a listing of the proxy hosts that fails because its caller went away is
// answered with the caller's error, not as an NPM that did not answer.
func TestW12dCancelledListingIsNotUnhealthy(t *testing.T) {
	h, _ := nwSetup(t)
	n := h.nm()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	inner := n.d.api
	n.d.api = func(ctx context.Context, env core.Env) (npmAPI, error) {
		live, err := inner(ctx, env)
		if err != nil {
			return nil, err
		}
		return w12dListingAPI{npmAPI: live, list: func(context.Context) ([]npmapi.ProxyHost, error) {
			cancel()
			return nil, fmt.Errorf("npmapi: GET /api/nginx/proxy-hosts: %w", context.Canceled)
		}}, nil
	}
	_, err := n.forwardTargets(ctx, h.env(""))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("forwardTargets = %v, want the caller's context.Canceled", err)
	}
	if code := errcode.From(err).Code; code == errcode.NPMUnhealthy {
		t.Errorf("a cancelled listing is answered %s", code)
	}
}

// TestW12dDockerNamesPastTheDeadline: a read of Docker's names that runs into the shared deadline while the caller
// still waits is a definite refusal to retry, as a resolver that does not answer is.
func TestW12dDockerNamesPastTheDeadline(t *testing.T) {
	h, user := nwSetup(t)
	w12aSubnet(t, h, user)
	w12aSeed(h, "slow.example.net", 3000)
	n := h.nm()
	n.d.docker = w12dSlowDocker{h.docker}
	_, err := n.forwardTargets(context.Background(), h.env(""))
	coded := nwRefused(t, err, errcode.Unavailable)
	if want := "Docker's names of the forward hosts could not be read in time; retry"; coded.Detail != want {
		t.Errorf("detail %q, want %q", coded.Detail, want)
	}
}

// TestA25_V2PortsStepSendsTheFirewallFixture: with the integration off, v2's ports step sends exactly the batch in
// the firewall module's fixture, the batch the firewall's A3 and A25 tests plan, so their proof holds for v2's own
// input.
func TestA25_V2PortsStepSendsTheFirewallFixture(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "firewall", "testdata", "w12d-deploy-public-v2-ports.json"))
	if err != nil {
		t.Fatal(err)
	}
	var want any
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatal(err)
	}
	in, _ := json.Marshal(qa12cIn(qa12cB(false), qa12cB(true), qa12cB(true)))
	var def core.ActionDef
	for _, a := range Module().Manifest().Actions {
		if a.Spec.Name == "npm.instance.deploy-public" && a.Spec.Version == 2 {
			def = a
		}
	}
	steps, err := def.Handler.Steps(context.Background(), core.Env{}, in)
	if err != nil {
		t.Fatal(err)
	}
	last := steps[len(steps)-1]
	var got any
	if err := json.Unmarshal(last.Input, &got); err != nil {
		t.Fatal(err)
	}
	if last.ID != "ports" || !reflect.DeepEqual(got, want) {
		t.Errorf("step %s sends %s, want the fixture %s", last.ID, last.Input, raw)
	}
}
