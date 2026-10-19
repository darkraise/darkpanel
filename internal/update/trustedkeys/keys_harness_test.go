//go:build harnesskeys

package trustedkeys

import (
	"fmt"
	"os"
	"testing"

	"aead.dev/minisign"
)

func TestHarnessBuild(t *testing.T) {
	if !TestBuild() {
		t.Error("TestBuild() = false in a harnesskeys build")
	}
	if got, want := BaseURL(), "http://198.18.0.20:8000"; got != want {
		t.Errorf("BaseURL() = %q, want %q", got, want)
	}
	text, err := os.ReadFile("testdata/harness.key")
	if err != nil {
		t.Fatal(err)
	}
	var priv minisign.PrivateKey
	if err := priv.UnmarshalText(text); err != nil {
		t.Fatalf("testdata/harness.key: %v", err)
	}
	pub, ok := priv.Public().(minisign.PublicKey)
	if !ok {
		t.Fatalf("public key of type %T", priv.Public())
	}
	ks := Keys()
	if len(ks) != 1 {
		t.Fatalf("Keys() = %v, want only the harness key", ks)
	}
	if got := (Key{ID: fmt.Sprintf("%016X", pub.ID()), PublicKey: pub.String()}); ks[0] != got || got.ID != harnessKeyID {
		t.Errorf("Keys()[0] = %+v, want the public half of testdata/harness.key %+v", ks[0], got)
	}
}
