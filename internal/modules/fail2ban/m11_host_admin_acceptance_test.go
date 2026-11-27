package fail2ban_test

import (
	"context"
	"encoding/json"
	"io/fs"
	"strings"
	"sync"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules/fail2ban"
)

type events struct {
	mu  sync.Mutex
	log []string
}

func (e *events) add(s string) { e.mu.Lock(); e.log = append(e.log, s); e.mu.Unlock() }
func (e *events) index(prefix string) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	for i, s := range e.log {
		if strings.HasPrefix(s, prefix) {
			return i
		}
	}
	return -1
}

type runner struct {
	ev       *events
	crowdsec bool
}

func (r runner) Run(_ context.Context, c core.Cmd) (core.CmdResult, error) {
	r.ev.add("run " + c.Path + " " + strings.Join(c.Args, " "))
	out := ""
	if strings.HasSuffix(c.Path, "sshd") {
		out = "port 22\n"
	}
	return core.CmdResult{Stdout: []byte(out)}, nil
}
func (runner) Stream(context.Context, string, []string, func([]byte) error) error { return nil }

type files struct {
	core.FileWriter
	ev *events
}

func (f files) MakeDir(_ context.Context, _ string, dir string) error {
	f.ev.add("mkdir " + dir)
	return nil
}

func (f files) WriteFile(_ context.Context, _ string, path string, data []byte, _ fs.FileMode) error {
	f.ev.add("write " + path + "\n" + string(data))
	return nil
}

type units struct {
	core.Units
	ev *events
}

func (u units) State(_ context.Context, name string) (core.UnitState, error) {
	return core.UnitState{LoadState: "not-found", ActiveState: "inactive"}, nil
}

func (u units) Start(_ context.Context, name string) error {
	u.ev.add("start " + name)
	return nil
}

var admin = core.Principal{Actor: "user:admin", UserID: "admin", Scope: core.ScopeAdmin, Source: "192.0.2.1"}

func TestA86_TheSettingsFileExistsBeforeThePackageIsInstalled(t *testing.T) {
	ev := &events{}
	env := core.Env{HostID: "h1", Principal: admin, Exec: runner{ev: ev}, Files: files{ev: ev}, Units: units{ev: ev}, ControllerAddrs: []string{"203.0.113.9"}}
	var def core.ActionDef
	for _, a := range fail2ban.Module().Manifest().Actions {
		if a.Spec.Name == "fail2ban.package.install" {
			def = a
		}
	}
	if def.Handler.Task == nil {
		t.Fatal("fail2ban.package.install has no task")
	}
	_, err := def.Handler.Task(context.Background(), env, json.RawMessage(`{}`))
	if err != nil {
		t.Logf("task ended: %v", err)
	}
	w := ev.index("write /etc/fail2ban/jail.d/zz-darkpanel.local")
	a := -1
	ev.mu.Lock()
	for i, e := range ev.log {
		if strings.HasPrefix(e, "run ") && strings.Contains(e, "install") {
			a = i
			break
		}
	}
	ev.mu.Unlock()
	if w < 0 {
		t.Fatalf("zz-darkpanel.local was never written; events %q", ev.log)
	}
	if a < 0 {
		t.Fatalf("no install command ran; events %q", ev.log)
	}
	if w > a {
		t.Errorf("the package was installed (event %d) before the settings file was written (event %d)", a, w)
	}
	ev.mu.Lock()
	defer ev.mu.Unlock()
	body := ev.log[w]
	for _, want := range []string{"banaction = nftables-multiport", "banaction_allports = nftables-allports", "backend = systemd", "127.0.0.1/8 ::1", "100.64.0.0/10", "203.0.113.9"} {
		if !strings.Contains(body, want) {
			t.Errorf("the default settings lack %q:\n%s", want, body)
		}
	}
}

type f2bRunner struct{ ev *events }

func (r f2bRunner) Run(_ context.Context, c core.Cmd) (core.CmdResult, error) {
	r.ev.add("run " + c.Path + " " + strings.Join(c.Args, " "))
	if strings.HasSuffix(c.Path, "fail2ban-client") && len(c.Args) > 0 && c.Args[0] == "status" {
		if len(c.Args) == 1 {
			return core.CmdResult{Stdout: []byte("Status\n|- Number of jail:\t1\n`- Jail list:\tsshd\n")}, nil
		}
		return core.CmdResult{Stdout: []byte("Status for the jail: sshd\n|- Filter\n|  |- Currently failed:\t0\n|  |- Total failed:\t0\n|  `- Journal matches:\t_SYSTEMD_UNIT=ssh.service + _COMM=sshd\n`- Actions\n   |- Currently banned:\t0\n   |- Total banned:\t0\n   `- Banned IP list:\t\n")}, nil
	}
	return core.CmdResult{}, nil
}
func (f2bRunner) Stream(context.Context, string, []string, func([]byte) error) error { return nil }

type installedUnits struct{ core.Units }

func (installedUnits) State(context.Context, string) (core.UnitState, error) {
	return core.UnitState{LoadState: "loaded", ActiveState: "active", SubState: "running"}, nil
}

func banPlan(t *testing.T, addr string) error {
	t.Helper()
	ev := &events{}
	env := core.Env{HostID: "h1", Principal: admin, Exec: f2bRunner{ev: ev}, Files: files{ev: ev}, Units: installedUnits{}, ControllerAddrs: []string{"203.0.113.9"}}
	var def core.ActionDef
	for _, a := range fail2ban.Module().Manifest().Actions {
		if a.Spec.Name == "fail2ban.ban.add" {
			def = a
		}
	}
	raw, _ := json.Marshal(map[string]string{"jail": "sshd", "address": addr})
	_, err := def.Handler.Plan(context.Background(), env, raw)
	return err
}

func TestA49_A93_BanAddRefusesIgnoredAddressesAndTheCallersOwn(t *testing.T) {
	if err := banPlan(t, "203.0.113.50"); err != nil {
		t.Fatalf("the control ban of an ordinary address was refused: %v", err)
	}
	for name, addr := range map[string]string{
		"loopback":                      "127.0.0.1",
		"inside 127.0.0.0/8":            "127.5.5.5",
		"ipv6 loopback":                 "::1",
		"a controller address":          "203.0.113.9",
		"Tailscale 100.64.0.0/10":       "100.100.1.2",
		"Tailscale fd7a:115c:a1e0::/48": "fd7a:115c:a1e0::1",
		"the caller's own address":      "192.0.2.1",
	} {
		if err := banPlan(t, addr); err == nil {
			t.Errorf("%s (%s): the ban was planned", name, addr)
		}
	}
}

type crowdUnits struct{ core.Units }

func (crowdUnits) State(_ context.Context, name string) (core.UnitState, error) {
	if name == "crowdsec.service" || name == "fail2ban.service" {
		return core.UnitState{LoadState: "loaded", ActiveState: "active", SubState: "running"}, nil
	}
	return core.UnitState{LoadState: "not-found", ActiveState: "inactive"}, nil
}

func TestA50_WithCrowdSecThePageReportsItAndInstallIsRefused(t *testing.T) {
	ev := &events{}
	env := core.Env{HostID: "h1", Principal: admin, Exec: f2bRunner{ev: ev}, Files: files{ev: ev}, Units: crowdUnits{}}
	m := fail2ban.Module().Manifest()
	var install, status core.ActionDef
	for _, a := range m.Actions {
		switch a.Spec.Name {
		case "fail2ban.package.install":
			install = a
		case "fail2ban.status.get":
			status = a
		}
	}
	if _, err := install.Handler.Plan(context.Background(), env, json.RawMessage(`{}`)); err == nil || !strings.Contains(err.Error(), "CrowdSec") {
		t.Errorf("install with CrowdSec present: %v; want a refusal naming CrowdSec", err)
	}
	out, err := status.Handler.Read(context.Background(), env, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	var st struct {
		Installed bool `json:"installed"`
		CrowdSec  bool `json:"crowdsec"`
	}
	if err := json.Unmarshal(out, &st); err != nil {
		t.Fatal(err)
	}
	if !st.CrowdSec || !st.Installed {
		t.Errorf("status %s; want crowdsec true and the existing fail2ban reported installed", out)
	}
}
