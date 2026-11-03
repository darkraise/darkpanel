package npm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/darkraise/darkpanel/internal/core"
)

// adminExpose declares npm.admin.expose: NPM's own admin UI published over HTTPS on a hostname through the host's
// public instance. A failed step undoes the earlier ones in reverse.
func (n nm) adminExpose() core.ActionDef {
	return core.DefineComposite[AdminExposeInput, AdminExposeOutput](core.ActionSpec{
		Name: ID + ".admin.expose", Version: 1, Unwind: core.UnwindReverse,
		Summary: "Publish Nginx Proxy Manager's admin UI over HTTPS on a hostname, with a certificate, forced SSL and HSTS.",
	}, adminExposeComposer{})
}

// adminUnexpose declares npm.admin.unexpose: the removal of the proxy host that publishes NPM's admin UI. The engine
// repeats the step on repair instead of undoing it.
func (n nm) adminUnexpose() core.ActionDef {
	return core.DefineComposite[AdminUnexposeInput, AdminUnexposeOutput](core.ActionSpec{
		Name: ID + ".admin.unexpose", Version: 1, Unwind: core.UnwindForward,
		Summary: "Remove the proxy host that publishes Nginx Proxy Manager's admin UI; its certificate stays.",
	}, adminUnexposeComposer{})
}

// adminStepInput is a step template built from members. The engine reads every "${" of a template as a reference, so
// the hostname an operator typed reaches a template only as a reference to the composite's input, never as its text.
func adminStepInput(members map[string]any) (json.RawMessage, error) {
	raw, err := json.Marshal(members)
	if err != nil {
		return nil, fmt.Errorf("npm: %w", err)
	}
	return raw, nil
}

// adminStepID is the id of the object step id returned.
func adminStepID(steps map[string]json.RawMessage, id string) (int, error) {
	raw, ok := steps[id]
	if !ok {
		return 0, errors.New("npm: the admin exposure has no output of its " + id + " step")
	}
	var out struct {
		ID int `json:"id"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return 0, fmt.Errorf("npm: the admin exposure's %s output: %w", id, err)
	}
	return out.ID, nil
}

// adminExposeComposer exposes the admin UI: it requires a public instance, requests a certificate for the hostname,
// then adds the proxy host to NPM's admin port.
type adminExposeComposer struct{}

// Steps returns the admin exposure's steps for in.
func (adminExposeComposer) Steps(_ context.Context, _ core.Env, in AdminExposeInput) ([]core.Step, error) {
	public, err := adminStepInput(map[string]any{"hostId": "${input.hostId}"})
	if err != nil {
		return nil, err
	}
	cert, err := adminStepInput(map[string]any{"domains": []string{"${input.hostname}"}})
	if err != nil {
		return nil, err
	}
	// npm.proxyhost.add accepts the reserved admin upstream, and records the reservation, only because it runs as a
	// child of npm.admin.expose.
	proxy, err := adminStepInput(map[string]any{
		"domains":       []string{"${input.hostname}"},
		"forwardScheme": "http",
		"forwardHost":   "127.0.0.1",
		"forwardPort":   81,
		"certificateId": "${steps.cert.output.id}",
		"accessListId":  in.AccessListID,
		"sslForced":     true,
		"hsts":          true,
		"websockets":    false,
	})
	if err != nil {
		return nil, err
	}
	return []core.Step{
		{ID: "public", Action: core.ActionRef{Name: ID + ".public.check", Version: 1}, Input: public},
		{
			ID: "cert", Host: in.HostID, Action: core.ActionRef{Name: ID + ".certificate.request", Version: 1},
			Input: cert, After: []string{"public"},
		},
		{
			ID: "proxy", Host: in.HostID, Action: core.ActionRef{Name: ID + ".proxyhost.add", Version: 1},
			Input: proxy, After: []string{"cert"},
		},
	}, nil
}

// Output is the ids of the added proxy host and of its certificate.
func (adminExposeComposer) Output(_ context.Context, _ core.Env, _ AdminExposeInput, steps map[string]json.RawMessage) (AdminExposeOutput, error) {
	proxy, err := adminStepID(steps, "proxy")
	if err != nil {
		return AdminExposeOutput{}, err
	}
	cert, err := adminStepID(steps, "cert")
	if err != nil {
		return AdminExposeOutput{}, err
	}
	return AdminExposeOutput{ProxyHostID: proxy, CertificateID: cert}, nil
}

// adminUnexposeComposer removes the admin UI's proxy host. Its certificate stays until npm.certificate.remove deletes
// it.
type adminUnexposeComposer struct{}

// Steps returns the admin unexposure's step for in.
func (adminUnexposeComposer) Steps(_ context.Context, _ core.Env, in AdminUnexposeInput) ([]core.Step, error) {
	proxy, err := adminStepInput(map[string]any{"domain": "${input.hostname}"})
	if err != nil {
		return nil, err
	}
	return []core.Step{
		{ID: "proxy", Host: in.HostID, Action: core.ActionRef{Name: ID + ".proxyhost.remove", Version: 1}, Input: proxy},
	}, nil
}

// Output is the hostname whose admin exposure was removed.
func (adminUnexposeComposer) Output(_ context.Context, _ core.Env, in AdminUnexposeInput, steps map[string]json.RawMessage) (AdminUnexposeOutput, error) {
	if _, ok := steps["proxy"]; !ok {
		return AdminUnexposeOutput{}, errors.New("npm: the admin unexposure has no output of its proxy step")
	}
	return AdminUnexposeOutput{Hostname: in.Hostname}, nil
}
