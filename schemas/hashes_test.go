package schemas_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"reflect"
	"regexp"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/schema"
	"github.com/darkraise/darkpanel/internal/registry"
	"github.com/darkraise/darkpanel/schemas"
)

func TestHashKey(t *testing.T) {
	if got := schemas.HashKey(core.ActionRef{Name: "hostinfo.summary", Version: 2}); got != "hostinfo.summary@2" {
		t.Errorf("HashKey = %q, want hostinfo.summary@2", got)
	}
}

// TestActionHashesMatchTheEmbeddedSnapshots shows that a hash computed from an
// action definition names the committed snapshot the controller looks up.
func TestActionHashesMatchTheEmbeddedSnapshots(t *testing.T) {
	reg, err := registry.Build()
	if err != nil {
		t.Fatal(err)
	}
	for _, side := range []core.Side{core.SideAgent, core.SideController} {
		hashes, err := schemas.ActionHashes(reg, side)
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, def := range reg.Actions() {
			if def.Spec.Side != side {
				continue
			}
			n++
			file, err := fs.ReadFile(schemas.FS, fmt.Sprintf("%s/v%d.json", def.Spec.Name, def.Spec.Version))
			if err != nil {
				t.Fatal(err)
			}
			want, err := schemas.SnapshotHashes(file)
			if err != nil {
				t.Fatal(err)
			}
			if got := hashes[schemas.HashKey(def.Ref())]; got != want {
				t.Errorf("%s: hash %+v, want the embedded snapshot's %+v", def.Ref(), got, want)
			}
		}
		// The agent side has no action until the update actions are
		// registered, so only the controller side must be non-empty.
		if len(hashes) != n || side == core.SideController && n == 0 {
			t.Errorf("side %s: %d hashes for %d actions", side, len(hashes), n)
		}
	}
}

type strmParams struct {
	Container string `json:"container"`
}

type strmEvent struct {
	Line string `json:"line"`
}

type strmEventPlus struct {
	Line   string `json:"line"`
	Stream string `json:"stream"`
}

func TestStreamHash(t *testing.T) {
	logs := core.DefineStream("logs", func(context.Context, core.Env, strmParams, func(strmEvent) error) error { return nil })
	hash, err := schemas.StreamHash(logs)
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(hash) {
		t.Errorf("StreamHash = %q, want 64 lower-case hex digits", hash)
	}
	params, err := schema.Input(reflect.TypeFor[strmParams]())
	if err != nil {
		t.Fatal(err)
	}
	event, err := schema.Output(reflect.TypeFor[strmEvent]())
	if err != nil {
		t.Fatal(err)
	}
	want, err := schemas.Hash(json.RawMessage(`{"params":` + string(params) + `,"event":` + string(event) + `}`))
	if err != nil || hash != want {
		t.Errorf("StreamHash = %s, %v; want the hash of {params, event}, %s", hash, err, want)
	}
	if again, err := schemas.StreamHash(logs); err != nil || again != hash {
		t.Errorf("StreamHash again = %s, %v; want %s", again, err, hash)
	}
	wider := core.DefineStream("logs", func(context.Context, core.Env, strmParams, func(strmEventPlus) error) error { return nil })
	if other, err := schemas.StreamHash(wider); err != nil || other == hash {
		t.Errorf("StreamHash of an event with one more field = %s, %v; want a different hash", other, err)
	}
	if _, err := schemas.StreamHash(core.StreamDef{Name: "broken"}); err == nil {
		t.Error("StreamHash of a def without types succeeded")
	}
}
