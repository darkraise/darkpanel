package main

import (
	"bytes"
	"encoding/json"
	"runtime"
	"testing"
	"testing/fstest"

	"github.com/darkraise/darkpanel/internal/buildinfo"
	"github.com/darkraise/darkpanel/internal/cli"
)

func runRoot(t *testing.T, args ...string) string {
	t.Helper()
	var out bytes.Buffer
	if code := run(args, cli.Options{Stdout: &out, Stderr: &out}); code != cli.ExitOK {
		t.Fatalf("darkpanel %v: exit %d: %s", args, code, out.String())
	}
	return out.String()
}

func TestVersionText(t *testing.T) {
	got := runRoot(t, "version")
	want := "darkpanel dev (commit unknown, " + runtime.Version() + ", " + runtime.GOOS + "/" + runtime.GOARCH + ")\n"
	if got != want {
		t.Errorf("version output = %q, want %q", got, want)
	}
}

func TestVersionJSON(t *testing.T) {
	var got map[string]any
	if err := json.Unmarshal([]byte(runRoot(t, "version", "--json")), &got); err != nil {
		t.Fatalf("version --json is not a JSON object: %v", err)
	}
	want := map[string]string{
		"version": "dev",
		"commit":  "unknown",
		"go":      runtime.Version(),
		"os":      runtime.GOOS,
		"arch":    runtime.GOARCH,
	}
	for key, value := range want {
		if got[key] != value {
			t.Errorf("version --json %q = %v, want %q", key, got[key], value)
		}
	}
	// The value depends on whether the web build has run, so only its type is fixed.
	if _, ok := got["ui"].(bool); !ok {
		t.Errorf("version --json \"ui\" = %v, want a boolean", got["ui"])
	}
	if len(got) != len(want)+1 {
		t.Errorf("version --json has keys %v, want exactly version, commit, go, os, arch, ui", got)
	}
}

func TestVersionReadsBuildinfo(t *testing.T) {
	oldVersion, oldCommit := buildinfo.Version, buildinfo.Commit
	t.Cleanup(func() { buildinfo.Version, buildinfo.Commit = oldVersion, oldCommit })
	buildinfo.Version, buildinfo.Commit = "v9.9.9", "abc1234"
	var got map[string]any
	if err := json.Unmarshal([]byte(runRoot(t, "version", "--json")), &got); err != nil {
		t.Fatal(err)
	}
	if got["version"] != "v9.9.9" || got["commit"] != "abc1234" {
		t.Errorf("version --json = %v, want version v9.9.9 and commit abc1234 from buildinfo", got)
	}
}

func TestUIEmbedded(t *testing.T) {
	tests := []struct {
		name string
		dist fstest.MapFS
		want bool
	}{
		{"built", fstest.MapFS{"dist/index.html": {Data: []byte("<html></html>")}, "dist/.gitkeep": {}}, true},
		{"placeholder only", fstest.MapFS{"dist/.gitkeep": {}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := uiEmbedded(tt.dist); got != tt.want {
				t.Errorf("uiEmbedded() = %v, want %v", got, tt.want)
			}
		})
	}
}
