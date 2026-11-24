package server

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/store"
)

// TestFailedModuleMigrationContributesNothing: a module whose controller
// migration failed provides no notifier, routes or services, so an Emit
// cannot roll back the caller's transaction on its missing tables.
func TestFailedModuleMigrationContributesNothing(t *testing.T) {
	opts := testOptions(t)
	ctx := context.Background()
	first, err := Start(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(ctx, opts.Paths.ControllerDB())
	if err != nil {
		t.Fatal(err)
	}
	rows, err := db.Read().QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type = 'table' AND name LIKE 'notify\_%' ESCAPE '\'`)
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, name)
	}
	if err := rows.Close(); err != nil || len(tables) == 0 {
		t.Fatalf("notify tables %v (%v), want some", tables, err)
	}
	// A table in the way makes the notify module's first migration fail.
	stmts := []string{`PRAGMA foreign_keys = OFF`, `DELETE FROM core_migrations WHERE module = 'notify'`}
	for _, name := range tables {
		stmts = append(stmts, `DROP TABLE `+name)
	}
	stmts = append(stmts, `CREATE TABLE notify_channels (x TEXT)`)
	for _, q := range stmts {
		if _, err := db.Writer().ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	s := start(t, opts)
	err = s.controllerDB.Write(ctx, func(tx *sql.Tx) error {
		return s.notifier.Emit(ctx, tx, core.NotifyEvent{Kind: core.NotifyRolloutFailed, Severity: core.SeverityCritical,
			Title: "Rollout failed", Body: "b", DedupeKey: "rollout.failed:r1"})
	})
	if err != nil {
		t.Errorf("Emit with the notify migration failed = %v, want nil", err)
	}
	_, services, err := s.contributions()
	if err != nil {
		t.Fatal(err)
	}
	for _, svc := range services {
		if strings.HasPrefix(svc.Name, "notify.") {
			t.Errorf("service %s runs although the notify migration failed", svc.Name)
		}
	}
}
