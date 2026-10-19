package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"

	"aead.dev/minisign"

	"github.com/darkraise/darkpanel/internal/update/trustedkeys"
)

const templatePath = "../../install.sh.in"

func readTemplate(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile(templatePath)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// signer is a minisign key pair generated for one test.
type signer struct {
	key  trustedkeys.Key
	priv minisign.PrivateKey
}

func newSigner(t *testing.T) signer {
	t.Helper()
	pub, priv, err := minisign.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return signer{trustedkeys.Key{ID: fmt.Sprintf("%016X", pub.ID()), PublicKey: pub.String()}, priv}
}

// sign writes dir/checksums.txt.<id>.minisig over dir/checksums.txt in hashed
// mode with the release trusted comment.
func (s signer) sign(t *testing.T, dir, comment string) {
	t.Helper()
	msg := readFile(t, filepath.Join(dir, "checksums.txt"))
	r := minisign.NewReader(strings.NewReader(msg))
	if _, err := io.Copy(io.Discard, r); err != nil {
		t.Fatal(err)
	}
	sig := r.SignWithComments(s.priv, comment, "signature from darkpanel test key")
	writeFile(t, filepath.Join(dir, "checksums.txt."+s.key.ID+".minisig"), string(sig))
}

// script writes install.sh generated with keys and returns its path.
func script(t *testing.T, keys []trustedkeys.Key, base string) string {
	t.Helper()
	b, err := Generate(readTemplate(t), keys, base)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "install.sh")
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func needTools(t *testing.T, tools ...string) {
	t.Helper()
	for _, tool := range tools {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not installed", tool)
		}
	}
}

var keysLine = regexp.MustCompile(`(?m)^KEYS='([^']*)'$`)

func TestGenerateEmbedsTheTrustedKeys(t *testing.T) {
	out, err := Generate(readTemplate(t), trustedkeys.Keys(), trustedkeys.BaseURL())
	if err != nil {
		t.Fatal(err)
	}
	m := keysLine.FindSubmatch(out)
	if m == nil {
		t.Fatal("install.sh has no KEYS line")
	}
	var want []string
	for _, k := range trustedkeys.Keys() {
		w, err := keyWord(k)
		if err != nil {
			t.Fatal(err)
		}
		want = append(want, w)
	}
	if got := strings.Fields(string(m[1])); !slices.Equal(got, want) {
		t.Errorf("embedded keys = %v, want %v", got, want)
	}
	if !bytes.Contains(out, []byte("DEFAULT_BASE_URL='"+trustedkeys.BaseURL()+"'")) {
		t.Errorf("install.sh does not embed the base URL %s", trustedkeys.BaseURL())
	}
	if bytes.Contains(out, []byte("@DARKPANEL_")) {
		t.Error("install.sh still holds a placeholder")
	}
	again, err := Generate(readTemplate(t), trustedkeys.Keys(), trustedkeys.BaseURL())
	if err != nil || !bytes.Equal(out, again) {
		t.Error("Generate is not deterministic")
	}
}

func TestGenerateRefuses(t *testing.T) {
	good := newSigner(t).key
	tests := []struct {
		name     string
		template string
		key      trustedkeys.Key
		base     string
	}{
		{"no keys placeholder", "B='@DARKPANEL_BASE_URL@'", good, "https://example.com"},
		{"no base placeholder", "K='@DARKPANEL_KEYS@'", good, "https://example.com"},
		{"id mismatch", "K='@DARKPANEL_KEYS@' B='@DARKPANEL_BASE_URL@'", trustedkeys.Key{ID: "0000000000000000", PublicKey: good.PublicKey}, "https://example.com"},
		{"not a key", "K='@DARKPANEL_KEYS@' B='@DARKPANEL_BASE_URL@'", trustedkeys.Key{ID: good.ID, PublicKey: "bm90IGEga2V5"}, "https://example.com"},
		{"quote in base", "K='@DARKPANEL_KEYS@' B='@DARKPANEL_BASE_URL@'", good, "https://example.com/'x"},
		{"not http", "K='@DARKPANEL_KEYS@' B='@DARKPANEL_BASE_URL@'", good, "ftp://example.com"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Generate([]byte(tt.template), []trustedkeys.Key{tt.key}, tt.base); err == nil {
				t.Error("Generate succeeded")
			}
		})
	}
}

func TestScriptSyntax(t *testing.T) {
	p := script(t, []trustedkeys.Key{newSigner(t).key}, "https://example.com/releases")
	for _, sh := range []string{"sh", "dash"} {
		if _, err := exec.LookPath(sh); err != nil {
			continue
		}
		if out, err := exec.Command(sh, "-n", p).CombinedOutput(); err != nil { //nolint:gosec // G204: a shell from a fixed list and a test file
			t.Errorf("%s -n: %v\n%s", sh, err, out)
		}
	}
	if _, err := exec.LookPath("shellcheck"); err != nil {
		t.Log("shellcheck is not installed; skipping the shellcheck run")
		return
	}
	if out, err := exec.Command("shellcheck", "-s", "sh", p).CombinedOutput(); err != nil { //nolint:gosec // G204: a test file
		t.Errorf("shellcheck: %v\n%s", err, out)
	}
}

// verify runs the script's verify_release on dir for version and reports
// whether it accepted the release.
func verify(t *testing.T, scriptPath, dir, version string) bool {
	t.Helper()
	cmd := exec.Command("sh", "-c", `. "$1"; verify_release "$2" "$3"`, "sh", scriptPath, dir, version) //nolint:gosec // G204: a fixed command over test files
	cmd.Env = append(os.Environ(), "DARKPANEL_INSTALL_SH_LIB=1")
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if err != nil && !errors.As(err, &exit) {
		t.Fatalf("run verify_release: %v\n%s", err, out)
	}
	return err == nil
}

func TestVerifyRelease(t *testing.T) {
	needTools(t, "sh", "openssl", "base64", "od")
	const comment = "version=1.4.0 channel=stable purpose=checksums"
	tests := []struct {
		name   string
		setup  func(t *testing.T, dir string, a, b signer)
		keys   func(a, b signer) []trustedkeys.Key
		accept bool
	}{
		{"valid", func(t *testing.T, dir string, a, _ signer) { a.sign(t, dir, comment) }, oneKey, true},
		{"beta channel", func(t *testing.T, dir string, a, _ signer) {
			a.sign(t, dir, "version=1.4.0 channel=beta purpose=checksums")
		}, oneKey, true},
		{"other version", func(t *testing.T, dir string, a, _ signer) {
			a.sign(t, dir, "version=1.3.0 channel=stable purpose=checksums")
		}, oneKey, false},
		{"other purpose", func(t *testing.T, dir string, a, _ signer) {
			a.sign(t, dir, "version=1.4.0 channel=stable purpose=other")
		}, oneKey, false},
		{"only an unknown key signed", func(t *testing.T, dir string, _, b signer) { b.sign(t, dir, comment) }, oneKey, false},
		{"unknown key signed under a trusted id", func(t *testing.T, dir string, a, b signer) {
			b.sign(t, dir, comment)
			rename(t, filepath.Join(dir, "checksums.txt."+b.key.ID+".minisig"), filepath.Join(dir, "checksums.txt."+a.key.ID+".minisig"))
		}, oneKey, false},
		{"tampered checksums", func(t *testing.T, dir string, a, _ signer) {
			a.sign(t, dir, comment)
			appendFile(t, filepath.Join(dir, "checksums.txt"), "0000  evil.tar.gz\n")
		}, oneKey, false},
		{"bad global signature", func(t *testing.T, dir string, a, _ signer) {
			a.sign(t, dir, comment)
			p := filepath.Join(dir, "checksums.txt."+a.key.ID+".minisig")
			lines := strings.Split(readFile(t, p), "\n")
			lines[3] = strings.Repeat("A", 86) + "=="
			writeFile(t, p, strings.Join(lines, "\n"))
		}, oneKey, false},
		{"no signature files", func(*testing.T, string, signer, signer) {}, oneKey, false},
		{"second of two keys signed", func(t *testing.T, dir string, _, b signer) { b.sign(t, dir, comment) }, twoKeys, true},
		{"both keys signed", func(t *testing.T, dir string, a, b signer) {
			a.sign(t, dir, comment)
			b.sign(t, dir, comment)
		}, twoKeys, true},
		{"no keys embedded", func(t *testing.T, dir string, a, _ signer) { a.sign(t, dir, comment) }, noKeys, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, b := newSigner(t), newSigner(t)
			dir := t.TempDir()
			writeFile(t, filepath.Join(dir, "checksums.txt"), "abc  darkpanel_1.4.0_linux_amd64.tar.gz\n")
			tt.setup(t, dir, a, b)
			if got := verify(t, script(t, tt.keys(a, b), "https://example.com"), dir, "1.4.0"); got != tt.accept {
				t.Errorf("verify_release accepted = %v, want %v", got, tt.accept)
			}
		})
	}
}

func oneKey(a, _ signer) []trustedkeys.Key  { return []trustedkeys.Key{a.key} }
func twoKeys(a, b signer) []trustedkeys.Key { return []trustedkeys.Key{a.key, b.key} }
func noKeys(signer, signer) []trustedkeys.Key {
	return nil
}

func TestInstallAgentEndToEnd(t *testing.T) {
	needTools(t, "sh", "openssl", "base64", "od", "sha256sum", "tar", "stat")
	if _, err := exec.LookPath("curl"); err != nil {
		needTools(t, "wget")
	}
	if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		t.Skipf("install.sh supports amd64 and arm64, not %s", runtime.GOARCH)
	}
	const version = "1.10.0"
	s := newSigner(t)
	stub := "#!/bin/sh\nprintf '%s\\n' \"$@\" >\"$DARKPANEL_STUB_LOG.args\"\ncat >\"$DARKPANEL_STUB_LOG.stdin\"\n"
	tarball := "darkpanel_" + version + "_linux_" + runtime.GOARCH + ".tar.gz"
	release := t.TempDir()
	writeFile(t, filepath.Join(release, tarball), string(tarGz(t, map[string]string{"darkpanel": stub, "LICENSE": "license\n", "README.md": "readme\n"})))
	sum := sha256.Sum256([]byte(readFile(t, filepath.Join(release, tarball))))
	// The other architecture's tarball and the SBOM are listed but never fetched.
	writeFile(t, filepath.Join(release, "checksums.txt"), hex.EncodeToString(sum[:])+"  "+tarball+"\n"+
		strings.Repeat("0", 64)+"  darkpanel_"+version+"_linux_other.tar.gz\n"+
		strings.Repeat("1", 64)+"  darkpanel_"+version+".cdx.json\n")
	s.sign(t, release, "version="+version+" channel=stable purpose=checksums")
	index := `{"formatVersion": 1, "releases": [
  {"version": "1.11.0-beta.1", "channel": "beta"},
  {"version": "1.2.0", "channel": "stable"},
  {"version": "1.10.0", "channel": "stable"}
]}`
	mux := http.NewServeMux()
	mux.HandleFunc("GET /index/releases.json", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(index)) })
	mux.Handle("GET /v"+version+"/", http.StripPrefix("/v"+version+"/", http.FileServer(http.Dir(release))))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	root := t.TempDir()
	writeFile(t, filepath.Join(root, "etc", "os-release"), "ID=debian\nVERSION_ID=\"12\"\nPRETTY_NAME=\"Debian GNU/Linux 12 (bookworm)\"\n")
	tokenFile := filepath.Join(root, "token")
	writeFile(t, tokenFile, "dpe1.example-token\n")
	logBase := filepath.Join(root, "stub")
	run := func(args ...string) ([]byte, error) {
		cmd := exec.Command("sh", append([]string{script(t, []trustedkeys.Key{s.key}, srv.URL)}, args...)...) //nolint:gosec // G204: the generated test script
		cmd.Env = append(os.Environ(), "DARKPANEL_INSTALL_ROOT="+root, "DARKPANEL_STUB_LOG="+logBase)
		return cmd.CombinedOutput()
	}
	if out, err := run("--role", "agent", "--token-file", tokenFile, "--base-url", srv.URL); err != nil {
		t.Fatalf("install.sh: %v\n%s", err, out)
	}
	bin := filepath.Join(root, "usr", "local", "lib", "darkpanel", "darkpanel")
	if got := readFile(t, bin); got != stub {
		t.Errorf("installed binary = %q, want the stub", got)
	}
	if info, err := os.Stat(bin); err != nil || info.Mode().Perm() != 0o755 {
		t.Errorf("installed binary mode = %v, %v; want 0755", info, err)
	}
	if target, err := os.Readlink(filepath.Join(root, "usr", "local", "bin", "darkpanel")); err != nil || target != bin {
		t.Errorf("link = %q, %v; want %s", target, err, bin)
	}
	if got := readFile(t, logBase+".args"); got != "system\ninstall\n--role\nagent\n" {
		t.Errorf("darkpanel ran with %q, want system install --role agent", got)
	}
	if got := readFile(t, logBase+".stdin"); got != "dpe1.example-token\n" {
		t.Errorf("darkpanel read %q on standard input, want the token", got)
	}

	// With saved credentials and no token file, darkpanel gets an empty
	// standard input and the operator is told how to recover a revoked agent.
	writeFile(t, filepath.Join(root, "etc", "darkpanel", "agent", "current", "agent.crt"), "cert\n")
	out, err := run("--role", "agent")
	if err != nil {
		t.Fatalf("install.sh with saved credentials: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "rerun install.sh with --token-file") {
		t.Errorf("output %q does not say how to recover a revoked agent", out)
	}
	if got := readFile(t, logBase+".stdin"); got != "" {
		t.Errorf("darkpanel read %q on standard input, want nothing", got)
	}

	if err := os.Chmod(tokenFile, 0o644); err != nil { //nolint:gosec // G302: the test needs a token file others can read
		t.Fatal(err)
	}
	// A bad token file is refused before anything is downloaded.
	srv.Close()
	if out, err := run("--role", "agent", "--token-file", tokenFile); err == nil || !strings.Contains(string(out), "mode 0600 or 0400, not 0644") {
		t.Errorf("a world-readable token file: err %v, output %s", err, out)
	}
	writeFile(t, filepath.Join(root, "etc", "os-release"), "ID=ubuntu\nVERSION_ID=\"20.04\"\nPRETTY_NAME=\"Ubuntu 20.04.6 LTS\"\n")
	if out, err := run("--role", "controller"); err == nil || !strings.Contains(string(out), "this host is Ubuntu 20.04.6 LTS") {
		t.Errorf("an unsupported OS: err %v, output %s", err, out)
	}
}

func tarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(files[name])), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(files[name])); err != nil {
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

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p) //nolint:gosec // G304: a test file
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func writeFile(t *testing.T, p, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
		t.Fatal(err)
	}
}

func appendFile(t *testing.T, p, s string) {
	t.Helper()
	writeFile(t, p, readFile(t, p)+s)
}

func rename(t *testing.T, from, to string) {
	t.Helper()
	if err := os.Rename(from, to); err != nil {
		t.Fatal(err)
	}
}
