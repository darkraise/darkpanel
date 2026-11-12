package backup

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/paths"
)

const jnTx = "01JTXAAAAAAAAAAAAAAAAAAAAA"

// jnWrite creates path with content, and its directory when missing.
func jnWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// jnExists reports whether path exists, without following a symbolic link.
func jnExists(t *testing.T, path string) bool {
	t.Helper()
	ok, err := OSOps().Exists(path)
	if err != nil {
		t.Fatal(err)
	}
	return ok
}

// jnRecorder logs the calls of the Ops and Files that order a journal write,
// with paths relative to root, and then runs them on the wrapped Ops.
type jnRecorder struct {
	Ops
	root string
	log  []string
}

func (r *jnRecorder) note(format string, path string, more ...any) {
	rel, err := filepath.Rel(r.root, path)
	if err != nil {
		rel = path
	}
	r.log = append(r.log, fmt.Sprintf(format, append([]any{rel}, more...)...))
}

func (r *jnRecorder) Create(path string, perm fs.FileMode) (File, error) {
	r.note("create %s %04o", path, perm)
	f, err := r.Ops.Create(path, perm)
	if err != nil {
		return nil, err
	}
	return &jnRecFile{File: f, r: r, path: path}, nil
}

func (r *jnRecorder) Rename(from, to string) error {
	rel, err := filepath.Rel(r.root, to)
	if err != nil {
		rel = to
	}
	r.note("rename %s %s", from, rel)
	return r.Ops.Rename(from, to)
}

func (r *jnRecorder) Remove(path string) error {
	r.note("remove %s", path)
	return r.Ops.Remove(path)
}

func (r *jnRecorder) SyncDir(dir string) error {
	r.note("syncdir %s", dir)
	return r.Ops.SyncDir(dir)
}

type jnRecFile struct {
	File
	r    *jnRecorder
	path string
}

func (f *jnRecFile) Sync() error {
	f.r.note("sync %s", f.path)
	return f.File.Sync()
}

func (f *jnRecFile) Close() error {
	f.r.note("close %s", f.path)
	return f.File.Close()
}

// jnSaveSteps is what one SaveJournal of the journal at rel does, in order.
func jnSaveSteps(rel string) []string {
	return []string{
		"remove " + rel + ".new",
		"create " + rel + ".new 0600",
		"sync " + rel + ".new",
		"close " + rel + ".new",
		"rename " + rel + ".new " + rel,
		"syncdir " + filepath.Dir(rel),
	}
}

func jnSample() Journal {
	return Journal{
		Format: 1, Tx: jnTx, Phase: PhaseMovedAside,
		Files: []JournalFile{
			{Path: "/var/lib/darkpanel/controller/controller.db", Existed: true},
			{Path: "/var/lib/darkpanel/controller/controller.db-wal", Existed: false},
			{Path: "/var/lib/darkpanel/controller/controller.db-shm", Existed: false},
			{Path: "/etc/darkpanel/controller.key", Existed: true},
		},
		Renames: []Rename{
			{Phase: PhaseMovedAside, From: "/a/controller.db", To: "/a/controller.db.pre-" + jnTx, State: RenameDone},
			{Phase: PhaseMovedAside, From: "/k/controller.key", To: "/k/controller.key.pre-" + jnTx, State: RenameIntended},
		},
	}
}

func TestJnNames(t *testing.T) {
	p := paths.Under("/r")
	targets := Targets(p)
	want := []string{"/r/controller/controller.db", "/r/controller/controller.db-wal", "/r/controller/controller.db-shm",
		"/r/keys/controller.key"}
	if !slices.Equal(targets, want) {
		t.Fatalf("Targets = %v, want %v", targets, want)
	}
	for _, f := range targets {
		if got, want := StagedName(f, jnTx), f+".restore-"+jnTx; got != want {
			t.Errorf("StagedName(%s) = %s, want %s", f, got, want)
		}
		if got, want := AsideName(f, jnTx), f+".pre-"+jnTx; got != want {
			t.Errorf("AsideName(%s) = %s, want %s", f, got, want)
		}
	}
	if got, want := JournalPath("/r/controller", jnTx), "/r/controller/restore-"+jnTx+".journal"; got != want {
		t.Errorf("JournalPath = %s, want %s", got, want)
	}
}

// TestJnWireValues pins the strings a journal stores, which a later binary
// must read.
func TestJnWireValues(t *testing.T) {
	phases := map[Phase]string{
		PhaseBegun: "begun", PhaseStaged: "staged", PhaseValidated: "validated", PhaseQuarantined: "quarantined",
		PhaseJournaled: "journaled", PhaseMovedAside: "moved-aside", PhaseInstalled: "installed",
		PhaseVerified: "verified", PhaseRollback: "rollback",
	}
	dir := t.TempDir()
	for phase, text := range phases {
		if string(phase) != text {
			t.Errorf("phase %q, want %q", phase, text)
		}
		path := filepath.Join(dir, "restore-"+text+".journal")
		jnWrite(t, path, `{"format":1,"tx":"T","phase":"`+text+`"}`)
		if j, err := ReadJournal(path); err != nil || j.Phase != phase {
			t.Errorf("ReadJournal with phase %s = %+v, %v", text, j, err)
		}
	}
	if RenameIntended != "intended" || RenameDone != "done" {
		t.Errorf("rename states %q and %q, want intended and done", RenameIntended, RenameDone)
	}
}

func TestJnSaveAndReadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := JournalPath(dir, jnTx)
	j := jnSample()
	if err := SaveJournal(OSOps(), path, j); err != nil {
		t.Fatal(err)
	}
	got, err := ReadJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, j) {
		t.Errorf("ReadJournal = %+v, want %+v", got, j)
	}
	if st, err := os.Stat(path); err != nil || st.Mode().Perm() != 0o600 {
		t.Errorf("journal mode = %v, %v; want 0600", st, err)
	}
	names, err := os.ReadDir(dir)
	if err != nil || len(names) != 1 {
		t.Errorf("directory holds %v, %v; want only the journal", names, err)
	}

	j.Phase = PhaseInstalled
	j.Format = 0
	if err := SaveJournal(OSOps(), path, j); err != nil {
		t.Fatal(err)
	}
	got, err = ReadJournal(path)
	if err != nil || got.Phase != PhaseInstalled || got.Format != 1 {
		t.Errorf("after the second save ReadJournal = %+v, %v; want phase installed and format 1", got, err)
	}
}

func TestJnSaveRefusesAnUnknownPhase(t *testing.T) {
	dir := t.TempDir()
	path := JournalPath(dir, jnTx)
	if err := SaveJournal(OSOps(), path, Journal{Format: 1, Tx: jnTx, Phase: "bogus"}); err == nil {
		t.Fatal("SaveJournal accepted an unknown phase")
	}
	names, _ := os.ReadDir(dir)
	if len(names) != 0 {
		t.Errorf("directory holds %v after a refused save, want nothing", names)
	}
}

func TestJnSaveReplacesAStaleNewFile(t *testing.T) {
	dir := t.TempDir()
	path := JournalPath(dir, jnTx)
	jnWrite(t, path+".new", "half a journal")
	if err := SaveJournal(OSOps(), path, jnSample()); err != nil {
		t.Fatalf("SaveJournal beside a stale .new file: %v", err)
	}
	if _, err := ReadJournal(path); err != nil {
		t.Errorf("ReadJournal: %v", err)
	}
	if jnExists(t, path+".new") {
		t.Error("the .new file is still there")
	}
}

func TestJnSaveOrdersTheDurabilitySteps(t *testing.T) {
	dir := t.TempDir()
	rec := &jnRecorder{Ops: OSOps(), root: dir}
	if err := SaveJournal(rec, JournalPath(dir, jnTx), jnSample()); err != nil {
		t.Fatal(err)
	}
	if want := jnSaveSteps("restore-" + jnTx + ".journal"); !slices.Equal(rec.log, want) {
		t.Errorf("SaveJournal did\n%s\nwant\n%s", strings.Join(rec.log, "\n"), strings.Join(want, "\n"))
	}
}

func TestJnReadRefusals(t *testing.T) {
	tests := []struct {
		name, content string
		ok            bool
	}{
		{"minimal", `{"format":1,"tx":"T","phase":"begun"}`, true},
		{"format 2", `{"format":2,"tx":"T","phase":"begun"}`, false},
		{"format 0", `{"tx":"T","phase":"begun"}`, false},
		{"unknown phase", `{"format":1,"tx":"T","phase":"bogus"}`, false},
		{"empty phase", `{"format":1,"tx":"T"}`, false},
		{"not json", `restore in progress`, false},
		{"empty file", ``, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "restore-T.journal")
			jnWrite(t, path, tt.content)
			j, err := ReadJournal(path)
			if tt.ok != (err == nil) {
				t.Fatalf("ReadJournal = %+v, %v; want ok %v", j, err, tt.ok)
			}
		})
	}
	_, err := ReadJournal(filepath.Join(t.TempDir(), "restore-missing.journal"))
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("ReadJournal of a missing file = %v, want an error wrapping fs.ErrNotExist", err)
	}
}

// TestJnSaveSurvivesACrashAtEveryCall: whichever call of SaveJournal fails
// and on whichever side, the journal on disk is the old one or the new one,
// never a torn file, and a later save succeeds.
func TestJnSaveSurvivesACrashAtEveryCall(t *testing.T) {
	oldJ := jnSample()
	oldJ.Phase = PhaseJournaled
	oldJ.Renames = nil
	newJ := jnSample()
	for _, before := range []bool{true, false} {
		for at := 1; ; at++ {
			if at > 40 {
				t.Fatalf("before=%v: SaveJournal still crashes at call 40", before)
			}
			dir := t.TempDir()
			path := JournalPath(dir, jnTx)
			if err := SaveJournal(OSOps(), path, oldJ); err != nil {
				t.Fatal(err)
			}
			c := newCrashOps(OSOps(), at, before)
			err := SaveJournal(c, path, newJ)
			if !c.Crashed() {
				if err != nil {
					t.Fatalf("before=%v at=%d: no crash fired but SaveJournal = %v", before, at, err)
				}
				if at < 6 {
					t.Fatalf("SaveJournal made only %d calls, want at least its 6 durability steps", at-1)
				}
				break
			}
			if !errors.Is(err, errCrash) {
				t.Fatalf("before=%v at=%d: SaveJournal = %v, want the simulated crash", before, at, err)
			}
			got, rerr := ReadJournal(path)
			if rerr != nil {
				t.Fatalf("before=%v at=%d: the journal is unreadable after the crash: %v", before, at, rerr)
			}
			if !reflect.DeepEqual(got, oldJ) && !reflect.DeepEqual(got, newJ) {
				t.Fatalf("before=%v at=%d: journal %+v is neither the old nor the new one", before, at, got)
			}
			if err := SaveJournal(OSOps(), path, newJ); err != nil {
				t.Fatalf("before=%v at=%d: the save after the crash: %v", before, at, err)
			}
			if got, err := ReadJournal(path); err != nil || !reflect.DeepEqual(got, newJ) {
				t.Fatalf("before=%v at=%d: after the second save ReadJournal = %+v, %v", before, at, got, err)
			}
		}
	}
}

func TestJnListJournals(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{
		"restore-01B.journal", "restore-01A.journal", "restore-01A.journal.new", "restore-01C.journal.bak",
		"controller.db", "journal", "restore.journal.d",
	} {
		jnWrite(t, filepath.Join(dir, name), "x")
	}
	got, err := ListJournals(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{filepath.Join(dir, "restore-01A.journal"), filepath.Join(dir, "restore-01B.journal")}
	if !slices.Equal(got, want) {
		t.Errorf("ListJournals = %v, want %v", got, want)
	}
	if got, err := ListJournals(filepath.Join(dir, "missing")); err != nil || len(got) != 0 {
		t.Errorf("ListJournals of a missing directory = %v, %v; want nothing and no error", got, err)
	}
}

func TestJnRenameJournaledOrdersIntentRenameAndDone(t *testing.T) {
	root := t.TempDir()
	journal := JournalPath(filepath.Join(root, "j"), jnTx)
	from, to := filepath.Join(root, "a", "controller.db"), filepath.Join(root, "b", "controller.db.pre-"+jnTx)
	jnWrite(t, from, "database")
	if err := os.MkdirAll(filepath.Dir(to), 0o700); err != nil {
		t.Fatal(err)
	}
	j := Journal{Format: 1, Tx: jnTx, Phase: PhaseMovedAside}
	if err := os.MkdirAll(filepath.Dir(journal), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := SaveJournal(OSOps(), journal, j); err != nil {
		t.Fatal(err)
	}

	rec := &jnRecorder{Ops: OSOps(), root: root}
	if err := RenameJournaled(rec, journal, &j, PhaseMovedAside, from, to); err != nil {
		t.Fatal(err)
	}
	rel := filepath.Join("j", "restore-"+jnTx+".journal")
	want := slices.Concat(jnSaveSteps(rel), []string{"rename a/controller.db b/controller.db.pre-" + jnTx, "syncdir b"}, jnSaveSteps(rel))
	if !slices.Equal(rec.log, want) {
		t.Errorf("RenameJournaled did\n%s\nwant\n%s", strings.Join(rec.log, "\n"), strings.Join(want, "\n"))
	}
	if jnExists(t, from) || !jnExists(t, to) {
		t.Errorf("after the rename %s exists %v, %s exists %v; want moved", from, jnExists(t, from), to, jnExists(t, to))
	}
	wantRenames := []Rename{{Phase: PhaseMovedAside, From: from, To: to, State: RenameDone}}
	if !reflect.DeepEqual(j.Renames, wantRenames) {
		t.Errorf("j.Renames = %+v, want %+v", j.Renames, wantRenames)
	}
	if got, err := ReadJournal(journal); err != nil || !reflect.DeepEqual(got.Renames, wantRenames) {
		t.Errorf("saved renames = %+v, %v; want %+v", got.Renames, err, wantRenames)
	}

	second := filepath.Join(root, "a", "controller.key")
	jnWrite(t, second, "key")
	if err := RenameJournaled(OSOps(), journal, &j, PhaseInstalled, second, filepath.Join(root, "b", "controller.key")); err != nil {
		t.Fatal(err)
	}
	if got, err := ReadJournal(journal); err != nil || len(got.Renames) != 2 || got.Renames[1].Phase != PhaseInstalled ||
		got.Renames[1].State != RenameDone {
		t.Errorf("saved renames after the second = %+v, %v; want two, the second installed and done", got.Renames, err)
	}
}

// TestJnRenameJournaledKeepsTheIntentWhenTheRenameFails: a rename that
// fails leaves the journal naming it as intended, which is what recovery
// settles from the names that exist.
func TestJnRenameJournaledKeepsTheIntentWhenTheRenameFails(t *testing.T) {
	dir := t.TempDir()
	journal := JournalPath(dir, jnTx)
	j := Journal{Format: 1, Tx: jnTx, Phase: PhaseMovedAside}
	if err := SaveJournal(OSOps(), journal, j); err != nil {
		t.Fatal(err)
	}
	from, to := filepath.Join(dir, "missing"), filepath.Join(dir, "target")
	if err := RenameJournaled(OSOps(), journal, &j, PhaseMovedAside, from, to); err == nil {
		t.Fatal("RenameJournaled of a missing file succeeded")
	}
	want := []Rename{{Phase: PhaseMovedAside, From: from, To: to, State: RenameIntended}}
	if got, err := ReadJournal(journal); err != nil || !reflect.DeepEqual(got.Renames, want) {
		t.Errorf("saved renames = %+v, %v; want %+v", got.Renames, err, want)
	}
}

// TestJnRenameJournaledSurvivesACrashAtEveryCall: after a crash at any call,
// on either side, the journal reads, exactly one of the two names exists, a
// rename that happened is in the journal, and an intended entry settles to
// what happened.
func TestJnRenameJournaledSurvivesACrashAtEveryCall(t *testing.T) {
	for _, before := range []bool{true, false} {
		for at := 1; ; at++ {
			if at > 60 {
				t.Fatalf("before=%v: RenameJournaled still crashes at call 60", before)
			}
			root := t.TempDir()
			journal := JournalPath(filepath.Join(root, "j"), jnTx)
			from, to := filepath.Join(root, "a", "controller.db"), filepath.Join(root, "b", "controller.db.pre-"+jnTx)
			jnWrite(t, from, "database")
			if err := os.MkdirAll(filepath.Dir(to), 0o700); err != nil {
				t.Fatal(err)
			}
			j := Journal{Format: 1, Tx: jnTx, Phase: PhaseMovedAside}
			if err := os.MkdirAll(filepath.Dir(journal), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := SaveJournal(OSOps(), journal, j); err != nil {
				t.Fatal(err)
			}
			c := newCrashOps(OSOps(), at, before)
			err := RenameJournaled(c, journal, &j, PhaseMovedAside, from, to)
			if !c.Crashed() {
				if err != nil {
					t.Fatalf("before=%v at=%d: no crash fired but RenameJournaled = %v", before, at, err)
				}
				break
			}
			if !errors.Is(err, errCrash) {
				t.Fatalf("before=%v at=%d: RenameJournaled = %v, want the simulated crash", before, at, err)
			}
			got, rerr := ReadJournal(journal)
			if rerr != nil {
				t.Fatalf("before=%v at=%d: the journal is unreadable: %v", before, at, rerr)
			}
			fromHere, toHere := jnExists(t, from), jnExists(t, to)
			if fromHere == toHere {
				t.Fatalf("before=%v at=%d: from exists %v and to exists %v; want exactly one", before, at, fromHere, toHere)
			}
			if toHere && len(got.Renames) != 1 {
				t.Fatalf("before=%v at=%d: the rename ran but the journal holds %+v", before, at, got.Renames)
			}
			if len(got.Renames) == 0 {
				continue
			}
			entry := got.Renames[0]
			if entry.Phase != PhaseMovedAside || entry.From != from || entry.To != to {
				t.Fatalf("before=%v at=%d: journal entry %+v does not name the rename", before, at, entry)
			}
			switch entry.State {
			case RenameDone:
				if !toHere {
					t.Fatalf("before=%v at=%d: the entry is done but the rename did not run", before, at)
				}
			case RenameIntended:
				done, serr := Settle(OSOps(), entry)
				if serr != nil || done != toHere {
					t.Fatalf("before=%v at=%d: Settle = %v, %v; want %v", before, at, done, serr, toHere)
				}
			default:
				t.Fatalf("before=%v at=%d: entry state %q", before, at, entry.State)
			}
		}
	}
}

func TestJnSettle(t *testing.T) {
	tests := []struct {
		name         string
		from, to     bool
		want, wantOK bool
	}{
		{"only from: not done", true, false, false, true},
		{"only to: done", false, true, true, true},
		{"both: ambiguous", true, true, false, false},
		{"neither: ambiguous", false, false, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			r := Rename{Phase: PhaseInstalled, From: filepath.Join(dir, "from"), To: filepath.Join(dir, "to"), State: RenameIntended}
			if tt.from {
				jnWrite(t, r.From, "x")
			}
			if tt.to {
				jnWrite(t, r.To, "x")
			}
			done, err := Settle(OSOps(), r)
			if tt.wantOK {
				if err != nil || done != tt.want {
					t.Fatalf("Settle = %v, %v; want %v", done, err, tt.want)
				}
				return
			}
			if !errors.Is(err, ErrAmbiguousRename) {
				t.Fatalf("Settle = %v, %v; want ErrAmbiguousRename", done, err)
			}
		})
	}
}

// ReadJournal reads the journal at path; a format other than 1 or an unknown phase is an error.
func ReadJournal(path string) (Journal, error) {
	data, err := os.ReadFile(path) //nolint:gosec // G304: the path is a journal in the controller directory
	if err != nil {
		return Journal{}, fmt.Errorf("backup: read journal: %w", err)
	}
	return decodeJournal(path, data)
}
