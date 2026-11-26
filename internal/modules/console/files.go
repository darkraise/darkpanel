package console

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"sync"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// actionFilePrefix starts the journal action of each file operation,
// console.file.<op>.
const actionFilePrefix = "console.file."

// The phases of a file operation's two journal entries.
const (
	PhaseIntent = "intent"
	PhaseResult = "result"
)

// The results a console_file_ops row ends with.
const (
	resultOK          = "ok"
	resultError       = "error"
	ResultInterrupted = "interrupted" // a crash cut the operation off
)

// fileRecord is the Input of a file operation's journal entries. No file
// content is ever recorded.
type fileRecord struct {
	Source string `json:"source"`
	Path   string `json:"path"`
	To     string `json:"to,omitempty"`   // rename
	Mode   string `json:"mode,omitempty"` // chmod
	UID    *int   `json:"uid,omitempty"`  // chown
	GID    *int   `json:"gid,omitempty"`  // chown
	Size   *int64 `json:"size,omitempty"`
	SHA256 string `json:"sha256,omitempty"`
	Error  string `json:"error,omitempty"`
}

var _ core.FileManager = (*module)(nil)

// fileOps is the set of file operations running in this process, by id.
type fileOps struct {
	mu   sync.Mutex
	open map[string]bool
}

func newFileOps() *fileOps { return &fileOps{open: map[string]bool{}} }

func (s *fileOps) add() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	id := hex.EncodeToString(b[:])
	s.mu.Lock()
	defer s.mu.Unlock()
	s.open[id] = true
	return id, nil
}

func (s *fileOps) remove(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.open, id)
}

// Files returns env.ConsoleFS with every mutation recorded: a
// console_file_ops row and an intent entry before the first byte, a result
// entry, with the size and SHA-256 of a write or the error, before it
// answers.
func (m *module) Files(env core.Env) core.ConsoleFiles { return &recordedFiles{m: m, env: env} }

type recordedFiles struct {
	m   *module
	env core.Env
}

func (f *recordedFiles) List(ctx context.Context, path, cursor string) (core.DirPage, error) {
	return f.env.ConsoleFS.List(ctx, path, cursor)
}

func (f *recordedFiles) Stat(ctx context.Context, path string) (core.FileInfo, error) {
	return f.env.ConsoleFS.Stat(ctx, path)
}

func (f *recordedFiles) ReadText(ctx context.Context, path string) ([]byte, string, error) {
	return f.env.ConsoleFS.ReadText(ctx, path)
}

func (f *recordedFiles) Open(ctx context.Context, path string) (io.ReadCloser, int64, error) {
	return f.env.ConsoleFS.Open(ctx, path)
}

func (f *recordedFiles) RemoveTemp(ctx context.Context, path string) error {
	return f.env.ConsoleFS.RemoveTemp(ctx, path)
}

func (f *recordedFiles) WriteText(ctx context.Context, path string, data []byte, expectSHA string) (string, error) {
	size := int64(len(data))
	var sum string
	err := f.m.recordOp(ctx, f.env, "write", fileRecord{Path: path, Size: &size}, func(ctx context.Context, res *fileRecord) error {
		var err error
		sum, err = f.env.ConsoleFS.WriteText(ctx, path, data, expectSHA)
		res.SHA256 = sum
		return err
	})
	return sum, err
}

func (f *recordedFiles) Upload(ctx context.Context, path string, r io.Reader, size int64) (string, error) {
	var sum string
	err := f.m.recordOp(ctx, f.env, "upload", fileRecord{Path: path, Size: &size}, func(ctx context.Context, res *fileRecord) error {
		var err error
		sum, err = f.env.ConsoleFS.Upload(ctx, path, r, size)
		res.SHA256 = sum
		return err
	})
	return sum, err
}

func (f *recordedFiles) Mkdir(ctx context.Context, path string) error {
	return f.m.recordOp(ctx, f.env, "mkdir", fileRecord{Path: path}, func(ctx context.Context, _ *fileRecord) error {
		return f.env.ConsoleFS.Mkdir(ctx, path)
	})
}

func (f *recordedFiles) Rename(ctx context.Context, from, to string) error {
	return f.m.recordOp(ctx, f.env, "rename", fileRecord{Path: from, To: to}, func(ctx context.Context, _ *fileRecord) error {
		return f.env.ConsoleFS.Rename(ctx, from, to)
	})
}

func (f *recordedFiles) Delete(ctx context.Context, path, confirm string) error {
	return f.m.recordOp(ctx, f.env, "delete", fileRecord{Path: path}, func(ctx context.Context, _ *fileRecord) error {
		return f.env.ConsoleFS.Delete(ctx, path, confirm)
	})
}

func (f *recordedFiles) Chmod(ctx context.Context, path string, mode fs.FileMode) error {
	rec := fileRecord{Path: path, Mode: fmt.Sprintf("%04o", uint32(mode))}
	return f.m.recordOp(ctx, f.env, "chmod", rec, func(ctx context.Context, _ *fileRecord) error {
		return f.env.ConsoleFS.Chmod(ctx, path, mode)
	})
}

func (f *recordedFiles) Chown(ctx context.Context, path string, uid, gid int) error {
	rec := fileRecord{Path: path, UID: &uid, GID: &gid}
	return f.m.recordOp(ctx, f.env, "chown", rec, func(ctx context.Context, _ *fileRecord) error {
		return f.env.ConsoleFS.Chown(ctx, path, uid, gid)
	})
}

// recordOp runs op, one mutation, between its records. The id joins the
// open set before its row exists and leaves it once the row has its result,
// so a sweep never takes a running operation for one a crash cut off. A
// write's temporary file is put on the row before its first byte.
func (m *module) recordOp(ctx context.Context, env core.Env, op string, intent fileRecord, run func(ctx context.Context, res *fileRecord) error) error {
	id, err := m.ops.add()
	if err != nil {
		return err
	}
	defer m.ops.remove(id)
	intent.Source = env.Principal.Source
	at := env.Clock.Now()
	err = env.DB.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			"INSERT INTO console_file_ops (id, op, path, actor, source, size, started_at) VALUES (?, ?, ?, ?, ?, ?, ?)",
			id, op, intent.Path, env.Principal.Actor, intent.Source, intent.Size, at.UTC().Format(time.RFC3339Nano))
		return err
	})
	if err != nil {
		return fmt.Errorf("console: record the file operation: %w", err)
	}
	action := actionFilePrefix + op
	if err := recordFile(ctx, env, action, PhaseIntent, env.Principal.Actor, at, intent, nil); err != nil {
		_ = env.DB.Write(context.WithoutCancel(ctx), func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, "DELETE FROM console_file_ops WHERE id = ?", id)
			return err
		})
		return err
	}
	runCtx := core.WithTempRecord(ctx, func(tmp string) error {
		return env.DB.Write(ctx, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, "UPDATE console_file_ops SET tmp_path = ? WHERE id = ?", tmp, id)
			return err
		})
	})
	res := intent
	opErr := run(runCtx, &res)
	if opErr != nil {
		res.Size, res.SHA256, res.Error = nil, "", errorText(opErr)
	}
	// The result is recorded whatever ended the request's context.
	ctx = context.WithoutCancel(ctx)
	if err := recordFile(ctx, env, action, PhaseResult, env.Principal.Actor, env.Clock.Now(), res, opErr); err != nil {
		// The row keeps no result, so the next start journals the
		// operation as interrupted rather than not at all.
		env.Log.Error("console: journal a file result", "action", action, "error", err)
		return opErr
	}
	result := resultOK
	if opErr != nil {
		result = resultError
	}
	err = env.DB.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "UPDATE console_file_ops SET result = ?, size = ?, sha256 = ? WHERE id = ?",
			result, res.Size, nullable(res.SHA256), id)
		return err
	})
	if err != nil {
		env.Log.Error("console: record a file result", "action", action, "error", err)
	}
	return opErr
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// errorText is the error a result records: the error's detail, never file
// content.
func errorText(err error) string {
	if errors.Is(err, context.Canceled) {
		return "cancelled"
	}
	if e := errcode.From(err); e.Detail != "" {
		return e.Detail
	}
	return err.Error()
}

func recordFile(ctx context.Context, env core.Env, action, phase, actor string, at time.Time, rec fileRecord, opErr error) error {
	input, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	e := core.JournalEntry{At: at, Actor: actor, Action: action, Version: 1, Phase: phase, Input: input, Result: resultOK, Source: rec.Source}
	if opErr != nil || rec.Error != "" {
		e.Result = resultError
	}
	if opErr != nil {
		e.ErrorCode = string(errcode.From(opErr).Code)
	}
	if err := env.Journal.Record(ctx, e); err != nil {
		return fmt.Errorf("console: journal %s: %w", action, err)
	}
	return nil
}

// sweepFiles journals the result "interrupted" of every operation recorded
// without a result that this process does not run, removes its temporary
// file, and forgets the operations that have their result.
func (m *module) sweepFiles(ctx context.Context, env core.Env) error {
	type left struct{ id, op, path, actor, source, tmp string }
	var orphans []left
	err := func() error {
		// The lock holds while the rows are read: an operation joins the
		// open set before its row exists and leaves it after its result,
		// so a row read here without a result whose id is not open belongs
		// to no running operation.
		m.ops.mu.Lock()
		defer m.ops.mu.Unlock()
		rows, err := env.DB.Read().QueryContext(ctx,
			"SELECT id, op, path, actor, source, COALESCE(tmp_path, '') FROM console_file_ops WHERE result IS NULL ORDER BY started_at")
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var l left
			if err := rows.Scan(&l.id, &l.op, &l.path, &l.actor, &l.source, &l.tmp); err != nil {
				return err
			}
			if !m.ops.open[l.id] {
				orphans = append(orphans, l)
			}
		}
		return rows.Err()
	}()
	if err != nil {
		return fmt.Errorf("console: read the file operations: %w", err)
	}
	var errs []error
	for _, l := range orphans {
		rec := fileRecord{Source: l.source, Path: l.path, Error: ResultInterrupted}
		if err := recordFile(ctx, env, actionFilePrefix+l.op, PhaseResult, l.actor, env.Clock.Now(), rec, nil); err != nil {
			errs = append(errs, err)
			continue
		}
		if l.tmp != "" {
			if err := env.ConsoleFS.RemoveTemp(ctx, l.tmp); err != nil {
				env.Log.Warn("console: remove a stale temporary file", "path", l.tmp, "error", err)
			}
		}
		errs = append(errs, env.DB.Write(ctx, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, "UPDATE console_file_ops SET result = ? WHERE id = ?", ResultInterrupted, l.id)
			return err
		}))
	}
	errs = append(errs, env.DB.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "DELETE FROM console_file_ops WHERE result IS NOT NULL")
		return err
	}))
	return errors.Join(errs...)
}
