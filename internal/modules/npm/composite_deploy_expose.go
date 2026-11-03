package npm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules/firewall"
	"github.com/darkraise/darkpanel/internal/modules/firewall/dockerfw"
)

// exposeClaimantPrefix starts the claimant of an exposure's network claim; the claimant ends with one of its domains.
const exposeClaimantPrefix = "expose:"

// instanceDeployPublic declares npm.instance.deploy-public: the checked deployment of NPM on a host, with its public
// ports opened when the host's Docker firewall integration is on. A failed step undoes the earlier ones in reverse.
func (n nm) instanceDeployPublic() core.ActionDef {
	return core.DefineComposite[DeployPublicInput, DeployPublicOutput](core.ActionSpec{
		Name: ID + ".instance.deploy-public", Version: 1, Unwind: core.UnwindReverse,
		Summary: "Deploy Nginx Proxy Manager and, when the host's Docker firewall integration is on, open ports 80 and 443 for it.",
	}, deployPublicComposer{})
}

// exposeContainer declares npm.expose.container: a container served on its domains through the host's public NPM
// instance. A failed step undoes the earlier ones in reverse.
func (n nm) exposeContainer() core.ActionDef {
	return core.DefineComposite[ExposeInput, ExposeOutput](core.ActionSpec{
		Name: ID + ".expose.container", Version: 1, Unwind: core.UnwindReverse,
		Summary: "Attach the proxy to a container's network, request a certificate and add a proxy host that forwards to the container.",
	}, exposeContainerComposer{})
}

// exposeRemove declares npm.expose.remove: the removal of an exposed container's proxy host and network claims. The
// engine repeats the steps on repair instead of undoing them.
func (n nm) exposeRemove() core.ActionDef {
	return core.DefineComposite[ExposeRemoveInput, ExposeRemoveOutput](core.ActionSpec{
		Name: ID + ".expose.remove", Version: 1, Unwind: core.UnwindForward,
		Summary: "Remove an exposed container's proxy host and its network claims.",
	}, exposeRemoveComposer{})
}

// exposeStepInput is a step template built from members. The engine reads every "${" of a template as a reference, so
// what an operator typed reaches a template only as a reference to the composite's input, never as its text.
func exposeStepInput(members map[string]any) (json.RawMessage, error) {
	raw, err := json.Marshal(members)
	if err != nil {
		return nil, fmt.Errorf("npm: %w", err)
	}
	return raw, nil
}

// exposeStepID is the id of the object step id returned.
func exposeStepID(steps map[string]json.RawMessage, id string) (int, error) {
	raw, ok := steps[id]
	if !ok {
		return 0, errors.New("npm: the exposure has no output of its " + id + " step")
	}
	var out struct {
		ID int `json:"id"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return 0, fmt.Errorf("npm: the exposure's %s output: %w", id, err)
	}
	return out.ID, nil
}

// deployPublicComposer deploys NPM after checking the host, then opens ports 80 and 443 when the integration is on.
type deployPublicComposer struct{}

// Steps returns the deployment's steps for in.
func (deployPublicComposer) Steps(_ context.Context, _ core.Env, in DeployPublicInput) ([]core.Step, error) {
	check, err := exposeStepInput(map[string]any{"hostId": "${input.hostId}", "integration": in.Integration})
	if err != nil {
		return nil, err
	}
	deploy, err := exposeStepInput(map[string]any{"acmeEmail": "${input.acmeEmail}"})
	if err != nil {
		return nil, err
	}
	steps := []core.Step{
		{ID: "check", Action: core.ActionRef{Name: ID + ".instance.check", Version: 1}, Input: check},
		{
			ID: "deploy", Host: in.HostID, Action: core.ActionRef{Name: ID + ".instance.deploy", Version: 1},
			Input: deploy, After: []string{"check"},
		},
	}
	if !in.Integration {
		return steps, nil
	}
	allow := func(port uint16) firewall.ContainerOp {
		return firewall.ContainerOp{Op: "allow", Binding: &dockerfw.Binding{Selector: containerName, HostPort: port, Proto: "tcp"}}
	}
	ports, err := json.Marshal(struct {
		Ops []firewall.ContainerOp `json:"ops"`
	}{[]firewall.ContainerOp{allow(80), allow(443)}})
	if err != nil {
		return nil, fmt.Errorf("npm: %w", err)
	}
	// firewall.containers.apply is revertible, and the engine accepts a
	// revertible step only as a composite's last.
	return append(steps, core.Step{
		ID: "ports", Host: in.HostID, Action: core.ActionRef{Name: "firewall.containers.apply", Version: 1},
		Input: ports, After: []string{"deploy"},
	}), nil
}

// Output is the deployed container and whether the ports were opened.
func (deployPublicComposer) Output(_ context.Context, _ core.Env, in DeployPublicInput, steps map[string]json.RawMessage) (DeployPublicOutput, error) {
	raw, ok := steps["deploy"]
	if !ok {
		return DeployPublicOutput{}, errors.New("npm: the deployment has no output of its deploy step")
	}
	var out Deployed
	if err := json.Unmarshal(raw, &out); err != nil {
		return DeployPublicOutput{}, fmt.Errorf("npm: the deployment's deploy output: %w", err)
	}
	return DeployPublicOutput{ContainerID: out.ContainerID, PortsOpened: in.Integration}, nil
}

// exposeContainerComposer exposes a container: it requires a public instance, attaches the proxy to the container's
// network and requests a certificate, then adds the proxy host.
type exposeContainerComposer struct{}

// Steps returns the exposure's steps for in.
func (exposeContainerComposer) Steps(_ context.Context, _ core.Env, in ExposeInput) ([]core.Step, error) {
	public, err := exposeStepInput(map[string]any{"hostId": "${input.hostId}"})
	if err != nil {
		return nil, err
	}
	// The claim is named by the smallest domain because the removal names it from the removed proxy host's domains,
	// which npm.proxyhost.add stores sorted, whichever of them the removal is given.
	smallest := 0
	for i, d := range in.Domains {
		if d < in.Domains[smallest] {
			smallest = i
		}
	}
	network, err := exposeStepInput(map[string]any{
		"network":  "${input.network}",
		"claimant": exposeClaimantPrefix + "${input.domains." + strconv.Itoa(smallest) + "}",
		"target":   "${input.container}",
	})
	if err != nil {
		return nil, err
	}
	cert, err := exposeStepInput(map[string]any{"domains": "${input.domains}"})
	if err != nil {
		return nil, err
	}
	proxy, err := exposeStepInput(map[string]any{
		"domains":       "${input.domains}",
		"forwardScheme": "http",
		"forwardHost":   "${input.container}",
		"forwardPort":   in.Port,
		"certificateId": "${steps.cert.output.id}",
		"accessListId":  in.AccessListID,
		"sslForced":     true,
		"hsts":          true,
		"websockets":    in.Websockets,
	})
	if err != nil {
		return nil, err
	}
	return []core.Step{
		{ID: "public", Action: core.ActionRef{Name: ID + ".public.check", Version: 1}, Input: public},
		{
			ID: "network", Host: in.HostID, Action: core.ActionRef{Name: ID + ".network.attach", Version: 1},
			Input: network, After: []string{"public"},
		},
		{
			ID: "cert", Host: in.HostID, Action: core.ActionRef{Name: ID + ".certificate.request", Version: 1},
			Input: cert, After: []string{"public"},
		},
		{
			ID: "proxy", Host: in.HostID, Action: core.ActionRef{Name: ID + ".proxyhost.add", Version: 1},
			Input: proxy, After: []string{"network", "cert"},
		},
	}, nil
}

// Output is the ids of the added proxy host and of its certificate.
func (exposeContainerComposer) Output(_ context.Context, _ core.Env, _ ExposeInput, steps map[string]json.RawMessage) (ExposeOutput, error) {
	proxy, err := exposeStepID(steps, "proxy")
	if err != nil {
		return ExposeOutput{}, err
	}
	cert, err := exposeStepID(steps, "cert")
	if err != nil {
		return ExposeOutput{}, err
	}
	return ExposeOutput{ProxyHostID: proxy, CertificateID: cert}, nil
}

// exposeRemoveComposer removes an exposure: its proxy host, then every network claim of its claimant.
type exposeRemoveComposer struct{}

// Steps returns the removal's steps for in.
func (exposeRemoveComposer) Steps(_ context.Context, _ core.Env, in ExposeRemoveInput) ([]core.Step, error) {
	proxy, err := exposeStepInput(map[string]any{"domain": "${input.domain}"})
	if err != nil {
		return nil, err
	}
	// The exposure named its claim by its smallest domain, which is the first of the removed host's domains because
	// npm.proxyhost.add stores them sorted, so a host exposed on several domains loses its claim whichever domain the
	// removal is given.
	network, err := exposeStepInput(map[string]any{
		"network":  "",
		"claimant": exposeClaimantPrefix + "${steps.proxy.output.domains.0}",
	})
	if err != nil {
		return nil, err
	}
	return []core.Step{
		{ID: "proxy", Host: in.HostID, Action: core.ActionRef{Name: ID + ".proxyhost.remove", Version: 1}, Input: proxy},
		{
			ID: "network", Host: in.HostID, Action: core.ActionRef{Name: ID + ".network.detach", Version: 1},
			Input: network, After: []string{"proxy"},
		},
	}, nil
}

// Output is the removed exposure's domain.
func (exposeRemoveComposer) Output(_ context.Context, _ core.Env, in ExposeRemoveInput, steps map[string]json.RawMessage) (ExposeRemoveOutput, error) {
	if _, ok := steps["network"]; !ok {
		return ExposeRemoveOutput{}, errors.New("npm: the removal has no output of its network step")
	}
	return ExposeRemoveOutput{Domain: in.Domain}, nil
}
