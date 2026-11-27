package web

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// distState lists every path under dist with its size, mtime and content hash.
func distState(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir("dist", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		sum := ""
		if info.Mode().IsRegular() {
			b, err := os.ReadFile(p) //nolint:gosec // G304: a file of the built UI
			if err != nil {
				return err
			}
			h := sha256.Sum256(b)
			sum = hex.EncodeToString(h[:])
		}
		out[p] = info.ModTime().String() + " " + sum
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return out
}

func TestA35_RunningTheWebAcceptanceSuiteLeavesDistUnchanged(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the web app twice")
	}
	before := distState(t)
	if len(before) == 0 {
		t.Skip("no web/dist to compare: build the web app first")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 14*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "pnpm", "exec", "vitest", "run", ".acceptance.test.") //nolint:gosec // G204: fixed arguments
	cmd.Dir = "."
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("the web acceptance test suite failed: %v\n%s", err, tail(out, 4000))
	}
	after := distState(t)
	for p, v := range before {
		if after[p] != v {
			t.Errorf("web/dist changed: %s was %q, is %q", p, v, after[p])
		}
	}
	for p := range after {
		if _, ok := before[p]; !ok {
			t.Errorf("web/dist gained %s", p)
		}
	}

	size := exec.CommandContext(ctx, "go", "test", "-count=1", "-run", "^TestMainBundleSize$", "-v", ".") //nolint:gosec // G204: fixed arguments
	size.Env = append(os.Environ(), "DARKPANEL_REQUIRE_UI=1")
	if out, err := size.CombinedOutput(); err != nil {
		t.Errorf("TestMainBundleSize after the web acceptance suite: %v\n%s", err, tail(out, 2000))
	}
}

func tail(b []byte, n int) string {
	if len(b) > n {
		b = b[len(b)-n:]
	}
	return string(b)
}
