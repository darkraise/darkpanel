package panel

import (
	"context"
	"database/sql"
	"net/netip"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core/store"
)

// fdRow is a live npm exposure whose id is id.
func fdRow(id string) exposureRow {
	return exposureRow{
		ID: id, Via: ViaNPM, Hostname: "panel.example.org", HostID: "h1", Subnet: netip.MustParsePrefix("172.31.255.248/29"),
		Gateway: netip.MustParseAddr("172.31.255.249"), Proxy: netip.MustParseAddr("172.31.255.250"), Port: DefaultPort,
		ListenerID: "l1", RuleID: RuleID(id), State: StateOpen, CreatedAt: fdNow, UpdatedAt: fdNow,
	}
}

func fdWrite(t *testing.T, db *store.DB, fn func(ctx context.Context, tx *sql.Tx) error) {
	t.Helper()
	ctx := context.Background()
	if err := db.Write(ctx, func(tx *sql.Tx) error { return fn(ctx, tx) }); err != nil {
		t.Fatal(err)
	}
}

// TestFdMigrationConstraints: the migration makes a STRICT table that refuses a second row of an id, a route that is
// neither npm nor tunnel and a state outside open, revoked and needs_repair.
func TestFdMigrationConstraints(t *testing.T) {
	ctx := context.Background()
	db := fdDB(t)
	fdInsertExposure(t, db, fdRow("c1"))
	var strict int
	if err := db.Read().QueryRowContext(ctx, `SELECT strict FROM pragma_table_list('panel_exposures')`).Scan(&strict); err != nil || strict != 1 {
		t.Errorf("panel_exposures strict = %d, err %v; want a STRICT table", strict, err)
	}
	const insert = `INSERT INTO panel_exposures (id, via, hostname, host_id, subnet, gateway, proxy, port, listener_id, rule_id, state, created_at, updated_at)
		VALUES (?, ?, 'h', 'h1', 's', 'g', 'p', 9443, 'l', 'r', ?, 't', 't')`
	cases := map[string][]any{
		"same id":       {"c1", "npm", "open"},
		"unknown via":   {"c2", "ssh", "open"},
		"unknown state": {"c3", "npm", "closed"},
	}
	for name, args := range cases {
		err := db.Write(ctx, func(tx *sql.Tx) error { _, err := tx.ExecContext(ctx, insert, args...); return err })
		if err == nil {
			t.Errorf("%s: the insert succeeded", name)
		}
	}
}

// TestFdExposureRoundTrip: a row is stored and read back whole, the times in UTC, and view() shows it.
func TestFdExposureRoundTrip(t *testing.T) {
	ctx := context.Background()
	db := fdDB(t)
	if _, ok, err := liveExposure(ctx, db.Read()); err != nil || ok {
		t.Fatalf("empty table: ok %v, err %v", ok, err)
	}
	in := fdRow("c1")
	in.Via, in.Tunnel = ViaTunnel, "0123abcd-0123-4abc-8def-0123456789ab"
	in.CreatedAt = fdNow.In(time.FixedZone("x", 7*3600))
	in.UpdatedAt = fdNow.Add(time.Minute)
	fdInsertExposure(t, db, in)
	got, ok, err := liveExposure(ctx, db.Read())
	if err != nil || !ok {
		t.Fatalf("liveExposure: ok %v, err %v", ok, err)
	}
	if !got.CreatedAt.Equal(fdNow) || !got.UpdatedAt.Equal(fdNow.Add(time.Minute)) {
		t.Errorf("times %v and %v, want %v and %v", got.CreatedAt, got.UpdatedAt, fdNow, fdNow.Add(time.Minute))
	}
	got.CreatedAt, got.UpdatedAt = in.CreatedAt, in.UpdatedAt
	if got != in {
		t.Errorf("liveExposure = %+v, want %+v", got, in)
	}
	v := got.view()
	want := ExposureView{ID: "c1", Via: ViaTunnel, Hostname: "panel.example.org", HostID: "h1", Tunnel: in.Tunnel, Subnet: in.Subnet,
		Gateway: in.Gateway, Proxy: in.Proxy, Port: DefaultPort, State: StateOpen, ListenerID: "l1", CreatedAt: in.CreatedAt}
	if v != want {
		t.Errorf("view() = %+v, want %+v", v, want)
	}
	fdWrite(t, db, func(ctx context.Context, tx *sql.Tx) error {
		err := insertExposure(ctx, tx, fdRow("c1"))
		if err == nil || !strings.HasPrefix(err.Error(), "panel:") {
			t.Errorf("a second insert of c1 = %v, want a panel: error", err)
		}
		return nil
	})
}

// TestFdLiveAndGone: an exposure is gone only when it is revoked and its removal change completed; every other row is
// live, and liveExposure and GoneSQL agree.
func TestFdLiveAndGone(t *testing.T) {
	tests := []struct {
		name      string
		state     string
		removal   string // removal_change_id
		removalAs string // the state of the change removal names; "" inserts no change
		gone      bool
	}{
		{name: "open", state: StateOpen},
		{name: "needs repair", state: StateNeedsRepair},
		{name: "revoked without a removal", state: StateRevoked},
		{name: "revoked by a missing change", state: StateRevoked, removal: "r1"},
		{name: "revoked by a change still applying", state: StateRevoked, removal: "r1", removalAs: "applying"},
		{name: "revoked by a change that needs repair", state: StateRevoked, removal: "r1", removalAs: "needs_repair"},
		{name: "revoked by a failed change", state: StateRevoked, removal: "r1", removalAs: "failed"},
		{name: "revoked by a completed change", state: StateRevoked, removal: "r1", removalAs: "completed", gone: true},
		{name: "open with a completed removal change recorded", state: StateOpen, removal: "r1", removalAs: "completed"},
		{name: "needs repair with a completed removal change recorded", state: StateNeedsRepair, removal: "r1", removalAs: "completed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			db := fdDB(t)
			if tt.removalAs != "" {
				fdInsertChange(t, db, tt.removal, ActionRemove, tt.removalAs)
			}
			r := fdRow("c1")
			r.State, r.RemovalChangeID = tt.state, tt.removal
			fdInsertExposure(t, db, r)
			got, ok, err := liveExposure(ctx, db.Read())
			if err != nil {
				t.Fatal(err)
			}
			if ok == tt.gone || (ok && got.ID != "c1") {
				t.Errorf("liveExposure = %q, %v; gone = %v", got.ID, ok, tt.gone)
			}
			var ids []string
			rows, err := db.Read().QueryContext(ctx, "SELECT e.id FROM panel_exposures e WHERE "+GoneSQL)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = rows.Close() }()
			for rows.Next() {
				var id string
				if err := rows.Scan(&id); err != nil {
					t.Fatal(err)
				}
				ids = append(ids, id)
			}
			if (len(ids) == 1) != tt.gone || len(ids) > 1 {
				t.Errorf("GoneSQL matched %v, gone = %v", ids, tt.gone)
			}
		})
	}
}

// TestFdLiveExposureSkipsGoneRows: after a removal completed and a new exposure was made, liveExposure answers the new
// one.
func TestFdLiveExposureSkipsGoneRows(t *testing.T) {
	ctx := context.Background()
	db := fdDB(t)
	fdInsertChange(t, db, "r1", ActionRemove, "completed")
	old := fdRow("c1")
	old.State, old.RemovalChangeID = StateRevoked, "r1"
	fdInsertExposure(t, db, old)
	fresh := fdRow("c2")
	fresh.CreatedAt = fdNow.Add(time.Hour)
	fdInsertExposure(t, db, fresh)
	got, ok, err := liveExposure(ctx, db.Read())
	if err != nil || !ok || got.ID != "c2" {
		t.Fatalf("liveExposure = %q, %v, %v; want c2", got.ID, ok, err)
	}
}

// TestFdSetExposureState: the state and the update time change; a removal change id is recorded only when given; a
// missing exposure is an error.
func TestFdSetExposureState(t *testing.T) {
	ctx := context.Background()
	db := fdDB(t)
	fdInsertExposure(t, db, fdRow("c1"))
	later := fdNow.Add(time.Minute)
	fdWrite(t, db, func(ctx context.Context, tx *sql.Tx) error {
		return setExposureState(ctx, tx, "c1", StateNeedsRepair, "", later)
	})
	got, _, err := liveExposure(ctx, db.Read())
	if err != nil {
		t.Fatal(err)
	}
	if got.State != StateNeedsRepair || got.RemovalChangeID != "" || !got.UpdatedAt.Equal(later) || !got.CreatedAt.Equal(fdNow) {
		t.Errorf("after needs_repair: %+v", got)
	}
	fdWrite(t, db, func(ctx context.Context, tx *sql.Tx) error {
		return setExposureState(ctx, tx, "c1", StateRevoked, "r1", later.Add(time.Minute))
	})
	fdWrite(t, db, func(ctx context.Context, tx *sql.Tx) error {
		return setExposureState(ctx, tx, "c1", StateRevoked, "", later.Add(2*time.Minute))
	})
	got, _, err = liveExposure(ctx, db.Read())
	if err != nil {
		t.Fatal(err)
	}
	if got.State != StateRevoked || got.RemovalChangeID != "r1" || !got.UpdatedAt.Equal(later.Add(2*time.Minute)) {
		t.Errorf("after revoked: %+v, want removal change r1 kept", got)
	}
	err = db.Write(ctx, func(tx *sql.Tx) error { return setExposureState(ctx, tx, "nope", StateRevoked, "", later) })
	if err == nil || !strings.Contains(err.Error(), "panel:") {
		t.Errorf("setExposureState of a missing id = %v, want a panel: error", err)
	}
	err = db.Write(ctx, func(tx *sql.Tx) error { return setExposureState(ctx, tx, "c1", "closed", "", later) })
	if err == nil {
		t.Error("setExposureState to a state outside the table's list succeeded")
	}
}

// TestFdDeleteExposure: the row goes, and deleting it again is not an error.
func TestFdDeleteExposure(t *testing.T) {
	ctx := context.Background()
	db := fdDB(t)
	fdInsertExposure(t, db, fdRow("c1"))
	for range 2 {
		fdWrite(t, db, func(ctx context.Context, tx *sql.Tx) error { return deleteExposure(ctx, tx, "c1") })
	}
	if _, ok, err := liveExposure(ctx, db.Read()); err != nil || ok {
		t.Errorf("after delete: ok %v, err %v", ok, err)
	}
}

// TestFdRuleID: the rule id is a 26-character firewall id, the same for one exposure id and different for another, and
// two ids hash to the values below (SHA-256 of "darkpanel/panel/" + id, Crockford base-32, first 25 characters).
func TestFdRuleID(t *testing.T) {
	if got, want := RuleID("01JC0000000000000000000001"), "0T6TGP8XA19D3CXP6GZDFX42FX"; got != want {
		t.Errorf("RuleID(...001) = %s, want %s", got, want)
	}
	if got, want := RuleID("01JC0000000000000000000002"), "0JBCM1G2AKVMZ62MEWM3JG206E"; got != want {
		t.Errorf("RuleID(...002) = %s, want %s", got, want)
	}
	shape := regexp.MustCompile(`^0[0-9A-HJKMNP-TV-Z]{25}$`)
	for _, id := range []string{"", "c1", "01JC0000000000000000000003"} {
		if got := RuleID(id); !shape.MatchString(got) || got != RuleID(id) {
			t.Errorf("RuleID(%q) = %q, want a stable 0 + 25 Crockford characters", id, got)
		}
	}
	if RuleID("c1") == RuleID("c2") {
		t.Error("two exposure ids share a rule id")
	}
}
