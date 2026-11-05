package platform

import (
	"bytes"
	"testing"
)

// fz5Head returns at most the first 64 bytes of b, for a failure message.
func fz5Head(b []byte) []byte { return b[:min(len(b), 64)] }

// FuzzMarkedBlocks feeds arbitrary file content and block bodies to
// scanMarkers, replaceBlock, removeBlock and splitLines: joining the lines of
// a file gives the file back; a file scanMarkers refuses is refused by both
// edits; replaceBlock's output passes scanMarkers and holds the block, and
// replacing again with the same body changes nothing; removing a block that
// replaceBlock just added to a file without one gives the file back, with the
// final newline replaceBlock supplies (R74).
func FuzzMarkedBlocks(f *testing.F) {
	const module, blockID = "fw", "rules"
	f.Add([]byte(""), []byte("allow 22\n"), uint8(0))
	f.Add([]byte("a"), []byte("x"), uint8(0))
	f.Add([]byte("a\n# BEGIN DARKPANEL fw/rules\nold\n# END DARKPANEL fw/rules\nb\n"), []byte("new\n"), uint8(0))
	f.Add([]byte("# BEGIN DARKPANEL fw/other\nkeep\n# END DARKPANEL fw/other\n"), []byte("x\n"), uint8(1))
	f.Add([]byte("// BEGIN DARKPANEL fw/rules\n// END DARKPANEL fw/rules"), []byte(""), uint8(1))
	f.Add([]byte("# BEGIN DARKPANEL fw/rules\n"), []byte("x\n"), uint8(0))
	f.Add([]byte("# BEGIN DARKPANEL fw/rules \n# END DARKPANEL fw/rules\n"), []byte(nil), uint8(0))
	f.Add([]byte("a\n"), []byte("# END DARKPANEL fw/rules\n"), uint8(0))
	f.Fuzz(func(t *testing.T, content, body []byte, which uint8) {
		prefix := []string{"#", "//", ";"}[int(which)%3]
		id := module + "/" + blockID
		lines := splitLines(content)
		if !bytes.Equal(bytes.Join(lines, nil), content) {
			t.Fatalf("splitLines does not join back to %q", fz5Head(content))
		}
		spans, scanErr := scanMarkers(lines, prefix)
		_, had := spans[id]
		added, addErr := replaceBlock(content, prefix, module, blockID, body)
		removed, rmErr := removeBlock(content, prefix, module, blockID)
		if scanErr != nil {
			if addErr == nil || rmErr == nil {
				t.Fatalf("a file scanMarkers refuses was edited: %q", fz5Head(content))
			}
			return
		}
		if rmErr != nil {
			t.Fatalf("removeBlock refused a file scanMarkers accepts: %q: %v", fz5Head(content), rmErr)
		}
		if rs, err := scanMarkers(splitLines(removed), prefix); err != nil {
			t.Fatalf("removeBlock left markers scanMarkers refuses: %q", fz5Head(content))
		} else if _, ok := rs[id]; ok {
			t.Fatalf("removeBlock left the block in %q", fz5Head(content))
		}
		if addErr != nil {
			return
		}
		if as, err := scanMarkers(splitLines(added), prefix); err != nil {
			t.Fatalf("replaceBlock output is refused by scanMarkers: %q", fz5Head(content))
		} else if _, ok := as[id]; !ok {
			t.Fatalf("replaceBlock output lacks the block: %q", fz5Head(content))
		}
		again, err := replaceBlock(added, prefix, module, blockID, body)
		if err != nil || !bytes.Equal(again, added) {
			t.Fatalf("replaceBlock is not idempotent for %q: %v", fz5Head(content), err)
		}
		if had {
			return
		}
		if !bytes.HasPrefix(added, content) {
			t.Fatalf("replaceBlock changed the file it appended to: %q", fz5Head(content))
		}
		want := content
		if len(content) > 0 && content[len(content)-1] != '\n' {
			want = append(bytes.Clone(content), '\n')
		}
		if back, err := removeBlock(added, prefix, module, blockID); err != nil || !bytes.Equal(back, want) {
			t.Fatalf("removing the added block does not give %q back: %v", fz5Head(content), err)
		}
	})
}
