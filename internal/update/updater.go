package update

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os/exec"
	"slices"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/lifecycle"
	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/paths"
	"github.com/darkraise/darkpanel/internal/platform"
	"github.com/darkraise/darkpanel/internal/update/trustedkeys"
)

// The service units an update restarts and the unit their OnFailure= and
// every runner's OnFailure= start (spec section 7, Units).
const (
	ControllerUnit = "darkpanel-controller.service"
	AgentUnit      = "darkpanel-agent.service"
	RollbackUnit   = "darkpanel-rollback.service"
)

// Time limits of spec section 8: the runner waits ChangeWait for the change
// that started it, and one recover command works at most RecoverBudget, so a
// rollback finishes inside the two minutes S5 allows.
const (
	ChangeWait    = 60 * time.Second
	RecoverBudget = 30 * time.Second
)

// selfTestTimeout bounds a staged binary's self-test.
const selfTestTimeout = 30 * time.Second

// statusOperations is how many operations core.update.status reports when no
// operation or change is named.
const statusOperations = 20

// RunnerUnit is the transient unit that runs update transaction tx.
func RunnerUnit(tx string) string { return "darkpanel-update-" + tx + ".service" }

// RollbackRunnerUnit is the transient unit that runs rollback transaction tx.
func RollbackRunnerUnit(tx string) string { return "darkpanel-rollback-" + tx + ".service" }

// DeadlineTimer is the transient timer that rolls tx back when its health
// window ends.
func DeadlineTimer(tx string) string { return "darkpanel-update-deadline-" + tx + ".timer" }

// Config is what an Updater works on. The agent daemon, the runner and the
// recover commands each build one; only the agent's needs Index.
type Config struct {
	Paths   paths.Paths
	DB      *store.DB // agent.db
	Systemd platform.Systemd
	Clock   core.Clock
	Log     *slog.Logger
	Version string            // this binary's version, buildinfo.Version
	Arch    string            // runtime.GOARCH
	BaseURL string            // trustedkeys.BaseURL()
	Keys    []trustedkeys.Key // trustedkeys.Keys()
	HTTP    *http.Client
	Index   *IndexSource
	Probes  Probes
	// SelfTest runs a staged binary's self-test; nil runs "<binary> selftest"
	// with a 30 s limit.
	SelfTest func(ctx context.Context, binary string) error
	// NewerAgents lists enrolled hosts whose agent reports a version newer
	// than version; set on the controller's host only, nil elsewhere.
	NewerAgents func(ctx context.Context, version string) ([]string, error)
}

// Updater runs update and rollback operations on this host.
type Updater struct {
	cfg Config
	// lockTimeout is LockTimeout; tests shorten it.
	lockTimeout time.Duration

	idMu    sync.Mutex // guards entropy
	entropy io.Reader
}

// New returns an updater for cfg.
func New(cfg Config) (*Updater, error) {
	switch {
	case cfg.Paths.UpdateDir == "" || cfg.Paths.BinaryDir == "":
		return nil, errors.New("update: the update and binary directories are required")
	case cfg.DB == nil:
		return nil, errors.New("update: the agent database is required")
	case cfg.Systemd == nil:
		return nil, errors.New("update: systemd is required")
	case cfg.Clock == nil:
		return nil, errors.New("update: a clock is required")
	case cfg.Version == "":
		return nil, errors.New("update: the binary version is required")
	}
	if cfg.Log == nil {
		cfg.Log = slog.New(slog.DiscardHandler)
	}
	if cfg.SelfTest == nil {
		cfg.SelfTest = runSelfTest
	}
	if cfg.HTTP == nil {
		cfg.HTTP = http.DefaultClient
	}
	return &Updater{cfg: cfg, lockTimeout: LockTimeout, entropy: ulid.Monotonic(rand.Reader, 0)}, nil
}

// newID returns a transaction id. Ids made by one process within one
// millisecond still sort in the order they were made, so the records of an
// operation stay in order when the clock does not move.
func (u *Updater) newID() string {
	u.idMu.Lock()
	defer u.idMu.Unlock()
	return ulid.MustNew(ulid.Timestamp(u.cfg.Clock.Now()), u.entropy).String()
}

func (u *Updater) now() time.Time { return u.cfg.Clock.Now().UTC() }

// startTime is the StartedAt of a new record: now, or just after the newest
// record when the clock reads no later, so records keep the order they were
// made in even when the clock is set back or several processes make records
// within one clock reading.
func (u *Updater) startTime() (time.Time, error) {
	now := u.now()
	all, err := ListRecords(u.cfg.Paths)
	if err != nil {
		return time.Time{}, err
	}
	if n := len(all); n > 0 && !now.After(all[n-1].StartedAt) {
		now = all[n-1].StartedAt.Add(time.Nanosecond)
	}
	return now, nil
}

// lock takes the update lock for a runner or an action, which never stops
// another process to get it.
func (u *Updater) lock(ctx context.Context) (*Lock, error) {
	l, err := AcquireLock(ctx, u.cfg.Paths.UpdateLock(), u.lockTimeout)
	if errors.Is(err, ErrLockTimeout) {
		return nil, errcode.Wrap(errcode.UpdateInProgress, err, "another update process holds the update lock")
	}
	return l, err
}

// unlock releases l, logging a failure: the lock ends with the file anyway.
func (u *Updater) unlock(l *Lock) {
	if err := l.Release(); err != nil {
		u.cfg.Log.Warn("update: release the update lock", "error", err)
	}
}

// save writes r in state with err as its error, stamping UpdatedAt. The
// caller holds the update lock.
func (u *Updater) save(r *Record, state string, err *core.ErrorDetail) error {
	r.State, r.Error, r.UpdatedAt = state, err, u.now()
	return WriteRecord(u.cfg.Paths, *r)
}

// release drops the reservation of operation op.
func (u *Updater) release(ctx context.Context, op string) error {
	return lifecycle.Release(ctx, u.cfg.DB, lifecycle.ReservationOwner(op))
}

// detail is the record error of err: its code when it carries one.
func detail(err error) *core.ErrorDetail {
	e := errcode.From(err)
	msg := e.Detail
	if msg == "" {
		msg = err.Error()
	}
	return &core.ErrorDetail{Code: string(e.Code), Message: msg}
}

// reserve gives a new operation every lock and returns its id. The caller
// holds the update lock, so a recover pass, which sweeps reservations whose
// operation has no record, cannot sweep this one before its record exists.
func (u *Updater) reserve(ctx context.Context) (string, error) {
	tx := u.newID()
	if err := lifecycle.Reserve(ctx, u.cfg.DB, lifecycle.ReserveAll, lifecycle.ReservationOwner(tx)); err != nil {
		return "", err
	}
	return tx, nil
}

// begin records r, the first transaction of a new operation, and starts its
// transient runner unit running argv. The caller holds the update lock and
// the operation's reservation; when anything fails, begin records r failed,
// if r was written, and releases the reservation.
func (u *Updater) begin(ctx context.Context, r Record, unit string, argv []string) error {
	err := WriteRecord(u.cfg.Paths, r)
	if err == nil {
		if err = u.startRunner(ctx, unit, argv); err != nil {
			err = errors.Join(err, u.save(&r, core.UpdateFailed, detail(err)))
		}
	}
	if err != nil {
		return errors.Join(err, u.release(ctx, r.OperationID))
	}
	return nil
}

// startRunner starts a transient runner unit and waits until systemd reports
// it started, so a recover pass that later sees the unit inactive knows the
// runner is gone. The caller holds the update lock, which the new runner
// waits for before it touches its record.
func (u *Updater) startRunner(ctx context.Context, unit string, argv []string) error {
	err := u.cfg.Systemd.StartTransientService(ctx, unit, platform.TransientService{
		Description: "darkpanel update " + unit,
		ExecStart:   argv,
		OnFailure:   []string{RollbackUnit},
	})
	if err != nil {
		return fmt.Errorf("update: start %s: %w", unit, err)
	}
	for range 100 {
		st, err := u.cfg.Systemd.State(ctx, unit)
		if err != nil {
			return fmt.Errorf("update: start %s: %w", unit, err)
		}
		if st.ActiveState != "inactive" || st.LoadState == "not-found" {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
	u.cfg.Log.Warn("update: the runner unit has not started yet", "unit", unit)
	return nil
}

// CheckApply applies the update rules of in without side effects: the
// releases the update would install, target last.
func (u *Updater) CheckApply(ctx context.Context, in core.UpdateApplyInput) ([]Release, error) {
	if u.cfg.Index == nil {
		return nil, errors.New("update: no release index is configured")
	}
	releases, err := u.cfg.Index.Releases(ctx)
	if err != nil {
		return nil, err
	}
	return Steps(u.cfg.Version, in.Version, in.Channel, releases, in.Force)
}

// StartUpdate is core.update.apply's Apply: it checks the rules again and,
// under the update lock, reserves every lock for the new operation, records
// its first transaction pending for changeID and starts the runner, then
// returns before any service restarts (spec section 8, Apply returns before
// the restart).
func (u *Updater) StartUpdate(ctx context.Context, changeID string, in core.UpdateApplyInput) (core.UpdateTransaction, error) {
	steps, err := u.CheckApply(ctx, in)
	if err != nil {
		return core.UpdateTransaction{}, err
	}
	l, err := u.lock(ctx)
	if err != nil {
		return core.UpdateTransaction{}, err
	}
	defer u.unlock(l)
	now, err := u.startTime()
	if err != nil {
		return core.UpdateTransaction{}, err
	}
	tx, err := u.reserve(ctx)
	if err != nil {
		return core.UpdateTransaction{}, err
	}
	r := Record{
		ID: tx, OperationID: tx, Kind: core.UpdateKindUpdate,
		FromVersion: u.cfg.Version, ToVersion: steps[0].Version, TargetVersion: in.Version,
		State: core.UpdatePending, StartedAt: now, UpdatedAt: now, ChangeID: changeID, Steps: steps,
	}
	if err := u.begin(ctx, r, RunnerUnit(tx), []string{u.cfg.Paths.Binary(), "update", "run", tx}); err != nil {
		return core.UpdateTransaction{}, err
	}
	return r.Transaction(), nil
}

// Status answers core.update.status/v1: the records of one operation, of the
// operation a change started, or of the newest 20 operations, newest first.
func (u *Updater) Status(_ context.Context, in core.UpdateStatusInput) (core.UpdateStatus, error) {
	if in.OperationID != "" && in.ChangeID != "" {
		return core.UpdateStatus{}, errcode.New(errcode.InvalidInput, "name an operationId or a changeId, not both")
	}
	all, err := ListRecords(u.cfg.Paths)
	if err != nil {
		return core.UpdateStatus{}, err
	}
	var picked []Record
	switch {
	case in.OperationID != "":
		picked = OperationRecords(all, in.OperationID)
	case in.ChangeID != "":
		if i := slices.IndexFunc(all, func(r Record) bool { return r.ChangeID == in.ChangeID }); i >= 0 {
			picked = OperationRecords(all, all[i].OperationID)
		}
	default:
		ops := map[string]bool{}
		for i := len(all) - 1; i >= 0; i-- {
			if !ops[all[i].OperationID] && len(ops) == statusOperations {
				continue
			}
			ops[all[i].OperationID] = true
		}
		for _, r := range all {
			if ops[r.OperationID] {
				picked = append(picked, r)
			}
		}
	}
	out := make([]core.UpdateTransaction, 0, len(picked))
	for i := len(picked) - 1; i >= 0; i-- {
		out = append(out, picked[i].Transaction())
	}
	return core.UpdateStatus{Transactions: out}, nil
}

// Transaction reads the record of transaction id: core.update.apply's Verify
// checks that it exists and has not failed.
func (u *Updater) Transaction(_ context.Context, id string) (core.UpdateTransaction, error) {
	r, err := ReadRecord(u.cfg.Paths, id)
	if errors.Is(err, fs.ErrNotExist) {
		return core.UpdateTransaction{}, errcode.Wrap(errcode.NotFound, err, "no update transaction "+id)
	}
	if err != nil {
		return core.UpdateTransaction{}, err
	}
	return r.Transaction(), nil
}

// runSelfTest runs "<binary> selftest" with the agent's fixed environment
// (ADR 0009) and a time limit. The binary is the one this updater just
// verified and staged, never a path from a request.
func runSelfTest(ctx context.Context, binary string) error {
	ctx, cancel := context.WithTimeout(ctx, selfTestTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "selftest") //nolint:gosec // G204: binary is the staged, verified release
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C.UTF-8", "LC_ALL=C.UTF-8"}
	out, err := cmd.CombinedOutput()
	if err != nil {
		const keep = 2048
		if len(out) > keep {
			out = out[len(out)-keep:]
		}
		return errcode.Wrap(errcode.UpdateInterrupted, err, fmt.Sprintf("the new binary's self-test failed: %s", out))
	}
	return nil
}
