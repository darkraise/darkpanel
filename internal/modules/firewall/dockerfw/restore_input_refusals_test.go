package dockerfw

import (
	"strings"
	"testing"
)

func TestW11c11ChainsInputWithoutALoadedRuleset(t *testing.T) {
	in := string(ChainsInput([]byte("*filter\n-A darkpanel-docker-bindings -j DROP\nCOMMIT\n"), nil))
	if strings.Contains(in, "-X ") {
		t.Errorf("input deletes a chain with nothing loaded:\n%s", in)
	}
	if !strings.Contains(in, "-A darkpanel-docker-bindings -j DROP") {
		t.Errorf("input lost the block's rule:\n%s", in)
	}
}
