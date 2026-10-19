package schemas

import (
	"encoding/json"
	"io/fs"
	"testing"
)

// userDeleteSnapshot and revokeSnapshot are copies of two committed snapshots, so a
// later change to the files under schemas/ cannot move the test vectors.
const userDeleteSnapshot = `{
  "name": "core.user.delete",
  "version": 1,
  "class": "irreversible",
  "side": "controller",
  "breakGlass": true,
  "input": {
    "$schema": "https://json-schema.org/draft/2020-12/schema",
    "$ref": "#/$defs/UsernameInput",
    "$defs": {
      "UsernameInput": {
        "properties": {
          "username": {
            "type": "string",
            "pattern": "^[a-z0-9][a-z0-9._-]{0,63}$",
            "description": "The user's name."
          }
        },
        "additionalProperties": false,
        "type": "object",
        "required": [
          "username"
        ]
      }
    }
  },
  "output": {
    "$schema": "https://json-schema.org/draft/2020-12/schema",
    "$ref": "#/$defs/Empty",
    "$defs": {
      "Empty": {
        "properties": {},
        "type": "object"
      }
    }
  }
}`

const revokeSnapshot = `{
  "name": "core.token.revoke",
  "version": 1,
  "class": "irreversible",
  "side": "controller",
  "breakGlass": false,
  "input": {
    "$schema": "https://json-schema.org/draft/2020-12/schema",
    "$ref": "#/$defs/TokenRevokeInput",
    "$defs": {
      "TokenRevokeInput": {
        "properties": {
          "id": {
            "type": "string",
            "minLength": 1,
            "description": "Id of one of the calling user's tokens."
          }
        },
        "additionalProperties": false,
        "type": "object",
        "required": [
          "id"
        ]
      }
    }
  },
  "output": {
    "$schema": "https://json-schema.org/draft/2020-12/schema",
    "$ref": "#/$defs/TokenInfo",
    "$defs": {
      "TokenInfo": {
        "properties": {
          "id": {
            "type": "string"
          },
          "name": {
            "type": "string"
          },
          "scope": {
            "type": "string"
          },
          "createdAt": {
            "type": "string",
            "format": "date-time"
          },
          "expiresAt": {
            "type": "string",
            "format": "date-time"
          },
          "lastUsedAt": {
            "oneOf": [
              {
                "type": "string",
                "format": "date-time"
              },
              {
                "type": "null"
              }
            ]
          },
          "revokedAt": {
            "oneOf": [
              {
                "type": "string",
                "format": "date-time"
              },
              {
                "type": "null"
              }
            ]
          }
        },
        "type": "object",
        "required": [
          "id",
          "name",
          "scope",
          "createdAt",
          "expiresAt",
          "lastUsedAt",
          "revokedAt"
        ]
      }
    }
  }
}`

func TestSnapshotHashes(t *testing.T) {
	tests := []struct {
		name, snapshot, input, output string
	}{
		{"core.user.delete/v1", userDeleteSnapshot,
			"d3b3f8f18795bff4bd079f933449546518a8447431dd2ccfe0ad762b157a511d",
			"66fd38057a96d6525f6628f34c25cf81ea54119c1b052af31a4f53b0208781fc"},
		{"core.token.revoke/v1", revokeSnapshot,
			"70958110f1480feebf8146303df39c9d80494e4eb6f5ec162590966eb3793c45",
			"0459807f13c2660e3255cd4a9773c4779e5b43882056eb9ecaf9e4807dd7d920"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := SnapshotHashes([]byte(tt.snapshot))
			if err != nil {
				t.Fatalf("SnapshotHashes: %v", err)
			}
			if got.Input != tt.input || got.Output != tt.output {
				t.Errorf("SnapshotHashes = %+v, want input %s, output %s", got, tt.input, tt.output)
			}
		})
	}
}

func TestHashIgnoresLayout(t *testing.T) {
	a, err := Hash(json.RawMessage(`{"b": 1, "a": [true, null]}`))
	if err != nil {
		t.Fatal(err)
	}
	b, err := Hash(json.RawMessage(`{"a":[true,null],"b":1}`))
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Errorf("Hash differs by key order and whitespace: %s, %s", a, b)
	}
	if _, err := Hash(json.RawMessage(`{`)); err == nil {
		t.Error("Hash of invalid JSON succeeded")
	}
}

func TestResourceSnapshotHashesNullInput(t *testing.T) {
	got, err := SnapshotHashes([]byte(`{"name": "x.y", "output": {"type": "object"}}`))
	if err != nil {
		t.Fatal(err)
	}
	null, err := Hash(json.RawMessage("null"))
	if err != nil {
		t.Fatal(err)
	}
	if got.Input != null {
		t.Errorf("Input = %s, want the hash of null %s", got.Input, null)
	}
	if got.Output == null || got.Output == "" {
		t.Errorf("Output = %q, want the hash of the output schema", got.Output)
	}
}

func TestFS(t *testing.T) {
	for _, name := range []string{"core.host.list/v1.json", "hostinfo.summary/v1.json", "released/README.md"} {
		if _, err := fs.Stat(FS, name); err != nil {
			t.Errorf("FS lacks %s: %v", name, err)
		}
	}
	if _, err := fs.Stat(FS, "embed.go"); err == nil {
		t.Error("FS embeds embed.go")
	}
}
