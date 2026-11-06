package derp

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/netip"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/derp/artifact"
	"github.com/darkraise/darkpanel/internal/modules/derp/server"
	"github.com/darkraise/darkpanel/internal/platform/tslocal"
)

var errSsBoom = errors.New("ss: boom")

const ssIP = "203.0.113.9"

var (
	ssStoredAfter = time.Date(2027, 1, 2, 3, 4, 5, 0, time.UTC)
	ssLiveAfter   = time.Date(2027, 3, 4, 5, 6, 7, 0, time.UTC)
	ssStoredPin   = server.PinPrefix + strings.Repeat("b", 64)
)

// ssCert returns a self-signed certificate for ip as PEM, and its pin computed here from the DER.
func ssCert(t *testing.T, ip string, notAfter time.Time) (certPEM []byte, pin string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: ip},
		NotBefore:    notAfter.Add(-365 * 24 * time.Hour),
		NotAfter:     notAfter,
		IPAddresses:  []net.IP{net.ParseIP(ip)},
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(der)
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), "sha256-raw:" + hex.EncodeToString(sum[:])
}

// ssMaps makes the host maps a test writes into non-nil.
func ssMaps(h *dpHost) {
	if h.files == nil {
		h.files = map[string][]byte{}
	}
	if h.units.States == nil {
		h.units.States = map[string]core.UnitState{}
	}
}

// ssRow is the agent's server row for a server installed in mode at host name.
func ssRow(mode, hostName string) localServer {
	at := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	return localServer{
		ChangeID: "c0", Mode: mode, HostName: hostName, VerifyClients: true, DerperVersion: artifact.DerperVersion,
		BinarySHA256: strings.Repeat("a", 64), InstalledAt: at, UpdatedAt: at,
	}
}

func ssInstall(t *testing.T, h *dpHost, s localServer) {
	t.Helper()
	if err := insertLocalServer(context.Background(), h.db, s); err != nil {
		t.Fatal(err)
	}
}

// ssTailnet makes tailscaled report backend, a self with tags and the addresses ips, in the order given.
func ssTailnet(h *dpHost, backend string, tags []string, ips ...string) {
	addrs := make([]netip.Addr, 0, len(ips))
	for _, ip := range ips {
		addrs = append(addrs, netip.MustParseAddr(ip))
	}
	h.local.Status = &tslocal.Status{
		BackendState: backend, TailscaleIPs: addrs,
		Self: &tslocal.PeerStatus{ID: "n1", HostName: "derp-1", DNSName: "derp-1.example.ts.net.", Tags: tags, TailscaleIPs: addrs, Online: true},
	}
}

// ssRead runs derp.server.status on d and decodes the answer twice: as a ServerStatus and as a generic map, which
// shows whether an empty list was encoded as [] and an absent time as null.
func ssRead(t *testing.T, d dp, env core.Env, input string) (ServerStatus, map[string]any, error) {
	t.Helper()
	raw, err := d.serverStatus().Handler.Read(context.Background(), env, json.RawMessage(input))
	if err != nil {
		return ServerStatus{}, nil, err
	}
	var st ServerStatus
	if err := json.Unmarshal(raw, &st); err != nil {
		t.Fatalf("the answer %s does not decode as a ServerStatus: %v", raw, err)
	}
	var generic map[string]any
	if err := json.Unmarshal(raw, &generic); err != nil {
		t.Fatal(err)
	}
	return st, generic, nil
}

func TestSsDefinition(t *testing.T) {
	def := newDPHost(t).dp().serverStatus()
	s := def.Spec
	if s.Name != "derp.server.status" || s.Version != 1 || s.Class != core.ClassRead || s.Side != core.SideAgent ||
		len(s.LockKeys) != 0 || s.Available != nil || s.Internal || s.LongRunning || s.Task {
		t.Errorf("spec %+v, want a plain agent read derp.server.status/v1 without locks or an availability check", s)
	}
	if def.InputType != reflect.TypeFor[ServerStatusInput]() || def.OutputType != reflect.TypeFor[ServerStatus]() {
		t.Errorf("input %v and output %v, want ServerStatusInput and ServerStatus", def.InputType, def.OutputType)
	}
	if def.Handler.Read == nil {
		t.Error("the action has no Read handler")
	}
}

// TestSsNotInstalled: a host without a server still reports its tailnet identity; every list is an array, an
// absent certificate time is null, and the read runs no command and calls no unit method.
func TestSsNotInstalled(t *testing.T) {
	h := newDPHost(t)
	ssTailnet(h, "Running", []string{"tag:z", "tag:a"}, "fd7a:115c:a1e0::1", "100.64.0.10")
	got, generic, err := ssRead(t, h.dp(), h.env(""), `{}`)
	if err != nil {
		t.Fatal(err)
	}
	want := ServerStatus{
		AvailableVersion: artifact.DerperVersion, OnTailnet: true,
		Tags: []string{"tag:a", "tag:z"}, Addresses: []string{"100.64.0.10", "fd7a:115c:a1e0::1"}, Visible: []Visibility{},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("status\n got %+v\nwant %+v", got, want)
	}
	if v, ok := generic["certNotAfter"]; !ok || v != nil {
		t.Errorf("certNotAfter = %v (present %v), want null", v, ok)
	}
	if !reflect.DeepEqual(generic["visible"], []any{}) {
		t.Errorf("visible = %#v, want []", generic["visible"])
	}
	if len(h.ran) != 0 || len(h.units.Calls) != 0 {
		t.Errorf("the read ran %v and called %v, want nothing", h.ran, h.units.Calls)
	}
}

// TestSsInstalledIPMode: the certificate is read from derper's file.
func TestSsInstalledIPMode(t *testing.T) {
	livePEM, livePin := ssCert(t, ssIP, ssLiveAfter)
	crt := server.CertDir + "/" + ssIP + ".crt"
	for _, tc := range []struct {
		name      string
		file      []byte
		wantPin   string
		wantAfter time.Time
	}{
		{"the live certificate wins over the stored one", livePEM, livePin, ssLiveAfter},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newDPHost(t)
			ssMaps(h)
			row := ssRow(server.ModeIP, ssIP)
			row.CertName, row.CertNotAfter = ssStoredPin, ssStoredAfter
			ssInstall(t, h, row)
			h.units.States[server.UnitName] = core.UnitState{LoadState: "loaded", ActiveState: "active", SubState: "running", MainPID: 700}
			if tc.file != nil {
				h.files[crt] = tc.file
			}
			got, _, err := ssRead(t, h.dp(), h.env(""), `{}`)
			if err != nil {
				t.Fatal(err)
			}
			if !got.Installed || got.Mode != server.ModeIP || got.HostName != ssIP || !got.VerifyClients ||
				got.DerperVersion != artifact.DerperVersion || !got.UnitActive || got.AvailableVersion != artifact.DerperVersion {
				t.Errorf("status %+v, want an installed, active, verifying ip-mode server %s at %s", got, artifact.DerperVersion, ssIP)
			}
			if got.CertName != tc.wantPin || got.CertNotAfter == nil || !got.CertNotAfter.Equal(tc.wantAfter) {
				t.Errorf("certificate %q until %v, want %q until %v", got.CertName, got.CertNotAfter, tc.wantPin, tc.wantAfter)
			}
		})
	}
}

// TestSsLetsEncrypt: a Let's Encrypt server has no pin and no certificate time, and its certificate file, which
// derper keeps in its own cache, is not read; the unit state and the client verification flag come through.
func TestSsLetsEncrypt(t *testing.T) {
	livePEM, _ := ssCert(t, ssIP, ssLiveAfter)
	for _, tc := range []struct {
		name       string
		state      core.UnitState
		wantActive bool
	}{
		{"active", core.UnitState{LoadState: "loaded", ActiveState: "active", SubState: "running"}, true},
		{"inactive", core.UnitState{LoadState: "loaded", ActiveState: "inactive", SubState: "dead"}, false},
		{"failed", core.UnitState{LoadState: "loaded", ActiveState: "failed", SubState: "failed"}, false},
		{"activating", core.UnitState{LoadState: "loaded", ActiveState: "activating", SubState: "start"}, false},
		{"not found", core.UnitState{LoadState: "not-found"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newDPHost(t)
			ssMaps(h)
			row := ssRow(server.ModeLetsEncrypt, "derp.example.com")
			row.VerifyClients = false
			ssInstall(t, h, row)
			h.units.States[server.UnitName] = tc.state
			h.files[server.CertDir+"/derp.example.com.crt"] = livePEM
			got, _, err := ssRead(t, h.dp(), h.env(""), `{}`)
			if err != nil {
				t.Fatal(err)
			}
			if !got.Installed || got.Mode != server.ModeLetsEncrypt || got.HostName != "derp.example.com" || got.VerifyClients {
				t.Errorf("status %+v, want an installed Let's Encrypt server without client verification", got)
			}
			if got.UnitActive != tc.wantActive {
				t.Errorf("unitActive = %v, want %v for unit state %+v", got.UnitActive, tc.wantActive, tc.state)
			}
			if got.CertName != "" || got.CertNotAfter != nil {
				t.Errorf("certificate %q until %v, want none for a Let's Encrypt server", got.CertName, got.CertNotAfter)
			}
		})
	}
}

// TestSsOffTailnet: a node that is not Running, or tailscaled not running, is no error; the node's identity is
// withheld.
func TestSsOffTailnet(t *testing.T) {
	for _, tc := range []struct {
		name      string
		backend   string
		statusErr error
	}{
		{"logged out", "NeedsLogin", nil},
		{"stopped", "Stopped", nil},
		{"tailscaled not running", "Running", fmt.Errorf("%w: no such socket", tslocal.ErrUnavailable)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newDPHost(t)
			ssTailnet(h, tc.backend, []string{"tag:derp"}, "100.64.0.10")
			if tc.statusErr != nil {
				h.local.Errs = map[string]error{"Status": tc.statusErr}
			}
			got, generic, err := ssRead(t, h.dp(), h.env(""), `{}`)
			if err != nil {
				t.Fatal(err)
			}
			if got.OnTailnet || len(got.Tags) != 0 || len(got.Addresses) != 0 {
				t.Errorf("status %+v, want OnTailnet false and no tags or addresses", got)
			}
			if !reflect.DeepEqual(generic["tags"], []any{}) || !reflect.DeepEqual(generic["addresses"], []any{}) {
				t.Errorf("tags %#v and addresses %#v, want [] and []", generic["tags"], generic["addresses"])
			}
		})
	}
}

func TestSsLinkViaTailnet(t *testing.T) {
	h := newDPHost(t)
	got, _, err := ssRead(t, h.dp(), h.env(""), `{}`)
	if err != nil || got.LinkViaTailnet {
		t.Fatalf("link over the tailnet = %v, err %v, want false", got.LinkViaTailnet, err)
	}
	h.link = true
	if got, _, err = ssRead(t, h.dp(), h.env(""), `{}`); err != nil || !got.LinkViaTailnet {
		t.Fatalf("link over the tailnet = %v, err %v, want true", got.LinkViaTailnet, err)
	}
	d := h.dp()
	d.d.linkViaTailnet = func() (bool, error) { return false, errSsBoom }
	if _, _, err = ssRead(t, d, h.env(""), `{}`); !errors.Is(err, errSsBoom) {
		t.Errorf("err = %v, want the failure to read the agent credentials, not an answer of false", err)
	}
}

func TestSsWhois(t *testing.T) {
	h := newDPHost(t)
	h.local.WhoIs = map[netip.Addr]*tslocal.WhoIs{
		netip.MustParseAddr("100.64.0.20"):       {Node: &tslocal.WhoIsNode{Name: "web-1.example.ts.net."}},
		netip.MustParseAddr("fd7a:115c:a1e0::5"): {Node: &tslocal.WhoIsNode{Name: "db.example.ts.net."}},
	}
	got, generic, err := ssRead(t, h.dp(), h.env(""), `{"whois":["100.64.0.20","100.64.0.21","fd7a:115c:a1e0::5"]}`)
	if err != nil {
		t.Fatal(err)
	}
	want := []Visibility{
		{Address: "100.64.0.20", Visible: true, Node: "web-1.example.ts.net"},
		{Address: "100.64.0.21"},
		{Address: "fd7a:115c:a1e0::5", Visible: true, Node: "db.example.ts.net"},
	}
	if !reflect.DeepEqual(got.Visible, want) {
		t.Errorf("visible\n got %+v\nwant %+v", got.Visible, want)
	}
	if _, ok := generic["visible"].([]any); !ok {
		t.Errorf("visible = %#v, want an array", generic["visible"])
	}
}

func TestSsWhoisRefusals(t *testing.T) {
	entries := make([]string, 257)
	for i := range entries {
		entries[i] = "100.64.0.1"
	}
	tooMany, err := json.Marshal(ServerStatusInput{Whois: entries})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, input, path string
	}{
		{"an entry that is not an address", `{"whois":["100.64.0.20","not-an-ip"]}`, "/whois/1"},
		{"a host name instead of an address", `{"whois":["web-1.example.ts.net"]}`, "/whois/0"},
		{"more than 256 entries", string(tooMany), "/whois"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newDPHost(t)
			_, _, err := ssRead(t, h.dp(), h.env(""), tc.input)
			coded := errcode.From(err)
			if err == nil || coded.Code != errcode.InvalidInput || len(coded.Fields) == 0 || coded.Fields[0].Path != tc.path {
				t.Errorf("err = %v (fields %+v), want request.invalid_input with a field error at %s", err, coded.Fields, tc.path)
			}
		})
	}
	t.Run("tailscaled failing is not read as not visible", func(t *testing.T) {
		h := newDPHost(t)
		h.local.Errs = map[string]error{"WhoIs": errSsBoom}
		_, _, err := ssRead(t, h.dp(), h.env(""), `{"whois":["100.64.0.20"]}`)
		if !errors.Is(err, errSsBoom) || errcode.From(err).Code != errcode.TailscaleUnavailable {
			t.Errorf("err = %v, want tailscale.unavailable wrapping the failure", err)
		}
	})
}

func TestSsReadStatusOverAgents(t *testing.T) {
	ctx := context.Background()
	h := newDPHost(t)
	h.addHost("h2", "web-2", false)
	want := ServerStatus{
		Installed: true, Mode: server.ModeIP, HostName: ssIP, VerifyClients: true, DerperVersion: artifact.DerperVersion,
		UnitActive: true, CertName: ssStoredPin, CertNotAfter: &ssStoredAfter, AvailableVersion: artifact.DerperVersion,
		OnTailnet: true, Tags: []string{"tag:derp"}, Addresses: []string{"100.64.0.10"},
		Visible: []Visibility{{Address: "100.64.0.9", Visible: true, Node: "web-1.example.ts.net"}},
	}
	ag := h.agent("h2")
	ag.Reads = map[string]func(json.RawMessage) (any, error){
		"derp.server.status/v1": func(json.RawMessage) (any, error) { return want, nil },
	}
	got, err := readStatus(ctx, h.ctlEnv(""), "h2", []string{"100.64.0.9"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("status\n got %+v\nwant %+v", got, want)
	}
	if _, err := readStatus(ctx, h.ctlEnv(""), "h2", nil); err != nil {
		t.Fatal(err)
	}
	wantCalls := []string{`derp.server.status/v1 {"whois":["100.64.0.9"]}`, `derp.server.status/v1 {}`}
	if !reflect.DeepEqual(ag.Calls, wantCalls) {
		t.Errorf("agent calls %q, want %q", ag.Calls, wantCalls)
	}

	ag.Reads["derp.server.status/v1"] = func(json.RawMessage) (any, error) { return nil, errSsBoom }
	if _, err := readStatus(ctx, h.ctlEnv(""), "h2", nil); !errors.Is(err, errSsBoom) || !strings.Contains(err.Error(), "h2") {
		t.Errorf("err = %v, want the agent's failure, naming host h2", err)
	}
	ag.Reads["derp.server.status/v1"] = func(json.RawMessage) (any, error) { return "oops", nil }
	if _, err := readStatus(ctx, h.ctlEnv(""), "h2", nil); err == nil || !strings.Contains(err.Error(), "h2") {
		t.Errorf("err = %v, want a decode failure naming host h2", err)
	}
}

// w10spLocal wraps the fake local API and records the deadline of the context of every call, in call order.
type w10spLocal struct {
	localAPI
	mu        sync.Mutex
	deadlines []time.Time
}

func (l *w10spLocal) note(ctx context.Context) {
	dl, _ := ctx.Deadline()
	l.mu.Lock()
	l.deadlines = append(l.deadlines, dl)
	l.mu.Unlock()
	time.Sleep(2 * time.Millisecond)
}

func (l *w10spLocal) Status(ctx context.Context) (*tslocal.Status, error) {
	l.note(ctx)
	return l.localAPI.Status(ctx)
}

func (l *w10spLocal) WhoIs(ctx context.Context, addr netip.Addr) (*tslocal.WhoIs, error) {
	l.note(ctx)
	return l.localAPI.WhoIs(ctx, addr)
}

// TestW10SpStatusGivesEachLocalCallItsOwnDeadline: the Status call and each WhoIs call run under a deadline of
// their own, so a slow answer does not use up the time of the lookups after it.
func TestW10SpStatusGivesEachLocalCallItsOwnDeadline(t *testing.T) {
	h := newDPHost(t)
	ssTailnet(h, "Running", []string{"tag:derp"}, "100.64.0.10")
	rec := &w10spLocal{localAPI: thLocal{h.local}}
	d := h.dp()
	d.d.local = func() localAPI { return rec }
	if _, _, err := ssRead(t, d, h.env(""), `{"whois":["100.64.0.20","100.64.0.21","100.64.0.22"]}`); err != nil {
		t.Fatal(err)
	}
	if len(rec.deadlines) != 4 {
		t.Fatalf("%d local API calls, want one Status and three WhoIs", len(rec.deadlines))
	}
	for i, dl := range rec.deadlines {
		if dl.IsZero() {
			t.Errorf("call %d ran without a deadline", i)
		}
		if i > 0 && dl.Sub(rec.deadlines[i-1]) < time.Millisecond {
			t.Errorf("call %d shares the deadline of call %d (%v and %v), want a deadline of its own", i, i-1, dl, rec.deadlines[i-1])
		}
	}
}

// TestW10SpStatusRefusesAnIPServerWhoseCertificateCannotBeRead: the stored pin is not shown as the current
// certificate when derper's file is absent or is not a certificate.
func TestW10SpStatusRefusesAnIPServerWhoseCertificateCannotBeRead(t *testing.T) {
	for _, tc := range []struct {
		name string
		file []byte
	}{
		{"no certificate file", nil},
		{"a file that is not a certificate", []byte("not a certificate")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newDPHost(t)
			ssMaps(h)
			row := ssRow(server.ModeIP, ssIP)
			row.CertName, row.CertNotAfter = ssStoredPin, ssStoredAfter
			ssInstall(t, h, row)
			h.units.States[server.UnitName] = core.UnitState{LoadState: "loaded", ActiveState: "active", SubState: "running", MainPID: 700}
			if tc.file != nil {
				h.files[server.CertDir+"/"+ssIP+".crt"] = tc.file
			}
			_, _, err := ssRead(t, h.dp(), h.env(""), `{}`)
			coded := errcode.From(err)
			wantDetail := "derper's certificate for " + ssIP + " cannot be read"
			if err == nil || coded.Code != errcode.DERPNotReady || coded.Detail != wantDetail {
				t.Errorf("err = %v, want derp.not_ready saying %q", err, wantDetail)
			}
		})
	}
}

// TestW10SpStatusRefusesASilentTailscaled: a tailscaled that is running but fails or does not answer is an error,
// not a node that is off the tailnet; only a socket that cannot be reached at all is no error.
func TestW10SpStatusRefusesASilentTailscaled(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"the call times out", context.DeadlineExceeded},
		{"tailscaled answers with an error", &tslocal.APIError{StatusCode: 500, Message: "boom"}},
		{"the answer cannot be read", errSsBoom},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newDPHost(t)
			ssTailnet(h, "Running", []string{"tag:derp"}, "100.64.0.10")
			h.local.Errs = map[string]error{"Status": tc.err}
			_, _, err := ssRead(t, h.dp(), h.env(""), `{}`)
			if !errors.Is(err, tc.err) || errcode.From(err).Code != errcode.TailscaleUnavailable {
				t.Errorf("err = %v, want tailscale.unavailable wrapping %v", err, tc.err)
			}
		})
	}
}
