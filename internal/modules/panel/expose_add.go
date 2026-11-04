package panel

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// The actions of other modules that panel.exposure.add runs as steps.
const (
	addPublicCheck   = "npm.public.check"
	addNPMAttach     = "npm.network.attach"
	addCertificate   = "npm.certificate.request"
	addProxyHost     = "npm.proxyhost.add"
	addTunnelAttach  = "cloudflare.network.attach"
	addTunnelHost    = "cloudflare.hostname.add"
	addFirewallApply = "firewall.rules.apply"
)

// addLabelMax is the longest label firewall.rules.apply accepts.
const addLabelMax = 64

// exposureAddAction declares panel.exposure.add: the exposure of the panel on the controller's host through Nginx
// Proxy Manager or a Cloudflare tunnel. A failed step undoes the earlier ones in reverse.
func exposureAddAction() core.ActionDef {
	return core.DefineComposite[ExposeInput, ExposeOutput](core.ActionSpec{
		Name: ActionAdd, Version: 1, Unwind: core.UnwindReverse, MinScope: core.ScopeAdmin,
		Summary: "Expose the panel on the controller's host through Nginx Proxy Manager or a Cloudflare tunnel.",
	}, addComposer{})
}

// addComposer exposes the panel: it checks the public instance or the tunnel's connector, creates the panel network
// and attaches the proxy to it, opens the listener, routes the hostname to it and lets the proxy reach it.
type addComposer struct{}

// addStepInput is a step template built from members. The engine reads every "${" of a template as a reference, so
// what an operator typed reaches a template only as a reference to the composite's input, never as its text.
func addStepInput(members map[string]any) (json.RawMessage, error) {
	raw, err := json.Marshal(members)
	if err != nil {
		return nil, fmt.Errorf("panel: %w", err)
	}
	return raw, nil
}

// exposureLabel is the firewall label of the exposure of hostname, cut to the length the firewall accepts. The firewall
// checks the label even when the input only deletes, so a longer one would fail the removal's last step after the
// revocation.
func exposureLabel(hostname string) string {
	label := "panel exposure " + hostname
	if len(label) > addLabelMax {
		label = label[:addLabelMax]
	}
	return label
}

// addRefuse refuses an exposure the controller cannot take: a host other than its own, a user still to enrol TOTP,
// or an exposure that is still live.
func addRefuse(ctx context.Context, env core.Env, in ExposeInput) error {
	db := env.DB.Read()
	var local string
	err := db.QueryRowContext(ctx, `SELECT id FROM core_hosts WHERE is_local = 1`).Scan(&local)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("panel: read the controller's host: %w", err)
	}
	if local != in.HostID {
		return errcode.New(errcode.PanelWrongHost, fmt.Sprintf("host %s is not the controller's host", in.HostID))
	}
	var pending int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM core_users WHERE state = 'pending'`).Scan(&pending); err != nil {
		return fmt.Errorf("panel: count the users still to enrol TOTP: %w", err)
	}
	if pending > 0 {
		verb := "have"
		if pending == 1 {
			verb = "has"
		}
		return errcode.New(errcode.PanelTOTPIncomplete, fmt.Sprintf("every user must enrol TOTP before the panel is exposed, and %d %s not", pending, verb))
	}
	row, live, err := liveExposure(ctx, db)
	if err != nil {
		return err
	}
	if live {
		return errcode.New(errcode.PanelAlreadyExposed, fmt.Sprintf("the panel is already exposed at %s", row.Hostname))
	}
	return nil
}

// Steps returns the exposure's steps for in, after refusing an input or a state the exposure cannot take.
func (addComposer) Steps(ctx context.Context, env core.Env, in ExposeInput) ([]core.Step, error) {
	// The validation comes first: it keeps "$" and "{" out of the hostname that the label below writes literally.
	if err := ValidateExpose(in); err != nil {
		return nil, err
	}
	if err := addRefuse(ctx, env, in); err != nil {
		return nil, err
	}
	port := in.Port
	if port == 0 {
		port = DefaultPort
	}
	tunnel := in.Via == ViaTunnel

	type template struct {
		id, host, action string
		members          map[string]any
	}
	var tmpls []template
	if tunnel {
		tmpls = append(tmpls, template{"connector", in.HostID, ActionConnCheck, map[string]any{"tunnel": "${input.tunnel}"}})
	} else {
		tmpls = append(tmpls, template{"public", "", addPublicCheck, map[string]any{"hostId": "${input.hostId}"}})
	}
	tmpls = append(tmpls, template{"network", in.HostID, ActionNetCreate, map[string]any{}})
	attach := map[string]any{"network": NetworkName, "claimant": Claimant, "address": "${steps.network.output.proxy}"}
	listen := map[string]any{
		"via": in.Via, "hostname": "${input.hostname}", "hostId": "${input.hostId}",
		"subnet": "${steps.network.output.subnet}", "gateway": "${steps.network.output.gateway}",
		"proxy": "${steps.network.output.proxy}", "port": port,
	}
	if tunnel {
		attach["tunnel"] = "${input.tunnel}"
		listen["tunnel"] = "${input.tunnel}"
		tmpls = append(tmpls,
			template{"attach", in.HostID, addTunnelAttach, attach},
			template{"listen", "", ActionOpen, listen},
			template{"proxy", "", addTunnelHost, map[string]any{
				"tunnel": "${input.tunnel}", "hostname": "${input.hostname}",
				"service": "https://${steps.network.output.gateway}:" + strconv.Itoa(port), "noTLSVerify": true,
			}},
		)
	} else {
		tmpls = append(tmpls,
			template{"attach", in.HostID, addNPMAttach, attach},
			template{"listen", "", ActionOpen, listen},
			template{"cert", in.HostID, addCertificate, map[string]any{"domains": []string{"${input.hostname}"}}},
			template{"proxy", in.HostID, addProxyHost, map[string]any{
				"domains": []string{"${input.hostname}"}, "forwardScheme": "https",
				"forwardHost": "${steps.network.output.gateway}", "forwardPort": port,
				"certificateId": "${steps.cert.output.id}", "sslForced": true, "hsts": true,
			}},
		)
	}
	tmpls = append(tmpls, template{"ports", in.HostID, addFirewallApply, map[string]any{
		"label": exposureLabel(in.Hostname),
		"add": []map[string]any{{
			"id": "${steps.listen.output.ruleId}", "action": "allow", "direction": "in", "proto": "tcp",
			"from": "${steps.network.output.proxy}", "to": "${steps.network.output.gateway}",
			"toPort": strconv.Itoa(port), "inIface": BridgeName, "family": "v4",
		}},
	}})

	steps := make([]core.Step, 0, len(tmpls))
	for i, t := range tmpls {
		input, err := addStepInput(t.members)
		if err != nil {
			return nil, err
		}
		s := core.Step{ID: t.id, Host: t.host, Action: core.ActionRef{Name: t.action, Version: 1}, Input: input}
		if i > 0 {
			s.After = []string{tmpls[i-1].id}
		}
		steps = append(steps, s)
	}
	return steps, nil
}

// addStepOutput decodes the output of step id into v.
func addStepOutput(steps map[string]json.RawMessage, id string, v any) error {
	raw, ok := steps[id]
	if !ok {
		return errors.New("panel: the exposure has no output of its " + id + " step")
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return fmt.Errorf("panel: the exposure's %s output: %w", id, err)
	}
	return nil
}

// Output is the exposure as the network and listen steps made it.
func (addComposer) Output(_ context.Context, _ core.Env, in ExposeInput, steps map[string]json.RawMessage) (ExposeOutput, error) {
	var network NetworkView
	if err := addStepOutput(steps, "network", &network); err != nil {
		return ExposeOutput{}, err
	}
	var listen ListenerOpenOutput
	if err := addStepOutput(steps, "listen", &listen); err != nil {
		return ExposeOutput{}, err
	}
	return ExposeOutput{
		ExposureID: listen.ExposureID, Via: in.Via, Hostname: in.Hostname, HostID: in.HostID, Tunnel: in.Tunnel,
		Subnet: network.Subnet, Gateway: network.Gateway, Proxy: network.Proxy, Port: int(listen.Address.Port()),
	}, nil
}
