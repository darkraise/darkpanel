package install

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/paths"
)

func TestParseRole(t *testing.T) {
	for _, s := range []string{"controller", "agent"} {
		if r, err := ParseRole(s); err != nil || string(r) != s {
			t.Errorf("ParseRole(%q) = %q, %v", s, r, err)
		}
	}
	for _, s := range []string{"", "Agent", "both"} {
		if _, err := ParseRole(s); err == nil {
			t.Errorf("ParseRole(%q) succeeded", s)
		}
	}
}

func TestProductionLayout(t *testing.T) {
	l := Production()
	if l.Paths != paths.Production() {
		t.Errorf("Paths = %+v, want paths.Production()", l.Paths)
	}
	if l.DataDir != "/var/lib/darkpanel" || l.ConfigDir != "/etc/darkpanel" || l.UnitDir != "/etc/systemd/system" {
		t.Errorf("layout = %+v", l)
	}
	if got := l.DropInPath(); got != "/etc/systemd/system/darkpanel-controller.service.d/10-agent.conf" {
		t.Errorf("DropInPath = %s", got)
	}
	// Every data path lies under DataDir and every key or credential under
	// ConfigDir, which uninstall --keep-data relies on.
	for _, d := range []string{l.Paths.ControllerDir, l.Paths.AgentDir, l.Paths.UpdateDir} {
		if !strings.HasPrefix(d, l.DataDir+"/") {
			t.Errorf("%s is not under %s", d, l.DataDir)
		}
	}
	for _, d := range []string{l.Paths.ControllerKey, l.Paths.AgentKey, l.Paths.AgentTLSDir} {
		if !strings.HasPrefix(d, l.ConfigDir+"/") {
			t.Errorf("%s is not under %s", d, l.ConfigDir)
		}
	}
}

func TestRootedLayout(t *testing.T) {
	root := t.TempDir()
	got, prod := Rooted(root), Production()
	pairs := [][2]string{
		{got.Paths.ControllerDir, prod.Paths.ControllerDir}, {got.Paths.AgentDir, prod.Paths.AgentDir},
		{got.Paths.ControllerKey, prod.Paths.ControllerKey}, {got.Paths.AgentKey, prod.Paths.AgentKey},
		{got.Paths.ControllerSocket, prod.Paths.ControllerSocket}, {got.Paths.AgentSocket, prod.Paths.AgentSocket},
		{got.Paths.AgentTLSDir, prod.Paths.AgentTLSDir}, {got.Paths.UpdateDir, prod.Paths.UpdateDir},
		{got.Paths.BinaryDir, prod.Paths.BinaryDir}, {got.Paths.BinaryLink, prod.Paths.BinaryLink},
		{got.DataDir, prod.DataDir}, {got.ConfigDir, prod.ConfigDir}, {got.UnitDir, prod.UnitDir},
	}
	for _, p := range pairs {
		if p[0] != filepath.Join(root, p[1]) {
			t.Errorf("rooted %s, want %s under %s", p[0], p[1], root)
		}
	}
}

// TestRootedCoversEveryPath catches a field added to paths.Paths that Rooted
// does not move under the root, which would point tests at the real host.
func TestRootedCoversEveryPath(t *testing.T) {
	root := t.TempDir()
	l := Rooted(root)
	check := func(name, p string) {
		if !strings.HasPrefix(p, root+"/") {
			t.Errorf("%s = %s, not under %s", name, p, root)
		}
	}
	v := reflect.ValueOf(l.Paths)
	for i := range v.NumField() {
		if f := v.Type().Field(i); f.Type.Kind() == reflect.String {
			check("Paths."+f.Name, v.Field(i).String())
		}
	}
	check("DataDir", l.DataDir)
	check("ConfigDir", l.ConfigDir)
	check("UnitDir", l.UnitDir)
}

func TestOwnedPaths(t *testing.T) {
	l := Production()
	agent := OwnedPaths(l, RoleAgent)
	controller := OwnedPaths(l, RoleController)
	if !reflect.DeepEqual(controller[:len(agent)], agent) {
		t.Fatal("the controller role's paths do not start with the agent role's")
	}
	want := map[string]string{
		"/etc/systemd/system/darkpanel-agent.service":                      core.OwnedUnit,
		"/etc/systemd/system/darkpanel-rollback.service":                   core.OwnedUnit,
		"/etc/systemd/system/darkpanel-update-recover.service":             core.OwnedUnit,
		"/usr/local/lib/darkpanel":                                         core.OwnedDir,
		"/usr/local/bin/darkpanel":                                         core.OwnedFile,
		"/var/lib/darkpanel":                                               core.OwnedDir,
		"/etc/darkpanel":                                                   core.OwnedDir,
		"/var/lib/darkpanel/agent":                                         core.OwnedDir,
		"/var/lib/darkpanel/update":                                        core.OwnedDir,
		"/etc/darkpanel/agent":                                             core.OwnedDir,
		"/etc/darkpanel/agent.key":                                         core.OwnedFile,
		"/etc/systemd/system/darkpanel-controller.service":                 core.OwnedUnit,
		"/etc/systemd/system/darkpanel-controller.service.d":               core.OwnedDir,
		"/etc/systemd/system/darkpanel-controller.service.d/10-agent.conf": core.OwnedUnit,
		"/var/lib/darkpanel/controller":                                    core.OwnedDir,
		"/etc/darkpanel/controller.key":                                    core.OwnedFile,
		"darkpanel":                                                        core.OwnedUser,
	}
	if len(controller) != len(want) {
		t.Errorf("controller role owns %d paths, want %d", len(controller), len(want))
	}
	for _, op := range controller {
		if want[op.Path] != op.Kind {
			t.Errorf("%s has kind %q, want %q", op.Path, op.Kind, want[op.Path])
		}
		if len(op.BlockIDs) != 0 {
			t.Errorf("%s declares blocks", op.Path)
		}
	}
	for _, op := range agent {
		if op.Kind == core.OwnedUser || strings.Contains(op.Path, "controller") {
			t.Errorf("the agent role owns %s", op.Path)
		}
	}
	if !reflect.DeepEqual(CoreOwnedPaths(), controller) {
		t.Error("CoreOwnedPaths differs from the production controller role's paths")
	}
}
