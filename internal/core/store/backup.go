package store

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const backupTimeLayout = "20060102T150405Z"

var (
	unsafeVersionChars = regexp.MustCompile(`[^A-Za-z0-9._]`)
	backupDirName      = regexp.MustCompile(`^[A-Za-z0-9._]+-[A-Za-z0-9._]+-(\d{8}T\d{6}Z)$`)
)

// chmod is os.Chmod; a test replaces it to fail the backup's mode change.
var chmod = os.Chmod

// Backup copies db with VACUUM INTO to root/<from>-<to>-<UTC time>/<db file
// name> (directory 0700, file 0600). A failed copy or mode change removes its
// directory. Characters other than letters, digits, dot and underscore in
// from and to become underscores, and an empty one becomes "unknown", so
// retention always recognizes the name. It returns the new backup's
// directory and deletes no older backup; PruneBackups does. Backups are for
// disaster recovery only; nothing restores them automatically (ADR 0005).
func Backup(ctx context.Context, db *DB, root, from, to string, now time.Time) (string, error) {
	name := fmt.Sprintf("%s-%s-%s", versionLabel(from), versionLabel(to), now.UTC().Format(backupTimeLayout))
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", fmt.Errorf("store: backup root: %w", err)
	}
	if err := os.Mkdir(dir, 0o700); err != nil {
		return "", fmt.Errorf("store: backup directory: %w", err)
	}
	target := filepath.Join(dir, filepath.Base(db.Path()))
	if _, err := db.Writer().ExecContext(ctx, "VACUUM INTO ?", target); err != nil {
		// A failed copy must not leave a directory that retention counts as a backup.
		return "", errors.Join(fmt.Errorf("store: VACUUM INTO %s: %w", target, err), os.RemoveAll(dir))
	}
	if err := chmod(target, 0o600); err != nil {
		return "", errors.Join(fmt.Errorf("store: backup mode: %w", err), os.RemoveAll(dir))
	}
	return dir, nil
}

func versionLabel(v string) string {
	if v == "" {
		return "unknown"
	}
	return unsafeVersionChars.ReplaceAllString(v, "_")
}

// PruneBackups deletes the oldest backups under root beyond the newest keep,
// ranked by the time in their names. A backup is a directory named as Backup
// names one that holds dbName as a regular file, so an unrelated directory, a
// symlink or a directory without the database is never counted or deleted.
// A root that does not exist holds no backups. A directory that cannot be
// deleted does not stop the others; every failure is returned.
func PruneBackups(root, dbName string, keep int) error {
	return pruneBackups(root, dbName, "", "", keep)
}

// pruneBackups is PruneBackups that never deletes the backup named current
// or the newest backup made for version keepTo, and counts them first. A
// clock set back would otherwise rank the backup taken before an upgrade
// among the oldest, on the run that took it or on any restart after it. An
// empty current or keepTo protects nothing.
func pruneBackups(root, dbName, current, keepTo string, keep int) error {
	if keep < 1 {
		return fmt.Errorf("store: backups kept = %d, want at least 1", keep)
	}
	entries, err := os.ReadDir(root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("store: list backups: %w", err)
	}
	type backup struct{ name, to, stamp string }
	var backups []backup
	for _, e := range entries {
		m := backupDirName.FindStringSubmatch(e.Name())
		if !e.IsDir() || m == nil || !holdsDatabase(filepath.Join(root, e.Name()), dbName) {
			continue
		}
		// Version labels hold no '-', so a backup name splits into exactly
		// from, to and the time.
		backups = append(backups, backup{e.Name(), strings.Split(e.Name(), "-")[1], m[1]})
	}
	sort.Slice(backups, func(i, j int) bool {
		if backups[i].stamp != backups[j].stamp {
			return backups[i].stamp > backups[j].stamp
		}
		return strings.Compare(backups[i].name, backups[j].name) > 0
	})
	protected := map[string]bool{}
	if current != "" {
		protected[current] = true
	}
	for _, b := range backups {
		if keepTo != "" && b.to == keepTo {
			protected[b.name] = true
			break
		}
	}
	kept := 0
	for _, b := range backups {
		if protected[b.name] {
			kept++
		}
	}
	var errs []error
	for _, b := range backups {
		if protected[b.name] {
			continue
		}
		if kept < keep {
			kept++
			continue
		}
		if err := os.RemoveAll(filepath.Join(root, b.name)); err != nil {
			// Keep going: stopping here would let every retried run add a copy.
			errs = append(errs, fmt.Errorf("store: prune backup %s: %w", b.name, err))
		}
	}
	return errors.Join(errs...)
}

// holdsDatabase reports whether dir holds name as a regular file; Lstat keeps
// a symlink from qualifying.
func holdsDatabase(dir, name string) bool {
	info, err := os.Lstat(filepath.Join(dir, name))
	return err == nil && info.Mode().IsRegular()
}
