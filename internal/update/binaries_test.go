package update

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/paths"
)

// binaryPaths returns a layout under a temporary root whose BinaryDir holds
// an installed binary with content installed.
func binaryPaths(t *testing.T, installed string) paths.Paths {
	t.Helper()
	p := paths.Under(t.TempDir())
	if err := os.MkdirAll(p.BinaryDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeBinaryFile(t, p.Binary(), installed)
	return p
}

func writeBinaryFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil { //nolint:gosec // G306: a test binary is executable
		t.Fatal(err)
	}
}

func sumOf(content string) string {
	s := sha256.Sum256([]byte(content))
	return hex.EncodeToString(s[:])
}

// binaryContent is the content at path, or "<missing>".
func binaryContent(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path) //nolint:gosec // G304: a test path
	if errors.Is(err, fs.ErrNotExist) {
		return "<missing>"
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestStageKeepAndSwap(t *testing.T) {
	p := binaryPaths(t, "old binary")
	src := filepath.Join(t.TempDir(), "darkpanel")
	writeBinaryFile(t, src, "new binary")
	if err := os.Chmod(src, 0o600); err != nil {
		t.Fatal(err)
	}

	newSum, err := Stage(p, "01TX", src)
	if err != nil {
		t.Fatal(err)
	}
	if newSum != sumOf("new binary") || binaryContent(t, p.NewBinary("01TX")) != "new binary" {
		t.Fatalf("Stage = %s; staged %q", newSum, binaryContent(t, p.NewBinary("01TX")))
	}
	if info, _ := os.Stat(p.NewBinary("01TX")); info.Mode().Perm() != 0o755 {
		t.Errorf("staged mode = %v, want 0755", info.Mode().Perm())
	}
	if _, err := Stage(p, "../x", src); err == nil {
		t.Error("Stage accepted a bad transaction id")
	}

	writeBinaryFile(t, p.RecoveryBinary(), "an older recovery binary")
	prevSum, err := KeepPrevious(p)
	if err != nil {
		t.Fatal(err)
	}
	if prevSum != sumOf("old binary") || binaryContent(t, p.PrevBinary()) != "old binary" {
		t.Fatalf("KeepPrevious = %s; prev %q", prevSum, binaryContent(t, p.PrevBinary()))
	}
	prev, _ := os.Stat(p.PrevBinary())
	recovery, err := os.Stat(p.RecoveryBinary())
	if err != nil || !os.SameFile(prev, recovery) {
		t.Fatalf("darkpanel.recovery is not a hard link of darkpanel.prev: %v", err)
	}

	if err := Swap(p, "01TX"); err != nil {
		t.Fatal(err)
	}
	if binaryContent(t, p.Binary()) != "new binary" || binaryContent(t, p.NewBinary("01TX")) != "<missing>" {
		t.Errorf("after Swap darkpanel = %q, staged = %q", binaryContent(t, p.Binary()), binaryContent(t, p.NewBinary("01TX")))
	}
	entries, _ := os.ReadDir(p.BinaryDir)
	for _, e := range entries {
		switch e.Name() {
		case "darkpanel", "darkpanel.prev", "darkpanel.recovery":
		default:
			t.Errorf("BinaryDir holds a leftover %s", e.Name())
		}
	}
	if sum, err := FileSHA256(p.Binary()); err != nil || sum != sumOf("new binary") {
		t.Errorf("FileSHA256 = %s, %v", sum, err)
	}
}

func TestRestorePrevious(t *testing.T) {
	oldSum, newSum := sumOf("old"), sumOf("new")
	tests := []struct {
		name                 string
		binary, prev, failed string // content, "" for absent
		code                 errcode.Code
		wantBinary, wantPrev string
		wantFailed           string
	}{
		{"before either rename", "new", "old", "", "", "old", "<missing>", "new"},
		{"after the first rename", "", "old", "new", "", "old", "<missing>", "new"},
		{"after both renames", "old", "", "new", "", "old", "<missing>", "new"},
		{"crash before the swap", "old", "old", "", "", "old", "old", "<missing>"},
		{"previous binary changed", "new", "other", "", errcode.UpdatePrevMismatch, "new", "other", "<missing>"},
		{"previous binary missing", "new", "", "", errcode.UpdatePrevMismatch, "new", "<missing>", "<missing>"},
		{"unknown installed binary", "other", "old", "", errcode.UpdatePrevMismatch, "other", "old", "<missing>"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := paths.Under(t.TempDir())
			if err := os.MkdirAll(p.BinaryDir, 0o700); err != nil {
				t.Fatal(err)
			}
			for path, content := range map[string]string{p.Binary(): tt.binary, p.PrevBinary(): tt.prev, p.FailedBinary(): tt.failed} {
				if content != "" {
					writeBinaryFile(t, path, content)
				}
			}
			err := RestorePrevious(p, oldSum, newSum)
			var ce *errcode.Error
			switch {
			case tt.code == "" && err != nil:
				t.Fatalf("RestorePrevious = %v", err)
			case tt.code != "" && (!errors.As(err, &ce) || ce.Code != tt.code):
				t.Fatalf("RestorePrevious = %v, want %s", err, tt.code)
			}
			got := [3]string{binaryContent(t, p.Binary()), binaryContent(t, p.PrevBinary()), binaryContent(t, p.FailedBinary())}
			if want := [3]string{tt.wantBinary, tt.wantPrev, tt.wantFailed}; got != want {
				t.Errorf("darkpanel, .prev, .failed = %q, want %q", got, want)
			}
		})
	}
}
