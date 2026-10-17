package paths

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestResolve(t *testing.T) {
	noHome := func() (string, error) { return "", errors.New("no home") }
	home := func() (string, error) { return "/home/ada", nil }
	tests := []struct {
		name string
		dev  bool
		env  map[string]string
		home func() (string, error)
		want Paths
	}{
		{"production ignores the environment", false, map[string]string{"XDG_STATE_HOME": "/s", "XDG_RUNTIME_DIR": "/r"}, noHome, Paths{
			ControllerDir:    "/var/lib/darkpanel/controller",
			AgentDir:         "/var/lib/darkpanel/agent",
			ControllerKey:    "/etc/darkpanel/controller.key",
			AgentKey:         "/etc/darkpanel/agent.key",
			ControllerSocket: "/run/darkpanel-controller/controller.sock",
		}},
		{"dev with both variables", true, map[string]string{"XDG_STATE_HOME": "/s", "XDG_RUNTIME_DIR": "/r"}, noHome, Paths{
			ControllerDir:    "/s/darkpanel-dev/controller",
			AgentDir:         "/s/darkpanel-dev/agent",
			ControllerKey:    "/s/darkpanel-dev/keys/controller.key",
			AgentKey:         "/s/darkpanel-dev/keys/agent.key",
			ControllerSocket: "/r/darkpanel-dev/controller.sock",
		}},
		{"dev defaults", true, map[string]string{}, home, Paths{
			ControllerDir:    "/home/ada/.local/state/darkpanel-dev/controller",
			AgentDir:         "/home/ada/.local/state/darkpanel-dev/agent",
			ControllerKey:    "/home/ada/.local/state/darkpanel-dev/keys/controller.key",
			AgentKey:         "/home/ada/.local/state/darkpanel-dev/keys/agent.key",
			ControllerSocket: "/home/ada/.local/state/darkpanel-dev/run/controller.sock",
		}},
		{"dev ignores relative values", true, map[string]string{"XDG_STATE_HOME": "rel", "XDG_RUNTIME_DIR": "rel"}, home, Paths{
			ControllerDir:    "/home/ada/.local/state/darkpanel-dev/controller",
			AgentDir:         "/home/ada/.local/state/darkpanel-dev/agent",
			ControllerKey:    "/home/ada/.local/state/darkpanel-dev/keys/controller.key",
			AgentKey:         "/home/ada/.local/state/darkpanel-dev/keys/agent.key",
			ControllerSocket: "/home/ada/.local/state/darkpanel-dev/run/controller.sock",
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolve(tt.dev, func(k string) string { return tt.env[k] }, tt.home)
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			if got != tt.want {
				t.Errorf("resolve = %+v\nwant %+v", got, tt.want)
			}
		})
	}
}

func TestResolveDevWithoutHome(t *testing.T) {
	if _, err := resolve(true, func(string) string { return "" }, func() (string, error) { return "", errors.New("no home") }); err == nil {
		t.Fatal("resolve succeeded without XDG_STATE_HOME or a home directory")
	}
}

func TestDerivedPaths(t *testing.T) {
	p := Under("/x")
	for got, want := range map[string]string{
		p.ControllerDB():      "/x/controller/controller.db",
		p.AgentDB():           "/x/agent/agent.db",
		p.ControllerBackups(): "/x/controller/backups",
		p.AgentBackups():      "/x/agent/backups",
	} {
		if got != want {
			t.Errorf("got %s, want %s", got, want)
		}
	}
}

func TestMkdirAll(t *testing.T) {
	p := Under(filepath.Join(t.TempDir(), "dp"))
	if err := p.MkdirAll(); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	for _, d := range []string{p.ControllerDir, p.AgentDir, filepath.Dir(p.ControllerKey), filepath.Dir(p.ControllerSocket)} {
		info, err := os.Stat(d)
		if err != nil {
			t.Fatal(err)
		}
		if !info.IsDir() || info.Mode().Perm() != 0o700 {
			t.Errorf("%s: dir %v mode %o, want a 0700 directory", d, info.IsDir(), info.Mode().Perm())
		}
	}
	if err := p.MkdirAll(); err != nil {
		t.Errorf("second MkdirAll: %v", err)
	}
}
