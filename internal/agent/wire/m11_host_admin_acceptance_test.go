package wire_test

import (
	"bytes"
	"encoding/json"
	"maps"
	"slices"
	"testing"

	"github.com/darkraise/darkpanel/internal/agent/wire"
	"github.com/darkraise/darkpanel/internal/core"
)

func keys(t *testing.T, v any) []string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return slices.Sorted(maps.Keys(m))
}

func TestA8_HelloIsUnchanged(t *testing.T) {
	connected := true
	h := wire.FromHello(core.AgentHello{
		ProtocolVersion: 1, BinaryVersion: "v", OS: "linux", Arch: "amd64", HostID: "h", JournalEpoch: "e", ControllerConnected: &connected,
		Modules:      []core.ModuleStatus{{ID: "console", Version: 1, Available: true}},
		SchemaHashes: map[string]core.SchemaHash{"a@1": {Input: "i", Output: "o"}},
	})
	want := []string{"arch", "binaryVersion", "controllerConnected", "hostId", "journalEpoch", "modules", "os", "protocolVersion", "schemaHashes"}
	if got := keys(t, h); !slices.Equal(got, want) {
		t.Errorf("hello fields %v; want exactly %v", got, want)
	}
	if got := keys(t, h.Modules[0]); !slices.Equal(got, []string{"available", "id", "reason", "version"}) {
		t.Errorf("module status fields %v", got)
	}
	if got := keys(t, h.SchemaHashes["a@1"]); !slices.Equal(got, []string{"input", "output"}) {
		t.Errorf("schema hash fields %v", got)
	}
	if h.ProtocolVersion != 1 {
		t.Errorf("protocol version %d; want 1", h.ProtocolVersion)
	}
}

func TestA11_FramesOverThirtyTwoKiBAndUnknownTypesAreRefused(t *testing.T) {
	var buf = new(bytes.Buffer)
	big := make([]byte, 32<<10+1)
	if err := wire.WriteFrame(buf, wire.FrameData, big); err == nil {
		t.Error("a 32 KiB + 1 byte frame was written")
	}
	buf = new(bytes.Buffer)
	if err := wire.WriteFrame(buf, wire.FrameData, make([]byte, 32<<10)); err != nil {
		t.Errorf("a 32 KiB frame: %v", err)
	}
	buf = new(bytes.Buffer)
	if err := wire.WriteFrame(buf, 9, []byte("x")); err == nil {
		t.Error("an unknown frame type was written")
	}
	// A peer that sends them anyway: a frame of an unknown type, and one longer than 32 KiB.
	for name, raw := range map[string][]byte{
		"unknown type": {9, 0, 0, 0, 1, 'x'},
		"too long":     append([]byte{1, 0, 0, 0x80, 0x01}, make([]byte, 32<<10+1)...),
	} {
		if _, _, err := wire.ReadFrame(bytes.NewReader(raw)); err == nil {
			t.Errorf("%s: a frame the peer sent was accepted", name)
		}
	}
}
