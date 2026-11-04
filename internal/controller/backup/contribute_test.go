package backup

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/darkraise/darkpanel/internal/controller/fleet"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/secret"
	"github.com/darkraise/darkpanel/internal/core/store"
)

// The fleet authority is the TokenIssuer the assembly passes.
var _ TokenIssuer = (*fleet.Authority)(nil)

func TestCbNewKeepsTheConfig(t *testing.T) {
	cfg := Config{KeyFile: "/etc/darkpanel/controller.key", AgentAdvertise: "10.77.0.10:7443"}
	b := New(cfg)
	if b == nil || b.cfg != cfg {
		t.Fatalf("New(%+v) = %+v, want a Backup holding the config", cfg, b)
	}
	if b.running.Load() {
		t.Error("a new Backup reports a backup running")
	}
	if New(Config{}).cfg.Tokens != nil {
		t.Error("an empty Config gained a token issuer")
	}
}

func cbScan(t *testing.T, db *store.DB, query string, dest ...any) {
	t.Helper()
	if err := db.Read().QueryRowContext(context.Background(), query).Scan(dest...); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}

func TestCbFixtureDB(t *testing.T) {
	dir := t.TempDir()
	db := fxDB(t, dir)
	if db.Path() != filepath.Join(dir, "controller.db") {
		t.Errorf("database at %s, want controller.db in %s", db.Path(), dir)
	}
	for _, table := range []string{"core_meta", "core_hosts", "core_plans", "core_changes", "core_locks", "core_composites",
		"core_composite_steps", "core_rollouts", "core_rollout_hosts", "core_listeners", "cloudflare_hostnames", "tailnet_keys"} {
		var n int
		cbScan(t, db, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = '`+table+`'`, &n)
		if n != 1 {
			t.Errorf("the migrated database has no table %s", table)
		}
	}
	var modulesApplied int
	cbScan(t, db, `SELECT COUNT(DISTINCT module) FROM core_migrations WHERE module IN ('core', 'cloudflare', 'tailnet')`, &modulesApplied)
	if modulesApplied != 3 {
		t.Errorf("core_migrations names %d of core, cloudflare and tailnet, want all 3", modulesApplied)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !slices.Contains([]string{"controller.db", "controller.db-wal", "controller.db-shm"}, e.Name()) {
			t.Errorf("fxDB left %s in the database directory", e.Name())
		}
	}
	if other := fxDB(t, ""); other.Path() == db.Path() {
		t.Error("fxDB with no directory reused a path")
	}
}

func TestCbFixtureInsertChange(t *testing.T) {
	db := fxDB(t, "")
	fxInsertChange(t, db, fxChange{ID: "c1", HostID: "h1", Action: "core.test.apply", State: core.StateApplying, Owner: "owner-1"})
	fxInsertChange(t, db, fxChange{ID: "c2", Action: "tailnet.key.create", Class: string(core.ClassIrreversible), State: core.StatePending, ParentID: "c1"})
	want := fxNow.Format("2006-01-02T15:04:05.000000000Z")
	for _, tt := range []struct {
		id, host, action, class, state, owner string
		parent                                sql.NullString
	}{
		{"c1", "h1", "core.test.apply", "compensable", core.StateApplying, "owner-1", sql.NullString{}},
		{"c2", "", "tailnet.key.create", "irreversible", core.StatePending, "", sql.NullString{String: "c1", Valid: true}},
	} {
		var host, action, class, state, owner, actor, input, created, updated, planAction, planHost string
		var version int
		var parent sql.NullString
		cbScan(t, db, `SELECT c.host_id, c.action, c.class, c.state, c.recovery_owner, c.actor, c.input_json, c.version, c.parent_change_id,
				c.created_at, c.updated_at, p.action, p.host_id
			FROM core_changes c JOIN core_plans p ON p.id = c.plan_id WHERE c.id = '`+tt.id+`'`,
			&host, &action, &class, &state, &owner, &actor, &input, &version, &parent, &created, &updated, &planAction, &planHost)
		if host != tt.host || action != tt.action || class != tt.class || state != tt.state || owner != tt.owner || parent != tt.parent {
			t.Errorf("change %s = host %q action %q class %q state %q owner %q parent %v", tt.id, host, action, class, state, owner, parent)
		}
		if actor != "user:fixture" || input != "{}" || version != 1 || created != want || updated != want {
			t.Errorf("change %s defaults = actor %q input %q version %d created %q updated %q; want user:fixture, {}, 1, %s", tt.id, actor, input, version, created, updated, want)
		}
		if planAction != tt.action || planHost != tt.host {
			t.Errorf("change %s has a plan for %q on %q, want %q on %q", tt.id, planAction, planHost, tt.action, tt.host)
		}
	}
}

func TestCbFixtureInsertComposite(t *testing.T) {
	db := fxDB(t, "")
	fxInsertComposite(t, db, "comp1", core.StateApplying, "", []fxStep{
		{ID: "s1", HostID: "h1", State: "applying", ChildPlanID: "p1", ChildChangeID: "cc1"},
		{ID: "s2", Action: "core.x/v1"},
	})
	fxInsertComposite(t, db, "comp2", core.StateUndoing, "unwind", nil)
	for _, tt := range []struct{ id, state, dir string }{{"comp1", core.StateApplying, "forward"}, {"comp2", core.StateUndoing, "unwind"}} {
		var class, state, dir string
		var cursor int
		cbScan(t, db, `SELECT c.class, c.state, k.direction, k.cursor FROM core_changes c JOIN core_composites k ON k.change_id = c.id
			WHERE c.id = '`+tt.id+`'`, &class, &state, &dir, &cursor)
		if class != "composite" || state != tt.state || dir != tt.dir || cursor != 0 {
			t.Errorf("composite %s = class %q state %q direction %q cursor %d", tt.id, class, state, dir, cursor)
		}
	}
	rows, err := db.Read().QueryContext(context.Background(), `SELECT step_id, ordinal, host_id, action, state, child_plan_id, child_change_id
		FROM core_composite_steps WHERE change_id = 'comp1' ORDER BY ordinal`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	type step struct {
		id          string
		ordinal     int
		host        string
		action      string
		state       string
		plan, child sql.NullString
	}
	var got []step
	for rows.Next() {
		var s step
		if err := rows.Scan(&s.id, &s.ordinal, &s.host, &s.action, &s.state, &s.plan, &s.child); err != nil {
			t.Fatal(err)
		}
		got = append(got, s)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	want := []step{
		{"s1", 0, "h1", "test.step/v1", "applying", sql.NullString{String: "p1", Valid: true}, sql.NullString{String: "cc1", Valid: true}},
		{"s2", 1, "", "core.x/v1", "pending", sql.NullString{}, sql.NullString{}},
	}
	if !slices.Equal(got, want) {
		t.Errorf("steps = %+v, want %+v", got, want)
	}
}

func TestCbFixtureRows(t *testing.T) {
	db := fxDB(t, "")
	fxInsertLock(t, db, "docker:web", "c1")
	var owner string
	cbScan(t, db, `SELECT owner FROM core_locks WHERE key = 'docker:web'`, &owner)
	if owner != "c1" {
		t.Errorf("lock owner = %q, want c1", owner)
	}

	fxInsertRollout(t, db, "r1", "running", "hostA", "hostB")
	fxInsertRollout(t, db, "r2", "succeeded")
	var state string
	var outcome sql.NullString
	cbScan(t, db, `SELECT state, outcome FROM core_rollouts WHERE id = 'r1'`, &state, &outcome)
	if state != "running" || outcome.Valid {
		t.Errorf("rollout r1 = %q, outcome %v; want running with no outcome", state, outcome)
	}
	var hosts string
	cbScan(t, db, `SELECT group_concat(host_id || ':' || position || ':' || state, ',') FROM
		(SELECT * FROM core_rollout_hosts WHERE rollout_id = 'r1' ORDER BY position)`, &hosts)
	if hosts != "hostA:0:waiting,hostB:1:waiting" {
		t.Errorf("rollout hosts = %q", hosts)
	}
	var n int
	cbScan(t, db, `SELECT COUNT(*) FROM core_rollout_hosts WHERE rollout_id = 'r2'`, &n)
	if n != 0 {
		t.Errorf("rollout r2 has %d host rows, want none", n)
	}
	draining := fxDB(t, "")
	fxInsertRollout(t, draining, "r3", "draining", "hostA")
	cbScan(t, draining, `SELECT state, outcome FROM core_rollouts WHERE id = 'r3'`, &state, &outcome)
	if state != "draining" || outcome.String != "failed" {
		t.Errorf("rollout r3 = %q, outcome %v; want draining with outcome failed", state, outcome)
	}

	fxInsertListener(t, db, "l1", "open")
	fxInsertListener(t, db, "l2", "closed")
	cbScan(t, db, `SELECT group_concat(id || ':' || state, ',') FROM (SELECT * FROM core_listeners ORDER BY id)`, &hosts)
	if hosts != "l1:open,l2:closed" {
		t.Errorf("listeners = %q", hosts)
	}

	fxInsertHost(t, db, "h-local", "enrolled", true)
	fxInsertHost(t, db, "h-agent", "pending", false)
	cbScan(t, db, `SELECT group_concat(id || ':' || state || ':' || is_local, ',') FROM (SELECT * FROM core_hosts ORDER BY id)`, &hosts)
	if hosts != "h-agent:pending:0,h-local:enrolled:1" {
		t.Errorf("hosts = %q", hosts)
	}
}

func TestCbFixtureKey(t *testing.T) {
	dir := t.TempDir()
	path, raw, sealer := fxKey(t, dir)
	if path != filepath.Join(dir, "controller.key") {
		t.Errorf("key at %s, want controller.key in %s", path, dir)
	}
	if st, err := os.Stat(path); err != nil || st.Mode().Perm() != 0o600 {
		t.Errorf("key file: %v, %v; want mode 0600", st, err)
	}
	if onDisk, err := os.ReadFile(path); err != nil || !bytes.Equal(onDisk, raw) { //nolint:gosec // G304: a path under t.TempDir()
		t.Errorf("raw differs from the file's bytes: %v", err)
	}
	if !bytes.HasPrefix(raw, []byte(secret.KeyPrefix)) || !bytes.HasSuffix(raw, []byte("\n")) {
		t.Errorf("raw = %q, want a key file line", raw)
	}
	key, err := secret.LoadOrCreateKey(path)
	if err != nil {
		t.Fatal(err)
	}
	same, err := secret.NewSealer(key)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := sealer.Seal("ad", []byte("plain"))
	if err != nil {
		t.Fatal(err)
	}
	if got, err := same.Open("ad", sealed); err != nil || string(got) != "plain" {
		t.Errorf("a sealer over the key file opens the fixture sealer's value as %q, %v", got, err)
	}
	if _, raw2, _ := fxKey(t, t.TempDir()); bytes.Equal(raw, raw2) {
		t.Error("two fixture keys are equal")
	}
}
