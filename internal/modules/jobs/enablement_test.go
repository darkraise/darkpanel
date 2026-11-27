package jobs

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
)

// TestModuleOffStopsTimers is M11 spec section 6's module off: flag 0 stops
// and disables every job timer, so no reboot starts it, and remembers the
// running ones; flag 1 enables and starts exactly those; and an agent that
// starts with flag 0 stops a timer that runs anyway.
func TestModuleOffStopsTimers(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	for _, slug := range []string{"backup", "clean", "idle"} {
		r.apply("jobs.job.create", strings.Replace(backupInput, `"backup"`, `"`+slug+`"`, 1))
	}
	r.apply("jobs.job.disable", `{"slug":"idle"}`)
	env := r.env(adminP)

	r.units.calls = nil
	if err := r.m.OnEnabled(ctx, env, false); err != nil {
		t.Fatal(err)
	}
	offCalls := []string{"Stop darkpanel-job-backup.timer", "Disable darkpanel-job-backup.timer", "Stop darkpanel-job-clean.timer",
		"Disable darkpanel-job-clean.timer", "Stop darkpanel-job-idle.timer", "Disable darkpanel-job-idle.timer"}
	if !slices.Equal(r.units.calls, offCalls) {
		t.Errorf("module off: calls %q, want %q", r.units.calls, offCalls)
	}
	if got := r.paused(); !slices.Equal(got, []string{"darkpanel-job-backup.timer", "darkpanel-job-clean.timer"}) {
		t.Errorf("remembered %q", got)
	}

	// The agent restarts after a reboot started the enabled timers again.
	r.units.set("darkpanel-job-backup.timer", true)
	r.units.set("darkpanel-job-clean.timer", true)
	r.units.calls = nil
	if err := r.m.OnEnabled(ctx, env, false); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(r.units.calls, offCalls) {
		t.Errorf("agent start with the module off: calls %q, want %q", r.units.calls, offCalls)
	}

	r.units.calls = nil
	if err := r.m.OnEnabled(ctx, env, true); err != nil {
		t.Fatal(err)
	}
	if want := []string{"Enable darkpanel-job-backup.timer", "Start darkpanel-job-backup.timer", "Enable darkpanel-job-clean.timer", "Start darkpanel-job-clean.timer"}; !slices.Equal(r.units.calls, want) {
		t.Errorf("module on: calls %q, want %q (not the disabled idle)", r.units.calls, want)
	}
	if got := r.paused(); len(got) != 0 {
		t.Errorf("still remembered after the module came back: %q", got)
	}
	r.units.calls = nil
	if err := r.m.OnEnabled(ctx, env, true); err != nil || len(r.units.calls) != 0 {
		t.Errorf("a second on: calls %q, %v; want none", r.units.calls, err)
	}
}

func (r *rig) paused() []string {
	r.t.Helper()
	rows, err := r.db.Read().Query(`SELECT timer FROM jobs_paused ORDER BY timer`)
	if err != nil {
		r.t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			r.t.Fatal(err)
		}
		out = append(out, s)
	}
	return out
}

// TestModuleOffStopsTheRest: a timer that fails to stop is still disabled,
// the timers after it are still stopped, and off reports the failure.
func TestModuleOffStopsTheRest(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	for _, slug := range []string{"backup", "clean"} {
		r.apply("jobs.job.create", strings.Replace(backupInput, `"backup"`, `"`+slug+`"`, 1))
	}
	r.units.stopFails = map[string]bool{"darkpanel-job-backup.timer": true}
	r.units.calls = nil
	if err := r.m.OnEnabled(ctx, r.env(adminP), false); err == nil || !strings.Contains(err.Error(), "stop failed") {
		t.Errorf("module off = %v, want the failed stop", err)
	}
	if want := []string{"Stop darkpanel-job-backup.timer", "Disable darkpanel-job-backup.timer", "Stop darkpanel-job-clean.timer", "Disable darkpanel-job-clean.timer"}; !slices.Equal(r.units.calls, want) {
		t.Errorf("module off: calls %q, want %q", r.units.calls, want)
	}
}

// TestOffStopsEveryTimer: off remembers the timers that run, are starting or
// have a start queued, stops and disables every job timer whatever its
// state, and reports a state it cannot read after still stopping that
// timer; on starts exactly the remembered ones.
func TestOffStopsEveryTimer(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	slugs := []string{"activating", "active", "idle", "queued", "unread"}
	for _, slug := range slugs {
		r.apply("jobs.job.create", strings.Replace(backupInput, `"backup"`, `"`+slug+`"`, 1))
	}
	r.units.states = map[string]core.UnitState{
		timerName("activating"): {LoadState: "loaded", ActiveState: "activating"},
		timerName("idle"):       {LoadState: "loaded", ActiveState: "inactive"},
		timerName("queued"):     {LoadState: "loaded", ActiveState: "inactive", PendingJob: "start"},
	}
	r.units.stateFails = map[string]bool{timerName("unread"): true}
	r.units.calls = nil
	env := r.env(adminP)
	if err := r.m.OnEnabled(ctx, env, false); err == nil || !strings.Contains(err.Error(), "state failed") {
		t.Errorf("off = %v, want the failed state read", err)
	}
	var want []string
	for _, slug := range slugs {
		want = append(want, "Stop "+timerName(slug), "Disable "+timerName(slug))
	}
	if !slices.Equal(r.units.calls, want) {
		t.Errorf("off: calls %q, want %q", r.units.calls, want)
	}
	remembered := []string{timerName("activating"), timerName("active"), timerName("queued")}
	if got := r.paused(); !slices.Equal(got, remembered) {
		t.Errorf("remembered %q, want %q", got, remembered)
	}

	r.units.states, r.units.stateFails, r.units.calls = nil, nil, nil
	if err := r.m.OnEnabled(ctx, env, true); err != nil {
		t.Fatal(err)
	}
	want = nil
	for _, timer := range remembered {
		want = append(want, "Enable "+timer, "Start "+timer)
	}
	if !slices.Equal(r.units.calls, want) {
		t.Errorf("on: calls %q, want %q", r.units.calls, want)
	}
}
