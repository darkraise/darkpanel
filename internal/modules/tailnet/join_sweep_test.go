package tailnet

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/ext"
	"github.com/darkraise/darkpanel/internal/modules/tailnet/fake"
)

// jsHostID returns a 26-character host id that differs by n.
func jsHostID(n int) string { return fmt.Sprintf("01J9X4K8Q2M5N7P0R3S6T8W%03d", n) }

// jsHost records a core_hosts row in the given state.
func jsHost(t *testing.T, h *tnHost, id, state string) {
	t.Helper()
	ctx := context.Background()
	err := h.db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO core_hosts (id, name, is_local, created_at, state) VALUES (?, ?, 0, ?, ?)`,
			id, "host-"+id[len(id)-3:], formatStoreTime(h.clock.Now()), state)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

// jsToken records the enrollment token of a host, expiring at expires and
// consumed or not.
func jsToken(t *testing.T, h *tnHost, hostID string, expires time.Time, consumed bool) {
	t.Helper()
	ctx := context.Background()
	var consumedAt sql.NullString
	if consumed {
		consumedAt = sql.NullString{String: formatStoreTime(h.clock.Now()), Valid: true}
	}
	err := h.db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO core_enroll_tokens (host_id, hash, expires_at, consumed_at, created_at) VALUES (?, x'00', ?, ?, ?)`,
			hostID, formatStoreTime(expires), consumedAt, formatStoreTime(h.clock.Now()))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

// jsKey records the enroll key row of host, requested age ago with a one hour
// expiry, and returns its description.
func jsKey(t *testing.T, h *tnHost, host, kind, state string, age time.Duration) string {
	t.Helper()
	description := "darkpanel-enroll-" + host
	if kind == "change" {
		description = "darkpanel-" + host
	}
	now := h.clock.Now()
	err := h.db.Write(context.Background(), func(tx *sql.Tx) error {
		return insertKey(context.Background(), tx, keyRow{
			Description: description, Kind: kind, OwnerID: host, State: state,
			RequestedAt: now.Add(-age), UpdatedAt: now.Add(-age), ExpirySeconds: 3600, Tags: []string{"tag:ci"},
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	return description
}

func jsRow(t *testing.T, h *tnHost, description string) keyRow {
	t.Helper()
	r, ok, err := loadKey(context.Background(), h.db.Read(), description)
	if err != nil || !ok {
		t.Fatalf("row %s: found %v, error %v", description, ok, err)
	}
	return r
}

func TestJsSettleEnrollKeys(t *testing.T) {
	const (
		young = time.Minute
		// The enroll key lives one hour; a row older than that plus 15 minutes is over.
		window = 75 * time.Minute
	)
	cases := []struct {
		name  string
		state string        // the key row's state
		host  string        // the owner's state; "" records no core_hosts row
		token string        // "", "open", "expired", "now", "soon" or "consumed-expired"
		age   time.Duration // how long ago the key was requested
		want  string
	}{
		{"live key of an enrolled host is used", "live", "enrolled", "", young, "used"},
		{"uncertain key of an enrolled host is used", "uncertain", "enrolled", "", young, "used"},
		{"enrolled wins over an expired token", "live", "enrolled", "expired", young, "used"},
		{"enrolled wins over age", "live", "enrolled", "", 48 * time.Hour, "used"},
		{"live key of a pending host with an open token stays live", "live", "pending", "open", young, "live"},
		{"uncertain key of a pending host with an open token stays uncertain", "uncertain", "pending", "open", young, "uncertain"},
		{"owner row gone retires a live key", "live", "", "", young, "retired"},
		{"owner row gone retires an uncertain key", "uncertain", "", "", young, "retired"},
		{"expired unconsumed token retires the key", "live", "pending", "expired", young, "retired"},
		{"a token expiring now is expired", "live", "pending", "now", young, "retired"},
		{"a token expiring in a second is open", "live", "pending", "soon", young, "live"},
		{"a consumed token is not an unconsumed one", "live", "pending", "consumed-expired", young, "live"},
		{"no token and exactly the window old stays live", "live", "pending", "", window, "live"},
		{"no token and past the window retires the key", "live", "pending", "", window + time.Second, "retired"},
		{"requested row is left to the sweep", "requested", "", "", 48 * time.Hour, "requested"},
		{"used row is final", "used", "", "", 48 * time.Hour, "used"},
		{"retired row is final", "retired", "enrolled", "", young, "retired"},
		{"deleted row is final", "deleted", "enrolled", "", young, "deleted"},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newTNHost(t)
			now := h.clock.Now()
			owner := jsHostID(i)
			if tc.host != "" {
				jsHost(t, h, owner, tc.host)
			}
			switch tc.token {
			case "open":
				jsToken(t, h, owner, now.Add(30*time.Minute), false)
			case "expired":
				jsToken(t, h, owner, now.Add(-time.Second), false)
			case "now":
				jsToken(t, h, owner, now, false)
			case "soon":
				jsToken(t, h, owner, now.Add(time.Second), false)
			case "consumed-expired":
				jsToken(t, h, owner, now.Add(-time.Second), true)
			}
			description := jsKey(t, h, owner, "enroll", tc.state, tc.age)
			if err := settleEnrollKeys(context.Background(), h.db, now); err != nil {
				t.Fatal(err)
			}
			r := jsRow(t, h, description)
			if r.State != tc.want {
				t.Errorf("row %q, want %q", r.State, tc.want)
			}
			wantAt := now.Add(-tc.age)
			if r.State != tc.state {
				wantAt = now
			}
			if !r.UpdatedAt.Equal(wantAt) {
				t.Errorf("updated at %v, want %v", r.UpdatedAt, wantAt)
			}
			if err := settleEnrollKeys(context.Background(), h.db, now); err != nil {
				t.Fatal(err)
			}
			if again := jsRow(t, h, description); again.State != tc.want {
				t.Errorf("row %q after a second pass, want %q", again.State, tc.want)
			}
		})
	}
}

func TestJsSettleLeavesChangeKeysAlone(t *testing.T) {
	h := newTNHost(t)
	description := jsKey(t, h, jsHostID(90), "change", "live", time.Minute)
	if err := settleEnrollKeys(context.Background(), h.db, h.clock.Now()); err != nil {
		t.Fatal(err)
	}
	if r := jsRow(t, h, description); r.State != "live" {
		t.Errorf("a change key's row %q, want live", r.State)
	}
}

func TestJsSettleMovesEveryRow(t *testing.T) {
	h := newTNHost(t)
	enrolled, gone := jsHostID(91), jsHostID(92)
	jsHost(t, h, enrolled, "enrolled")
	used := jsKey(t, h, enrolled, "enroll", "live", time.Minute)
	retired := jsKey(t, h, gone, "enroll", "live", time.Minute)
	if err := settleEnrollKeys(context.Background(), h.db, h.clock.Now()); err != nil {
		t.Fatal(err)
	}
	if got := jsRow(t, h, used).State; got != "used" {
		t.Errorf("enrolled host's key %q, want used", got)
	}
	if got := jsRow(t, h, retired).State; got != "retired" {
		t.Errorf("missing host's key %q, want retired", got)
	}
}

func TestJsSweepSettlesBeforeItDeletes(t *testing.T) {
	f := swSetup(t)
	enrolled, gone := jsHostID(93), jsHostID(94)
	jsHost(t, f.h, enrolled, "enrolled")
	usedDesc := jsKey(t, f.h, enrolled, "enroll", "live", time.Minute)
	goneDesc := jsKey(t, f.h, gone, "enroll", "live", time.Minute)
	usedID, goneID := f.key(t, usedDesc), f.key(t, goneDesc)

	if err := f.t.sweep(context.Background(), f.deps, f.h.clock.Now(), false); err != nil {
		t.Fatal(err)
	}
	if !f.alive(usedID) {
		t.Error("the pass deleted the key of an enrolled host")
	}
	if got := jsRow(t, f.h, usedDesc).State; got != "used" {
		t.Errorf("enrolled host's row %q, want used", got)
	}
	if f.alive(goneID) {
		t.Error("the pass left the key of a host that no longer exists")
	}
	if got := jsRow(t, f.h, goneDesc).State; got != "deleted" {
		t.Errorf("missing host's row %q, want deleted", got)
	}
}

func TestJsSweepSettlesWithoutAnAccount(t *testing.T) {
	srv := fake.New(t)
	h := newTNHost(t)
	description := jsKey(t, h, jsHostID(95), "enroll", "live", time.Minute)
	deps := ext.Deps{DB: h.db, Sealer: coretest.Sealer{}, Clock: h.clock, Audit: &coretest.MemAudit{}, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if err := tnWith(srv).sweep(context.Background(), deps, h.clock.Now(), true); err != nil {
		t.Fatal(err)
	}
	if got := jsRow(t, h, description).State; got != "retired" {
		t.Errorf("row %q, want retired", got)
	}
	if n := len(srv.Cloud().Requests()); n != 0 {
		t.Errorf("%d provider requests without an account", n)
	}
}
