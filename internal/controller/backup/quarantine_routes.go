package backup

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/quarantine"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/ext"
)

// Record is one quarantined record.
type Record struct {
	ID              string   `json:"id"`
	Kind            string   `json:"kind"`   // "change" | "composite" | "cloud" | "rollout"
	Action          string   `json:"action"` // the change's action; "core.update.rollout" for a rollout
	HostID          string   `json:"hostId"` // "" for controller-side records and rollouts
	QuarantinedFrom string   `json:"quarantinedFrom"`
	Locks           []string `json:"locks"` // sorted; [] when none, never null
}

// Status answers GET /api/v1/controller/quarantine.
type Status struct {
	Active     bool       `json:"active"`
	Tx         string     `json:"tx,omitempty"`
	RestoredAt *time.Time `json:"restoredAt,omitempty"`
	Records    []Record   `json:"records"` // every record in state quarantined, by id; [] when none
}

// ResolveRequest is the body of POST /api/v1/controller/quarantine/{record}/resolve.
type ResolveRequest struct {
	Outcome string `json:"outcome"` // "done" | "never" | "repair"
}

// EnrollTokenRequest is the body of POST /api/v1/controller/quarantine/enroll-token.
type EnrollTokenRequest struct {
	HostID string `json:"hostId"`
}

// EnrollTokenResult answers POST /api/v1/controller/quarantine/enroll-token.
type EnrollTokenResult struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// ReleaseRequest is the body of POST /api/v1/controller/quarantine/release.
type ReleaseRequest struct {
	KeepUnresolved bool `json:"keepUnresolved"`
}

// HeldLock is a lock a quarantined record still holds.
type HeldLock struct {
	Key    string `json:"key"`
	Record string `json:"record"`
}

// ReleaseResult answers POST /api/v1/controller/quarantine/release.
type ReleaseResult struct {
	HeldLocks []HeldLock `json:"heldLocks"` // sorted by key; [] when none
}

// queryer is *sql.DB or *sql.Tx.
type queryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

const quarantineRolloutAction = "core.update.rollout"

// listRecords returns every change and rollout in state quarantined, sorted
// by id, each with the sorted keys of the locks it owns.
func listRecords(ctx context.Context, q queryer) ([]Record, error) {
	records := []Record{}
	rows, err := q.QueryContext(ctx,
		`SELECT id, action, host_id, class, quarantined_from FROM core_changes WHERE state = ?`, core.StateQuarantined)
	if err != nil {
		return nil, fmt.Errorf("backup: list quarantined changes: %w", err)
	}
	if records, err = quarantineScanRows(rows, records, quarantineScanChange); err != nil {
		return nil, fmt.Errorf("backup: list quarantined changes: %w", err)
	}
	rows, err = q.QueryContext(ctx, `SELECT id, quarantined_from FROM core_rollouts WHERE state = ?`, core.RolloutQuarantined)
	if err != nil {
		return nil, fmt.Errorf("backup: list quarantined rollouts: %w", err)
	}
	if records, err = quarantineScanRows(rows, records, quarantineScanRollout); err != nil {
		return nil, fmt.Errorf("backup: list quarantined rollouts: %w", err)
	}

	locks := map[string][]string{}
	rows, err = q.QueryContext(ctx, `SELECT key, owner FROM core_locks`)
	if err != nil {
		return nil, fmt.Errorf("backup: list locks: %w", err)
	}
	if _, err = quarantineScanRows(rows, nil, func(s quarantineScanner) (struct{}, error) {
		var key, owner string
		if err := s.Scan(&key, &owner); err != nil {
			return struct{}{}, err
		}
		locks[owner] = append(locks[owner], key)
		return struct{}{}, nil
	}); err != nil {
		return nil, fmt.Errorf("backup: list locks: %w", err)
	}

	for i := range records {
		if keys := locks[records[i].ID]; len(keys) > 0 {
			slices.Sort(keys)
			records[i].Locks = keys
		}
	}
	slices.SortFunc(records, func(a, b Record) int { return strings.Compare(a.ID, b.ID) })
	return records, nil
}

// recordByID returns the record id whatever its state now, so that a record
// just resolved out of quarantine is still answered with the state it was
// quarantined from. A record is any change or rollout whose quarantined_from
// is set; every other id is not found.
func recordByID(ctx context.Context, q queryer, id string) (Record, bool, error) {
	rec, err := quarantineScanChange(q.QueryRowContext(ctx,
		`SELECT id, action, host_id, class, quarantined_from FROM core_changes WHERE id = ? AND quarantined_from IS NOT NULL`, id))
	if errors.Is(err, sql.ErrNoRows) {
		rec, err = quarantineScanRollout(q.QueryRowContext(ctx,
			`SELECT id, quarantined_from FROM core_rollouts WHERE id = ? AND quarantined_from IS NOT NULL`, id))
	}
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return Record{}, false, nil
	case err != nil:
		return Record{}, false, fmt.Errorf("backup: read quarantined record %s: %w", id, err)
	}

	rows, err := q.QueryContext(ctx, `SELECT key FROM core_locks WHERE owner = ? ORDER BY key`, id)
	if err != nil {
		return Record{}, false, fmt.Errorf("backup: read locks of %s: %w", id, err)
	}
	keys, err := quarantineScanRows(rows, rec.Locks, func(s quarantineScanner) (string, error) {
		var key string
		err := s.Scan(&key)
		return key, err
	})
	if err != nil {
		return Record{}, false, fmt.Errorf("backup: read locks of %s: %w", id, err)
	}
	rec.Locks = keys
	return rec, true, nil
}

// quarantineScanner is *sql.Row or *sql.Rows.
type quarantineScanner interface {
	Scan(dest ...any) error
}

// quarantineScanRows appends scan of every row to out and closes rows.
func quarantineScanRows[T any](rows *sql.Rows, out []T, scan func(quarantineScanner) (T, error)) ([]T, error) {
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		v, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, rows.Close()
}

func quarantineScanChange(s quarantineScanner) (Record, error) {
	var (
		id, action, hostID, class string
		from                      sql.NullString
	)
	if err := s.Scan(&id, &action, &hostID, &class, &from); err != nil {
		return Record{}, err
	}
	kind := "change"
	prefix, _, _ := strings.Cut(action, ".")
	switch {
	case class == string(core.ClassComposite):
		kind = "composite"
	case prefix == "tailnet" || prefix == "cloudflare":
		kind = "cloud"
	}
	return Record{ID: id, Kind: kind, Action: action, HostID: hostID, QuarantinedFrom: from.String, Locks: []string{}}, nil
}

func quarantineScanRollout(s quarantineScanner) (Record, error) {
	var (
		id   string
		from sql.NullString
	)
	if err := s.Scan(&id, &from); err != nil {
		return Record{}, err
	}
	return Record{ID: id, Kind: "rollout", Action: quarantineRolloutAction, QuarantinedFrom: from.String, Locks: []string{}}, nil
}

// quarantineHandler answers the quarantine's status. Records kept by a
// release with keepUnresolved are listed after the flag is cleared.
func (b *Backup) quarantineHandler(deps ext.Deps) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		m, active, err := quarantine.Read(ctx, deps.DB)
		if err != nil {
			writeProblem(w, deps.Log, err)
			return
		}
		st := Status{Active: active}
		if active {
			restoredAt := m.RestoredAt
			st.Tx, st.RestoredAt = m.Tx, &restoredAt
		}
		if st.Records, err = listRecords(ctx, deps.DB.Read()); err != nil {
			writeProblem(w, deps.Log, err)
			return
		}
		writeJSON(w, deps.Log, http.StatusOK, st)
	})
}

// enrollTokenHandler issues a fresh enrollment token for a remote host while
// the controller is quarantined.
func (b *Backup) enrollTokenHandler(deps ext.Deps) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		var req EnrollTokenRequest
		if err := decodeBody(w, r, &req); err != nil {
			writeProblem(w, deps.Log, err)
			return
		}
		if req.HostID == "" {
			writeProblem(w, deps.Log, errcode.New(errcode.InvalidInput, "hostId is required"))
			return
		}
		active, err := quarantine.Active(ctx, deps.DB)
		if err != nil {
			writeProblem(w, deps.Log, err)
			return
		}
		if !active {
			writeProblem(w, deps.Log, errcode.New(errcode.InvalidInput,
				"the controller is not in restore quarantine; use darkpanel host token"))
			return
		}
		if b.cfg.Tokens == nil || b.cfg.AgentAdvertise == "" {
			writeProblem(w, deps.Log, errcode.New(errcode.InvalidInput,
				"start the controller with --agent-advertise to issue enrollment tokens"))
			return
		}

		var (
			token   string
			expires time.Time
		)
		err = deps.DB.Write(ctx, func(tx *sql.Tx) error {
			// A release can commit between the check above and this
			// transaction, so the flag is read again where the token is issued.
			var n int
			if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM core_meta WHERE key = ?`, quarantine.MetaKey).Scan(&n); err != nil {
				return fmt.Errorf("backup: read core_meta %s: %w", quarantine.MetaKey, err)
			}
			if n == 0 {
				return errcode.New(errcode.InvalidInput,
					"the controller is not in restore quarantine; use darkpanel host token")
			}
			var (
				local bool
				state string
			)
			err := tx.QueryRowContext(ctx, `SELECT is_local, state FROM core_hosts WHERE id = ?`, req.HostID).Scan(&local, &state)
			switch {
			case errors.Is(err, sql.ErrNoRows):
				return errcode.New(errcode.NotFound, "no host "+req.HostID)
			case err != nil:
				return fmt.Errorf("backup: read host %s: %w", req.HostID, err)
			case local:
				return errcode.New(errcode.InvalidInput, req.HostID+
					" is the local host; enroll it with core.host.enroll-local over the controller socket")
			}
			// The fleet authority enrolls only a pending or revoked host.
			if state == core.HostEnrolled {
				if _, err := tx.ExecContext(ctx, `UPDATE core_hosts SET state = ? WHERE id = ?`, core.HostPending, req.HostID); err != nil {
					return fmt.Errorf("backup: set host %s pending: %w", req.HostID, err)
				}
			}
			token, expires, err = b.cfg.Tokens.IssueToken(ctx, tx, req.HostID, b.cfg.AgentAdvertise)
			return err
		})
		if err != nil {
			writeProblem(w, deps.Log, err)
			return
		}
		recordAudit(ctx, deps, "controller.quarantine.enroll-token", "apply", "", req.HostID)
		writeJSON(w, deps.Log, http.StatusOK, EnrollTokenResult{Token: token, ExpiresAt: expires.UTC()})
	})
}

// releaseHandler clears the quarantine flag and names the locks quarantined
// records still hold.
func (b *Backup) releaseHandler(deps ext.Deps) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		var req ReleaseRequest
		if err := decodeBody(w, r, &req); err != nil {
			writeProblem(w, deps.Log, err)
			return
		}
		res := ReleaseResult{HeldLocks: []HeldLock{}}
		released := false
		// The flag, the records and the delete share one transaction so that
		// no record can be quarantined or resolved between the check and the
		// release; every read goes through tx, which alone sees its writes.
		err := deps.DB.Write(ctx, func(tx *sql.Tx) error {
			var n int
			if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM core_meta WHERE key = ?`, quarantine.MetaKey).Scan(&n); err != nil {
				return fmt.Errorf("backup: read core_meta %s: %w", quarantine.MetaKey, err)
			}
			active := n > 0
			records, err := listRecords(ctx, tx)
			if err != nil {
				return err
			}
			if active && len(records) > 0 && !req.KeepUnresolved {
				return errcode.New(errcode.Quarantined, fmt.Sprintf(
					"%d records are still quarantined; resolve each one, or release with --keep-unresolved", len(records)))
			}
			if active {
				if _, err := tx.ExecContext(ctx, `DELETE FROM core_meta WHERE key = ?`, quarantine.MetaKey); err != nil {
					return fmt.Errorf("backup: delete core_meta %s: %w", quarantine.MetaKey, err)
				}
				released = true
			}
			for _, rec := range records {
				for _, key := range rec.Locks {
					res.HeldLocks = append(res.HeldLocks, HeldLock{Key: key, Record: rec.ID})
				}
			}
			slices.SortFunc(res.HeldLocks, func(a, b HeldLock) int { return strings.Compare(a.Key, b.Key) })
			return nil
		})
		if err != nil {
			writeProblem(w, deps.Log, err)
			return
		}
		if released {
			recordAudit(ctx, deps, "controller.quarantine.release", "release", "", "")
		}
		writeJSON(w, deps.Log, http.StatusOK, res)
	})
}

const maxRouteBody = 64 << 10

// quarantineProblem is an RFC 9457 problem details body.
type quarantineProblem struct {
	Type   string               `json:"type"`
	Title  string               `json:"title"`
	Status int                  `json:"status"`
	Detail string               `json:"detail"`
	Code   errcode.Code         `json:"code"`
	Errors []errcode.FieldError `json:"errors,omitempty"`
}

// writeProblem answers err as application/problem+json. A code outside the
// registry becomes internal. A 5xx answer carries only the code's title,
// because neither the detail nor the cause of an internal failure is
// client-safe; the cause is logged instead.
func writeProblem(w http.ResponseWriter, log *slog.Logger, err error) {
	e := errcode.From(err)
	code := e.Code
	if !errcode.Known(code) {
		code = errcode.Internal
	}
	status := code.Status()
	p := quarantineProblem{Type: "about:blank", Title: code.Title(), Status: status, Detail: e.Detail, Code: code, Errors: e.Fields}
	if status >= http.StatusInternalServerError {
		p.Detail, p.Errors = code.Title(), nil
		log.Error("backup: request failed", "code", e.Code, "err", err)
	}
	if p.Detail == "" {
		p.Detail = code.Title()
	}
	b, errMarshal := json.Marshal(p)
	if errMarshal != nil {
		panic(fmt.Sprintf("backup: marshal problem: %v", errMarshal)) // strings, ints and field errors always marshal
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_, _ = w.Write(b)
}

// writeJSON answers v as application/json with status; a value that does
// not marshal is answered as internal.
func writeJSON(w http.ResponseWriter, log *slog.Logger, status int, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		writeProblem(w, log, fmt.Errorf("backup: marshal response: %w", err))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(b)
}

// decodeBody decodes one JSON object of at most maxRouteBody bytes into dst,
// refusing unknown members and anything but whitespace after the object.
// Every refusal is request.invalid_input with a fixed text, so no part of
// the input, which may carry a passphrase, reaches the answer.
func decodeBody(w http.ResponseWriter, r *http.Request, dst any) error {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRouteBody))
	var tooLarge *http.MaxBytesError
	switch {
	case errors.As(err, &tooLarge):
		return errcode.New(errcode.InvalidInput, fmt.Sprintf("the request body exceeds %d bytes", maxRouteBody))
	case err != nil:
		return errcode.New(errcode.InvalidInput, "the request body could not be read")
	}
	body = bytes.TrimSpace(body)
	if len(body) == 0 {
		return errcode.New(errcode.InvalidInput, "the request body is empty")
	}
	if body[0] != '{' {
		return errcode.New(errcode.InvalidInput, "the request body must be a JSON object")
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var wrongType *json.UnmarshalTypeError
		switch {
		case errors.As(err, &wrongType):
			return errcode.New(errcode.InvalidInput, "a member of the request body has the wrong type")
		case strings.HasPrefix(err.Error(), "json: unknown field "):
			return errcode.New(errcode.InvalidInput, "the request body names an unknown member")
		}
		return errcode.New(errcode.InvalidInput, "the request body is not valid JSON")
	}
	if dec.InputOffset() != int64(len(body)) {
		return errcode.New(errcode.InvalidInput, "the request body holds more than one JSON value")
	}
	return nil
}

// recordAudit writes one ok audit row as the local root. The change it
// records has committed, so a failed write is logged and the caller still
// answers the result.
func recordAudit(ctx context.Context, deps ext.Deps, action, phase, changeID, hostID string) {
	err := deps.Audit.Record(ctx, core.AuditEntry{
		Actor: "local:root@" + deps.LocalHostID, HostID: hostID, Action: action, Phase: phase,
		ChangeID: changeID, Result: "ok", At: deps.Clock.Now(),
	})
	if err != nil {
		deps.Log.Error("backup: record audit", "action", action, "err", err)
	}
}
