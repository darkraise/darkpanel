package daemon

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/transport"
)

// testAuthority is a stand-in agent root for tests that need certificates
// but no controller.
type testAuthority struct {
	cert *x509.Certificate
	key  ed25519.PrivateKey
}

func newTestAuthority(t *testing.T) *testAuthority {
	t.Helper()
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test agent root"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, pub, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return &testAuthority{cert: cert, key: key}
}

// credentials returns hostID's credentials with a fresh key, as an
// enrollment or a renewal produces them.
func (a *testAuthority) credentials(t *testing.T, hostID string) *Credentials {
	t.Helper()
	return a.issue(t, transport.HostURI(hostID), x509.ExtKeyUsageClientAuth)
}

// issue returns credentials whose certificate names uri and has usage eku.
func (a *testAuthority) issue(t *testing.T, uri string, eku x509.ExtKeyUsage) *Credentials {
	t.Helper()
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(uri)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(12 * time.Hour),
		ExtKeyUsage: []x509.ExtKeyUsage{eku}, URIs: []*url.URL{u},
		KeyUsage: x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, a.cert, pub, a.key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return &Credentials{
		Key: key, Cert: cert, CA: a.cert,
		Controller: Endpoint{Address: "127.0.0.1:7443", SAN: transport.ControllerURI("C1")},
	}
}

// sameCredentials reports whether got holds want's key and certificate.
func sameCredentials(got, want *Credentials) bool {
	return got != nil && got.Key.Equal(want.Key) && got.Cert.Equal(want.Cert) && got.CA.Equal(want.CA) &&
		got.Controller == want.Controller
}

// generations lists the gen-<n> entries of dir.
func generations(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		if e.Name() != currentLink {
			out = append(out, e.Name())
		}
	}
	return out
}

func TestCredentialsRoundTrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "agent")
	if _, err := LoadCredentials(dir); !errors.Is(err, ErrNotEnrolled) {
		t.Fatalf("LoadCredentials of an empty directory = %v, want ErrNotEnrolled", err)
	}
	ca := newTestAuthority(t)
	first := ca.credentials(t, "H1")
	n, err := SaveGeneration(dir, first)
	if err != nil || n != 1 {
		t.Fatalf("SaveGeneration = %d, %v; want generation 1", n, err)
	}
	got, err := LoadCredentials(dir)
	if err != nil || !sameCredentials(got, first) || got.Generation != 1 || got.HostID() != "H1" {
		t.Fatalf("LoadCredentials = %+v, %v", got, err)
	}
	if c := got.TLSCertificate(); c.Leaf != got.Cert || len(c.Certificate) != 1 {
		t.Errorf("TLSCertificate = %+v", c)
	}
	for path, mode := range map[string]os.FileMode{
		dir: 0o700, filepath.Join(dir, "gen-1"): 0o700, filepath.Join(dir, "gen-1", keyFile): 0o600,
		filepath.Join(dir, "gen-1", certFile): 0o600, filepath.Join(dir, "gen-1", caFile): 0o600,
		filepath.Join(dir, "gen-1", controllerFile): 0o600,
	} {
		if st, err := os.Stat(path); err != nil || st.Mode().Perm() != mode {
			t.Errorf("%s: %v, %v; want mode %v", path, st.Mode(), err, mode)
		}
	}
	second := ca.credentials(t, "H1")
	if n, err := SaveGeneration(dir, second); err != nil || n != 2 {
		t.Fatalf("second SaveGeneration = %d, %v", n, err)
	}
	if got, err := LoadCredentials(dir); err != nil || !sameCredentials(got, second) || got.Generation != 2 {
		t.Fatalf("LoadCredentials after the second generation = %+v, %v", got, err)
	}
	// The previous generation stays until a handshake with the new one.
	if gens := generations(t, dir); !slices.Equal(gens, []string{"gen-1", "gen-2"}) {
		t.Errorf("generations = %v, want both", gens)
	}
	// An enrollment beside the running daemon may already have written a
	// newer generation; a handshake with generation 2 must not delete it.
	third := ca.credentials(t, "H1")
	if n, err := SaveGeneration(dir, third); err != nil || n != 3 {
		t.Fatalf("third SaveGeneration = %d, %v", n, err)
	}
	if err := PruneGenerations(dir, 2); err != nil {
		t.Fatal(err)
	}
	if gens := generations(t, dir); !slices.Equal(gens, []string{"gen-2", "gen-3"}) {
		t.Errorf("generations after pruning = %v, want gen-2 and gen-3", gens)
	}
	if got, err := LoadCredentials(dir); err != nil || !sameCredentials(got, third) {
		t.Errorf("LoadCredentials after pruning = %+v, %v; want the third generation", got, err)
	}
	if err := PruneGenerations(dir, 3); err != nil {
		t.Fatal(err)
	}
	if gens := generations(t, dir); !slices.Equal(gens, []string{"gen-3"}) {
		t.Errorf("generations after the second pruning = %v, want gen-3 alone", gens)
	}
}

func TestSaveGenerationRefusesBadCredentials(t *testing.T) {
	ca := newTestAuthority(t)
	other := newTestAuthority(t)
	cases := map[string]func(c *Credentials){
		"another key":       func(c *Credentials) { c.Key = ca.credentials(t, "H1").Key },
		"another authority": func(c *Credentials) { c.CA = other.cert },
		"no address":        func(c *Credentials) { c.Controller.Address = "" },
		"address port":      func(c *Credentials) { c.Controller.Address = "controller" },
		"controller SAN":    func(c *Credentials) { c.Controller.SAN = "darkpanel://host/C1" },
		"server usage": func(c *Credentials) {
			*c = *ca.issue(t, transport.HostURI("H1"), x509.ExtKeyUsageServerAuth)
		},
		"no host id": func(c *Credentials) {
			*c = *ca.issue(t, transport.ControllerURI("H1"), x509.ExtKeyUsageClientAuth)
		},
	}
	for name, spoil := range cases {
		dir := t.TempDir()
		c := ca.credentials(t, "H1")
		spoil(c)
		if _, err := SaveGeneration(dir, c); err == nil {
			t.Errorf("%s: SaveGeneration accepted it", name)
		}
		if _, err := LoadCredentials(dir); !errors.Is(err, ErrNotEnrolled) {
			t.Errorf("%s: a refused generation became current: %v", name, err)
		}
	}
}

func TestLoadCredentialsRefusesAForeignCurrent(t *testing.T) {
	dir := t.TempDir()
	ca := newTestAuthority(t)
	if _, err := SaveGeneration(dir, ca.credentials(t, "H1")); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"../gen-1", "/etc", "gen-x"} {
		if err := os.Remove(filepath.Join(dir, currentLink)); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, filepath.Join(dir, currentLink)); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadCredentials(dir); err == nil || errors.Is(err, ErrNotEnrolled) {
			t.Errorf("current -> %s: LoadCredentials = %v, want a refusal", target, err)
		}
	}
}

// Spec Verification "credentials": an enrollment or a renewal interrupted
// at any step before, during or after the switch of current leaves a
// complete old or a complete new generation, and a restarted agent can
// write the next one.
func TestSaveGenerationInterrupted(t *testing.T) {
	ca := newTestAuthority(t)
	crash := errors.New("power cut")
	switched := slices.Index(generationSteps, stepRename) + 1
	for _, renewal := range []bool{false, true} {
		for i, step := range generationSteps {
			dir := filepath.Join(t.TempDir(), "agent")
			var old *Credentials
			if renewal {
				old = ca.credentials(t, "H1")
				if _, err := SaveGeneration(dir, old); err != nil {
					t.Fatal(err)
				}
			}
			next := ca.credentials(t, "H1")
			_, err := saveGeneration(dir, next, func(s string) error {
				if s == step {
					return crash
				}
				return nil
			})
			if !errors.Is(err, crash) {
				t.Fatalf("renewal %v, step %s: saveGeneration = %v, want the crash", renewal, step, err)
			}
			// The restarted agent reads one complete generation.
			got, err := LoadCredentials(dir)
			switch {
			case i >= switched:
				if err != nil || !sameCredentials(got, next) {
					t.Errorf("renewal %v, crash at %s: loaded %+v, %v; want the new generation", renewal, step, got, err)
				}
			case renewal:
				if err != nil || !sameCredentials(got, old) {
					t.Errorf("renewal %v, crash at %s: loaded %+v, %v; want the old generation", renewal, step, got, err)
				}
			default:
				if !errors.Is(err, ErrNotEnrolled) {
					t.Errorf("enrollment, crash at %s: LoadCredentials = %v, want ErrNotEnrolled", step, err)
				}
			}
			// It enrolls or renews again, and the first handshake with the
			// result prunes everything else.
			again := ca.credentials(t, "H1")
			n, err := SaveGeneration(dir, again)
			if err != nil {
				t.Fatalf("renewal %v, crash at %s: SaveGeneration after the restart: %v", renewal, step, err)
			}
			if got, err := LoadCredentials(dir); err != nil || !sameCredentials(got, again) || got.Generation != n {
				t.Fatalf("renewal %v, crash at %s: loaded %+v, %v after the restart", renewal, step, got, err)
			}
			if err := PruneGenerations(dir, n); err != nil {
				t.Fatal(err)
			}
			if gens := generations(t, dir); !slices.Equal(gens, []string{generationName(n)}) {
				t.Errorf("renewal %v, crash at %s: left %v after pruning", renewal, step, gens)
			}
		}
	}
}
