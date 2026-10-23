package cli

import (
	"maps"
	"slices"
	"testing"
)

func TestModuleCommandsCoverEveryModule(t *testing.T) {
	want := []string{"cloudflare", "derp", "docker", "firewall", "npm", "panel", "tailnet", "tailscale"}
	if got := slices.Sorted(maps.Keys(moduleCommands)); !slices.Equal(got, want) {
		t.Fatalf("moduleCommands has %v, want %v", got, want)
	}
	for _, id := range want {
		if cmds := moduleCommands[id](&app{}); len(cmds) != 0 {
			t.Errorf("the %s stub returns %d commands, want none", id, len(cmds))
		}
	}
	if len(controllerCommands) != 1 {
		t.Fatalf("controllerCommands has %d entries, want 1 (backupCommands)", len(controllerCommands))
	}
	if cmds := controllerCommands[0](&app{}); len(cmds) != 0 {
		t.Errorf("the backup stub returns %d commands, want none", len(cmds))
	}
}
