package firewall

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"net/netip"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/modules/firewall/dockerfw"
)

var dsTables = []string{"firewall_settings", "firewall_container_rules", "firewall_docker_cleanups", "firewall_docker_foreign"}

// dsDB opens a temporary agent database with the core's and every
// firewall migration applied, and fails unless the Docker tables exist.
func dsDB(t *testing.T) *store.DB {
	t.Helper()
	db := fwTestDB(t)
	for _, table := range dsTables {
		var name string
		err := db.Read().QueryRow("SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?", table).Scan(&name)
		if err != nil {
			t.Fatalf("table %s is missing after the firewall migrations: %v", table, err)
		}
	}
	return db
}

var dsT0 = time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)

func dsWrite(t *testing.T, db *store.DB, fn func(tx *sql.Tx) error) {
	t.Helper()
	if err := db.Write(context.Background(), fn); err != nil {
		t.Fatal(err)
	}
}

func dsExec(t *testing.T, db *store.DB, query string, args ...any) error {
	t.Helper()
	return db.Write(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.Exec(query, args...)
		return err
	})
}

func dsRule(id, selector, host string, port uint16, proto string, kind dockerfw.Kind, from string) dockerfw.ContainerRule {
	r := dockerfw.ContainerRule{
		ID:      id,
		Binding: dockerfw.Binding{Selector: selector, HostPort: port, Proto: proto},
		Kind:    kind,
	}
	if host != "" {
		r.Binding.HostAddr = netip.MustParseAddr(host)
	}
	if from != "" {
		r.From = netip.MustParsePrefix(from)
	}
	return r
}

func dsAllRules(t *testing.T, db *store.DB) []dockerfw.ContainerRule {
	t.Helper()
	got, err := listContainerRules(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func dsOwners(t *testing.T, db *store.DB) []string {
	t.Helper()
	got, err := pendingCleanups(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestDsMigrationOnlyAddsTables(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "agent.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	coreSet, err := store.CoreMigrations(store.SideAgent)
	if err != nil {
		t.Fatal(err)
	}
	old := fstest.MapFS{}
	for _, name := range []string{"0001_firewall.sql", "0002_firewall_restore_kind.sql"} {
		b, err := fs.ReadFile(agentMigrations(), name)
		if err != nil {
			t.Fatal(err)
		}
		old[name] = &fstest.MapFile{Data: b}
	}
	opts := store.MigrateOptions{Side: store.SideAgent, BinaryVersion: "test", BackupRoot: filepath.Join(t.TempDir(), "backups"), Keep: 5, Now: time.Now()}
	if res, err := store.Migrate(ctx, db, opts, coreSet, []store.MigrationSet{{Module: ID, FS: old}}); err != nil || len(res.Failed) > 0 {
		t.Fatalf("migrate 0001-0002: %v %v", err, res.Failed)
	}
	if err := dsExec(t, db, `INSERT INTO firewall_snapshots (id, created_at, actor, entries, state, hashes) VALUES ('s1', 't', 'a', '[]', '{}', '{}')`); err != nil {
		t.Fatal(err)
	}
	opts.BinaryVersion = "test2"
	if res, err := store.Migrate(ctx, db, opts, coreSet, []store.MigrationSet{{Module: ID, FS: agentMigrations()}}); err != nil || len(res.Failed) > 0 {
		t.Fatalf("migrate 0003: %v %v", err, res.Failed)
	}
	var snapshots int
	if err := db.Read().QueryRow("SELECT count(*) FROM firewall_snapshots WHERE id = 's1'").Scan(&snapshots); err != nil || snapshots != 1 {
		t.Errorf("the snapshot written before 0003: count %d, err %v, want 1", snapshots, err)
	}
	for _, table := range dsTables {
		var strict int
		err := db.Read().QueryRow("SELECT strict FROM pragma_table_list WHERE name = ?", table).Scan(&strict)
		if err != nil || strict != 1 {
			t.Errorf("table %s: strict %d, err %v, want a STRICT table", table, strict, err)
		}
	}
}

func TestDsMigrationConstraints(t *testing.T) {
	db := dsDB(t)
	const insert = "INSERT INTO firewall_container_rules (id, selector, host_addr, host_port, proto, kind, source, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, 't')"
	if err := dsExec(t, db, insert, "r0", "web", "", 8080, "tcp", "allow", ""); err != nil {
		t.Fatalf("a valid row: %v", err)
	}
	for _, c := range []struct {
		name  string
		query string
		args  []any
	}{
		{"settings row other than 1", "INSERT INTO firewall_settings (id, docker, settings) VALUES (2, 0, '{}')", nil},
		{"docker flag 2", "INSERT INTO firewall_settings (id, docker, settings) VALUES (1, 2, '{}')", nil},
		{"port 0", insert, []any{"r1", "web", "", 0, "tcp", "allow", ""}},
		{"port 65536", insert, []any{"r1", "web", "", 65536, "tcp", "allow", ""}},
		{"proto icmp", insert, []any{"r1", "web", "", 8080, "icmp", "allow", ""}},
		{"kind maybe", insert, []any{"r1", "web", "", 8080, "tcp", "maybe", ""}},
		{"same tuple under another id", insert, []any{"r1", "web", "", 8080, "tcp", "allow", ""}},
		{"same id", insert, []any{"r0", "other", "", 8080, "tcp", "allow", ""}},
	} {
		if err := dsExec(t, db, c.query, c.args...); err == nil {
			t.Errorf("%s: the insert succeeded, want a constraint error", c.name)
		}
	}
	if err := dsExec(t, db, insert, "r2", "web", "", 8080, "tcp", "deny", "203.0.113.0/24"); err != nil {
		t.Errorf("the same binding with another kind and source: %v", err)
	}
}

func TestDsDockerConfig(t *testing.T) {
	ctx := context.Background()
	db := dsDB(t)
	got, err := readDockerConfig(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if want := (dockerConfig{Settings: dockerfw.DefaultSettings()}); !reflect.DeepEqual(got, want) {
		t.Errorf("no row: %+v, want the integration off with the default settings %+v", got, want)
	}

	boom := errors.New("boom")
	err = db.Write(ctx, func(tx *sql.Tx) error {
		if err := writeDockerConfig(ctx, tx, dockerConfig{Enabled: true, Settings: dockerfw.DefaultSettings()}); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("Write = %v, want the rollback error", err)
	}
	if got, _ := readDockerConfig(ctx, db); got.Enabled {
		t.Error("a write in a rolled-back transaction was stored")
	}

	first := dockerConfig{Enabled: true, Settings: dockerfw.Settings{
		PrivateSources: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("fc00::/7")},
		ForwardExemptions: []dockerfw.Exemption{{
			InInterface: "eth0", OrigDestination: netip.MustParseAddr("192.0.2.10"), OrigPort: 2222,
			Destination: netip.MustParseAddr("10.9.0.5"), Proto: "tcp", Port: 22,
		}},
	}}
	second := dockerConfig{Enabled: false, Settings: dockerfw.Settings{
		PrivateSources:    []netip.Prefix{netip.MustParsePrefix("192.168.0.0/16")},
		ForwardExemptions: []dockerfw.Exemption{},
	}}
	for _, want := range []dockerConfig{first, second} {
		dsWrite(t, db, func(tx *sql.Tx) error { return writeDockerConfig(ctx, tx, want) })
		got, err := readDockerConfig(ctx, db)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("read back %+v, want %+v", got, want)
		}
	}
	var rows int
	if err := db.Read().QueryRow("SELECT count(*) FROM firewall_settings").Scan(&rows); err != nil || rows != 1 {
		t.Errorf("firewall_settings holds %d rows (err %v), want the one row rewritten", rows, err)
	}

	if err := dsExec(t, db, "UPDATE firewall_settings SET settings = 'not json'"); err != nil {
		t.Fatal(err)
	}
	if _, err := readDockerConfig(ctx, db); err == nil || !strings.HasPrefix(err.Error(), "firewall:") {
		t.Errorf("a corrupt settings column: %v, want a firewall: error", err)
	}
}

func TestDsContainerRulesRoundTrip(t *testing.T) {
	ctx := context.Background()
	db := dsDB(t)
	rules := []dockerfw.ContainerRule{
		dsRule("01J00000000000000000000003", "shop/web", "192.0.2.10", 443, "tcp", dockerfw.Deny, "203.0.113.0/24"),
		dsRule("01J00000000000000000000001", "web", "", 8080, "tcp", dockerfw.Allow, ""),
		dsRule("01J00000000000000000000002", "dns", "2001:db8::1", 53, "udp", dockerfw.Allow, "198.51.100.0/24"),
	}
	for i, r := range rules {
		at := dsT0.Add(time.Duration(i) * time.Minute)
		dsWrite(t, db, func(tx *sql.Tx) error { return insertContainerRule(ctx, tx, r, at) })
	}
	want := []dockerfw.ContainerRule{rules[1], rules[2], rules[0]}
	if got := dsAllRules(t, db); !reflect.DeepEqual(got, want) {
		t.Errorf("listContainerRules =\n%+v\nwant, ordered by id,\n%+v", got, want)
	}
	for _, r := range rules {
		got, ok, err := containerRule(ctx, db, r.ID)
		if err != nil || !ok || !reflect.DeepEqual(got, r) {
			t.Errorf("containerRule(%s) = %+v, %v, %v, want %+v", r.ID, got, ok, err, r)
		}
	}
	if _, ok, err := containerRule(ctx, db, "missing"); ok || err != nil {
		t.Errorf("containerRule(missing) = %v, %v, want not found without an error", ok, err)
	}

	var host, source, created string
	err := db.Read().QueryRow("SELECT host_addr, source, created_at FROM firewall_container_rules WHERE id = ?", rules[1].ID).Scan(&host, &source, &created)
	if err != nil || host != "" || source != "" || created != formatSnapshotTime(dsT0.Add(time.Minute)) {
		t.Errorf("stored columns of an any rule = %q %q %q (err %v), want empty, empty and the creation time", host, source, created, err)
	}
	err = db.Read().QueryRow("SELECT host_addr, source FROM firewall_container_rules WHERE id = ?", rules[0].ID).Scan(&host, &source)
	if err != nil || host != "192.0.2.10" || source != "203.0.113.0/24" {
		t.Errorf("stored columns of a deny rule = %q %q (err %v), want 192.0.2.10 and 203.0.113.0/24", host, source, err)
	}
}

func TestDsContainerRuleRefusals(t *testing.T) {
	ctx := context.Background()
	db := dsDB(t)
	base := dsRule("01J00000000000000000000001", "web", "", 8080, "tcp", dockerfw.Allow, "")
	dsWrite(t, db, func(tx *sql.Tx) error { return insertContainerRule(ctx, tx, base, dsT0) })
	sameTuple := base
	sameTuple.ID = "01J00000000000000000000002"
	sameID := dsRule(base.ID, "other", "", 9090, "tcp", dockerfw.Allow, "")
	for name, r := range map[string]dockerfw.ContainerRule{"the same binding, kind and source": sameTuple, "the same id": sameID} {
		err := db.Write(ctx, func(tx *sql.Tx) error { return insertContainerRule(ctx, tx, r, dsT0) })
		if err == nil || !strings.HasPrefix(err.Error(), "firewall:") {
			t.Errorf("insert of %s: %v, want a firewall: error", name, err)
		}
	}
	if got := dsAllRules(t, db); !reflect.DeepEqual(got, []dockerfw.ContainerRule{base}) {
		t.Errorf("after the refused inserts: %+v, want only the first rule", got)
	}

	if err := dsExec(t, db, "UPDATE firewall_container_rules SET host_addr = 'nonsense'"); err != nil {
		t.Fatal(err)
	}
	if _, err := listContainerRules(ctx, db); err == nil || !strings.HasPrefix(err.Error(), "firewall:") {
		t.Errorf("list with a corrupt host address: %v, want a firewall: error", err)
	}
	if _, _, err := containerRule(ctx, db, base.ID); err == nil || !strings.HasPrefix(err.Error(), "firewall:") {
		t.Errorf("containerRule with a corrupt host address: %v, want a firewall: error", err)
	}
}

func TestDsDeleteContainerRule(t *testing.T) {
	ctx := context.Background()
	db := dsDB(t)
	a := dsRule("01J00000000000000000000001", "web", "", 8080, "tcp", dockerfw.Allow, "")
	b := dsRule("01J00000000000000000000002", "web", "", 8080, "tcp", dockerfw.Deny, "203.0.113.0/24")
	dsWrite(t, db, func(tx *sql.Tx) error {
		if err := insertContainerRule(ctx, tx, a, dsT0); err != nil {
			return err
		}
		return insertContainerRule(ctx, tx, b, dsT0)
	})
	dsWrite(t, db, func(tx *sql.Tx) error { return deleteContainerRule(ctx, tx, a.ID) })
	if got := dsAllRules(t, db); !reflect.DeepEqual(got, []dockerfw.ContainerRule{b}) {
		t.Errorf("after deleting %s: %+v, want only %s", a.ID, got, b.ID)
	}
	dsWrite(t, db, func(tx *sql.Tx) error { return deleteContainerRule(ctx, tx, a.ID) })
	if got := dsAllRules(t, db); !reflect.DeepEqual(got, []dockerfw.ContainerRule{b}) {
		t.Errorf("after deleting an absent id: %+v, want %s untouched", got, b.ID)
	}
}

func TestDsReplaceContainerRules(t *testing.T) {
	ctx := context.Background()
	db := dsDB(t)
	old := []dockerfw.ContainerRule{
		dsRule("01J00000000000000000000001", "web", "", 8080, "tcp", dockerfw.Allow, ""),
		dsRule("01J00000000000000000000002", "dns", "", 53, "udp", dockerfw.Allow, ""),
	}
	next := []dockerfw.ContainerRule{
		dsRule("01J00000000000000000000009", "web", "", 8080, "tcp", dockerfw.Allow, ""),
		dsRule("01J00000000000000000000008", "api", "192.0.2.10", 443, "tcp", dockerfw.Deny, "203.0.113.0/24"),
	}
	dsWrite(t, db, func(tx *sql.Tx) error { return replaceContainerRules(ctx, tx, old, dsT0) })
	if got := dsAllRules(t, db); !reflect.DeepEqual(got, old) {
		t.Fatalf("replace into an empty table: %+v, want %+v", got, old)
	}

	dsWrite(t, db, func(tx *sql.Tx) error { return replaceContainerRules(ctx, tx, next, dsT0.Add(time.Hour)) })
	if got, want := dsAllRules(t, db), []dockerfw.ContainerRule{next[1], next[0]}; !reflect.DeepEqual(got, want) {
		t.Errorf("replace: %+v, want %+v", got, want)
	}
	var created string
	if err := db.Read().QueryRow("SELECT created_at FROM firewall_container_rules WHERE id = ?", next[0].ID).Scan(&created); err != nil || created != formatSnapshotTime(dsT0.Add(time.Hour)) {
		t.Errorf("created_at of a replaced row = %q (err %v), want the time passed to replace", created, err)
	}

	twice := []dockerfw.ContainerRule{old[0], old[0]}
	if err := db.Write(ctx, func(tx *sql.Tx) error { return replaceContainerRules(ctx, tx, twice, dsT0) }); err == nil {
		t.Error("replace with a duplicated row succeeded")
	}
	if got, want := dsAllRules(t, db), []dockerfw.ContainerRule{next[1], next[0]}; !reflect.DeepEqual(got, want) {
		t.Errorf("after a failed replace: %+v, want the previous rows %+v", got, want)
	}

	dsWrite(t, db, func(tx *sql.Tx) error { return replaceContainerRules(ctx, tx, nil, dsT0) })
	if got := dsAllRules(t, db); len(got) != 0 {
		t.Errorf("replace with no rows left %+v", got)
	}
}

func TestDsCleanupIntents(t *testing.T) {
	ctx := context.Background()
	db := dsDB(t)
	if got := dsOwners(t, db); len(got) != 0 {
		t.Fatalf("pendingCleanups on a new database = %v, want none", got)
	}
	if err := beginCleanup(ctx, db, "uninstall", dsT0); err != nil {
		t.Fatal(err)
	}
	if err := beginCleanup(ctx, db, "01J00000000000000000000001", dsT0.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if got, want := dsOwners(t, db), []string{"uninstall", "01J00000000000000000000001"}; !reflect.DeepEqual(got, want) {
		t.Errorf("pendingCleanups = %v, want %v, oldest intent first", got, want)
	}

	if err := beginCleanup(ctx, db, "uninstall", dsT0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	var started string
	if err := db.Read().QueryRow("SELECT started_at FROM firewall_docker_cleanups WHERE owner = 'uninstall'").Scan(&started); err != nil || started != formatSnapshotTime(dsT0) {
		t.Errorf("started_at after recording an owner twice = %q (err %v), want the first time", started, err)
	}
	if got := dsOwners(t, db); len(got) != 2 {
		t.Errorf("recording an owner twice left %v, want two intents", got)
	}

	if err := endCleanup(ctx, db, "uninstall"); err != nil {
		t.Fatal(err)
	}
	if err := endCleanup(ctx, db, "uninstall"); err != nil {
		t.Errorf("endCleanup of an absent owner: %v", err)
	}
	if got, want := dsOwners(t, db), []string{"01J00000000000000000000001"}; !reflect.DeepEqual(got, want) {
		t.Errorf("pendingCleanups after ending one = %v, want %v", got, want)
	}
	if err := endCleanup(ctx, db, "01J00000000000000000000001"); err != nil {
		t.Fatal(err)
	}
	if got := dsOwners(t, db); len(got) != 0 {
		t.Errorf("pendingCleanups after ending both = %v, want none", got)
	}
}
