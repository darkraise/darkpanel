package main

import (
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/modules/derp/artifact"
)

func TestParseVersion(t *testing.T) {
	for _, s := range []string{"0.1.0", "1.2.3", "1.3.0-beta.1", "1.0.0-rc.1.2", "1.0.0-x-y.7"} {
		if _, err := parseVersion(s); err != nil {
			t.Errorf("parseVersion(%q) = %v, want nil", s, err)
		}
	}
	for _, s := range []string{"", "1.2", "v1.2.3", "01.2.3", "1.2.3-", "1.2.3-01", "1.2.3+build.1", "1.2.3-beta..1"} {
		if _, err := parseVersion(s); err == nil {
			t.Errorf("parseVersion(%q) = nil, want an error", s)
		}
	}
}

func TestCompareOrder(t *testing.T) {
	// Ascending precedence: the semver.org section 11 example, then minor and
	// major steps where string order and numeric order disagree.
	order := []string{
		"1.0.0-alpha", "1.0.0-alpha.1", "1.0.0-alpha.beta", "1.0.0-beta",
		"1.0.0-beta.2", "1.0.0-beta.11", "1.0.0-rc.1", "1.0.0",
		"1.3.0-beta.1", "1.3.0", "1.10.0", "2.0.0",
	}
	vs := make([]version, len(order))
	for i, s := range order {
		v, err := parseVersion(s)
		if err != nil {
			t.Fatal(err)
		}
		vs[i] = v
	}
	for i := range vs {
		for j := range vs {
			if got, want := compare(vs[i], vs[j]), cmp.Compare(i, j); got != want {
				t.Errorf("compare(%s, %s) = %d, want %d", order[i], order[j], got, want)
			}
		}
	}
}

func TestRunCheckTag(t *testing.T) {
	tests := []struct {
		tag  string
		want string
		ok   bool
	}{
		{"v1.3.0", "version=1.3.0\nchannel=stable\n", true},
		{"v1.3.0-beta.1", "version=1.3.0-beta.1\nchannel=beta\n", true},
		{"1.3.0", "", false},
		{"v1.3", "", false},
		{"index", "", false},
	}
	for _, tt := range tests {
		var out bytes.Buffer
		err := run([]string{"check-tag", tt.tag}, strings.NewReader(""), &out)
		if (err == nil) != tt.ok || out.String() != tt.want {
			t.Errorf("check-tag %q = %q, %v; want %q, ok %v", tt.tag, out.String(), err, tt.want, tt.ok)
		}
	}
}

func TestRunCheckVersion(t *testing.T) {
	if err := run([]string{"check-version", "1.2.3"}, strings.NewReader(""), &bytes.Buffer{}); err != nil {
		t.Errorf("check-version 1.2.3 = %v, want nil", err)
	}
	for _, s := range []string{"v1.2.3", "dev", "1.2"} {
		if err := run([]string{"check-version", s}, strings.NewReader(""), &bytes.Buffer{}); err == nil {
			t.Errorf("check-version %q = nil, want an error", s)
		}
	}
}

func TestRunIndex(t *testing.T) {
	// gh release list order (newest publication first): the rolling index
	// release, a draft, and betas published after the latest stable.
	list := `[
  {"tagName": "index", "isDraft": false},
  {"tagName": "v1.3.0-beta.2", "isDraft": false},
  {"tagName": "v1.4.0", "isDraft": true},
  {"tagName": "v1.3.0-beta.1", "isDraft": false},
  {"tagName": "v1.2.10", "isDraft": false},
  {"tagName": "v1.2.1", "isDraft": false},
  {"tagName": "v1.2.0", "isDraft": false}
]`
	want := `{
  "formatVersion": 1,
  "releases": [
    {
      "version": "1.3.0-beta.2",
      "channel": "beta"
    },
    {
      "version": "1.3.0-beta.1",
      "channel": "beta"
    },
    {
      "version": "1.2.10",
      "channel": "stable"
    },
    {
      "version": "1.2.1",
      "channel": "stable"
    },
    {
      "version": "1.2.0",
      "channel": "stable"
    }
  ]
}
`
	var out bytes.Buffer
	if err := run([]string{"index"}, strings.NewReader(list), &out); err != nil {
		t.Fatalf("index: %v", err)
	}
	// The exact comparison fixes the whole encoding: members, indentation and
	// the trailing newline.
	if out.String() != want {
		t.Errorf("index =\n%s\nwant\n%s", out.String(), want)
	}

	out.Reset()
	if err := run([]string{"index"}, strings.NewReader("[]"), &out); err != nil {
		t.Fatalf("index of no releases: %v", err)
	}
	if want := "{\n  \"formatVersion\": 1,\n  \"releases\": []\n}\n"; out.String() != want {
		t.Errorf("index of no releases = %q, want %q", out.String(), want)
	}

	if err := run([]string{"index"}, strings.NewReader("not json"), &bytes.Buffer{}); err == nil {
		t.Error("index of invalid JSON = nil, want an error")
	}
}

// minisig returns a minisign signature file with the given algorithm bytes
// and key id; the signatures themselves are zero.
func minisig(alg string, id uint64) []byte {
	raw := make([]byte, 74)
	copy(raw, alg)
	binary.LittleEndian.PutUint64(raw[2:10], id)
	return []byte("untrusted comment: signature from minisign secret key\n" +
		base64.StdEncoding.EncodeToString(raw) + "\n" +
		"trusted comment: version=1.2.3 channel=stable purpose=checksums\n" +
		base64.StdEncoding.EncodeToString(make([]byte, 64)) + "\n")
}

func TestKeyID(t *testing.T) {
	tests := []struct {
		name string
		sig  []byte
		want string
	}{
		{"hashed", minisig("ED", 0x0123456789ABCDEF), "0123456789ABCDEF"},
		// minisign prints this id without its leading zeros; file names pad it.
		{"zero padded", minisig("ED", 0xABCDEF), "0000000000ABCDEF"},
	}
	for _, tt := range tests {
		if got, err := keyID(tt.sig); err != nil || got != tt.want {
			t.Errorf("%s: keyID = %q, %v; want %q", tt.name, got, err, tt.want)
		}
	}
	for name, sig := range map[string][]byte{
		"legacy algorithm": minisig("Ed", 1),
		"no comment line":  []byte("not a signature\n"),
		"short signature":  []byte("untrusted comment: x\nAAAA\n"),
		"not base64":       []byte("untrusted comment: x\n!!!!\n"),
	} {
		if _, err := keyID(sig); err == nil {
			t.Errorf("%s: keyID = nil error, want an error", name)
		}
	}
}

func TestRunKeyID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "checksums.txt.minisig")
	if err := os.WriteFile(path, minisig("ED", 0x0123456789ABCDEF), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := run([]string{"keyid", path}, strings.NewReader(""), &out); err != nil {
		t.Fatalf("keyid: %v", err)
	}
	if out.String() != "0123456789ABCDEF\n" {
		t.Errorf("keyid = %q, want %q", out.String(), "0123456789ABCDEF\n")
	}
	if err := run([]string{"keyid", filepath.Join(t.TempDir(), "missing")}, strings.NewReader(""), &out); err == nil {
		t.Error("keyid of a missing file = nil, want an error")
	}
}

func TestCheckBuild(t *testing.T) {
	const a, b = "A000000000000000", "B000000000000000"
	tests := []struct {
		name    string
		info    string
		version string
		keys    []string
		ok      bool
	}{
		{"one key", `{"version":"1.2.3","testBuild":false,"trustedKeys":["A000000000000000"]}`, "1.2.3", []string{a}, true},
		{"rotation in any order", `{"version":"1.2.3","testBuild":false,"trustedKeys":["B000000000000000","A000000000000000"]}`, "1.2.3", []string{a, b}, true},
		{"test build", `{"version":"1.2.3","testBuild":true,"trustedKeys":["A000000000000000"]}`, "1.2.3", []string{a}, false},
		{"no testBuild field", `{"version":"1.2.3","trustedKeys":["A000000000000000"]}`, "1.2.3", []string{a}, false},
		{"binary of another version", `{"version":"1.2.4","testBuild":false,"trustedKeys":["A000000000000000"]}`, "1.2.3", []string{a}, false},
		{"binary of the tag name", `{"version":"v1.2.3","testBuild":false,"trustedKeys":["A000000000000000"]}`, "1.2.3", []string{a}, false},
		{"no version field", `{"testBuild":false,"trustedKeys":["A000000000000000"]}`, "1.2.3", []string{a}, false},
		{"binary trusts an extra key", `{"version":"1.2.3","testBuild":false,"trustedKeys":["A000000000000000","B000000000000000"]}`, "1.2.3", []string{a}, false},
		{"signing key not trusted", `{"version":"1.2.3","testBuild":false,"trustedKeys":["A000000000000000"]}`, "1.2.3", []string{a, b}, false},
		{"no signing keys", `{"version":"1.2.3","testBuild":false,"trustedKeys":[]}`, "1.2.3", nil, false},
		{"not JSON", `darkpanel dev`, "1.2.3", []string{a}, false},
	}
	for _, tt := range tests {
		if err := checkBuild([]byte(tt.info), tt.version, tt.keys); (err == nil) != tt.ok {
			t.Errorf("%s: checkBuild = %v, want ok %v", tt.name, err, tt.ok)
		}
	}
}

func TestRunCheckBuild(t *testing.T) {
	info := `{"version":"1.2.3","testBuild":false,"trustedKeys":["A000000000000000","B000000000000000"]}`
	if err := run([]string{"check-build", "1.2.3", "A000000000000000,B000000000000000"}, strings.NewReader(info), &bytes.Buffer{}); err != nil {
		t.Errorf("check-build of both keys = %v, want nil", err)
	}
	if err := run([]string{"check-build", "1.2.3", ""}, strings.NewReader(info), &bytes.Buffer{}); err == nil {
		t.Error("check-build of no keys = nil, want an error")
	}
	if err := run([]string{"check-build", "1.2.4", "A000000000000000,B000000000000000"}, strings.NewReader(info), &bytes.Buffer{}); err == nil {
		t.Error("check-build of another version = nil, want an error")
	}
}

func TestRunLatest(t *testing.T) {
	// gh release list order: the rolling index release, a draft of a newer
	// line, and betas of the next minor version.
	list := `[
  {"tagName": "index", "isDraft": false},
  {"tagName": "v1.5.0", "isDraft": true},
  {"tagName": "v1.4.0-beta.1", "isDraft": false},
  {"tagName": "v1.3.1", "isDraft": false},
  {"tagName": "v1.3.0", "isDraft": false},
  {"tagName": "v1.2.4", "isDraft": false}
]`
	tests := []struct {
		version string
		want    string
	}{
		{"1.3.2", "true\n"},
		{"1.4.0", "true\n"},
		{"2.0.0", "true\n"},
		{"1.2.5", "false\n"},
		{"1.3.1", "false\n"},
		{"1.4.0-beta.2", "false\n"},
		{"1.6.0-beta.1", "false\n"},
	}
	for _, tt := range tests {
		var out bytes.Buffer
		if err := run([]string{"latest", tt.version}, strings.NewReader(list), &out); err != nil || out.String() != tt.want {
			t.Errorf("latest %s = %q, %v; want %q", tt.version, out.String(), err, tt.want)
		}
	}

	var out bytes.Buffer
	if err := run([]string{"latest", "0.1.0"}, strings.NewReader("[]"), &out); err != nil || out.String() != "true\n" {
		t.Errorf("latest of the first release = %q, %v; want %q", out.String(), err, "true\n")
	}
	if err := run([]string{"latest", "v1.3.2"}, strings.NewReader(list), &bytes.Buffer{}); err == nil {
		t.Error("latest of a tag name = nil, want an error")
	}
	if err := run([]string{"latest", "1.3.2"}, strings.NewReader("not json"), &bytes.Buffer{}); err == nil {
		t.Error("latest of invalid JSON = nil, want an error")
	}
}

func TestRunUsage(t *testing.T) {
	for _, args := range [][]string{nil, {"publish"}, {"check-tag"}, {"index", "extra"}, {"check-build", "A000000000000000"}, {"latest"}} {
		if err := run(args, strings.NewReader(""), &bytes.Buffer{}); err == nil || !strings.HasPrefix(err.Error(), "usage:") {
			t.Errorf("run(%q) = %v, want the usage error", args, err)
		}
	}
}

// drCall is one call of the fake builder.
type drCall struct{ version, arch, dst string }

// drFake returns a builder that writes "derper <arch>" to dst, and the calls it received. An arch in failArch makes it
// fail after the other arch was built.
func drFake(failArch string) (derperBuilder, *[]drCall) {
	var calls []drCall
	build := func(_ context.Context, version, arch, dst string) error {
		calls = append(calls, drCall{version, arch, dst})
		if arch == failArch {
			return errors.New("go build failed")
		}
		return os.WriteFile(dst, []byte("derper "+arch), 0o755) //nolint:gosec // G306: a test binary
	}
	return build, &calls
}

func drSum(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func drName(arch string) string { return artifact.BinaryName(artifact.DerperVersion, arch) }

func drRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path) //nolint:gosec // G304: a path under the test's temporary directory
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func drMissing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("%s exists (stat error %v), want it absent", path, err)
	}
}

func TestDrBuildsEveryArchAndWritesTheManifest(t *testing.T) {
	out := filepath.Join(t.TempDir(), "out")
	build, calls := drFake("")
	var stdout bytes.Buffer
	if err := runDerper(context.Background(), []string{"1.4.0", out}, &stdout, build); err != nil {
		t.Fatalf("runDerper: %v", err)
	}
	wantCalls := []drCall{
		{artifact.DerperVersion, "amd64", filepath.Join(out, drName("amd64"))},
		{artifact.DerperVersion, "arm64", filepath.Join(out, drName("arm64"))},
	}
	if len(*calls) != len(wantCalls) || (*calls)[0] != wantCalls[0] || (*calls)[1] != wantCalls[1] {
		t.Errorf("builder calls %+v, want %+v", *calls, wantCalls)
	}
	m := artifact.Manifest{
		DarkpanelVersion: "1.4.0",
		DerperVersion:    artifact.DerperVersion,
		Artifacts:        map[string]string{"amd64": drSum([]byte("derper amd64")), "arm64": drSum([]byte("derper arm64"))},
	}
	want, err := m.Encode()
	if err != nil {
		t.Fatal(err)
	}
	got := drRead(t, filepath.Join(out, artifact.ManifestName))
	if !bytes.Equal(got, want) {
		t.Errorf("derper.json =\n%s\nwant\n%s", got, want)
	}
	if parsed, err := artifact.ParseManifest(got); err != nil || parsed.Artifacts["amd64"] != m.Artifacts["amd64"] {
		t.Errorf("ParseManifest = %+v, %v", parsed, err)
	}
	wantOut := fmt.Sprintf("%s  %s\n%s  %s\n", m.Artifacts["amd64"], drName("amd64"), m.Artifacts["arm64"], drName("arm64"))
	if stdout.String() != wantOut {
		t.Errorf("stdout = %q, want %q", stdout.String(), wantOut)
	}
	drMissing(t, filepath.Join(out, "checksums.txt"))
}

func TestDrOneArchWritesNoManifest(t *testing.T) {
	out := t.TempDir()
	build, calls := drFake("")
	var stdout bytes.Buffer
	if err := runDerper(context.Background(), []string{"1.4.0", out, "-arch", "arm64"}, &stdout, build); err != nil {
		t.Fatalf("runDerper: %v", err)
	}
	if len(*calls) != 1 || (*calls)[0].arch != "arm64" {
		t.Errorf("builder calls %+v, want one call for arm64", *calls)
	}
	if string(drRead(t, filepath.Join(out, drName("arm64")))) != "derper arm64" {
		t.Error("the arm64 binary was not written to the output directory")
	}
	drMissing(t, filepath.Join(out, drName("amd64")))
	drMissing(t, filepath.Join(out, artifact.ManifestName))
	if want := drSum([]byte("derper arm64")) + "  " + drName("arm64") + "\n"; stdout.String() != want {
		t.Errorf("stdout = %q, want %q", stdout.String(), want)
	}
}

func TestDrRewritesChecksumsAsPackageShWritesThem(t *testing.T) {
	out := t.TempDir()
	files := map[string]string{
		"ZZ-first.txt":                           "upper case sorts before lower case in byte order",
		"darkpanel_1.4.0.cdx.json":               "{}",
		"darkpanel_1.4.0_linux_amd64.tar.gz":     "tarball",
		"install.sh":                             "#!/bin/sh\n",
		"checksums.txt":                          "stale\n",
		"checksums.txt.0123456789ABCDEF.minisig": "signature",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(out, name), []byte(body), 0o644); err != nil { //nolint:gosec // G306: test files
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(out, "subdir"), 0o755); err != nil { //nolint:gosec // G301: a test directory
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(out, "subdir", "inner.txt"), []byte("inner"), 0o644); err != nil { //nolint:gosec // G306: test files
		t.Fatal(err)
	}

	build, _ := drFake("")
	if err := runDerper(context.Background(), []string{"1.4.0", out}, &bytes.Buffer{}, build); err != nil {
		t.Fatalf("runDerper: %v", err)
	}

	bodies := map[string][]byte{
		"ZZ-first.txt":                       []byte(files["ZZ-first.txt"]),
		"darkpanel_1.4.0.cdx.json":           []byte("{}"),
		"darkpanel_1.4.0_linux_amd64.tar.gz": []byte("tarball"),
		"install.sh":                         []byte("#!/bin/sh\n"),
		drName("amd64"):                      []byte("derper amd64"),
		drName("arm64"):                      []byte("derper arm64"),
		artifact.ManifestName:                drRead(t, filepath.Join(out, artifact.ManifestName)),
	}
	names := make([]string, 0, len(bodies))
	for n := range bodies {
		names = append(names, n)
	}
	sort.Strings(names)
	var want strings.Builder
	for _, n := range names {
		fmt.Fprintf(&want, "%s  %s\n", drSum(bodies[n]), n)
	}
	if got := string(drRead(t, filepath.Join(out, "checksums.txt"))); got != want.String() {
		t.Errorf("checksums.txt =\n%s\nwant\n%s", got, want.String())
	}
	if want := names[0]; want != "ZZ-first.txt" {
		t.Fatalf("test setup: first name is %s", want)
	}
}

func TestDrFailedBuildLeavesTheOutputUntouched(t *testing.T) {
	out := t.TempDir()
	if err := os.WriteFile(filepath.Join(out, "checksums.txt"), []byte("old\n"), 0o644); err != nil { //nolint:gosec // G306: test files
		t.Fatal(err)
	}
	build, _ := drFake("arm64")
	err := runDerper(context.Background(), []string{"1.4.0", out}, &bytes.Buffer{}, build)
	if err == nil || !strings.Contains(err.Error(), "arm64") {
		t.Fatalf("runDerper = %v, want an error naming arm64", err)
	}
	drMissing(t, filepath.Join(out, artifact.ManifestName))
	if got := string(drRead(t, filepath.Join(out, "checksums.txt"))); got != "old\n" {
		t.Errorf("checksums.txt = %q after a failed build, want it unchanged", got)
	}
}

func TestDrRefusals(t *testing.T) {
	out := t.TempDir()
	tests := []struct {
		name  string
		args  []string
		usage bool
	}{
		{"a tag instead of a version", []string{"v1.4.0", out}, false},
		{"a development version", []string{"dev", out}, false},
		{"an unknown architecture", []string{"1.4.0", out, "-arch", "riscv64"}, false},
		{"no output directory", []string{"1.4.0"}, true},
		{"no arguments", nil, true},
		{"an architecture flag without a value", []string{"1.4.0", out, "-arch"}, true},
		{"an unknown flag", []string{"1.4.0", out, "-march", "amd64"}, true},
		{"a trailing word", []string{"1.4.0", out, "-arch", "amd64", "extra"}, true},
	}
	for _, tt := range tests {
		build, calls := drFake("")
		err := runDerper(context.Background(), tt.args, &bytes.Buffer{}, build)
		if err == nil {
			t.Errorf("%s: runDerper = nil, want an error", tt.name)
			continue
		}
		if tt.usage && !strings.HasPrefix(err.Error(), "usage:") {
			t.Errorf("%s: error %q, want the usage text", tt.name, err)
		}
		if len(*calls) != 0 {
			t.Errorf("%s: the builder was called %d times, want none", tt.name, len(*calls))
		}
	}
	entries, err := os.ReadDir(out)
	if err != nil || len(entries) != 0 {
		t.Errorf("a refused run wrote %v (read error %v), want an empty directory", entries, err)
	}
}

func TestDrRunDispatchesTheSubcommand(t *testing.T) {
	for _, args := range [][]string{{"derper"}, {"derper", "1.4.0"}, {"derper", "1.4.0", t.TempDir(), "-arch"}} {
		if err := run(args, strings.NewReader(""), &bytes.Buffer{}); err == nil || !strings.HasPrefix(err.Error(), "usage:") {
			t.Errorf("run(%q) = %v, want the usage error", args, err)
		}
	}
	if !strings.Contains(usage, "releasetool derper <darkpanel version> <out dir> [-arch amd64|arm64]") {
		t.Errorf("the usage text lacks the derper subcommand:\n%s", usage)
	}
}
