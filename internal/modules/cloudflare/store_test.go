package cloudflare

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/cloud"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/store"
)

// cfDB opens a temporary controller database with the core's and the
// Cloudflare module's migrations applied.
func cfDB(t *testing.T) *store.DB {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "controller.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	set, err := store.CoreMigrations(store.SideController)
	if err != nil {
		t.Fatal(err)
	}
	res, err := store.Migrate(ctx, db, store.MigrateOptions{
		Side: store.SideController, BinaryVersion: "test", BackupRoot: filepath.Join(t.TempDir(), "backups"),
		Keep: 5, Now: time.Now(),
	}, set, []store.MigrationSet{{Module: ID, FS: controllerMigrations()}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Failed) > 0 {
		t.Fatalf("cloudflare migrations failed: %v", res.Failed[0].Err)
	}
	return db
}

var cfT0 = time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)

// cfWrite runs fn in a write transaction and fails the test on its error.
func cfWrite(t *testing.T, db *store.DB, fn func(ctx context.Context, tx *sql.Tx) error) {
	t.Helper()
	ctx := context.Background()
	if err := db.Write(ctx, func(tx *sql.Tx) error { return fn(ctx, tx) }); err != nil {
		t.Fatal(err)
	}
}

func cfAccount() account {
	return account{
		ID: "01J000000000000000000000A1", AccountID: "0123456789abcdef0123456789abcdef", TokenCT: []byte{0, 1, 2, 0xff},
		Capabilities: []cloud.Capability{
			{Name: "tunnels.read", State: cloud.CapabilityOK},
			{Name: "zones.read", State: cloud.CapabilityMissing, Detail: "cloud.permission_missing"},
			{Name: "tunnels.write", State: cloud.CapabilityUnverified},
		},
		ConnectedAt: cfT0,
	}
}

func cfTunnel(n int) tunnelRow {
	return tunnelRow{
		ID: fmt.Sprintf("tunnel-%d-0000", n), Name: fmt.Sprintf("darkpanel-01J00000000000000000000T%02d", n),
		ChangeID: fmt.Sprintf("01J00000000000000000000T%02d", n), CreatedAt: cfT0.Add(time.Duration(n) * time.Minute),
	}
}

func cfHostname(name, tunnelID, phase string) hostnameRow {
	return hostnameRow{Hostname: name, TunnelID: tunnelID, Service: "http://app:8080", Phase: phase, ChangeID: "01J00000000000000000000H01", UpdatedAt: cfT0}
}

// cfEffect inserts an effect row of a new change into core_effects.
func cfEffect(t *testing.T, db *store.DB, changeID string, seq int, resource, state string) {
	t.Helper()
	at := cfT0.Format("2006-01-02T15:04:05.000000000Z")
	cfWrite(t, db, func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO core_plans (id, host_id, action, version, actor, input_hash, state_hash,
			diff_json, guards_json, confirmation, created_at, expires_at)
			VALUES (?, '', 'cloudflare.tunnel.create', 1, 'user:u1', '', '', '[]', '[]', '', ?, ?)`, "p-"+changeID, at, at); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO core_changes (id, plan_id, host_id, action, version, class, compensated_by,
			actor, state, input_json, created_at, updated_at)
			VALUES (?, ?, '', 'cloudflare.tunnel.create', 1, 'compensable', '', 'user:u1', 'needs_repair', '{}', ?, ?)`,
			changeID, "p-"+changeID, at, at); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO core_effects (change_id, seq, kind, resource, local, inverse_local, before_json,
			state, created_at, updated_at) VALUES (?, ?, 'create', ?, 0, 0, '{"state":null}', ?, ?, ?)`,
			changeID, seq, resource, state, at, at)
		return err
	})
}

// TestCFMigrationConstraints: the migration's own constraints hold, whatever
// the store functions check: one account row, unique tunnel names, a
// hostname's tunnel must exist, and the phase, health and flag columns take
// only their listed values.
func TestCFMigrationConstraints(t *testing.T) {
	ctx := context.Background()
	db := cfDB(t)
	cfWrite(t, db, func(ctx context.Context, tx *sql.Tx) error {
		if err := insertAccount(ctx, tx, cfAccount()); err != nil {
			return err
		}
		return insertTunnel(ctx, tx, cfTunnel(0))
	})
	tun := cfTunnel(0)
	cases := map[string]string{
		"second account": `INSERT INTO cloudflare_accounts (id, account_id, token_ct, capabilities_json, connected_at)
			VALUES ('other', 'x', x'00', '[]', '2026-10-30T12:00:00.000000000Z')`,
		"tunnel name taken": `INSERT INTO cloudflare_tunnels (id, name, change_id, created_at)
			VALUES ('other', '` + tun.Name + `', 'c', '2026-10-30T12:00:00.000000000Z')`,
		"drift 2": `INSERT INTO cloudflare_tunnels (id, name, change_id, drift, created_at)
			VALUES ('other', 'n', 'c', 2, '2026-10-30T12:00:00.000000000Z')`,
		"hostname without tunnel": `INSERT INTO cloudflare_hostnames (hostname, tunnel_id, service, phase, change_id, updated_at)
			VALUES ('a.example.com', 'missing', 's', 'active', 'c', '2026-10-30T12:00:00.000000000Z')`,
		"unknown phase": `INSERT INTO cloudflare_hostnames (hostname, tunnel_id, service, phase, change_id, updated_at)
			VALUES ('a.example.com', '` + tun.ID + `', 's', 'done', 'c', '2026-10-30T12:00:00.000000000Z')`,
		"unknown health": `INSERT INTO cloudflare_hostnames (hostname, tunnel_id, service, phase, change_id, health, updated_at)
			VALUES ('a.example.com', '` + tun.ID + `', 's', 'active', 'c', 'fine', '2026-10-30T12:00:00.000000000Z')`,
		"no_tls_verify 2": `INSERT INTO cloudflare_hostnames (hostname, tunnel_id, service, phase, change_id, no_tls_verify, updated_at)
			VALUES ('a.example.com', '` + tun.ID + `', 's', 'active', 'c', 2, '2026-10-30T12:00:00.000000000Z')`,
	}
	for name, q := range cases {
		err := db.Write(ctx, func(tx *sql.Tx) error { _, err := tx.ExecContext(ctx, q); return err })
		if err == nil {
			t.Errorf("%s: the insert succeeded", name)
		}
	}
	for _, table := range []string{"cloudflare_accounts", "cloudflare_tunnels", "cloudflare_hostnames"} {
		var strict int
		if err := db.Read().QueryRowContext(ctx, `SELECT strict FROM pragma_table_list(?)`, table).Scan(&strict); err != nil || strict != 1 {
			t.Errorf("%s: strict = %d, err %v; want a STRICT table", table, strict, err)
		}
	}
}

// TestCFAccountRoundTrip: an account is stored and read back whole, the
// sealed secret byte for byte, and a second account is refused.
func TestCFAccountRoundTrip(t *testing.T) {
	ctx := context.Background()
	db := cfDB(t)
	if _, ok, err := loadAccount(ctx, db.Read()); err != nil || ok {
		t.Fatalf("empty database: ok %v, err %v", ok, err)
	}
	in := cfAccount()
	in.ConnectedAt = cfT0.In(time.FixedZone("x", 7*3600))
	cfWrite(t, db, func(ctx context.Context, tx *sql.Tx) error { return insertAccount(ctx, tx, in) })
	got, ok, err := loadAccount(ctx, db.Read())
	if err != nil || !ok {
		t.Fatalf("loadAccount: ok %v, err %v", ok, err)
	}
	if got.ID != in.ID || got.AccountID != in.AccountID || !bytes.Equal(got.TokenCT, in.TokenCT) ||
		!reflect.DeepEqual(got.Capabilities, in.Capabilities) || !got.ConnectedAt.Equal(cfT0) {
		t.Errorf("loadAccount = %+v, want %+v", got, in)
	}
	err = db.Write(ctx, func(tx *sql.Tx) error {
		second := cfAccount()
		second.ID = "01J000000000000000000000A2"
		return insertAccount(ctx, tx, second)
	})
	if err == nil || !strings.HasPrefix(err.Error(), "cloudflare:") {
		t.Errorf("second insertAccount error = %v, want a cloudflare: error", err)
	}
	cfWrite(t, db, func(ctx context.Context, tx *sql.Tx) error {
		if err := deleteAccount(ctx, tx, "no-such-id"); err != nil {
			return err
		}
		return deleteAccount(ctx, tx, in.ID)
	})
	if _, ok, _ := loadAccount(ctx, db.Read()); ok {
		t.Error("the account is still there after deleteAccount")
	}
}

// TestCFAccountWithoutCapabilities: an account with no capabilities is read
// back as an empty list, not an error.
func TestCFAccountWithoutCapabilities(t *testing.T) {
	ctx := context.Background()
	db := cfDB(t)
	in := cfAccount()
	in.Capabilities = nil
	cfWrite(t, db, func(ctx context.Context, tx *sql.Tx) error { return insertAccount(ctx, tx, in) })
	got, ok, err := loadAccount(ctx, db.Read())
	if err != nil || !ok || len(got.Capabilities) != 0 {
		t.Errorf("loadAccount = %+v, %v, %v", got, ok, err)
	}
}

// TestCFTunnels: tunnels are found by id or by name and listed oldest first;
// the hash and the check are stored; a tunnel with hostnames cannot be
// deleted.
func TestCFTunnels(t *testing.T) {
	ctx := context.Background()
	db := cfDB(t)
	t1, t0 := cfTunnel(1), cfTunnel(0)
	cfWrite(t, db, func(ctx context.Context, tx *sql.Tx) error {
		if err := insertTunnel(ctx, tx, t1); err != nil {
			return err
		}
		return insertTunnel(ctx, tx, t0)
	})
	list, err := listTunnels(ctx, db.Read())
	if err != nil || len(list) != 2 || list[0].ID != t0.ID || list[1].ID != t1.ID {
		t.Fatalf("listTunnels = %+v, %v; want %s then %s", list, err, t0.ID, t1.ID)
	}
	for _, key := range []string{t0.ID, t0.Name} {
		got, ok, err := loadTunnel(ctx, db.Read(), key)
		if err != nil || !ok || got.ID != t0.ID || got.Name != t0.Name || got.ChangeID != t0.ChangeID ||
			!got.CreatedAt.Equal(t0.CreatedAt) || got.Drift || got.ConfigHash != "" || !got.CheckedAt.IsZero() {
			t.Errorf("loadTunnel(%q) = %+v, %v, %v", key, got, ok, err)
		}
	}
	if _, ok, err := loadTunnel(ctx, db.Read(), "missing"); err != nil || ok {
		t.Errorf("loadTunnel(missing): ok %v, err %v", ok, err)
	}

	checked := cfT0.Add(time.Hour)
	cfWrite(t, db, func(ctx context.Context, tx *sql.Tx) error {
		if err := setTunnelHash(ctx, tx, t0.ID, "abc123"); err != nil {
			return err
		}
		return setTunnelCheck(ctx, tx, t0.ID, true, checked)
	})
	got, _, _ := loadTunnel(ctx, db.Read(), t0.ID)
	if got.ConfigHash != "abc123" || !got.Drift || !got.CheckedAt.Equal(checked) {
		t.Errorf("after setTunnelHash and setTunnelCheck: %+v", got)
	}
	other, _, _ := loadTunnel(ctx, db.Read(), t1.ID)
	if other.ConfigHash != "" || other.Drift {
		t.Errorf("another tunnel changed: %+v", other)
	}
	err = db.Write(ctx, func(tx *sql.Tx) error { return setTunnelHash(ctx, tx, "missing", "x") })
	if err == nil || !strings.Contains(err.Error(), "missing") {
		t.Errorf("setTunnelHash of a missing tunnel: %v", err)
	}

	cfWrite(t, db, func(ctx context.Context, tx *sql.Tx) error {
		return insertHostname(ctx, tx, cfHostname("a.example.com", t0.ID, "active"))
	})
	if err := db.Write(ctx, func(tx *sql.Tx) error { return deleteTunnel(ctx, tx, t0.ID) }); err == nil {
		t.Error("deleteTunnel succeeded with a hostname row on the tunnel")
	}
	cfWrite(t, db, func(ctx context.Context, tx *sql.Tx) error { return deleteTunnel(ctx, tx, t1.ID) })
	if _, ok, _ := loadTunnel(ctx, db.Read(), t1.ID); ok {
		t.Error("the tunnel is still there after deleteTunnel")
	}
}

// TestCFHostnames: a hostname row is stored and read back, its phase moves
// with the zone and record ids kept when the call gives none, its health is
// recorded without touching its phase, and an unknown phase or hostname
// fails.
func TestCFHostnames(t *testing.T) {
	ctx := context.Background()
	db := cfDB(t)
	tun := cfTunnel(0)
	in := cfHostname("b.example.com", tun.ID, "adding_rule")
	in.NoTLSVerify = true
	cfWrite(t, db, func(ctx context.Context, tx *sql.Tx) error {
		if err := insertTunnel(ctx, tx, tun); err != nil {
			return err
		}
		if err := insertHostname(ctx, tx, cfHostname("c.example.com", tun.ID, "active")); err != nil {
			return err
		}
		return insertHostname(ctx, tx, in)
	})
	list, err := listHostnames(ctx, db.Read())
	if err != nil || len(list) != 2 || list[0].Hostname != "b.example.com" || list[1].Hostname != "c.example.com" {
		t.Fatalf("listHostnames = %+v, %v; want b before c", list, err)
	}
	got, ok, err := loadHostname(ctx, db.Read(), "b.example.com")
	if err != nil || !ok {
		t.Fatalf("loadHostname: ok %v, err %v", ok, err)
	}
	want := in
	want.Health = "unknown"
	if !reflect.DeepEqual(got, want) {
		t.Errorf("loadHostname = %+v, want %+v", got, want)
	}
	if _, ok, err := loadHostname(ctx, db.Read(), "z.example.com"); err != nil || ok {
		t.Errorf("loadHostname(missing): ok %v, err %v", ok, err)
	}

	at1, at2, at3 := cfT0.Add(time.Minute), cfT0.Add(2*time.Minute), cfT0.Add(3*time.Minute)
	cfWrite(t, db, func(ctx context.Context, tx *sql.Tx) error {
		if err := setHostnamePhase(ctx, tx, "b.example.com", "adding_record", "zone1", "", at1); err != nil {
			return err
		}
		if err := setHostnamePhase(ctx, tx, "b.example.com", "active", "", "rec1", at2); err != nil {
			return err
		}
		return setHostnameHealth(ctx, tx, "b.example.com", "record_missing", at3)
	})
	got, _, _ = loadHostname(ctx, db.Read(), "b.example.com")
	if got.Phase != "active" || got.ZoneID != "zone1" || got.RecordID != "rec1" || !got.UpdatedAt.Equal(at2) ||
		got.Health != "record_missing" || !got.CheckedAt.Equal(at3) {
		t.Errorf("after the updates: %+v", got)
	}
	untouched, _, _ := loadHostname(ctx, db.Read(), "c.example.com")
	if untouched.Phase != "active" || untouched.RecordID != "" || untouched.Health != "unknown" {
		t.Errorf("another hostname changed: %+v", untouched)
	}

	for name, fn := range map[string]func(ctx context.Context, tx *sql.Tx) error{
		"unknown phase": func(ctx context.Context, tx *sql.Tx) error {
			return setHostnamePhase(ctx, tx, "b.example.com", "done", "", "", at1)
		},
		"unknown hostname": func(ctx context.Context, tx *sql.Tx) error {
			return setHostnamePhase(ctx, tx, "z.example.com", "active", "", "", at1)
		},
		"unknown health": func(ctx context.Context, tx *sql.Tx) error {
			return setHostnameHealth(ctx, tx, "b.example.com", "fine", at1)
		},
		"health of nobody": func(ctx context.Context, tx *sql.Tx) error {
			return setHostnameHealth(ctx, tx, "z.example.com", "ok", at1)
		},
	} {
		if err := db.Write(ctx, func(tx *sql.Tx) error { return fn(ctx, tx) }); err == nil {
			t.Errorf("%s: no error", name)
		}
	}

	cfWrite(t, db, func(ctx context.Context, tx *sql.Tx) error { return deleteHostname(ctx, tx, "b.example.com") })
	if _, ok, _ := loadHostname(ctx, db.Read(), "b.example.com"); ok {
		t.Error("the hostname is still there after deleteHostname")
	}
	if _, ok, _ := loadHostname(ctx, db.Read(), "c.example.com"); !ok {
		t.Error("deleteHostname removed another hostname")
	}
}

// TestCFCleanupPending: only a hostname outside the active phase and an
// unresolved effect of this module block deleting the account.
func TestCFCleanupPending(t *testing.T) {
	db := cfDB(t)
	pending := func() []string {
		t.Helper()
		var out []string
		cfWrite(t, db, func(ctx context.Context, tx *sql.Tx) (err error) {
			out, err = cleanupPending(ctx, tx)
			return err
		})
		return out
	}
	if got := pending(); len(got) != 0 {
		t.Fatalf("empty database: pending %q", got)
	}

	tun := cfTunnel(0)
	cfWrite(t, db, func(ctx context.Context, tx *sql.Tx) error {
		if err := insertTunnel(ctx, tx, tun); err != nil {
			return err
		}
		return insertHostname(ctx, tx, cfHostname("a.example.com", tun.ID, "active"))
	})
	cfEffect(t, db, "01J00000000000000000000C01", 1, "cloudflare.tunnel:t1", string(core.EffectMade))
	cfEffect(t, db, "01J00000000000000000000C01", 2, "tailnet.policy:example.com", string(core.EffectUnresolved))
	cfEffect(t, db, "01J00000000000000000000C01", 3, "cloudflare.tunnel:t1", string(core.EffectIntended))
	if got := pending(); len(got) != 0 {
		t.Fatalf("an active hostname, a made effect, an intended one and another module's unresolved one block: %q", got)
	}

	cfWrite(t, db, func(ctx context.Context, tx *sql.Tx) error {
		if err := insertHostname(ctx, tx, cfHostname("b.example.com", tun.ID, "removing_record")); err != nil {
			return err
		}
		return insertHostname(ctx, tx, cfHostname("0.example.com", tun.ID, "adding_rule"))
	})
	cfEffect(t, db, "01J00000000000000000000C02", 1, "cloudflare.dnsrecord:?darkpanel:01J00000000000000000000C02", string(core.EffectUnresolved))
	got := pending()
	want := []string{
		"hostname 0.example.com is in phase adding_rule",
		"hostname b.example.com is in phase removing_record",
		"unresolved effect 01J00000000000000000000C02#1 on cloudflare.dnsrecord:?darkpanel:01J00000000000000000000C02",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("pending = %q, want %q", got, want)
	}
}
