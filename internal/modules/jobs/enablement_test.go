package jobs

import (
	"context"
	"slices"
	"strings"
	"testing"
)

// TestModuleOffStopsTimers is M11 spec section 6's module off: flag 0 stops
// and disables every running job timer, so no reboot starts it, and
// remembers it; flag 1 enables and starts exactly those; and an agent that
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
	if want := []string{"Stop darkpanel-job-backup.timer", "Disable darkpanel-job-backup.timer", "Stop darkpanel-job-clean.timer", "Disable darkpanel-job-clean.timer"}; !slices.Equal(r.units.calls, want) {
		t.Errorf("module off: calls %q, want %q", r.units.calls, want)
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
	if want := []string{"Stop darkpanel-job-backup.timer", "Disable darkpanel-job-backup.timer", "Stop darkpanel-job-clean.timer", "Disable darkpanel-job-clean.timer"}; !slices.Equal(r.units.calls, want) {
		t.Errorf("agent start with the module off: calls %q, want %q", r.units.calls, want)
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
