package backup

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

// rcFile is a saved file.
type rcFile struct {
	data []byte
	mode fs.FileMode
}

// rcTree is a saved copy of the two directories of a layout, so a test can
// return to a state without redoing the work that made it.
type rcTree struct {
	roots  []string
	dirs   []string // every directory below the roots, parents first
	files  map[string]rcFile
	digest map[string]string
}

func rcSave(t *testing.T, l *rtLayout) rcTree {
	t.Helper()
	tree := rcTree{roots: l.dirs(), files: map[string]rcFile{}}
	for _, root := range tree.roots {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if path != root {
					tree.dirs = append(tree.dirs, path)
				}
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			tree.files[path] = rcFile{data: rtRead(t, path), mode: info.Mode().Perm()}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	tree.digest = l.digest(t)
	return tree
}

// rcReset empties the layout's directories and writes tree back.
func rcReset(t *testing.T, _ *rtLayout, tree rcTree) {
	t.Helper()
	for _, root := range tree.roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			if err := os.RemoveAll(filepath.Join(root, e.Name())); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, d := range tree.dirs {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for path, f := range tree.files {
		if err := os.WriteFile(path, f.data, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, f.mode); err != nil {
			t.Fatal(err)
		}
	}
}

// rcPoint is the journal on disk after one rename or removal of a restore.
type rcPoint struct {
	call    int // the number of that call among the restore's Ops calls
	journal Journal
}

// rcSpyOps counts calls through a never-crashing crashOps and reads the
// journal after every rename and removal.
type rcSpyOps struct {
	Ops
	counter *crashOps
	dir     string
	points  []rcPoint
}

func (s *rcSpyOps) observe() {
	js, err := ListJournals(s.dir)
	if err != nil || len(js) != 1 {
		return
	}
	j, err := ReadJournal(js[0])
	if err != nil {
		return
	}
	s.points = append(s.points, rcPoint{call: s.counter.Calls(), journal: j})
}

func (s *rcSpyOps) Rename(from, to string) error {
	err := s.Ops.Rename(from, to)
	s.observe()
	return err
}

func (s *rcSpyOps) Remove(path string) error {
	err := s.Ops.Remove(path)
	s.observe()
	return err
}

// rcTimeline runs one successful restore and returns the journal states it
// went through, then puts the layout back to tree.
func rcTimeline(t *testing.T, l *rtLayout, bk rtBackup, tree rcTree) []rcPoint {
	t.Helper()
	counter := newCrashOps(OSOps(), math.MaxInt, false)
	spy := &rcSpyOps{Ops: counter, counter: counter, dir: l.paths.ControllerDir}
	h := l.host
	h.Ops = spy
	if _, err := Restore(context.Background(), h, bytes.NewReader(bk.file), []byte(rtPass)); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	rcReset(t, l, tree)
	return spy.points
}

// rcDone counts the journal's finished renames of phase.
func rcDone(j Journal, phase Phase) int {
	n := 0
	for _, r := range j.Renames {
		if r.Phase == phase && r.State == RenameDone {
			n++
		}
	}
	return n
}

// rcFind returns the call number after which the journal first matches pred.
func rcFind(t *testing.T, points []rcPoint, pred func(Journal) bool) int {
	t.Helper()
	for _, p := range points {
		if pred(p.journal) {
			return p.call
		}
	}
	t.Fatal("no state of the restore matches")
	return 0
}

// rcCrashRestore runs Restore with a crash before or after Ops call number at.
func rcCrashRestore(t *testing.T, l *rtLayout, bk rtBackup, at int, before bool) {
	t.Helper()
	ops := newCrashOps(OSOps(), at, before)
	h := l.host
	h.Ops = ops
	_, _ = Restore(context.Background(), h, bytes.NewReader(bk.file), []byte(rtPass))
	if !ops.Crashed() {
		t.Fatalf("Restore made %d calls and never reached call %d", ops.Calls(), at)
	}
}

func rcExists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// rcSumAt is the SHA-256 of the file at path, "" when it cannot be read.
func rcSumAt(path string) string {
	b, err := os.ReadFile(path) //nolint:gosec // G304: a path under t.TempDir()
	if err != nil {
		return ""
	}
	return rtSum(b)
}

// rcTxNames lists the files of both directories whose name carries tx, sorted.
func rcTxNames(t *testing.T, l *rtLayout, tx string) []string {
	t.Helper()
	var out []string
	for _, dir := range l.dirs() {
		for _, name := range rtFiles(t, dir) {
			if strings.Contains(name, tx) {
				out = append(out, name)
			}
		}
	}
	slices.Sort(out)
	return out
}

// rcExpectRestored says which end state the layout's journal commands: true
// when phase 6 has begun (or the transaction already finished), false when
// the restore must roll back (or never began).
func rcExpectRestored(t *testing.T, l *rtLayout, baseline rcTree) bool {
	t.Helper()
	js, err := ListJournals(l.paths.ControllerDir)
	if err != nil {
		t.Fatal(err)
	}
	switch len(js) {
	case 0:
		return rcSumAt(l.paths.ControllerDB()) != baseline.digest[l.paths.ControllerDB()]
	case 1:
		j, err := ReadJournal(js[0])
		if err != nil {
			t.Fatal(err)
		}
		return j.Phase == PhaseInstalled || j.Phase == PhaseVerified
	}
	t.Fatalf("%d journals after one restore", len(js))
	return false
}

// rcAssertNothingLost checks that every original of baseline is still in
// place or at its set-aside name.
func rcAssertNothingLost(t *testing.T, l *rtLayout, baseline rcTree, tx string) {
	t.Helper()
	for _, f := range Targets(l.paths) {
		orig, existed := baseline.files[f]
		if !existed {
			continue
		}
		want := rtSum(orig.data)
		if rcSumAt(f) != want && rcSumAt(AsideName(f, tx)) != want {
			t.Errorf("the original %s is neither in place nor at its set-aside name", filepath.Base(f))
		}
	}
}

// rcAssertAsideBeforeInstall checks that a journal in phase 6 or later is
// never written while an original is still in place.
func rcAssertAsideBeforeInstall(t *testing.T, l *rtLayout) {
	t.Helper()
	js, err := ListJournals(l.paths.ControllerDir)
	if err != nil || len(js) != 1 {
		return
	}
	j, err := ReadJournal(js[0])
	if err != nil || (j.Phase != PhaseInstalled && j.Phase != PhaseVerified) {
		return
	}
	for _, f := range j.Files {
		if f.Existed && !rcExists(AsideName(f.Path, j.Tx)) {
			t.Errorf("the journal is in phase %s but %s is not at its set-aside name", j.Phase, filepath.Base(f.Path))
		}
	}
}

// rcAssertOriginals checks the layout equals baseline. With tolerateNew, the
// restore-<tx>.journal.new file of a crash inside the transaction's very
// first journal save is ignored: no journal names it, so nothing can
// attribute it.
func rcAssertOriginals(t *testing.T, l *rtLayout, baseline rcTree, tx string, tolerateNew bool) {
	t.Helper()
	got := l.digest(t)
	if tolerateNew {
		delete(got, JournalPath(l.paths.ControllerDir, tx)+".new")
	}
	if diff := rtDiff(baseline.digest, got); diff != "" {
		t.Errorf("the installation is not the original one: %s", diff)
	}
	if js, err := ListJournals(l.paths.ControllerDir); err != nil || len(js) != 0 {
		t.Errorf("journals left: %v (error %v)", js, err)
	}
}

// rcAssertRestored checks the layout holds transaction tx's restore of bk on
// top of baseline: the backup's key and quarantined database installed,
// baseline's files at their .pre-<tx> names, and no other name carrying tx.
func rcAssertRestored(t *testing.T, l *rtLayout, bk rtBackup, baseline rcTree, tx string) {
	t.Helper()
	p := l.paths
	if !bytes.Equal(rtRead(t, p.ControllerKey), bk.key) {
		t.Error("the installed key is not the backup's key")
	}
	db := rtOpenRO(t, p.ControllerDB())
	if got := rtMarker(t, db).Tx; got != tx {
		t.Errorf("installed marker tx = %q, want %q", got, tx)
	}
	if got := rtMeta(t, db, "rt_marker"); got != "backup" {
		t.Errorf("installed database rt_marker = %q, want the backup's", got)
	}
	var wantNames []string
	for _, f := range Targets(p) {
		orig, existed := baseline.files[f]
		if !existed {
			continue
		}
		wantNames = append(wantNames, filepath.Base(AsideName(f, tx)))
		if rcSumAt(AsideName(f, tx)) != rtSum(orig.data) {
			t.Errorf("%s does not hold the original %s", filepath.Base(AsideName(f, tx)), filepath.Base(f))
		}
	}
	slices.Sort(wantNames)
	if got := rcTxNames(t, l, tx); !slices.Equal(got, wantNames) {
		t.Errorf("files carrying the transaction id = %v, want only %v", got, wantNames)
	}
	for _, f := range []string{p.ControllerDB(), p.ControllerKey} {
		if info, err := os.Stat(f); err != nil || info.Mode().Perm() != 0o600 {
			t.Errorf("%s: mode %v, error %v, want 0600", filepath.Base(f), info, err)
		}
	}
	if js, err := ListJournals(p.ControllerDir); err != nil || len(js) != 0 {
		t.Errorf("journals left: %v (error %v)", js, err)
	}
}

// rcAssertEnd checks the end state a recovery must reach.
func rcAssertEnd(t *testing.T, l *rtLayout, bk rtBackup, baseline rcTree, tx string, restored bool) {
	t.Helper()
	if restored {
		rcAssertRestored(t, l, bk, baseline, tx)
		return
	}
	rcAssertOriginals(t, l, baseline, tx, true)
}

// rcOrderOps records the removals in order.
type rcOrderOps struct {
	Ops
	mu      sync.Mutex
	removed []string
}

func (o *rcOrderOps) Remove(path string) error {
	o.mu.Lock()
	o.removed = append(o.removed, path)
	o.mu.Unlock()
	return o.Ops.Remove(path)
}

func TestRcWithoutAJournalTakesNoLock(t *testing.T) {
	tx := rtTx(1)
	l := rtNewLayout(t, tx)
	release, err := l.host.lock()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(release)

	before := l.digest(t)
	if err := Recover(context.Background(), l.host); err != nil {
		t.Fatalf("Recover with no journal while the lock is held elsewhere: %v", err)
	}
	if diff := rtDiff(before, l.digest(t)); diff != "" {
		t.Errorf("Recover with no journal changed the installation: %s", diff)
	}

	if err := SaveJournal(OSOps(), JournalPath(l.paths.ControllerDir, tx), Journal{Format: 1, Tx: tx, Phase: PhaseBegun}); err != nil {
		t.Fatal(err)
	}
	before = l.digest(t)
	err = Recover(context.Background(), l.host)
	if err == nil || !strings.Contains(err.Error(), "another restore or recovery holds") {
		t.Fatalf("Recover with a journal while the lock is held = %v, want the lock refusal", err)
	}
	if diff := rtDiff(before, l.digest(t)); diff != "" {
		t.Errorf("a refused Recover changed the installation: %s", diff)
	}
}

func TestRcRollsBackBeforePhaseSix(t *testing.T) {
	tx := rtTx(1)
	l := rtNewLayout(t, tx)
	bk := rtMakeBackup(t)
	pristine := rcSave(t, l)
	points := rcTimeline(t, l, bk, pristine)
	at := rcFind(t, points, func(j Journal) bool { return j.Phase == PhaseMovedAside && rcDone(j, PhaseMovedAside) == 1 })

	rcCrashRestore(t, l, bk, at, false)
	if !rcExists(AsideName(l.paths.ControllerDB(), tx)) || rcExists(l.paths.ControllerDB()) {
		t.Fatal("the crash did not leave the database at its set-aside name")
	}

	if err := Recover(context.Background(), l.host); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	rcAssertOriginals(t, l, pristine, tx, false)
	if err := Recover(context.Background(), l.host); err != nil {
		t.Fatalf("second Recover: %v", err)
	}
	rcAssertOriginals(t, l, pristine, tx, false)
}

func TestRcCompletesFromPhaseSixWithoutCheckingServices(t *testing.T) {
	tx := rtTx(1)
	l := rtNewLayout(t, tx)
	bk := rtMakeBackup(t)
	pristine := rcSave(t, l)
	points := rcTimeline(t, l, bk, pristine)
	at := rcFind(t, points, func(j Journal) bool { return j.Phase == PhaseInstalled && rcDone(j, PhaseInstalled) == 1 })

	rcCrashRestore(t, l, bk, at, false)
	if !rcExists(StagedName(l.paths.ControllerKey, tx)) || rcExists(StagedName(l.paths.ControllerDB(), tx)) {
		t.Fatal("the crash did not leave the database installed and the key staged")
	}
	l.units.active[rtControllerUnit] = "active"
	l.units.active[rtAgentUnit] = "active"

	if err := Recover(context.Background(), l.host); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	rcAssertRestored(t, l, bk, pristine, tx)
	settled := l.digest(t)
	if err := Recover(context.Background(), l.host); err != nil {
		t.Fatalf("second Recover: %v", err)
	}
	if diff := rtDiff(settled, l.digest(t)); diff != "" {
		t.Errorf("a second Recover changed the installation: %s", diff)
	}
}

func TestRcFailedVerificationKeepsTheJournal(t *testing.T) {
	tx := rtTx(1)
	l := rtNewLayout(t, tx)
	bk := rtMakeBackup(t)
	pristine := rcSave(t, l)
	points := rcTimeline(t, l, bk, pristine)
	at := rcFind(t, points, func(j Journal) bool { return j.Phase == PhaseInstalled && rcDone(j, PhaseInstalled) == 2 })
	rcCrashRestore(t, l, bk, at, false)

	// A database made by hand carries no restore_quarantine flag.
	other := fxDB(t, t.TempDir())
	if err := other.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(l.paths.ControllerDB(), rtRead(t, other.Path()), 0o600); err != nil {
		t.Fatal(err)
	}

	for i := range 2 {
		err := Recover(context.Background(), l.host)
		if !errors.Is(err, ErrNotQuarantined) {
			t.Fatalf("Recover %d = %v, want ErrNotQuarantined", i+1, err)
		}
		js, lerr := ListJournals(l.paths.ControllerDir)
		if lerr != nil || len(js) != 1 {
			t.Fatalf("journals after the failed verification = %v (error %v), want the journal kept", js, lerr)
		}
		for _, f := range Targets(l.paths) {
			if _, existed := pristine.files[f]; existed && !rcExists(AsideName(f, tx)) {
				t.Errorf("%s was removed although verification failed", filepath.Base(AsideName(f, tx)))
			}
		}
	}
}

func TestRcAmbiguousRenameKeepsTheJournals(t *testing.T) {
	tx1, tx2 := rtTx(1), rtTx(2)
	l := rtNewLayout(t, tx1)
	p := l.paths
	aside := AsideName(p.ControllerDB(), tx1)
	if err := os.WriteFile(aside, []byte("set aside"), 0o600); err != nil {
		t.Fatal(err)
	}
	// The database and its set-aside name both exist: no one can tell which
	// is the original.
	first := Journal{
		Format: 1, Tx: tx1, Phase: PhaseMovedAside,
		Files: []JournalFile{
			{Path: p.ControllerDB(), Existed: true}, {Path: p.ControllerDB() + "-wal", Existed: true},
			{Path: p.ControllerDB() + "-shm", Existed: true}, {Path: p.ControllerKey, Existed: true},
		},
		Renames: []Rename{{Phase: PhaseMovedAside, From: p.ControllerDB(), To: aside, State: RenameIntended}},
	}
	firstPath := JournalPath(p.ControllerDir, tx1)
	if err := SaveJournal(OSOps(), firstPath, first); err != nil {
		t.Fatal(err)
	}
	// A younger transaction that recovery must not reach after the first fails.
	staged := StagedName(p.ControllerKey, tx2)
	if err := os.WriteFile(staged, []byte("staged"), 0o600); err != nil {
		t.Fatal(err)
	}
	secondPath := JournalPath(p.ControllerDir, tx2)
	if err := SaveJournal(OSOps(), secondPath, Journal{Format: 1, Tx: tx2, Phase: PhaseBegun}); err != nil {
		t.Fatal(err)
	}
	dbSum := rcSumAt(p.ControllerDB())

	err := Recover(context.Background(), l.host)
	if !errors.Is(err, ErrAmbiguousRename) {
		t.Fatalf("Recover = %v, want ErrAmbiguousRename", err)
	}
	for _, want := range []string{firstPath, aside, p.ControllerDB()} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not name %s: %v", want, err)
		}
	}
	js, lerr := ListJournals(p.ControllerDir)
	if lerr != nil || !slices.Equal(js, []string{firstPath, secondPath}) {
		t.Errorf("journals = %v (error %v), want both kept", js, lerr)
	}
	if rcSumAt(p.ControllerDB()) != dbSum || rcSumAt(aside) != rtSum([]byte("set aside")) {
		t.Error("recovery touched the two files whose rename it could not settle")
	}
	if !rcExists(staged) {
		t.Error("recovery went on to the second transaction after the first failed")
	}
}

func TestRcProcessesJournalsOldestFirst(t *testing.T) {
	tx1, tx2 := rtTx(1), rtTx(2)
	l := rtNewLayout(t, tx1)
	p := l.paths
	pristine := l.digest(t)
	for _, tx := range []string{tx2, tx1} {
		if err := os.WriteFile(StagedName(p.ControllerKey, tx), []byte("staged"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := SaveJournal(OSOps(), JournalPath(p.ControllerDir, tx), Journal{Format: 1, Tx: tx, Phase: PhaseBegun}); err != nil {
			t.Fatal(err)
		}
	}
	ops := &rcOrderOps{Ops: OSOps()}
	h := l.host
	h.Ops = ops

	if err := Recover(context.Background(), h); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if diff := rtDiff(pristine, l.digest(t)); diff != "" {
		t.Errorf("both transactions should be rolled back to the installation: %s", diff)
	}
	last1, first2 := -1, -1
	for i, path := range ops.removed {
		if strings.Contains(path, tx1) {
			last1 = i
		}
		if strings.Contains(path, tx2) && first2 < 0 {
			first2 = i
		}
	}
	if last1 < 0 || first2 < 0 || last1 > first2 {
		t.Errorf("removals of the older transaction end at %d, the younger begin at %d; want older first (%v)", last1, first2, ops.removed)
	}
}
