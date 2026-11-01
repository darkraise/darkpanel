package tailnet

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// nodeJoinKeySeconds is the lifetime of the auth key a join creates.
const nodeJoinKeySeconds = 3600

// NodeJoinInput is the input of tailnet.node.join.
type NodeJoinInput struct {
	HostID  string   `json:"hostId" jsonschema:"minLength=1"`
	Tags    []string `json:"tags,omitempty"`
	Install bool     `json:"install,omitempty"`
}

// NodeJoined is the output of tailnet.node.join.
type NodeJoined struct {
	BackendState string   `json:"backendState"`
	Addresses    []string `json:"addresses"`
}

func (t tn) nodeJoin() core.ActionDef {
	return core.DefineComposite[NodeJoinInput, NodeJoined](core.ActionSpec{
		Name: ID + ".node.join", Summary: "Join an enrolled host to the tailnet.", Version: 1,
		Unwind: core.UnwindReverse,
	}, nodeJoinComposer{})
}

// nodeJoinComposer joins a host in three steps: install tailscale when asked,
// create a one-use pre-authorized auth key, and log the host in with it.
type nodeJoinComposer struct{}

// Steps returns the join's steps for in.
func (nodeJoinComposer) Steps(_ context.Context, _ core.Env, in NodeJoinInput) ([]core.Step, error) {
	if len(in.Tags) == 0 {
		return nil, &errcode.Error{Code: errcode.InvalidInput, Detail: "the join input is invalid",
			Fields: []errcode.FieldError{{Path: "/tags", Message: "at least one tag is required"}}}
	}
	key, err := json.Marshal(AuthKeyInput{Tags: in.Tags, Preauthorized: true, ExpirySeconds: nodeJoinKeySeconds})
	if err != nil {
		return nil, fmt.Errorf("tailnet: %w", err)
	}
	var steps []core.Step
	loginAfter := []string{"key"}
	if in.Install {
		steps = append(steps, core.Step{
			ID: "install", Host: in.HostID,
			Action: core.ActionRef{Name: "tailscale.package.install", Version: 1},
			Input:  json.RawMessage(`{}`),
		})
		loginAfter = []string{"install", "key"}
	}
	// The engine refuses a literal in a secret field and stores a template in
	// clear, so the auth key reaches the login only as a reference to the key
	// step's private output.
	return append(steps,
		core.Step{
			ID: "key", Action: core.ActionRef{Name: ID + ".authkey.create", Version: 1}, Input: key,
		},
		core.Step{
			ID: "login", Host: in.HostID,
			Action: core.ActionRef{Name: "tailscale.node.login", Version: 1},
			Input:  json.RawMessage(`{"authKey":"${steps.key.output.key}"}`),
			After:  loginAfter,
		},
	), nil
}

// Output is the login step's backend state and addresses.
func (nodeJoinComposer) Output(_ context.Context, _ core.Env, _ NodeJoinInput, steps map[string]json.RawMessage) (NodeJoined, error) {
	raw, ok := steps["login"]
	if !ok {
		return NodeJoined{}, errors.New("tailnet: the join has no output of its login step")
	}
	var out NodeJoined
	if err := json.Unmarshal(raw, &out); err != nil {
		return NodeJoined{}, fmt.Errorf("tailnet: %w", err)
	}
	return out, nil
}
