package system

import (
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/platform"
)

func TestAutoupdatesSet(t *testing.T) {
	h := newSysHost(t)
	_, err := h.planErr("system.autoupdates.set", `{"enabled":true}`)
	if errcode.From(err).Code != "system.unattended_absent" {
		t.Fatalf("absent package: err = %v", err)
	}
	h.installed("unattended-upgrades")
	if _, err := h.applyTask("system.autoupdates.set", `{"enabled":true}`); err != nil {
		t.Fatal(err)
	}
	want := "APT::Periodic::Update-Package-Lists \"1\";\nAPT::Periodic::Unattended-Upgrade \"1\";\n"
	if got := string(h.written("/etc/apt/apt.conf.d/52darkpanel-auto-upgrades")); got != want {
		t.Fatalf("file = %q", got)
	}
	if err := h.undo("system.autoupdates.set"); err != nil {
		t.Fatal(err)
	}
	if h.exists("/etc/apt/apt.conf.d/52darkpanel-auto-upgrades") {
		t.Fatal("undo left the file")
	}
}

func TestRebootRefusalsAndSchedule(t *testing.T) {
	h := newSysHost(t)
	h.pendingChange("c-fw", "firewall.rules.apply", "awaiting_confirmation")
	if _, err := h.planErr("system.host.reboot", `{}`); errcode.From(err).Code != "system.change_pending" || !strings.Contains(err.Error(), "c-fw") {
		t.Fatalf("awaiting: err = %v", err)
	}
	h.clearChanges()
	h.pendingChange("c-ts", "tailscale.package.install", "applying")
	if _, err := h.planErr("system.host.reboot", `{}`); errcode.From(err).Code != "system.change_pending" {
		t.Fatalf("applying: err = %v", err)
	}
	h.clearChanges()
	plan := h.plan(t, "system.host.reboot", `{}`)
	if plan.Phrase != h.hostName() {
		t.Fatalf("phrase = %q", plan.Phrase)
	}
	h.file("/proc/uptime", []byte("3600.00 7000.00\n")) // bootTimeBefore = clock now − uptime, truncated to the second, as hostinfo computes it
	out, err := h.applyTask("system.host.reboot", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	at := h.reboot.scheduledAt
	if d := at.Sub(h.now()); d != 15*time.Second {
		t.Fatalf("scheduled %v after now", d)
	}
	if !strings.Contains(out, `"scheduledAt":"`+at.Format(time.RFC3339)+`"`) || !strings.Contains(out, `"bootTimeBefore":"2026-11-19T11:00:00Z"`) {
		t.Fatalf("output = %s", out)
	}
	h.reboot.err = &platform.LogindError{Reason: "blocked by inhibitor"}
	if _, err := h.applyTask("system.host.reboot", `{}`); errcode.From(err).Code != "system.logind_refused" || !strings.Contains(err.Error(), "blocked by inhibitor") {
		t.Fatalf("logind refusal: err = %v", err)
	}
}

// Additions to the plan's cases.

func TestAutoupdatesUndoRestoresThePriorFile(t *testing.T) {
	h := newSysHost(t)
	h.installed("unattended-upgrades")
	prior := "APT::Periodic::Update-Package-Lists \"7\";\n"
	h.file("/etc/apt/apt.conf.d/52darkpanel-auto-upgrades", []byte(prior))
	if _, err := h.applyTask("system.autoupdates.set", `{"enabled":false}`); err != nil {
		t.Fatal(err)
	}
	if got := string(h.written("/etc/apt/apt.conf.d/52darkpanel-auto-upgrades")); !strings.Contains(got, `Unattended-Upgrade "0";`) {
		t.Fatalf("file = %q", got)
	}
	if err := h.undo("system.autoupdates.set"); err != nil {
		t.Fatal(err)
	}
	if got := string(h.written("/etc/apt/apt.conf.d/52darkpanel-auto-upgrades")); got != prior {
		t.Fatalf("undo left %q, want the prior bytes", got)
	}
	if p := h.plan(t, "system.autoupdates.set", `{"enabled":false}`); !strings.Contains(p.Text(), "Turn automatic updates off") {
		t.Fatalf("plan:\n%s", p.Text())
	}
}

func TestRebootTaskRefusesAPendingChangeOtherThanItsOwn(t *testing.T) {
	h := newSysHost(t)
	h.file("/proc/uptime", []byte("60.5 1.0\n"))
	h.pendingChange("c1", "system.host.reboot", "applying") // the reboot's own change: applyTask names it c1
	if _, err := h.applyTask("system.host.reboot", `{}`); err != nil {
		t.Fatalf("own change counted as pending: %v", err)
	}
	h.pendingChange("c-other", "system.packages.install", "applying")
	if _, err := h.applyTask("system.host.reboot", `{}`); errcode.From(err).Code != "system.change_pending" {
		t.Fatalf("err = %v", err)
	}
}
