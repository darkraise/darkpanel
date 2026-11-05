package dockerfw

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// fz7Head returns at most the first 64 bytes of b, for a failure message.
func fz7Head(b []byte) []byte { return b[:min(len(b), 64)] }

// fz7Terminated is b with the final newline WithBlock supplies.
func fz7Terminated(b []byte) []byte {
	if len(b) > 0 && b[len(b)-1] != '\n' {
		return append(bytes.Clone(b), '\n')
	}
	return b
}

// FuzzBlock feeds an arbitrary after.rules content and block to BlockOf,
// WithBlock and WithoutBlock: a file that BlockOf refuses is refused by both
// edits; WithBlock's output holds the block, ended by a newline, and setting
// the same block again changes nothing; WithoutBlock of it gives the file
// back, with the newline WithBlock supplies, when the file had no block, and
// WithoutBlock of the file when it had one (R74).
func FuzzBlock(f *testing.F) {
	names, err := filepath.Glob(filepath.Join("testdata", "capture", "*", "*", "files", "etc", "ufw", "after*.rules"))
	if err != nil {
		f.Fatal(err)
	}
	if len(names) == 0 {
		f.Fatal("no seed under testdata/capture")
	}
	for _, name := range names {
		file, err := os.ReadFile(name) //nolint:gosec // G304: a fixture under testdata/
		if err != nil {
			f.Fatal(err)
		}
		block, ok, err := BlockOf(file)
		if err != nil {
			f.Fatalf("%s: %v", name, err)
		}
		if !ok {
			block = []byte("*filter\n:DOCKER-USER - [0:0]\n-A DOCKER-USER -j RETURN\nCOMMIT\n")
		}
		f.Add(file, block)
	}
	f.Add([]byte(""), []byte(""))
	f.Add([]byte("a\nCOMMIT"), []byte("*filter\nCOMMIT"))
	f.Add([]byte("# BEGIN DARKPANEL docker\nx\n# END DARKPANEL docker"), []byte("y"))
	f.Add([]byte("# BEGIN DARKPANEL docker \nx\n# END DARKPANEL docker\n"), []byte("y\n"))
	f.Add([]byte("a\n"), []byte("# END DARKPANEL docker\n"))
	f.Fuzz(func(t *testing.T, file, block []byte) {
		_, had, scanErr := BlockOf(file)
		first, addErr := WithBlock(file, block)
		without, rmErr := WithoutBlock(file)
		if scanErr != nil {
			if addErr == nil || rmErr == nil {
				t.Fatalf("a file BlockOf refuses was edited: %q", fz7Head(file))
			}
			return
		}
		if rmErr != nil {
			t.Fatalf("WithoutBlock refused a file BlockOf accepts: %q: %v", fz7Head(file), rmErr)
		}
		if addErr != nil {
			return
		}
		got, ok, err := BlockOf(first)
		if err != nil || !ok || !bytes.Equal(got, fz7Terminated(block)) {
			t.Fatalf("BlockOf(WithBlock(%q, %q)) = %q, %v, %v", fz7Head(file), fz7Head(block), fz7Head(got), ok, err)
		}
		if again, err := WithBlock(first, block); err != nil || !bytes.Equal(again, first) {
			t.Fatalf("WithBlock is not idempotent for %q: %v", fz7Head(file), err)
		}
		want := without
		if !had {
			want = fz7Terminated(file)
		}
		if back, err := WithoutBlock(first); err != nil || !bytes.Equal(back, want) {
			t.Fatalf("WithoutBlock(WithBlock(%q, %q)) = %q, %v", fz7Head(file), fz7Head(block), fz7Head(back), err)
		}
	})
}
