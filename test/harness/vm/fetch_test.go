//go:build harness

package vm

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestFetch(t *testing.T) {
	body := []byte("a cloud image")
	sum := sha256.Sum256(body)
	want := hex.EncodeToString(sum[:])
	var downloads atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/disk.img" {
			http.NotFound(w, r)
			return
		}
		downloads.Add(1)
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	ctx := context.Background()
	cache := filepath.Join(t.TempDir(), "cache")

	path, err := Fetch(ctx, srv.URL+"/disk.img", want, cache)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, body) { //nolint:gosec // G304: the test's own cache file
		t.Errorf("cached image = %q, %v; want %q", got, err, body)
	}
	if again, err := Fetch(ctx, srv.URL+"/disk.img", want, cache); err != nil || again != path || downloads.Load() != 1 {
		t.Errorf("second Fetch = %s, %v after %d downloads; want the cached %s and no download", again, err, downloads.Load(), path)
	}

	if err := os.WriteFile(path, []byte("damaged"), 0o600); err != nil {
		t.Fatal(err)
	}
	if again, err := Fetch(ctx, srv.URL+"/disk.img", want, cache); err != nil || again != path || downloads.Load() != 2 {
		t.Errorf("Fetch over a damaged copy = %s, %v after %d downloads; want a fresh download", again, err, downloads.Load())
	}
	if got, _ := os.ReadFile(path); !bytes.Equal(got, body) { //nolint:gosec // G304: the test's own cache file
		t.Errorf("image after the fresh download = %q, want %q", got, body)
	}

	wrong := strings.Repeat("0", 64)
	if _, err := Fetch(ctx, srv.URL+"/disk.img", wrong, cache); err == nil || !strings.Contains(err.Error(), "update the harness pins") {
		t.Errorf("Fetch with a wrong checksum: %v, want a refusal naming the pins", err)
	}
	if _, err := Fetch(ctx, srv.URL+"/gone.img", wrong, cache); err == nil || !strings.Contains(err.Error(), "HTTP 404") {
		t.Errorf("Fetch of a missing image: %v, want HTTP 404", err)
	}
	entries, err := os.ReadDir(cache)
	if err != nil || len(entries) != 1 || entries[0].Name() != want+".img" {
		t.Errorf("cache holds %v (%v), want only the verified %s.img", entries, err, want)
	}
}
