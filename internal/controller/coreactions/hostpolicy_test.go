package coreactions

import (
	"database/sql"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// policyRow is a core_host_update row; "<null>" stands for NULL.
type policyRow struct {
	Channel, Days, Time, LastWindow string
}

func (e *testEnv) policyRow(hostID string) policyRow {
	e.t.Helper()
	var r policyRow
	var days, at, window sql.NullString
	if err := e.db.Read().QueryRow(`SELECT channel, days, time_utc, last_window FROM core_host_update WHERE host_id = ?`,
		hostID).Scan(&r.Channel, &days, &at, &window); err != nil {
		e.t.Fatal(err)
	}
	for _, f := range []struct {
		dst *string
		src sql.NullString
	}{{&r.Days, days}, {&r.Time, at}, {&r.LastWindow, window}} {
		*f.dst = "<null>"
		if f.src.Valid {
			*f.dst = f.src.String
		}
	}
	return r
}

func TestHostUpdatePolicy(t *testing.T) {
	e := newTestEnv(t)
	e.addHost("h1", "alpha", true)
	e.addHost("h2", "web1", false)
	beta := core.UpdatePolicy{HostID: "h2", Channel: core.ChannelBeta,
		Schedule: &core.UpdateSchedule{Days: []string{"thu", "mon"}, Time: "03:00"}}
	if err := e.verify("core.host.update-policy", beta); err == nil {
		t.Error("Verify passed before the policy was set")
	}

	pv, err := e.plan(operator, "core.host.update-policy", beta)
	if err != nil {
		t.Fatal(err)
	}
	wantDiff := []core.DiffLine{
		{Op: "-", Text: "host web1 (h2): channel stable, updates on demand only"},
		{Op: "+", Text: "host web1 (h2): channel beta, a two-hour window opening at 03:00 UTC on mon,thu"},
		{Op: " ", Text: "compensated by core.host.update-policy with the previous policy"},
	}
	if !reflect.DeepEqual(pv.Diff, wantDiff) {
		t.Errorf("diff = %+v\nwant %+v", pv.Diff, wantDiff)
	}
	var got core.UpdatePolicy
	e.run(operator, "core.host.update-policy", beta, &got)
	want := core.UpdatePolicy{HostID: "h2", Channel: core.ChannelBeta,
		Schedule: &core.UpdateSchedule{Days: []string{"mon", "thu"}, Time: "03:00"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("output = %+v, want %+v", got, want)
	}
	if r := e.policyRow("h2"); r != (policyRow{"beta", "mon,thu", "03:00", "<null>"}) {
		t.Errorf("row = %+v, want beta with days mon,thu at 03:00", r)
	}
	if err := e.verify("core.host.update-policy", beta); err != nil {
		t.Errorf("Verify: %v", err)
	}

	// Setting the policy again keeps the scheduler's last window.
	e.exec(`UPDATE core_host_update SET last_window = '2026-10-15T03:00' WHERE host_id = 'h2'`)
	stable := core.UpdatePolicy{HostID: "h2", Channel: core.ChannelStable}
	v := e.run(operator, "core.host.update-policy", stable, nil)
	if !jsonEqual(t, v.Output, `{"hostId":"h2","channel":"stable","schedule":null}`) {
		t.Errorf("output = %s, want stable without a schedule", v.Output)
	}
	if r := e.policyRow("h2"); r != (policyRow{"stable", "<null>", "<null>", "2026-10-15T03:00"}) {
		t.Errorf("row = %+v, want stable without a schedule, keeping its last window", r)
	}
	if err := e.verify("core.host.update-policy", stable); err != nil {
		t.Errorf("Verify: %v", err)
	}
	if n := e.count(`SELECT count(*) FROM core_host_update WHERE host_id = 'h1'`); n != 0 {
		t.Errorf("h1 has %d policy rows, want none", n)
	}

	_, err = e.plan(operator, "core.host.update-policy", core.UpdatePolicy{HostID: "nope", Channel: core.ChannelStable})
	wantCode(t, err, errcode.NotFound)
	for _, bad := range []string{
		`{"hostId":"h2","channel":"nightly","schedule":null}`,
		`{"hostId":"h2","channel":"beta","schedule":{"days":["mon","mon"],"time":"03:00"}}`,
		`{"hostId":"h2","channel":"beta","schedule":{"days":[],"time":"03:00"}}`,
		`{"hostId":"h2","channel":"beta","schedule":{"days":["mon"],"time":"24:00"}}`,
	} {
		_, err := e.engine.Plan(t.Context(), operator, ref("core.host.update-policy"), json.RawMessage(bad), core.PlanOptions{})
		wantCode(t, err, errcode.InvalidInput)
	}
}
