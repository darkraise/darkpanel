package compose

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// fz6Head returns at most the first 64 bytes of b, for a failure message.
func fz6Head(b []byte) []byte { return b[:min(len(b), 64)] }

// FuzzParse feeds arbitrary bytes to Parse, and a configuration it accepts to
// Validate: input over MaxConfigBytes is refused, every refusal starts with
// the package name, an accepted configuration lists its keys sorted, and
// Validate over it never panics, answers only docker.owned or
// docker.compose_unavailable, and answers the same twice (R74).
func FuzzParse(f *testing.F) {
	names, err := filepath.Glob(filepath.Join("testdata", "*.json"))
	if err != nil {
		f.Fatal(err)
	}
	if len(names) == 0 {
		f.Fatal("no seed under testdata/")
	}
	for _, name := range names {
		b, err := os.ReadFile(name) //nolint:gosec // G304: a fixture under testdata/
		if err != nil {
			f.Fatal(err)
		}
		f.Add(b, "shop", false)
	}
	f.Add([]byte(`{"name":"darkpanel-x","services":{"web":{"image":"nginx","labels":{"darkpanel.module":"npm"}}}}`), "darkpanel-x", false)
	f.Add([]byte(`{"name":"x","services":{"a":{"container_name":"own-a","volumes_from":["container:own-b:ro"]}}}`), "x", false)
	f.Add([]byte(`{"name":"x"}`), "x", true)
	f.Add([]byte(`{"name":"x"} {}`), "x", false)
	f.Add([]byte(`{"services":{"a":{},"a":{}}}`), "x", false)
	f.Fuzz(func(t *testing.T, data []byte, project string, over bool) {
		if over && len(data) <= MaxConfigBytes {
			data = append(data[:len(data):len(data)], make([]byte, MaxConfigBytes+1-len(data))...)
		}
		c, err := Parse(data)
		if len(data) > MaxConfigBytes && err == nil {
			t.Fatalf("a configuration of %d bytes was accepted", len(data))
		}
		if err != nil {
			if !strings.HasPrefix(err.Error(), "compose:") {
				t.Fatalf("error %q of %q does not start with the package name", err, fz6Head(data))
			}
			return
		}
		lists := [][]string{c.TopKeys}
		for _, s := range c.Services {
			lists = append(lists, s.Keys, s.Networks, s.Volumes, s.ContainerRefs)
		}
		for _, objects := range []map[string]Object{c.Networks, c.Volumes, c.Configs, c.Secrets} {
			for _, o := range objects {
				lists = append(lists, o.Keys)
			}
		}
		for _, l := range lists {
			if !slices.IsSorted(l) {
				t.Fatalf("a key list of %q is not sorted", fz6Head(data))
			}
		}
		facts := Facts{
			ImagePresent: func(ref string) bool { return len(ref)%2 == 0 },
			Owner: func(_, name string) string {
				if strings.HasPrefix(name, "own") {
					return "npm"
				}
				return ""
			},
		}
		for _, fc := range []Facts{{}, facts} {
			err := Validate(project, c, fc)
			if err == nil {
				continue
			}
			var ce *errcode.Error
			if !errors.As(err, &ce) || (ce.Code != errcode.DockerOwned && ce.Code != errcode.DockerComposeUnavailable) {
				t.Fatalf("Validate of %q answered %v", fz6Head(data), err)
			}
			if again := Validate(project, c, fc); again == nil || again.Error() != err.Error() {
				t.Fatalf("Validate of %q answered %v, then %v", fz6Head(data), err, again)
			}
		}
	})
}
