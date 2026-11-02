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
	"math/big"
	"net"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/derp/server"
)

const (
	crIP       = "203.0.113.9"
	crResource = "derp.cert:" + crIP
)

var (
	crCrt      = server.CertDir + "/" + crIP + ".crt"
	crKey      = server.CertDir + "/" + crIP + ".key"
	crOldAfter = time.Date(2027, 1, 2, 3, 4, 5, 0, time.UTC)
	crNewAfter = time.Date(2027, 3, 4, 5, 6, 7, 0, time.UTC)
)

// crCert returns a self-signed certificate for ip as PEM, and its pin computed here from the DER.
func crCert(t *testing.T, ip string, notAfter time.Time) (certPEM []byte, pin string) {
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

// crHost is a host with an installed ip-mode server and the certificate derper made when it first started.
type crHost struct {
	*dpHost
	oldPEM []byte
	oldPin string
	newPEM []byte
	newPin string
}

func crInstalled(t *testing.T) *crHost {
	t.Helper()
	h := &crHost{dpHost: newDPHost(t)}
	h.oldPEM, h.oldPin = crCert(t, crIP, crOldAfter)
	h.newPEM, h.newPin = crCert(t, crIP, crNewAfter)
	if h.files == nil {
		h.files = map[string][]byte{}
	}
	if h.owned == nil {
		h.owned = map[string]bool{}
	}
	if h.units.States == nil {
		h.units.States = map[string]core.UnitState{}
	}
	h.units.States[server.UnitName] = core.UnitState{LoadState: "loaded", ActiveState: "active", SubState: "running", MainPID: 700}
	h.owned[server.StateDir] = true
	h.files[crCrt] = h.oldPEM
	h.files[crKey] = []byte("old key")
	at := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	err := insertLocalServer(context.Background(), h.db, localServer{
		ChangeID: "c0", Mode: server.ModeIP, HostName: crIP, VerifyClients: true, DerperVersion: "v1.104.1",
		BinarySHA256: strings.Repeat("a", 64), CertName: h.oldPin, CertNotAfter: crOldAfter, InstalledAt: at, UpdatedAt: at,
	})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// derperRestarts is the tick that stands for derper: once restarted without its certificate files it writes a new
// pair.
func (h *crHost) derperRestarts() func() {
	return func() {
		h.files[crCrt] = h.newPEM
		h.files[crKey] = []byte("new key")
	}
}

// crDrive runs call while advancing the host's clock by one poll whenever it waits; tick, when set, runs once, at
// the first wait, before the clock moves.
func crDrive(t *testing.T, h *dpHost, tick func(), call func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		call()
	}()
	ticked := false
	for deadline := time.Now().Add(10 * time.Second); ; {
		select {
		case <-done:
			return
		default:
		}
		if h.clock.Pending() > 0 {
			if tick != nil && !ticked {
				ticked = true
				tick()
			}
			h.clock.Advance(certRenewPoll)
		} else {
			time.Sleep(time.Millisecond)
		}
		if time.Now().After(deadline) {
			t.Fatal("the call did not finish")
		}
	}
}

// crApply applies derp.cert.renew as change c1.
func crApply(t *testing.T, h *dpHost, tick func()) (CertRenewed, error) {
	t.Helper()
	var raw json.RawMessage
	var err error
	crDrive(t, h, tick, func() {
		raw, err = h.dp().certRenew().Handler.Apply(context.Background(), h.env("c1"), json.RawMessage(`{}`))
	})
	if err != nil {
		return CertRenewed{}, err
	}
	var out CertRenewed
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("the answer %s does not decode as a CertRenewed: %v", raw, err)
	}
	return out, nil
}

func crEffects(t *testing.T, h *dpHost) []core.Effect {
	t.Helper()
	effects, err := h.env("c1").Effects.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return effects
}

func crSame(t *testing.T, got json.RawMessage, want string) bool {
	t.Helper()
	var a, b any
	if err := json.Unmarshal(got, &a); err != nil {
		t.Fatalf("document %s: %v", got, err)
	}
	if err := json.Unmarshal([]byte(want), &b); err != nil {
		t.Fatalf("document %s: %v", want, err)
	}
	return reflect.DeepEqual(a, b)
}

func crDoc(pin string, notAfter time.Time) string {
	if pin == "" {
		return `{"certName":"","notAfter":""}`
	}
	return `{"certName":"` + pin + `","notAfter":"` + notAfter.UTC().Format(time.RFC3339) + `"}`
}

func crRow(t *testing.T, h *dpHost) localServer {
	t.Helper()
	row, found, err := loadLocalServer(context.Background(), h.db.Read())
	if err != nil || !found {
		t.Fatalf("server row: found %v, err %v", found, err)
	}
	return row
}

// crApplied is a host on which the renewal ran: the old pair sits under .prev names, the new pair is in place,
// and the journal holds the renewal's one effect.
func crApplied(t *testing.T) (*crHost, core.Effect) {
	t.Helper()
	h := crInstalled(t)
	if _, err := crApply(t, h.dpHost, h.derperRestarts()); err != nil {
		t.Fatal(err)
	}
	effects := crEffects(t, h.dpHost)
	if len(effects) != 1 {
		t.Fatalf("%d effects, want 1", len(effects))
	}
	return h, effects[0]
}

func TestCrDefinition(t *testing.T) {
	def := newDPHost(t).dp().certRenew()
	s := def.Spec
	if s.Name != "derp.cert.renew" || s.Version != 1 || s.Class != core.ClassCompensable || s.Side != core.SideAgent ||
		!reflect.DeepEqual(s.LockKeys, []string{"derp"}) || s.CompensatedBy != "" || s.ForwardOnly || s.ApplyTimeout != 45*time.Second ||
		s.LongRunning || s.Task || s.Available == nil {
		t.Errorf("spec %+v, want a compensable agent action locking derp, with its own undo, 45 s and an availability check", s)
	}
	if def.InputType != reflect.TypeFor[CertRenewInput]() || def.OutputType != reflect.TypeFor[CertRenewed]() {
		t.Errorf("input %v and output %v, want CertRenewInput and CertRenewed", def.InputType, def.OutputType)
	}
	if h := def.Handler; h.Plan == nil || h.Apply == nil || h.Verify == nil || h.Observe == nil || h.Inverse == nil || h.Task != nil {
		t.Error("the handler needs Plan, Apply, Verify, Observe and Inverse, and no Task")
	}
}

// TestCrRefusals: a host without a server, a Let's Encrypt server and a server without a certificate are refused
// at plan and at apply, before anything is journaled or changed.
func TestCrRefusals(t *testing.T) {
	for _, tc := range []struct {
		name   string
		setup  func(t *testing.T) *dpHost
		code   errcode.Code
		detail string
	}{
		{
			"no server installed",
			func(t *testing.T) *dpHost { return newDPHost(t) },
			errcode.DERPNotReady, "",
		},
		{
			"a Let's Encrypt server",
			func(t *testing.T) *dpHost {
				h := newDPHost(t)
				at := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
				err := insertLocalServer(context.Background(), h.db, localServer{
					ChangeID: "c0", Mode: server.ModeLetsEncrypt, HostName: "derp.example.com", VerifyClients: true,
					DerperVersion: "v1.104.1", BinarySHA256: strings.Repeat("a", 64), InstalledAt: at, UpdatedAt: at,
				})
				if err != nil {
					t.Fatal(err)
				}
				return h
			},
			errcode.InvalidInput, "derper renews a Let's Encrypt certificate itself",
		},
		{
			"derper has not written its certificate",
			func(t *testing.T) *dpHost {
				h := crInstalled(t)
				delete(h.files, crCrt)
				return h.dpHost
			},
			errcode.DERPNotReady, "",
		},
	} {
		for _, phase := range []string{"plan", "apply"} {
			t.Run(tc.name+" at "+phase, func(t *testing.T) {
				h := tc.setup(t)
				def := h.dp().certRenew()
				var err error
				if phase == "plan" {
					_, err = def.Handler.Plan(context.Background(), h.env(""), json.RawMessage(`{}`))
				} else {
					_, err = def.Handler.Apply(context.Background(), h.env("c1"), json.RawMessage(`{}`))
				}
				coded := errcode.From(err)
				if err == nil || coded.Code != tc.code || !core.IsDefinite(err) {
					t.Fatalf("err = %v (definite %v), want a definite %s", err, core.IsDefinite(err), tc.code)
				}
				if tc.detail != "" && coded.Detail != tc.detail {
					t.Errorf("detail %q, want %q", coded.Detail, tc.detail)
				}
				if len(h.units.Calls) != 0 || len(h.ran) != 0 {
					t.Errorf("unit calls %q and commands %q, want none", h.units.Calls, h.ran)
				}
				if phase == "apply" && len(crEffects(t, h)) != 0 {
					t.Error("a refused apply journaled an effect")
				}
			})
		}
	}
}

func TestCrPlan(t *testing.T) {
	h := crInstalled(t)
	out, err := h.dp().certRenew().Handler.Plan(context.Background(), h.env(""), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	want := []core.DiffLine{
		{Op: "-", Text: "certificate " + h.oldPin + " (valid until 2027-01-02T03:04:05Z)"},
		{Op: "+", Text: "a new self-signed certificate that derper generates when it restarts"},
		{Op: " ", Text: "derper restarts; connections through it drop"},
		{Op: " ", Text: "clients that hold the old pin cannot use the region until the DERP map carries the new one"},
	}
	if !reflect.DeepEqual(out.Diff, want) || len(out.Guards) != 0 || out.Confirmation != "" {
		t.Errorf("plan %+v, want diff %+v and no guard or confirmation", out, want)
	}
	if out.StateHash == "" {
		t.Error("the plan has no state hash")
	}
	h.files[crCrt] = h.newPEM
	again, err := h.dp().certRenew().Handler.Plan(context.Background(), h.env(""), json.RawMessage(`{}`))
	if err != nil || again.StateHash == out.StateHash {
		t.Errorf("state hash %q after the certificate changed (err %v), want one different from %q", again.StateHash, err, out.StateHash)
	}
}

// TestCrApply: the old pair moves to .prev names, derper restarts and writes a new pair, and the new pin is
// stored; the effect is journaled with the old pin before any of it.
func TestCrApply(t *testing.T) {
	h, effect := crApplied(t)
	if got := crRow(t, h.dpHost); got.CertName != h.newPin || !got.CertNotAfter.Equal(crNewAfter) {
		t.Errorf("stored certificate %q until %v, want %q until %v", got.CertName, got.CertNotAfter, h.newPin, crNewAfter)
	}
	if want := []string{"restart " + server.UnitName}; !reflect.DeepEqual(h.units.Calls, want) {
		t.Errorf("unit calls %q, want %q", h.units.Calls, want)
	}
	for name, want := range map[string][]byte{
		crCrt + ".prev": h.oldPEM, crKey + ".prev": []byte("old key"), crCrt: h.newPEM, crKey: []byte("new key"),
	} {
		if got := h.files[name]; string(got) != string(want) {
			t.Errorf("%s holds %q, want %q", name, got, want)
		}
	}
	if effect.Resource != crResource || effect.Kind != core.EffectModify || !effect.Local || !effect.InverseLocal || effect.State != core.EffectMade {
		t.Errorf("effect = %s %s local %v inverse-local %v %s, want a made, local modify of %s", effect.Kind, effect.Resource, effect.Local, effect.InverseLocal, effect.State, crResource)
	}
	if !crSame(t, effect.Before.State, crDoc(h.oldPin, crOldAfter)) {
		t.Errorf("before state %s, want %s", effect.Before.State, crDoc(h.oldPin, crOldAfter))
	}
	if effect.Post == nil || !crSame(t, effect.Post.State, crDoc(h.newPin, crNewAfter)) {
		t.Errorf("post state %+v, want %s", effect.Post, crDoc(h.newPin, crNewAfter))
	}
}

func TestCrApplyOutput(t *testing.T) {
	h := crInstalled(t)
	out, err := crApply(t, h.dpHost, h.derperRestarts())
	if err != nil {
		t.Fatal(err)
	}
	want := CertRenewed{CertName: h.newPin, CertNotAfter: crNewAfter, PriorCertName: h.oldPin}
	if !reflect.DeepEqual(out, want) {
		t.Errorf("output %+v, want %+v", out, want)
	}
}

// TestCrApplyTimeout: derper that writes no new pair within the wait fails the renewal with derp.verify_failed,
// leaves the effect intended, and leaves the stored pin alone.
func TestCrApplyTimeout(t *testing.T) {
	h := crInstalled(t)
	start := h.clock.Now()
	_, err := crApply(t, h.dpHost, nil)
	if err == nil || errcode.From(err).Code != errcode.DERPVerifyFailed {
		t.Fatalf("err = %v, want derp.verify_failed", err)
	}
	if waited := h.clock.Now().Sub(start); waited < certWait {
		t.Errorf("gave up after %s, want at least %s", waited, certWait)
	}
	effects := crEffects(t, h.dpHost)
	if len(effects) != 1 || effects[0].State != core.EffectIntended || effects[0].Resource != crResource {
		t.Errorf("effects %+v, want the renewal's alone, intended", effects)
	}
	if got := crRow(t, h.dpHost); got.CertName != h.oldPin {
		t.Errorf("stored pin %q, want the old %q", got.CertName, h.oldPin)
	}
	if want := []string{"restart " + server.UnitName}; !reflect.DeepEqual(h.units.Calls, want) {
		t.Errorf("unit calls %q, want %q", h.units.Calls, want)
	}
}

// TestCrRefusesAMissingKey: a certificate without its key file is refused at plan and at apply, before the
// certificate is renamed, so the renewal never strands a pair half moved.
func TestCrRefusesAMissingKey(t *testing.T) {
	for _, phase := range []string{"plan", "apply"} {
		t.Run(phase, func(t *testing.T) {
			h := crInstalled(t)
			delete(h.files, crKey)
			def := h.dp().certRenew()
			var err error
			if phase == "plan" {
				_, err = def.Handler.Plan(context.Background(), h.env(""), json.RawMessage(`{}`))
			} else {
				_, err = def.Handler.Apply(context.Background(), h.env("c1"), json.RawMessage(`{}`))
			}
			if coded := errcode.From(err); err == nil || coded.Code != errcode.DERPNotReady || !core.IsDefinite(err) || !strings.Contains(coded.Detail, "key") {
				t.Fatalf("err = %v, want a definite derp.not_ready naming the key file", err)
			}
			if _, moved := h.files[crCrt+".prev"]; moved || len(h.units.Calls) != 0 || len(crEffects(t, h.dpHost)) != 0 {
				t.Errorf("files %v, unit calls %q: want nothing renamed, restarted or journaled", h.files, h.units.Calls)
			}
		})
	}
}

// TestCrApplyTimeoutNamesTheLastFailure: a wait that ends without a new pair says what it last found.
func TestCrApplyTimeoutNamesTheLastFailure(t *testing.T) {
	h := crInstalled(t)
	_, err := crApply(t, h.dpHost, func() { h.files[crCrt] = []byte("not a certificate") })
	if coded := errcode.From(err); err == nil || coded.Code != errcode.DERPVerifyFailed || !strings.Contains(coded.Detail, "CERTIFICATE") {
		t.Errorf("err = %v, want derp.verify_failed naming the undecodable certificate", err)
	}
}

func TestCrInverse(t *testing.T) {
	ctx := context.Background()
	t.Run("the old pair comes back and the old pin is stored again", func(t *testing.T) {
		h, effect := crApplied(t)
		if _, err := h.dp().certRenew().Handler.Inverse(ctx, h.env("c1"), effect); err != nil {
			t.Fatal(err)
		}
		if got := h.files[crCrt]; string(got) != string(h.oldPEM) {
			t.Errorf("%s holds %q, want the old certificate", crCrt, got)
		}
		if got := h.files[crKey]; string(got) != "old key" {
			t.Errorf("%s holds %q, want the old key", crKey, got)
		}
		if want := []string{"restart " + server.UnitName, "restart " + server.UnitName}; !reflect.DeepEqual(h.units.Calls, want) {
			t.Errorf("unit calls %q, want %q", h.units.Calls, want)
		}
		if got := crRow(t, h.dpHost); got.CertName != h.oldPin || !got.CertNotAfter.Equal(crOldAfter) {
			t.Errorf("stored certificate %q until %v, want %q until %v", got.CertName, got.CertNotAfter, h.oldPin, crOldAfter)
		}
	})
	t.Run("a restored pair whose pin is not the old one is an error and the row is kept", func(t *testing.T) {
		h, effect := crApplied(t)
		other, _ := crCert(t, crIP, crOldAfter)
		h.files[crCrt+".prev"] = other
		_, err := h.dp().certRenew().Handler.Inverse(ctx, h.env("c1"), effect)
		if err == nil || !strings.Contains(err.Error(), "pin") {
			t.Fatalf("err = %v, want the pin mismatch", err)
		}
		if got := crRow(t, h.dpHost); got.CertName != h.newPin {
			t.Errorf("stored pin %q, want the new %q still", got.CertName, h.newPin)
		}
	})
	t.Run("a missing .prev file changes nothing and says so", func(t *testing.T) {
		h, effect := crApplied(t)
		delete(h.files, crCrt+".prev")
		_, err := h.dp().certRenew().Handler.Inverse(ctx, h.env("c1"), effect)
		if err == nil || !core.IsDefinite(err) {
			t.Fatalf("err = %v (definite %v), want a definite error", err, core.IsDefinite(err))
		}
		if len(h.units.Calls) != 1 {
			t.Errorf("unit calls %q, want only the renewal's restart", h.units.Calls)
		}
		if got := h.files[crCrt]; string(got) != string(h.newPEM) {
			t.Errorf("%s holds %q, want the new certificate untouched", crCrt, got)
		}
	})
}

// TestCrObserve: the observation is the document of the live certificate, empty when the file is absent, which is
// how the engine tells a renewal that did not start from one that did.
func TestCrObserve(t *testing.T) {
	ctx := context.Background()
	h := crInstalled(t)
	observe := func(resource string) (core.Observation, error) {
		return h.dp().certRenew().Handler.Observe(ctx, h.env(""), core.Effect{Resource: resource, Kind: core.EffectModify})
	}
	check := func(want string) {
		t.Helper()
		obs, err := observe(crResource)
		if err != nil {
			t.Fatal(err)
		}
		if !crSame(t, obs.Current.State, want) || obs.Matches != 1 || !obs.Exclusive {
			t.Errorf("observation %s (matches %d, exclusive %v), want %s", obs.Current.State, obs.Matches, obs.Exclusive, want)
		}
	}
	check(crDoc(h.oldPin, crOldAfter))
	delete(h.files, crCrt)
	check(crDoc("", time.Time{}))
	h.files[crCrt] = h.newPEM
	check(crDoc(h.newPin, crNewAfter))
	for _, bad := range []string{"derp.cert:derp.example.com", "derp.file:" + crCrt, "derp.cert:"} {
		if _, err := observe(bad); err == nil {
			t.Errorf("observe %q succeeded, want an error", bad)
		}
	}
}

func TestCrRenewsAnIPv6CertificateUnderDerpersNames(t *testing.T) {
	ctx := context.Background()
	const ip = "2001:db8::9"
	crt := server.CertDir + "/2001db89.crt"
	key := server.CertDir + "/2001db89.key"
	h := newDPHost(t)
	if h.files == nil {
		h.files = map[string][]byte{}
	}
	if h.owned == nil {
		h.owned = map[string]bool{}
	}
	if h.units.States == nil {
		h.units.States = map[string]core.UnitState{}
	}
	oldPEM, oldPin := crCert(t, ip, crOldAfter)
	newPEM, newPin := crCert(t, ip, crNewAfter)
	h.units.States[server.UnitName] = core.UnitState{LoadState: "loaded", ActiveState: "active", SubState: "running", MainPID: 700}
	h.owned[server.StateDir] = true
	h.files[crt] = oldPEM
	h.files[key] = []byte("old key")
	at := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	if err := insertLocalServer(ctx, h.db, localServer{
		ChangeID: "c0", Mode: server.ModeIP, HostName: ip, VerifyClients: true, DerperVersion: "v1.104.1",
		BinarySHA256: strings.Repeat("a", 64), CertName: oldPin, CertNotAfter: crOldAfter, InstalledAt: at, UpdatedAt: at,
	}); err != nil {
		t.Fatal(err)
	}
	out, err := crApply(t, h, func() {
		h.files[crt] = newPEM
		h.files[key] = []byte("new key")
	})
	if err != nil {
		t.Fatalf("renew of an IPv6 server: %v", err)
	}
	if out.CertName != newPin || out.PriorCertName != oldPin {
		t.Errorf("output %+v, want CertName %s and PriorCertName %s", out, newPin, oldPin)
	}
	if string(h.files[crt+".prev"]) != string(oldPEM) || string(h.files[key+".prev"]) != "old key" {
		t.Errorf("the old pair was not kept as %s.prev and %s.prev, derper's names for %s", crt, key, ip)
	}
	effects := crEffects(t, h)
	if len(effects) != 1 || effects[0].Resource != "derp.cert:"+ip {
		t.Fatalf("effects %+v, want one on derp.cert:%s", effects, ip)
	}
	obs, err := h.dp().certRenew().Handler.Observe(ctx, h.env(""), effects[0])
	if err != nil {
		t.Fatalf("observe of the IPv6 certificate: %v", err)
	}
	if !crSame(t, obs.Current.State, crDoc(newPin, crNewAfter)) {
		t.Errorf("observation %s, want the new certificate read from %s", obs.Current.State, crt)
	}
	if _, err := h.dp().certRenew().Handler.Inverse(ctx, h.env("c1"), effects[0]); err != nil {
		t.Fatalf("undo of the IPv6 renewal: %v", err)
	}
	if string(h.files[crt]) != string(oldPEM) || string(h.files[key]) != "old key" {
		t.Errorf("the undo did not restore the old pair under %s and %s", crt, key)
	}
}

func TestCrVerify(t *testing.T) {
	ctx := context.Background()
	h, _ := crApplied(t)
	verify := func() error {
		return h.dp().certRenew().Handler.Verify(ctx, h.env("c1"), json.RawMessage(`{}`), json.RawMessage(`{}`))
	}
	if err := verify(); err != nil {
		t.Fatalf("verify after the renewal: %v", err)
	}
	if err := setLocalCert(ctx, h.db, h.oldPin, crOldAfter, h.clock.Now()); err != nil {
		t.Fatal(err)
	}
	if err := verify(); err == nil || !strings.Contains(err.Error(), "pin") {
		t.Errorf("err = %v, want the stored pin to differ from the certificate's", err)
	}
	if err := setLocalCert(ctx, h.db, h.newPin, crNewAfter, h.clock.Now()); err != nil {
		t.Fatal(err)
	}
	h.units.States[server.UnitName] = core.UnitState{LoadState: "loaded", ActiveState: "inactive", SubState: "dead"}
	if err := verify(); err == nil || !strings.Contains(err.Error(), server.UnitName) {
		t.Errorf("err = %v, want the inactive unit named", err)
	}
}
