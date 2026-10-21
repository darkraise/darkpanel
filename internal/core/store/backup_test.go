package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

// makeBackup creates root/name holding a test.db file, as Backup leaves a
// backup of openTemp's database.
func makeBackup(t *testing.T, root, name string) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "test.db"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
}

// entryNames lists the names in root, sorted.
func entryNames(t *testing.T, root string) []string {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func TestBackupCopiesDatabase(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	if err := db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.Exec("CREATE TABLE t (x INTEGER); INSERT INTO t VALUES (42)")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "backups")
	now := time.Date(2026, 10, 17, 9, 30, 0, 0, time.UTC)

	dir, err := Backup(ctx, db, root, "v1.0.0+dirty", "v1.1.0", now)
	if err != nil {
		t.Fatalf("Backup: %v", err)
	}
	if want := filepath.Join(root, "v1.0.0_dirty-v1.1.0-20261017T093000Z"); dir != want {
		t.Errorf("backup dir = %s, want %s", dir, want)
	}
	copyPath := filepath.Join(dir, "test.db")
	info, err := os.Stat(copyPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("backup mode = %o, want 600", info.Mode().Perm())
	}
	copyDB, err := Open(ctx, copyPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = copyDB.Close() }()
	var x int
	if err := copyDB.Read().QueryRow("SELECT x FROM t").Scan(&x); err != nil || x != 42 {
		t.Errorf("backup row = %d, %v; want 42", x, err)
	}
}

func TestPruneBackupsKeepsNewest(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "unrelated"), 0o700); err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC)
	for i := range 7 {
		if _, err := Backup(ctx, db, root, "a", "b", start.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatalf("Backup %d: %v", i, err)
		}
	}
	if err := PruneBackups(root, "test.db", 5); err != nil {
		t.Fatalf("PruneBackups: %v", err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	want := []string{
		"a-b-20261017T000200Z", "a-b-20261017T000300Z", "a-b-20261017T000400Z",
		"a-b-20261017T000500Z", "a-b-20261017T000600Z", "unrelated",
	}
	if len(names) != len(want) {
		t.Fatalf("entries = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("entries = %v, want %v", names, want)
		}
	}
}

func TestPruneBackupsRecognizesEmptyVersions(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	root := t.TempDir()
	start := time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC)
	for i := range 7 {
		dir, err := Backup(ctx, db, root, "", "", start.Add(time.Duration(i)*time.Minute))
		if err != nil {
			t.Fatalf("Backup %d: %v", i, err)
		}
		if i == 0 && filepath.Base(dir) != "unknown-unknown-20261017T000000Z" {
			t.Errorf("backup dir = %s, want unknown-unknown-20261017T000000Z", filepath.Base(dir))
		}
	}
	if err := PruneBackups(root, "test.db", 5); err != nil {
		t.Fatalf("PruneBackups: %v", err)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 5 {
		t.Errorf("entries = %v, %v; want 5", entries, err)
	}
}

func TestBackupRefusesExistingDirectory(t *testing.T) {
	db := openTemp(t)
	root := t.TempDir()
	now := time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC)
	if _, err := Backup(context.Background(), db, root, "a", "b", now); err != nil {
		t.Fatal(err)
	}
	if _, err := Backup(context.Background(), db, root, "a", "b", now); err == nil {
		t.Fatal("second backup with the same name succeeded, want an error")
	}
}

func TestBackupFailureLeavesNoDirectory(t *testing.T) {
	db := openTemp(t)
	root := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Backup(ctx, db, root, "a", "b", time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC)); err == nil {
		t.Fatal("Backup succeeded with a cancelled context")
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Errorf("entries after a failed backup = %v, %v; want none", entries, err)
	}
}

func TestPruneKeepsNewDirectoryWhenClockGoesBack(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	root := t.TempDir()
	later := time.Date(2026, 10, 18, 0, 0, 0, 0, time.UTC)
	for i := range 5 {
		if _, err := Backup(ctx, db, root, "a", "b", later.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatalf("Backup %d: %v", i, err)
		}
	}
	dir, err := Backup(ctx, db, root, "a", "b", later.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err := pruneBackups(root, "test.db", filepath.Base(dir), "", 5); err != nil {
		t.Fatalf("pruneBackups: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "test.db")); err != nil {
		t.Errorf("new backup: %v", err)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 5 {
		t.Errorf("entries = %v, %v; want 5", entries, err)
	}
	if _, err := os.Stat(filepath.Join(root, "a-b-20261018T000000Z")); !os.IsNotExist(err) {
		t.Errorf("oldest of the later backups survived: %v", err)
	}
}

func TestPruneBackupsCountsOnlyDirectoriesHoldingTheDatabase(t *testing.T) {
	root := t.TempDir()
	for i := range 3 {
		makeBackup(t, root, fmt.Sprintf("a-b-20261017T00000%dZ", i))
	}
	// Named like backups and older than every real one, but holding no
	// database file of their own.
	empty := filepath.Join(root, "a-b-20200101T000000Z")
	if err := os.Mkdir(empty, 0o700); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(root, "a-b-20200101T000001Z")
	if err := os.Mkdir(linked, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "a-b-20261017T000000Z", "test.db"), filepath.Join(linked, "test.db")); err != nil {
		t.Fatal(err)
	}
	if err := PruneBackups(root, "test.db", 2); err != nil {
		t.Fatalf("PruneBackups: %v", err)
	}
	want := []string{"a-b-20200101T000000Z", "a-b-20200101T000001Z", "a-b-20261017T000001Z", "a-b-20261017T000002Z"}
	if got := entryNames(t, root); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("entries = %v, want %v", got, want)
	}
}

func TestPruneBackupsIgnoresSymlinks(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	makeBackup(t, outside, "a-b-20200101T000000Z")
	if err := os.Symlink(filepath.Join(outside, "a-b-20200101T000000Z"), filepath.Join(root, "a-b-20200101T000000Z")); err != nil {
		t.Fatal(err)
	}
	makeBackup(t, root, "a-b-20261017T000000Z")
	makeBackup(t, root, "a-b-20261017T000001Z")
	if err := PruneBackups(root, "test.db", 1); err != nil {
		t.Fatalf("PruneBackups: %v", err)
	}
	want := []string{"a-b-20200101T000000Z", "a-b-20261017T000001Z"}
	if got := entryNames(t, root); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("entries = %v, want %v", got, want)
	}
	if _, err := os.Stat(filepath.Join(outside, "a-b-20200101T000000Z", "test.db")); err != nil {
		t.Errorf("the symlink's target: %v, want it untouched", err)
	}
}

func TestPruneBackupsRefusesKeepBelowOne(t *testing.T) {
	root := t.TempDir()
	makeBackup(t, root, "a-b-20261017T000000Z")
	for _, keep := range []int{0, -1} {
		if err := PruneBackups(root, "test.db", keep); err == nil {
			t.Errorf("PruneBackups with keep %d succeeded, want an error", keep)
		}
	}
	if got := entryNames(t, root); len(got) != 1 {
		t.Errorf("entries = %v, want the one backup", got)
	}
}

func TestPruneBackupsWithoutRoot(t *testing.T) {
	if err := PruneBackups(filepath.Join(t.TempDir(), "missing"), "test.db", 5); err != nil {
		t.Errorf("PruneBackups of a missing root = %v, want nil", err)
	}
}

func TestBackupFailedChmodLeavesNoDirectory(t *testing.T) {
	db := openTemp(t)
	root := t.TempDir()
	boom := errors.New("boom")
	chmod = func(string, fs.FileMode) error { return boom }
	t.Cleanup(func() { chmod = os.Chmod })
	_, err := Backup(context.Background(), db, root, "a", "b", time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC))
	if !errors.Is(err, boom) {
		t.Fatalf("Backup error = %v, want the chmod failure", err)
	}
	if got := entryNames(t, root); len(got) != 0 {
		t.Errorf("entries after a failed chmod = %v, want none", got)
	}
}

func TestPruneProtectsKeepToWithUnsafeCharacters(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	root := t.TempDir()
	start := time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC)
	upgrade, err := Backup(ctx, db, root, "v0", "v1.0.0-rc1+dirty", start)
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 2; i++ {
		if _, err := Backup(ctx, db, root, "a", "v0", start.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatalf("Backup %d: %v", i, err)
		}
	}
	if err := pruneBackups(root, "test.db", "", "v1.0.0-rc1+dirty", 1); err != nil {
		t.Fatalf("pruneBackups: %v", err)
	}
	want := []string{filepath.Base(upgrade)}
	if got := entryNames(t, root); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("entries = %v, want %v", got, want)
	}
}
