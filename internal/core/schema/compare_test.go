package schema

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "compat", name+".json")) //nolint:gosec // G304: a fixture name from the test table
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestCompare(t *testing.T) {
	tests := []struct {
		fixture string
		want    string // "" when the change is allowed
	}{
		{"base", ""},
		{"new-optional-input-field", ""},
		{"new-output-field", ""},
		{"new-output-enum-value", ""},
		{"removed-field", "input/port: field removed"},
		{"renamed-field", "output/id: field removed"},
		{"changed-type", `input/port: changed type from "integer" to "string"`},
		{"changed-default", `input/mode: changed default from "safe" to "fast"`},
		{"changed-constraint", `input/name: changed pattern from "^[a-z]+$" to "^[a-z0-9]+$"`},
		{"new-required-input-field", "input/zone: new required input field"},
		{"removed-enum-value", `output/state: removed enum value "idle"`},
		{"added-input-enum-value", `input/mode: added enum value "slow" to an input`},
		{"changed-class", `class changed from "revertible" to "irreversible"`},
		{"changed-side", `side changed from "agent" to "controller"`},
		{"changed-break-glass", "breakGlass changed from false to true"},
		{"changed-secret", "input/name: changed x-secret from none to true"},
		{"changed-write-only", "input/name: changed writeOnly from none to true"},
		{"optional-became-required", "input/mode: required changed from false to true"},
	}
	base := readFixture(t, "base")
	for _, tt := range tests {
		t.Run(tt.fixture, func(t *testing.T) {
			got, err := Compare(base, readFixture(t, tt.fixture))
			if err != nil {
				t.Fatal(err)
			}
			if tt.want == "" {
				if len(got) != 0 {
					t.Errorf("violations = %q, want none", got)
				}
				return
			}
			if len(got) != 1 || got[0] != tt.want {
				t.Errorf("violations = %q, want [%q]", got, tt.want)
			}
		})
	}
}

func TestCompareRefusesDifferentActions(t *testing.T) {
	other := strings.Replace(string(readFixture(t, "base")), `"demo.item.add"`, `"demo.item.remove"`, 1)
	if _, err := Compare(readFixture(t, "base"), []byte(other)); err == nil {
		t.Error("Compare accepted snapshots of two different actions")
	}
	if _, err := Compare([]byte("{"), readFixture(t, "base")); err == nil {
		t.Error("Compare accepted invalid JSON")
	}
}

func TestCheckReleased(t *testing.T) {
	dir := t.TempDir()
	released := filepath.Join(dir, "released")
	path := "demo.item.add/v1.json"
	if err := os.MkdirAll(filepath.Join(released, "v1.0.0", "demo.item.add"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(released, "v1.0.0", path), readFixture(t, "base"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := CheckReleased(filepath.Join(dir, "absent"), nil); err != nil {
		t.Errorf("missing released directory: %v, want nil", err)
	}
	current := map[string][]byte{path: readFixture(t, "new-output-field")}
	if err := CheckReleased(released, current); err != nil {
		t.Errorf("allowed change: %v, want nil", err)
	}
	current[path] = readFixture(t, "removed-field")
	err := CheckReleased(released, current)
	if err == nil || !strings.Contains(err.Error(), "v1.0.0/demo.item.add/v1.json: input/port: field removed") {
		t.Errorf("breaking change: err = %v", err)
	}
	err = CheckReleased(released, map[string][]byte{})
	if err == nil || !strings.Contains(err.Error(), "v1.0.0/demo.item.add/v1.json: released version removed") {
		t.Errorf("removed version: err = %v", err)
	}
}
