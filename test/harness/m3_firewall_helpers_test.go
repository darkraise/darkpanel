//go:build harness

package harness

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules/firewall/model"
	"github.com/darkraise/darkpanel/test/harness/vm"
)

// m3Status is the part of the firewall.status resource the M3 scenarios
// read.
type m3Status struct {
	Active      bool         `json:"active"`
	InSync      bool         `json:"inSync"`
	OutOfSync   []m3Chain    `json:"outOfSync"`
	Supported   bool         `json:"supported"`
	Unsupported []string     `json:"unsupported"`
	Rules       []model.Rule `json:"rules"`
}

// m3Chain names one chain of firewall.status's outOfSync list.
type m3Chain struct {
	Family string `json:"family"`
	Table  string `json:"table"`
	Chain  string `json:"chain"`
}

// m3Plan is the part of a plan the M3 scenarios read.
type m3Plan struct {
	PlanID string          `json:"planId"`
	Diff   []core.DiffLine `json:"diff"`
	Guards []core.Guard    `json:"guards"`
}

// m3Lockout returns the firewall.lockout guard of p.
func (p m3Plan) m3Lockout() (core.Guard, bool) {
	for _, g := range p.Guards {
		if g.ID == "firewall.lockout" {
			return g, true
		}
	}
	return core.Guard{}, false
}

// m3Status reads host's firewall.status.
func (a *API) m3Status(ctx context.Context, host string) (m3Status, error) {
	var s m3Status
	err := a.Resource(ctx, host, "firewall.status", &s)
	return s, err
}

// m3Plan plans version 1 of the firewall action on host with input.
func (a *API) m3Plan(ctx context.Context, host, action string, input any) (m3Plan, error) {
	path, err := actionPath(host, action+"/v1", "plan")
	if err != nil {
		return m3Plan{}, err
	}
	var p m3Plan
	if err := a.do(ctx, http.MethodPost, path, map[string]any{"input": input}, &p); err != nil {
		return m3Plan{}, fmt.Errorf("plan %s: %w", action, err)
	}
	return p, nil
}

// m3TLSAPI is NewAPI for an https listener of the controller, trusting
// only the controller's web CA, given in PEM.
func m3TLSAPI(base string, dial func(ctx context.Context, network, addr string) (net.Conn, error), caPEM []byte) (*API, error) {
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		return nil, errors.New("harness: the web CA is not a PEM certificate")
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	tr := &http.Transport{DialContext: dial, DisableKeepAlives: true, TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}
	return &API{base: strings.TrimSuffix(base, "/"), http: &http.Client{Transport: tr, Jar: jar, Timeout: apiTimeout}}, nil
}

// m3WebCA reads the controller's web CA root in PEM through its socket.
func m3WebCA(ctx context.Context, t *testing.T, v *vm.VM) []byte {
	t.Helper()
	out := m3Run(ctx, t, v, "sudo darkpanel controller web-ca --json")
	var ca struct {
		PEM string `json:"pem"`
	}
	if err := json.Unmarshal([]byte(out), &ca); err != nil || ca.PEM == "" {
		t.Fatalf("darkpanel controller web-ca --json: %v\n%s", err, out)
	}
	return []byte(ca.PEM)
}

// m3Run runs cmd on v and fails the test when it fails.
func m3Run(ctx context.Context, t *testing.T, v *vm.VM, cmd string) string {
	t.Helper()
	out, err := v.Run(ctx, cmd)
	if err != nil {
		t.Fatalf("%s: %v\n%s", cmd, err, out)
	}
	return out
}

// m3WaitStatus polls host's firewall.status through api until check
// accepts it.
func m3WaitStatus(ctx context.Context, t *testing.T, api *API, host string, timeout time.Duration, check func(m3Status) error) m3Status {
	t.Helper()
	var st m3Status
	err := eventually(ctx, timeout, func(ctx context.Context) error {
		var err error
		if st, err = api.m3Status(ctx, host); err != nil {
			return err
		}
		return check(st)
	})
	if err != nil {
		t.Fatalf("firewall.status of host %s: %v", host, err)
	}
	return st
}

// m3Healthy accepts a status that is supported, in sync and active.
func m3Healthy(st m3Status) error {
	if !st.Supported || !st.InSync || !st.Active {
		return fmt.Errorf("supported %t (%v), in sync %t (%v), active %t", st.Supported, st.Unsupported, st.InSync, st.OutOfSync, st.Active)
	}
	return nil
}

// m3FindRule returns the first rule of rules that match accepts.
func m3FindRule(rules []model.Rule, match func(model.Rule) bool) (model.Rule, bool) {
	for _, r := range rules {
		if match(r) {
			return r, true
		}
	}
	return model.Rule{}, false
}

// m3ManualPort accepts the manual incoming allow rule of tcp port with the
// interface given ("" for any), as provisioning and the scenarios add them
// with ufw allow.
func m3ManualPort(port, iface string) func(model.Rule) bool {
	return func(r model.Rule) bool {
		return r.Origin == model.OriginManual && r.Action == model.ActionAllow && r.Direction == model.DirIn &&
			r.ToPort == port && r.InIface == iface && r.Raw == ""
	}
}

// m3JSON is v as JSON, for messages.
func m3JSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}
