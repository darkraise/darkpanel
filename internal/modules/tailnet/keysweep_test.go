package tailnet

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/cloud/cloudfake"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/ext"
	"github.com/darkraise/darkpanel/internal/modules/tailnet/fake"
)

const swHost = "01J9X4K8Q2M5N7P0R3S6T8V1H1"

var swKeysPath = "/api/v2/tailnet/" + fake.Tailnet + "/keys"

// swChange returns a 26-character change id that differs by n.
func swChange(n int) string { return fmt.Sprintf("01J9X4K8Q2M5N7P0R3S6T8V%03d", n) }

type swFix struct {
	h     *tnHost
	srv   *fake.Server
	t     tn
	api   *api
	audit *coretest.MemAudit
	deps  ext.Deps
}

func swSetup(t *testing.T) swFix {
	t.Helper()
	h := newTNHost(t)
	srv := fake.New(t)
	acConnect(t, h, srv)
	tnn := tnWith(srv)
	a, _, err := tnn.connected(context.Background(), h.db, coretest.Sealer{}, h.clock)
	if err != nil {
		t.Fatal(err)
	}
	audit := &coretest.MemAudit{}
	return swFix{h: h, srv: srv, t: tnn, api: a, audit: audit, deps: ext.Deps{
		DB: h.db, Sealer: coretest.Sealer{}, Clock: h.clock, Audit: audit,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}}
}

// key creates a provider key with the description and returns its id.
func (f swFix) key(t *testing.T, description string) string {
	t.Helper()
	ck, err := f.api.CreateKey(context.Background(), KeyRequest{Description: description, Tags: []string{"tag:ci"}, ExpirySeconds: 3600})
	if err != nil {
		t.Fatal(err)
	}
	return ck.ID
}

func (f swFix) alive(id string) bool {
	k, err := f.api.Key(context.Background(), id)
	return err == nil && !k.Revoked
}

// row records a tailnet_keys row requested age ago with a one hour expiry.
func (f swFix) row(t *testing.T, description, state, keyID string, age time.Duration) {
	t.Helper()
	now := f.h.clock.Now()
	err := f.h.db.Write(context.Background(), func(tx *sql.Tx) error {
		return insertKey(context.Background(), tx, keyRow{
			Description: description, Kind: "change", OwnerID: swChange(900), KeyID: keyID, State: state,
			RequestedAt: now.Add(-age), UpdatedAt: now.Add(-age), ExpirySeconds: 3600, Tags: []string{"tag:ci"},
		})
	})
	if err != nil {
		t.Fatal(err)
	}
}

func (f swFix) state(t *testing.T, description string) string {
	t.Helper()
	r, ok, err := loadKey(context.Background(), f.h.db.Read(), description)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		return ""
	}
	return r.State
}

func swEventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestSwSweepRules(t *testing.T) {
	change := "darkpanel-" + swChange(1)
	enroll := "darkpanel-enroll-" + swHost
	const (
		inWindow   = time.Hour      // request time + 1 h expiry + 24 h is still ahead
		pastWindow = 48 * time.Hour // it is not
	)
	cases := []struct {
		name        string
		desc        string
		state       string // "" records no row
		age         time.Duration
		orphans     bool
		wantDeleted bool
		wantState   string
	}{
		{"uncertain in window, five-minute pass", change, "uncertain", inWindow, false, true, "uncertain"},
		{"retired in window, five-minute pass", change, "retired", inWindow, false, true, "deleted"},
		{"retired past window, five-minute pass", change, "retired", pastWindow, false, false, "retired"},
		{"retired past window, daily pass", change, "retired", pastWindow, true, true, "deleted"},
		{"uncertain past window, five-minute pass", change, "uncertain", pastWindow, false, false, "uncertain"},
		{"uncertain past window, daily pass", change, "uncertain", pastWindow, true, true, "uncertain"},
		{"live row is never swept", change, "live", pastWindow, true, false, "live"},
		{"used row is never swept", enroll, "used", pastWindow, true, false, "used"},
		{"orphan, five-minute pass", change, "", 0, false, false, ""},
		{"orphan, daily pass", change, "", 0, true, true, ""},
		{"enroll orphan, daily pass", enroll, "", 0, true, true, ""},
		{"deleted row, five-minute pass", change, "deleted", pastWindow, false, false, "deleted"},
		{"deleted row, daily pass", change, "deleted", pastWindow, true, true, "deleted"},
		{"young requested row is skipped", change, "requested", 2 * time.Minute, true, false, "requested"},
		{"old requested row becomes uncertain and is swept", change, "requested", 10 * time.Minute, true, true, "uncertain"},
		{"old requested row, five-minute pass", change, "requested", 10 * time.Minute, false, false, "requested"},
		{"foreign description", "ci-runner", "", 0, true, false, ""},
		{"change namespace with a short id", "darkpanel-" + swChange(1)[1:], "", 0, true, false, ""},
		{"enroll namespace with a short id", "darkpanel-enroll-short", "", 0, true, false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := swSetup(t)
			id := f.key(t, tc.desc)
			if tc.state != "" {
				f.row(t, tc.desc, tc.state, "", tc.age)
			}
			if err := f.t.sweep(context.Background(), f.deps, f.h.clock.Now(), tc.orphans); err != nil {
				t.Fatal(err)
			}
			if got := !f.alive(id); got != tc.wantDeleted {
				t.Errorf("key deleted %v, want %v", got, tc.wantDeleted)
			}
			if got := f.state(t, tc.desc); got != tc.wantState {
				t.Errorf("row %q, want %q", got, tc.wantState)
			}
			entries, _, err := f.audit.List(context.Background(), core.AuditQuery{Actor: "system:tailnet.keysweep"})
			if err != nil {
				t.Fatal(err)
			}
			if !tc.wantDeleted {
				if len(entries) != 0 {
					t.Errorf("%d audit entries for a pass that deleted nothing", len(entries))
				}
				return
			}
			if len(entries) != 1 {
				t.Fatalf("%d audit entries, want 1", len(entries))
			}
			e := entries[0]
			var in map[string]string
			if err := json.Unmarshal(e.Input, &in); err != nil {
				t.Fatal(err)
			}
			if e.Action != "tailnet.keysweep" || e.Phase != "delete" || e.Result != "ok" ||
				in["keyId"] != id || in["description"] != tc.desc || len(in) != 2 {
				t.Errorf("audit entry %+v input %s", e, e.Input)
			}
		})
	}
}

func TestSwRetiredRowWithoutAKey(t *testing.T) {
	for _, tc := range []struct {
		age  time.Duration
		want string
	}{{time.Hour, "retired"}, {48 * time.Hour, "deleted"}} {
		f := swSetup(t)
		desc := "darkpanel-" + swChange(2)
		f.row(t, desc, "retired", "", tc.age)
		if err := f.t.sweep(context.Background(), f.deps, f.h.clock.Now(), false); err != nil {
			t.Fatal(err)
		}
		if got := f.state(t, desc); got != tc.want {
			t.Errorf("age %v: row %s, want %s", tc.age, got, tc.want)
		}
	}
}

func TestSwProviderFailureDoesNotStopThePass(t *testing.T) {
	f := swSetup(t)
	d1, d2 := "darkpanel-"+swChange(3), "darkpanel-"+swChange(4)
	k1, k2 := f.key(t, d1), f.key(t, d2)
	f.row(t, d1, "uncertain", k1, time.Hour)
	f.row(t, d2, "uncertain", k2, time.Hour)
	f.srv.Cloud().Fail(http.MethodDelete, swKeysPath+"/"+k1, cloudfake.Fault{Status: http.StatusInternalServerError, Times: 4})
	if err := f.t.sweep(context.Background(), f.deps, f.h.clock.Now(), false); err == nil {
		t.Fatal("the pass hid a failed delete")
	}
	if !f.alive(k1) || f.alive(k2) {
		t.Errorf("first key alive %v, second alive %v; want true, false", f.alive(k1), f.alive(k2))
	}
	entries, _, _ := f.audit.List(context.Background(), core.AuditQuery{Actor: "system:tailnet.keysweep"})
	if len(entries) != 1 {
		t.Errorf("%d audit entries, want 1 (only the delete that happened)", len(entries))
	}
}

// onUncertain installs a trigger that runs stmt when the row of description
// becomes uncertain, standing in for a create or a resolution that commits
// after the pass has read its rows.
func (f swFix) onUncertain(t *testing.T, description, stmt string) {
	t.Helper()
	err := f.h.db.Write(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(context.Background(), fmt.Sprintf(`CREATE TRIGGER sw_race AFTER UPDATE OF state ON tailnet_keys
			WHEN NEW.description = '%s' AND NEW.state = 'uncertain' BEGIN %s; END`, description, stmt))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestSwRowWrittenDuringThePass(t *testing.T) {
	cases := []struct {
		name      string
		state     string // "" records no row
		age       time.Duration
		withKey   bool
		stmt      string // %[1]s is the description, %[2]s the key id, %[3]s now
		wantState string
	}{
		{"uncertain row becomes live", "uncertain", time.Hour, true,
			`UPDATE tailnet_keys SET state = 'live' WHERE description = '%[1]s'`, "live"},
		{"orphan gains a row", "", 0, true,
			`INSERT INTO tailnet_keys (description, kind, owner_id, requested_at, expiry_seconds, tags_json, state, updated_at)
				VALUES ('%[1]s', 'change', 'x', '%[3]s', 3600, '[]', 'requested', '%[3]s')`, "requested"},
		{"old requested row becomes live", "requested", 10 * time.Minute, true,
			`UPDATE tailnet_keys SET state = 'live', key_id = '%[2]s' WHERE description = '%[1]s'`, "live"},
		{"retired row without a key becomes live", "retired", 48 * time.Hour, false,
			`UPDATE tailnet_keys SET state = 'live' WHERE description = '%[1]s'`, "live"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := swSetup(t)
			// The first row the pass moves to uncertain fires the trigger;
			// rows are read oldest first, so it is moved before the target.
			first := "darkpanel-" + swChange(10)
			f.row(t, first, "requested", "", 20*time.Minute)
			desc := "darkpanel-" + swChange(11)
			id := ""
			if tc.withKey {
				id = f.key(t, desc)
			}
			if tc.state != "" {
				f.row(t, desc, tc.state, id, tc.age)
			}
			f.onUncertain(t, first, fmt.Sprintf(tc.stmt, desc, id, formatStoreTime(f.h.clock.Now())))

			if err := f.t.sweep(context.Background(), f.deps, f.h.clock.Now(), true); err != nil {
				t.Fatal(err)
			}
			if got := f.state(t, first); got != "uncertain" {
				t.Fatalf("first row %q, want uncertain: the trigger never fired", got)
			}
			if tc.withKey && !f.alive(id) {
				t.Error("the pass deleted a key whose row changed after it was read")
			}
			if got := f.state(t, desc); got != tc.wantState {
				t.Errorf("row %q, want %q", got, tc.wantState)
			}
		})
	}
}

func TestSwNoAccountDoesNothing(t *testing.T) {
	srv := fake.New(t)
	h := newTNHost(t)
	deps := ext.Deps{DB: h.db, Sealer: coretest.Sealer{}, Clock: h.clock, Audit: &coretest.MemAudit{}, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if err := tnWith(srv).sweep(context.Background(), deps, h.clock.Now(), true); err != nil {
		t.Fatal(err)
	}
	if n := len(srv.Cloud().Requests()); n != 0 {
		t.Errorf("%d provider requests without an account", n)
	}
}

func TestSwControllerContributesOneService(t *testing.T) {
	f := swSetup(t)
	routes, services, err := module{t: f.t}.Controller(f.deps)
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 0 || len(services) != 1 || services[0].Name != "tailnet.keysweep" || services[0].Run == nil {
		t.Errorf("routes %d, services %+v", len(routes), services)
	}
}

func TestSwServiceSchedule(t *testing.T) {
	f := swSetup(t)
	_, services, err := module{t: f.t}.Controller(f.deps)
	if err != nil {
		t.Fatal(err)
	}
	orphanAtStart := f.key(t, "darkpanel-"+swChange(5))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- services[0].Run(ctx) }()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Run returned %v after its context ended", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("Run did not return after its context ended")
		}
	}()

	swEventually(t, "the pass at start to delete an orphan", func() bool { return !f.alive(orphanAtStart) })
	swEventually(t, "both timers to be armed", func() bool { return f.h.clock.Pending() == 2 })

	owned := "darkpanel-" + swChange(6)
	ownedID := f.key(t, owned)
	f.row(t, owned, "uncertain", ownedID, time.Minute)
	orphan := f.key(t, "darkpanel-"+swChange(7))

	f.h.clock.Advance(5 * time.Minute)
	swEventually(t, "the five-minute pass", func() bool { return !f.alive(ownedID) && f.h.clock.Pending() == 2 })
	if !f.alive(orphan) {
		t.Fatal("a five-minute pass deleted an orphan")
	}

	f.h.clock.Advance(24 * time.Hour)
	swEventually(t, "the daily pass to delete the orphan", func() bool { return !f.alive(orphan) })
}
