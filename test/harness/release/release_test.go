//go:build harness

package release

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"aead.dev/minisign"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/update"
	"github.com/darkraise/darkpanel/internal/update/trustedkeys"
)

// harnessKey is the committed harness key, relative to this package's
// directory, the working directory of its tests.
const harnessKey = "../../../internal/update/trustedkeys/testdata/harness.key"

// harnessKeyID is the ID the harnesskeys build trusts the harness key under.
const harnessKeyID = "BCB10AB3E9AF694E"

// trusted returns the public half of key as the harnesskeys build lists it.
func trusted(t *testing.T, key minisign.PrivateKey) trustedkeys.Key {
	t.Helper()
	pub, ok := key.Public().(minisign.PublicKey)
	if !ok {
		t.Fatalf("public key of type %T", key.Public())
	}
	return trustedkeys.Key{ID: fmt.Sprintf("%016X", pub.ID()), PublicKey: pub.String()}
}

func TestLoadKey(t *testing.T) {
	key, err := LoadKey(harnessKey)
	if err != nil {
		t.Fatal(err)
	}
	if got := trusted(t, key).ID; got != harnessKeyID {
		t.Errorf("harness key ID = %s, want %s", got, harnessKeyID)
	}
	if _, err := LoadKey(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Error("LoadKey of a missing file = nil, want an error")
	}
}

// TestPublishVerifies serves what Publish writes and fetches it with the
// updater's own index source and download, so the layout, the tarball, the
// checksums and the signatures are checked by the verifiers that read them in
// production.
func TestPublishVerifies(t *testing.T) {
	key, err := LoadKey(harnessKey)
	if err != nil {
		t.Fatal(err)
	}
	bins := t.TempDir()
	versions := []Version{
		{Version: "0.2.0", Channel: "stable", Binary: filepath.Join(bins, "darkpanel-0.2.0")},
		{Version: "0.3.0", Channel: "beta", Binary: filepath.Join(bins, "darkpanel-0.3.0")},
	}
	for _, v := range versions {
		if err := os.WriteFile(v.Binary, []byte("#!/bin/sh\necho darkpanel "+v.Version+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	dir := t.TempDir()
	if err := Publish(dir, key, versions); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.FileServer(http.Dir(dir)))
	t.Cleanup(srv.Close)
	client := srv.Client()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	keys := []trustedkeys.Key{trusted(t, key)}

	releases, err := update.NewIndexSource(srv.URL, client, core.SystemClock{}).Releases(ctx)
	want := []update.Release{{Version: "0.2.0", Channel: "stable"}, {Version: "0.3.0", Channel: "beta"}}
	if err != nil || !slices.Equal(releases, want) {
		t.Errorf("index releases = %v, %v; want %v", releases, err, want)
	}

	cases := []struct {
		rel     update.Release
		binary  string // the binary the download must extract; empty wants the refusal
		comment string
	}{
		{update.Release{Version: "0.2.0", Channel: "stable"}, versions[0].Binary, "stable release"},
		{update.Release{Version: "0.3.0", Channel: "beta"}, versions[1].Binary, "beta release"},
		{update.Release{Version: "0.2.0", Channel: "beta"}, "", "a stable signature does not pass for beta"},
	}
	for _, c := range cases {
		got, err := update.Download(ctx, client, srv.URL, keys, c.rel, "amd64", t.TempDir())
		if c.binary == "" {
			if ce := errcode.From(err); ce.Code != errcode.UpdateSignatureInvalid {
				t.Errorf("%s: Download = %v, want %s", c.comment, err, errcode.UpdateSignatureInvalid)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: Download: %v", c.comment, err)
			continue
		}
		gotBytes, err := os.ReadFile(got) //nolint:gosec // G304: the path Download returned
		if err != nil {
			t.Fatal(err)
		}
		wantBytes, err := os.ReadFile(c.binary)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(gotBytes, wantBytes) {
			t.Errorf("%s: extracted %q, want %q", c.comment, gotBytes, wantBytes)
		}
	}

	checksums := filepath.Join(dir, "v0.2.0", "checksums.txt")
	text, err := os.ReadFile(checksums) //nolint:gosec // G304: a file Publish wrote
	if err != nil {
		t.Fatal(err)
	}
	tampered := append(bytes.Clone(text), []byte("0000000000000000000000000000000000000000000000000000000000000000  extra\n")...)
	if err := os.WriteFile(checksums, tampered, 0o600); err != nil { //nolint:gosec // G703: a file Publish wrote in a test directory
		t.Fatal(err)
	}
	_, err = update.Download(ctx, client, srv.URL, keys, update.Release{Version: "0.2.0", Channel: "stable"}, "amd64", t.TempDir())
	if ce := errcode.From(err); ce.Code != errcode.UpdateSignatureInvalid {
		t.Errorf("Download of a tampered checksums.txt = %v, want %s", err, errcode.UpdateSignatureInvalid)
	}
}

// TestPublishTarball checks the archive against what release/package.sh
// writes: darkpanel, LICENSE and README.md at the root, owned by 0:0, with
// the repository's LICENSE and README.md.
func TestPublishTarball(t *testing.T) {
	key, err := LoadKey(harnessKey)
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "darkpanel")
	if err := os.WriteFile(bin, []byte("binary"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := Publish(dir, key, []Version{{Version: "0.2.0", Channel: "stable", Binary: bin}}); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(filepath.Join(dir, "v0.2.0", "darkpanel_0.2.0_linux_amd64.tar.gz")) //nolint:gosec // G304: a file Publish wrote
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	type entry struct {
		name     string
		mode     int64
		uid, gid int
		body     string
	}
	var got []entry
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		if hdr.Typeflag != tar.TypeReg {
			t.Errorf("%s has type %c, want a regular file", hdr.Name, hdr.Typeflag)
		}
		got = append(got, entry{hdr.Name, hdr.Mode, hdr.Uid, hdr.Gid, string(body)})
	}
	license, err := os.ReadFile("../../../LICENSE")
	if err != nil {
		t.Fatal(err)
	}
	readme, err := os.ReadFile("../../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	want := []entry{
		{"darkpanel", 0o755, 0, 0, "binary"},
		{"LICENSE", 0o644, 0, 0, string(license)},
		{"README.md", 0o644, 0, 0, string(readme)},
	}
	if !slices.Equal(got, want) {
		t.Errorf("tarball entries differ from package.sh's:\n got %+v\nwant %+v", got, want)
	}
	sigs, err := filepath.Glob(filepath.Join(dir, "v0.2.0", "*.minisig"))
	if err != nil || len(sigs) != 1 || filepath.Base(sigs[0]) != "checksums.txt."+harnessKeyID+".minisig" {
		t.Errorf("signature files = %v, %v; want only checksums.txt.%s.minisig", sigs, err, harnessKeyID)
	}
}
