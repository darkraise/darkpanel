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
	"net/url"
	"regexp"
	"slices"
	"strconv"
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

// m3Apply plans version 1 of the revertible firewall action on host and
// applies it with the revert timeout given, without confirming it; a
// failing guard is an error and nothing is applied. It returns the change
// in whatever state the apply answered.
func (a *API) m3Apply(ctx context.Context, host, action string, input any, timeoutSeconds int) (Change, error) {
	p, err := a.m3Plan(ctx, host, action, input)
	if err != nil {
		return Change{}, err
	}
	for _, g := range p.Guards {
		if !g.OK {
			return Change{}, fmt.Errorf("harness: plan %s: guard %s failed: %s", action, g.ID, g.Message)
		}
	}
	path, _ := actionPath(host, action+"/v1", "apply")
	var ch Change
	body := map[string]any{"planId": p.PlanID, "confirmTimeoutSeconds": timeoutSeconds}
	if err := a.do(ctx, http.MethodPost, path, body, &ch); err != nil {
		return Change{}, fmt.Errorf("apply %s: %w", action, err)
	}
	return ch, nil
}

// m3Change reads the change id.
func (a *API) m3Change(ctx context.Context, id string) (Change, error) {
	var ch Change
	err := a.do(ctx, http.MethodGet, "/api/v1/changes/"+url.PathEscape(id), nil, &ch)
	return ch, err
}

// m3Revert reverts the pending change id now.
func (a *API) m3Revert(ctx context.Context, id string) (Change, error) {
	var ch Change
	err := a.do(ctx, http.MethodPost, "/api/v1/changes/"+url.PathEscape(id)+"/revert", nil, &ch)
	return ch, err
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

// for up to timeout, and returns it.
func m3WaitChange(ctx context.Context, t *testing.T, api *API, id, want string, timeout time.Duration) Change {
	t.Helper()
	var ch Change
	err := eventually(ctx, timeout, func(ctx context.Context) error {
		var err error
		if ch, err = api.m3Change(ctx, id); err != nil {
			return err
		}
		if ch.State != want {
			return fmt.Errorf("change %s is %s, want %s: error %s, output %s", id, ch.State, want, m3JSON(ch.Error), ch.Output)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return ch
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

// m3HasPort reports whether rules hold a rule for the destination port.
func m3HasPort(rules []model.Rule, port string) bool {
	_, ok := m3FindRule(rules, func(r model.Rule) bool { return r.ToPort == port })
	return ok
}

// m3PortRule is firewall.rule.add's input for the Darkpanel rule the
// revert scenarios add and expect to disappear: allow incoming tcp to port.
func m3PortRule(port int) map[string]any {
	return map[string]any{"rule": model.Rule{Action: model.ActionAllow, Direction: model.DirIn, Proto: model.ProtoTCP,
		From: model.Any, To: model.Any, ToPort: strconv.Itoa(port), Family: model.FamilyBoth, Comment: "m3 revert"}}
}

// m3Hold keeps a TCP connection from v to host:port open in the transient
// unit until m3Release stops it. A connection to sshd that never
// authenticates lasts until sshd's LoginGraceTime, two minutes by default.
func m3Hold(ctx context.Context, t *testing.T, v *vm.VM, unit, host string, port int) {
	t.Helper()
	script := fmt.Sprintf("import socket,time\ns=socket.create_connection((%q,%d))\ntime.sleep(600)\n", host, port)
	m3Run(ctx, t, v, fmt.Sprintf("sudo systemd-run --unit=%s --collect /usr/bin/python3 -c %s", unit, m3Quote(script)))
}

// m3Release stops the unit m3Hold started.
func m3Release(ctx context.Context, v *vm.VM, unit string) {
	_, _ = v.Run(ctx, "sudo systemctl stop "+unit)
}

// m3WaitSession waits until v holds an established TCP connection to its
// local port from peer.
func m3WaitSession(ctx context.Context, t *testing.T, v *vm.VM, port int, peer string) {
	t.Helper()
	err := eventually(ctx, time.Minute, func(ctx context.Context) error {
		out, err := v.Run(ctx, fmt.Sprintf("ss -Htn state established '( sport = :%d )'", port))
		if err != nil {
			return err
		}
		if !strings.Contains(out, peer) {
			return fmt.Errorf("no established connection from %s to port %d:\n%s", peer, port, out)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// m3Quote quotes s for a POSIX shell.
func m3Quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// m3DiffText is p's diff, one line per entry, for messages and searches.
func (p m3Plan) m3DiffText() string {
	var b strings.Builder
	for _, d := range p.Diff {
		b.WriteString(d.Op + " " + d.Text + "\n")
	}
	return b.String()
}

var (
	m3CounterRe = regexp.MustCompile(` \[[0-9]+:[0-9]+\]$`)
	m3OwnedRe   = regexp.MustCompile(`^(:ufw6?-|-A ufw6?-|:INPUT |:OUTPUT )`)
	m3JumpRe    = regexp.MustCompile(`^-A (INPUT|FORWARD|OUTPUT) .*-j ufw6?-`)
)

// m3UFWLines returns the lines of iptables-save output that UFW decides:
// the INPUT and OUTPUT policies, the declarations and rules of its chains
// and the built-in chains' jumps into them, in order, with the counters
// zeroed as the golden captures hold them. Every other program's chains,
// and the FORWARD policy Docker also sets, are dropped, so the output of a
// host running Docker compares with a golden capture taken without it.
func m3UFWLines(save string) []string {
	var out []string
	for line := range strings.SplitSeq(save, "\n") {
		line = strings.TrimSpace(line)
		if m3OwnedRe.MatchString(line) || m3JumpRe.MatchString(line) {
			out = append(out, m3CounterRe.ReplaceAllString(line, " [0:0]"))
		}
	}
	return out
}

func TestM3UFWLines(t *testing.T) {
	save := `# Generated by iptables-nft-save v1.8.10 (nf_tables) on Sat Oct 24 08:00:00 2026
*filter
:INPUT DROP [12:3456]
:FORWARD DROP [0:0]
:OUTPUT ACCEPT [7:890]
:DOCKER - [0:0]
:DOCKER-USER - [0:0]
:ufw-before-input - [3:120]
:ufw-user-input - [0:0]
-A INPUT -j ufw-before-logging-input
-A INPUT -j ufw-before-input
-A FORWARD -j DOCKER-USER
-A FORWARD -j ufw-before-forward
-A DOCKER-USER -j RETURN
-A ufw-before-input -i lo -j ACCEPT
-A ufw-user-input -p tcp -m tcp --dport 22 -m comment --comment "dapp_OpenSSH" -j ACCEPT
COMMIT
# Completed on Sat Oct 24 08:00:00 2026
`
	want := []string{
		":INPUT DROP [0:0]", ":OUTPUT ACCEPT [0:0]", ":ufw-before-input - [0:0]", ":ufw-user-input - [0:0]",
		"-A INPUT -j ufw-before-logging-input", "-A INPUT -j ufw-before-input", "-A FORWARD -j ufw-before-forward",
		"-A ufw-before-input -i lo -j ACCEPT",
		`-A ufw-user-input -p tcp -m tcp --dport 22 -m comment --comment "dapp_OpenSSH" -j ACCEPT`,
	}
	if got := m3UFWLines(save); !slices.Equal(got, want) {
		t.Errorf("m3UFWLines =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if got := m3UFWLines(":ufw6-user-input - [1:2]\n-A ufw6-user-input -j RETURN\n"); !slices.Equal(got, []string{":ufw6-user-input - [0:0]", "-A ufw6-user-input -j RETURN"}) {
		t.Errorf("m3UFWLines of IPv6 chains = %q", got)
	}
}

// m3xOutOfSyncRefusal checks the plans of one ordinary change on an
// out-of-sync host, planned without and with the override of
// firewall.lockout: both plans must succeed, the first with a failing
// firewall.lockout guard, and the override must not pass it. It returns nil
// when they do.
func m3xOutOfSyncRefusal(plan m3Plan, planErr error, overridden m3Plan, overriddenErr error) error {
	if planErr != nil {
		return fmt.Errorf("the plan was refused (%w); want a plan that succeeds with a failing firewall.lockout guard", planErr)
	}
	g, ok := plan.m3Lockout()
	switch {
	case !ok:
		return fmt.Errorf("the plan has no firewall.lockout guard: %s", m3JSON(plan.Guards))
	case g.OK:
		return fmt.Errorf("firewall.lockout passed on an out-of-sync host: %s", m3JSON(plan.Guards))
	case overriddenErr != nil:
		return fmt.Errorf("the plan with the override was refused (%w); want a plan whose guard still fails", overriddenErr)
	}
	og, ok := overridden.m3Lockout()
	switch {
	case !ok:
		return fmt.Errorf("the plan with the override has no firewall.lockout guard: %s", m3JSON(overridden.Guards))
	case og.OK:
		return fmt.Errorf("an override passed firewall.lockout on an out-of-sync host: %s", m3JSON(overridden.Guards))
	}
	return nil
}

// m3xOutOfSync accepts a status whose loaded state differs from UFW's files.
func m3xOutOfSync(st m3Status) error {
	if st.InSync {
		return errors.New("the host is still in sync")
	}
	return nil
}

// m3xTailscaledSocket accepts the output of ss -Htuanp when it shows a
// socket held by tailscaled, which is how the guard learns that tailscaled
// runs.
func m3xTailscaledSocket(ss string) error {
	if !strings.Contains(ss, `"tailscaled"`) {
		return fmt.Errorf("tailscaled holds no TCP or UDP socket; ss -Htuanp printed:\n%s", ss)
	}
	return nil
}

// m3JSON is v as JSON, for messages.
func m3JSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}

func TestM3xOutOfSyncRefusal(t *testing.T) {
	failing := m3Plan{Guards: []core.Guard{{ID: "firewall.lockout", OK: false, Overridable: true, Message: "out of sync"}}}
	passing := m3Plan{Guards: []core.Guard{{ID: "firewall.lockout", OK: true}}}
	overridden := m3Plan{Guards: []core.Guard{{ID: "firewall.lockout", OK: true, Overridden: true}}}
	refused := &APIError{Code: "request.invalid_input", Detail: "bad"}
	for _, tc := range []struct {
		name         string
		plan         m3Plan
		planErr      error
		overrideP    m3Plan
		overrideErr  error
		wantRejected string
	}{
		{"refused", failing, nil, failing, nil, ""},
		{"plan error", m3Plan{}, refused, failing, nil, "the plan was refused"},
		{"plan without the guard", m3Plan{}, nil, failing, nil, "no firewall.lockout guard"},
		{"guard passed", passing, nil, failing, nil, "passed"},
		{"override error", failing, nil, m3Plan{}, refused, "the plan with the override was refused"},
		{"override without the guard", failing, nil, m3Plan{}, nil, "no firewall.lockout guard"},
		{"override passed the guard", failing, nil, overridden, nil, "an override passed"},
	} {
		err := m3xOutOfSyncRefusal(tc.plan, tc.planErr, tc.overrideP, tc.overrideErr)
		switch {
		case tc.wantRejected == "" && err != nil:
			t.Errorf("%s: %v, want nil", tc.name, err)
		case tc.wantRejected != "" && (err == nil || !strings.Contains(err.Error(), tc.wantRejected)):
			t.Errorf("%s: %v, want an error naming %q", tc.name, err, tc.wantRejected)
		}
	}
}

func TestM3xOutOfSync(t *testing.T) {
	if err := m3xOutOfSync(m3Status{InSync: true}); err == nil {
		t.Error("m3xOutOfSync accepted a host in sync")
	}
	if err := m3xOutOfSync(m3Status{InSync: false}); err != nil {
		t.Errorf("m3xOutOfSync refused a host out of sync: %v", err)
	}
}

// TestM3xTailscaledSocket: tailscaled holds its WireGuard UDP port while it
// is logged out. The first output is what ss -Htuanp printed in a
// privileged debian:13 container running tailscale 1.104.1 (the version of
// images.json) as its unit starts it, before any login.
func TestM3xTailscaledSocket(t *testing.T) {
	loggedOut := `udp UNCONN    0      0         0.0.0.0:41641         0.0.0.0:*   users:(("tailscaled",pid=2075,fd=17))
udp UNCONN    0      0            [::]:41641            [::]:*   users:(("tailscaled",pid=2075,fd=16))
tcp ESTAB     0      0      172.17.0.9:58584 199.165.136.100:443 users:(("tailscaled",pid=2075,fd=18))
tcp TIME-WAIT 0      0      172.17.0.9:35994 151.101.130.132:80
`
	if err := m3xTailscaledSocket(loggedOut); err != nil {
		t.Errorf("m3xTailscaledSocket(logged-out tailscaled) = %v, want nil", err)
	}
	for name, out := range map[string]string{
		"no output":    "",
		"other daemon": `tcp LISTEN 0 4096 0.0.0.0:22 0.0.0.0:* users:(("sshd",pid=800,fd=3))` + "\n",
		"no process":   "udp UNCONN 0 0 0.0.0.0:41641 0.0.0.0:*\n",
	} {
		if err := m3xTailscaledSocket(out); err == nil {
			t.Errorf("%s: m3xTailscaledSocket = nil, want an error", name)
		}
	}
}

func TestM3xNftablesReason(t *testing.T) {
	check := m3UnsupportedNamed(m3xNftablesReason)
	if err := check(m3Status{Unsupported: []string{`nft table inet m3: base chain "input" on hook "input" has 1 rule(s) and policy accept`}}); err != nil {
		t.Errorf("the reason of the m3 table was refused: %v", err)
	}
	for _, other := range []string{
		"/etc/ufw/m3.rules is missing",
		`nft table inet m3x: base chain "input" on hook "input" has 1 rule(s) and policy accept`,
		`nft table ip m3: base chain "input" on hook "input" has 1 rule(s) and policy accept`,
		`nft table inet m3: base chain "forward" on hook "forward" has 1 rule(s) and policy accept`,
	} {
		if err := check(m3Status{Unsupported: []string{other}}); err == nil {
			t.Errorf("the reason %q was accepted", other)
		}
	}
	if err := check(m3Status{Supported: true, Unsupported: []string{`nft table inet m3: base chain "input" on hook "input" has 1 rule(s)`}}); err == nil {
		t.Error("a supported host was accepted")
	}
}
