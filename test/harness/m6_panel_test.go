//go:build harness

package harness

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/auth"
	"github.com/darkraise/darkpanel/internal/controller/coreactions"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/firewall"
	"github.com/darkraise/darkpanel/internal/modules/firewall/model"
	"github.com/darkraise/darkpanel/internal/modules/npm"
	"github.com/darkraise/darkpanel/internal/modules/panel"
	"github.com/darkraise/darkpanel/internal/paths"
	"github.com/darkraise/darkpanel/internal/update"
	"github.com/darkraise/darkpanel/test/harness/vm"
)

var m6pFlag = flag.Bool("m6p", false, "run TestM6Panel; needs -os and -m2, and is a go test run of its own")

// m6pDeadline bounds TestM6Panel. The workflow step that runs it must keep its
// -timeout at least m6pDeadline plus 10 minutes, for the cleanups that stop the VMs.
const m6pDeadline = 40 * time.Minute

const (
	// m6pHost is the panel's public hostname; Pebble's DNS server answers
	// every name with NPM's address.
	m6pHost = "panel.example.test"
	// m6pClientName is the container on the panel network that sends logins.
	m6pClientName = "m6p-client"

	// The users of the refused logins, one per source, so that the audit
	// log tells the sources apart.
	m6pUserPublic   = "m6p-public"
	m6pUserPrivate  = "m6p-private"
	m6pUserAttached = "m6p-attached"
	m6pUserPlain    = "m6p-plain"
	m6pUserAfter    = "m6p-after"

	// m6pWrongPassword and m6pWrongCode are what every refused login sends.
	m6pWrongPassword = "wrong-password-0000" //nolint:gosec // G101: a wrong password the test sends on purpose
	m6pWrongCode     = "000000"

	// The forged client headers, which the proxy path must not trust.
	m6pForgedFor    = "203.0.113.99, 198.51.100.7"
	m6pForgedReal   = "203.0.113.98"
	m6pForgedClient = "203.0.113.97"
)

// m6pForgedAddrs are the addresses of the forged headers, none of which the
// audit log may record.
var m6pForgedAddrs = []string{"203.0.113.99", "198.51.100.7", m6pForgedReal, m6pForgedClient}

// m6pSpoofHeaders are the client headers a forging client sends: three that
// name another client and a protocol that claims plain HTTP.
func m6pSpoofHeaders() http.Header {
	h := http.Header{}
	h.Set("X-Forwarded-For", m6pForgedFor)
	h.Set("X-Real-Ip", m6pForgedReal)
	h.Set("X-Darkpanel-Client", m6pForgedClient)
	h.Set("X-Forwarded-Proto", "http")
	return h
}

// m6pCheckAudit checks the login rows of entries: no source holds a forged
// address, each user of want has a row from the wanted source, and no user of
// absent has a row.
func m6pCheckAudit(entries []coreactions.AuditEntry, want map[string]string, absent []string) error {
	var logins []coreactions.AuditEntry
	for _, e := range entries {
		if e.Action == auth.AuditLoginAction {
			logins = append(logins, e)
		}
	}
	source := func(e coreactions.AuditEntry) string {
		if e.Source == nil {
			return ""
		}
		return *e.Source
	}
	for _, e := range logins {
		for _, addr := range m6pForgedAddrs {
			if src := source(e); strings.Contains(src, addr) {
				return fmt.Errorf("harness: audit entry %s records the source %q, which holds the forged address %s", e.ID, src, addr)
			}
		}
	}
	sources := map[string][]string{}
	for _, e := range logins {
		var in struct {
			Username string `json:"username"`
		}
		if err := json.Unmarshal([]byte(e.Input), &in); err != nil {
			return fmt.Errorf("harness: the input %.80q of audit entry %s: %w", e.Input, e.ID, err)
		}
		sources[in.Username] = append(sources[in.Username], source(e))
	}
	for _, user := range slices.Sorted(maps.Keys(want)) {
		seen, ok := sources[user]
		if !ok {
			return fmt.Errorf("harness: the audit log holds no login of %s", user)
		}
		if !slices.Contains(seen, want[user]) {
			return fmt.Errorf("harness: the logins of %s were recorded from %q, want one from %q", user, seen, want[user])
		}
	}
	for _, user := range absent {
		if seen, ok := sources[user]; ok {
			return fmt.Errorf("harness: the audit log holds logins of %s from %q, want none", user, seen)
		}
	}
	return nil
}

// m6pReply is the line m6pClientScript prints.
type m6pReply struct {
	Status int    `json:"status"`
	Body   string `json:"body"`
	Error  string `json:"error"`
}

// m6pParseReply decodes the last line of out, which may follow noise such as
// docker's own messages.
func m6pParseReply(out string) (m6pReply, error) {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	var r m6pReply
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &r); err != nil {
		return m6pReply{}, fmt.Errorf("harness: the client printed %.200q: %w", out, err)
	}
	if r.Status == 0 && r.Error == "" {
		return m6pReply{}, fmt.Errorf("harness: the client printed %.200q, which holds neither a status nor an error", out)
	}
	return r, nil
}

// m6pCheckRefusal accepts the panel's own refusal of a login with a wrong
// password, which shows that the request reached the panel.
func m6pCheckRefusal(status int, body []byte) error {
	var p struct {
		Code string `json:"code"`
	}
	if status == http.StatusUnauthorized && json.Unmarshal(body, &p) == nil && p.Code == string(errcode.InvalidCredentials) {
		return nil
	}
	return fmt.Errorf("harness: the login answered %d %.120q, want the panel's %d %s", status, body, http.StatusUnauthorized, errcode.InvalidCredentials)
}

// m6pCheckPlain accepts a redirect of a plain-HTTP request to https on host.
func m6pCheckPlain(status int, location, host string) error {
	switch status {
	case http.StatusMovedPermanently, http.StatusFound, http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		if u, err := url.Parse(location); err == nil && u.Scheme == "https" && u.Host == host {
			return nil
		}
	}
	return fmt.Errorf("harness: plain HTTP answered %d with Location %q, want a redirect to https://%s", status, location, host)
}

// m6pDirectRefused accepts the http_code curl writes when it got no answer.
func m6pDirectRefused(code string) error {
	if strings.TrimSpace(code) != "000" {
		return fmt.Errorf("harness: the direct request answered %q, want no answer (000)", code)
	}
	return nil
}

// m6pCheckExposed checks the output of panel.exposure.add, change changeID,
// against its input in.
func m6pCheckExposed(out panel.ExposeOutput, changeID string, in panel.ExposeInput) error {
	if out.ExposureID != changeID {
		return fmt.Errorf("harness: the exposure id %q is not the change %q", out.ExposureID, changeID)
	}
	port := in.Port
	if port == 0 {
		port = panel.DefaultPort
	}
	if out.Via != in.Via || out.Hostname != in.Hostname || out.HostID != in.HostID || out.Tunnel != in.Tunnel || out.Port != port {
		return fmt.Errorf("harness: the exposure %+v does not echo the input %+v with port %d", out, in, port)
	}
	s := out.Subnet
	if !s.IsValid() || s.Bits() != 29 || s != s.Masked() {
		return fmt.Errorf("harness: the subnet %s is not a /29", s)
	}
	if !panel.PanelRange.Contains(s.Addr()) {
		return fmt.Errorf("harness: the subnet %s is not inside %s", s, panel.PanelRange)
	}
	if out.Gateway != s.Addr().Next() || out.Proxy != out.Gateway.Next() {
		return fmt.Errorf("harness: the gateway %s and the proxy %s are not the first and second addresses of %s", out.Gateway, out.Proxy, s)
	}
	return nil
}

// m6pCheckNetwork checks the output of docker network inspect of the panel
// network against the exposure out.
func m6pCheckNetwork(inspect []byte, out panel.ExposeOutput) error {
	var nets []struct {
		Name       string            `json:"Name"`
		EnableIPv6 bool              `json:"EnableIPv6"`
		Labels     map[string]string `json:"Labels"`
		Options    map[string]string `json:"Options"`
		IPAM       struct {
			Config []struct {
				Subnet  string `json:"Subnet"`
				Gateway string `json:"Gateway"`
			} `json:"Config"`
		} `json:"IPAM"`
		Containers map[string]struct {
			Name        string `json:"Name"`
			IPv4Address string `json:"IPv4Address"`
		} `json:"Containers"`
	}
	if err := json.Unmarshal(inspect, &nets); err != nil {
		return fmt.Errorf("harness: docker network inspect: %w", err)
	}
	if len(nets) != 1 {
		return fmt.Errorf("harness: docker network inspect gave %d networks, want 1", len(nets))
	}
	n := nets[0]
	if n.Name != panel.NetworkName {
		return fmt.Errorf("harness: the network is named %q, want %q", n.Name, panel.NetworkName)
	}
	if n.Labels[core.LabelModule] != panel.ID {
		return fmt.Errorf("harness: the network's labels %v lack %s=%s", n.Labels, core.LabelModule, panel.ID)
	}
	if n.Options[panel.BridgeOption] != panel.BridgeName {
		return fmt.Errorf("harness: the network's options %v lack %s=%s", n.Options, panel.BridgeOption, panel.BridgeName)
	}
	if n.EnableIPv6 {
		return errors.New("harness: the network has IPv6 enabled")
	}
	cfg := n.IPAM.Config
	if len(cfg) != 1 {
		return fmt.Errorf("harness: the network's IPAM config %+v, want one of subnet %s and gateway %s", cfg, out.Subnet, out.Gateway)
	}
	subnet, errS := netip.ParsePrefix(cfg[0].Subnet)
	gateway, errG := netip.ParseAddr(cfg[0].Gateway)
	if errS != nil || errG != nil || subnet != out.Subnet || gateway != out.Gateway {
		return fmt.Errorf("harness: the network's IPAM config %+v, want subnet %s and gateway %s", cfg, out.Subnet, out.Gateway)
	}
	for _, c := range n.Containers {
		if c.Name != m6nNPMName {
			continue
		}
		if p, err := netip.ParsePrefix(c.IPv4Address); err != nil || p.Addr() != out.Proxy {
			return fmt.Errorf("harness: %s is at %q on the panel network, want %s", m6nNPMName, c.IPv4Address, out.Proxy)
		}
		return nil
	}
	return fmt.Errorf("harness: %s is not on the panel network", m6nNPMName)
}

// m6pCheckStatus checks that st, the output of panel.exposure.status, shows
// the exposure out open, and returns the banner text it gives the web UI.
func m6pCheckStatus(st panel.StatusOutput, out panel.ExposeOutput) (string, error) {
	if !st.Exposed || st.Exposure == nil {
		return "", fmt.Errorf("harness: panel.exposure.status reports exposed %v with exposure %v, want the exposure %s", st.Exposed, st.Exposure, out.ExposureID)
	}
	v := *st.Exposure
	got := panel.ExposeOutput{
		ExposureID: v.ID, Via: v.Via, Hostname: v.Hostname, HostID: v.HostID, Tunnel: v.Tunnel,
		Subnet: v.Subnet, Gateway: v.Gateway, Proxy: v.Proxy, Port: v.Port,
	}
	if got != out || v.State != panel.StateOpen || v.ListenerID == "" {
		return "", fmt.Errorf("harness: panel.exposure.status reports %+v, want the exposure %+v open with a listener", v, out)
	}
	return "Panel exposed via " + v.Via + " at " + v.Hostname, nil
}

// m6pForwardedProtoLine accepts grep output that holds a line passing the
// scheme NPM itself saw on as X-Forwarded-Proto.
func m6pForwardedProtoLine(out string) error {
	want := []string{"proxy_set_header", "X-Forwarded-Proto", "$scheme;"}
	var seen []string
	for _, line := range strings.Split(out, "\n") {
		if !strings.Contains(line, "X-Forwarded-Proto") {
			continue
		}
		line = strings.TrimSpace(line)
		if slices.Equal(strings.Fields(line), want) {
			return nil
		}
		seen = append(seen, line)
	}
	return fmt.Errorf("harness: no line of NPM's templates is %q; its X-Forwarded-Proto lines are %q", strings.Join(want, " "), seen)
}

// m6pListening reports whether out, the output of ss -H -ltn, lists a socket
// whose local address is addr.
func m6pListening(out, addr string) bool {
	for _, line := range strings.Split(out, "\n") {
		if f := strings.Fields(line); len(f) >= 4 && f[3] == addr {
			return true
		}
	}
	return false
}

// m6pController returns a copy of f whose agent is the controller's own host,
// so that the steps of m6nRun act where the panel is exposed. Only the
// original calls nextCode, which keeps lastCode in one place.
func m6pController(f *fleet) *fleet {
	c := *f
	c.agent = f.controller
	c.agentID = f.localID
	return &c
}

// m6pAnswer is what m6pRequest returns.
type m6pAnswer struct {
	Status   int
	Body     []byte
	Location string
}

// m6pTransport opens each connection from inside from to addr, whatever host
// the request names, and verifies an HTTPS server's chain for host against
// roots.
func m6pTransport(from *vm.VM, addr, host string, roots *x509.CertPool) *http.Transport {
	return &http.Transport{
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return from.DialContext(ctx, network, addr)
		},
		DisableKeepAlives: true,
		TLSClientConfig:   &tls.Config{RootCAs: roots, ServerName: host, MinVersion: tls.VersionTLS12},
	}
}

// m6pRequest sends one request to scheme://host<path> over a connection from
// from to addr, so the TCP peer the proxy sees is from, with header added. It
// follows no redirect.
func m6pRequest(ctx context.Context, from *vm.VM, scheme, addr, host string, roots *x509.CertPool, method, path string, body []byte, header http.Header) (m6pAnswer, error) {
	u := url.URL{Scheme: scheme, Host: host, Path: path}
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), rd)
	if err != nil {
		return m6pAnswer{}, fmt.Errorf("harness: %w", err)
	}
	for k, vs := range header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	tr := m6pTransport(from, addr, host, roots)
	defer tr.CloseIdleConnections()
	client := &http.Client{
		Transport:     tr,
		Timeout:       15 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := client.Do(req)
	if err != nil {
		return m6pAnswer{}, fmt.Errorf("harness: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return m6pAnswer{}, fmt.Errorf("harness: %s %s: %w", method, u.String(), err)
	}
	return m6pAnswer{Status: resp.StatusCode, Body: data, Location: resp.Header.Get("Location")}, nil
}

// m6pLoginBody is the body of a login of user that the panel refuses.
func m6pLoginBody(user string) []byte {
	b, _ := json.Marshal(map[string]string{"username": user, "password": m6pWrongPassword, "code": m6pWrongCode})
	return b
}

// m6pExposureAPI returns a client of the panel's API through the exposure:
// its connections go from from to addr and verify the chain for host against
// roots. NewAPI builds its own transport, so it cannot be used.
func m6pExposureAPI(from *vm.VM, addr, host string, roots *x509.CertPool) *API {
	jar, err := cookiejar.New(nil)
	if err != nil {
		panic(err) // cookiejar.New fails only on options, and it gets none
	}
	return &API{base: "https://" + host, http: &http.Client{Transport: m6pTransport(from, addr, host, roots), Jar: jar, Timeout: apiTimeout}}
}

// m6pApplyThrough plans and applies version 1 of the controller-side action
// with input through via, as m4ApplyAsync does, settles the change through
// settle, and fails the test unless it completed.
func m6pApplyThrough(ctx context.Context, t *testing.T, via, settle *API, action string, input any) Change {
	t.Helper()
	p, err := m4Plan(ctx, via, "", action, input)
	if err != nil {
		t.Fatal(err)
	}
	path, err := actionPath("", action+"/v1", "apply")
	if err != nil {
		t.Fatal(err)
	}
	body := map[string]any{"planId": p.PlanID}
	if p.Confirmation != "" {
		body["confirmation"] = p.Confirmation
	}
	var applied Change
	if err := via.do(ctx, http.MethodPost, path, body, &applied); err != nil {
		t.Fatalf("apply %s: %v", action, err)
	}
	ch, err := m4Settle(ctx, settle, applied.ID)
	if err != nil {
		t.Fatalf("change %s of %s: %v", applied.ID, action, err)
	}
	if ch.State != core.StateCompleted {
		detail := "no error recorded"
		if ch.Error != nil {
			detail = ch.Error.Code + ": " + ch.Error.Message
		}
		t.Fatalf("change %s of %s ended %s, want %s: %s", ch.ID, action, ch.State, core.StateCompleted, detail)
	}
	return ch
}

// m6pClientScript sends one HTTPS login, refused by its wrong password and
// code, to the address and port of its first two arguments with the server
// name and Host of the third, for the user of the fourth, with the headers of
// the JSON fifth, and prints one line: the status and body, or the error.
const m6pClientScript = `
const [address, port, host, user, headers] = process.argv.slice(1);
const body = JSON.stringify({username: user, password: "` + m6pWrongPassword + `", code: "` + m6pWrongCode + `"});
const done = (o) => { console.log(JSON.stringify(o)); process.exit(0); };
setTimeout(() => done({error: "timeout"}), 10000);
const req = require("https").request({
  host: address, port: Number(port), servername: host, method: "POST", path: "/api/v1/auth/login",
  rejectUnauthorized: false,
  headers: Object.assign(JSON.parse(headers), {"Host": host, "Content-Type": "application/json", "Content-Length": Buffer.byteLength(body)}),
}, (res) => {
  let text = "";
  res.setEncoding("utf8");
  res.on("data", (c) => { text += c; });
  res.on("end", () => done({status: res.statusCode, body: text}));
  res.on("error", (e) => done({error: e.code || e.message}));
});
req.on("error", (e) => done({error: e.code || e.message}));
req.end(body);
`

// m6pExec runs m6pClientScript in the container m6pClientName on v against
// address and port for user, with the forged headers of m6pSpoofHeaders.
func m6pExec(ctx context.Context, t *testing.T, v *vm.VM, address, port, host, user string) m6pReply {
	t.Helper()
	headers, err := json.Marshal(m6pSpoofHeaders())
	if err != nil {
		t.Fatal(err)
	}
	out := m3Run(ctx, t, v, m3dxShell("sudo", "docker", "exec", m6pClientName, "node", "-e", m6pClientScript, address, port, host, user, string(headers)))
	r, err := m6pParseReply(out)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// m6pDiagnostics are what a failed step logs from the controller's host.
func m6pDiagnostics() []m6nDiagnostic {
	ds := []m6nDiagnostic{
		{"containers", `sudo docker ps -a --format 'table {{.Names}}\t{{.Image}}\t{{.Status}}'`},
		{"networks", "sudo docker network ls"},
		{"panel network", "sudo docker network inspect " + panel.NetworkName},
		{"listeners", "sudo ss -H -ltn"},
		{"ufw status", "sudo ufw status verbose"},
		{"NPM API", "curl -sS --max-time 10 http://127.0.0.1:81/api/"},
	}
	for _, name := range []string{m6nNPMName, m6nPebbleName, m6nChallName, m6pClientName} {
		ds = append(ds, m6nDiagnostic{name + " log", "sudo docker logs --tail 40 " + name + " 2>&1"})
	}
	return append(ds, m6nDiagnostic{"journal", "sudo journalctl -u " + update.ControllerUnit + " -u " + update.AgentUnit + " --no-pager -n 80"})
}

// m6pRun is what the steps of TestM6Panel share.
type m6pRun struct {
	ctx      context.Context
	f        *fleet
	n        *m6nRun // the NPM steps, acting on the controller's host
	out      panel.ExposeOutput
	change   string         // the change of panel.exposure.add
	x        *API           // the admin's session through the exposure
	roots    *x509.CertPool // Pebble's root
	clientIP string         // m6pClientName's address on the panel network
}

// TestM6Panel is the panel exposure's M6 evidence (spec section 8): the panel,
// exposed through a real NPM whose certificate comes from Pebble on the
// controller's own host, serves a login over HTTPS, records the true client
// and the proxy in the audit log whatever headers a client forges, refuses
// plain HTTP and direct peers, reports the exposure for the banner, and is
// removed through the exposure itself. The workflow selects it with -run,
// -os, -m2 and -m6p.
func TestM6Panel(t *testing.T) {
	if *osFlag == "" || *m2Flag == "" || !*m6pFlag {
		t.Skip("no -os, -m2 or -m6p given: the VM tests run in .github/workflows/harness.yml")
	}
	ctx, cancel := context.WithTimeout(context.Background(), m6pDeadline)
	t.Cleanup(cancel)
	f := m5dLab(ctx, t)
	r := &m6pRun{ctx: ctx, f: f, n: &m6nRun{ctx: ctx, f: m6pController(f)}}
	steps := []struct {
		name string
		gate bool // later steps need this one
		run  func(t *testing.T)
	}{
		{"verify", true, r.verify},
		{"prepare", true, r.n.prepare},
		{"deploy", true, r.n.deploy},
		{"harness-settings", true, r.harnessSettings},
		{"public", true, r.public},
		{"expose", true, r.expose},
		{"login", true, r.login},
		{"banner", false, r.banner},
		{"spoofed-clients", false, r.spoofedClients},
		{"plain-http", false, r.plainHTTP},
		{"audit", false, r.audit},
		{"remove-through-the-exposure", true, r.removeThroughTheExposure},
		{"after-removal", false, r.afterRemoval},
	}
	for _, s := range steps {
		if !t.Run(s.name, s.run) {
			r.logDiagnostics(t)
			if s.gate {
				return
			}
		}
	}
}

// verify checks on the VMs what the code cannot show: the controller's host
// runs Docker, its local agent can deploy NPM and keeps its database at the
// production path, the firewall can be changed on that host, and NPM's
// templates pass on the scheme NPM saw. The runtime listener requires
// X-Forwarded-Proto https exactly once, so a template that passed on the
// client's value would let a forged header reach the panel over plain HTTP.
func (r *m6pRun) verify(t *testing.T) {
	f := r.f
	if v := strings.TrimSpace(m3Run(r.ctx, t, f.controller, "sudo docker version --format "+m3Quote("{{.Server.Version}}"))); v == "" {
		t.Error("docker version printed no server version on the controller's host")
	}

	hosts, err := f.api.Hosts(r.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if h, ok := localHost(hosts); !ok || h.ID != f.localID {
		t.Errorf("the host list %+v has no local host %s", hosts, f.localID)
	}
	if _, err := m4Plan(r.ctx, f.api, "", "npm.instance.deploy-public", npm.DeployPublicInput{HostID: f.localID, AcmeEmail: m6nAcmeEmail}); err != nil {
		t.Errorf("npm.instance.deploy-public on the local host: %v", err)
	}

	m6nPull(r.ctx, t, f.controller, npm.Image)
	// grep exits 1 when nothing matches; the output is what is checked.
	out, err := f.controller.Run(r.ctx, "sudo docker run --rm --entrypoint grep "+m3dxShell(npm.Image)+" -rhs X-Forwarded-Proto /app /etc/nginx")
	t.Logf("X-Forwarded-Proto in NPM's templates (grep: %v):\n%s", err, out)
	if err := m6pForwardedProtoLine(out); err != nil {
		t.Error(err)
	}

	p, err := m4Plan(r.ctx, f.api, f.localID, "firewall.rules.apply", firewall.RulesApplyInput{
		Label: "m6p probe",
		Add: []model.Rule{{
			ID: "0M6PPR0BE00000000000000000", Action: "allow", Direction: "in", Proto: "tcp",
			From: "192.0.2.1", To: "any", ToPort: "9", Family: "v4",
		}},
	})
	if err != nil {
		t.Errorf("firewall.rules.apply on the local host: %v", err)
	}
	for _, g := range p.Guards {
		if !g.OK {
			t.Errorf("firewall.rules.apply on the local host: guard %s: %s", g.ID, g.Message)
		}
	}

	if out, err := f.controller.Run(r.ctx, "sudo test -f "+m3Quote(paths.Production().AgentDB())); err != nil {
		t.Errorf("the local agent's database is not at %s: %v\n%s", paths.Production().AgentDB(), err, out)
	}
}

// harnessSettings is m6nRun.harnessSettings on the controller's host, then
// keeps Pebble's root for the clients that verify NPM's chain.
func (r *m6pRun) harnessSettings(t *testing.T) {
	r.n.harnessSettings(t)
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(r.n.root) {
		t.Fatalf("Pebble's root is not a PEM certificate: %.80q", r.n.root)
	}
	r.roots = roots
}

// public is m6nRun.public for the controller's host, whose public address is
// targetIPv4.
func (r *m6pRun) public(t *testing.T) {
	f := r.f
	for _, port := range []string{"80", "443"} {
		err := eventually(r.ctx, time.Minute, func(ctx context.Context) error {
			c, err := f.scanner.DialContext(ctx, "tcp", net.JoinHostPort(targetIPv4, port))
			if err != nil {
				return err
			}
			return c.Close()
		})
		if err != nil {
			t.Fatalf("%s:%s from the scanner: %v", targetIPv4, port, err)
		}
	}
	m4ApplyAsync(r.ctx, t, f.api, f.localID, "npm.instance.confirm-public", npm.ConfirmPublicInput{})
	var st npm.InstanceStatus
	if err := f.api.Read(r.ctx, f.localID, "npm.instance.status/v1", npm.StatusInput{}, &st); err != nil {
		t.Fatal(err)
	}
	if st.Public != "confirmed" {
		t.Errorf("npm.instance.status public %q after npm.instance.confirm-public, want confirmed", st.Public)
	}
}

// expose plans and applies panel.exposure.add through NPM, so the composite's
// plan-time dry run runs on a real host, and checks what it made: the panel
// network, the listener on the gateway, the firewall rule and the proxy host.
func (r *m6pRun) expose(t *testing.T) {
	f := r.f
	in := panel.ExposeInput{Via: panel.ViaNPM, Hostname: m6pHost, HostID: f.localID}
	ch := m4ApplyAsync(r.ctx, t, f.api, "", panel.ActionAdd, in)
	if err := json.Unmarshal(ch.Output, &r.out); err != nil {
		t.Fatalf("output of %s: %s: %v", panel.ActionAdd, ch.Output, err)
	}
	if err := m6pCheckExposed(r.out, ch.ID, in); err != nil {
		t.Fatal(err)
	}
	r.change = ch.ID

	inspect := m3dDocker(r.ctx, t, f.controller, "network", "inspect", panel.NetworkName)
	if err := m6pCheckNetwork([]byte(inspect), r.out); err != nil {
		t.Error(err)
	}
	listen := net.JoinHostPort(r.out.Gateway.String(), strconv.Itoa(r.out.Port))
	err := eventually(r.ctx, 30*time.Second, func(ctx context.Context) error {
		out, err := f.controller.Run(ctx, "sudo ss -H -ltn")
		if err != nil {
			return err
		}
		if !m6pListening(out, listen) {
			return fmt.Errorf("harness: ss lists no listener on %s:\n%s", listen, out)
		}
		return nil
	})
	if err != nil {
		t.Errorf("the panel's listener: %v", err)
	}
	if st := m3Run(r.ctx, t, f.controller, "sudo ufw status"); !strings.Contains(st, panel.BridgeName) {
		t.Errorf("ufw status holds no rule on %s:\n%s", panel.BridgeName, st)
	}
	if hosts := r.n.listProxyHosts(t); !m6nHasDomain(hosts, m6pHost) {
		t.Errorf("proxy hosts %+v, want one for %s", hosts, m6pHost)
	}
}

// login: NPM serves the certificate Pebble issued for the panel's hostname and
// reaches the panel, and the admin logs in over the exposure, with the TOTP
// code the panel checks as everywhere.
func (r *m6pRun) login(t *testing.T) {
	f := r.f
	addr := net.JoinHostPort(targetIPv4, "443")
	r.x = m6pExposureAPI(f.scanner, addr, m6pHost, r.roots)
	err := eventually(r.ctx, 3*time.Minute, func(ctx context.Context) error {
		a, err := m6pRequest(ctx, f.scanner, "https", addr, m6pHost, r.roots, http.MethodGet, "/healthz", nil, nil)
		if err != nil {
			return err
		}
		if a.Status != http.StatusOK {
			return fmt.Errorf("harness: GET /healthz answered %d %.80q", a.Status, a.Body)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("GET https://%s/healthz from the scanner: %v", m6pHost, err)
	}
	if err := r.x.Login(r.ctx, adminName, f.password, nextCode(r.ctx, t, f)); err != nil {
		t.Fatalf("the admin's login through the exposure: %v", err)
	}
}

// banner: panel.exposure.status, the banner's data, reports the exposure both
// through the exposure and over the loopback API.
func (r *m6pRun) banner(t *testing.T) {
	for _, c := range []struct {
		name string
		api  *API
	}{{"through the exposure", r.x}, {"over the loopback API", r.f.api}} {
		var st panel.StatusOutput
		if err := c.api.Read(r.ctx, "", panel.ActionStatus+"/v1", struct{}{}, &st); err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		banner, err := m6pCheckStatus(st, r.out)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if banner != "Panel exposed via npm at panel.example.test" {
			t.Errorf("%s: the banner reads %q", c.name, banner)
		}
	}
}

// spoofedClients sends a refused login with forged client headers from a
// public source, a private source and a container on the panel network, each
// of which the panel must handle as an ordinary login; then a peer other than
// the proxy, on the host and on the panel network, gets no answer from the
// listener even with the headers the proxy would set.
func (r *m6pRun) spoofedClients(t *testing.T) {
	f := r.f
	header := m6pSpoofHeaders()
	header.Set("Content-Type", "application/json")
	for _, c := range []struct {
		name, user, addr string
		from             *vm.VM
	}{
		{"public source", m6pUserPublic, net.JoinHostPort(targetIPv4, "443"), f.scanner},
		{"private source", m6pUserPrivate, net.JoinHostPort(controllerPriv, "443"), f.agent},
	} {
		a, err := m6pRequest(r.ctx, c.from, "https", c.addr, m6pHost, r.roots, http.MethodPost, "/api/v1/auth/login", m6pLoginBody(c.user), header)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if err := m6pCheckRefusal(a.Status, a.Body); err != nil {
			t.Errorf("%s: %v", c.name, err)
		}
	}

	v := f.controller
	m3Run(r.ctx, t, v, m6nRunCmd(m6nRunSpec{Name: m6pClientName, Image: npm.Image, Network: panel.NetworkName, Entrypoint: "sleep", Args: []string{"3600"}}))
	m3dWaitRunning(r.ctx, t, v, m6pClientName)
	r.clientIP = m6nContainerIP(r.ctx, t, v, m6pClientName, panel.NetworkName)
	if reply := m6pExec(r.ctx, t, v, r.out.Proxy.String(), "443", m6pHost, m6pUserAttached); reply.Error != "" {
		t.Errorf("attached container: %s", reply.Error)
	} else if err := m6pCheckRefusal(reply.Status, []byte(reply.Body)); err != nil {
		t.Errorf("attached container: %v", err)
	}

	port := strconv.Itoa(r.out.Port)
	direct := "sudo curl -sk -o /dev/null -w '%{http_code}' --max-time 10 -H 'X-Forwarded-Proto: https' -H " +
		m3Quote("X-Darkpanel-Client: "+m6pForgedClient) + " " +
		m3Quote("https://"+net.JoinHostPort(r.out.Gateway.String(), port)+"/healthz") + " || true"
	if err := m6pDirectRefused(m3Run(r.ctx, t, v, direct)); err != nil {
		t.Errorf("the listener from the controller's host: %v", err)
	}
	if reply := m6pExec(r.ctx, t, v, r.out.Gateway.String(), port, m6pHost, m6pUserAttached); reply.Error == "" || reply.Status != 0 {
		t.Errorf("the listener from the attached container answered %+v, want an error and no status", reply)
	}
}

// plainHTTP: NPM forces SSL, so a plain-HTTP request, a login among them, is
// redirected to HTTPS and never reaches the panel.
func (r *m6pRun) plainHTTP(t *testing.T) {
	addr := net.JoinHostPort(targetIPv4, "80")
	for _, c := range []struct {
		method, path string
		body         []byte
		header       http.Header
	}{
		{http.MethodGet, "/", nil, nil},
		{http.MethodPost, "/api/v1/auth/login", m6pLoginBody(m6pUserPlain), http.Header{"Content-Type": {"application/json"}}},
	} {
		a, err := m6pRequest(r.ctx, r.f.scanner, "http", addr, m6pHost, nil, c.method, c.path, c.body, c.header)
		if err != nil {
			t.Errorf("%s http://%s%s: %v", c.method, m6pHost, c.path, err)
			continue
		}
		if err := m6pCheckPlain(a.Status, a.Location, m6pHost); err != nil {
			t.Errorf("%s http://%s%s: %v", c.method, m6pHost, c.path, err)
		}
	}
}

// audit: the audit log records each login with its true client and the
// proxy, none of the forged addresses, and no plain-HTTP login.
func (r *m6pRun) audit(t *testing.T) {
	var list coreactions.AuditList
	in := coreactions.AuditListInput{Action: auth.AuditLoginAction, Limit: coreactions.MaxAuditLimit}
	if err := r.f.api.Read(r.ctx, "", "core.audit.list/v1", in, &list); err != nil {
		t.Fatal(err)
	}
	via := " via " + r.out.Proxy.String()
	want := map[string]string{
		m6pUserPublic:   scannerIPv4 + via,
		adminName:       scannerIPv4 + via,
		m6pUserPrivate:  agentPriv + via,
		m6pUserAttached: r.clientIP + via,
	}
	if err := m6pCheckAudit(list.Entries, want, []string{m6pUserPlain}); err != nil {
		t.Error(err)
	}
}

// removeThroughTheExposure plans and applies panel.exposure.remove over the
// exposure it removes and settles it over the loopback API. Its last step, the
// firewall's, is accepted only because the composite retired the exposure
// after revoke.
func (r *m6pRun) removeThroughTheExposure(t *testing.T) {
	// Docker refuses to remove a network a container is still attached to.
	m3Run(r.ctx, t, r.f.controller, "sudo docker rm -f "+m6pClientName+" >/dev/null 2>&1 || true")
	ch := m6pApplyThrough(r.ctx, t, r.x, r.f.api, panel.ActionRemove, panel.RemoveInput{})
	var out panel.RemoveOutput
	if err := json.Unmarshal(ch.Output, &out); err != nil {
		t.Fatalf("output of %s: %s: %v", panel.ActionRemove, ch.Output, err)
	}
	if want := (panel.RemoveOutput{ExposureID: r.out.ExposureID, Hostname: m6pHost}); out != want {
		t.Errorf("output of %s %+v, want %+v", panel.ActionRemove, out, want)
	}
}

// afterRemoval: nothing of the exposure is left, and the panel is no longer
// reachable through the proxy.
func (r *m6pRun) afterRemoval(t *testing.T) {
	f := r.f
	var st panel.StatusOutput
	if err := f.api.Read(r.ctx, "", panel.ActionStatus+"/v1", struct{}{}, &st); err != nil {
		t.Error(err)
	} else if st.Exposed {
		t.Errorf("panel.exposure.status %+v after the removal, want not exposed", st)
	}
	if out, err := f.controller.Run(r.ctx, "sudo docker network inspect "+panel.NetworkName); err == nil {
		t.Errorf("network %s is still there after the removal:\n%s", panel.NetworkName, out)
	}
	listen := net.JoinHostPort(r.out.Gateway.String(), strconv.Itoa(r.out.Port))
	if out := m3Run(r.ctx, t, f.controller, "sudo ss -H -ltn"); m6pListening(out, listen) {
		t.Errorf("a socket still listens on %s after the removal:\n%s", listen, out)
	}
	if out := m3Run(r.ctx, t, f.controller, "sudo ufw status"); strings.Contains(out, panel.BridgeName) {
		t.Errorf("ufw status still holds a rule on %s after the removal:\n%s", panel.BridgeName, out)
	}
	if hosts := r.n.listProxyHosts(t); m6nHasDomain(hosts, m6pHost) {
		t.Errorf("proxy hosts %+v after the removal, want none for %s", hosts, m6pHost)
	}
	header := http.Header{"Content-Type": {"application/json"}}
	err := eventually(r.ctx, time.Minute, func(ctx context.Context) error {
		a, err := m6pRequest(ctx, f.scanner, "https", net.JoinHostPort(targetIPv4, "443"), m6pHost, r.roots,
			http.MethodPost, "/api/v1/auth/login", m6pLoginBody(m6pUserAfter), header)
		if err == nil && m6pCheckRefusal(a.Status, a.Body) == nil {
			return errors.New("harness: a login through the proxy still reaches the panel")
		}
		return nil
	})
	if err != nil {
		t.Errorf("https://%s after the removal: %v", m6pHost, err)
	}
}

// logDiagnostics logs what m6pDiagnostics collects. A command that fails is
// logged with its error and never fails the test.
func (r *m6pRun) logDiagnostics(t *testing.T) {
	for _, d := range m6pDiagnostics() {
		ctx, cancel := context.WithTimeout(r.ctx, m6nDiagnosticTimeout)
		out, err := r.f.controller.Run(ctx, d.Command)
		cancel()
		out = m5dCapDiagnostic(out, strings.Contains(d.Command, "logs"))
		if err != nil {
			t.Logf("diagnostic %s\n$ %s\n%s\nerror: %v", d.Label, d.Command, out, err)
			continue
		}
		t.Logf("diagnostic %s\n$ %s\n%s", d.Label, d.Command, out)
	}
}
