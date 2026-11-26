package npm

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/npm/npmapi"
)

// proxyHostWebsocketsRepair maps each owning composite to the composite whose children may change the WebSocket flag
// of the proxy host it reserved.
var proxyHostWebsocketsRepair = map[string]string{proxyhostAddPanelOwner: "panel.websockets.repair"}

// ProxyHostWebsocketsInput is the input of npm.proxyhost.websockets: the proxy host serving Domain and the WebSocket
// flag it should have.
type ProxyHostWebsocketsInput struct {
	Domain     string `json:"domain"`
	Websockets bool   `json:"websockets"`
}

// proxyhostWebsockets is npm.proxyhost.websockets: it turns WebSocket upgrades on or off for a proxy host and leaves
// every other setting as NPM holds it.
func (n nm) proxyhostWebsockets() core.ActionDef {
	return core.DefineMutating[ProxyHostWebsocketsInput, ProxyHostView](core.ActionSpec{
		Name:      "npm.proxyhost.websockets",
		Summary:   "Turn WebSocket upgrades on or off for a proxy host.",
		Version:   1,
		Class:     core.ClassCompensable,
		Side:      core.SideAgent,
		LockKeys:  []string{lockKey},
		Converges: true,
		MinScope:  core.ScopeAdmin,
	}, proxyHostWebsocketsHandler{proxyHostEdit{n: n}})
}

// proxyHostWebsocketsHandler is the handler of npm.proxyhost.websockets.
type proxyHostWebsocketsHandler struct{ proxyHostEdit }

// refuse refuses with a Definite npm.reserved a reserved proxy host, unless the caller is the composite that may change
// the WebSocket flag of what the host's owner reserved. It returns the reservation's owner, "" for an unreserved host.
func (h proxyHostWebsocketsHandler) refuse(ctx context.Context, env core.Env, id int) (string, error) {
	row, ok, err := reservedOf(ctx, env.DB.Read(), "proxy_host", id)
	if err != nil {
		return "", core.Definite(err)
	}
	if !ok {
		return "", nil
	}
	if parent, known := proxyHostWebsocketsRepair[row.Owner]; !known || env.Call.ParentAction != parent {
		return "", core.Definite(errcode.New(errcode.NPMReserved, fmt.Sprintf("proxy host %d belongs to the exposure %s", id, row.Exposure)))
	}
	return row.Owner, nil
}

// check finds the host serving the input's domain and decides whether its flag may change; every refusal is Definite.
// The body it returns is the host's own with only the WebSocket flag set: the forward target, the locations and the
// advanced configuration go back exactly as NPM holds them, so no forward check applies.
func (h proxyHostWebsocketsHandler) check(ctx context.Context, env core.Env, in ProxyHostWebsocketsInput) (npmAPI, npmapi.ProxyHost, npmapi.ProxyHostBody, string, error) {
	if strings.TrimSpace(in.Domain) == "" {
		return nil, npmapi.ProxyHost{}, npmapi.ProxyHostBody{}, "", proxyhostAddInvalid([]errcode.FieldError{{Path: "/domain", Message: "domain is required"}})
	}
	api, err := h.client(ctx, env)
	if err != nil {
		return nil, npmapi.ProxyHost{}, npmapi.ProxyHostBody{}, "", err
	}
	hosts, err := api.ProxyHosts(ctx)
	if err != nil {
		return nil, npmapi.ProxyHost{}, npmapi.ProxyHostBody{}, "", proxyHostEditUnhealthy(err)
	}
	listed, ok := proxyHostRemoveFind(hosts, in.Domain)
	var host npmapi.ProxyHost
	if ok {
		host, ok, err = h.fetch(ctx, api, listed.ID)
		if err != nil {
			return nil, npmapi.ProxyHost{}, npmapi.ProxyHostBody{}, "", err
		}
	}
	if !ok {
		return nil, npmapi.ProxyHost{}, npmapi.ProxyHostBody{}, "", core.Definite(errcode.New(errcode.NotFound, fmt.Sprintf("no proxy host serves %s", in.Domain)))
	}
	owner, err := h.refuse(ctx, env, host.ID)
	if err != nil {
		return nil, npmapi.ProxyHost{}, npmapi.ProxyHostBody{}, "", err
	}
	body := proxyHostEditBody(host)
	body.AllowWebsocketUpgrade = in.Websockets
	return api, host, body, owner, nil
}

// websocketsDiff is the line pair of the flag's change, empty when the flag already has its value.
func websocketsDiff(from, to bool) []core.DiffLine {
	if from == to {
		return []core.DiffLine{}
	}
	onOff := map[bool]string{false: "off", true: "on"}
	return []core.DiffLine{{Op: "-", Text: "websockets: " + onOff[from]}, {Op: "+", Text: "websockets: " + onOff[to]}}
}

// Plan shows the flag's change; the hash covers the host's current state and the state the change sends.
func (h proxyHostWebsocketsHandler) Plan(ctx context.Context, env core.Env, in ProxyHostWebsocketsInput) (core.PlanOutcome, error) {
	_, host, body, _, err := h.check(ctx, env, in)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	current := proxyHostEditBody(host)
	return core.PlanOutcome{
		Diff:      websocketsDiff(current.AllowWebsocketUpgrade, body.AllowWebsocketUpgrade),
		Guards:    []core.Guard{},
		StateHash: proxyHostEditSum(proxyHostEditState(env, current), proxyHostEditState(env, body)),
	}, nil
}

// Apply sets the flag; a host that already has it is left alone.
func (h proxyHostWebsocketsHandler) Apply(ctx context.Context, env core.Env, in ProxyHostWebsocketsInput) (ProxyHostView, error) {
	api, host, body, owner, err := h.check(ctx, env, in)
	if err != nil {
		return ProxyHostView{}, err
	}
	if host.AllowWebsocketUpgrade == in.Websockets {
		return proxyHostEditView(host, owner), nil
	}
	current := proxyHostEditBody(host)
	e := npmEffect(core.EffectModify, resourceOf(nounProxyHost, strconv.Itoa(host.ID)), proxyHostEditState(env, current), commitWait)
	e.After = &core.StateDoc{State: proxyHostEditState(env, body)}
	secret, _ := json.Marshal(current)
	var updated npmapi.ProxyHost
	err = h.n.run(ctx, withEffectSecret(env, secret), e, h.Observe, func(ctx context.Context) (string, error) {
		u, err := api.UpdateProxyHost(ctx, host.ID, body)
		if err != nil {
			return "", proxyhostAddAnswer(err)
		}
		updated = u
		return e.Resource, nil
	})
	if err != nil {
		return ProxyHostView{}, err
	}
	return proxyHostEditView(updated, owner), nil
}

// Verify checks from in alone that the host serving the domain has the requested flag.
func (h proxyHostWebsocketsHandler) Verify(ctx context.Context, env core.Env, in ProxyHostWebsocketsInput, _ ProxyHostView) error {
	api, err := h.client(ctx, env)
	if err != nil {
		return err
	}
	hosts, err := api.ProxyHosts(ctx)
	if err != nil {
		return proxyHostEditUnhealthy(err)
	}
	host, ok := proxyHostRemoveFind(hosts, in.Domain)
	if !ok {
		return fmt.Errorf("npm: no proxy host serves %s", in.Domain)
	}
	if host.AllowWebsocketUpgrade != in.Websockets {
		return fmt.Errorf("npm: proxy host %d does not have the requested WebSocket setting", host.ID)
	}
	return nil
}

// Inverse sends the host the body it had before the change. The body is what NPM held, so it is written back without
// the forward check, which would refuse the panel's upstream; the reservation is checked again.
func (h proxyHostWebsocketsHandler) Inverse(ctx context.Context, env core.Env, e core.Effect) (core.InverseResult, error) {
	id, err := proxyHostEditID(e.Resource)
	if err != nil {
		return core.InverseResult{}, core.Definite(err)
	}
	body, err := h.undoBody(ctx, env, e)
	if err != nil {
		return core.InverseResult{}, err
	}
	if _, err := h.refuse(ctx, env, id); err != nil {
		return core.InverseResult{}, err
	}
	api, err := h.client(ctx, env)
	if err != nil {
		return core.InverseResult{}, err
	}
	_, err = api.UpdateProxyHost(ctx, id, body)
	switch {
	case err == nil:
		return core.InverseResult{}, nil
	case npmapi.IsNotFound(err):
		return core.InverseResult{}, core.Definite(errcode.New(errcode.NotFound, fmt.Sprintf("proxy host %d no longer exists", id)))
	case npmapi.IsClientError(err):
		return core.InverseResult{}, core.Definite(proxyhostAddAnswer(err))
	default:
		return core.InverseResult{}, err
	}
}
