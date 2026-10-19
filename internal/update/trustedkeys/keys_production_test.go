//go:build !harnesskeys

package trustedkeys

import "testing"

func TestProductionBuild(t *testing.T) {
	if TestBuild() {
		t.Error("TestBuild() = true in a build without the harnesskeys tag")
	}
	if got, want := BaseURL(), "https://github.com/Darkraise/darkpanel/releases/download"; got != want {
		t.Errorf("BaseURL() = %q, want %q", got, want)
	}
	for _, k := range Keys() {
		if k.ID == harnessKeyID {
			t.Errorf("the production build trusts the harness test key %s", k.ID)
		}
	}
}
