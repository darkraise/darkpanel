package trustedkeys

import (
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"testing"
)

// harnessKeyID is the id of the harness test key, which no production
// build may trust.
const harnessKeyID = "BCB10AB3E9AF694E"

func TestKeysAreWellFormed(t *testing.T) {
	seen := map[string]bool{}
	for _, k := range Keys() {
		raw, err := base64.StdEncoding.DecodeString(k.PublicKey)
		if err != nil || len(raw) != 42 || string(raw[:2]) != "Ed" {
			t.Errorf("key %s: public key %q is not a minisign Ed25519 key", k.ID, k.PublicKey)
			continue
		}
		if id := fmt.Sprintf("%016X", binary.LittleEndian.Uint64(raw[2:10])); id != k.ID {
			t.Errorf("key %s: its public key carries id %s", k.ID, id)
		}
		if seen[k.ID] {
			t.Errorf("key %s listed twice", k.ID)
		}
		seen[k.ID] = true
	}
}

func TestKeysReturnsACopy(t *testing.T) {
	ks := Keys()
	if len(ks) == 0 {
		t.Skip("no trusted keys in this build")
	}
	ks[0].ID = "changed"
	if Keys()[0].ID == "changed" {
		t.Error("Keys returned the package's own slice")
	}
}
