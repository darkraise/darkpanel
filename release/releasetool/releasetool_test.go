package main

import (
	"bytes"
	"cmp"
	"encoding/base64"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
		name string
		info string
		keys []string
		ok   bool
	}{
		{"one key", `{"testBuild":false,"trustedKeys":["A000000000000000"]}`, []string{a}, true},
		{"rotation in any order", `{"testBuild":false,"trustedKeys":["B000000000000000","A000000000000000"]}`, []string{a, b}, true},
		{"test build", `{"testBuild":true,"trustedKeys":["A000000000000000"]}`, []string{a}, false},
		{"no testBuild field", `{"trustedKeys":["A000000000000000"]}`, []string{a}, false},
		{"binary trusts an extra key", `{"testBuild":false,"trustedKeys":["A000000000000000","B000000000000000"]}`, []string{a}, false},
		{"signing key not trusted", `{"testBuild":false,"trustedKeys":["A000000000000000"]}`, []string{a, b}, false},
		{"no signing keys", `{"testBuild":false,"trustedKeys":[]}`, nil, false},
		{"not JSON", `darkpanel dev`, []string{a}, false},
	}
	for _, tt := range tests {
		if err := checkBuild([]byte(tt.info), tt.keys); (err == nil) != tt.ok {
			t.Errorf("%s: checkBuild = %v, want ok %v", tt.name, err, tt.ok)
		}
	}
}

func TestRunCheckBuild(t *testing.T) {
	info := `{"testBuild":false,"trustedKeys":["A000000000000000","B000000000000000"]}`
	if err := run([]string{"check-build", "A000000000000000,B000000000000000"}, strings.NewReader(info), &bytes.Buffer{}); err != nil {
		t.Errorf("check-build of both keys = %v, want nil", err)
	}
	if err := run([]string{"check-build", ""}, strings.NewReader(info), &bytes.Buffer{}); err == nil {
		t.Error("check-build of no keys = nil, want an error")
	}
}

func TestRunUsage(t *testing.T) {
	for _, args := range [][]string{nil, {"publish"}, {"check-tag"}, {"index", "extra"}} {
		if err := run(args, strings.NewReader(""), &bytes.Buffer{}); err == nil || !strings.HasPrefix(err.Error(), "usage:") {
			t.Errorf("run(%q) = %v, want the usage error", args, err)
		}
	}
}
