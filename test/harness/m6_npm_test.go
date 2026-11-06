//go:build harness

package harness

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/npm"
	"github.com/darkraise/darkpanel/internal/paths"
	"github.com/darkraise/darkpanel/test/harness/vm"
)

func TestM6nUnitRunCmd(t *testing.T) {
	got := m6nRunCmd(m6nRunSpec{
		Name: "n", Image: "img", Network: "net", Alias: "al", Entrypoint: "ep", Restart: "unless-stopped",
		Labels: []string{"a=b"}, Publish: []string{"127.0.0.1:81:81"}, Env: []string{"K=v w"}, Volumes: []string{"/h:/c:ro"},
		Args: []string{"-dns01", "", "x"},
	})
	want := "sudo docker run -d --name n --restart unless-stopped --network net --network-alias al --label a=b -p 127.0.0.1:81:81 -e 'K=v w' -v /h:/c:ro --entrypoint ep img -dns01 '' x"
	if got != want {
		t.Errorf("m6nRunCmd\n got %s\nwant %s", got, want)
	}
	if got, want := m6nRunCmd(m6nRunSpec{Name: "n", Image: "img"}), "sudo docker run -d --name n img"; got != want {
		t.Errorf("m6nRunCmd of a bare spec\n got %s\nwant %s", got, want)
	}
}

func TestM6nUnitNPMSpec(t *testing.T) {
	cmd := m6nRunCmd(m6nNPMSpec("img@sha256:aa", []string{"LE_SERVER=https://pebble:14000/dir"}, "/var/lib/m6n/ca.pem"))
	for _, want := range []string{
		"--name darkpanel-npm", "--network darkpanel-npm", "--restart unless-stopped", "--label darkpanel.module=npm",
		"-p 80:80", "-p 443:443", "-p 127.0.0.1:81:81", "-v darkpanel-npm-data:/data", "-v darkpanel-npm-letsencrypt:/etc/letsencrypt",
		"-v /var/lib/m6n/ca.pem:/m6n/pebble.minica.pem:ro", "-e LE_SERVER=https://pebble:14000/dir", " img@sha256:aa",
	} {
		if !strings.Contains(cmd, want) {
			t.Errorf("the NPM container command lacks %q:\n%s", want, cmd)
		}
	}
}

func TestM6nUnitInspectEnv(t *testing.T) {
	env, err := m6nInspectEnv([]byte(`[{"Config":{"Env":["PATH=/bin","DISABLE_IPV6=true"]}}]`))
	if err != nil || !slices.Equal(env, []string{"PATH=/bin", "DISABLE_IPV6=true"}) {
		t.Errorf("m6nInspectEnv = %v, %v, want the two variables", env, err)
	}
	for _, bad := range []string{`[]`, `[{},{}]`, `{`} {
		if _, err := m6nInspectEnv([]byte(bad)); err == nil {
			t.Errorf("m6nInspectEnv(%s) was accepted, want an error", bad)
		}
	}
}

func TestM6nUnitPebbleConfig(t *testing.T) {
	var c struct {
		Pebble struct {
			ListenAddress string `json:"listenAddress"`
			HTTPPort      int    `json:"httpPort"`
			TLSPort       int    `json:"tlsPort"`
			Certificate   string `json:"certificate"`
			PrivateKey    string `json:"privateKey"`
		} `json:"pebble"`
	}
	if err := json.Unmarshal([]byte(m6nPebbleConfig), &c); err != nil {
		t.Fatal(err)
	}
	p := c.Pebble
	if p.HTTPPort != 80 || p.TLSPort != 443 || p.ListenAddress != "0.0.0.0:14000" || p.Certificate == "" || p.PrivateKey == "" {
		t.Errorf("Pebble configuration %+v, want validation on 80 and 443, the directory on 14000 and a certificate", p)
	}
}

func TestM6nUnitRawHeaders(t *testing.T) {
	h, err := m6nRawHeaders([]byte(`["Host","a.test","host","b.test","X-Darkpanel-Client","198.18.0.20"]`))
	if err != nil {
		t.Fatal(err)
	}
	if got := h.Values("Host"); !slices.Equal(got, []string{"a.test", "b.test"}) {
		t.Errorf("Host values %q, want both repetitions in order", got)
	}
	if got := h.Values("x-darkpanel-client"); !slices.Equal(got, []string{"198.18.0.20"}) {
		t.Errorf("X-Darkpanel-Client values %q, want the one", got)
	}
	for _, bad := range []string{`["Host"]`, `{"host":"a"}`, ``, `[1,2]`} {
		if _, err := m6nRawHeaders([]byte(bad)); err == nil {
			t.Errorf("m6nRawHeaders(%q) was accepted, want an error", bad)
		}
	}
}

// m6nUnitCA returns a self-signed CA certificate with its key.
func m6nUnitCA(t *testing.T, name string, notAfter time.Time) (*x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: name},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: notAfter, IsCA: true, BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return cert, key
}

// m6nUnitLeaf returns a leaf for host signed by ca.
func m6nUnitLeaf(t *testing.T, ca *x509.Certificate, caKey *ecdsa.PrivateKey, host string, notAfter time.Time) *x509.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: host}, DNSNames: []string{host},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: notAfter, KeyUsage: x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return cert
}

func TestM6nUnitCheckChain(t *testing.T) {
	now := time.Now()
	root, rootKey := m6nUnitCA(t, "Pebble Root", now.Add(24*time.Hour))
	other, _ := m6nUnitCA(t, "Other Root", now.Add(24*time.Hour))
	rootPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: root.Raw})
	otherPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: other.Raw})
	leaf := m6nUnitLeaf(t, root, rootKey, "app.example.test", now.Add(time.Hour))
	expired := m6nUnitLeaf(t, root, rootKey, "app.example.test", now.Add(-time.Minute))
	for _, tc := range []struct {
		name  string
		chain []*x509.Certificate
		root  []byte
		host  string
		want  string // "" when accepted
	}{
		{"valid", []*x509.Certificate{leaf}, rootPEM, "app.example.test", ""},
		{"other host", []*x509.Certificate{leaf}, rootPEM, "other.example.test", "not other.example.test"},
		{"expired", []*x509.Certificate{expired}, rootPEM, "app.example.test", "expired"},
		{"unknown root", []*x509.Certificate{leaf}, otherPEM, "app.example.test", "unknown authority"},
		{"no chain", nil, rootPEM, "app.example.test", "no certificate"},
		{"root not PEM", []*x509.Certificate{leaf}, []byte("x"), "app.example.test", "not a PEM"},
	} {
		err := m6nCheckChain(tc.chain, tc.root, tc.host, now)
		switch {
		case tc.want == "" && err != nil:
			t.Errorf("%s: %v, want it accepted", tc.name, err)
		case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
			t.Errorf("%s: %v, want an error containing %q", tc.name, err, tc.want)
		}
	}
}

func TestM6nUnitIsApp(t *testing.T) {
	for _, tc := range []struct {
		name string
		r    m6nResponse
		want bool
	}{
		{"the target's 404", m6nResponse{Status: 404, Body: []byte("404 page not found\n")}, true},
		{"NPM's 404 page", m6nResponse{Status: 404, Body: []byte("<html>404 Not Found nginx</html>")}, false},
		{"bad gateway", m6nResponse{Status: 502, Body: []byte("404 page not found")}, false},
	} {
		if got := m6nIsApp(tc.r); got != tc.want {
			t.Errorf("%s: m6nIsApp = %v, want %v", tc.name, got, tc.want)
		}
	}
}

const (
	m6nAcmeEmail = "ops@darkpanel.dev"
	// m6nCABundleVar is the variable certbot reads for the CA bundle of the ACME server.
	m6nCABundleVar = "REQUESTS_CA_BUNDLE"
	// m6nDir holds the files the scenario puts on the agent VM.
	m6nDir = "/var/lib/m6n"
	// m6nNPMName is both the container and the network spec 6 names.
	m6nNPMName      = "darkpanel-npm"
	m6nPebbleName   = "m6n-pebble"
	m6nChallName    = "m6n-challtestsrv"
	m6nAppName      = "m6n-app"
	m6nEchoName     = "m6n-echo"
	m6nBridgeName   = "m6n-bridge"
	m6nAppNet       = "m6n-net"
	m6nPanelNet     = "darkpanel-panel"
	m6nPanelSubnet  = "10.99.77.0/24"
	m6nPanelGateway = "10.99.77.1"
	// m6nDirectory is Pebble's ACME directory as the NPM container reaches
	// it: the alias pebble is on the SAN list of Pebble's own certificate.
	m6nDirectory = "https://pebble:14000/dir"
	// m6nCAInNPM is where the NPM container finds the CA file that signed
	// Pebble's own certificate.
	m6nCAInNPM  = "/m6n/pebble.minica.pem"
	m6nAppHost  = "m6n-app.example.test"
	m6nEchoHost = "m6n-echo.example.test"
	m6nAppPort  = 8055
	m6nEchoPort = 8080
	// m6nAppBody is what the management server of pebble-challtestsrv answers
	// for a path it does not serve: Go's own 404, which NPM's pages are not.
	m6nAppBody = "404 page not found"
	// m6nForged is a client header value the scanner sends and NPM must overwrite.
	m6nForged = "203.0.113.99"
	// m6nUnsupportedReason is the reason spec 6 gives for a refused target.
	m6nUnsupportedReason = "connect the container to a user-defined network"
	// m6nEchoScript answers every request with its raw header lines, in
	// order, as a JSON array of alternating names and values.
	m6nEchoScript = `require("http").createServer((q, r) => r.end(JSON.stringify(q.rawHeaders))).listen(8080)`
	// m6nPebbleConfig is Pebble's own test configuration with its validation
	// ports moved to 80 and 443, where NPM listens.
	m6nPebbleConfig = `{
  "pebble": {
    "listenAddress": "0.0.0.0:14000",
    "managementListenAddress": "0.0.0.0:15000",
    "certificate": "test/certs/localhost/cert.pem",
    "privateKey": "test/certs/localhost/key.pem",
    "httpPort": 80,
    "tlsPort": 443,
    "ocspResponderURL": "",
    "externalAccountBindingRequired": false
  }
}
`
)

// m6nRunSpec describes one container started with docker run on the agent VM.
type m6nRunSpec struct {
	Name, Image, Network, Alias, Entrypoint, Restart string
	Labels, Publish, Env, Volumes, Args              []string
}

// m6nRunCmd returns the shell command line that runs s detached.
func m6nRunCmd(s m6nRunSpec) string {
	words := []string{"sudo", "docker", "run", "-d", "--name", s.Name}
	if s.Restart != "" {
		words = append(words, "--restart", s.Restart)
	}
	if s.Network != "" {
		words = append(words, "--network", s.Network)
	}
	if s.Alias != "" {
		words = append(words, "--network-alias", s.Alias)
	}
	for _, l := range s.Labels {
		words = append(words, "--label", l)
	}
	for _, p := range s.Publish {
		words = append(words, "-p", p)
	}
	for _, e := range s.Env {
		words = append(words, "-e", e)
	}
	for _, v := range s.Volumes {
		words = append(words, "-v", v)
	}
	if s.Entrypoint != "" {
		words = append(words, "--entrypoint", s.Entrypoint)
	}
	words = append(words, s.Image)
	words = append(words, s.Args...)
	return m3dxShell(words...)
}

// m6nNPMSpec is the darkpanel-npm container as the deploy creates it, with env
// and the mounted CA file of the ACME server added.
func m6nNPMSpec(image string, env []string, caFile string) m6nRunSpec {
	return m6nRunSpec{
		Name: m6nNPMName, Image: image, Network: m6nNPMName, Restart: "unless-stopped",
		Labels:  []string{"darkpanel.module=npm"},
		Publish: []string{"80:80", "443:443", "127.0.0.1:81:81"},
		Env:     env,
		Volumes: []string{"darkpanel-npm-data:/data", "darkpanel-npm-letsencrypt:/etc/letsencrypt", caFile + ":" + m6nCAInNPM + ":ro"},
	}
}

// m6nInspectEnv returns the environment of the one container in the JSON array
// that docker inspect prints.
func m6nInspectEnv(inspect []byte) ([]string, error) {
	var cs []struct {
		Config struct {
			Env []string `json:"Env"`
		} `json:"Config"`
	}
	if err := json.Unmarshal(inspect, &cs); err != nil {
		return nil, fmt.Errorf("harness: docker inspect: %w", err)
	}
	if len(cs) != 1 {
		return nil, fmt.Errorf("harness: docker inspect printed %d containers, want 1", len(cs))
	}
	return cs[0].Config.Env, nil
}

// m6nRawHeaders reads the echo server's answer, a JSON array of alternating
// header names and values, keeping every repetition in order.
func m6nRawHeaders(body []byte) (http.Header, error) {
	var words []string
	if err := json.Unmarshal(body, &words); err != nil {
		return nil, fmt.Errorf("harness: the echo server's answer %.80q: %w", body, err)
	}
	if len(words)%2 != 0 {
		return nil, fmt.Errorf("harness: the echo server's answer holds %d words, want names and values in pairs", len(words))
	}
	h := http.Header{}
	for i := 0; i < len(words); i += 2 {
		h.Add(words[i], words[i+1])
	}
	return h, nil
}

// m6nResponse is what m6nGet returns.
type m6nResponse struct {
	Status int
	Body   []byte
	Chain  []*x509.Certificate
}

// m6nIsApp reports whether r is the answer of the exposed target rather than
// one of NPM's own pages.
func m6nIsApp(r m6nResponse) bool {
	return r.Status == http.StatusNotFound && string(bytes.TrimSpace(r.Body)) == m6nAppBody
}

// m6nCheckChain verifies the served chain for host at now against the root in
// rootPEM, with the rest of the chain as intermediates.
func m6nCheckChain(chain []*x509.Certificate, rootPEM []byte, host string, now time.Time) error {
	if len(chain) == 0 {
		return errors.New("harness: the server presented no certificate")
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(rootPEM) {
		return errors.New("harness: Pebble's root is not a PEM certificate")
	}
	inter := x509.NewCertPool()
	for _, c := range chain[1:] {
		inter.AddCert(c)
	}
	if _, err := chain[0].Verify(x509.VerifyOptions{DNSName: host, Roots: roots, Intermediates: inter, CurrentTime: now}); err != nil {
		return fmt.Errorf("harness: the served certificate: %w", err)
	}
	return nil
}

var m6nFlag = flag.Bool("m6n", false, "run TestM6NPM; needs -os and -m2, and is a go test run of its own")

// m6nDeadline bounds TestM6NPM. The workflow step that runs it must keep its
// -timeout at least m6nDeadline plus 10 minutes, for the cleanups that stop the VMs.
const m6nDeadline = 40 * time.Minute

// m6nGet GETs https://host/path through the network of from, which reaches
// the agent's port 443 whatever host names, with header added. The server
// name goes out in the handshake so that NPM picks the proxy host.
func m6nGet(ctx context.Context, from *vm.VM, host, path string, header http.Header) (m6nResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	u := url.URL{Scheme: "https", Host: host, Path: path}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return m6nResponse{}, fmt.Errorf("harness: %w", err)
	}
	for k, vs := range header {
		req.Header[k] = vs
	}
	tr := &http.Transport{
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return from.DialContext(ctx, network, net.JoinHostPort(agentIPv4, "443"))
		},
		DisableKeepAlives: true,
		TLSClientConfig:   &tls.Config{InsecureSkipVerify: true, ServerName: host}, //nolint:gosec // G402: the chain is checked by m6nCheckChain
	}
	defer tr.CloseIdleConnections()
	resp, err := (&http.Client{Transport: tr}).Do(req)
	if err != nil {
		return m6nResponse{}, fmt.Errorf("harness: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return m6nResponse{}, fmt.Errorf("harness: GET %s: %w", u.String(), err)
	}
	out := m6nResponse{Status: resp.StatusCode, Body: body}
	if resp.TLS != nil {
		out.Chain = resp.TLS.PeerCertificates
	}
	return out, nil
}

// m6nPull pulls ref on v, retrying for ten minutes, because the registries
// refuse anonymous pulls now and then.
func m6nPull(ctx context.Context, t *testing.T, v *vm.VM, ref string) {
	t.Helper()
	err := eventually(ctx, 10*time.Minute, func(ctx context.Context) error {
		out, err := v.Run(ctx, "sudo docker pull "+m3dxShell(ref))
		if err != nil {
			return fmt.Errorf("harness: %w\n%s", err, out)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("docker pull %s: %v", ref, err)
	}
}

// m6nContainerIP returns the address of container on network.
func m6nContainerIP(ctx context.Context, t *testing.T, v *vm.VM, container, network string) string {
	t.Helper()
	format := `{{(index .NetworkSettings.Networks "` + network + `").IPAddress}}`
	out := strings.TrimSpace(m3Run(ctx, t, v, "sudo docker inspect -f "+m3Quote(format)+" "+m3dxShell(container)))
	if _, err := netip.ParseAddr(out); err != nil {
		t.Fatalf("container %s has no address on %s: %q", container, network, out)
	}
	return out
}

// m6nNetworkMembers returns the names of the containers attached to network.
func m6nNetworkMembers(ctx context.Context, t *testing.T, v *vm.VM, network string) []string {
	t.Helper()
	format := `{{range .Containers}}{{.Name}} {{end}}`
	return strings.Fields(m3Run(ctx, t, v, "sudo docker network inspect -f "+m3Quote(format)+" "+m3dxShell(network)))
}

// m6nDiagnostic is one command that a failed step logs.
type m6nDiagnostic struct{ Label, Command string }

// m6nDiagnostics are what a failed step logs from the agent: the containers,
// the networks, the end of each container's log and NPM's own answer.
func m6nDiagnostics() []m6nDiagnostic {
	ds := []m6nDiagnostic{
		{"containers", `sudo docker ps -a --format 'table {{.Names}}\t{{.Image}}\t{{.Status}}'`},
		{"networks", "sudo docker network ls"},
		{"NPM API", "curl -sS --max-time 10 http://127.0.0.1:81/api/"},
		{"ufw status", "sudo ufw status verbose"},
	}
	for _, name := range []string{m6nNPMName, m6nPebbleName, m6nChallName, m6nAppName, m6nEchoName} {
		ds = append(ds, m6nDiagnostic{name + " log", "sudo docker logs --tail 40 " + name + " 2>&1"})
	}
	return ds
}

// m6nRun is what the steps of TestM6NPM share.
type m6nRun struct {
	ctx   context.Context
	f     *fleet
	npmIP string // darkpanel-npm's address on its network, set by harnessSettings
	root  []byte // Pebble's root certificate, PEM, set by harnessSettings
}

// TestM6NPM is the NPM half of M6 (spec 6, Verification): a real NPM,
// deployed through npm.instance.deploy-public, issues a certificate from Pebble
// for a container it exposes, forwards an ordinary request with one Host header
// and an overwritten X-Darkpanel-Client, refuses a default-bridge-only target
// in the preview and the reserved upstreams outside their composites, removes
// an exposure by claim, and survives npm.instance.upgrade. The workflow selects
// it with -run, -os, -m2 and -m6n.
func TestM6NPM(t *testing.T) {
	if *osFlag == "" || *m2Flag == "" || !*m6nFlag {
		t.Skip("no -os, -m2 or -m6n given: the VM tests run in .github/workflows/harness.yml")
	}
	ctx, cancel := context.WithTimeout(context.Background(), m6nDeadline)
	t.Cleanup(cancel)
	r := &m6nRun{ctx: ctx, f: m5dLab(ctx, t)}
	steps := []struct {
		name string
		gate bool // later steps need this one
		run  func(t *testing.T)
	}{
		{"prepare", true, r.prepare},
		{"deploy", true, r.deploy},
		{"harness-settings", true, r.harnessSettings},
		{"admin", false, r.admin},
		{"public", true, r.public},
		{"unsupported-target", false, r.unsupportedTarget},
		{"reserved-upstreams", false, r.reservedUpstreams},
		{"expose", true, r.expose},
		{"headers", false, r.headers},
		{"expose-remove", true, r.exposeRemove},
		{"upgrade", true, r.upgrade},
		{"expose-remove-last", false, r.exposeRemoveLast},
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

// image returns the pinned reference of the container image name.
func (r *m6nRun) image(t *testing.T, name string) string {
	t.Helper()
	ref, err := m3dxImage(name)
	if err != nil {
		t.Fatal(err)
	}
	return ref
}

// prepare puts the files and images the scenario needs on the agent: Pebble's
// configuration and CA file, and the three images pulled once, with retries.
func (r *m6nRun) prepare(t *testing.T) {
	v := r.f.agent
	m3Run(r.ctx, t, v, "sudo install -d -m 0755 "+m6nDir)
	conf := filepath.Join(t.TempDir(), "pebble-config.json")
	if err := os.WriteFile(conf, []byte(m6nPebbleConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := v.CopyFile(r.ctx, conf, m6nDir+"/pebble-config.json", 0o644); err != nil {
		t.Fatal(err)
	}
	pebble, chall := r.image(t, "pebble"), r.image(t, "pebble-challtestsrv")
	for _, ref := range []string{npm.Image, pebble, chall} {
		m6nPull(r.ctx, t, v, ref)
	}
	m3Run(r.ctx, t, v, "sudo docker rm -f m6n-ca >/dev/null 2>&1 || true")
	m3dDocker(r.ctx, t, v, "create", "--name", "m6n-ca", pebble)
	m3dDocker(r.ctx, t, v, "cp", "m6n-ca:/test/certs/pebble.minica.pem", m6nDir+"/pebble.minica.pem")
	m3dDocker(r.ctx, t, v, "rm", "m6n-ca")
}

// deploy deploys NPM through npm.instance.deploy-public. The lab leaves the
// Docker integration of the firewall off, so the composite has no ports step
// and the instance is public only after npm.instance.confirm-public.
func (r *m6nRun) deploy(t *testing.T) {
	var fd struct {
		Enabled   bool `json:"enabled"`
		Supported bool `json:"supported"`
	}
	if err := r.f.api.Resource(r.ctx, r.f.agentID, "firewall.docker", &fd); err != nil {
		t.Fatal(err)
	}
	if fd.Enabled {
		t.Fatal("the lab's Docker integration is enabled, which this scenario does not expect")
	}
	ch := m4ApplyAsync(r.ctx, t, r.f.api, "", "npm.instance.deploy-public",
		npm.DeployPublicInput{HostID: r.f.agentID, AcmeEmail: m6nAcmeEmail, Integration: false})
	var out npm.DeployPublicOutput
	if err := json.Unmarshal(ch.Output, &out); err != nil {
		t.Fatalf("output of npm.instance.deploy-public: %v", err)
	}
	if out.ContainerID == "" || out.PortsOpened {
		t.Errorf("deploy-public output %+v, want a container id and no ports step", out)
	}
}

// w10hxSetContainerCmd returns the command that writes id as the container of
// the NPM instance row in the agent database db. It prints the number of rows
// the update changed. The caller has checked that id is 64 hexadecimal
// characters.
func w10hxSetContainerCmd(db, id string) string {
	return "sudo sqlite3 -cmd '.timeout 10000' " + db + " \"UPDATE npm_instances SET container_id = '" + id + "' WHERE id = 1; SELECT changes();\""
}

// w10hxChanged accepts the output of w10hxSetContainerCmd only when exactly
// one row changed, so an update that matched no row is not mistaken for a
// write.
func w10hxChanged(out string) error {
	if strings.TrimSpace(out) != "1" {
		return fmt.Errorf("harness: the update of the instance row printed %q, want 1 changed row", out)
	}
	return nil
}

// harnessSettings is what only a harness deployment has (spec 6, Verification):
// NPM's certbot talks to Pebble. The image takes the ACME directory and the CA
// file from its environment, which a deployed container cannot be given, so the
// container is recreated over SSH with the same name, labels, mounts, ports
// and network plus the variables; the volumes, and with them the admin, stay.
// The module finds its container by the id in its instance row, so the new id
// is written there; the agent database is SQLite and takes the write while the
// agent runs.
// Pebble and its DNS server then join the NPM network, and the DNS server
// answers every name with NPM's address.
func (r *m6nRun) harnessSettings(t *testing.T) {
	v := r.f.agent
	inspect := m3dDocker(r.ctx, t, v, "inspect", m6nNPMName)
	env, err := m6nInspectEnv([]byte(inspect))
	if err != nil {
		t.Fatal(err)
	}
	env = append(env, "LE_SERVER="+m6nDirectory, m6nCABundleVar+"="+m6nCAInNPM)
	m3dDocker(r.ctx, t, v, "rm", "-f", m6nNPMName)
	m3Run(r.ctx, t, v, m6nRunCmd(m6nNPMSpec(npm.Image, env, m6nDir+"/pebble.minica.pem")))
	id := strings.TrimSpace(m3dDocker(r.ctx, t, v, "inspect", "-f", "{{.Id}}", m6nNPMName))
	if len(id) != 64 || strings.Trim(id, "0123456789abcdef") != "" {
		t.Fatalf("harness: the recreated container's id is %q", id)
	}
	if err := w10hxChanged(m3Run(r.ctx, t, v, w10hxSetContainerCmd(paths.Production().AgentDB(), id))); err != nil {
		t.Fatal(err)
	}
	err = eventually(r.ctx, 3*time.Minute, func(ctx context.Context) error {
		out, err := v.Run(ctx, "curl -fsS --max-time 10 http://127.0.0.1:81/api/")
		if err != nil {
			return err
		}
		var health struct {
			Setup bool `json:"setup"`
		}
		if err := json.Unmarshal([]byte(out), &health); err != nil {
			return err
		}
		if !health.Setup {
			return errors.New("harness: GET /api reports setup false: the admin did not survive the recreation")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("NPM after the recreation: %v", err)
	}
	r.npmIP = m6nContainerIP(r.ctx, t, v, m6nNPMName, m6nNPMName)

	m3Run(r.ctx, t, v, m6nRunCmd(m6nRunSpec{
		Name: m6nChallName, Image: r.image(t, "pebble-challtestsrv"), Network: m6nNPMName,
		Args: []string{"-defaultIPv4", r.npmIP, "-defaultIPv6", "", "-http01", "", "-https01", "", "-tlsalpn01", ""},
	}))
	m3dWaitRunning(r.ctx, t, v, m6nChallName)
	dns := m6nContainerIP(r.ctx, t, v, m6nChallName, m6nNPMName) + ":8053"
	m3Run(r.ctx, t, v, m6nRunCmd(m6nRunSpec{
		Name: m6nPebbleName, Image: r.image(t, "pebble"), Network: m6nNPMName, Alias: "pebble",
		Publish: []string{"127.0.0.1:15000:15000"},
		Env:     []string{"PEBBLE_VA_NOSLEEP=1", "PEBBLE_WFE_NONCEREJECT=0"},
		Volumes: []string{m6nDir + "/pebble-config.json:/m6n/pebble-config.json:ro"},
		Args:    []string{"-config", "/m6n/pebble-config.json", "-dnsserver", dns},
	}))
	err = eventually(r.ctx, time.Minute, func(ctx context.Context) error {
		out, err := v.Run(ctx, "curl -fsSk --max-time 10 https://127.0.0.1:15000/roots/0")
		if err != nil {
			return err
		}
		if !strings.Contains(out, "BEGIN CERTIFICATE") {
			return fmt.Errorf("harness: Pebble answered %.80q for its root", out)
		}
		r.root = []byte(out)
		return nil
	})
	if err != nil {
		t.Fatalf("Pebble: %v", err)
	}
}

// admin: npm.instance.status shows the instance running with its API ready
// after the recreation, npm.instance.list shows it on the fleet read,
// npm.admin.reveal-password returns a 32-byte base64url password, and NPM
// accepts that password for the admin's email. The password is sent on
// standard input, never named in a command line, a log or a message.
func (r *m6nRun) admin(t *testing.T) {
	var st npm.InstanceStatus
	if err := r.f.api.Read(r.ctx, r.f.agentID, "npm.instance.status/v1", npm.StatusInput{}, &st); err != nil {
		t.Fatal(err)
	}
	if !st.Deployed || !st.Running || !st.APIReady || st.Image != npm.Image || st.PinnedImage != npm.Image || st.AdminEmail != m6nAcmeEmail || st.Public != "no" {
		t.Errorf("npm.instance.status %+v, want a running, ready instance of %s with admin %s that is not public yet", st, npm.Image, m6nAcmeEmail)
	}
	var instances npm.InstancesOutput
	if err := r.f.api.Read(r.ctx, "", "npm.instance.list/v1", struct{}{}, &instances); err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(instances.Instances, func(i npm.HostInstance) bool { return i.HostID == r.f.agentID && i.Status.Deployed }) {
		t.Errorf("npm.instance.list %+v, want the agent's host with a deployed instance", instances.Instances)
	}
	var pw npm.AdminPassword
	if err := m4Reveal(r.ctx, r.f.api, r.f.agentID, "npm.admin.reveal-password", npm.RevealInput{}, &pw); err != nil {
		t.Fatal(err)
	}
	raw, err := base64.RawURLEncoding.DecodeString(pw.Password)
	if pw.Email != m6nAcmeEmail || err != nil || len(raw) != 32 {
		t.Fatalf("npm.admin.reveal-password gave email %q and a password of %d characters (decode error %v), want %s and 32 random bytes in base64url", pw.Email, len(pw.Password), err, m6nAcmeEmail)
	}
	body, err := json.Marshal(map[string]string{"identity": pw.Email, "secret": pw.Password})
	if err != nil {
		t.Fatal(err)
	}
	cmd := "curl -fsS --max-time 20 -o /dev/null -X POST -H 'Content-Type: application/json' --data-binary @- http://127.0.0.1:81/api/tokens"
	if _, err := r.f.agent.RunInput(r.ctx, cmd, bytes.NewReader(body)); err != nil {
		t.Errorf("POST /api/tokens with the revealed password: %v", err)
	}
}

// public: ports 80 and 443 answer from the scanner, then the operator's
// confirmation marks the instance public, which npm.expose.container requires.
func (r *m6nRun) public(t *testing.T) {
	for _, port := range []string{"80", "443"} {
		err := eventually(r.ctx, time.Minute, func(ctx context.Context) error {
			c, err := r.f.scanner.DialContext(ctx, "tcp", net.JoinHostPort(agentIPv4, port))
			if err != nil {
				return err
			}
			return c.Close()
		})
		if err != nil {
			t.Fatalf("%s:%s from the scanner: %v", agentIPv4, port, err)
		}
	}
	m4ApplyAsync(r.ctx, t, r.f.api, r.f.agentID, "npm.instance.confirm-public", npm.ConfirmPublicInput{})
	var st npm.InstanceStatus
	if err := r.f.api.Read(r.ctx, r.f.agentID, "npm.instance.status/v1", npm.StatusInput{}, &st); err != nil {
		t.Fatal(err)
	}
	if st.Public != "confirmed" {
		t.Errorf("npm.instance.status public %q after npm.instance.confirm-public, want confirmed", st.Public)
	}
}

// m6nRefused requires that err is an API refusal with code and, when reason is
// not empty, a detail naming it.
func m6nRefused(t *testing.T, what string, err error, code errcode.Code, reason string) {
	t.Helper()
	var apiErr *APIError
	switch {
	case err == nil:
		t.Errorf("%s was planned, want it refused with %s", what, code)
	case !errors.As(err, &apiErr) || apiErr.Code != string(code):
		t.Errorf("%s: %v, want an API refusal with code %s", what, err, code)
	case reason != "" && !strings.Contains(apiErr.Detail, reason):
		t.Errorf("%s: detail %q, want it to name %q", what, apiErr.Detail, reason)
	}
}

// listProxyHosts reads npm.proxyhost.list.
func (r *m6nRun) listProxyHosts(t *testing.T) []npm.ProxyHostView {
	t.Helper()
	var l npm.ProxyHostList
	if err := r.f.api.Read(r.ctx, r.f.agentID, "npm.proxyhost.list/v1", npm.ProxyHostListInput{}, &l); err != nil {
		t.Fatal(err)
	}
	return l.Items
}

// m6nHasDomain reports whether a proxy host of hosts serves domain.
func m6nHasDomain(hosts []npm.ProxyHostView, domain string) bool {
	return slices.ContainsFunc(hosts, func(h npm.ProxyHostView) bool { return slices.Contains(h.Domains, domain) })
}

// unsupportedTarget: a container on the default bridge only cannot be reached
// by name, and the exposure is refused in its preview.
func (r *m6nRun) unsupportedTarget(t *testing.T) {
	v := r.f.agent
	m3Run(r.ctx, t, v, m6nRunCmd(m6nRunSpec{
		Name: m6nBridgeName, Image: r.image(t, "pebble-challtestsrv"),
		Args: []string{"-dnsserver", "", "-http01", "", "-https01", "", "-tlsalpn01", ""},
	}))
	m3dWaitRunning(r.ctx, t, v, m6nBridgeName)
	_, err := m4Plan(r.ctx, r.f.api, "", "npm.expose.container", npm.ExposeInput{
		HostID: r.f.agentID, Container: m6nBridgeName, Port: m6nAppPort, Network: "bridge", Domains: []string{"m6n-bridge.example.test"},
	})
	m6nRefused(t, "the exposure of a default-bridge-only container", err, errcode.NPMUnsupportedTarget, m6nUnsupportedReason)
}

// reservedUpstreams: npm.proxyhost.add, called on its own, refuses the
// upstreams that belong to the admin and panel exposures, whose composites
// are the only parents allowed to use them. A network named darkpanel-panel
// stands in for the panel's.
func (r *m6nRun) reservedUpstreams(t *testing.T) {
	v := r.f.agent
	m3dDocker(r.ctx, t, v, "network", "create", "--subnet", m6nPanelSubnet, "--gateway", m6nPanelGateway, m6nPanelNet)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		_, _ = v.Run(ctx, "sudo docker network rm "+m6nPanelNet)
	})
	for i, c := range []struct {
		name, host string
		port       int
	}{
		{"the admin port on 127.0.0.1", "127.0.0.1", 81},
		{"the admin port on localhost", "localhost", 81},
		{"the admin port on NPM's own address", r.npmIP, 81},
		{"the panel gateway", m6nPanelGateway, 8080},
		{"an address of the panel network", "10.99.77.2", 8080},
	} {
		_, err := m4Plan(r.ctx, r.f.api, r.f.agentID, "npm.proxyhost.add", npm.ProxyHostInput{
			Domains: []string{fmt.Sprintf("m6n-reserved-%d.example.test", i)}, ForwardScheme: "http", ForwardHost: c.host, ForwardPort: c.port,
		})
		m6nRefused(t, "npm.proxyhost.add to "+c.name, err, errcode.NPMReserved, "")
	}
	for _, h := range r.listProxyHosts(t) {
		t.Errorf("proxy host %v exists after the refused plans, want none", h.Domains)
	}
}

// expose exposes the Pebble image's HTTP listener and an echo server on one
// user-defined network through npm.expose.container: NPM joins the network, a
// certificate comes from Pebble through the real ACME flow, and the scanner
// reaches both targets over HTTPS with a chain that verifies under Pebble's root.
func (r *m6nRun) expose(t *testing.T) {
	v := r.f.agent
	m3dDocker(r.ctx, t, v, "network", "create", m6nAppNet)
	m3Run(r.ctx, t, v, m6nRunCmd(m6nRunSpec{
		Name: m6nAppName, Image: r.image(t, "pebble-challtestsrv"), Network: m6nAppNet,
		Args: []string{"-dnsserver", "", "-http01", "", "-https01", "", "-tlsalpn01", ""},
	}))
	m3Run(r.ctx, t, v, m6nRunCmd(m6nRunSpec{
		Name: m6nEchoName, Image: npm.Image, Network: m6nAppNet, Entrypoint: "node", Args: []string{"-e", m6nEchoScript},
	}))
	m3dWaitRunning(r.ctx, t, v, m6nAppName)
	m3dWaitRunning(r.ctx, t, v, m6nEchoName)
	for _, e := range []struct {
		container, host string
		port            int
	}{{m6nAppName, m6nAppHost, m6nAppPort}, {m6nEchoName, m6nEchoHost, m6nEchoPort}} {
		ch := m4ApplyAsync(r.ctx, t, r.f.api, "", "npm.expose.container", npm.ExposeInput{
			HostID: r.f.agentID, Container: e.container, Port: e.port, Network: m6nAppNet, Domains: []string{e.host},
		})
		var out npm.ExposeOutput
		if err := json.Unmarshal(ch.Output, &out); err != nil || out.ProxyHostID == 0 || out.CertificateID == 0 {
			t.Fatalf("output of npm.expose.container for %s: %s (%v), want a proxy host and a certificate id", e.host, ch.Output, err)
		}
	}
	if members := m6nNetworkMembers(r.ctx, t, v, m6nAppNet); !slices.Contains(members, m6nNPMName) {
		t.Errorf("network %s holds %v, want %s attached by the exposure", m6nAppNet, members, m6nNPMName)
	}
	for _, host := range []string{m6nAppHost, m6nEchoHost} {
		err := eventually(r.ctx, 2*time.Minute, func(ctx context.Context) error {
			resp, err := m6nGet(ctx, r.f.scanner, host, "/", nil)
			if err != nil {
				return err
			}
			if err := m6nCheckChain(resp.Chain, r.root, host, time.Now()); err != nil {
				return err
			}
			switch {
			case host == m6nAppHost && !m6nIsApp(resp):
				return fmt.Errorf("harness: GET / answered %d %.80q, want the target's own 404", resp.Status, resp.Body)
			case host == m6nEchoHost && resp.Status != http.StatusOK:
				return fmt.Errorf("harness: GET / answered %d %.80q, want 200", resp.Status, resp.Body)
			}
			return nil
		})
		if err != nil {
			t.Errorf("GET https://%s/ from the scanner: %v", host, err)
		}
	}
}

// headers: a request through the custom location arrives with exactly one Host
// header, and X-Darkpanel-Client arrives once with the scanner's address
// whether the request lacks it, forges it, or forges it twice.
func (r *m6nRun) headers(t *testing.T) {
	for _, c := range []struct {
		name   string
		header http.Header
	}{
		{"ordinary request", nil},
		{"forged client header", http.Header{"X-Darkpanel-Client": {m6nForged}}},
		{"client header forged twice", http.Header{"X-Darkpanel-Client": {m6nForged, "198.51.100.7"}}},
	} {
		resp, err := m6nGet(r.ctx, r.f.scanner, m6nEchoHost, "/", c.header)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		h, err := m6nRawHeaders(resp.Body)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if got := h.Values("Host"); !slices.Equal(got, []string{m6nEchoHost}) {
			t.Errorf("%s: the target received Host %q, want exactly %q", c.name, got, m6nEchoHost)
		}
		if got := h.Values("X-Darkpanel-Client"); !slices.Equal(got, []string{scannerIPv4}) {
			t.Errorf("%s: the target received X-Darkpanel-Client %q, want exactly %q", c.name, got, scannerIPv4)
		}
	}
}

// exposeRemove removes the first exposure by npm.expose.remove: its proxy host
// is gone and its host no longer reaches the target, while the other
// exposure still works and NPM stays on the shared network, because the claim
// of the other exposure remains.
func (r *m6nRun) exposeRemove(t *testing.T) {
	m4ApplyAsync(r.ctx, t, r.f.api, "", "npm.expose.remove", npm.ExposeRemoveInput{HostID: r.f.agentID, Domain: m6nAppHost})
	hosts := r.listProxyHosts(t)
	if m6nHasDomain(hosts, m6nAppHost) || !m6nHasDomain(hosts, m6nEchoHost) {
		t.Errorf("proxy hosts %+v after removing %s, want only the one for %s", hosts, m6nAppHost, m6nEchoHost)
	}
	err := eventually(r.ctx, 30*time.Second, func(ctx context.Context) error {
		return w10hxGone(m6nGet(ctx, r.f.scanner, m6nAppHost, "/", nil))
	})
	if err != nil {
		t.Errorf("GET https://%s/ after the removal: %v", m6nAppHost, err)
	}
	if members := m6nNetworkMembers(r.ctx, t, r.f.agent, m6nAppNet); !slices.Contains(members, m6nNPMName) {
		t.Errorf("network %s holds %v, want %s kept for the exposure that remains", m6nAppNet, members, m6nNPMName)
	}
	resp, err := m6nGet(r.ctx, r.f.scanner, m6nEchoHost, "/", nil)
	switch {
	case err != nil:
		t.Errorf("GET https://%s/ after the removal: %v", m6nEchoHost, err)
	case resp.Status != http.StatusOK:
		t.Errorf("GET https://%s/ after the removal answered %d, want 200", m6nEchoHost, resp.Status)
	}
}

// w10hxGone accepts the reply to a request for a removed exposure's host only
// when it arrived and is not the removed target's answer, or when NPM refused
// the handshake with the TLS unrecognized_name alert: with the proxy host
// deleted it has no server for that name. Any other failed request shows an
// outage, not the removal.
func w10hxGone(resp m6nResponse, err error) error {
	if err != nil {
		// crypto/tls reports a received alert as a "remote error" OpError
		// around its unexported alert type, so only the text identifies it.
		var opErr *net.OpError
		if errors.As(err, &opErr) && opErr.Op == "remote error" && opErr.Err != nil && opErr.Err.Error() == "tls: unrecognized name" {
			return nil
		}
		return fmt.Errorf("harness: the request failed, so the removal is not shown: %w", err)
	}
	if m6nIsApp(resp) {
		return errors.New("harness: the host still reaches the removed target")
	}
	return nil
}

// w10hxRecreated checks that the upgrade replaced the container: the upgrade
// stops, removes and recreates it, so the id changes.
func w10hxRecreated(before, after string) error {
	if before == "" || after == "" {
		return fmt.Errorf("harness: no container id to compare: %q before the upgrade, %q after", before, after)
	}
	if before == after {
		return fmt.Errorf("harness: the container id %s did not change over the upgrade", after)
	}
	return nil
}

// upgrade: npm.instance.upgrade to the pinned image, which the instance
// already runs, completes, recreates the container, leaves the instance ready
// and keeps the proxy host.
func (r *m6nRun) upgrade(t *testing.T) {
	before := r.listProxyHosts(t)
	var prev npm.InstanceStatus
	if err := r.f.api.Read(r.ctx, r.f.agentID, "npm.instance.status/v1", npm.StatusInput{}, &prev); err != nil {
		t.Fatal(err)
	}
	ch := m4ApplyAsync(r.ctx, t, r.f.api, r.f.agentID, "npm.instance.upgrade", npm.UpgradeInput{})
	var out npm.Upgraded
	if err := json.Unmarshal(ch.Output, &out); err != nil || out.ToImage != npm.Image {
		t.Errorf("output of npm.instance.upgrade: %s (%v), want ToImage %s", ch.Output, err, npm.Image)
	}
	var st npm.InstanceStatus
	if err := r.f.api.Read(r.ctx, r.f.agentID, "npm.instance.status/v1", npm.StatusInput{}, &st); err != nil {
		t.Fatal(err)
	}
	if !st.Running || !st.APIReady || st.Image != npm.Image {
		t.Errorf("npm.instance.status %+v after the upgrade, want a running, ready instance of %s", st, npm.Image)
	}
	if err := w10hxRecreated(prev.ContainerID, st.ContainerID); err != nil {
		t.Error(err)
	}
	after := r.listProxyHosts(t)
	if len(after) != len(before) || !m6nHasDomain(after, m6nEchoHost) {
		t.Errorf("proxy hosts %+v after the upgrade, want the %d from before", after, len(before))
	}
	err := eventually(r.ctx, 2*time.Minute, func(ctx context.Context) error {
		resp, err := m6nGet(ctx, r.f.scanner, m6nEchoHost, "/", nil)
		if err != nil {
			return err
		}
		if resp.Status != http.StatusOK {
			return fmt.Errorf("harness: GET / answered %d", resp.Status)
		}
		return nil
	})
	if err != nil {
		t.Errorf("GET https://%s/ after the upgrade: %v", m6nEchoHost, err)
	}
}

// exposeRemoveLast removes the last exposure, after the upgrade: NPM, now
// recreated, still manages its objects, and no claim is left, so the network
// is detached.
func (r *m6nRun) exposeRemoveLast(t *testing.T) {
	m4ApplyAsync(r.ctx, t, r.f.api, "", "npm.expose.remove", npm.ExposeRemoveInput{HostID: r.f.agentID, Domain: m6nEchoHost})
	if hosts := r.listProxyHosts(t); len(hosts) != 0 {
		t.Errorf("proxy hosts %+v after the last removal, want none", hosts)
	}
	if members := m6nNetworkMembers(r.ctx, t, r.f.agent, m6nAppNet); slices.Contains(members, m6nNPMName) {
		t.Errorf("network %s holds %v, want %s detached once no exposure claims it", m6nAppNet, members, m6nNPMName)
	}
}

// m6nDiagnosticTimeout bounds each command of logDiagnostics.
const m6nDiagnosticTimeout = 15 * time.Second

// w10hxDiagnosticContext returns the context of one diagnostic command. It
// keeps the values of parent and drops its cancellation, because the
// diagnostics are wanted most when the test context has already expired.
func w10hxDiagnosticContext(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(parent), m6nDiagnosticTimeout)
}

// logDiagnostics logs what m6nDiagnostics collects. A command that fails is
// logged with its error and never fails the test.
func (r *m6nRun) logDiagnostics(t *testing.T) {
	for _, d := range m6nDiagnostics() {
		ctx, cancel := w10hxDiagnosticContext(r.ctx)
		out, err := r.f.agent.Run(ctx, d.Command)
		cancel()
		out = m5dCapDiagnostic(out, strings.Contains(d.Command, "logs"))
		if err != nil {
			t.Logf("diagnostic %s\n$ %s\n%s\nerror: %v", d.Label, d.Command, out, err)
			continue
		}
		t.Logf("diagnostic %s\n$ %s\n%s", d.Label, d.Command, out)
	}
}
