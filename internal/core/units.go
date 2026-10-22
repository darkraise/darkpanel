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
