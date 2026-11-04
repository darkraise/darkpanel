package backup

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"syscall"

	"github.com/oklog/ulid/v2"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/ext"
)

// BackupRequest is the body of POST /api/v1/controller/backup.
type BackupRequest struct { //nolint:revive // the wire type is named after its route, as ResolveRequest and ReleaseRequest are
	Passphrase string  `json:"passphrase"`
	Argon2     *Params `json:"argon2,omitempty"` // nil: DefaultParams
}

const streamMaxPassphrase = 1024

// backupHandler streams an encrypted backup of the controller database and
// key. Everything that can fail before the first body byte is answered as a
// problem; a failure after it aborts the response so the client keeps a
// truncated file that Read refuses.
func (b *Backup) backupHandler(deps ext.Deps) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		var req BackupRequest
		if err := decodeBody(w, r, &req); err != nil {
			writeProblem(w, deps.Log, err)
			return
		}
		if len(req.Passphrase) < 1 || len(req.Passphrase) > streamMaxPassphrase {
			writeProblem(w, deps.Log, errcode.New(errcode.InvalidInput, "the passphrase must be 1 to 1024 bytes"))
			return
		}
		params := DefaultParams
		if req.Argon2 != nil {
			if err := req.Argon2.Validate(); err != nil {
				writeProblem(w, deps.Log, err)
				return
			}
			params = *req.Argon2
		}

		if !b.running.CompareAndSwap(false, true) {
			writeProblem(w, deps.Log, errcode.New(errcode.ChangeBusy, "another backup is running"))
			return
		}
		defer b.running.Store(false)

		if err := streamWriteCanary(ctx, deps); err != nil {
			writeProblem(w, deps.Log, err)
			return
		}

		path := filepath.Join(filepath.Dir(deps.DB.Path()), "backup-"+ulid.Make().String()+".db")
		defer func() {
			if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
				deps.Log.Error("backup: remove the temporary copy", "path", path, "err", err)
			}
		}()
		if _, err := deps.DB.Writer().ExecContext(ctx, `VACUUM INTO ?`, path); err != nil {
			writeProblem(w, deps.Log, fmt.Errorf("backup: copy the controller database to %s: %w", path, err))
			return
		}
		if err := os.Chmod(path, 0o600); err != nil {
			writeProblem(w, deps.Log, fmt.Errorf("backup: chmod %s: %w", path, err))
			return
		}

		key, err := streamReadKey(b.cfg.KeyFile)
		if err != nil {
			writeProblem(w, deps.Log, err)
			return
		}
		defer clear(key)

		f, err := os.Open(path) //nolint:gosec // G304: the path comes from configuration or the database directory, never from a request
		if err != nil {
			writeProblem(w, deps.Log, fmt.Errorf("backup: open the temporary copy %s: %w", path, err))
			return
		}
		defer func() { _ = f.Close() }()
		st, err := f.Stat()
		if err != nil {
			writeProblem(w, deps.Log, fmt.Errorf("backup: stat the temporary copy %s: %w", path, err))
			return
		}
		members := []Member{
			{Name: MemberDB, Size: st.Size(), Body: f},
			{Name: MemberKey, Size: int64(len(key)), Body: bytes.NewReader(key)},
		}

		sw := &streamWriter{w: w, clock: deps.Clock}
		errWrite := Write(sw, rand.Reader, []byte(req.Passphrase), params, members)
		if errWrite != nil && !sw.began {
			writeProblem(w, deps.Log, errWrite)
			return
		}
		auditCtx := context.WithoutCancel(ctx)
		if errWrite != nil {
			deps.Log.Error("backup: the backup stream failed after its first byte", "err", errWrite)
			streamAuditFailure(auditCtx, deps)
			panic(http.ErrAbortHandler)
		}
		recordAudit(auditCtx, deps, "controller.backup", "apply", "", "")
	})
}

// streamWriteCanary seals the current time into core_meta, so a restore can
// prove the key it installs opens this database.
func streamWriteCanary(ctx context.Context, deps ext.Deps) error {
	plain, err := json.Marshal(Canary{CreatedAt: deps.Clock.Now().UTC()})
	if err != nil {
		return fmt.Errorf("backup: encode the canary: %w", err)
	}
	sealed, err := deps.Sealer.Seal(CanaryAD, plain)
	if err != nil {
		return fmt.Errorf("backup: seal the canary: %w", err)
	}
	value := base64.StdEncoding.EncodeToString(sealed)
	return deps.DB.Write(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO core_meta (key, value) VALUES (?, ?) ON CONFLICT (key) DO UPDATE SET value = excluded.value`,
			CanaryKey, value); err != nil {
			return fmt.Errorf("backup: write core_meta %s: %w", CanaryKey, err)
		}
		return nil
	})
}

// streamReadKey reads the controller key file. O_NOFOLLOW refuses a symbolic
// link and O_NONBLOCK keeps a FIFO from blocking until the Stat below refuses
// it; no error carries the file's bytes.
func streamReadKey(path string) ([]byte, error) {
	f, err := os.OpenFile(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC|syscall.O_NONBLOCK, 0) //nolint:gosec // G304: the path comes from configuration or the database directory, never from a request
	if err != nil {
		return nil, fmt.Errorf("backup: open the controller key file: %w", err)
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("backup: stat the controller key file %s: %w", path, err)
	}
	if !st.Mode().IsRegular() {
		return nil, fmt.Errorf("backup: the controller key file %s is not a regular file", path)
	}
	if st.Size() > MaxKeyMember {
		return nil, fmt.Errorf("backup: the controller key file %s is larger than %d bytes", path, MaxKeyMember)
	}
	key, err := io.ReadAll(io.LimitReader(f, MaxKeyMember))
	if err != nil {
		clear(key)
		return nil, fmt.Errorf("backup: read the controller key file %s: %w", path, err)
	}
	return key, nil
}

// streamAuditFailure records a backup that failed after it began, which
// recordAudit cannot write because it records only successes.
func streamAuditFailure(ctx context.Context, deps ext.Deps) {
	err := deps.Audit.Record(ctx, core.AuditEntry{
		Actor: "local:root@" + deps.LocalHostID, Action: "controller.backup", Phase: "apply",
		Result: "error", ErrorCode: string(errcode.Internal), At: deps.Clock.Now(),
	})
	if err != nil {
		deps.Log.Error("backup: record audit", "action", "controller.backup", "err", err)
	}
}

// streamWriter sends the response headers on the first body write, so every
// failure before it can still be answered as a problem.
type streamWriter struct {
	w     http.ResponseWriter
	clock core.Clock
	began bool // set before the first delegated write, so a failed first write counts
}

func (s *streamWriter) Write(p []byte) (int, error) {
	if !s.began {
		s.began = true
		name := "darkpanel-controller-" + s.clock.Now().UTC().Format("20060102T150405Z") + ".dpbk"
		h := s.w.Header()
		h.Set("Content-Type", "application/octet-stream")
		h.Set("Content-Disposition", `attachment; filename="`+name+`"`)
		s.w.WriteHeader(http.StatusOK)
	}
	return s.w.Write(p)
}
