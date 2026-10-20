//go:build harnessmigration

package store

import (
	"context"
	"io/fs"
	"slices"
	"testing"
	"time"
)

func TestHarnessMigration(t *testing.T) {
	for _, side := range []string{SideController, SideAgent} {
		t.Run(side, func(t *testing.T) {
			set, err := CoreMigrations(side)
			if err != nil {
				t.Fatal(err)
			}
			files, err := fs.Glob(set.FS, "*.sql")
			if err != nil || len(files) < 2 || files[len(files)-1] != "9000_harness_marker.sql" {
				t.Fatalf("core migrations of %s = %v, %v; want the real ones, then 9000_harness_marker.sql", side, files, err)
			}
			db := openTemp(t)
			opts := migrateOpts(t, "v1", time.Now())
			opts.Side = side
			if _, err := Migrate(context.Background(), db, opts, set, nil); err != nil {
				t.Fatal(err)
			}
			var note string
			if err := db.Read().QueryRow("SELECT note FROM harness_marker").Scan(&note); err != nil || note != "written by the harnessmigration build" {
				t.Errorf("harness_marker = %q, %v", note, err)
			}
			if v, err := SchemaVersion(context.Background(), db, side); err != nil || v != 9000 {
				t.Errorf("SchemaVersion = %d, %v; want 9000", v, err)
			}
		})
	}
}

func TestHarnessMigrationWalks(t *testing.T) {
	set, err := CoreMigrations(SideAgent)
	if err != nil {
		t.Fatal(err)
	}
	want, err := fs.Glob(set.FS, "*.sql")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	err = fs.WalkDir(set.FS, ".", func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			got = append(got, p)
		}
		return err
	})
	if err != nil || !slices.Equal(got, want) {
		t.Errorf("fs.WalkDir = %q, %v; want %q", got, err, want)
	}
}

func TestHarnessMigrationRefusesAShadowedName(t *testing.T) {
	core := sqlFS(map[string]string{"0001_core.sql": metaSQL})
	if _, err := fs.ReadDir(unionFS{core, core}, "."); err == nil {
		t.Error("the union read a migration that is in both sets")
	}
}
