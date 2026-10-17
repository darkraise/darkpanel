package store

import (
	"context"
	"errors"
	"fmt"
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

// Backup copies db with VACUUM INTO to root/<from>-<to>-<UTC time>/<db file
// name> (directory 0700, file 0600) and then deletes backup directories under
// root beyond keep, counting the new one first whatever its timestamp. A failed
// copy removes its directory. Characters other than letters, digits, dot
// and underscore in from and to become underscores, so the name always parses.
// It returns the new backup's directory. Backups are for disaster recovery
// only; nothing restores them automatically (ADR 0005).
func Backup(ctx context.Context, db *DB, root, from, to string, now time.Time, keep int) (string, error) {
	if keep < 1 {
		return "", fmt.Errorf("store: backup keep = %d, want at least 1", keep)
	}
	name := fmt.Sprintf("%s-%s-%s",
		unsafeVersionChars.ReplaceAllString(from, "_"),
		unsafeVersionChars.ReplaceAllString(to, "_"),
		now.UTC().Format(backupTimeLayout))
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
	if err := os.Chmod(target, 0o600); err != nil {
		return "", errors.Join(fmt.Errorf("store: backup mode: %w", err), os.RemoveAll(dir))
	}
	if err := pruneBackups(root, name, keep); err != nil {
		return "", err
	}
	return dir, nil
}

// pruneBackups deletes the oldest backup directories beyond keep, never the
// one named current. It only considers directories whose names Backup produces.
func pruneBackups(root, current string, keep int) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		return fmt.Errorf("store: list backups: %w", err)
	}
	type backup struct{ name, stamp string }
	var backups []backup
	for _, e := range entries {
		m := backupDirName.FindStringSubmatch(e.Name())
		// current is excluded so a clock set back cannot rank it among the oldest.
		if !e.IsDir() || m == nil || e.Name() == current {
			continue
		}
		backups = append(backups, backup{e.Name(), m[1]})
	}
	sort.Slice(backups, func(i, j int) bool {
		if backups[i].stamp != backups[j].stamp {
			return backups[i].stamp > backups[j].stamp
		}
		return strings.Compare(backups[i].name, backups[j].name) > 0
	})
	var errs []error
	for _, b := range backups[min(keep-1, len(backups)):] {
		if err := os.RemoveAll(filepath.Join(root, b.name)); err != nil {
			// Keep going: stopping here would let every retried run add a copy.
			errs = append(errs, fmt.Errorf("store: prune backup %s: %w", b.name, err))
		}
	}
	return errors.Join(errs...)
}
