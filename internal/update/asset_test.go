package update

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/update/trustedkeys"
)

// asPublish publishes assets under v<version>/ with a checksums.txt that lists
// exactly them, signed for channel by every signer.
func asPublish(srv *releaseServer, version, channel string, assets map[string][]byte, signers ...testSigner) {
	dir := "v" + version + "/"
	names := make([]string, 0, len(assets))
	for name := range assets {
		names = append(names, name)
	}
	sort.Strings(names)
	var sums strings.Builder
	for _, name := range names {
		srv.put(dir+name, assets[name])
		sums.WriteString(checksumLine(assets[name], name))
	}
	srv.put(dir+"checksums.txt", []byte(sums.String()))
	for _, s := range signers {
		srv.put(dir+"checksums.txt."+s.Key.ID+".minisig", s.sign([]byte(sums.String()), releaseComment(version, channel)))
	}
}

func TestAsDownloadAssetReturnsTheVerifiedAsset(t *testing.T) {
	signer, second := newTestSigner(t), newTestSigner(t)
	longName := strings.Repeat("a", 128)
	assets := map[string][]byte{
		"derper.json":                 []byte(`{"derperVersion":"v1.104.1"}`),
		"derper-v1.104.1-linux-arm64": []byte("arm64 binary"),
		"A_b.c-d":                     []byte("odd characters"),
		"a":                           []byte("one character"),
		longName:                      []byte("longest name"),
	}
	srv := newReleaseServer(t)
	asPublish(srv, "1.3.0", "stable", assets, signer)
	keys := []trustedkeys.Key{second.Key, signer.Key}
	for name, want := range assets {
		t.Run(name, func(t *testing.T) {
			got, err := DownloadAsset(context.Background(), srv.client(), srv.URL+"/", keys,
				Release{Version: "1.3.0", Channel: "stable"}, name, int64(len(want)))
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != string(want) {
				t.Errorf("DownloadAsset(%s) = %q, want %q", name, got, want)
			}
		})
	}
}

func TestAsDownloadAssetRefuses(t *testing.T) {
	signer := newTestSigner(t)
	keys := []trustedkeys.Key{signer.Key}
	body := []byte("0123456789")
	stable := Release{Version: "1.3.0", Channel: "stable"}
	tests := []struct {
		name    string
		rel     Release
		asset   string
		max     int64
		tamper  func(srv *releaseServer)
		code    errcode.Code
		message string
	}{
		{name: "release not published", rel: Release{Version: "1.9.0", Channel: "stable"}, asset: "derper.json", max: 64,
			code: errcode.UpdateNotFound},
		{name: "version that is not a version", rel: Release{Version: "nope", Channel: "stable"}, asset: "derper.json", max: 64,
			message: "semantic version"},
		{name: "signature file missing", rel: stable, asset: "derper.json", max: 64, tamper: func(srv *releaseServer) {
			srv.remove("v1.3.0/checksums.txt." + signer.Key.ID + ".minisig")
		}, code: errcode.UpdateSignatureInvalid},
		{name: "older checksums.txt replayed", rel: stable, asset: "derper.json", max: 64, tamper: func(srv *releaseServer) {
			srv.put("v1.3.0/checksums.txt", srv.get("v1.2.0/checksums.txt"))
			srv.put("v1.3.0/checksums.txt."+signer.Key.ID+".minisig", srv.get("v1.2.0/checksums.txt."+signer.Key.ID+".minisig"))
		}, code: errcode.UpdateSignatureInvalid},
		{name: "signed for beta, listed as stable", rel: Release{Version: "1.4.0-beta.1", Channel: "stable"}, asset: "derper.json", max: 64,
			code: errcode.UpdateSignatureInvalid},
		{name: "checksums.txt over its limit", rel: stable, asset: "derper.json", max: 64, tamper: func(srv *releaseServer) {
			srv.put("v1.3.0/checksums.txt", []byte(strings.Repeat("#", 64<<10+1)))
		}, message: "bytes"},
		{name: "asset not listed", rel: stable, asset: "missing.json", max: 64,
			code: errcode.UpdateNotFound, message: "lists no missing.json"},
		{name: "asset listed but not served", rel: stable, asset: "derper.json", max: 64, tamper: func(srv *releaseServer) {
			srv.remove("v1.3.0/derper.json")
		}, code: errcode.UpdateNotFound},
		{name: "asset does not match its line", rel: stable, asset: "derper.json", max: 64, tamper: func(srv *releaseServer) {
			srv.put("v1.3.0/derper.json", []byte("0123456780"))
		}, code: errcode.UpdateSignatureInvalid, message: "derper.json does not match the signed checksums.txt"},
		{name: "asset over the limit", rel: stable, asset: "derper.json", max: int64(len(body)) - 1, message: "9 bytes"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newReleaseServer(t)
			asPublish(srv, "1.2.0", "stable", map[string][]byte{"derper.json": body}, signer)
			asPublish(srv, "1.3.0", "stable", map[string][]byte{"derper.json": body}, signer)
			asPublish(srv, "1.4.0-beta.1", "beta", map[string][]byte{"derper.json": body}, signer)
			if tt.tamper != nil {
				tt.tamper(srv)
			}
			got, err := DownloadAsset(context.Background(), srv.client(), srv.URL, keys, tt.rel, tt.asset, tt.max)
			var ce *errcode.Error
			switch {
			case err == nil:
				t.Fatalf("DownloadAsset accepted the asset and returned %q", got)
			case tt.code != "" && (!errors.As(err, &ce) || ce.Code != tt.code):
				t.Fatalf("DownloadAsset = %v, want %s", err, tt.code)
			case tt.message != "" && !strings.Contains(err.Error(), tt.message):
				t.Fatalf("DownloadAsset = %v, want an error mentioning %q", err, tt.message)
			}
			if got != nil {
				t.Errorf("a refused asset returned %q", got)
			}
		})
	}
}

func TestAsDownloadAssetRefusesNamesThatAreNotAssetNamesBeforeAnyRequest(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	for _, name := range []string{
		"", "../checksums.txt", "a/b", "/etc/passwd", ".hidden", "-flag", "_x", "a b", "x?y", "x#y", "x\ny", "café", "a:b",
		strings.Repeat("a", 129),
	} {
		_, err := DownloadAsset(context.Background(), srv.Client(), srv.URL, nil, Release{Version: "1.3.0", Channel: "stable"}, name, 64)
		if err == nil || !strings.Contains(err.Error(), "is not an asset name") {
			t.Errorf("DownloadAsset(%q) = %v, want an error saying it is not an asset name", name, err)
		}
	}
	if n := hits.Load(); n != 0 {
		t.Errorf("%d requests reached the server for names that are not asset names, want none", n)
	}
}
