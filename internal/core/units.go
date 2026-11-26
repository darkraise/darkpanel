package core

import (
	"context"
	"syscall"
	"time"
)

// Units manages systemd units through systemd's D-Bus API; Darkpanel never
// runs systemctl (ADR 0009). Every method queues its job and returns without
// waiting for it, so a caller that needs the outcome polls State.
// internal/platform implements it.
type Units interface {
	StartTransientService(ctx context.Context, name string, svc TransientService) error
	StartTransientTimer(ctx context.Context, name string, t TransientTimer) error
	Start(ctx context.Context, unit string) error
	Stop(ctx context.Context, unit string) error
	Restart(ctx context.Context, unit string) error
	Kill(ctx context.Context, unit string, sig syscall.Signal) error
	ResetFailed(ctx context.Context, unit string) error
	Enable(ctx context.Context, unitFiles ...string) error
	Disable(ctx context.Context, unitFiles ...string) error
	Reload(ctx context.Context) error
	State(ctx context.Context, unit string) (UnitState, error)
	// List returns the loaded units whose names match one glob pattern
	// (systemd's ListUnitsByPatterns), states as State reports them.
	List(ctx context.Context, pattern string) ([]UnitStatus, error)
}

// TransientService is a service unit that exists until it stops.
type TransientService struct {
	Description string
	ExecStart   []string      // ExecStart[0] is an absolute path
	OnFailure   []string      // unit names
	RuntimeMax  time.Duration // RuntimeMaxSec; zero sets none
	CollectMode string        // CollectMode; "" leaves systemd's default, task units set "inactive-or-failed"
}

// TransientTimer is a timer unit that exists until it is stopped; it starts
// Service, named like the timer with .service, once OnActive has passed.
type TransientTimer struct {
	Description string
	OnActive    time.Duration
	Accuracy    time.Duration
	Service     TransientService
}

// UnitState is a unit's state as systemd reports it. A unit that does not
// exist has LoadState "not-found". MainPID is the Service interface's MainPID
// property, 0 for a unit that is not a running service. PendingJob is the
// type of the job queued for the unit ("start", "stop", ...), "" when none.
type UnitState struct {
	LoadState, ActiveState, SubState string
	MainPID                          uint32
	PendingJob                       string
}

// UnitStatus is one unit List found.
type UnitStatus struct {
	Name string
	UnitState
}

// UnitReloader is implemented by a unit client that may ask a unit to
// reload its configuration (systemctl reload); a module's scoped client
// allows it on the external units that declare UnitOpReload.
type UnitReloader interface {
	ReloadUnit(ctx context.Context, unit string) error
}

// UnitAdmin is the unit client of the services module, the one module that
// drives units it does not declare (ADR 0020): only .service, .socket and
// .timer units that are not Darkpanel's own, and reads of every unit.
type UnitAdmin interface {
	Units
	// ReloadUnit asks a unit to reload its configuration (systemctl reload).
	ReloadUnit(ctx context.Context, unit string) error
	// ListAll returns every loaded unit and every unit file, templates left
	// out, of the types service, socket, timer, mount, path and target, with
	// the list fields of UnitInfo. A unit file systemd has not loaded reports
	// LoadState "not-loaded".
	ListAll(ctx context.Context) ([]UnitInfo, error)
	// Inspect loads unit and reads every field of UnitInfo. A unit that does
	// not exist reports LoadState "not-found".
	Inspect(ctx context.Context, unit string) (UnitInfo, error)
}

// UnitInfo is what systemd reports of one unit. ListAll fills the fields up
// to MainPID; Inspect fills them all, and leaves FileText empty for a unit
// file that is not a regular file every user may read.
type UnitInfo struct {
	ID                               string
	Description                      string
	LoadState, ActiveState, SubState string
	UnitFileState                    string // "enabled", "disabled", "static", ...; "" without a unit file
	MainPID                          uint32
	Names                            []string // every name of the unit, ID included
	Requires, Wants, Triggers        []string
	RefuseManualStart, AllowIsolate  bool
	// The action properties: "none" or what systemd does when the unit
	// succeeds, fails, times out on a job or hits its start limit.
	SuccessAction, FailureAction, JobTimeoutAction, StartLimitAction string
	MemoryCurrent, TasksCurrent                                      *uint64 // nil when systemd does not account them
	Restarts                                                         uint32
	StartedAt                                                        time.Time // zero when the unit never became active
	LeftInactive                                                     uint64    // InactiveExitTimestamp, microseconds; it advances whenever a job starts the unit
	FragmentPath                                                     string
	FileText                                                         string // the unit file, at most 64 KiB
	FileTruncated                                                    bool
	DropInPaths                                                      []string
}
