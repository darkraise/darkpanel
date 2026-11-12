package backup

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/darkraise/darkpanel/internal/paths"
)

// Phase is the step of a restore a journal records.
type Phase string

// The phases of a restore, in the order a restore reaches them, and the
// phase of recovery's own renames back.
const (
	PhaseBegun       Phase = "begun" // written before the first staged file
	PhaseStaged      Phase = "staged"
	PhaseValidated   Phase = "validated"
	PhaseQuarantined Phase = "quarantined"
	PhaseJournaled   Phase = "journaled"
	PhaseMovedAside  Phase = "moved-aside"
	PhaseInstalled   Phase = "installed"
	PhaseVerified    Phase = "verified"
	PhaseRollback    Phase = "rollback" // recovery's own renames back
)

// The states of a journaled rename.
const (
	// RenameIntended is a rename recorded before it runs.
	RenameIntended = "intended"
	// RenameDone is a rename that ran and whose directory was fsynced.
	RenameDone = "done"
)

var journalPhases = []Phase{
	PhaseBegun, PhaseStaged, PhaseValidated, PhaseQuarantined, PhaseJournaled,
	PhaseMovedAside, PhaseInstalled, PhaseVerified, PhaseRollback,
}

// JournalFile is one original a restore replaces, and whether it existed
// when the restore set the originals aside.
type JournalFile struct {
	Path    string `json:"path"`
	Existed bool   `json:"existed"`
}

// Rename is one rename a restore or its recovery makes, recorded as
// intended before it runs and as done after its directory is fsynced.
type Rename struct {
	Phase Phase  `json:"phase"`
	From  string `json:"from"`
	To    string `json:"to"`
	State string `json:"state"`
}

// Journal is the on-disk record of one restore transaction.
type Journal struct {
	Format  int           `json:"format"` // 1
	Tx      string        `json:"tx"`
	Phase   Phase         `json:"phase"`
	Files   []JournalFile `json:"files,omitempty"` // set at PhaseJournaled: Targets order
	Renames []Rename      `json:"renames,omitempty"`
}

// Targets are the four originals a restore replaces or sets aside, in this order:
// ControllerDB(), ControllerDB()+"-wal", ControllerDB()+"-shm", ControllerKey.
func Targets(p paths.Paths) []string {
	db := p.ControllerDB()
	return []string{db, db + "-wal", db + "-shm", p.ControllerKey}
}

// StagedName is the name file is staged under before transaction tx installs it.
func StagedName(file, tx string) string {
	return file + ".restore-" + tx
}

// AsideName is the name transaction tx sets the original file aside under.
func AsideName(file, tx string) string {
	return file + ".pre-" + tx
}

// JournalPath is the journal of transaction tx in controllerDir.
func JournalPath(controllerDir, tx string) string {
	return filepath.Join(controllerDir, "restore-"+tx+".journal")
}

// SaveJournal removes a stale path+".new", writes path+".new" through ops (Create 0600, Write, Sync,
// Close), renames it over path and fsyncs the directory. It always writes format 1, and refuses an
// unknown phase without touching the disk.
func SaveJournal(ops Ops, path string, j Journal) error {
	if !slices.Contains(journalPhases, j.Phase) {
		return fmt.Errorf("backup: journal phase %q is unknown", j.Phase)
	}
	j.Format = 1
	data, err := json.Marshal(j)
	if err != nil {
		return fmt.Errorf("backup: encode journal: %w", err)
	}
	tmp := path + ".new"
	if err := ops.Remove(tmp); err != nil {
		return fmt.Errorf("backup: remove stale journal %s: %w", tmp, err)
	}
	f, err := ops.Create(tmp, 0o600)
	if err != nil {
		return fmt.Errorf("backup: create journal %s: %w", tmp, err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return fmt.Errorf("backup: write journal %s: %w", tmp, err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("backup: sync journal %s: %w", tmp, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("backup: close journal %s: %w", tmp, err)
	}
	if err := ops.Rename(tmp, path); err != nil {
		return fmt.Errorf("backup: install journal %s: %w", path, err)
	}
	if err := ops.SyncDir(filepath.Dir(path)); err != nil {
		return fmt.Errorf("backup: sync journal directory of %s: %w", path, err)
	}
	return nil
}

// decodeJournal decodes the journal read from path; a format other than 1 or an unknown phase is an
// error.
func decodeJournal(path string, data []byte) (Journal, error) {
	var j Journal
	if err := json.Unmarshal(data, &j); err != nil {
		return Journal{}, fmt.Errorf("backup: decode journal %s: %w", path, err)
	}
	if j.Format != 1 {
		return Journal{}, fmt.Errorf("backup: journal %s has format %d, want 1", path, j.Format)
	}
	if !slices.Contains(journalPhases, j.Phase) {
		return Journal{}, fmt.Errorf("backup: journal %s has unknown phase %q", path, j.Phase)
	}
	return j, nil
}

// ListJournals returns the restore-<tx>.journal paths in dir sorted by tx ascending (ULIDs sort by
// time); other names, including *.journal.new, are ignored. A missing dir holds no journals.
func ListJournals(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("backup: list journals: %w", err)
	}
	var txs []string
	for _, e := range entries {
		ok, err := filepath.Match("restore-*.journal", e.Name())
		if err != nil {
			return nil, fmt.Errorf("backup: list journals: %w", err)
		}
		if ok {
			txs = append(txs, strings.TrimSuffix(strings.TrimPrefix(e.Name(), "restore-"), ".journal"))
		}
	}
	slices.Sort(txs)
	list := make([]string, 0, len(txs))
	for _, tx := range txs {
		list = append(list, JournalPath(dir, tx))
	}
	return list, nil
}

// RenameJournaled appends {phase, from, to, intended} to j and saves it, renames from to to, fsyncs
// the directory of to, then marks the entry done and saves j again. It leaves j.Phase as it is; when
// a step fails the entry stays in j as it was last saved, for recovery to settle.
func RenameJournaled(ops Ops, journalPath string, j *Journal, phase Phase, from, to string) error {
	j.Renames = append(j.Renames, Rename{Phase: phase, From: from, To: to, State: RenameIntended})
	i := len(j.Renames) - 1
	if err := SaveJournal(ops, journalPath, *j); err != nil {
		return fmt.Errorf("backup: journal the rename of %s: %w", from, err)
	}
	if err := ops.Rename(from, to); err != nil {
		return fmt.Errorf("backup: rename %s to %s: %w", from, to, err)
	}
	if err := ops.SyncDir(filepath.Dir(to)); err != nil {
		return fmt.Errorf("backup: sync directory of %s: %w", to, err)
	}
	j.Renames[i].State = RenameDone
	if err := SaveJournal(ops, journalPath, *j); err != nil {
		return fmt.Errorf("backup: journal the finished rename of %s: %w", from, err)
	}
	return nil
}

// ErrAmbiguousRename is an intended rename whose from and to both exist or are both missing.
var ErrAmbiguousRename = errors.New("backup: a journaled rename cannot be settled")

// Settle decides an intended rename from which names exist: from present and to absent → false (not
// done); to present and from absent → true (done); both or neither → ErrAmbiguousRename.
func Settle(ops Ops, r Rename) (bool, error) {
	fromHere, err := ops.Exists(r.From)
	if err != nil {
		return false, fmt.Errorf("backup: settle rename: %w", err)
	}
	toHere, err := ops.Exists(r.To)
	if err != nil {
		return false, fmt.Errorf("backup: settle rename: %w", err)
	}
	if fromHere == toHere {
		return false, fmt.Errorf("%w: %s exists %v, %s exists %v", ErrAmbiguousRename, r.From, fromHere, r.To, toHere)
	}
	return toHere, nil
}
