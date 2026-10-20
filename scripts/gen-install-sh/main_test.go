package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

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
	writeFile(t, s.sigPath(dir), string(sig))
}

func (s signer) sigPath(dir string) string {
	return filepath.Join(dir, "checksums.txt."+s.key.ID+".minisig")
}

// patchSignature rewrites the decoded signature line of the signature file p.
// The bytes after the algorithm and key id still verify, so a refusal proves
// the script checks the patched field itself.
func patchSignature(t *testing.T, p string, patch func(sig []byte)) {
	t.Helper()
	lines := strings.Split(readFile(t, p), "\n")
	sig, err := base64.StdEncoding.DecodeString(lines[1])
	if err != nil {
		t.Fatal(err)
	}
	patch(sig)
	lines[1] = base64.StdEncoding.EncodeToString(sig)
	writeFile(t, p, strings.Join(lines, "\n"))
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

func TestCheckAddress(t *testing.T) {
	needTools(t, "sh")
	p := script(t, []trustedkeys.Key{newSigner(t).key}, "https://example.com")
	tests := []struct {
		addr string
		ok   bool
	}{
		{"0.0.0.0:7443", true},
		{"[::1]:7443", true},
		{"host.example:7443", true},
		{"", false},
		{"0.0.0.0:7443\n--foo bar", false},
		{"a b:1", false},
		{"a;b:1", false},
		{"*:1", false},
	}
	for _, sh := range []string{"sh", "dash"} {
		if _, err := exec.LookPath(sh); err != nil {
			continue
		}
		for _, tt := range tests {
			t.Run(fmt.Sprintf("%s %q", sh, tt.addr), func(t *testing.T) {
				cmd := exec.Command(sh, "-c", `. "$1"; check_address "$2"`, sh, p, tt.addr) //nolint:gosec // G204: a shell from a fixed list and a test file
				cmd.Env = append(os.Environ(), "DARKPANEL_INSTALL_SH_LIB=1")
				out, err := cmd.CombinedOutput()
				if tt.ok {
					if err != nil {
						t.Errorf("check_address refused %q: %v\n%s", tt.addr, err, out)
					}
					return
				}
				if err == nil || !strings.Contains(string(out), "is not host:port") {
					t.Errorf("check_address(%q): err %v, output %q; want a refusal saying \"is not host:port\"", tt.addr, err, out)
				}
			})
		}
	}
}

func TestNewestStable(t *testing.T) {
	needTools(t, "sh")
	p := script(t, []trustedkeys.Key{newSigner(t).key}, "https://example.com")
	tests := []struct {
		name, index, want string
	}{
		{"pretty printed", "{\"formatVersion\": 1, \"releases\": [\n  {\"version\": \"1.2.0\", \"channel\": \"stable\"},\n  {\"version\": \"1.10.0\", \"channel\": \"stable\"}\n]}\n", "1.10.0"},
		{"compact", `{"formatVersion":1,"releases":[{"version":"1.2.0","channel":"stable"},{"version":"1.3.0","channel":"stable"}]}`, "1.3.0"},
		{"sorted keys", `{"formatVersion":1,"releases":[{"channel":"stable","version":"1.2.0"},{"channel":"beta","version":"1.11.0-beta.1"},{"channel":"stable","version":"1.10.0"}]}`, "1.10.0"},
		{"extra members", `{"formatVersion":1,"releases":[{"date":"2026-10-01","version":"1.2.0","channel":"stable","notes":"x"},{"version":"1.4.0","size":3,"channel":"stable"}]}`, "1.4.0"},
		{"betas and prereleases are skipped", `{"formatVersion":1,"releases":[{"version":"1.5.0","channel":"beta"},{"version":"1.3.0-rc.1","channel":"stable"},{"version":"1.2.0","channel":"stable"}]}`, "1.2.0"},
		{"no stable release", `{"formatVersion":1,"releases":[{"version":"1.5.0","channel":"beta"}]}`, ""},
		{"empty list", `{"formatVersion":1,"releases":[]}`, ""},
	}
	for _, sh := range []string{"sh", "dash"} {
		if _, err := exec.LookPath(sh); err != nil {
			continue
		}
		for _, tt := range tests {
			t.Run(sh+" "+tt.name, func(t *testing.T) {
				index := filepath.Join(t.TempDir(), "releases.json")
				writeFile(t, index, tt.index)
				cmd := exec.Command(sh, "-c", `. "$1"; newest_stable "$2"`, sh, p, index) //nolint:gosec // G204: a shell from a fixed list and test files
				cmd.Env = append(os.Environ(), "DARKPANEL_INSTALL_SH_LIB=1")
				out, err := cmd.Output()
				if err != nil {
					t.Fatalf("newest_stable: %v", err)
				}
				if got := strings.TrimSpace(string(out)); got != tt.want {
					t.Errorf("newest_stable = %q, want %q", got, tt.want)
				}
			})
		}
	}
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
			p := a.sigPath(dir)
			lines := strings.Split(readFile(t, p), "\n")
			lines[3] = strings.Repeat("A", 86) + "=="
			writeFile(t, p, strings.Join(lines, "\n"))
		}, oneKey, false},
		{"legacy Ed signature", func(t *testing.T, dir string, a, _ signer) {
			a.sign(t, dir, comment)
			patchSignature(t, a.sigPath(dir), func(sig []byte) { sig[1] = 'd' })
		}, oneKey, false},
		{"signature key id patched", func(t *testing.T, dir string, a, _ signer) {
			a.sign(t, dir, comment)
			patchSignature(t, a.sigPath(dir), func(sig []byte) {
				binary.LittleEndian.PutUint64(sig[2:10], ^binary.LittleEndian.Uint64(sig[2:10]))
			})
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

func TestVerifyReleaseWithoutATemporaryDirectory(t *testing.T) {
	needTools(t, "sh", "openssl")
	s := newSigner(t)
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "checksums.txt"), "abc  darkpanel_1.4.0_linux_amd64.tar.gz\n")
	s.sign(t, dir, "version=1.4.0 channel=stable purpose=checksums")
	cmd := exec.Command("sh", "-c", `. "$1"; verify_release "$2" "$3"`, "sh", script(t, []trustedkeys.Key{s.key}, "https://example.com"), dir, "1.4.0") //nolint:gosec // G204: a fixed command over test files
	cmd.Env = append(os.Environ(), "DARKPANEL_INSTALL_SH_LIB=1", "TMPDIR="+filepath.Join(t.TempDir(), "missing"))
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "cannot create a temporary directory") {
		t.Errorf("err %v, output %q; want the temporary directory refusal", err, out)
	}
}

func oneKey(a, _ signer) []trustedkeys.Key  { return []trustedkeys.Key{a.key} }
func twoKeys(a, b signer) []trustedkeys.Key { return []trustedkeys.Key{a.key, b.key} }
func noKeys(signer, signer) []trustedkeys.Key {
	return nil
}

// fixture is a signed release of version fixtureVersion served over HTTP,
// an install root holding a supported os-release, and a stub darkpanel that
// logs its arguments and standard input under logBase.
type fixture struct {
	script  string
	root    string
	release string
	tarball string
	logBase string
	srv     *httptest.Server
	// tarballRequests counts requests for any tarball.
	tarballRequests atomic.Int32
	// onTarball, when set, runs before a tarball is served. It is set while
	// the server runs, and the race detector cannot see the ordering that
	// install.sh's request gives it.
	onTarball atomic.Pointer[func(*http.Request)]
}

const (
	fixtureVersion = "1.10.0"
	fixtureStub    = "#!/bin/sh\nprintf '%s\\n' \"$@\" >\"$DARKPANEL_STUB_LOG.args\"\ncat >\"$DARKPANEL_STUB_LOG.stdin\"\n"
)

func newFixture(t *testing.T) *fixture {
	t.Helper()
	needTools(t, "sh", "openssl", "base64", "od", "sha256sum", "tar", "stat")
	if _, err := exec.LookPath("curl"); err != nil {
		needTools(t, "wget")
	}
	if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		t.Skipf("install.sh supports amd64 and arm64, not %s", runtime.GOARCH)
	}
	f := &fixture{
		release: t.TempDir(),
		root:    t.TempDir(),
		tarball: "darkpanel_" + fixtureVersion + "_linux_" + runtime.GOARCH + ".tar.gz",
	}
	s := newSigner(t)
	writeFile(t, filepath.Join(f.release, f.tarball), string(tarGz(t, map[string]string{"darkpanel": fixtureStub, "LICENSE": "license\n", "README.md": "readme\n"})))
	sum := sha256.Sum256([]byte(readFile(t, filepath.Join(f.release, f.tarball))))
	// The other architecture's tarball and the SBOM are listed but never fetched.
	writeFile(t, filepath.Join(f.release, "checksums.txt"), hex.EncodeToString(sum[:])+"  "+f.tarball+"\n"+
		strings.Repeat("0", 64)+"  darkpanel_"+fixtureVersion+"_linux_other.tar.gz\n"+
		strings.Repeat("1", 64)+"  darkpanel_"+fixtureVersion+".cdx.json\n")
	s.sign(t, f.release, "version="+fixtureVersion+" channel=stable purpose=checksums")
	index := `{"formatVersion": 1, "releases": [
  {"version": "1.11.0-beta.1", "channel": "beta"},
  {"version": "1.2.0", "channel": "stable"},
  {"version": "1.10.0", "channel": "stable"}
]}`
	mux := http.NewServeMux()
	mux.HandleFunc("GET /index/releases.json", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(index)) })
	mux.Handle("GET /v"+fixtureVersion+"/", http.StripPrefix("/v"+fixtureVersion+"/", http.FileServer(http.Dir(f.release))))
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".tar.gz") {
			f.tarballRequests.Add(1)
			if on := f.onTarball.Load(); on != nil {
				(*on)(r)
			}
		}
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(f.srv.Close)
	f.script = script(t, []trustedkeys.Key{s.key}, f.srv.URL)
	writeFile(t, filepath.Join(f.root, "etc", "os-release"), "ID=debian\nVERSION_ID=\"12\"\nPRETTY_NAME=\"Debian GNU/Linux 12 (bookworm)\"\n")
	f.logBase = filepath.Join(f.root, "stub")
	return f
}

// command is install.sh with args against the fixture; env adds variables.
func (f *fixture) command(env []string, args ...string) *exec.Cmd {
	cmd := exec.Command("sh", append([]string{f.script}, args...)...) //nolint:gosec // G204: the generated test script
	cmd.Env = append(append(os.Environ(), "DARKPANEL_INSTALL_ROOT="+f.root, "DARKPANEL_STUB_LOG="+f.logBase), env...)
	return cmd
}

func (f *fixture) run(args ...string) ([]byte, error) {
	return f.command(nil, args...).CombinedOutput()
}

func (f *fixture) bin() string {
	return filepath.Join(f.root, "usr", "local", "lib", "darkpanel", "darkpanel")
}

// tokenFile writes content to a token file of mode 0600 and returns its path.
func (f *fixture) tokenFile(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "token")
	writeFile(t, p, content)
	return p
}

func TestInstallAgentEndToEnd(t *testing.T) {
	f := newFixture(t)
	tokenFile := f.tokenFile(t, "dpe1.example-token\n")
	if out, err := f.run("--role", "agent", "--token-file", tokenFile, "--base-url", f.srv.URL); err != nil {
		t.Fatalf("install.sh: %v\n%s", err, out)
	}
	if got := readFile(t, f.bin()); got != fixtureStub {
		t.Errorf("installed binary = %q, want the stub", got)
	}
	if info, err := os.Stat(f.bin()); err != nil || info.Mode().Perm() != 0o755 {
		t.Errorf("installed binary mode = %v, %v; want 0755", info, err)
	}
	if target, err := os.Readlink(filepath.Join(f.root, "usr", "local", "bin", "darkpanel")); err != nil || target != f.bin() {
		t.Errorf("link = %q, %v; want %s", target, err, f.bin())
	}
	if got := readFile(t, f.logBase+".args"); got != "system\ninstall\n--role\nagent\n" {
		t.Errorf("darkpanel ran with %q, want system install --role agent", got)
	}
	if got := readFile(t, f.logBase+".stdin"); got != "dpe1.example-token\n" {
		t.Errorf("darkpanel read %q on standard input, want the token", got)
	}

	// A version with a second line is refused before any tarball is fetched.
	fetched := f.tarballRequests.Load()
	if out, err := f.run("--role", "controller", "--version", "1.0.0\nx", "--base-url", f.srv.URL); err == nil || !strings.Contains(string(out), "is not a semantic version") {
		t.Errorf("a multi-line version: err %v, output %s", err, out)
	}
	if got := f.tarballRequests.Load(); got != fetched {
		t.Errorf("a multi-line version fetched %d tarballs, want none", got-fetched)
	}

	// With saved credentials and no token file, darkpanel gets an empty
	// standard input and the operator is told how to recover a revoked agent.
	writeFile(t, filepath.Join(f.root, "etc", "darkpanel", "agent", "current", "agent.crt"), "cert\n")
	out, err := f.run("--role", "agent")
	if err != nil {
		t.Fatalf("install.sh with saved credentials: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "rerun install.sh with --token-file") {
		t.Errorf("output %q does not say how to recover a revoked agent", out)
	}
	if got := readFile(t, f.logBase+".stdin"); got != "" {
		t.Errorf("darkpanel read %q on standard input, want nothing", got)
	}

	writeFile(t, filepath.Join(f.root, "etc", "os-release"), "ID=ubuntu\nVERSION_ID=\"20.04\"\nPRETTY_NAME=\"Ubuntu 20.04.6 LTS\"\n")
	if out, err := f.run("--role", "controller"); err == nil || !strings.Contains(string(out), "this host is Ubuntu 20.04.6 LTS") {
		t.Errorf("an unsupported OS: err %v, output %s", err, out)
	}
}

func TestInstallControllerPassesListenArgs(t *testing.T) {
	f := newFixture(t)
	if out, err := f.run("--role", "controller", "--agent-listen", "0.0.0.0:7443", "--agent-listen", "[::1]:7443", "--agent-advertise", "panel.example:7443"); err != nil {
		t.Fatalf("install.sh: %v\n%s", err, out)
	}
	want := "system\ninstall\n--role\ncontroller\n--agent-listen\n0.0.0.0:7443\n--agent-listen\n[::1]:7443\n--agent-advertise\npanel.example:7443\n"
	if got := readFile(t, f.logBase+".args"); got != want {
		t.Errorf("darkpanel ran with %q, want %q", got, want)
	}
	if got := readFile(t, f.logBase+".stdin"); got != "" {
		t.Errorf("darkpanel read %q on standard input, want nothing", got)
	}
}

func TestInstallRefusesAChecksumMismatch(t *testing.T) {
	f := newFixture(t)
	writeFile(t, filepath.Join(f.release, f.tarball), string(tarGz(t, map[string]string{"darkpanel": "#!/bin/sh\necho evil\n"})))
	out, err := f.run("--role", "controller")
	if err == nil || !strings.Contains(string(out), f.tarball+" does not match checksums.txt") {
		t.Errorf("a tampered tarball: err %v, output %s", err, out)
	}
	if _, err := os.Stat(f.bin()); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a tampered tarball was installed: stat %v", err)
	}
}

func TestInstallRefusesABadTokenFile(t *testing.T) {
	f := newFixture(t)
	missing := filepath.Join(t.TempDir(), "missing")
	worldReadable := f.tokenFile(t, "dpe1.x\n")
	if err := os.Chmod(worldReadable, 0o644); err != nil { //nolint:gosec // G302: the test needs a token file others can read
		t.Fatal(err)
	}
	tests := []struct {
		name, path, want string
	}{
		{"missing", missing, "token file " + missing + " does not exist or cannot be read"},
		{"a directory", t.TempDir(), "does not exist or cannot be read"},
		{"empty", f.tokenFile(t, ""), "is empty"},
		{"world readable", worldReadable, "mode 0600 or 0400, not 0644"},
	}
	if os.Geteuid() == 0 {
		otherOwner := f.tokenFile(t, "dpe1.x\n")
		if err := os.Chown(otherOwner, 65534, 65534); err != nil {
			t.Fatal(err)
		}
		tests = append(tests, struct{ name, path, want string }{"owned by another user", otherOwner, "must be owned by uid 0, the caller, not uid 65534"})
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := f.run("--role", "agent", "--token-file", tt.path)
			if err == nil || !strings.Contains(string(out), tt.want) {
				t.Errorf("err %v, output %q; want a refusal containing %q", err, out, tt.want)
			}
		})
	}
	// Every refusal comes before anything is downloaded.
	if got := f.tarballRequests.Load(); got != 0 {
		t.Errorf("bad token files fetched %d tarballs, want none", got)
	}
}

func TestInstallWithoutATerminal(t *testing.T) {
	f := newFixture(t)
	cmd := f.command(nil, "--role", "agent")
	// A new session has no controlling terminal, whatever runs the test.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "no terminal to read the enrollment token from; pass --token-file") {
		t.Errorf("err %v, output %q; want the no-terminal refusal", err, out)
	}
	if strings.Contains(string(out), "/dev/tty") {
		t.Errorf("output %q holds a raw shell error", out)
	}
}

func TestInstallWithoutATemporaryDirectory(t *testing.T) {
	f := newFixture(t)
	out, err := f.command([]string{"TMPDIR=" + filepath.Join(t.TempDir(), "missing")}, "--role", "controller").CombinedOutput()
	if err == nil || !strings.Contains(string(out), "cannot create a temporary directory") {
		t.Errorf("err %v, output %q; want the temporary directory refusal", err, out)
	}
}

func TestInstallCleansUpOnInterrupt(t *testing.T) {
	f := newFixture(t)
	tmp := t.TempDir()
	requested := make(chan struct{})
	hold := func(r *http.Request) {
		close(requested)
		select {
		case <-r.Context().Done():
		case <-time.After(30 * time.Second):
		}
	}
	f.onTarball.Store(&hold)
	cmd := f.command([]string{"TMPDIR=" + tmp}, "--role", "controller")
	// The interrupt goes to the whole group, as a terminal's Ctrl-C does.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-requested:
	case <-time.After(30 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("install.sh never requested the tarball")
	}
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	err := cmd.Wait()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 130 {
		t.Errorf("install.sh after SIGINT: %v, want exit status 130", err)
	}
	entries, err := os.ReadDir(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("install.sh left %v in its temporary directory", entries)
	}
}

func TestCommittedScriptIsCurrent(t *testing.T) {
	if trustedkeys.TestBuild() {
		t.Skip("the committed install.sh embeds the production keys")
	}
	want, err := Generate(readTemplate(t), trustedkeys.Keys(), trustedkeys.BaseURL())
	if err != nil {
		t.Fatal(err)
	}
	const committed = "../../install.sh"
	if got := readFile(t, committed); got != string(want) {
		t.Error("install.sh differs from install.sh.in rendered with the trusted keys; run make install-sh")
	}
	if info, err := os.Stat(committed); err != nil || info.Mode().Perm() != 0o755 {
		t.Errorf("install.sh mode = %v, %v; want 0755", info, err)
	}
}

// TestSignedReleaseVerifies signs with release/sign.sh, the real minisign
// command and the harness key, so the release workflow and install.sh are
// proven to agree on the signature format rather than each against its own
// fixture.
func TestSignedReleaseVerifies(t *testing.T) {
	needTools(t, "bash", "go", "sh", "openssl", "base64", "od")
	work := t.TempDir()
	minisignBin := filepath.Join(work, "minisign")
	if out, err := exec.Command("go", "build", "-o", minisignBin, "aead.dev/minisign/cmd/minisign").CombinedOutput(); err != nil { //nolint:gosec // G204: a fixed build of a required module
		t.Fatalf("build minisign: %v\n%s", err, out)
	}
	keyText := readFile(t, "../../internal/update/trustedkeys/testdata/harness.key")
	var priv minisign.PrivateKey
	if err := priv.UnmarshalText([]byte(keyText)); err != nil {
		t.Fatalf("harness.key: %v", err)
	}
	pub, ok := priv.Public().(minisign.PublicKey)
	if !ok {
		t.Fatalf("public key of type %T", priv.Public())
	}
	key := trustedkeys.Key{ID: fmt.Sprintf("%016X", pub.ID()), PublicKey: pub.String()}
	const version = "1.4.0"
	rel := filepath.Join(work, "rel")
	writeFile(t, filepath.Join(rel, "checksums.txt"), "abc  darkpanel_"+version+"_linux_amd64.tar.gz\n")
	info := filepath.Join(work, "version.json")
	writeFile(t, info, `{"version":"`+version+`","testBuild":false,"trustedKeys":["`+key.ID+`"]}`+"\n")

	cmd := exec.Command("bash", "../../release/sign.sh", version, "stable", rel, info) //nolint:gosec // G204: the repository's signing script over test files
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "MINISIGN") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	// The harness key is not encrypted, so minisign reads no password.
	cmd.Env = append(cmd.Env, "MINISIGN="+minisignBin, "MINISIGN_KEY_1="+keyText)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("sign.sh: %v\n%s", err, out)
	}
	if !verify(t, script(t, []trustedkeys.Key{key}, "https://example.com"), rel, version) {
		t.Error("verify_release refused a release signed by sign.sh")
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
