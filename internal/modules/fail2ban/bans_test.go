package fail2ban

import (
	"context"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/platform"
)

func TestBans(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	r.files[SettingsFile] = []byte(testdata(t, "zz-darkpanel.local"))
	add, remove := banHandler{m: r.module(), ban: true}, banHandler{m: r.module()}

	for addr, why := range map[string]string{
		"127.0.0.2":    "loopback",
		"198.51.100.7": "the controller",
		"100.101.1.2":  "a Tailscale address",
		"203.0.113.40": "the admin's own entry",
		"192.0.2.1":    "the caller's own address",
		"192.0.2.10":   "an address banned already",
		"not-an-ip":    "not an address",
	} {
		_, err := add.Plan(ctx, r.env(adminP), BanInput{Jail: "sshd", Address: addr})
		wantCode(t, "ban "+why, err, errcode.InvalidInput)
	}
	_, err := add.Plan(ctx, r.env(adminP), BanInput{Jail: "nginx", Address: "192.0.2.50"})
	wantCode(t, "ban in a jail not running", err, errcode.InvalidInput)

	plan, err := add.Plan(ctx, r.env(adminP), BanInput{Jail: "sshd", Address: "192.0.2.50"})
	if err != nil || len(plan.Diff) != 1 || plan.Diff[0].Text != "Ban 192.0.2.50 in sshd" {
		t.Fatalf("plan %+v, %v", plan, err)
	}
	r.answer(platform.Fail2banClientPath, platform.Fail2banBan("sshd", "192.0.2.50"), "1\n")
	if _, err := add.Apply(ctx, r.env(adminP), BanInput{Jail: "sshd", Address: "192.0.2.50"}); err != nil {
		t.Fatal(err)
	}

	// An operator may unban; the plan names the address and its jail.
	r.events = nil
	in := BanInput{Jail: "sshd", Address: "192.0.2.10"}
	if _, err := remove.Plan(ctx, r.env(operatorP), in); err != nil {
		t.Fatal(err)
	}
	r.answer(platform.Fail2banClientPath, platform.Fail2banUnban("sshd", "192.0.2.10"), "1\n")
	if _, err := remove.Apply(ctx, r.env(operatorP), in); err != nil {
		t.Fatal(err)
	}
	unban := "run " + key(platform.Fail2banClientPath, []string{"set", "sshd", "unbanip", "192.0.2.10"})
	if r.events[len(r.events)-1] != unban {
		t.Errorf("events %v, want %q last", r.events, unban)
	}
	_, err = remove.Plan(ctx, r.env(operatorP), BanInput{Jail: "sshd", Address: "192.0.2.99"})
	wantCode(t, "unban an address not banned", err, errcode.InvalidInput)

	// The specs: unban is an operator's, ban an admin's, and each undoes the other.
	defs := map[string]core.ActionSpec{}
	for _, d := range r.module().banActions() {
		defs[d.Spec.Name] = d.Spec
	}
	if s := defs["fail2ban.ban.remove"]; s.MinScope != "" || s.CompensatedBy != "fail2ban.ban.add" ||
		core.DefaultScope(s.Class, s.Side) != core.ScopeOperate {
		t.Errorf("ban.remove spec %+v", s)
	}
	if s := defs["fail2ban.ban.add"]; s.MinScope != core.ScopeAdmin || s.CompensatedBy != "fail2ban.ban.remove" {
		t.Errorf("ban.add spec %+v", s)
	}

	// Verify reads the jail again.
	r.answer(platform.Fail2banClientPath, platform.Fail2banStatus("sshd"),
		strings.Replace(testdata(t, "status-sshd"), "192.0.2.10 198.51.100.20", "198.51.100.20", 1))
	if err := remove.Verify(ctx, r.env(operatorP), in, BanChanged{}); err != nil {
		t.Errorf("verify unban: %v", err)
	}
	if err := add.Verify(ctx, r.env(adminP), in, BanChanged{}); err == nil {
		t.Error("verify of a ban the jail does not show passed")
	}
}
