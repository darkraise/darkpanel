package web

import (
	"bytes"
	"compress/gzip"
	"io/fs"
	"strings"
	"testing"
)

// baseMainGzip is the gzip size of the main chunk of a production build of the
// spec's base commit c07f9d60, measured when the acceptance test was written.
const baseMainGzip = 252476

func TestA74_MainBundleGrowsByAtMostThirtyKiBAndXtermIsNotInIt(t *testing.T) {
	index, err := fs.ReadFile(Dist, "dist/index.html")
	if err != nil {
		t.Skipf("no built web UI: %v", err)
	}
	m := mainScript.FindSubmatch(index)
	if m == nil {
		t.Fatal("index.html loads no assets/index-*.js chunk")
	}
	chunk, err := fs.ReadFile(Dist, "dist/"+string(m[1]))
	if err != nil {
		t.Fatal(err)
	}
	var gz bytes.Buffer
	w, _ := gzip.NewWriterLevel(&gz, gzip.BestCompression)
	_, _ = w.Write(chunk)
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if grew := gz.Len() - baseMainGzip; grew > 30*1024 {
		t.Errorf("the main chunk is %d bytes gzipped, %d over the base %d; the limit is %d", gz.Len(), grew, baseMainGzip, 30*1024)
	}
	for _, marker := range []string{"@xterm", "xterm-helper-textarea", "xterm-screen"} {
		if strings.Contains(string(chunk), marker) {
			t.Errorf("the main chunk holds xterm (%q)", marker)
		}
	}
}
