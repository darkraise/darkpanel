package update

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"time"

	"golang.org/x/sys/unix"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/paths"
)

// RecordFormat is the transaction record format this binary writes. Formats
// change only additively within a major version, so a reader accepts every
// format from 1 up and ignores members it does not know (ADR 0004).
const RecordFormat = 1

// recordID bounds transaction ids, which name files under UpdateDir.
var recordID = regexp.MustCompile(`^[0-9A-Za-z]{1,64}$`)

// recordFile matches a record's file name; the lock and temporary files never
// match.
var recordFile = regexp.MustCompile(`^([0-9A-Za-z]{1,64})\.json$`)

// Record is one transaction of an update or rollback operation, stored as
// UpdateDir/<id>.json: the transaction fields core.update.status reports,
// plus what the runner and the rollback need.
type Record struct {
	FormatVersion int               `json:"formatVersion"`
	ID            string            `json:"id"`
	OperationID   string            `json:"operationId"`
	ParentID      *string           `json:"parentId"`
	Kind          string            `json:"kind"`
	FromVersion   string            `json:"fromVersion"`
	ToVersion     string            `json:"toVersion"`
	TargetVersion string            `json:"targetVersion"`
	State         string            `json:"state"`
	Error         *core.ErrorDetail `json:"error"`
	StartedAt     time.Time         `json:"startedAt"`
	UpdatedAt     time.Time         `json:"updatedAt"`
	// ChangeID is the change that started the operation; empty for a stepping
	// child and for a rollback started without the agent.
	ChangeID string `json:"changeId"`
	// Steps is every step of the operation with the channel its index entry
	// carried, written before the first swap.
	Steps      []Release  `json:"steps"`
	Deadline   *time.Time `json:"deadline"`
	PrevSHA256 string     `json:"prevSha256"`
	NewSHA256  string     `json:"newSha256"`
}

// Transaction is the record as core.update.status reports it.
func (r Record) Transaction() core.UpdateTransaction {
	return core.UpdateTransaction{
		ID:            r.ID,
		OperationID:   r.OperationID,
		ParentID:      r.ParentID,
		Kind:          r.Kind,
		FromVersion:   r.FromVersion,
		ToVersion:     r.ToVersion,
		TargetVersion: r.TargetVersion,
		State:         r.State,
		Error:         r.Error,
		StartedAt:     r.StartedAt,
		UpdatedAt:     r.UpdatedAt,
	}
}

// ReadRecord reads the record of transaction id. A missing record's error
// wraps fs.ErrNotExist.
func ReadRecord(p paths.Paths, id string) (Record, error) {
	if !recordID.MatchString(id) {
		return Record{}, fmt.Errorf("update: %q is not a transaction id", id)
	}
	data, err := os.ReadFile(p.UpdateRecord(id))
	if err != nil {
		return Record{}, fmt.Errorf("update: read record %s: %w", id, err)
	}
	var r Record
	if err := json.Unmarshal(data, &r); err != nil {
		return Record{}, fmt.Errorf("update: read record %s: %w", id, err)
	}
	switch {
	case r.FormatVersion < 1:
		return Record{}, fmt.Errorf("update: record %s has no format version", id)
	case r.ID != id:
		return Record{}, fmt.Errorf("update: record %s names transaction %q", id, r.ID)
	}
	return r, nil
}

// WriteRecord replaces the record of r.ID with format RecordFormat. The record
// is written to a temporary file, synced and renamed into place, and UpdateDir
// is synced, so a crash leaves the old or the new record, never a torn one.
// UpdateDir must exist; the agent creates it with its own mode.
func WriteRecord(p paths.Paths, r Record) error {
	if !recordID.MatchString(r.ID) {
		return fmt.Errorf("update: %q is not a transaction id", r.ID)
	}
	r.FormatVersion = RecordFormat
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("update: encode record %s: %w", r.ID, err)
	}
	data = append(data, '\n')
	tmp := filepath.Join(p.UpdateDir, "."+r.ID+".json.tmp")
	if err := writeSynced(tmp, data, 0o600); err != nil {
		return fmt.Errorf("update: write record %s: %w", r.ID, err)
	}
	if err := os.Rename(tmp, p.UpdateRecord(r.ID)); err != nil {
		return errors.Join(fmt.Errorf("update: write record %s: %w", r.ID, err), os.Remove(tmp))
	}
	if err := syncDir(p.UpdateDir); err != nil {
		return fmt.Errorf("update: write record %s: %w", r.ID, err)
	}
	return nil
}

// ListRecords returns every record under UpdateDir, ordered by StartedAt then
// ID; none when UpdateDir does not exist. An unreadable record is an error,
// never skipped, because skipping a swapped record would skip its rollback.
func ListRecords(p paths.Paths) ([]Record, error) {
	entries, err := os.ReadDir(p.UpdateDir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("update: list records: %w", err)
	}
	var out []Record
	for _, e := range entries {
		m := recordFile.FindStringSubmatch(e.Name())
		if m == nil || !e.Type().IsRegular() {
			continue
		}
		r, err := ReadRecord(p, m[1])
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	sortRecords(out)
	return out, nil
}

// OperationRecords returns the records of operationID from all, ordered by
// StartedAt then ID.
func OperationRecords(all []Record, operationID string) []Record {
	var out []Record
	for _, r := range all {
		if r.OperationID == operationID {
			out = append(out, r)
		}
	}
	sortRecords(out)
	return out
}

func sortRecords(rs []Record) {
	slices.SortStableFunc(rs, func(a, b Record) int {
		if c := a.StartedAt.Compare(b.StartedAt); c != 0 {
			return c
		}
		return cmp.Compare(a.ID, b.ID)
	})
}

// Finished reports whether the operation whose records are op, ordered as
// OperationRecords orders them, is over: any record failed or rolled back,
// or its last record committed the target version. A committed intermediate
// step is not finished, because the next step still holds the reservation.
// An operation with no record is not finished; what a missing record means
// is the caller's to decide.
func Finished(op []Record) bool {
	if len(op) == 0 {
		return false
	}
	for _, r := range op {
		if r.State == core.UpdateFailed || r.State == core.UpdateRolledBack {
			return true
		}
	}
	last := op[len(op)-1]
	return last.Kind == core.UpdateKindUpdate && last.State == core.UpdateCommitted &&
		last.ToVersion == last.TargetVersion
}

// writeSynced writes data to a new or truncated file at path with perm and
// syncs it before closing.
func writeSynced(path string, data []byte, perm os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm) //nolint:gosec // G304: path is under paths.Paths, never from a request
	if err != nil {
		return err
	}
	if err := f.Chmod(perm); err != nil {
		return errors.Join(err, f.Close())
	}
	if _, err := f.Write(data); err != nil {
		return errors.Join(err, f.Close())
	}
	if err := f.Sync(); err != nil {
		return errors.Join(err, f.Close())
	}
	return f.Close()
}

// syncDir syncs a directory, so that renames and links in it survive a crash.
func syncDir(dir string) error {
	d, err := os.Open(dir) //nolint:gosec // G304: dir comes from paths.Paths, never from a request
	if err != nil {
		return err
	}
	if err := d.Sync(); err != nil {
		return errors.Join(err, d.Close())
	}
	return d.Close()
}

// LockTimeout is how long a recover path waits for the update lock before it
// stops the runner that holds it (spec section 8, Taking the update lock).
const LockTimeout = 5 * time.Second

// lockPoll is how often AcquireLock retries a held lock.
const lockPoll = 20 * time.Millisecond

// ErrLockTimeout is AcquireLock's error when the lock stayed held for the
// whole timeout.
var ErrLockTimeout = errors.New("update: the update lock is held by another process")

// Lock is a held exclusive flock on the update lock file. The lock
// belongs to the open file, so it ends with the process that holds it even
// when that process is killed, and Go's close-on-exec keeps a restarted
// service from inheriting it.
type Lock struct {
	f *os.File
}

// AcquireLock takes an exclusive flock on path, creating the file with mode
// 0600, and retries a held lock until timeout passes (ErrLockTimeout) or ctx
// ends.
func AcquireLock(ctx context.Context, path string, timeout time.Duration) (*Lock, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600) //nolint:gosec // G304: the path is paths.UpdateLock, never from a request
	if err != nil {
		return nil, fmt.Errorf("update: open the update lock: %w", err)
	}
	deadline := time.Now().Add(timeout)
	for {
		err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB) //nolint:gosec // G115: a file descriptor fits an int
		if err == nil {
			return &Lock{f: f}, nil
		}
		if !errors.Is(err, unix.EWOULDBLOCK) {
			return nil, errors.Join(fmt.Errorf("update: take the update lock: %w", err), f.Close())
		}
		if !time.Now().Before(deadline) {
			return nil, errors.Join(ErrLockTimeout, f.Close())
		}
		select {
		case <-ctx.Done():
			return nil, errors.Join(ctx.Err(), f.Close())
		case <-time.After(lockPoll):
		}
	}
}

// Release drops the lock.
func (l *Lock) Release() error {
	if err := l.f.Close(); err != nil {
		return fmt.Errorf("update: release the update lock: %w", err)
	}
	return nil
}
