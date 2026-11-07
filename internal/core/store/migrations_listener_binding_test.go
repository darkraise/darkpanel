package store

import (
	"context"
	"testing"
	"time"
)

func TestV1r18ListenerPublicBindingMigration(t *testing.T) {
	ctx := context.Background()
	db := openTemp(t)
	if _, err := Migrate(ctx, db, migrateOpts(t, "w10a", time.Now()), coreSetUpTo(t, SideController, 15), nil); err != nil {
		t.Fatal(err)
	}
	const at = "2026-11-06T00:00:00.000000000Z"
	const insert = `INSERT INTO core_listeners (id, address, port, allowed_peer, client_header, public_host, change_id, state,
		created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, 'open', ?, ?)`
	for _, row := range [][]any{
		{"L1", "172.31.255.1", 9443, "172.31.255.2", "X-Darkpanel-Client", "panel.example.com", "C1", at, at},
		{"L2", "172.31.255.1", 9444, "172.31.255.2", "Cf-Connecting-IP", "panel.example.net", "C2", at, at},
	} {
		if _, err := db.Writer().ExecContext(ctx, insert, row...); err != nil {
			t.Fatalf("listener %v: %v", row[0], err)
		}
	}
	set, err := CoreMigrations(SideController)
	if err != nil {
		t.Fatal(err)
	}
	res, err := Migrate(ctx, db, migrateOpts(t, "w10b", time.Now()), set, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Applied) == 0 || res.Applied[0].Number != 16 || res.Applied[0].Name != "listener_public_binding" {
		t.Fatalf("applied %+v, want 0016_listener_public_binding first", res.Applied)
	}
	const rows = `SELECT json_array(id, public_binding) FROM core_listeners ORDER BY id`
	got := surfacesRows(t, db, rows)
	if want := []string{`["L1","0.0.0.0:443"]`, `["L2",""]`}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("rows after the migration = %v, want %v: an npm listener keeps the binding NPM publishes, a tunnel listener has none", got, want)
	}
	if _, err := db.Writer().ExecContext(ctx, `INSERT INTO core_listeners (id, address, port, allowed_peer, client_header,
		public_host, change_id, state, created_at, updated_at) VALUES ('L3', '172.31.255.5', 9443, '172.31.255.6',
		'Cf-Connecting-IP', 'panel.example.org', 'C3', 'open', ?, ?)`, at, at); err != nil {
		t.Fatalf("a listener row without a public binding: %v", err)
	}
	if got := surfacesRows(t, db, `SELECT json_array(public_binding) FROM core_listeners WHERE id = 'L3'`); len(got) != 1 || got[0] != `[""]` {
		t.Errorf("a row inserted without the column reads %v, want an empty binding", got)
	}
}
