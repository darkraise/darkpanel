package web

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io/fs"
	"os"
	"regexp"
	"strings"
	"testing"
)

// mainGrowthLimit is how far the main chunk may grow past the recorded
// baseline, gzipped: 30 KiB for M11 (spec, Global Constraints).
const mainGrowthLimit = 30 * 1024

// mainScript finds the entry chunk index.html loads.
var mainScript = regexp.MustCompile(`<script[^>]+src="/(assets/index-[^"]+\.js)"`)

// TestMainBundleSize keeps the main chunk within the baseline plus the
// growth limit, and keeps xterm in the console's lazy chunk.
func TestMainBundleSize(t *testing.T) {
	index, err := fs.ReadFile(Dist, "dist/index.html")
	if err != nil {
		if os.Getenv("DARKPANEL_REQUIRE_UI") == "1" {
			t.Fatalf("no built web UI: %v", err)
		}
		t.Skip("no built web UI; set DARKPANEL_REQUIRE_UI=1 to require one")
	}
	m := mainScript.FindSubmatch(index)
	if m == nil {
		t.Fatal("index.html loads no assets/index-*.js chunk")
	}
	chunk, err := fs.ReadFile(Dist, "dist/"+string(m[1]))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("bundle-baseline.json")
	if err != nil {
		t.Fatal(err)
	}
	var baseline struct {
		MainGzipBytes int `json:"mainGzipBytes"`
	}
	if err := json.Unmarshal(raw, &baseline); err != nil || baseline.MainGzipBytes <= 0 {
		t.Fatalf("bundle-baseline.json holds no mainGzipBytes: %v", err)
	}
	var gz bytes.Buffer
	w, _ := gzip.NewWriterLevel(&gz, gzip.BestCompression)
	_, _ = w.Write(chunk)
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if limit := baseline.MainGzipBytes + mainGrowthLimit; gz.Len() > limit {
		t.Errorf("main chunk %s is %d bytes gzipped, over the limit %d (baseline %d + %d)", m[1], gz.Len(), limit, baseline.MainGzipBytes, mainGrowthLimit)
	}
	text := string(chunk)
	for _, marker := range []string{"@xterm", "xterm-helper-textarea"} {
		if strings.Contains(text, marker) {
			t.Errorf("main chunk %s holds xterm (%q); xterm belongs in the console's lazy chunk", m[1], marker)
		}
	}
}
