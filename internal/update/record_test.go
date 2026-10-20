package update

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/paths"
)

// recordPaths returns a layout under a temporary root with UpdateDir created
// as the agent creates it.
func recordPaths(t *testing.T) paths.Paths {
	t.Helper()
	p := paths.Under(t.TempDir())
	if err := os.MkdirAll(p.UpdateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	return p
}

var recordTime = time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)

// testRecord returns a pending update record started at recordTime plus
// offset.
func testRecord(id, op string, offset time.Duration) Record {
	return Record{
		ID: id, OperationID: op, Kind: core.UpdateKindUpdate,
		FromVersion: "1.2.0", ToVersion: "1.3.0", TargetVersion: "1.3.0",
		State: core.UpdatePending, StartedAt: recordTime.Add(offset), UpdatedAt: recordTime.Add(offset),
		Steps: []Release{{Version: "1.3.0", Channel: "stable"}},
	}
}

func TestRecordRoundTrip(t *testing.T) {
	p := recordPaths(t)
	parent := "01PARENT"
	deadline := recordTime.Add(80 * time.Second)
	r := testRecord("01TX", "01OP", 0)
	r.ParentID = &parent
	r.State = core.UpdateSwapped
	r.Error = &core.ErrorDetail{Code: "update.interrupted", Message: "the runner stopped"}
	r.ChangeID = "01CHANGE"
	r.Deadline = &deadline
	r.PrevSHA256, r.NewSHA256 = "aa", "bb"
	if err := WriteRecord(p, r); err != nil {
		t.Fatal(err)
	}
	got, err := ReadRecord(p, "01TX")
	if err != nil {
		t.Fatal(err)
	}
	r.FormatVersion = RecordFormat
	if !reflect.DeepEqual(got, r) {
		t.Fatalf("ReadRecord = %+v, want %+v", got, r)
	}
	info, err := os.Stat(p.UpdateRecord("01TX"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("record mode = %v, %v; want 0600", info.Mode(), err)
	}
	entries, _ := os.ReadDir(p.UpdateDir)
	if len(entries) != 1 {
		t.Errorf("UpdateDir holds %d entries, want only the record", len(entries))
	}
	raw, _ := os.ReadFile(p.UpdateRecord("01TX"))
	var members map[string]json.RawMessage
	if err := json.Unmarshal(raw, &members); err != nil {
		t.Fatal(err)
	}
	var names []string
	for k := range members {
		names = append(names, k)
	}
	slices.Sort(names)
	want := []string{"changeId", "deadline", "error", "formatVersion", "fromVersion", "id", "kind", "newSha256",
		"operationId", "parentId", "prevSha256", "startedAt", "state", "steps", "targetVersion", "toVersion", "updatedAt"}
	if !reflect.DeepEqual(names, want) {
		t.Errorf("record members = %v, want %v", names, want)
	}
	wantTx := core.UpdateTransaction{
		ID: "01TX", OperationID: "01OP", ParentID: &parent, Kind: core.UpdateKindUpdate,
		FromVersion: "1.2.0", ToVersion: "1.3.0", TargetVersion: "1.3.0", State: core.UpdateSwapped,
		Error: r.Error, StartedAt: recordTime, UpdatedAt: recordTime,
	}
	if tx := got.Transaction(); !reflect.DeepEqual(tx, wantTx) {
		t.Errorf("Transaction = %+v, want %+v", tx, wantTx)
	}
}

func TestReadRecordFormats(t *testing.T) {
	p := recordPaths(t)
	write := func(id, body string) {
		t.Helper()
		if err := os.WriteFile(p.UpdateRecord(id), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// A newer binary of the same major adds members only (ADR 0004); this
	// binary must still read its records during a rollback.
	write("NEWER", `{"formatVersion": 2, "id": "NEWER", "state": "swapped", "addedLater": {"x": 1}}`)
	if r, err := ReadRecord(p, "NEWER"); err != nil || r.State != core.UpdateSwapped {
		t.Errorf("a newer additive record = %+v, %v", r, err)
	}
	write("NOFORMAT", `{"id": "NOFORMAT", "state": "swapped"}`)
	if _, err := ReadRecord(p, "NOFORMAT"); err == nil {
		t.Error("a record without a format version was accepted")
	}
	write("OTHER", `{"formatVersion": 1, "id": "ELSE"}`)
	if _, err := ReadRecord(p, "OTHER"); err == nil {
		t.Error("a record naming another id was accepted")
	}
	write("BROKEN", `{"formatVersion": 1,`)
	if _, err := ReadRecord(p, "BROKEN"); err == nil {
		t.Error("a truncated record was accepted")
	}
	if _, err := ReadRecord(p, "MISSING"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a missing record = %v, want fs.ErrNotExist", err)
	}
	for _, id := range []string{"", "../x", "a.b", "a/b"} {
		if _, err := ReadRecord(p, id); err == nil {
			t.Errorf("ReadRecord(%q) was accepted", id)
		}
		if err := WriteRecord(p, Record{ID: id}); err == nil {
			t.Errorf("WriteRecord(%q) was accepted", id)
		}
	}
}

func TestListRecords(t *testing.T) {
	p := paths.Under(t.TempDir())
	if all, err := ListRecords(p); err != nil || len(all) != 0 {
		t.Fatalf("ListRecords without UpdateDir = %v, %v", all, err)
	}
	p = recordPaths(t)
	for _, r := range []Record{
		testRecord("C", "OP2", time.Minute),
		testRecord("B", "OP1", 0),
		testRecord("A", "OP1", 0),
		testRecord("D", "OP1", 2*time.Minute),
	} {
		if err := WriteRecord(p, r); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"lock", ".E.json.tmp", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(p.UpdateDir, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	all, err := ListRecords(p)
	if err != nil {
		t.Fatal(err)
	}
	ids := func(rs []Record) []string {
		var out []string
		for _, r := range rs {
			out = append(out, r.ID)
		}
		return out
	}
	if got := ids(all); !reflect.DeepEqual(got, []string{"A", "B", "C", "D"}) {
		t.Errorf("ListRecords = %v", got)
	}
	if got := ids(OperationRecords(all, "OP1")); !reflect.DeepEqual(got, []string{"A", "B", "D"}) {
		t.Errorf("OperationRecords = %v", got)
	}
	if err := os.WriteFile(p.UpdateRecord("Z"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ListRecords(p); err == nil {
		t.Error("ListRecords skipped an unreadable record")
	}
}

func TestFinished(t *testing.T) {
	step := func(state, to, target string) Record {
		r := testRecord("X", "OP", 0)
		r.State, r.ToVersion, r.TargetVersion = state, to, target
		return r
	}
	rollback := func(state string) Record {
		r := step(state, "1.2.0", "1.2.0")
		r.Kind = core.UpdateKindRollback
		return r
	}
	tests := []struct {
		name string
		op   []Record
		want bool
	}{
		{"no record", nil, false},
		{"pending", []Record{step(core.UpdatePending, "1.3.0", "1.3.0")}, false},
		{"swapped", []Record{step(core.UpdateSwapped, "1.3.0", "1.3.0")}, false},
		{"rolling back", []Record{step(core.UpdateRollingBack, "1.3.0", "1.3.0")}, false},
		{"committed final step", []Record{step(core.UpdateCommitted, "1.3.0", "1.3.0")}, true},
		{"committed intermediate step", []Record{step(core.UpdateCommitted, "1.3.0", "1.5.0")}, false},
		{"intermediate committed, next pending", []Record{
			step(core.UpdateCommitted, "1.3.0", "1.5.0"), step(core.UpdatePending, "1.4.0", "1.5.0")}, false},
		{"stepped to the target", []Record{
			step(core.UpdateCommitted, "1.3.0", "1.5.0"), step(core.UpdateCommitted, "1.4.0", "1.5.0"),
			step(core.UpdateCommitted, "1.5.0", "1.5.0")}, true},
		{"failed step after a commit", []Record{
			step(core.UpdateCommitted, "1.3.0", "1.5.0"), step(core.UpdateFailed, "1.4.0", "1.5.0")}, true},
		{"rolled back", []Record{step(core.UpdateRolledBack, "1.3.0", "1.3.0")}, true},
		{"rollback pending", []Record{rollback(core.UpdatePending)}, false},
		{"rollback rolling back", []Record{rollback(core.UpdateRollingBack)}, false},
		{"rollback done", []Record{rollback(core.UpdateRolledBack)}, true},
		{"rollback failed", []Record{rollback(core.UpdateFailed)}, true},
	}
	for _, tt := range tests {
		if got := Finished(tt.op); got != tt.want {
			t.Errorf("%s: Finished = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestAcquireLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")
	ctx := context.Background()
	first, err := AcquireLock(ctx, path, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("lock file mode = %v, %v; want 0600", info.Mode(), err)
	}

	start := time.Now()
	if _, err := AcquireLock(ctx, path, 100*time.Millisecond); !errors.Is(err, ErrLockTimeout) {
		t.Fatalf("a second AcquireLock = %v, want ErrLockTimeout", err)
	}
	if elapsed := time.Since(start); elapsed < 100*time.Millisecond {
		t.Errorf("AcquireLock gave up after %v, before its timeout", elapsed)
	}

	cancelled, cancel := context.WithCancel(ctx)
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	if _, err := AcquireLock(cancelled, path, time.Minute); !errors.Is(err, context.Canceled) {
		t.Fatalf("AcquireLock after cancel = %v, want context.Canceled", err)
	}

	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	second, err := AcquireLock(ctx, path, time.Second)
	if err != nil {
		t.Fatalf("AcquireLock after Release: %v", err)
	}
	if err := second.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestAcquireLockWaitsForRelease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")
	first, err := AcquireLock(context.Background(), path, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(50 * time.Millisecond)
		_ = first.Release()
	}()
	second, err := AcquireLock(context.Background(), path, 5*time.Second)
	if err != nil {
		t.Fatalf("AcquireLock while the holder releases: %v", err)
	}
	_ = second.Release()
}
