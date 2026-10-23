package listen

import (
	"context"
	"database/sql"
	"errors"
	"net/netip"
	"reflect"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/store"
)

var rsSpec = core.ListenerSpec{
	Addr:         netip.MustParseAddrPort("172.31.255.1:9443"),
	AllowedPeer:  netip.MustParseAddr("172.31.255.2"),
	ClientHeader: core.ClientHeaderNPM,
	PublicHost:   "panel.example.com",
	ChangeID:     "01CHANGE",
}

// rsInsert records spec as listener id at now.
func rsInsert(t *testing.T, db *store.DB, id string, spec core.ListenerSpec, now time.Time) {
	t.Helper()
	if err := db.Write(context.Background(), func(tx *sql.Tx) error { return insertListener(context.Background(), tx, id, spec, now) }); err != nil {
		t.Fatal(err)
	}
}

// rsSet moves id to state from the states in from.
func rsSet(t *testing.T, db *store.DB, id, state string, from ...string) bool {
	t.Helper()
	var changed bool
	err := db.Write(context.Background(), func(tx *sql.Tx) error {
		var err error
		changed, err = setListenerState(context.Background(), tx, id, state, from, testStart)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return changed
}

func TestRuntimeStoreRoundTrip(t *testing.T) {
	ctx := context.Background()
	db := openControllerDB(t)
	rsInsert(t, db, "L1", rsSpec, testStart)
	got, err := getListener(ctx, db, "L1")
	if err != nil {
		t.Fatal(err)
	}
	want := core.ListenerInfo{ID: "L1", Spec: rsSpec, State: core.ListenerOpen}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("getListener = %+v, want %+v", got, want)
	}
	six := rsSpec
	six.Addr = netip.MustParseAddrPort("[fd00:d0::1]:9443")
	six.AllowedPeer = netip.MustParseAddr("fd00:d0::2")
	six.ClientHeader = core.ClientHeaderTunnel
	rsInsert(t, db, "L2", six, testStart.Add(time.Second))
	if got, err := getListener(ctx, db, "L2"); err != nil || got.Spec != six {
		t.Fatalf("getListener(L2) = %+v, %v; want spec %+v", got, err, six)
	}
	var e *errcode.Error
	if _, err := getListener(ctx, db, "L9"); !errors.As(err, &e) || e.Code != errcode.NotFound {
		t.Fatalf("getListener of a missing id = %v, want not_found", err)
	}
}

func TestRuntimeStoreStates(t *testing.T) {
	ctx := context.Background()
	db := openControllerDB(t)
	rsInsert(t, db, "L2", rsSpec, testStart.Add(time.Second))
	rsInsert(t, db, "L1", rsSpec, testStart)
	rsInsert(t, db, "L3", rsSpec, testStart.Add(2*time.Second))

	if !rsSet(t, db, "L3", core.ListenerRevoked, core.ListenerOpen) {
		t.Fatal("open → revoked did not change the row")
	}
	if rsSet(t, db, "L3", core.ListenerClosed, core.ListenerOpen) {
		t.Fatal("the conditional update changed a row in another state")
	}
	if !rsSet(t, db, "L3", core.ListenerRevoked, core.ListenerOpen, core.ListenerRevoked) {
		t.Fatal("revoked → revoked did not match the row")
	}
	if rsSet(t, db, "L9", core.ListenerRevoked, core.ListenerOpen) {
		t.Fatal("a missing id changed a row")
	}
	var updated string
	if err := db.Read().QueryRowContext(ctx, `SELECT updated_at FROM core_listeners WHERE id = 'L3'`).Scan(&updated); err != nil ||
		updated != testStart.UTC().Format(listenerTimeFormat) {
		t.Fatalf("updated_at = %q, %v", updated, err)
	}

	open, err := listListeners(ctx, db, core.ListenerOpen)
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 2 || open[0].ID != "L1" || open[1].ID != "L2" {
		t.Fatalf("open listeners %+v, want L1 then L2", open)
	}
	if revoked, err := listListeners(ctx, db, core.ListenerRevoked); err != nil || len(revoked) != 1 || revoked[0].ID != "L3" {
		t.Fatalf("revoked listeners %+v, %v; want L3", revoked, err)
	}
	if closed, err := listListeners(ctx, db, core.ListenerClosed); err != nil || len(closed) != 0 {
		t.Fatalf("closed listeners %+v, %v; want none", closed, err)
	}
	err = db.Write(ctx, func(tx *sql.Tx) error {
		_, err := setListenerState(ctx, tx, "L1", core.ListenerClosed, nil, testStart)
		return err
	})
	if err == nil {
		t.Fatal("setListenerState without a from state succeeded")
	}
}

func TestRuntimeStoreRefusesBadRows(t *testing.T) {
	db := openControllerDB(t)
	for _, tc := range []struct {
		name string
		spec core.ListenerSpec
	}{
		{"an unknown client header", core.ListenerSpec{Addr: rsSpec.Addr, AllowedPeer: rsSpec.AllowedPeer, ClientHeader: "X-Forwarded-For"}},
		{"port 0", core.ListenerSpec{Addr: netip.MustParseAddrPort("172.31.255.1:0"), AllowedPeer: rsSpec.AllowedPeer, ClientHeader: core.ClientHeaderNPM}},
	} {
		err := db.Write(context.Background(), func(tx *sql.Tx) error { return insertListener(context.Background(), tx, "B", tc.spec, testStart) })
		if err == nil {
			t.Errorf("%s: recorded", tc.name)
		}
	}
	rsInsert(t, db, "L1", rsSpec, testStart)
	err := db.Write(context.Background(), func(tx *sql.Tx) error {
		_, err := setListenerState(context.Background(), tx, "L1", "paused", []string{core.ListenerOpen}, testStart)
		return err
	})
	if err == nil {
		t.Error("state paused accepted")
	}
}
