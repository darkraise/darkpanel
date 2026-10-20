package install

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// unitsByPath renders the units of role for the production layout.
func unitsByPath(t *testing.T, role Role, listen []string, advertise string) map[string]string {
	t.Helper()
	files, err := Units(Production(), role, listen, advertise)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, f := range files {
		out[f.Path] = string(f.Content)
	}
	return out
}

func wantLines(t *testing.T, unit, text string, lines ...string) {
	t.Helper()
	have := strings.Split(text, "\n")
	for _, l := range lines {
		found := false
		for _, h := range have {
			found = found || h == l
		}
		if !found {
			t.Errorf("%s lacks the line %q:\n%s", unit, l, text)
		}
	}
	if strings.Contains(text, "{{") || strings.Contains(text, "<no value>") {
		t.Errorf("%s is not fully rendered:\n%s", unit, text)
	}
}

func TestControllerUnits(t *testing.T) {
	units := unitsByPath(t, RoleController, []string{"10.77.0.10:7443", "[fd7a::1]:7443"}, "10.77.0.10:7443")
	if len(units) != 5 {
		t.Fatalf("the controller role has %d unit files, want 5", len(units))
	}
	wantLines(t, "controller", units["/etc/systemd/system/darkpanel-controller.service"],
		"User=darkpanel", "ExecStart=/usr/local/lib/darkpanel/darkpanel serve", "RuntimeDirectory=darkpanel-controller",
		"Restart=on-failure", "RestartSec=2", "StartLimitIntervalSec=60", "StartLimitBurst=5", "TimeoutStartSec=30s",
		"TimeoutStopSec=10s", "OnFailure=darkpanel-rollback.service", "NoNewPrivileges=yes", "ProtectSystem=strict",
		"ReadWritePaths=/var/lib/darkpanel/controller", "PrivateTmp=yes", "WantedBy=multi-user.target")
	wantLines(t, "agent", units["/etc/systemd/system/darkpanel-agent.service"],
		"ExecStart=/usr/local/lib/darkpanel/darkpanel agent run", "RuntimeDirectory=darkpanel-agent", "RuntimeDirectoryMode=0700",
		"Restart=on-failure", "RestartSec=2", "StartLimitIntervalSec=60", "StartLimitBurst=5", "TimeoutStartSec=30s",
		"TimeoutStopSec=10s", "OnFailure=darkpanel-rollback.service", "WantedBy=multi-user.target")
	if strings.Contains(units["/etc/systemd/system/darkpanel-agent.service"], "User=") {
		t.Error("the agent unit names a user; the agent runs as root")
	}
	wantLines(t, "rollback", units["/etc/systemd/system/darkpanel-rollback.service"],
		"Type=oneshot", "ExecStart=/usr/local/lib/darkpanel/darkpanel.recovery update recover", "TimeoutStartSec=30s",
		"ConditionPathExists=/usr/local/lib/darkpanel/darkpanel.recovery")
	if strings.Contains(units["/etc/systemd/system/darkpanel-rollback.service"], "[Install]") {
		t.Error("the rollback unit can be enabled; only OnFailure= may start it")
	}
	wantLines(t, "recover", units["/etc/systemd/system/darkpanel-update-recover.service"],
		"Type=oneshot", "ExecStart=/usr/local/lib/darkpanel/darkpanel.recovery update recover --boot", "TimeoutStartSec=30s",
		"ConditionPathExists=/usr/local/lib/darkpanel/darkpanel.recovery",
		"Before=darkpanel-controller.service darkpanel-agent.service", "WantedBy=multi-user.target")
	wantLines(t, "drop-in", units["/etc/systemd/system/darkpanel-controller.service.d/10-agent.conf"],
		"[Service]", "ExecStart=",
		"ExecStart=/usr/local/lib/darkpanel/darkpanel serve --agent-listen 10.77.0.10:7443 --agent-listen [fd7a::1]:7443 --agent-advertise 10.77.0.10:7443")
}

func TestControllerDropInWithoutFlags(t *testing.T) {
	units := unitsByPath(t, RoleController, nil, "")
	wantLines(t, "drop-in", units["/etc/systemd/system/darkpanel-controller.service.d/10-agent.conf"],
		"ExecStart=", "ExecStart=/usr/local/lib/darkpanel/darkpanel serve")
}

func TestAgentRoleUnits(t *testing.T) {
	units := unitsByPath(t, RoleAgent, nil, "")
	var got []string
	for p := range units {
		got = append(got, filepath.Base(p))
	}
	for _, name := range []string{"darkpanel-agent.service", "darkpanel-rollback.service", "darkpanel-update-recover.service"} {
		if _, ok := units["/etc/systemd/system/"+name]; !ok {
			t.Errorf("the agent role lacks %s (has %v)", name, got)
		}
	}
	if len(units) != 3 {
		t.Errorf("the agent role writes %v, want three units", got)
	}
	if _, err := Units(Production(), RoleAgent, []string{"10.0.0.1:7443"}, ""); err == nil {
		t.Error("the agent role accepted --agent-listen")
	}
	if _, err := Units(Production(), RoleAgent, nil, "10.0.0.1:7443"); err == nil {
		t.Error("the agent role accepted --agent-advertise")
	}
}

func TestRootedUnitsUseRootedPaths(t *testing.T) {
	root := t.TempDir()
	files, err := Units(Rooted(root), RoleController, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if !strings.HasPrefix(f.Path, root+"/") {
			t.Errorf("unit %s is outside %s", f.Path, root)
		}
		if strings.Contains(string(f.Content), "/usr/local/lib/darkpanel/darkpanel") && !strings.Contains(string(f.Content), root) {
			t.Errorf("%s runs the production binary under a rooted layout", f.Path)
		}
	}
}

func TestCheckAgentAddress(t *testing.T) {
	for _, ok := range []string{"10.77.0.10:7443", "[fd7a:115c::1]:7443", "controller.example.com:443", "h:1", "h:65535"} {
		if err := CheckAgentAddress(ok); err != nil {
			t.Errorf("CheckAgentAddress(%q) = %v", ok, err)
		}
	}
	for _, bad := range []string{"", "10.0.0.1", ":7443", "h:0", "h:65536", "h:port", "a b:1", "h:1;rm", "h:1\n", "$(id):1", "[::1]"} {
		if err := CheckAgentAddress(bad); err == nil {
			t.Errorf("CheckAgentAddress(%q) succeeded", bad)
		}
	}
	if _, err := Units(Production(), RoleController, []string{"h:1 --dev"}, ""); err == nil {
		t.Error("Units accepted a listen address carrying a flag")
	}
}

func TestEnabledUnits(t *testing.T) {
	if got, want := EnabledUnits(RoleController), []string{"darkpanel-controller.service", "darkpanel-agent.service", "darkpanel-update-recover.service"}; !reflect.DeepEqual(got, want) {
		t.Errorf("controller role enables %v, want %v", got, want)
	}
	if got, want := EnabledUnits(RoleAgent), []string{"darkpanel-agent.service", "darkpanel-update-recover.service"}; !reflect.DeepEqual(got, want) {
		t.Errorf("agent role enables %v, want %v", got, want)
	}
}

func TestWriteIfChanged(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "sub", "unit.service")
	for i, tt := range []struct {
		data  string
		wrote bool
	}{{"one\n", true}, {"one\n", false}, {"two\n", true}} {
		wrote, err := writeIfChanged(p, []byte(tt.data))
		if err != nil || wrote != tt.wrote {
			t.Fatalf("write %d: wrote %v, %v; want %v", i, wrote, err, tt.wrote)
		}
		if b, err := os.ReadFile(p); err != nil || string(b) != tt.data { //nolint:gosec // G304: a test's temporary path
			t.Fatalf("write %d: file holds %q, %v", i, b, err)
		}
	}
	info, err := os.Stat(p)
	if err != nil || info.Mode().Perm() != 0o644 {
		t.Errorf("mode %v, %v; want 0644", info.Mode().Perm(), err)
	}
	entries, err := os.ReadDir(filepath.Dir(p))
	if err != nil || len(entries) != 1 {
		t.Errorf("directory holds %v, %v; want only the unit, no temporary file", entries, err)
	}
}
