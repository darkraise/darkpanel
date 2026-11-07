package docker

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/modules/docker/logfilter"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

// fz6Head returns at most the first 64 bytes of s, for a failure message.
func fz6Head(s string) string { return s[:min(len(s), 64)] }

// fz6Scrub is c with every value that inspect must not show replaced by
// another: the value of each environment entry, each argument after the
// first and each label value outside shownLabels.
func fz6Scrub(c dockerapi.ContainerJSON) dockerapi.ContainerJSON {
	out := c
	if c.Config.Env != nil {
		out.Config.Env = make([]string, len(c.Config.Env))
		for i, e := range c.Config.Env {
			if name, _, found := strings.Cut(e, "="); found {
				e = name + "=scrubbed"
			}
			out.Config.Env[i] = e
		}
	}
	args := func(in []string, from int) []string {
		if in == nil {
			return nil
		}
		scrubbed := slices.Clone(in)
		for i := from; i < len(scrubbed); i++ {
			scrubbed[i] = "scrubbed"
		}
		return scrubbed
	}
	out.Config.Cmd, out.Config.Entrypoint = args(c.Config.Cmd, 0), args(c.Config.Entrypoint, 1)
	if c.Config.Labels != nil {
		out.Config.Labels = make(map[string]string, len(c.Config.Labels))
		for k, v := range c.Config.Labels {
			if !shownLabels[k] {
				v = "scrubbed"
			}
			out.Config.Labels[k] = v
		}
	}
	return out
}

// FuzzContainerDetail feeds arbitrary inspect JSON to containerDetailOf: it
// never panics, every environment value of the answer is the mask, every
// command element and every entrypoint element after the first is the mask,
// every label outside shownLabels has the mask for its value, and the answer
// does not change when those values are replaced by others, so no such value
// reaches any other member (R74).
func FuzzContainerDetail(f *testing.F) {
	for _, c := range []dockerapi.ContainerJSON{rdWeb(), rdWeb(rdOwned), dkContainer("bare")} {
		b, err := json.Marshal(c)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(b)
	}
	f.Add([]byte(`{"Id":"x","Created":"2026-10-01T00:00:00Z","State":{"StartedAt":"0001-01-01T00:00:00Z","FinishedAt":"0001-01-01T00:00:00Z"},` +
		`"Config":{"Env":["K=v","BARE"],"Cmd":["run","--token=secret"],"Entrypoint":["/e","x"],"Labels":{"a":"secret","com.docker.compose.project":"p"}}}`))
	f.Fuzz(func(t *testing.T, in []byte) {
		var c dockerapi.ContainerJSON
		if json.Unmarshal(in, &c) != nil {
			return
		}
		d, err := containerDetailOf(c)
		if err != nil {
			return
		}
		fail := func(what string) { t.Helper(); t.Fatalf("%s for %q", what, fz6Head(string(in))) }
		if len(d.Env) != len(c.Config.Env) {
			fail("the environment changed length")
		}
		for i, e := range d.Env {
			if name, _, _ := strings.Cut(c.Config.Env[i], "="); e.Name != name || e.Value != logfilter.Mask {
				fail("an environment entry is not name and mask")
			}
		}
		for _, p := range []struct {
			got, want   []string
			keepProgram bool
		}{{d.Cmd, c.Config.Cmd, false}, {d.Entrypoint, c.Config.Entrypoint, true}} {
			if len(p.got) != len(p.want) {
				fail("an argument list changed length")
			}
			for i := range p.got {
				if keep := p.keepProgram && i == 0; (keep && p.got[i] != p.want[i]) || (!keep && p.got[i] != logfilter.Mask) {
					fail("an argument is shown or masked wrongly")
				}
			}
		}
		if len(d.Labels) != len(c.Config.Labels) {
			fail("the labels changed length")
		}
		for k, v := range c.Config.Labels {
			want := logfilter.Mask
			if shownLabels[k] {
				want = v
			}
			if d.Labels[k] != want {
				fail("a label value is shown or masked wrongly")
			}
		}
		other, err := containerDetailOf(fz6Scrub(c))
		if err != nil || !reflect.DeepEqual(d, other) {
			fail("the answer depends on a redacted value")
		}
	})
}

// FuzzParseAPIVersion feeds an arbitrary string to parseAPIVersion: it never
// panics, and when it parses, major and minor are non-negative and print and
// parse back to the same pair (R74).
func FuzzParseAPIVersion(f *testing.F) {
	for _, s := range []string{
		"1.44", "1.49", "1.43", "", "1", "1.", ".1", "1.-0", "+1.+2", "1.2.3", " 1.2", "99999999999999999999.1",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		v, ok := parseAPIVersion(s)
		if !ok {
			return
		}
		if v[0] < 0 || v[1] < 0 {
			t.Fatalf("parseAPIVersion(%q) = %v", fz6Head(s), v)
		}
		if back, ok := parseAPIVersion(fmt.Sprintf("%d.%d", v[0], v[1])); !ok || back != v {
			t.Fatalf("parseAPIVersion(%q) = %v does not parse back: %v, %v", fz6Head(s), v, back, ok)
		}
	})
}
