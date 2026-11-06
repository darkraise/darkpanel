package backup

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
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

// copyDirSuffix ends the name of the private directory that holds a backup's
// temporary copy of the database.
const copyDirSuffix = ".tmp"

// backupHandler streams an encrypted backup of the controller database and
// key. Everything that can fail before the first body byte is answered as a
// problem; a failure after it aborts the response so the client keeps a
// truncated file that Read refuses.
func (b *Backup) backupHandler(deps ext.Deps) http.Handler {
	return b.streamServe(deps, rand.Reader)
}

// streamServe is backupHandler with the source of the salt and nonce prefix
// as a parameter, so a test can make Write fail before the first body byte.
func (b *Backup) streamServe(deps ext.Deps, rnd io.Reader) http.Handler {
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

		dir := filepath.Join(filepath.Dir(deps.DB.Path()), "backup-"+ulid.Make().String()+copyDirSuffix)
		if err := os.Mkdir(dir, 0o700); err != nil {
			writeProblem(w, deps.Log, fmt.Errorf("backup: create the temporary directory %s: %w", dir, err))
			return
		}
		defer func() {
			if err := os.RemoveAll(dir); err != nil {
				deps.Log.Error("backup: remove the temporary copy", "path", dir, "err", err)
			}
		}()
		// SQLite creates the copy under the process umask, so the private
		// directory is what keeps other users from reading it before the chmod.
		path := filepath.Join(dir, "backup.db")
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
		errWrite := Write(sw, rnd, []byte(req.Passphrase), params, members)
		auditCtx := context.WithoutCancel(ctx)
		if errWrite != nil && !sw.began {
			streamAuditFailure(auditCtx, deps)
			writeProblem(w, deps.Log, errWrite)
			return
		}
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
	return streamReadBounded(f, path)
}

// streamReadBounded reads a controller key of at most MaxKeyMember bytes from
// r. It reads one byte more than the limit because a file that grew after
// streamReadKey's Stat must be refused, not cut off. No error carries the
// bytes read.
func streamReadBounded(r io.Reader, path string) ([]byte, error) {
	key, err := io.ReadAll(io.LimitReader(r, MaxKeyMember+1))
	if err != nil {
		clear(key)
		return nil, fmt.Errorf("backup: read the controller key file %s: %w", path, err)
	}
	if len(key) > MaxKeyMember {
		clear(key)
		return nil, fmt.Errorf("backup: the controller key file %s is larger than %d bytes", path, MaxKeyMember)
	}
	return key, nil
}

// copyName matches the temporary copies a backup makes beside the database:
// backup-<ULID> with copyDirSuffix, or with .db as earlier versions named it.
var copyName = regexp.MustCompile(`^backup-[0-9A-HJKMNP-TV-Z]{26}(` + regexp.QuoteMeta(copyDirSuffix) + `|\.db)$`)

// sweepCopies removes from dir the temporary copies of the database that
// backups left behind. A crash leaves the copy behind because the deferred
// removal in streamServe does not run. Every other name stays, and a failure
// is logged without stopping anything.
func sweepCopies(dir string, log *slog.Logger) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		log.Error("backup: list the database directory for leftover copies", "dir", dir, "err", err)
		return
	}
	for _, e := range entries {
		if !copyName.MatchString(e.Name()) {
			continue
		}
		path := filepath.Join(dir, e.Name())
		if err := os.RemoveAll(path); err != nil {
			log.Error("backup: remove a leftover copy", "path", path, "err", err)
		}
	}
}

// streamAuditFailure records a backup that reached the stream and failed,
// which recordAudit cannot write because it records only successes.
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
