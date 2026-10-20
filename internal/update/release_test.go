package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"aead.dev/minisign"

	"github.com/darkraise/darkpanel/internal/update/trustedkeys"
)

// testSigner is a generated minisign key pair and its trusted-key entry.
type testSigner struct {
	Key  trustedkeys.Key
	priv minisign.PrivateKey
}

func newTestSigner(t *testing.T) testSigner {
	t.Helper()
	pub, priv, err := minisign.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return testSigner{Key: trustedkeys.Key{ID: fmt.Sprintf("%016X", pub.ID()), PublicKey: pub.String()}, priv: priv}
}

// sign returns a minisign signature of data in hashed mode (algorithm ED),
// as the release workflow makes them, with comment as its trusted comment.
func (s testSigner) sign(data []byte, comment string) []byte {
	r := minisign.NewReader(bytes.NewReader(data))
	_, _ = io.Copy(io.Discard, r)
	return r.SignWithComments(s.priv, comment, "signature from darkpanel test key "+s.Key.ID)
}

// releaseComment is the trusted comment of a release's checksums.txt (F-C16).
func releaseComment(version, channel string) string {
	return fmt.Sprintf("version=%s channel=%s purpose=checksums", version, channel)
}

// tarEntry is one file of a test tarball; Type zero means a regular file.
type tarEntry struct {
	Name     string
	Body     []byte
	Type     byte
	Linkname string
}

// testTarball builds a gzipped tar archive of entries.
func testTarball(t *testing.T, entries ...tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		typ := e.Type
		if typ == 0 {
			typ = tar.TypeReg
		}
		hdr := &tar.Header{Name: e.Name, Typeflag: typ, Linkname: e.Linkname, Mode: 0o755}
		if typ == tar.TypeReg {
			hdr.Size = int64(len(e.Body))
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(e.Body); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// releaseServer serves release files in the production layout under URL:
// index/releases.json and v<version>/<file>. Files can be replaced or removed
// between requests.
type releaseServer struct {
	URL   string
	srv   *httptest.Server
	mu    sync.Mutex
	files map[string][]byte
}

func newReleaseServer(t *testing.T) *releaseServer {
	t.Helper()
	s := &releaseServer{files: map[string][]byte{}}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		body, ok := s.files[strings.TrimPrefix(r.URL.Path, "/")]
		s.mu.Unlock()
		if !ok || r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(body)
	}))
	s.URL = s.srv.URL
	t.Cleanup(s.srv.Close)
	return s
}

// client is an HTTP client for the server.
func (s *releaseServer) client() *http.Client { return s.srv.Client() }

func (s *releaseServer) put(name string, body []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.files[name] = bytes.Clone(body)
}

func (s *releaseServer) get(name string) []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return bytes.Clone(s.files[name])
}

func (s *releaseServer) remove(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.files, name)
}

// publish publishes version for arch with binary as its darkpanel: the
// tarball (darkpanel, LICENSE, README.md), checksums.txt listing it and an
// SBOM, and one checksums.txt.<ID>.minisig per signer, each with the trusted
// comment for version and channel.
func (s *releaseServer) publish(t *testing.T, version, channel, arch string, binary []byte, signers ...testSigner) {
	t.Helper()
	tarball := testTarball(t,
		tarEntry{Name: "darkpanel", Body: binary},
		tarEntry{Name: "LICENSE", Body: []byte("license")},
		tarEntry{Name: "README.md", Body: []byte("readme")})
	sbom := []byte(`{"bomFormat": "CycloneDX"}`)
	dir := "v" + version + "/"
	name := TarballName(version, arch)
	s.put(dir+name, tarball)
	s.put(dir+"darkpanel_"+version+".cdx.json", sbom)
	checksums := checksumLine(tarball, name) + checksumLine(sbom, "darkpanel_"+version+".cdx.json")
	s.put(dir+"checksums.txt", []byte(checksums))
	for _, signer := range signers {
		s.put(dir+"checksums.txt."+signer.Key.ID+".minisig", signer.sign([]byte(checksums), releaseComment(version, channel)))
	}
}

// setIndex publishes index/releases.json listing releases.
func (s *releaseServer) setIndex(t *testing.T, releases ...Release) {
	t.Helper()
	body, err := json.Marshal(Index{FormatVersion: 1, Releases: releases})
	if err != nil {
		t.Fatal(err)
	}
	s.put("index/releases.json", body)
}

// checksumLine is one sha256sum output line for data named name.
func checksumLine(data []byte, name string) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]) + "  " + name + "\n"
}
