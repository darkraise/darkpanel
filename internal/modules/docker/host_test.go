package docker

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/store"
)

var (
	_ core.Runner        = (*dkHost)(nil)
	_ core.FileWriter    = (*dkHost)(nil)
	_ core.EffectJournal = (*dkJournal)(nil)
)

// dkFile is one entry of the fake host's file system.
type dkFile struct {
	Mode    fs.FileMode
	UID     int
	Symlink bool
	Data    []byte
}

// dkInfo is the fs.FileInfo of a dkFile.
type dkInfo struct {
	name string
	f    dkFile
}

func (i dkInfo) Name() string { return i.name }
func (i dkInfo) Size() int64  { return int64(len(i.f.Data)) }
func (i dkInfo) Mode() fs.FileMode {
	if i.f.Symlink {
		return i.f.Mode | fs.ModeSymlink
	}
	return i.f.Mode
}
func (i dkInfo) ModTime() time.Time { return time.Time{} }
func (i dkInfo) IsDir() bool        { return i.Mode().IsDir() }
func (i dkInfo) Sys() any {
	return &syscall.Stat_t{Uid: uint32(i.f.UID)} //nolint:gosec // G115: the fake host's uids are small non-negative numbers.
}

// dkHost is the fake host a docker test runs on: a fake Docker daemon, a
// command runner, a file system and an agent database.
type dkHost struct {
	t       *testing.T
	clock   *coretest.FakeClock
	docker  *fakeDocker
	journal *dkJournal
	files   map[string]dkFile
	managed map[string][]byte
	cmds    []core.Cmd
	results map[string]core.CmdResult
	secrets [][]byte
	db      *store.DB
}

func newDKHost(t *testing.T) *dkHost {
	t.Helper()
	ctx := context.Background()
	start := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	db, err := store.Open(ctx, filepath.Join(dir, "agent.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	set, err := store.CoreMigrations(store.SideAgent)
	if err != nil {
		t.Fatal(err)
	}
	opts := store.MigrateOptions{
		Side: store.SideAgent, BinaryVersion: "test", BackupRoot: filepath.Join(dir, "backups"), Keep: 5, Now: start,
	}
	res, err := store.Migrate(ctx, db, opts, set, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Failed) > 0 {
		t.Fatalf("migrations failed: %+v", res.Failed)
	}
	clock := coretest.NewFakeClock(start)
	return &dkHost{
		t:       t,
		clock:   clock,
		docker:  newFakeDocker(clock),
		journal: &dkJournal{},
		files:   map[string]dkFile{},
		managed: map[string][]byte{},
		results: map[string]core.CmdResult{},
		db:      db,
	}
}

// Run records cmd and answers the result scripted for its arguments.
func (h *dkHost) Run(_ context.Context, cmd core.Cmd) (core.CmdResult, error) {
	h.cmds = append(h.cmds, cmd)
	return h.results[strings.Join(cmd.Args, " ")], nil
}

func (h *dkHost) WriteManaged(_ context.Context, _, dir, name string, data []byte, _ fs.FileMode, _, _ int) error {
	h.managed[dir+"/"+name] = bytes.Clone(data)
	return nil
}

func (h *dkHost) RemoveManaged(_ context.Context, _, dir, name string) error {
	delete(h.managed, dir+"/"+name)
	return nil
}

func (h *dkHost) WriteFile(context.Context, string, string, []byte, fs.FileMode) error {
	return errDKUnused
}

func (h *dkHost) ReplaceBlock(context.Context, string, string, string, []byte) error {
	return errDKUnused
}

func (h *dkHost) RemoveBlock(context.Context, string, string, string) error { return errDKUnused }

func (h *dkHost) Remove(context.Context, string, string) error { return errDKUnused }

func (h *dkHost) Restore(context.Context, string, core.RestoreEntry) error { return errDKUnused }

func (h *dkHost) ReadSysctl(context.Context, string, string) (string, error) {
	return "", errDKUnused
}

func (h *dkHost) WriteSysctl(context.Context, string, string, string) error { return errDKUnused }

func (h *dkHost) Rename(context.Context, string, string, string) error { return errDKUnused }

func (h *dkHost) EnsureServiceDir(context.Context, string, string) error { return errDKUnused }

func (h *dkHost) RenameIn(context.Context, string, string, string, string) error {
	return errDKUnused
}

func (h *dkHost) RemoveIn(context.Context, string, string, string) error { return errDKUnused }

func (h *dkHost) RemoveServiceDir(context.Context, string, string) error { return errDKUnused }

func (h *dkHost) Owns(context.Context, string, string) (bool, error) { return false, errDKUnused }

// dk wires the fakes into the module's dependencies.
func (h *dkHost) dk() dk {
	return dk{d: deps{
		docker: h.docker,
		lstat: func(name string) (fs.FileInfo, error) {
			f, ok := h.files[name]
			if !ok {
				return nil, &fs.PathError{Op: "lstat", Path: name, Err: fs.ErrNotExist}
			}
			return dkInfo{name: path.Base(name), f: f}, nil
		},
		readFile: func(name string) ([]byte, error) {
			f, ok := h.files[name]
			if !ok {
				return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
			}
			return bytes.Clone(f.Data), nil
		},
		secrets: func(context.Context, core.Env, string) ([][]byte, error) { return h.secrets, nil },
	}}
}

// env returns the environment of a call of the change; an empty changeID is
// a plan or a read, which has no effects journal.
func (h *dkHost) env(changeID string) core.Env {
	env := core.Env{
		HostID:    "h1",
		ChangeID:  changeID,
		Principal: core.Principal{Actor: "user:u1", Scope: core.ScopeAdmin},
		DB:        h.db,
		Exec:      h,
		Files:     h,
		Sealer:    coretest.Sealer{},
		Clock:     h.clock,
		Log:       slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	if changeID != "" {
		h.journal.mu.Lock()
		h.journal.change = changeID
		h.journal.mu.Unlock()
		env.Effects = h.journal
	}
	return env
}

// dkJournal is an in-memory core.EffectJournal.
type dkJournal struct {
	mu      sync.Mutex
	change  string
	list    []core.Effect
	secrets map[int][]byte
}

// effects returns a copy of the journaled effects.
func (j *dkJournal) effects() []core.Effect {
	j.mu.Lock()
	defer j.mu.Unlock()
	return slices.Clone(j.list)
}

func (j *dkJournal) Intend(_ context.Context, e core.Effect, secret []byte) (int, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	e.Seq = len(j.list) + 1
	e.State = core.EffectIntended
	e.ChangeID = j.change
	j.list = append(j.list, e)
	if secret != nil {
		if j.secrets == nil {
			j.secrets = map[int][]byte{}
		}
		j.secrets[e.Seq] = bytes.Clone(secret)
	}
	return e.Seq, nil
}

// intended returns the index of the effect seq when it is still intended.
func (j *dkJournal) intended(seq int) (int, error) {
	if seq < 1 || seq > len(j.list) {
		return 0, fmt.Errorf("fake: no effect %d", seq)
	}
	if st := j.list[seq-1].State; st != core.EffectIntended {
		return 0, fmt.Errorf("fake: effect %d is %s, not intended", seq, st)
	}
	return seq - 1, nil
}

func (j *dkJournal) Made(_ context.Context, seq int, resource string, post core.StateDoc) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	i, err := j.intended(seq)
	if err != nil {
		return err
	}
	j.list[i].State = core.EffectMade
	j.list[i].Resource = resource
	j.list[i].Post = &post
	return nil
}

func (j *dkJournal) Skipped(_ context.Context, seq int) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	i, err := j.intended(seq)
	if err != nil {
		return err
	}
	j.list[i].State = core.EffectSkipped
	return nil
}

func (j *dkJournal) List(context.Context) ([]core.Effect, error) { return j.effects(), nil }

func (j *dkJournal) Secret(_ context.Context, seq int) ([]byte, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if seq < 1 || seq > len(j.list) {
		return nil, fmt.Errorf("fake: no effect %d", seq)
	}
	return bytes.Clone(j.secrets[seq]), nil
}
