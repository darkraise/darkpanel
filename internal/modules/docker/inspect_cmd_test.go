package docker

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

const v1r13Mask = "[redacted]"

func TestV1r13InspectMasksEveryCommandElement(t *testing.T) {
	for _, tc := range []struct {
		name           string
		entrypoint     []string
		cmd            []string
		wantEntrypoint []string
		wantCmd        []string
	}{
		{"the command is the arguments of an entrypoint", []string{"/entry"}, []string{"placeholder-secret", "--port", "80"},
			[]string{"/entry"}, []string{v1r13Mask, v1r13Mask, v1r13Mask}},
		{"a command that starts with a program", nil, []string{"redis-cli", "-a", "placeholder-secret"},
			nil, []string{v1r13Mask, v1r13Mask, v1r13Mask}},
		{"a command of one element", []string{"/entry"}, []string{"placeholder-secret"},
			[]string{"/entry"}, []string{v1r13Mask}},
		{"an entrypoint keeps only its first element", []string{"/entry", "--dsn=placeholder-secret"}, []string{"serve"},
			[]string{"/entry", v1r13Mask}, []string{v1r13Mask}},
		{"an empty command", []string{"/entry"}, []string{}, []string{"/entry"}, []string{}},
		{"no command", []string{"/entry"}, nil, []string{"/entry"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := rdWeb(func(c *dockerapi.ContainerJSON) {
				c.Config.Entrypoint, c.Config.Cmd = tc.entrypoint, tc.cmd
			})
			d, err := containerDetailOf(c)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(d.Cmd, tc.wantCmd) {
				t.Errorf("cmd %q, want %q", d.Cmd, tc.wantCmd)
			}
			if !reflect.DeepEqual(d.Entrypoint, tc.wantEntrypoint) {
				t.Errorf("entrypoint %q, want %q", d.Entrypoint, tc.wantEntrypoint)
			}
			raw, err := json.Marshal(d)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(raw), "placeholder-secret") {
				t.Errorf("the answer carries the secret:\n%s", raw)
			}
		})
	}
}

func TestV1r13InspectActionShowsNoCommandElement(t *testing.T) {
	h := newDKHost(t)
	h.docker.put(rdWeb(func(c *dockerapi.ContainerJSON) {
		c.Config.Entrypoint = []string{"/entry"}
		c.Config.Cmd = []string{"placeholder-secret", "--port", "80"}
	}))
	raw, err := rdRead(t, h.dk().containerInspect(), h, `{"container":"web"}`)
	if err != nil {
		t.Fatal(err)
	}
	var out struct{ Cmd, Entrypoint []string }
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("answer is not JSON: %v\n%s", err, raw)
	}
	if want := []string{v1r13Mask, v1r13Mask, v1r13Mask}; !reflect.DeepEqual(out.Cmd, want) {
		t.Errorf("cmd %q, want %q", out.Cmd, want)
	}
	if want := []string{"/entry"}; !reflect.DeepEqual(out.Entrypoint, want) {
		t.Errorf("entrypoint %q, want %q", out.Entrypoint, want)
	}
	if strings.Contains(string(raw), "placeholder-secret") {
		t.Errorf("inspect leaks the first command argument:\n%s", raw)
	}
}
