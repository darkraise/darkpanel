package fail2ban

import (
	"context"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

func TestStatusParse(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	st, err := statusReader{m: r.module()}.Read(ctx, r.env(operatorP), struct{}{})
	if err != nil {
		t.Fatal(err)
	}
	if !st.Installed || st.Version != "1.0.2" || st.Service != "active" || st.CrowdSec || st.Settings != nil {
		t.Errorf("status %+v", st)
	}
	if len(st.Jails) != 1 || st.Jails[0] != (Jail{Name: "sshd", Banned: 2, Failed: 3, FailedTotal: 17}) {
		t.Errorf("jails %+v", st.Jails)
	}
	if got := parseVersion([]byte("Fail2Ban v0.11.2\n")); got != "0.11.2" {
		t.Errorf("older version line read as %q", got)
	}

	bans, err := banLister{}.Read(ctx, r.env(operatorP), BanListInput{Jail: "sshd"})
	if err != nil {
		t.Fatal(err)
	}
	at := func(s string) time.Time {
		v, err := time.ParseInLocation(banTimeLayout, s, time.Local)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	want := []Ban{
		{Jail: "sshd", Address: "192.0.2.10", Start: at("2026-11-26 10:00:00")},
		{Jail: "sshd", Address: "198.51.100.20", Start: at("2026-11-26 10:05:30")},
	}
	ends := []time.Time{at("2026-11-26 10:10:00"), at("2026-11-26 10:15:30")}
	if len(bans.Bans) != 2 {
		t.Fatalf("bans %+v", bans.Bans)
	}
	for i, b := range bans.Bans {
		if b.Jail != want[i].Jail || b.Address != want[i].Address || !b.Start.Equal(want[i].Start) || b.End == nil || !b.End.Equal(ends[i]) {
			t.Errorf("ban %d = %+v, want %+v ending %v", i, b, want[i], ends[i])
		}
	}
	if all, err := (banLister{}).Read(ctx, r.env(operatorP), BanListInput{}); err != nil || len(all.Bans) != 2 {
		t.Errorf("every jail: %+v, %v", all, err)
	}
	_, err = banLister{}.Read(ctx, r.env(operatorP), BanListInput{Jail: "nginx"})
	wantCode(t, "a jail not running", err, errcode.InvalidInput)
	if b, err := parseBanTimes("recidive", "192.0.2.11 \t2026-11-26 10:00:00 + -1 = 9999-12-31 23:59:59\n"); err != nil || b[0].End != nil {
		t.Errorf("a ban without end: %+v, %v", b, err)
	}
	if _, err := parseBanTimes("sshd", "192.0.2.10\n"); err == nil {
		t.Error("a line without times was read")
	}

	// A stopped fail2ban has no jails and runs no client; a host without it
	// reports it not installed.
	r.units[fail2banUnit] = core.UnitState{LoadState: "loaded", ActiveState: "inactive"}
	r.events = nil
	st, err = statusReader{m: r.module()}.Read(ctx, r.env(operatorP), struct{}{})
	if err != nil || !st.Installed || st.Service != "inactive" || len(st.Jails) != 0 || len(r.events) != 1 {
		t.Errorf("stopped: %+v, %v, events %v", st, err, r.events)
	}
	delete(r.units, fail2banUnit)
	st, err = statusReader{m: r.module()}.Read(ctx, r.env(operatorP), struct{}{})
	if err != nil || st.Installed || st.Service != "" || st.Jails == nil {
		t.Errorf("not installed: %+v, %v", st, err)
	}
}
