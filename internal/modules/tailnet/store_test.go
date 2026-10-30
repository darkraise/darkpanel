package tailnet

import (
	"context"
	"database/sql"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/cloud"
	"github.com/darkraise/darkpanel/internal/core/store"
)

// tnDB opens a temporary controller database with the core's and the
// tailnet's migrations applied.
func tnDB(t *testing.T) *store.DB {
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
		t.Fatalf("tailnet migrations failed: %v", res.Failed[0].Err)
	}
	return db
}

func tnWrite(t *testing.T, db *store.DB, fn func(tx *sql.Tx) error) error {
	t.Helper()
	return db.Write(context.Background(), fn)
}

func tnExec(t *testing.T, db *store.DB, query string, args ...any) error {
	t.Helper()
	return tnWrite(t, db, func(tx *sql.Tx) error {
		_, err := tx.Exec(query, args...)
		return err
	})
}

func tnAccount(id string) account {
	return account{
		ID: id, Tailnet: "example.com", ClientID: "k123", SecretCT: []byte{0x01, 0x02, 0x03},
		TagsAllowed:  []string{"tag:server", "tag:ci"},
		Capabilities: []cloud.Capability{{Name: "devices:core", State: "ok"}, {Name: "policy_file", State: "missing", Detail: "cloud.permission_missing"}},
		ConnectedAt:  time.Date(2026, 10, 30, 14, 0, 0, 123456789, time.FixedZone("x", 2*3600)),
	}
}

func tnKey(description, state string, requested time.Time) keyRow {
	return keyRow{
		Description: description, Kind: "change", OwnerID: "01JD0000000000000000000001", State: state,
		RequestedAt: requested, UpdatedAt: requested, ExpirySeconds: 86400, Tags: []string{"tag:server"},
	}
}

// tnEffect inserts an effect of change changeID, creating the change and its
// plan when they are not there yet.
func tnEffect(t *testing.T, db *store.DB, changeID string, seq int, resource, state string) {
	t.Helper()
	at := "2026-10-30T12:00:00.000000000Z"
	err := tnWrite(t, db, func(tx *sql.Tx) error {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO core_plans (id, host_id, action, version, actor, input_hash, state_hash,
			diff_json, guards_json, confirmation, created_at, expires_at)
			VALUES (?, '', 'tailnet.policy.set', 1, 'user:u1', '', '', '[]', '[]', '', ?, ?)`, "p-"+changeID, at, at); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT OR IGNORE INTO core_changes (id, plan_id, host_id, action, version, class, compensated_by, actor,
			state, input_json, created_at, updated_at)
			VALUES (?, ?, '', 'tailnet.policy.set', 1, 'compensable', '', 'user:u1', 'applying', '{}', ?, ?)`, changeID, "p-"+changeID, at, at); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO core_effects (change_id, seq, kind, resource, local, inverse_local, before_json, state, created_at, updated_at)
			VALUES (?, ?, 'modify', ?, 0, 0, '{}', ?, ?, ?)`, changeID, seq, resource, state, at, at)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestTnMigrationCreatesStrictTables(t *testing.T) {
	db := tnDB(t)
	columns := func(table string) []string {
		rows, err := db.Read().Query(`SELECT name FROM pragma_table_info(?) ORDER BY cid`, table)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = rows.Close() }()
		var out []string
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				t.Fatal(err)
			}
			out = append(out, name)
		}
		return out
	}
	wantAccounts := []string{"id", "tailnet", "client_id", "client_secret_ct", "tags_allowed_json", "capabilities_json", "connected_at"}
	if got := columns("tailnet_accounts"); !reflect.DeepEqual(got, wantAccounts) {
		t.Errorf("tailnet_accounts columns = %v, want %v", got, wantAccounts)
	}
	wantKeys := []string{"description", "kind", "owner_id", "requested_at", "expiry_seconds", "tags_json", "key_id", "state", "updated_at"}
	if got := columns("tailnet_keys"); !reflect.DeepEqual(got, wantKeys) {
		t.Errorf("tailnet_keys columns = %v, want %v", got, wantKeys)
	}
	for _, table := range []string{"tailnet_accounts", "tailnet_keys"} {
		var strict int
		if err := db.Read().QueryRow(`SELECT strict FROM pragma_table_list WHERE name = ?`, table).Scan(&strict); err != nil || strict != 1 {
			t.Errorf("%s: strict = %d, %v, want 1", table, strict, err)
		}
	}
}

func TestTnMigrationConstraints(t *testing.T) {
	db := tnDB(t)
	insertKey := `INSERT INTO tailnet_keys (description, kind, owner_id, requested_at, expiry_seconds, tags_json, state, updated_at)
		VALUES (?, ?, 'o1', 't', 60, '[]', ?, 't')`
	if err := tnExec(t, db, insertKey, "darkpanel-c1", "change", "requested"); err != nil {
		t.Fatalf("a valid key row: %v", err)
	}
	var keyID string
	if err := db.Read().QueryRow(`SELECT key_id FROM tailnet_keys`).Scan(&keyID); err != nil || keyID != "" {
		t.Errorf("key_id default = %q, %v, want empty", keyID, err)
	}
	for name, c := range map[string][]any{
		"an unknown kind":            {"darkpanel-c2", "other", "requested"},
		"an unknown state":           {"darkpanel-c2", "change", "gone"},
		"a description used already": {"darkpanel-c1", "enroll", "live"},
	} {
		if err := tnExec(t, db, insertKey, c...); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	for _, state := range []string{"requested", "live", "uncertain", "used", "retired", "deleted"} {
		if err := tnExec(t, db, `UPDATE tailnet_keys SET state = ?`, state); err != nil {
			t.Errorf("state %q refused: %v", state, err)
		}
	}

	insertAccount := `INSERT INTO tailnet_accounts (id, tailnet, client_id, client_secret_ct, tags_allowed_json, capabilities_json, connected_at)
		VALUES (?, 'example.com', 'k', x'00', '[]', '[]', 't')`
	if err := tnExec(t, db, insertAccount, "c1"); err != nil {
		t.Fatalf("a valid account row: %v", err)
	}
	if err := tnExec(t, db, insertAccount, "c2"); err == nil {
		t.Error("a second account row was accepted")
	}
}

func TestTnAccountRoundTrip(t *testing.T) {
	db := tnDB(t)
	ctx := context.Background()
	if _, ok, err := loadAccount(ctx, db.Read()); err != nil || ok {
		t.Fatalf("loadAccount on an empty table = ok %v, err %v", ok, err)
	}
	want := tnAccount("01JD0000000000000000000001")
	if err := tnWrite(t, db, func(tx *sql.Tx) error { return insertAccount(ctx, tx, want) }); err != nil {
		t.Fatal(err)
	}
	got, ok, err := loadAccount(ctx, db.Read())
	if err != nil || !ok {
		t.Fatalf("loadAccount = ok %v, err %v", ok, err)
	}
	if !got.ConnectedAt.Equal(want.ConnectedAt) {
		t.Errorf("ConnectedAt = %v, want %v", got.ConnectedAt, want.ConnectedAt)
	}
	got.ConnectedAt, want.ConnectedAt = time.Time{}, time.Time{}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("loaded %+v, want %+v", got, want)
	}

	var raw string
	if err := db.Read().QueryRow(`SELECT connected_at FROM tailnet_accounts`).Scan(&raw); err != nil || raw != "2026-10-30T12:00:00.123456789Z" {
		t.Errorf("stored connected_at = %q, %v, want fixed-width UTC text", raw, err)
	}

	err = tnWrite(t, db, func(tx *sql.Tx) error {
		if err := insertAccount(ctx, tx, tnAccount("01JD0000000000000000000002")); err == nil {
			t.Error("a second account was inserted")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if again, _, _ := loadAccount(ctx, db.Read()); again.ID != "01JD0000000000000000000001" {
		t.Errorf("the account is %q after a refused insert, want the first", again.ID)
	}
}

func TestTnAccountListsLoadNonNil(t *testing.T) {
	db := tnDB(t)
	ctx := context.Background()
	a := tnAccount("c1")
	a.TagsAllowed, a.Capabilities = nil, nil
	if err := tnWrite(t, db, func(tx *sql.Tx) error { return insertAccount(ctx, tx, a) }); err != nil {
		t.Fatal(err)
	}
	got, _, err := loadAccount(ctx, db.Read())
	if err != nil {
		t.Fatal(err)
	}
	if got.TagsAllowed == nil || len(got.TagsAllowed) != 0 || got.Capabilities == nil || len(got.Capabilities) != 0 {
		t.Errorf("lists = %#v and %#v, want empty and not nil, so they encode as []", got.TagsAllowed, got.Capabilities)
	}
}

func TestTnLoadAccountReadsThroughATransaction(t *testing.T) {
	db := tnDB(t)
	ctx := context.Background()
	err := tnWrite(t, db, func(tx *sql.Tx) error {
		if err := insertAccount(ctx, tx, tnAccount("c1")); err != nil {
			return err
		}
		got, ok, err := loadAccount(ctx, tx)
		if err != nil || !ok || got.ID != "c1" {
			t.Errorf("loadAccount in the transaction = %+v, ok %v, err %v", got, ok, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestTnDeleteAccount(t *testing.T) {
	db := tnDB(t)
	ctx := context.Background()
	if err := tnWrite(t, db, func(tx *sql.Tx) error { return insertAccount(ctx, tx, tnAccount("c1")) }); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"other", "c1", "c1"} {
		if err := tnWrite(t, db, func(tx *sql.Tx) error { return deleteAccount(ctx, tx, id) }); err != nil {
			t.Fatalf("deleteAccount(%q): %v", id, err)
		}
		_, ok, err := loadAccount(ctx, db.Read())
		if err != nil {
			t.Fatal(err)
		}
		if want := id == "other"; ok != want {
			t.Errorf("after deleteAccount(%q) the account present = %v, want %v", id, ok, want)
		}
	}
}

func TestTnKeyRoundTripAndOrder(t *testing.T) {
	db := tnDB(t)
	ctx := context.Background()
	if _, ok, err := loadKey(ctx, db.Read(), "darkpanel-none"); err != nil || ok {
		t.Fatalf("loadKey of an absent key = ok %v, err %v", ok, err)
	}
	late := tnKey("darkpanel-c3", "live", tnT0.Add(2*time.Minute))
	late.KeyID = "kid-3"
	rows := []keyRow{late, tnKey("darkpanel-c2", "requested", tnT0), tnKey("darkpanel-c1", "uncertain", tnT0)}
	rows[2].Kind, rows[2].Tags = "enroll", nil
	for _, r := range rows {
		if err := tnWrite(t, db, func(tx *sql.Tx) error { return insertKey(ctx, tx, r) }); err != nil {
			t.Fatal(err)
		}
	}
	got, err := listKeys(ctx, db.Read())
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, k := range got {
		order = append(order, k.Description)
	}
	if want := []string{"darkpanel-c1", "darkpanel-c2", "darkpanel-c3"}; !reflect.DeepEqual(order, want) {
		t.Errorf("listKeys order = %v, want by requested_at then description %v", order, want)
	}

	one, ok, err := loadKey(ctx, db.Read(), "darkpanel-c3")
	if err != nil || !ok {
		t.Fatalf("loadKey = ok %v, err %v", ok, err)
	}
	if !one.RequestedAt.Equal(late.RequestedAt) || !one.UpdatedAt.Equal(late.UpdatedAt) {
		t.Errorf("times = %v, %v, want %v", one.RequestedAt, one.UpdatedAt, late.RequestedAt)
	}
	one.RequestedAt, one.UpdatedAt, late.RequestedAt, late.UpdatedAt = time.Time{}, time.Time{}, time.Time{}, time.Time{}
	if !reflect.DeepEqual(one, late) {
		t.Errorf("loaded %+v, want %+v", one, late)
	}
	if empty, _, _ := loadKey(ctx, db.Read(), "darkpanel-c1"); empty.Tags == nil || len(empty.Tags) != 0 || empty.Kind != "enroll" {
		t.Errorf("a key without tags loads Tags %#v, kind %q, want an empty list and enroll", empty.Tags, empty.Kind)
	}

	for name, r := range map[string]keyRow{
		"a description used already": tnKey("darkpanel-c1", "live", tnT0),
		"an unknown state":           tnKey("darkpanel-c9", "gone", tnT0),
	} {
		if err := tnWrite(t, db, func(tx *sql.Tx) error { return insertKey(ctx, tx, r) }); err == nil {
			t.Errorf("%s was inserted", name)
		}
	}
}

func TestTnSetKeyState(t *testing.T) {
	db := tnDB(t)
	ctx := context.Background()
	if err := tnWrite(t, db, func(tx *sql.Tx) error { return insertKey(ctx, tx, tnKey("darkpanel-c1", "requested", tnT0)) }); err != nil {
		t.Fatal(err)
	}
	set := func(state, keyID string, at time.Time) error {
		return tnWrite(t, db, func(tx *sql.Tx) error { return setKeyState(ctx, tx, "darkpanel-c1", state, keyID, at) })
	}
	load := func() keyRow {
		k, ok, err := loadKey(ctx, db.Read(), "darkpanel-c1")
		if err != nil || !ok {
			t.Fatalf("loadKey = ok %v, err %v", ok, err)
		}
		return k
	}

	at := tnT0.Add(time.Minute)
	if err := set("live", "kid-1", at); err != nil {
		t.Fatal(err)
	}
	if k := load(); k.State != "live" || k.KeyID != "kid-1" || !k.UpdatedAt.Equal(at) || !k.RequestedAt.Equal(tnT0) {
		t.Errorf("after live: %+v", k)
	}
	at = at.Add(time.Hour)
	if err := set("used", "", at); err != nil {
		t.Fatal(err)
	}
	if k := load(); k.State != "used" || k.KeyID != "kid-1" || !k.UpdatedAt.Equal(at) {
		t.Errorf("an empty key id must keep the stored one: %+v", k)
	}

	before := load()
	if err := set("gone", "kid-2", at.Add(time.Hour)); err == nil {
		t.Error("an unknown state was set")
	}
	if got := load(); !reflect.DeepEqual(got, before) {
		t.Errorf("a refused state change wrote: %+v, was %+v", got, before)
	}
	err := tnWrite(t, db, func(tx *sql.Tx) error { return setKeyState(ctx, tx, "darkpanel-none", "live", "kid", at) })
	if err == nil {
		t.Error("a key that does not exist was updated")
	}
}

func TestTnCleanupPending(t *testing.T) {
	db := tnDB(t)
	ctx := context.Background()
	pending := func() []string {
		var got []string
		err := tnWrite(t, db, func(tx *sql.Tx) error {
			var err error
			got, err = cleanupPending(ctx, tx)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	if got := pending(); len(got) != 0 {
		t.Fatalf("cleanupPending on an empty database = %v", got)
	}

	for i, state := range []string{"requested", "live", "uncertain", "used", "retired", "deleted"} {
		k := tnKey("darkpanel-k"+state, state, tnT0.Add(time.Duration(i)*time.Minute))
		if err := tnWrite(t, db, func(tx *sql.Tx) error { return insertKey(ctx, tx, k) }); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{
		"key darkpanel-krequested is requested",
		"key darkpanel-kuncertain is uncertain",
		"key darkpanel-kretired is retired",
	}
	if got := pending(); !reflect.DeepEqual(got, want) {
		t.Errorf("keys: cleanupPending = %v, want %v", got, want)
	}

	tnEffect(t, db, "c1", 1, "tailnet.policy:example.com", "unresolved")
	tnEffect(t, db, "c1", 2, "tailnet.key:?darkpanel-c1", "made")
	tnEffect(t, db, "c2", 1, "cloudflare.tunnel:t1", "unresolved")
	tnEffect(t, db, "c2", 2, "tailnetx.policy:example.com", "unresolved")
	tnEffect(t, db, "c3", 1, "tailnet.device:n1", "unresolved")
	want = append(want, "effect c1#1 tailnet.policy:example.com is unresolved", "effect c3#1 tailnet.device:n1 is unresolved")
	if got := pending(); !reflect.DeepEqual(got, want) {
		t.Errorf("effects: cleanupPending = %v, want %v", got, want)
	}
}

func TestTnCleanupPendingSeesTheTransactionsOwnWrites(t *testing.T) {
	db := tnDB(t)
	ctx := context.Background()
	err := tnWrite(t, db, func(tx *sql.Tx) error {
		if err := insertKey(ctx, tx, tnKey("darkpanel-c1", "requested", tnT0)); err != nil {
			return err
		}
		got, err := cleanupPending(ctx, tx)
		if want := []string{"key darkpanel-c1 is requested"}; err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("cleanupPending = %v, %v, want %v", got, err, want)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
