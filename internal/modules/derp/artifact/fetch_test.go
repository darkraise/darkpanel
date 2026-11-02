package artifact

import (
	"bytes"
	"cmp"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"aead.dev/minisign"

	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/update"
	"github.com/darkraise/darkpanel/internal/update/trustedkeys"
)

// feDarkpanel is the darkpanel version every test release belongs to.
const feDarkpanel = "1.4.0"

type feSigner struct {
	key  trustedkeys.Key
	priv minisign.PrivateKey
}

func feNewSigner(t *testing.T) feSigner {
	t.Helper()
	pub, priv, err := minisign.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return feSigner{key: trustedkeys.Key{ID: fmt.Sprintf("%016X", pub.ID()), PublicKey: pub.String()}, priv: priv}
}

// sign signs data in hashed mode with the trusted comment of a release's checksums.txt.
func (s feSigner) sign(data []byte, version, channel string) []byte {
	r := minisign.NewReader(bytes.NewReader(data))
	_, _ = io.Copy(io.Discard, r)
	return r.SignWithComments(s.priv, fmt.Sprintf("version=%s channel=%s purpose=checksums", version, channel), "signature from darkpanel test key "+s.key.ID)
}

func feSum(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// feServer serves release files under URL as v<version>/<file>, records every path asked for, and can answer one
// path with an endless stream of zero bytes.
type feServer struct {
	URL     string
	srv     *httptest.Server
	mu      sync.Mutex
	files   map[string][]byte
	endless map[string]bool
	asked   []string
	sent    map[string]int64
}

func newFeServer(t *testing.T) *feServer {
	t.Helper()
	s := &feServer{files: map[string][]byte{}, endless: map[string]bool{}, sent: map[string]int64{}}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/")
		s.mu.Lock()
		s.asked = append(s.asked, name)
		body, ok := s.files[name]
		endless := s.endless[name]
		s.mu.Unlock()
		switch {
		case endless:
			chunk := make([]byte, 1<<20)
			for sent := int64(0); sent < 200<<20; sent += int64(len(chunk)) {
				if _, err := w.Write(chunk); err != nil {
					return
				}
				s.mu.Lock()
				s.sent[name] += int64(len(chunk))
				s.mu.Unlock()
			}
		case !ok || r.Method != http.MethodGet:
			http.NotFound(w, r)
		default:
			_, _ = w.Write(body)
		}
	}))
	s.URL = s.srv.URL
	t.Cleanup(s.srv.Close)
	return s
}

func (s *feServer) put(name string, b []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.files[name] = bytes.Clone(b)
}

func (s *feServer) get(name string) []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return bytes.Clone(s.files[name])
}

func (s *feServer) remove(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.files, name)
}

func (s *feServer) paths() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.asked)
}

// sign rebuilds checksums.txt of release dp from the files now served under v<dp>/ and signs it for channel, so a
// test that changed a file and signs again has a release whose signed list matches what is served.
func (s *feServer) sign(signer feSigner, dp, channel string) {
	prefix := "v" + dp + "/"
	s.mu.Lock()
	defer s.mu.Unlock()
	var names []string
	for name := range s.files {
		if rest, ok := strings.CutPrefix(name, prefix); ok && !strings.HasPrefix(rest, "checksums.txt") {
			names = append(names, rest)
		}
	}
	slices.Sort(names)
	var sums bytes.Buffer
	for _, n := range names {
		fmt.Fprintf(&sums, "%s  %s\n", feSum(s.files[prefix+n]), n)
	}
	s.files[prefix+"checksums.txt"] = bytes.Clone(sums.Bytes())
	s.files[prefix+"checksums.txt."+signer.key.ID+".minisig"] = signer.sign(sums.Bytes(), dp, channel)
}

// fePublish serves release dp (stable) with derper version derper for every arch of Arches and returns each build.
func fePublish(t *testing.T, s *feServer, signer feSigner, dp, derper string) map[string][]byte {
	t.Helper()
	builds := map[string][]byte{}
	m := Manifest{DarkpanelVersion: dp, DerperVersion: derper, Artifacts: map[string]string{}}
	for _, arch := range Arches {
		bin := []byte("derper " + derper + " " + arch)
		builds[arch] = bin
		s.put("v"+dp+"/"+BinaryName(derper, arch), bin)
		m.Artifacts[arch] = feSum(bin)
	}
	enc, err := m.Encode()
	if err != nil {
		t.Fatal(err)
	}
	s.put("v"+dp+"/"+ManifestName, enc)
	s.sign(signer, dp, "stable")
	return builds
}

// feManifest is the manifest of release dp for derper, listing the hashes of builds.
func feManifest(t *testing.T, dp, derper string, builds map[string][]byte) []byte {
	t.Helper()
	m := Manifest{DarkpanelVersion: dp, DerperVersion: derper, Artifacts: map[string]string{}}
	for arch, bin := range builds {
		m.Artifacts[arch] = feSum(bin)
	}
	enc, err := m.Encode()
	if err != nil {
		t.Fatal(err)
	}
	return enc
}

// feSource reads the index of one stable release, feDarkpanel, and trusts keys.
func feSource(s *feServer, keys ...trustedkeys.Key) Source {
	return Source{
		Client:  s.srv.Client(),
		BaseURL: s.URL,
		Keys:    keys,
		Releases: func(context.Context) ([]update.Release, error) {
			return []update.Release{{Version: "1.3.0", Channel: "stable"}, {Version: feDarkpanel, Channel: "stable"}}, nil
		},
	}
}

func feBinaryAsked(paths []string) bool {
	for _, p := range paths {
		if strings.Contains(p, "-linux-") {
			return true
		}
	}
	return false
}

func TestFeFetch(t *testing.T) {
	signer, other := feNewSigner(t), feNewSigner(t)
	srv := newFeServer(t)
	builds := fePublish(t, srv, signer, feDarkpanel, DerperVersion)
	if bytes.Equal(builds["amd64"], builds["arm64"]) {
		t.Fatal("the test builds must differ")
	}
	src := feSource(srv, other.key, signer.key)

	bin, sha, err := Fetch(context.Background(), src, feDarkpanel, DerperVersion, "amd64")
	if err != nil {
		t.Fatalf("Fetch amd64: %v", err)
	}
	if !bytes.Equal(bin, builds["amd64"]) || sha != feSum(builds["amd64"]) {
		t.Errorf("Fetch amd64 = %q, %s; want the amd64 build and its SHA-256", bin, sha)
	}
	for _, p := range srv.paths() {
		if !strings.HasPrefix(p, "v"+feDarkpanel+"/") {
			t.Errorf("asked for %s; the agent takes derper only from its own release", p)
		}
		if strings.Contains(p, "linux-arm64") {
			t.Errorf("asked for %s while fetching amd64", p)
		}
	}

	bin, sha, err = Fetch(context.Background(), src, feDarkpanel, DerperVersion, "arm64")
	if err != nil || !bytes.Equal(bin, builds["arm64"]) || sha != feSum(builds["arm64"]) {
		t.Errorf("Fetch arm64 = %q, %s, %v; want the arm64 build", bin, sha, err)
	}
	if len(sha) != 64 || sha != strings.ToLower(sha) {
		t.Errorf("sha %q is not 64 lower-case hex digits", sha)
	}
}

func TestFeFetchRefuses(t *testing.T) {
	signer, stranger := feNewSigner(t), feNewSigner(t)
	manifestPath := "v" + feDarkpanel + "/" + ManifestName
	binaryPath := "v" + feDarkpanel + "/" + BinaryName(DerperVersion, "amd64")
	sigPath := "v" + feDarkpanel + "/checksums.txt." + signer.key.ID + ".minisig"
	resign := func(s *feServer) { s.sign(signer, feDarkpanel, "stable") }

	tests := []struct {
		name     string
		dp, want string // zero values mean feDarkpanel and DerperVersion
		arch     string // zero value means amd64
		tamper   func(t *testing.T, s *feServer)
		code     errcode.Code // "" means a plain error
		detail   string       // the exact detail of a coded error, else a substring of the message
		noBinary bool
	}{
		{name: "darkpanel version not in the index", dp: "1.5.0", code: errcode.UpdateNotFound,
			detail: "darkpanel 1.5.0 is not a published release; a DERP server needs a released darkpanel", noBinary: true},
		{name: "development build", dp: "dev", code: errcode.UpdateNotFound,
			detail: "darkpanel dev is not a published release; a DERP server needs a released darkpanel", noBinary: true},
		{name: "manifest names another derper", want: "v1.105.0", code: errcode.UpdateNotFound,
			detail: "release " + feDarkpanel + " carries derper " + DerperVersion + ", not v1.105.0", noBinary: true},
		{name: "manifest not published", tamper: func(_ *testing.T, s *feServer) { s.remove(manifestPath); resign(s) },
			code: errcode.UpdateNotFound, noBinary: true},
		{name: "manifest changed after signing", tamper: func(_ *testing.T, s *feServer) { s.put(manifestPath, append(s.get(manifestPath), ' ')) },
			code: errcode.UpdateSignatureInvalid, detail: "derper.json does not match the signed checksums.txt", noBinary: true},
		{name: "manifest names another darkpanel", tamper: func(t *testing.T, s *feServer) {
			s.put(manifestPath, feManifest(t, "1.3.0", DerperVersion, map[string][]byte{"amd64": s.get(binaryPath), "arm64": []byte("x")}))
			resign(s)
		}, code: errcode.UpdateSignatureInvalid, noBinary: true},
		{name: "manifest with an unknown member", tamper: func(t *testing.T, s *feServer) {
			doc := strings.TrimSuffix(strings.TrimSpace(string(feManifest(t, feDarkpanel, DerperVersion, map[string][]byte{"amd64": []byte("a"), "arm64": []byte("b")}))), "}")
			s.put(manifestPath, []byte(doc+`,"surprise":1}`))
			resign(s)
		}, detail: "surprise", noBinary: true},
		{name: "manifest over its limit", tamper: func(_ *testing.T, s *feServer) { s.put(manifestPath, bytes.Repeat([]byte(" "), 64<<10+1)); resign(s) },
			detail: "65536", noBinary: true},
		{name: "binary changed after signing", tamper: func(_ *testing.T, s *feServer) { s.put(binaryPath, []byte("evil")) },
			code: errcode.UpdateSignatureInvalid, detail: BinaryName(DerperVersion, "amd64") + " does not match the signed checksums.txt"},
		{name: "binary that the manifest does not list by hash", tamper: func(_ *testing.T, s *feServer) { s.put(binaryPath, []byte("evil")); resign(s) },
			code: errcode.UpdateSignatureInvalid},
		{name: "binary not listed in checksums.txt", tamper: func(_ *testing.T, s *feServer) { s.remove(binaryPath); resign(s) },
			code: errcode.UpdateNotFound},
		{name: "checksums.txt signed for the beta channel, listed as stable", tamper: func(_ *testing.T, s *feServer) { s.sign(signer, feDarkpanel, "beta") },
			code: errcode.UpdateSignatureInvalid, noBinary: true},
		{name: "signed by a key the agent does not trust", tamper: func(_ *testing.T, s *feServer) {
			s.remove(sigPath)
			s.sign(stranger, feDarkpanel, "stable")
		}, code: errcode.UpdateSignatureInvalid, noBinary: true},
		{name: "architecture without a build", arch: "riscv64", detail: "riscv64", noBinary: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newFeServer(t)
			fePublish(t, srv, signer, feDarkpanel, DerperVersion)
			if tt.tamper != nil {
				tt.tamper(t, srv)
			}
			dp, want, arch := cmp.Or(tt.dp, feDarkpanel), cmp.Or(tt.want, DerperVersion), cmp.Or(tt.arch, "amd64")
			bin, sha, err := Fetch(context.Background(), feSource(srv, signer.key), dp, want, arch)
			if err == nil {
				t.Fatalf("Fetch accepted the release and returned %d bytes", len(bin))
			}
			if bin != nil || sha != "" {
				t.Errorf("a refused fetch returned %d bytes and sha %q", len(bin), sha)
			}
			var ce *errcode.Error
			switch {
			case tt.code != "":
				if !errors.As(err, &ce) || ce.Code != tt.code {
					t.Fatalf("Fetch = %v, want %s", err, tt.code)
				}
				if tt.detail != "" && ce.Detail != tt.detail {
					t.Errorf("detail %q, want %q", ce.Detail, tt.detail)
				}
			case tt.detail != "" && !strings.Contains(err.Error(), tt.detail):
				t.Errorf("Fetch = %v, want an error mentioning %q", err, tt.detail)
			}
			if tt.noBinary && feBinaryAsked(srv.paths()) {
				t.Errorf("a binary was fetched before the refusal: %v", srv.paths())
			}
		})
	}
}

func TestFeFetchReadsTheIndexChannel(t *testing.T) {
	signer := feNewSigner(t)
	srv := newFeServer(t)
	builds := fePublish(t, srv, signer, feDarkpanel, DerperVersion)
	srv.sign(signer, feDarkpanel, "beta")
	src := feSource(srv, signer.key)
	src.Releases = func(context.Context) ([]update.Release, error) {
		return []update.Release{{Version: feDarkpanel, Channel: "beta"}}, nil
	}
	bin, _, err := Fetch(context.Background(), src, feDarkpanel, DerperVersion, "amd64")
	if err != nil || !bytes.Equal(bin, builds["amd64"]) {
		t.Fatalf("Fetch of a release the index lists as beta and the signature says beta = %q, %v", bin, err)
	}
}

func TestFeFetchIndexFailure(t *testing.T) {
	signer := feNewSigner(t)
	srv := newFeServer(t)
	fePublish(t, srv, signer, feDarkpanel, DerperVersion)
	boom := errors.New("index down")
	src := feSource(srv, signer.key)
	src.Releases = func(context.Context) ([]update.Release, error) { return nil, boom }
	if _, _, err := Fetch(context.Background(), src, feDarkpanel, DerperVersion, "amd64"); !errors.Is(err, boom) {
		t.Fatalf("Fetch = %v, want the index error", err)
	}
	if got := srv.paths(); len(got) != 0 {
		t.Errorf("the release server was asked for %v after the index failed", got)
	}
}

func TestFeFetchBoundsTheBinary(t *testing.T) {
	if testing.Short() {
		t.Skip("streams 128 MiB")
	}
	signer := feNewSigner(t)
	srv := newFeServer(t)
	fePublish(t, srv, signer, feDarkpanel, DerperVersion)
	name := "v" + feDarkpanel + "/" + BinaryName(DerperVersion, "amd64")
	srv.mu.Lock()
	srv.endless[name] = true
	srv.mu.Unlock()
	_, _, err := Fetch(context.Background(), feSource(srv, signer.key), feDarkpanel, DerperVersion, "amd64")
	if err == nil || !strings.Contains(err.Error(), "134217728") {
		t.Fatalf("Fetch of an endless binary = %v, want an error naming the 128 MiB limit", err)
	}
	srv.mu.Lock()
	sent := srv.sent[name]
	srv.mu.Unlock()
	if sent >= 200<<20 {
		t.Errorf("the server streamed %d bytes: the client did not stop at its limit", sent)
	}
}
