package store

import (
	"context"
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
// name> (directory 0700, file 0600) and then deletes all but the newest keep
// backup directories under root. Characters other than letters, digits, dot
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
		return "", fmt.Errorf("store: VACUUM INTO %s: %w", target, err)
	}
	if err := os.Chmod(target, 0o600); err != nil {
		return "", fmt.Errorf("store: backup mode: %w", err)
	}
	if err := pruneBackups(root, keep); err != nil {
		return "", err
	}
	return dir, nil
}

// pruneBackups deletes the oldest backup directories beyond keep. It only
// considers directories whose names Backup produces.
func pruneBackups(root string, keep int) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		return fmt.Errorf("store: list backups: %w", err)
	}
	type backup struct{ name, stamp string }
	var backups []backup
	for _, e := range entries {
		m := backupDirName.FindStringSubmatch(e.Name())
		if !e.IsDir() || m == nil {
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
	for _, b := range backups[min(keep, len(backups)):] {
		if err := os.RemoveAll(filepath.Join(root, b.name)); err != nil {
			return fmt.Errorf("store: prune backup %s: %w", b.name, err)
		}
	}
	return nil
}
