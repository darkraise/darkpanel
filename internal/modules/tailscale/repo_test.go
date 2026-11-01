package tailscale

import (
	"bytes"
	"context"
	"crypto/sha1" //nolint:gosec // G505: an OpenPGP v4 fingerprint is defined as a SHA-1
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// piTS is a ts on a host that runs the given OS and architecture.
func piTS(h *tsHost, id, codename, arch string) ts {
	t := h.ts()
	t.d.osRelease = func() (osInfo, error) { return osInfo{ID: id, Codename: codename}, nil }
	t.d.arch = arch
	return t
}

func piKeyring(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("repo_keyring.gpg")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func piSources(id, codename string) string {
	return "deb [signed-by=/usr/share/keyrings/tailscale-archive-keyring.gpg] https://pkgs.tailscale.com/stable/" + id + " " + codename + " main\n"
}

func TestPiRepoFiles(t *testing.T) {
	for _, tc := range []struct{ id, codename, arch string }{
		{"ubuntu", "jammy", "amd64"}, {"ubuntu", "noble", "arm64"}, {"debian", "bookworm", "amd64"}, {"debian", "trixie", "arm64"},
	} {
		t.Run(tc.id+" "+tc.codename+" "+tc.arch, func(t *testing.T) {
			keyring, sources, err := piTS(newTSHost(t), tc.id, tc.codename, tc.arch).repoFiles()
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(keyring, piKeyring(t)) {
				t.Error("the keyring is not the bytes of repo_keyring.gpg")
			}
			if string(sources) != piSources(tc.id, tc.codename) {
				t.Errorf("source list %q, want %q", sources, piSources(tc.id, tc.codename))
			}
		})
	}
}

func TestPiRepoFilesRefuseWhatTailscaleDoesNotSupport(t *testing.T) {
	for _, tc := range []struct{ id, codename, arch string }{
		{"ubuntu", "focal", "amd64"}, {"debian", "bullseye", "amd64"}, {"fedora", "noble", "amd64"}, {"ubuntu", "noble", "386"}, {"", "", "amd64"},
	} {
		_, _, err := piTS(newTSHost(t), tc.id, tc.codename, tc.arch).repoFiles()
		var coded *errcode.Error
		if !errors.As(err, &coded) || coded.Code != errcode.TailscaleUnavailable {
			t.Errorf("%s %s %s: error %v, want %s", tc.id, tc.codename, tc.arch, err, errcode.TailscaleUnavailable)
		}
	}
	h := newTSHost(t)
	tsv := h.ts()
	tsv.d.osRelease = func() (osInfo, error) { return osInfo{}, errors.New("no os-release") }
	if _, _, err := tsv.repoFiles(); err == nil || !strings.Contains(err.Error(), "no os-release") {
		t.Errorf("error %v, want the os-release failure", err)
	}
}

// piFingerprint is the OpenPGP v4 fingerprint of a public key packet body.
func piFingerprint(body []byte) string {
	h := sha1.New()                                                         //nolint:gosec // G401: see the import
	h.Write(binary.BigEndian.AppendUint16([]byte{0x99}, uint16(len(body)))) //nolint:gosec // G115: the test bodies are under 64 KiB
	h.Write(body)
	return strings.ToUpper(hex.EncodeToString(h.Sum(nil)))
}

func TestPiKeyringFingerprints(t *testing.T) {
	keyBody := append([]byte{4, 0x65, 0x00, 0x00, 0x01, 1}, bytes.Repeat([]byte{0xAB}, 20)...)
	subBody := append([]byte{4, 0x65, 0x00, 0x00, 0x02, 1}, bytes.Repeat([]byte{0xCD}, 300)...)
	v5Body := append([]byte{5}, bytes.Repeat([]byte{0x11}, 10)...)
	uid := []byte("Tailscale Inc. <info@tailscale.com>")
	oldHeader := func(tag byte, body []byte) []byte { // old format, two-byte length
		return append(binary.BigEndian.AppendUint16([]byte{0x80 | tag<<2 | 1}, uint16(len(body))), body...) //nolint:gosec // G115: the test bodies are under 64 KiB
	}
	newHeader := func(tag byte, body []byte) []byte { // new format, one-byte length when it fits
		if len(body) < 192 {
			return append([]byte{0xC0 | tag, byte(len(body))}, body...) //nolint:gosec // G115: shorter than 192
		}
		n := len(body) - 192
		return append([]byte{0xC0 | tag, byte(n>>8) + 192, byte(n)}, body...) //nolint:gosec // G115: the test bodies are under 16 KiB
	}
	cases := []struct {
		name string
		in   []byte
		want []string
		err  bool
	}{
		{"old format public key", oldHeader(6, keyBody), []string{piFingerprint(keyBody)}, false},
		{"new format public key", newHeader(6, keyBody), []string{piFingerprint(keyBody)}, false},
		{"a key, its user id and a long subkey in new format", slices.Concat(newHeader(6, keyBody), newHeader(13, uid), newHeader(14, subBody)),
			[]string{piFingerprint(keyBody), piFingerprint(subBody)}, false},
		{"a key and a subkey in old format", slices.Concat(oldHeader(6, keyBody), oldHeader(14, subBody)),
			[]string{piFingerprint(keyBody), piFingerprint(subBody)}, false},
		{"a version 5 key has no version 4 fingerprint", newHeader(6, v5Body), nil, false},
		{"an empty keyring", nil, nil, false},
		{"not a packet", []byte("-----BEGIN PGP PUBLIC KEY BLOCK-----"), nil, true},
		{"a packet longer than the data", newHeader(6, keyBody)[:10], nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := keyringFingerprints(tc.in)
			if (err != nil) != tc.err {
				t.Fatalf("error %v, want error %v", err, tc.err)
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("fingerprints %v, want %v", got, tc.want)
			}
		})
	}
}

func TestPiEmbeddedKeyringCarriesThePinnedFingerprint(t *testing.T) {
	if len(keyringFingerprint) != 40 || keyringFingerprint != strings.ToUpper(keyringFingerprint) {
		t.Fatalf("pinned fingerprint %q, want 40 upper-case hex digits", keyringFingerprint)
	}
	fps, err := keyringFingerprints(piKeyring(t))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(fps, keyringFingerprint) {
		t.Errorf("repo_keyring.gpg holds %v, none is the pinned %s", fps, keyringFingerprint)
	}
}

func TestPiRepoFilesRefuseAKeyringWithoutThePinnedFingerprint(t *testing.T) {
	orig := repoKeyring
	t.Cleanup(func() { repoKeyring = orig })
	repoKeyring = []byte{0xC0 | 6, 3, 4, 1, 2}
	_, _, err := piTS(newTSHost(t), "ubuntu", "noble", "amd64").repoFiles()
	if err == nil || !strings.Contains(err.Error(), "fingerprint") {
		t.Errorf("error %v, want a refusal naming the fingerprint", err)
	}
}

func TestPiEnsureRepo(t *testing.T) {
	want := map[string][]byte{keyringPath: nil, sourcesPath: []byte(piSources("ubuntu", "noble"))}
	cases := []struct {
		name        string
		reads       func(keyring []byte) map[string][]byte
		wantWritten []string
		conflict    string // the path the refusal names
	}{
		{"both absent", func([]byte) map[string][]byte { return nil }, []string{keyringPath, sourcesPath}, ""},
		{"both identical", func(k []byte) map[string][]byte {
			return map[string][]byte{keyringPath: k, sourcesPath: want[sourcesPath]}
		}, nil, ""},
		{"keyring identical, sources absent", func(k []byte) map[string][]byte { return map[string][]byte{keyringPath: k} }, []string{sourcesPath}, ""},
		{"keyring absent, sources identical", func([]byte) map[string][]byte { return map[string][]byte{sourcesPath: want[sourcesPath]} }, []string{keyringPath}, ""},
		{"keyring differs", func([]byte) map[string][]byte { return map[string][]byte{keyringPath: []byte("another key")} }, nil, keyringPath},
		{"sources differ, keyring absent", func([]byte) map[string][]byte {
			return map[string][]byte{sourcesPath: []byte("deb https://example.com/ stable main\n")}
		}, nil, sourcesPath},
		{"sources differ by a newline", func(k []byte) map[string][]byte {
			return map[string][]byte{keyringPath: k, sourcesPath: bytes.TrimSuffix(want[sourcesPath], []byte("\n"))}
		}, nil, sourcesPath},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newTSHost(t)
			keyring := piKeyring(t)
			for p, b := range tc.reads(keyring) {
				h.reads[p] = b
			}
			written, err := piTS(h, "ubuntu", "noble", "amd64").ensureRepo(context.Background(), h.env(""))
			if tc.conflict != "" {
				var coded *errcode.Error
				if !errors.As(err, &coded) || coded.Code != errcode.TailscaleRepoConflict || !strings.Contains(coded.Detail, tc.conflict) || !core.IsDefinite(err) {
					t.Fatalf("error %v, want a definite %s naming %s", err, errcode.TailscaleRepoConflict, tc.conflict)
				}
				if len(h.files) != 0 {
					t.Errorf("a conflict still wrote %v", h.files)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(written, tc.wantWritten) {
				t.Errorf("written %v, want %v", written, tc.wantWritten)
			}
			if len(h.files) != len(tc.wantWritten) {
				t.Errorf("files written %d, want %d", len(h.files), len(tc.wantWritten))
			}
			for _, p := range tc.wantWritten {
				wantBytes := want[p]
				if p == keyringPath {
					wantBytes = keyring
				}
				if !bytes.Equal(h.files[p], wantBytes) {
					t.Errorf("%s holds %q", p, h.files[p])
				}
			}
		})
	}
}

func TestPiEnsureRepoReportsAnUnreadableFile(t *testing.T) {
	h := newTSHost(t)
	tsv := piTS(h, "ubuntu", "noble", "amd64")
	tsv.d.readFile = func(string) ([]byte, error) { return nil, fs.ErrPermission }
	_, err := tsv.ensureRepo(context.Background(), h.env(""))
	var coded *errcode.Error
	if err == nil || errors.As(err, &coded) || !errors.Is(err, fs.ErrPermission) || len(h.files) != 0 {
		t.Errorf("error %v, files %v, want the read failure and nothing written", err, h.files)
	}
}

func TestPiEnsureRepoJournalsWhatItWrites(t *testing.T) {
	h := newTSHost(t)
	h.reads[keyringPath] = piKeyring(t)
	env := h.env("c1")
	if _, err := piTS(h, "ubuntu", "noble", "amd64").ensureRepo(context.Background(), env); err != nil {
		t.Fatal(err)
	}
	effects, err := env.Effects.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(effects) != 1 {
		t.Fatalf("%d effects, want one for the source list only", len(effects))
	}
	e := effects[0]
	if e.Kind != core.EffectCreate || e.Resource != "tailscale.file:"+sourcesPath || e.State != core.EffectMade || !e.Local || !e.InverseLocal ||
		string(e.Before.State) != "null" || e.Post == nil || !strings.Contains(string(e.Post.State), `"sha256"`) {
		t.Errorf("effect %+v, want a made, local creation of the source list from null", e)
	}
}
