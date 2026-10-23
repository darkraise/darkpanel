package schemas

import (
	"encoding/json"
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

// StreamHash is the lower-case hex SHA-256 of {"params": <input schema of
// ParamsType>, "event": <output schema of EventType>} re-encoded by
// encoding/json. The controller relays a stream only when the agent reports
// the hash of its own registry's definition.
func StreamHash(def core.StreamDef) (string, error) {
	params, err := schema.Input(def.ParamsType)
	if err != nil {
		return "", fmt.Errorf("schemas: stream %s params: %w", def.Name, err)
	}
	event, err := schema.Output(def.EventType)
	if err != nil {
		return "", fmt.Errorf("schemas: stream %s event: %w", def.Name, err)
	}
	doc, err := json.Marshal(map[string]json.RawMessage{"params": params, "event": event})
	if err != nil {
		return "", fmt.Errorf("schemas: stream %s: %w", def.Name, err)
	}
	return Hash(doc)
}
