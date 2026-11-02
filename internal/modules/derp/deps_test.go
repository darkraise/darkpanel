package derp

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/agent/daemon"
	"github.com/darkraise/darkpanel/internal/buildinfo"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/derp/server"
	"github.com/darkraise/darkpanel/internal/platform/tslocal"
	"github.com/darkraise/darkpanel/internal/transport"
)

func TestSdProductionDP(t *testing.T) {
	d := productionDP().d
	for name, ok := range map[string]bool{
		"local": d.local != nil, "fetch": d.fetch != nil, "readFile": d.readFile != nil, "lookupUser": d.lookupUser != nil,
		"sockets": d.sockets != nil, "linkViaTailnet": d.linkViaTailnet != nil, "tlsProbe": d.tlsProbe != nil, "stun": d.stun != nil,
		"resolve": d.resolve != nil, "policy": d.policy != nil, "devices": d.devices != nil, "connected": d.connected != nil,
	} {
		if !ok {
			t.Errorf("productionDP leaves deps.%s unset", name)
		}
	}
	if d.arch != runtime.GOARCH || d.version != buildinfo.Version {
		t.Errorf("arch %q and version %q, want %q and %q", d.arch, d.version, runtime.GOARCH, buildinfo.Version)
	}
	_, err := d.local().Status(context.Background())
	if !errors.Is(err, tslocal.ErrUnavailable) || !strings.Contains(err.Error(), "/nonexistent/darkpanel/") {
		t.Errorf("the local API of a test binary answered %v; it must dial a socket that never exists, never the host's tailscaled", err)
	}
}

func TestSdModuleCarriesProductionDeps(t *testing.T) {
	m, ok := Module().(module)
	if !ok {
		t.Fatalf("Module() is %T, want module", Module())
	}
	if m.d.d.local == nil || m.d.d.fetch == nil {
		t.Error("Module() has no production deps")
	}
}

func TestSdLookupUser(t *testing.T) {
	d := productionDP().d
	uid, gid, ok, err := d.lookupUser("root")
	if err != nil || !ok || uid != 0 || gid != 0 {
		t.Errorf("lookupUser(root) = %d, %d, %v, %v", uid, gid, ok, err)
	}
	if _, _, ok, err := d.lookupUser("darkpanel-no-such-user"); err != nil || ok {
		t.Errorf("lookupUser of an unknown user = ok %v, err %v; want ok false and no error", ok, err)
	}
}

func TestSdOwnedPaths(t *testing.T) {
	want := []core.OwnedPath{
		{Path: server.ServiceUser, Kind: core.OwnedUser},
		{Path: server.BinaryPath, Kind: core.OwnedFile},
		{Path: server.BinaryNewPath, Kind: core.OwnedFile},
		{Path: server.BinaryPrevPath, Kind: core.OwnedFile},
		{Path: server.UnitPath, Kind: core.OwnedUnit},
		{Path: server.StateDir, Kind: core.OwnedServiceDir, User: server.ServiceUser, Subdirs: []string{server.CertSubdir}},
	}
	if got := ownedPaths(); !reflect.DeepEqual(got, want) {
		t.Errorf("ownedPaths = %+v\nwant %+v", got, want)
	}
}

func sdSQLNames(t *testing.T, fsys fs.FS) []string {
	t.Helper()
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func TestSdMigrationSets(t *testing.T) {
	if got := sdSQLNames(t, agentMigrations()); !reflect.DeepEqual(got, []string{"0001_derp.sql"}) {
		t.Errorf("agent migrations %v, want [0001_derp.sql]", got)
	}
	if got := sdSQLNames(t, controllerMigrations()); !reflect.DeepEqual(got, []string{"0001_derp.sql"}) {
		t.Errorf("controller migrations %v, want [0001_derp.sql]", got)
	}
}

func TestSdAvailable(t *testing.T) {
	tests := []struct {
		name   string
		set    func(h *dpHost, env *core.Env)
		code   errcode.Code
		detail string
	}{
		{"on the tailnet", func(*dpHost, *core.Env) {}, "", ""},
		{"no clock in the environment", func(_ *dpHost, env *core.Env) { env.Clock = nil }, "", ""},
		{"tailscaled does not answer", func(h *dpHost, _ *core.Env) {
			h.local.Errs = map[string]error{"Status": fmt.Errorf("dial: %w", tslocal.ErrUnavailable)}
		}, errcode.TailscaleUnavailable, "tailscaled does not answer on its socket"},
		{"logged out", func(h *dpHost, _ *core.Env) { h.local.Status.BackendState = "NeedsLogin" }, errcode.DERPNotReady,
			"the host is not on the tailnet (tailscaled is NeedsLogin)"},
		{"stopped", func(h *dpHost, _ *core.Env) { h.local.Status.BackendState = "Stopped" }, errcode.DERPNotReady,
			"the host is not on the tailnet (tailscaled is Stopped)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newDPHost(t)
			env := h.env("")
			tt.set(h, &env)
			err := h.dp().available(context.Background(), env)
			if tt.code == "" {
				if err != nil {
					t.Fatalf("available = %v, want nil", err)
				}
				if env.Clock != nil && h.clock.Pending() != 0 {
					t.Errorf("available left %d timers armed", h.clock.Pending())
				}
				return
			}
			var ce *errcode.Error
			if !errors.As(err, &ce) || ce.Code != tt.code || ce.Detail != tt.detail {
				t.Errorf("available = %#v, want %s with %q", err, tt.code, tt.detail)
			}
		})
	}
}

// sdBlockedLocal never answers until its context ends.
type sdBlockedLocal struct{}

func (sdBlockedLocal) Status(ctx context.Context) (*tslocal.Status, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (sdBlockedLocal) WhoIs(context.Context, netip.Addr) (*tslocal.WhoIs, error) {
	return nil, errors.New("unused")
}

func (sdBlockedLocal) DebugDERPRegion(context.Context, string) (*tslocal.DERPRegionReport, error) {
	return nil, errors.New("unused")
}

func TestSdAvailableTimesOut(t *testing.T) {
	h := newDPHost(t)
	d := h.dp()
	d.d.local = func() localAPI { return sdBlockedLocal{} }
	done := make(chan error, 1)
	go func() { done <- d.available(context.Background(), h.env("")) }()
	for deadline := time.Now().Add(10 * time.Second); h.clock.Pending() == 0; {
		if time.Now().After(deadline) {
			t.Fatal("available never armed its timeout")
		}
		time.Sleep(time.Millisecond)
	}
	h.clock.Advance(availableWait)
	select {
	case err := <-done:
		var ce *errcode.Error
		if !errors.As(err, &ce) || ce.Code != errcode.TailscaleUnavailable || ce.Detail != "tailscaled does not answer on its socket" {
			t.Errorf("available = %#v, want tailscale.unavailable", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("available did not end after availableWait")
	}
}

// sdCredentials saves an enrolled agent's credentials for a controller at address in dir.
func sdCredentials(t *testing.T, dir, address string) {
	t.Helper()
	caPub, caKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test agent root"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, caPub, caKey)
	if err != nil {
		t.Fatal(err)
	}
	ca, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	uri, err := url.Parse(transport.HostURI("h1"))
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(12 * time.Hour),
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, URIs: []*url.URL{uri}, KeyUsage: x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, pub, caKey)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	c := &daemon.Credentials{Key: key, Cert: cert, CA: ca, Controller: daemon.Endpoint{Address: address, SAN: transport.ControllerURI("C1")}}
	if _, err := daemon.SaveGeneration(dir, c, daemon.AnyCurrent); err != nil {
		t.Fatal(err)
	}
}

func TestSdLinkViaTailnet(t *testing.T) {
	tests := []struct {
		name, address string // "" leaves the agent not enrolled
		want          bool
	}{
		{"not enrolled", "", false},
		{"controller over an IPv4 tailnet address", "100.64.0.9:7443", true},
		{"controller over an IPv6 tailnet address", "[fd7a:115c:a1e0::9]:7443", true},
		{"controller over a LAN address", "192.0.2.5:7443", false},
		{"controller by name", "controller.example.com:7443", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "tls")
			if tt.address != "" {
				sdCredentials(t, dir, tt.address)
			}
			got, err := linkViaTailnetIn(dir)
			if err != nil || got != tt.want {
				t.Errorf("linkViaTailnetIn = %v, %v; want %v", got, err, tt.want)
			}
		})
	}
}

// sdCert makes a self-signed certificate for 127.0.0.1 valid between notBefore and notAfter, and its DER.
func sdCert(t *testing.T, notBefore, notAfter time.Time) (tls.Certificate, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(7), Subject: pkix.Name{CommonName: "derp test"}, NotBefore: notBefore, NotAfter: notAfter,
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, der
}

// sdHits records the requests a TLS test server received.
type sdHits struct {
	mu    sync.Mutex
	paths []string
}

func (h *sdHits) seen() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.paths...)
}

// handler answers GET /derp/probe with status, or a redirect to /moved when status is 302.
func (h *sdHits) handler(status int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.mu.Lock()
		h.paths = append(h.paths, r.Method+" "+r.URL.Path)
		h.mu.Unlock()
		switch {
		case r.URL.Path == "/derp/probe" && status == http.StatusFound:
			http.Redirect(w, r, "/moved", http.StatusFound)
		case r.URL.Path == "/derp/probe":
			w.WriteHeader(status)
		default:
			w.WriteHeader(http.StatusOK)
		}
	})
}

// sdTLSServer serves handler over TLS with cert on 127.0.0.1 and returns its port.
func sdTLSServer(t *testing.T, cert tls.Certificate, handler http.Handler) int {
	t.Helper()
	srv := httptest.NewUnstartedServer(handler)
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
	srv.Config.ErrorLog = log.New(io.Discard, "", 0)
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv.Listener.Addr().(*net.TCPAddr).Port
}

func TestSdProbeTLS(t *testing.T) {
	now := time.Now()
	cert, der := sdCert(t, now.Add(-time.Hour), now.Add(time.Hour))
	hits := &sdHits{}
	port := sdTLSServer(t, cert, hits.handler(http.StatusOK))
	pin := server.Pin(der)
	tests := []struct {
		name    string
		target  probeTarget
		wantErr bool
	}{
		{"the pinned leaf, reached by its IP literal", probeTarget{HostName: "127.0.0.1", Port: port, CertName: pin}, false},
		{"the pinned leaf, reached by address under another name", probeTarget{HostName: "derp.example.test", Address: "127.0.0.1", Port: port, CertName: pin}, false},
		{"another pin", probeTarget{HostName: "127.0.0.1", Port: port, CertName: server.Pin([]byte("another certificate"))}, true},
		{"no pin: the chain is verified and a self-signed leaf fails", probeTarget{HostName: "127.0.0.1", Port: port}, true},
	}
	succeeded := 0
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, err := probeTLS(context.Background(), tt.target)
			if (err != nil) != tt.wantErr {
				t.Fatalf("probeTLS = %v, %v; wantErr %v", d, err, tt.wantErr)
			}
			if err == nil {
				succeeded++
				if d <= 0 {
					t.Errorf("latency %v, want a positive duration", d)
				}
			}
		})
	}
	if got := hits.seen(); len(got) != succeeded || (len(got) > 0 && got[0] != "GET /derp/probe") {
		t.Errorf("the server saw %v; only the accepted probes may reach it, as GET /derp/probe", got)
	}
}

func TestSdProbeTLSChecksTheCertificateDates(t *testing.T) {
	now := time.Now()
	for _, tt := range []struct {
		name                string
		notBefore, notAfter time.Time
		wantErr             bool
	}{
		{"valid", now.Add(-time.Hour), now.Add(time.Hour), false},
		{"expired", now.Add(-48 * time.Hour), now.Add(-time.Hour), true},
		{"not yet valid", now.Add(time.Hour), now.Add(48 * time.Hour), true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cert, der := sdCert(t, tt.notBefore, tt.notAfter)
			port := sdTLSServer(t, cert, (&sdHits{}).handler(http.StatusOK))
			_, err := probeTLS(context.Background(), probeTarget{HostName: "127.0.0.1", Port: port, CertName: server.Pin(der)})
			if (err != nil) != tt.wantErr {
				t.Errorf("probeTLS = %v, wantErr %v: a pinned leaf is accepted only inside its validity", err, tt.wantErr)
			}
		})
	}
}

func TestSdProbeTLSNeedsA200AndFollowsNoRedirect(t *testing.T) {
	now := time.Now()
	cert, der := sdCert(t, now.Add(-time.Hour), now.Add(time.Hour))
	for _, status := range []int{http.StatusNoContent, http.StatusServiceUnavailable, http.StatusNotFound, http.StatusFound} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			hits := &sdHits{}
			port := sdTLSServer(t, cert, hits.handler(status))
			if _, err := probeTLS(context.Background(), probeTarget{HostName: "127.0.0.1", Port: port, CertName: server.Pin(der)}); err == nil {
				t.Errorf("probeTLS accepted an answer of %d", status)
			}
			for _, p := range hits.seen() {
				if p == "GET /moved" {
					t.Error("probeTLS followed a redirect")
				}
			}
		})
	}
}

func TestSdProbeTLSPinsOnlyTheLeaf(t *testing.T) {
	now := time.Now()
	cert, der := sdCert(t, now.Add(-time.Hour), now.Add(time.Hour))
	_, metaDER := sdCert(t, now.Add(-time.Hour), now.Add(time.Hour))
	cert.Certificate = append(cert.Certificate, metaDER) // derper serves its meta certificate after the leaf
	port := sdTLSServer(t, cert, (&sdHits{}).handler(http.StatusOK))
	if _, err := probeTLS(context.Background(), probeTarget{HostName: "127.0.0.1", Port: port, CertName: server.Pin(der)}); err != nil {
		t.Errorf("probeTLS = %v; a pinned leaf followed by another certificate must be accepted", err)
	}
	if _, err := probeTLS(context.Background(), probeTarget{HostName: "127.0.0.1", Port: port, CertName: server.Pin(metaDER)}); err == nil {
		t.Error("probeTLS accepted the pin of a certificate that is not the leaf")
	}
}

func TestSdProbeTLSEndsWithItsContext(t *testing.T) {
	now := time.Now()
	cert, der := sdCert(t, now.Add(-time.Hour), now.Add(time.Hour))
	port := sdTLSServer(t, cert, (&sdHits{}).handler(http.StatusOK))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := probeTLS(ctx, probeTarget{HostName: "127.0.0.1", Port: port, CertName: server.Pin(der)}); err == nil {
		t.Error("probeTLS answered under a cancelled context")
	}
}

// TestSdProbeTLSNeverResolvesAName: probeTLS connects only to an IP literal, the checked Address or an IP HostName;
// a name without an address is refused before any lookup, so no second resolution can reach another address.
func TestSdProbeTLSNeverResolvesAName(t *testing.T) {
	now := time.Now()
	cert, der := sdCert(t, now.Add(-time.Hour), now.Add(time.Hour))
	hits := &sdHits{}
	port := sdTLSServer(t, cert, hits.handler(http.StatusOK))
	if _, err := probeTLS(context.Background(), probeTarget{HostName: "localhost", Port: port, CertName: server.Pin(der)}); err == nil {
		t.Error("probeTLS reached a name it resolved itself")
	}
	if got := hits.seen(); len(got) != 0 {
		t.Errorf("the server saw %v, want nothing", got)
	}
}
