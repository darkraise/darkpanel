package core

import "time"

// Host states.
const (
	HostPending  = "pending"
	HostEnrolled = "enrolled"
	HostRevoked  = "revoked"
)

// Release channels.
const (
	ChannelStable = "stable"
	ChannelBeta   = "beta"
)

// System and local actors added by ADR 0012.
const (
	ActorRollout        = "system:rollout"
	ActorUpdateSchedule = "system:update-schedule"
	ActorUnenrolled     = "local:root@unenrolled"
)

// Update transaction kinds.
const (
	UpdateKindUpdate   = "update"
	UpdateKindRollback = "rollback"
)

// Update transaction states.
const (
	UpdatePending     = "pending"
	UpdateDownloading = "downloading"
	UpdateStaged      = "staged"
	UpdateSwapped     = "swapped"
	UpdateRollingBack = "rolling-back"
	UpdateCommitted   = "committed"
	UpdateRolledBack  = "rolled-back"
	UpdateFailed      = "failed"
)

// Rollout states.
const (
	RolloutRunning   = "running"
	RolloutDraining  = "draining"
	RolloutSucceeded = "succeeded"
	RolloutFailed    = "failed"
	RolloutCancelled = "cancelled"
)

// Rollout host states.
const (
	RolloutHostWaiting   = "waiting"
	RolloutHostUpdating  = "updating"
	RolloutHostCommitted = "committed"
	RolloutHostFailed    = "failed"
	RolloutHostSkipped   = "skipped"
)

// ModuleState is one module's enablement on one host. A module can be used
// only when it is both enabled and available.
type ModuleState struct {
	ID        string     `json:"id" jsonschema_description:"Module id."`
	Version   int        `json:"version" jsonschema_description:"Module version compiled into this binary."`
	Enabled   bool       `json:"enabled" jsonschema_description:"Whether an operator enabled the module on the host."`
	Available bool       `json:"available" jsonschema_description:"Whether the module's migrations and preflight checks passed on the host."`
	Reason    *string    `json:"reason" jsonschema:"nullable" jsonschema_description:"Why the module is unavailable; null when it is available."`
	CheckedAt *time.Time `json:"checkedAt" jsonschema:"nullable" jsonschema_description:"When availability was last checked; null when it never was."`
}

// Host is a managed host. Online comes from the controller's live sessions
// and is never stored; the version and platform fields are null until the
// host's first Hello.
type Host struct {
	ID              string           `json:"id" jsonschema_description:"Host id."`
	Name            string           `json:"name" jsonschema_description:"Host name."`
	IsLocal         bool             `json:"isLocal" jsonschema_description:"Whether the host runs this controller."`
	State           string           `json:"state" jsonschema:"enum=pending,enum=enrolled,enum=revoked" jsonschema_description:"Enrollment state: pending until the agent enrolls, revoked after core.host.revoke."`
	Online          bool             `json:"online" jsonschema_description:"Whether the host's agent has a live session with the controller."`
	LastSeenAt      *time.Time       `json:"lastSeenAt" jsonschema:"nullable" jsonschema_description:"When the agent last answered; null when it never has."`
	AgentVersion    *string          `json:"agentVersion" jsonschema:"nullable" jsonschema_description:"Version of the agent's binary; null until its first Hello."`
	ProtocolVersion *int             `json:"protocolVersion" jsonschema:"nullable" jsonschema_description:"Agent protocol version; null until its first Hello."`
	OS              *string          `json:"os" jsonschema:"nullable" jsonschema_description:"Operating system the agent reports; null until its first Hello."`
	Arch            *string          `json:"arch" jsonschema:"nullable" jsonschema_description:"CPU architecture the agent reports; null until its first Hello."`
	Supported       bool             `json:"supported" jsonschema_description:"Whether the agent's version is inside the controller's support window; an unsupported agent accepts only update actions."`
	Modules         []ModuleState    `json:"modules" jsonschema_description:"Every compiled-in module's state on the host."`
	Ingestion       *IngestionStatus `json:"ingestion" jsonschema:"nullable" jsonschema_description:"Journal ingestion health; null until the controller first ingests the host's journal."`
}

// IngestionStatus is a host's journal ingestion health.
type IngestionStatus struct {
	OK    bool      `json:"ok" jsonschema_description:"Whether the last ingestion succeeded."`
	Since time.Time `json:"since" jsonschema_description:"When the current state began."`
	Error *string   `json:"error" jsonschema:"nullable" jsonschema_description:"The last ingestion error; null while ingestion succeeds."`
}

// HostList is the output of core.host.list/v1.
type HostList struct {
	Hosts []Host `json:"hosts" jsonschema_description:"Every host, the local host first, then by name."`
}

// HostCreateInput is the input of core.host.create/v1.
type HostCreateInput struct {
	Name              string `json:"name" jsonschema:"pattern=^[a-z0-9][a-z0-9.-]{0\\,62}$" jsonschema_description:"Host name: lower-case letters, digits, dots and hyphens, starting with a letter or digit."`
	ControllerAddress string `json:"controllerAddress,omitempty" jsonschema:"pattern=^(\\[[0-9A-Fa-f:.]+\\]|[A-Za-z0-9.-]+):[0-9]{1\\,5}$" jsonschema_description:"host:port the agent dials; the controller's --agent-advertise address when omitted."`
}

// HostTokenInput is the input of core.host.token/v1.
type HostTokenInput struct {
	HostID            string `json:"hostId" jsonschema:"minLength=1" jsonschema_description:"Id of the pending or revoked host to enroll."`
	ControllerAddress string `json:"controllerAddress,omitempty" jsonschema:"pattern=^(\\[[0-9A-Fa-f:.]+\\]|[A-Za-z0-9.-]+):[0-9]{1\\,5}$" jsonschema_description:"host:port the agent dials; the controller's --agent-advertise address when omitted."`
}

// HostIDInput is the input of core.host.revoke/v1 and core.host.delete/v1.
type HostIDInput struct {
	HostID string `json:"hostId" jsonschema:"minLength=1" jsonschema_description:"Host id."`
}

// EnrollmentGrant is a host with its one-time enrollment token, shown once.
type EnrollmentGrant struct {
	Host      Host      `json:"host" jsonschema_description:"The host the token enrolls."`
	Token     string    `json:"token" secret:"true" jsonschema_description:"Single-use enrollment token for darkpanel agent enroll; shown only once."`
	ExpiresAt time.Time `json:"expiresAt" jsonschema_description:"When the token stops working, one hour after it was made."`
}

// UpdateApplyInput is the input of core.update.apply/v1.
type UpdateApplyInput struct {
	Version string `json:"version" jsonschema:"pattern=^(0|[1-9][0-9]*)\\.(0|[1-9][0-9]*)\\.(0|[1-9][0-9]*)(-[0-9A-Za-z-]+(\\.[0-9A-Za-z-]+)*)?$" jsonschema_description:"Semantic version to install, such as 1.3.0 or 1.3.0-beta.1."`
	Channel string `json:"channel" jsonschema:"enum=stable,enum=beta" jsonschema_description:"Channel the version must be signed for."`
	Force   bool   `json:"force" jsonschema_description:"Allow a downgrade of at most one minor version."`
}

// UpdateRollbackInput is the input of core.update.rollback/v1.
type UpdateRollbackInput struct {
	Force bool `json:"force" jsonschema_description:"Roll back although an enrolled agent runs a newer version than the previous binary."`
}

// UpdateStatusInput is the input of core.update.status/v1; at most one field
// is set.
type UpdateStatusInput struct {
	OperationID string `json:"operationId,omitempty" jsonschema:"minLength=1" jsonschema_description:"Only the records of this update operation."`
	ChangeID    string `json:"changeId,omitempty" jsonschema:"minLength=1" jsonschema_description:"Only the records of the operation this change started."`
}

// ErrorDetail is the error of a failed update record, rollout or rollout
// host.
type ErrorDetail struct {
	Code    string `json:"code" jsonschema_description:"Stable error code, such as update.signature_invalid."`
	Message string `json:"message" jsonschema_description:"Human-readable explanation."`
}

// UpdateTransaction is one step of an update or rollback operation.
type UpdateTransaction struct {
	ID            string       `json:"id" jsonschema_description:"Transaction id."`
	OperationID   string       `json:"operationId" jsonschema_description:"Id of the operation: the id of its first transaction."`
	ParentID      *string      `json:"parentId" jsonschema:"nullable" jsonschema_description:"The previous step of a stepped update; null for the first."`
	Kind          string       `json:"kind" jsonschema:"enum=update,enum=rollback" jsonschema_description:"update or rollback."`
	FromVersion   string       `json:"fromVersion" jsonschema_description:"Version running before this step."`
	ToVersion     string       `json:"toVersion" jsonschema_description:"Version this step installs."`
	TargetVersion string       `json:"targetVersion" jsonschema_description:"Version the whole operation ends on."`
	State         string       `json:"state" jsonschema:"enum=pending,enum=downloading,enum=staged,enum=swapped,enum=rolling-back,enum=committed,enum=rolled-back,enum=failed" jsonschema_description:"Transaction state."`
	Error         *ErrorDetail `json:"error" jsonschema:"nullable" jsonschema_description:"Why the step failed or rolled back; null otherwise."`
	StartedAt     time.Time    `json:"startedAt" jsonschema_description:"When the step started."`
	UpdatedAt     time.Time    `json:"updatedAt" jsonschema_description:"When the step last changed state."`
}

// UpdateStatus is the output of core.update.status/v1.
type UpdateStatus struct {
	Transactions []UpdateTransaction `json:"transactions" jsonschema_description:"The matching records, newest first."`
}

// UpdateSchedule is a weekly two-hour update window in UTC.
type UpdateSchedule struct {
	Days []string `json:"days" jsonschema:"minItems=1,uniqueItems=true,enum=mon,enum=tue,enum=wed,enum=thu,enum=fri,enum=sat,enum=sun" jsonschema_description:"Days the window opens."`
	Time string   `json:"time" jsonschema:"pattern=^([01][0-9]|2[0-3]):[0-5][0-9]$" jsonschema_description:"UTC time the window opens, HH:MM."`
}

// UpdatePolicy is a host's update channel and schedule: the input and output
// of core.host.update-policy/v1.
type UpdatePolicy struct {
	HostID   string          `json:"hostId" jsonschema:"minLength=1" jsonschema_description:"Host id."`
	Channel  string          `json:"channel" jsonschema:"enum=stable,enum=beta" jsonschema_description:"Release channel the host follows."`
	Schedule *UpdateSchedule `json:"schedule" jsonschema:"nullable" jsonschema_description:"Weekly update window; null for on-demand updates only."`
}

// RolloutInput is the input of core.update.rollout/v1.
type RolloutInput struct {
	Hosts   []string `json:"hosts" jsonschema:"minItems=1,uniqueItems=true" jsonschema_description:"Ids of the hosts to update, in order; the controller's own host always goes first."`
	Version string   `json:"version" jsonschema:"pattern=^(0|[1-9][0-9]*)\\.(0|[1-9][0-9]*)\\.(0|[1-9][0-9]*)(-[0-9A-Za-z-]+(\\.[0-9A-Za-z-]+)*)?$" jsonschema_description:"Semantic version to roll out."`
	Channel string   `json:"channel" jsonschema:"enum=stable,enum=beta" jsonschema_description:"Channel the version must be signed for."`
}

// RolloutCancelInput is the input of core.update.rollout-cancel/v1.
type RolloutCancelInput struct {
	RolloutID string `json:"rolloutId" jsonschema:"minLength=1" jsonschema_description:"Rollout id."`
	Force     bool   `json:"force" jsonschema_description:"Mark the rollout cancelled at once, without waiting for a host's update to finish."`
}

// RolloutsInput is the input of core.update.rollouts/v1.
type RolloutsInput struct {
	Limit int `json:"limit,omitempty" jsonschema:"minimum=1,maximum=100" jsonschema_description:"Most rollouts to return, 1 to 100; 20 when omitted."`
}

// RolloutHost is one host of a rollout.
type RolloutHost struct {
	HostID      string       `json:"hostId" jsonschema_description:"Host id."`
	Position    int          `json:"position" jsonschema_description:"Order in which the host is updated, from 0."`
	State       string       `json:"state" jsonschema:"enum=waiting,enum=updating,enum=committed,enum=failed,enum=skipped" jsonschema_description:"Host state within the rollout."`
	PlanID      *string      `json:"planId" jsonschema:"nullable" jsonschema_description:"Plan of the host's core.update.apply; null before it is planned."`
	OperationID *string      `json:"operationId" jsonschema:"nullable" jsonschema_description:"The host's update operation; null before it starts."`
	Error       *ErrorDetail `json:"error" jsonschema:"nullable" jsonschema_description:"Why the host failed; null otherwise."`
}

// Rollout is an update of selected hosts, one host at a time.
type Rollout struct {
	ID        string        `json:"id" jsonschema_description:"Rollout id."`
	Version   string        `json:"version" jsonschema_description:"Version being rolled out."`
	Channel   string        `json:"channel" jsonschema:"enum=stable,enum=beta" jsonschema_description:"Channel the version is signed for."`
	State     string        `json:"state" jsonschema:"enum=running,enum=draining,enum=succeeded,enum=failed,enum=cancelled" jsonschema_description:"Rollout state; draining means dispatch stopped while a host's update is still running."`
	Error     *ErrorDetail  `json:"error" jsonschema:"nullable" jsonschema_description:"Why the rollout failed; null otherwise."`
	CreatedAt time.Time     `json:"createdAt" jsonschema_description:"When the rollout was created."`
	UpdatedAt time.Time     `json:"updatedAt" jsonschema_description:"When the rollout last changed."`
	Hosts     []RolloutHost `json:"hosts" jsonschema_description:"The rollout's hosts in order."`
}

// RolloutList is the output of core.update.rollouts/v1.
type RolloutList struct {
	Rollouts []Rollout `json:"rollouts" jsonschema_description:"Rollouts, newest first."`
}
