package schemas_test

import (
	"fmt"
	"io/fs"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
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
