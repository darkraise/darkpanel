// Package schemas embeds the committed schema snapshots (ADR 0002), so a
// schema hash an agent reports can be mapped back to the schema it names.
package schemas

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/darkraise/darkpanel/internal/core"
)

// FS holds "<action or resource>/v<n>.json" for the current tree and
// "released/<tag>/<action or resource>/v<n>.json" for released tags. An
// embed pattern cannot exclude this file, so the snapshots are matched by
// their one-directory depth; released/README.md keeps that directory
// embeddable before any tag exists, because embedding skips hidden files.
//
//go:embed */*.json released
var FS embed.FS

// Hash is the lower-case hex SHA-256 of member re-encoded by encoding/json
// after decoding it into any: sorted keys and no whitespace, so the layout of
// a snapshot file does not change its hash.
func Hash(member json.RawMessage) (string, error) {
	var v any
	if err := json.Unmarshal(member, &v); err != nil {
		return "", fmt.Errorf("schemas: hash: %w", err)
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("schemas: hash: %w", err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// SnapshotHashes returns the hashes of a snapshot's input and output
// schemas. A resource snapshot has no input; its Input is the hash of null.
func SnapshotHashes(snapshot []byte) (core.SchemaHash, error) {
	var s struct {
		Input  json.RawMessage `json:"input"`
		Output json.RawMessage `json:"output"`
	}
	if err := json.Unmarshal(snapshot, &s); err != nil {
		return core.SchemaHash{}, fmt.Errorf("schemas: read snapshot: %w", err)
	}
	if s.Input == nil {
		s.Input = json.RawMessage("null")
	}
	in, err := Hash(s.Input)
	if err != nil {
		return core.SchemaHash{}, err
	}
	out, err := Hash(s.Output)
	if err != nil {
		return core.SchemaHash{}, err
	}
	return core.SchemaHash{Input: in, Output: out}, nil
}
