package schemas

import (
	"fmt"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/schema"
)

// HashKey is the key of an action version in AgentHello.SchemaHashes:
// "<action name>@<version>".
func HashKey(ref core.ActionRef) string {
	return fmt.Sprintf("%s@%d", ref.Name, ref.Version)
}

// ActionHash returns the schema hashes of one action version. It hashes the
// snapshot schema.ActionSnapshot renders rather than the embedded file, so an
// action without a committed snapshot, such as a test module's, still has a
// hash; for a registered action the two are the same bytes, which
// TestSnapshotsCurrent enforces.
func ActionHash(def core.ActionDef) (core.SchemaHash, error) {
	snap, err := schema.ActionSnapshot(def)
	if err != nil {
		return core.SchemaHash{}, fmt.Errorf("schemas: snapshot %s: %w", def.Ref(), err)
	}
	return SnapshotHashes(snap)
}

// ActionHashes returns the hashes of every action version of side in reg,
// keyed by HashKey.
func ActionHashes(reg *core.Registry, side core.Side) (map[string]core.SchemaHash, error) {
	out := map[string]core.SchemaHash{}
	for _, def := range reg.Actions() {
		if def.Spec.Side != side {
			continue
		}
		h, err := ActionHash(def)
		if err != nil {
			return nil, err
		}
		out[HashKey(def.Ref())] = h
	}
	return out, nil
}
