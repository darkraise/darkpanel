package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"aead.dev/minisign"

	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/update/trustedkeys"
)

func TestDownload(t *testing.T) {
	signer, second := newTestSigner(t), newTestSigner(t)
	srv := newReleaseServer(t)
	srv.publish(t, "1.3.0", "stable", "amd64", []byte("binary 1.3.0"), signer)
	dir := t.TempDir()
	got, err := Download(context.Background(), srv.client(), srv.URL+"/", []trustedkeys.Key{second.Key, signer.Key},
		Release{Version: "1.3.0", Channel: "stable"}, "amd64", dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Join(dir, "darkpanel") {
		t.Errorf("Download = %s", got)
	}
	if b, _ := os.ReadFile(got); string(b) != "binary 1.3.0" { //nolint:gosec // G304: a test path
		t.Errorf("extracted %q", b)
	}
	if info, _ := os.Stat(got); info.Mode().Perm() != 0o755 {
		t.Errorf("extracted mode %v, want 0755", info.Mode().Perm())
	}
}

func TestDownloadRefuses(t *testing.T) {
	signer := newTestSigner(t)
	keys := []trustedkeys.Key{signer.Key}
	tarballName := TarballName("1.3.0", "amd64")
	// resign replaces the 1.3.0 tarball with one of entries and re-signs
	// checksums.txt, so only the archive's content is wrong.
	resign := func(srv *releaseServer, tarball []byte) {
		srv.put("v1.3.0/"+tarballName, tarball)
		sums := checksumLine(tarball, tarballName)
		srv.put("v1.3.0/checksums.txt", []byte(sums))
		srv.put("v1.3.0/checksums.txt."+signer.Key.ID+".minisig", signer.sign([]byte(sums), releaseComment("1.3.0", "stable")))
	}
	tests := []struct {
		name    string
		rel     Release
		tamper  func(t *testing.T, srv *releaseServer)
		code    errcode.Code
		message string
	}{
		{"not published", Release{Version: "1.9.0", Channel: "stable"}, nil, errcode.UpdateNotFound, ""},
		{"signature file missing", Release{Version: "1.3.0", Channel: "stable"}, func(_ *testing.T, srv *releaseServer) {
			srv.remove("v1.3.0/checksums.txt." + signer.Key.ID + ".minisig")
		}, errcode.UpdateSignatureInvalid, ""},
		{"older checksums.txt replayed", Release{Version: "1.3.0", Channel: "stable"}, func(_ *testing.T, srv *releaseServer) {
			srv.put("v1.3.0/checksums.txt", srv.get("v1.2.0/checksums.txt"))
			srv.put("v1.3.0/checksums.txt."+signer.Key.ID+".minisig", srv.get("v1.2.0/checksums.txt."+signer.Key.ID+".minisig"))
		}, errcode.UpdateSignatureInvalid, ""},
		{"signed for beta, listed as stable", Release{Version: "1.4.0-beta.1", Channel: "stable"}, nil,
			errcode.UpdateSignatureInvalid, ""},
		{"tarball does not match its line", Release{Version: "1.3.0", Channel: "stable"}, func(t *testing.T, srv *releaseServer) {
			srv.put("v1.3.0/"+tarballName, testTarball(t, tarEntry{Name: "darkpanel", Body: []byte("evil")}))
		}, errcode.UpdateSignatureInvalid, ""},
		{"tarball not listed", Release{Version: "1.3.0", Channel: "stable"}, func(_ *testing.T, srv *releaseServer) {
			sums := checksumLine([]byte("x"), "darkpanel_1.3.0_linux_arm64.tar.gz")
			srv.put("v1.3.0/checksums.txt", []byte(sums))
			srv.put("v1.3.0/checksums.txt."+signer.Key.ID+".minisig", signer.sign([]byte(sums), releaseComment("1.3.0", "stable")))
		}, errcode.UpdateNotFound, ""},
		{"checksums.txt over its limit", Release{Version: "1.3.0", Channel: "stable"}, func(_ *testing.T, srv *releaseServer) {
			sums := strings.Repeat("#", 64<<10+1)
			srv.put("v1.3.0/checksums.txt", []byte(sums))
		}, "", "bytes"},
		{"no darkpanel in the archive", Release{Version: "1.3.0", Channel: "stable"}, func(t *testing.T, srv *releaseServer) {
			resign(srv, testTarball(t, tarEntry{Name: "LICENSE", Body: []byte("l")}))
		}, "", "no darkpanel"},
		{"darkpanel outside the archive root", Release{Version: "1.3.0", Channel: "stable"}, func(t *testing.T, srv *releaseServer) {
			resign(srv, testTarball(t, tarEntry{Name: "../darkpanel", Body: []byte("x")}, tarEntry{Name: "sub/darkpanel", Body: []byte("x")}))
		}, "", "no darkpanel"},
		{"darkpanel is a symlink", Release{Version: "1.3.0", Channel: "stable"}, func(t *testing.T, srv *releaseServer) {
			resign(srv, testTarball(t, tarEntry{Name: "darkpanel", Type: tar.TypeSymlink, Linkname: "/bin/sh"}))
		}, "", "regular file"},
		{"darkpanel twice", Release{Version: "1.3.0", Channel: "stable"}, func(t *testing.T, srv *releaseServer) {
			resign(srv, testTarball(t, tarEntry{Name: "darkpanel", Body: []byte("a")}, tarEntry{Name: "./darkpanel", Body: []byte("b")}))
		}, "", "twice"},
		{"darkpanel over its limit", Release{Version: "1.3.0", Channel: "stable"}, func(t *testing.T, srv *releaseServer) {
			resign(srv, oversizedTarball(t))
		}, "", "bytes"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newReleaseServer(t)
			srv.publish(t, "1.2.0", "stable", "amd64", []byte("binary 1.2.0"), signer)
			srv.publish(t, "1.3.0", "stable", "amd64", []byte("binary 1.3.0"), signer)
			srv.publish(t, "1.4.0-beta.1", "beta", "amd64", []byte("binary 1.4.0-beta.1"), signer)
			if tt.tamper != nil {
				tt.tamper(t, srv)
			}
			dir := t.TempDir()
			_, err := Download(context.Background(), srv.client(), srv.URL, keys, tt.rel, "amd64", dir)
			var ce *errcode.Error
			switch {
			case err == nil:
				t.Fatal("Download accepted the release")
			case tt.code != "" && (!errors.As(err, &ce) || ce.Code != tt.code):
				t.Fatalf("Download = %v, want %s", err, tt.code)
			case tt.message != "" && !strings.Contains(err.Error(), tt.message):
				t.Fatalf("Download = %v, want an error mentioning %q", err, tt.message)
			}
			if _, err := os.Stat(filepath.Join(dir, "darkpanel")); !errors.Is(err, os.ErrNotExist) {
				t.Error("a refused release left a darkpanel binary")
			}
		})
	}
}

func TestDownloadBetaHostTakesStableRelease(t *testing.T) {
	signer := newTestSigner(t)
	srv := newReleaseServer(t)
	srv.publish(t, "1.3.0-beta.1", "beta", "arm64", []byte("beta binary"), signer)
	srv.publish(t, "1.3.0", "stable", "arm64", []byte("stable binary"), signer)
	srv.setIndex(t, Release{Version: "1.3.0-beta.1", Channel: "beta"}, Release{Version: "1.3.0", Channel: "stable"})
	releases, err := NewIndexSource(srv.URL, srv.client(), coretest.NewFakeClock(time.Unix(0, 0))).Releases(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	beta := InChannel(releases, "beta")
	newest := beta[len(beta)-1]
	// A host on beta takes the stable entry as listed: the trusted comment is
	// checked against the entry's channel, not the host's.
	bin, err := Download(context.Background(), srv.client(), srv.URL, []trustedkeys.Key{signer.Key}, newest, "arm64", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(bin); string(b) != "stable binary" { //nolint:gosec // G304: a test path
		t.Errorf("a beta host installed %q, want the stable binary", b)
	}
	if _, err := Download(context.Background(), srv.client(), srv.URL, []trustedkeys.Key{signer.Key},
		Release{Version: "1.3.0", Channel: "beta"}, "arm64", t.TempDir()); err == nil {
		t.Error("a stable signature was accepted for a beta entry")
	}
}

// oversizedTarball is a gzipped tar whose darkpanel header claims one byte
// more than the binary limit; the body is never written, because the limit
// is checked before anything is read.
func oversizedTarball(t *testing.T) []byte {
	t.Helper()
	var raw bytes.Buffer
	tw := tar.NewWriter(&raw)
	if err := tw.WriteHeader(&tar.Header{Name: "darkpanel", Typeflag: tar.TypeReg, Size: maxBinaryBytes + 1, Mode: 0o755}); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write(raw.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestVerifyChecksums(t *testing.T) {
	oldKey, newKey, otherKey := newTestSigner(t), newTestSigner(t), newTestSigner(t)
	checksums := []byte(checksumLine([]byte("tarball"), "darkpanel_1.3.0_linux_amd64.tar.gz"))
	comment := releaseComment("1.3.0", "stable")
	signed := func(signers ...testSigner) map[string][]byte {
		sigs := map[string][]byte{}
		for _, s := range signers {
			sigs[s.Key.ID] = s.sign(checksums, comment)
		}
		return sigs
	}
	legacy := minisign.SignWithComments(oldKey.priv, checksums, comment, "legacy")
	keys := func(signers ...testSigner) []trustedkeys.Key {
		var out []trustedkeys.Key
		for _, s := range signers {
			out = append(out, s.Key)
		}
		return out
	}
	tests := []struct {
		name      string
		data      []byte
		sigs      map[string][]byte
		trusted   []trustedkeys.Key
		version   string
		channel   string
		wantValid bool
	}{
		// Key rotation (ADR 0007): a dual-signed release verifies under the
		// old list, the new list and both.
		{"dual-signed, old key trusted", checksums, signed(oldKey, newKey), keys(oldKey), "1.3.0", "stable", true},
		{"dual-signed, new key trusted", checksums, signed(oldKey, newKey), keys(newKey), "1.3.0", "stable", true},
		{"dual-signed, both trusted", checksums, signed(oldKey, newKey), keys(oldKey, newKey), "1.3.0", "stable", true},
		{"new-only release, both trusted", checksums, signed(newKey), keys(oldKey, newKey), "1.3.0", "stable", true},
		{"old-only release, new key trusted", checksums, signed(oldKey), keys(newKey), "1.3.0", "stable", false},
		{"no signature", checksums, map[string][]byte{}, keys(oldKey), "1.3.0", "stable", false},
		{"no trusted key", checksums, signed(oldKey), nil, "1.3.0", "stable", false},
		{"older version replayed", checksums, signed(oldKey), keys(oldKey), "1.4.0", "stable", false},
		{"other channel", checksums, signed(oldKey), keys(oldKey), "1.3.0", "beta", false},
		{"tampered checksums", append([]byte("x"), checksums...), signed(oldKey), keys(oldKey), "1.3.0", "stable", false},
		{"untrusted key under a trusted name", checksums,
			map[string][]byte{oldKey.Key.ID: otherKey.sign(checksums, comment)}, keys(oldKey), "1.3.0", "stable", false},
		{"legacy Ed signature", checksums, map[string][]byte{oldKey.Key.ID: legacy}, keys(oldKey), "1.3.0", "stable", false},
		{"garbage signature, then a good one", checksums,
			map[string][]byte{oldKey.Key.ID: []byte("garbage"), newKey.Key.ID: newKey.sign(checksums, comment)},
			keys(oldKey, newKey), "1.3.0", "stable", true},
		{"malformed trusted key skipped", checksums, signed(newKey),
			[]trustedkeys.Key{{ID: oldKey.Key.ID, PublicKey: "not a key"}, newKey.Key}, "1.3.0", "stable", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := VerifyChecksums(tt.data, tt.sigs, tt.trusted, tt.version, tt.channel)
			if tt.wantValid && err != nil {
				t.Fatalf("VerifyChecksums = %v, want nil", err)
			}
			var ce *errcode.Error
			if !tt.wantValid && (!errors.As(err, &ce) || ce.Code != errcode.UpdateSignatureInvalid) {
				t.Fatalf("VerifyChecksums = %v, want %s", err, errcode.UpdateSignatureInvalid)
			}
		})
	}
}
