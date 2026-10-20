package update

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/paths"
)

// FileSHA256 is the lower-case hex SHA-256 of the file at path.
func FileSHA256(path string) (string, error) {
	f, err := os.Open(path) //nolint:gosec // G304: path is under paths.Paths, never from a request
	if err != nil {
		return "", fmt.Errorf("update: hash %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("update: hash %s: %w", path, err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Stage copies src, a verified binary, to p.NewBinary(tx) with mode 0755 and
// returns its hash. The staged file sits next to darkpanel, on the same
// filesystem, so the swap is a rename.
func Stage(p paths.Paths, tx, src string) (string, error) {
	if !recordID.MatchString(tx) {
		return "", fmt.Errorf("update: %q is not a transaction id", tx)
	}
	sum, err := copySynced(src, p.NewBinary(tx), 0o755)
	if err != nil {
		return "", errors.Join(fmt.Errorf("update: stage %s: %w", tx, err), removeIfExists(p.NewBinary(tx)))
	}
	if err := syncDir(p.BinaryDir); err != nil {
		return "", fmt.Errorf("update: stage %s: %w", tx, err)
	}
	return sum, nil
}

// KeepPrevious copies the installed binary to darkpanel.prev and hard-links
// that as darkpanel.recovery, which the recovery units run until the update
// is over, and returns the previous binary's hash. Both are replaced by a
// rename, so a crash leaves either the old file or the complete new one.
func KeepPrevious(p paths.Paths) (string, error) {
	tmp := filepath.Join(p.BinaryDir, "darkpanel.prev.tmp")
	sum, err := copySynced(p.Binary(), tmp, 0o755)
	if err != nil {
		return "", errors.Join(fmt.Errorf("update: keep the previous binary: %w", err), removeIfExists(tmp))
	}
	if err := os.Rename(tmp, p.PrevBinary()); err != nil {
		return "", fmt.Errorf("update: keep the previous binary: %w", err)
	}
	if err := linkRecoveryBinary(p); err != nil {
		return "", err
	}
	return sum, nil
}

// linkRecoveryBinary hard-links darkpanel.prev as darkpanel.recovery,
// replacing an older one by a rename, and syncs BinaryDir.
func linkRecoveryBinary(p paths.Paths) error {
	link := filepath.Join(p.BinaryDir, "darkpanel.recovery.tmp")
	if err := removeIfExists(link); err != nil {
		return fmt.Errorf("update: link the recovery binary: %w", err)
	}
	if err := os.Link(p.PrevBinary(), link); err != nil {
		return fmt.Errorf("update: link the recovery binary: %w", err)
	}
	if err := os.Rename(link, p.RecoveryBinary()); err != nil {
		return fmt.Errorf("update: link the recovery binary: %w", err)
	}
	if err := syncDir(p.BinaryDir); err != nil {
		return fmt.Errorf("update: link the recovery binary: %w", err)
	}
	return nil
}

// Swap renames the staged binary of tx over the installed one.
func Swap(p paths.Paths, tx string) error {
	if !recordID.MatchString(tx) {
		return fmt.Errorf("update: %q is not a transaction id", tx)
	}
	if err := os.Rename(p.NewBinary(tx), p.Binary()); err != nil {
		return fmt.Errorf("update: swap %s: %w", tx, err)
	}
	if err := syncDir(p.BinaryDir); err != nil {
		return fmt.Errorf("update: swap %s: %w", tx, err)
	}
	return nil
}

// RestorePrevious puts the previous binary back: darkpanel moves to
// darkpanel.failed and darkpanel.prev to darkpanel. It resumes from whichever
// rename a crash left undone, telling the binaries apart by their hashes:
// darkpanel already holding prevSHA means there is nothing left to do (also
// when the crash came before the swap). Anything else it cannot account for,
// a darkpanel.prev that is missing or is not prevSHA, or a darkpanel that is
// neither binary, fails with update.prev_mismatch and renames nothing, so an
// operator finds the files as they were.
func RestorePrevious(p paths.Paths, prevSHA, newSHA string) error {
	bin, err := hashIfExists(p.Binary())
	if err != nil {
		return err
	}
	if bin == prevSHA {
		return nil
	}
	prev, err := hashIfExists(p.PrevBinary())
	if err != nil {
		return err
	}
	switch {
	case prev == "":
		return errcode.New(errcode.UpdatePrevMismatch, "darkpanel.prev is missing, so the previous binary cannot be restored")
	case prev != prevSHA:
		return errcode.New(errcode.UpdatePrevMismatch, "darkpanel.prev is not the binary this update replaced")
	case bin != "" && bin != newSHA:
		return errcode.New(errcode.UpdatePrevMismatch, "darkpanel is neither the previous nor the updated binary")
	}
	if bin != "" {
		if err := os.Rename(p.Binary(), p.FailedBinary()); err != nil {
			return fmt.Errorf("update: set the failed binary aside: %w", err)
		}
		if err := syncDir(p.BinaryDir); err != nil {
			return fmt.Errorf("update: set the failed binary aside: %w", err)
		}
	}
	if err := os.Rename(p.PrevBinary(), p.Binary()); err != nil {
		return fmt.Errorf("update: restore the previous binary: %w", err)
	}
	if err := syncDir(p.BinaryDir); err != nil {
		return fmt.Errorf("update: restore the previous binary: %w", err)
	}
	return nil
}

// hashIfExists is FileSHA256, or "" when there is no file at path.
func hashIfExists(path string) (string, error) {
	sum, err := FileSHA256(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	return sum, err
}

// copySynced copies src to dst with perm, syncing dst before it returns the
// copy's hash.
func copySynced(src, dst string, perm os.FileMode) (string, error) {
	in, err := os.Open(src) //nolint:gosec // G304: src is a staged or installed binary path
	if err != nil {
		return "", err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm) //nolint:gosec // G304: dst is under paths.BinaryDir
	if err != nil {
		return "", err
	}
	h := sha256.New()
	if err := out.Chmod(perm); err != nil {
		return "", errors.Join(err, out.Close())
	}
	if _, err := io.Copy(io.MultiWriter(out, h), in); err != nil {
		return "", errors.Join(err, out.Close())
	}
	if err := out.Sync(); err != nil {
		return "", errors.Join(err, out.Close())
	}
	if err := out.Close(); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func removeIfExists(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}
