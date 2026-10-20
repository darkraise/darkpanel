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
	"sync"
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

// generations lists the entries of dir other than current and save.lock.
func generations(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		if e.Name() != currentLink && e.Name() != saveLock {
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
	n, err := SaveGeneration(dir, first, AnyCurrent)
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
		filepath.Join(dir, "gen-1", controllerFile): 0o600, filepath.Join(dir, saveLock): 0o600,
	} {
		if st, err := os.Stat(path); err != nil || st.Mode().Perm() != mode {
			t.Errorf("%s: %v, %v; want mode %v", path, st.Mode(), err, mode)
		}
	}
	second := ca.credentials(t, "H1")
	if n, err := SaveGeneration(dir, second, AnyCurrent); err != nil || n != 2 {
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
	if n, err := SaveGeneration(dir, third, AnyCurrent); err != nil || n != 3 {
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
		if _, err := SaveGeneration(dir, c, AnyCurrent); err == nil {
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
	if _, err := SaveGeneration(dir, ca.credentials(t, "H1"), AnyCurrent); err != nil {
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
				if _, err := SaveGeneration(dir, old, AnyCurrent); err != nil {
					t.Fatal(err)
				}
			}
			next := ca.credentials(t, "H1")
			_, err := saveGeneration(dir, next, AnyCurrent, func(s string) error {
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
			n, err := SaveGeneration(dir, again, AnyCurrent)
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

// A renewal saves only while current is still its session's generation; an
// enrollment saves whatever is current. A refused save writes nothing.
func TestSaveGenerationExpect(t *testing.T) {
	ca := newTestAuthority(t)
	cases := []struct {
		name    string
		current int // the generation current names; 0 removes current
		expect  int
		want    int // the saved generation; 0 wants ErrSuperseded
	}{
		{"the expected generation", 2, 2, 3},
		{"a newer generation", 2, 1, 0},
		{"an older generation", 1, 2, 0},
		{"no current", 0, 1, 0},
		{"any current", 1, AnyCurrent, 3},
		{"any current without current", 0, AnyCurrent, 3},
	}
	for _, tc := range cases {
		dir := filepath.Join(t.TempDir(), "agent")
		for range 2 {
			if _, err := SaveGeneration(dir, ca.credentials(t, "H1"), AnyCurrent); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.Remove(filepath.Join(dir, currentLink)); err != nil {
			t.Fatal(err)
		}
		if tc.current != 0 {
			if err := os.Symlink(generationName(tc.current), filepath.Join(dir, currentLink)); err != nil {
				t.Fatal(err)
			}
		}
		state := func() string {
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			target, _ := os.Readlink(filepath.Join(dir, currentLink))
			out := "current -> " + target
			for _, e := range entries {
				out += " " + e.Name()
			}
			return out
		}
		before := state()
		next := ca.credentials(t, "H1")
		n, err := SaveGeneration(dir, next, tc.expect)
		if tc.want == 0 {
			if !errors.Is(err, ErrSuperseded) || n != 0 || next.Generation != 0 {
				t.Errorf("%s: SaveGeneration = %d, %v, generation %d; want ErrSuperseded", tc.name, n, err, next.Generation)
			}
			if after := state(); after != before {
				t.Errorf("%s: a superseded save changed %s to %s", tc.name, before, after)
			}
			continue
		}
		if err != nil || n != tc.want {
			t.Errorf("%s: SaveGeneration = %d, %v; want generation %d", tc.name, n, err, tc.want)
			continue
		}
		if got, err := LoadCredentials(dir); err != nil || !sameCredentials(got, next) || got.Generation != tc.want {
			t.Errorf("%s: LoadCredentials = %+v, %v; want the saved generation", tc.name, got, err)
		}
	}
}

// darkpanel agent enroll and the daemon's renewal save beside each other. Whatever the interleaving, current names a complete
// generation that its writer reported, and a writer that finished after
// another succeeded only if it expected any current.
func TestSaveGenerationConcurrentWriters(t *testing.T) {
	ca := newTestAuthority(t)
	for _, tc := range []struct {
		name    string
		renewal [2]bool // whether each writer expects the generation current at the start
	}{
		{"a renewal and an enrollment", [2]bool{true, false}},
		{"two renewals", [2]bool{true, true}},
		{"two enrollments", [2]bool{false, false}},
	} {
		dir := filepath.Join(t.TempDir(), "agent")
		start, err := SaveGeneration(dir, ca.credentials(t, "H1"), AnyCurrent)
		if err != nil {
			t.Fatal(err)
		}
		for round := range 20 {
			var creds [2]*Credentials
			var gens [2]int
			var errs [2]error
			var wg sync.WaitGroup
			ready := make(chan struct{})
			for i := range 2 {
				creds[i] = ca.credentials(t, "H1")
				expect := AnyCurrent
				if tc.renewal[i] {
					expect = start
				}
				wg.Go(func() {
					<-ready
					gens[i], errs[i] = SaveGeneration(dir, creds[i], expect)
				})
			}
			close(ready)
			wg.Wait()
			winner, won := 0, false
			for i := range 2 {
				switch {
				case errs[i] == nil && (!won || gens[i] > gens[winner]):
					winner, won = i, true
				case errs[i] == nil:
				case !tc.renewal[i] || !errors.Is(errs[i], ErrSuperseded):
					t.Fatalf("%s, round %d: writer %d: %v", tc.name, round, i, errs[i])
				}
			}
			if !won {
				t.Fatalf("%s, round %d: both writers were superseded", tc.name, round)
			}
			if loser := 1 - winner; errs[loser] == nil && tc.renewal[winner] {
				t.Fatalf("%s, round %d: renewal %d of generation %d saved after generation %d",
					tc.name, round, gens[winner], start, gens[loser])
			}
			got, err := LoadCredentials(dir)
			if err != nil || !sameCredentials(got, creds[winner]) || got.Generation != gens[winner] {
				t.Fatalf("%s, round %d: current is %+v, %v; want the winner's generation %d", tc.name, round, got, err, gens[winner])
			}
			start = gens[winner]
			if err := PruneGenerations(dir, start); err != nil {
				t.Fatal(err)
			}
		}
	}
}
