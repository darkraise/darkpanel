//go:build !harnessmigration

package store

import (
	"io/fs"
	"strings"
	"testing"
)

// TestNoHarnessMigration: only the harnessmigration build embeds a 9000
// migration; an older binary ignores it once recorded
// (TestMigrateIgnoresNewerRecordedMigrations).
func TestNoHarnessMigration(t *testing.T) {
	for _, side := range []string{SideController, SideAgent} {
		set, err := CoreMigrations(side)
		if err != nil {
			t.Fatal(err)
		}
		files, err := fs.Glob(set.FS, "*.sql")
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range files {
			if strings.HasPrefix(f, "9000_") {
				t.Errorf("the default build embeds %s/%s", side, f)
			}
		}
	}
}
