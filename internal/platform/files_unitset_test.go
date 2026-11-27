package platform

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
)

func TestPatternDirMode(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "jobs")
	f, _ := fpFiles(t, FilesConfig{Owned: map[string][]core.OwnedPath{"pd": {{Path: dir, Kind: core.OwnedDir, Pattern: `^[a-z]+\.sh$`}}}})
	ctx := context.Background()
	mode := func() os.FileMode {
		t.Helper()
		info, err := os.Lstat(dir)
		if err != nil {
			t.Fatal(err)
		}
		return info.Mode().Perm()
	}
	if err := f.WriteDirFile(ctx, "pd", dir, "a.sh", []byte("true\n"), 0o700, -1); err != nil {
		t.Fatal(err)
	}
	if got := mode(); got != 0o700 {
		t.Errorf("the first write made the directory %o, want 700", got)
	}
	if err := os.Chmod(dir, 0o755); err != nil { //nolint:gosec // G302: the 0755 directory an older agent made
		t.Fatal(err)
	}
	if err := f.WriteDirFile(ctx, "pd", dir, "b.sh", []byte("true\n"), 0o700, -1); err != nil {
		t.Fatal(err)
	}
	if got := mode(); got != 0o700 {
		t.Errorf("after a write the recorded 0755 directory is %o, want 700", got)
	}
	if err := os.Chmod(dir, 0o775); err != nil { //nolint:gosec // G302: a group-writable directory under test
		t.Fatal(err)
	}
	if err := f.WriteDirFile(ctx, "pd", dir, "c.sh", []byte("true\n"), 0o700, -1); err == nil {
		t.Error("a write into a group-writable directory passed")
	}
}
