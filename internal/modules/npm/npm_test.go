package npm

import (
	"io/fs"
	"reflect"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
)

func TestSdManifest(t *testing.T) {
	m := Module().Manifest()
	if m.ID != ID || m.Version != 1 || !m.DefaultEnabled {
		t.Errorf("manifest %q version %d default enabled %t, want npm 1 true", m.ID, m.Version, m.DefaultEnabled)
	}
	if !reflect.DeepEqual(m.OwnedPaths, ownedPaths()) || len(m.OwnedPaths) != 1 || m.OwnedPaths[0].Path != backupDir {
		t.Errorf("owned paths %+v, want the backup directory", m.OwnedPaths)
	}
	if len(m.Migrations) != 1 || m.Migrations[core.SideAgent] == nil {
		t.Fatalf("migrations %v, want the agent side only", m.Migrations)
	}
	if _, err := fs.Stat(m.Migrations[core.SideAgent], "0001_npm.sql"); err != nil {
		t.Errorf("the agent migrations lack 0001_npm.sql: %v", err)
	}
	if _, err := core.NewRegistry(Module()); err != nil {
		t.Errorf("the module is not accepted by the registry: %v", err)
	}
}
