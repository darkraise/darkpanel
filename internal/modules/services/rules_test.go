package services

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

var allOps = []op{opStart, opStop, opRestart, opReload, opEnable, opDisable}

func TestRefusals(t *testing.T) {
	requires := func(deps ...string) func(*core.UnitInfo) { return func(u *core.UnitInfo) { u.Requires = deps } }
	reboots := func(u *core.UnitInfo) { u.SuccessAction = "reboot" }
	tests := []struct {
		name string
		unit core.UnitInfo
		ops  []op
	}{
		{"poweroff.target", unit("poweroff.target"), []op{opStart}},
		{"rescue.target", unit("rescue.target"), []op{opStart}},
		{"systemd-reboot.service", unit("systemd-reboot.service"), []op{opStart, opRestart}},
		{"systemd-suspend.service", unit("systemd-suspend.service"), []op{opStart}},
		{"systemd-hibernate.service", unit("systemd-hibernate.service"), []op{opStart}},
		{"systemd-exit.service", unit("systemd-exit.service"), []op{opStart}},
		{"systemd-storagetm.service", unit("systemd-storagetm.service", reboots), []op{opStart, opStop}},
		{"debug-shell.service", unit("debug-shell.service"), []op{opStart}},
		{"Requires=sleep.target", unit("nap.service", requires("system.slice", "sleep.target")), []op{opStart, opRestart}},
		{"Wants=final.target", unit("last.service", func(u *core.UnitInfo) { u.Wants = []string{"final.target"} }), []op{opStart}},
		{"SuccessAction=reboot", unit("update-done.service", reboots), []op{opStart, opRestart, opStop}},
		{"RefuseManualStart", unit("only-dep.service", func(u *core.UnitInfo) { u.RefuseManualStart = true }), []op{opStart}},
		{"a timer of systemd-suspend.service", unit("nightly-suspend.timer", func(u *core.UnitInfo) {
			u.Triggers = []string{"systemd-suspend.service"}
		}), []op{opStart, opRestart}},
		{"an alias of systemd-reboot.service", unit("quick-reboot.service", func(u *core.UnitInfo) {
			u.Names = []string{"quick-reboot.service", "systemd-reboot.service"}
		}), []op{opStart}},
		{"a mount", unit("home.mount"), allOps},
		{"a path", unit("cups.path"), allOps},
		{"a target", unit("multi-user.target"), allOps},
		{"darkpanel-agent.service", unit("darkpanel-agent.service"), allOps},
		{"darkpanel-job-x.service", unit("darkpanel-job-x.service"), allOps},
		{"darkpanel-job-x.timer", unit("darkpanel-job-x.timer"), allOps},
		{"dbus.service", unit("dbus.service"), []op{opStop, opRestart, opDisable}},
		{"dbus.socket", unit("dbus.socket"), []op{opStop, opRestart, opDisable}},
	}
	for _, tt := range tests {
		// The suspend service stands for a timer's target in the timer row.
		r := newRig(t, tt.unit, unit("systemd-suspend.service"))
		for _, o := range tt.ops {
			for _, p := range []core.Principal{adminP, operatorP} {
				_, err := r.plan(o, p, tt.unit.ID)
				wantCode(t, tt.name+": "+string(o)+" as "+p.Actor, err, errcode.ServicesProtected)
			}
		}
	}

	// The rules refuse what they name and nothing else.
	r := newRig(t, unit("nginx.service"), unit("dbus.service"), unit("backup.timer", func(u *core.UnitInfo) {
		u.Triggers = []string{"backup.service"}
	}), unit("backup.service"))
	for _, c := range []struct {
		o    op
		unit string
	}{{opStart, "nginx.service"}, {opStop, "nginx.service"}, {opRestart, "nginx.service"}, {opReload, "nginx.service"},
		{opStart, "dbus.service"}, {opReload, "dbus.service"}, {opStart, "backup.timer"}} {
		if _, err := r.plan(c.o, operatorP, c.unit); err != nil {
			t.Errorf("%s %s as an operator: %v", c.o, c.unit, err)
		}
	}
}

// guard returns the guard id of out, if reported.
func guard(out core.PlanOutcome, id string) (core.Guard, bool) {
	i := slices.IndexFunc(out.Guards, func(g core.Guard) bool { return g.ID == id })
	if i < 0 {
		return core.Guard{}, false
	}
	return out.Guards[i], true
}

func TestGuardedUnits(t *testing.T) {
	for _, g := range (module{}).Manifest().Actions {
		for _, s := range g.Spec.Guards {
			if s.Overridable {
				t.Errorf("%s declares guard %s overridable", g.Spec.Name, s.ID)
			}
		}
	}

	t.Run("network stack", func(t *testing.T) {
		r := newRig(t, unit("systemd-networkd.service"), unit("NetworkManager.service"))
		for _, o := range []op{opStop, opDisable} {
			out, err := r.plan(o, adminP, "systemd-networkd.service")
			if g, ok := guard(out, GuardNetworkStack); err != nil || !ok || g.OK {
				t.Errorf("%s systemd-networkd.service: %v, guards %+v; want %s failing", o, err, out.Guards, GuardNetworkStack)
			}
		}
		out, err := r.plan(opRestart, operatorP, "NetworkManager.service")
		if err != nil || out.Confirmation != "NetworkManager.service" || len(out.Guards) != 0 {
			t.Errorf("restart NetworkManager.service: %+v, %v; want the typed phrase and no guard", out, err)
		}
		if out, err := r.plan(opStart, operatorP, "NetworkManager.service"); err != nil || out.Confirmation != "" {
			t.Errorf("start NetworkManager.service: %+v, %v; want no phrase", out, err)
		}
	})

	t.Run("managed units", func(t *testing.T) {
		managers := map[string]string{
			"ufw.service": "firewall", "docker.service": "docker", "tailscaled.service": "tailscale", "ssh.service": "users",
			"ssh.socket": "users", "sshd.service": "users", "fail2ban.service": "fail2ban", "nftables.service": "firewall",
			"netfilter-persistent.service": "firewall", "firewalld.service": "firewall",
		}
		for name, module := range managers {
			r := newRig(t, unit(name))
			r.pending("docker.container.run", core.StateCompleted)
			for _, o := range allOps {
				_, err := r.plan(o, operatorP, name)
				wantCode(t, string(o)+" "+name+" as an operator", err, errcode.Forbidden)
				out, err := r.plan(o, adminP, name)
				g, ok := guard(out, GuardModuleChange)
				if err != nil || out.Confirmation != name || !ok || !g.OK {
					t.Errorf("%s %s as an admin: %+v, %v; want the typed phrase and %s passing", o, name, out, err, GuardModuleChange)
				}
			}
			r.pending(module+".x.set", core.StateApplying)
			out, err := r.plan(opStart, adminP, name)
			if g, ok := guard(out, GuardModuleChange); err != nil || !ok || g.OK || !strings.Contains(g.Message, module) {
				t.Errorf("start %s while %s applies: %+v, %v; want %s failing", name, module, out.Guards, err, GuardModuleChange)
			}
		}
		r := newRig(t, unit("ssh.service"))
		r.pending("users.key.add", core.StatePending)
		if out, _ := r.plan(opRestart, adminP, "ssh.service"); !slices.ContainsFunc(out.Guards, func(g core.Guard) bool {
			return g.ID == GuardModuleChange && !g.OK
		}) {
			t.Errorf("restart ssh.service while a users change awaits confirmation: guards %+v", out.Guards)
		}
	})

	t.Run("triggered units", func(t *testing.T) {
		r := newRig(t, unit("docker.socket", func(u *core.UnitInfo) { u.Triggers = []string{"docker.service"} }), unit("docker.service"),
			unit("reboot-check.timer", func(u *core.UnitInfo) { u.Triggers = []string{"reboot-check.service"} }),
			unit("reboot-check.service", func(u *core.UnitInfo) { u.FailureAction = "reboot" }))
		for _, name := range []string{"docker.socket", "reboot-check.timer"} {
			_, err := r.plan(opStart, operatorP, name)
			wantCode(t, "start "+name+" as an operator", err, errcode.Forbidden)
			if out, err := r.plan(opStart, adminP, name); err != nil || out.Confirmation != name {
				t.Errorf("start %s as an admin: %+v, %v; want the typed phrase", name, out, err)
			}
		}
		if out, _ := r.plan(opStart, adminP, "docker.socket"); !slices.ContainsFunc(out.Guards, func(g core.Guard) bool { return g.ID == GuardModuleChange }) {
			t.Errorf("start docker.socket: guards %+v, want %s", out.Guards, GuardModuleChange)
		}
	})

	t.Run("tailnet link", func(t *testing.T) {
		r := newRig(t, unit("tailscaled.service"))
		for _, on := range []bool{true, false} {
			r.onTailnet = on
			for _, o := range []op{opStop, opRestart} {
				out, err := r.plan(o, adminP, "tailscaled.service")
				if g, ok := guard(out, GuardTailnetLink); err != nil || !ok || g.OK == on {
					t.Errorf("%s tailscaled with the agent on the tailnet %v: %+v, %v", o, on, out.Guards, err)
				}
			}
		}
		r.onTailnet = true
		out, err := r.plan(opDisable, adminP, "tailscaled.service")
		_, ok := guard(out, GuardTailnetLink)
		if err != nil || ok || !slices.ContainsFunc(out.Diff, func(l core.DiffLine) bool { return strings.Contains(l.Text, "next boot") }) {
			t.Errorf("disable tailscaled on the tailnet: %+v, %v; want a warning line and no link guard", out, err)
		}
	})

	t.Run("action properties", func(t *testing.T) {
		r := newRig(t, unit("battery-check.service", func(u *core.UnitInfo) { u.FailureAction = "poweroff-force" }),
			unit("watchdog.service", func(u *core.UnitInfo) { u.StartLimitAction = "reboot" }))
		for _, name := range []string{"battery-check.service", "watchdog.service"} {
			_, err := r.plan(opStart, operatorP, name)
			wantCode(t, "start "+name+" as an operator", err, errcode.Forbidden)
			if out, err := r.plan(opStart, adminP, name); err != nil || out.Confirmation != name {
				t.Errorf("start %s as an admin: %+v, %v; want the typed phrase", name, out, err)
			}
		}
	})

	t.Run("journald", func(t *testing.T) {
		r := newRig(t, unit(journaldUnit))
		for _, o := range allOps {
			out, err := r.plan(o, adminP, journaldUnit)
			want := ""
			if o == opStop || o == opRestart || o == opDisable {
				want = journaldUnit
			}
			if err != nil || out.Confirmation != want {
				t.Errorf("%s %s: %+v, %v; want phrase %q", o, journaldUnit, out, err, want)
			}
		}
	})
}

// listedUnit is one line of a Task 1 unit listing: the label the probe gave
// and the unit built from the properties it recorded.
type listedUnit struct {
	label string
	unit  core.UnitInfo
}

// readListings parses testdata/actions/<os>.txt: "<name> <label>
// <property>=<value> ...", where a list value's items follow its key.
func readListings(t *testing.T) map[string][]listedUnit {
	t.Helper()
	files, err := filepath.Glob("testdata/actions/*.txt")
	if err != nil || len(files) != 5 {
		t.Fatalf("listings %v, %v; want the five OS", files, err)
	}
	keys := []string{"Id", "Names", "Requires", "Wants", "SuccessAction", "FailureAction", "JobTimeoutAction", "StartLimitAction"}
	out := map[string][]listedUnit{}
	for _, f := range files {
		b, err := os.ReadFile(f) //nolint:gosec // G304: a testdata file of this package
		if err != nil {
			t.Fatal(err)
		}
		for line := range strings.Lines(string(b)) {
			fields := strings.Fields(line)
			if len(fields) < 2 {
				continue
			}
			props := map[string][]string{}
			key := ""
			for _, tok := range fields[2:] {
				if k, v, ok := strings.Cut(tok, "="); ok && slices.Contains(keys, k) {
					key = k
					props[k] = nil
					tok = v
				}
				if key != "" && tok != "" {
					props[key] = append(props[key], tok)
				}
			}
			one := func(k string) string {
				if len(props[k]) == 0 {
					return ""
				}
				return props[k][0]
			}
			u := core.UnitInfo{ID: one("Id"), Names: props["Names"], Requires: props["Requires"], Wants: props["Wants"],
				SuccessAction: one("SuccessAction"), FailureAction: one("FailureAction"), JobTimeoutAction: one("JobTimeoutAction"),
				StartLimitAction: one("StartLimitAction"), LoadState: "loaded", ActiveState: "inactive", SubState: "dead", UnitFileState: "static"}
			if u.ID == "" {
				t.Fatalf("%s: line without Id: %s", f, line)
			}
			out[filepath.Base(f)] = append(out[filepath.Base(f)], listedUnit{label: fields[1], unit: u})
		}
	}
	return out
}

func TestRefusalListMatchesHosts(t *testing.T) {
	for file, units := range readListings(t) {
		for _, l := range units {
			r := newRig(t, l.unit)
			what := file + ": " + l.unit.ID + " (" + l.label + ")"
			switch l.label {
			case "dependency", "success-action":
				for _, p := range []core.Principal{adminP, operatorP} {
					_, err := r.plan(opStart, p, l.unit.ID)
					wantCode(t, what+" as "+p.Actor, err, errcode.ServicesProtected)
				}
			case "other-action":
				if out, err := r.plan(opStart, adminP, l.unit.ID); err != nil || out.Confirmation != l.unit.ID {
					t.Errorf("%s as an admin: %+v, %v; want a plan with the typed phrase", what, out, err)
				}
				_, err := r.plan(opStart, operatorP, l.unit.ID)
				wantCode(t, what+" as an operator", err, errcode.Forbidden)
			default:
				t.Errorf("%s: unknown label", what)
			}
		}
	}
}
