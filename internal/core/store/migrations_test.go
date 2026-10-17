package store

import (
	"context"
	"testing"
	"time"
)

func TestCoreMigrationsApply(t *testing.T) {
	tests := []struct {
		side   string
		tables []string
	}{
		{SideController, []string{"core_meta", "core_hosts", "core_module_state", "core_ca"}},
		{SideAgent, []string{"core_meta"}},
	}
	for _, tt := range tests {
		t.Run(tt.side, func(t *testing.T) {
			db := openTemp(t)
			set, err := CoreMigrations(tt.side)
			if err != nil {
				t.Fatal(err)
			}
			opts := migrateOpts(t, "v1", time.Now())
			opts.Side = tt.side
			res, err := Migrate(context.Background(), db, opts, set, nil)
			if err != nil {
				t.Fatalf("Migrate: %v", err)
			}
			if len(res.Applied) == 0 {
				t.Fatal("no core migration applied")
			}
			for _, table := range tt.tables {
				if !tableExists(t, db, table) {
					t.Errorf("table %s missing", table)
				}
			}
		})
	}
}

func TestCoreMigrationsUnknownSide(t *testing.T) {
	if _, err := CoreMigrations("browser"); err == nil {
		t.Fatal("CoreMigrations accepted an unknown side")
	}
}

func TestOneLocalHost(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	set, _ := CoreMigrations(SideController)
	if _, err := Migrate(ctx, db, migrateOpts(t, "v1", time.Now()), set, nil); err != nil {
		t.Fatal(err)
	}
	insert := "INSERT INTO core_hosts (id, name, is_local, created_at) VALUES (?, ?, 1, '2026-10-17T00:00:00Z')"
	if _, err := db.Writer().ExecContext(ctx, insert, "01A", "one"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Writer().ExecContext(ctx, insert, "01B", "two"); err == nil {
		t.Fatal("a second local host was accepted")
	}
}
