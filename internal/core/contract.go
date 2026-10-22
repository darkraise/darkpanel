// Package core is the module contract every Darkpanel component builds on:
// action and resource definitions, the registry, and the interfaces between
// the controller, the agent, the lifecycle engine and the API (spec section
// 2a). Changing a declaration here changes a contract other parts depend on.
package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/darkraise/darkpanel/internal/core/store"
)

// ModuleIDCore is the module id of the core's own actions (spec section 2).
const ModuleIDCore = "core"

// Side is where an action executes.
type Side string

// The two sides.
const (
	SideController Side = store.SideController
	SideAgent      Side = store.SideAgent
)

// Class is an action's change class (shared contracts).
type Class string

// The action classes. A composite is a controller-side action whose apply
// runs steps: child changes of other modules' actions.
const (
	ClassRead         Class = "read"
	ClassRevertible   Class = "revertible"
	ClassCompensable  Class = "compensable"
	ClassIrreversible Class = "irreversible"
	ClassComposite    Class = "composite"
)

// Unwind is a composite's failure direction.
type Unwind string

// The two directions.
const (
	UnwindReverse Unwind = "reverse" // adding composites: undo the applied steps
	UnwindForward Unwind = "forward" // removal composites: never unwind; repair repeats
)

// Scope is what a principal may do (spec section 10).
type Scope string

// The three scopes, in increasing order of power.
const (
	ScopeRead    Scope = "read"
	ScopeOperate Scope = "operate"
	ScopeAdmin   Scope = "admin"
)

// Allows reports whether s includes need.
func (s Scope) Allows(need Scope) bool {
	rank := map[Scope]int{ScopeRead: 1, ScopeOperate: 2, ScopeAdmin: 3}
	// An unknown scope on either side denies: a mistyped required scope must fail closed.
	return rank[s] > 0 && rank[need] > 0 && rank[s] >= rank[need]
}

// ActionRef names one version of an action.
type ActionRef struct {
	Name    string
	Version int
}

// String renders the reference as "<name>/v<version>".
func (r ActionRef) String() string { return fmt.Sprintf("%s/v%d", r.Name, r.Version) }

// MarshalText renders the reference as String does, so step lists and DTOs
// carry it as one string.
func (r ActionRef) MarshalText() ([]byte, error) { return []byte(r.String()), nil }

// UnmarshalText parses "<name>/v<version>", splitting at the last slash.
func (r *ActionRef) UnmarshalText(text []byte) error {
	i := strings.LastIndex(string(text), "/")
	version, err := ParseVersion(string(text[i+1:]))
	if i <= 0 || err != nil {
		return fmt.Errorf("core: action reference %q: want <name>/v<version>, such as core.update.apply/v1", text)
	}
	*r = ActionRef{Name: string(text[:i]), Version: version}
	return nil
}

// ParseVersion parses a path segment such as "v1" into 1.
func ParseVersion(s string) (int, error) {
	n, err := strconv.Atoi(strings.TrimPrefix(s, "v"))
	if !strings.HasPrefix(s, "v") || err != nil || n < 1 || strconv.Itoa(n) != s[1:] {
		return 0, fmt.Errorf("version %q: want v followed by a positive integer, such as v1", s)
	}
	return n, nil
}

// Principal is who is calling.
type Principal struct {
	Actor   string // shared-contract actor, e.g. "user:<id>", "token:<id>", "local:root@<host id>", "system:recovery"
	UserID  string // empty for local and system actors
	TokenID string // set only for token principals
	Scope   Scope  // local break-glass and system actors carry ScopeAdmin
	Source  string // client address, or "socket"
}

// Env is what a handler may use. HostID is empty for controller-side actions;
// Agents is nil on the agent side. ChangeID is the change a call acts on
// (Apply, Verify, Revert, Observe, Inverse, Recover and Task) and is empty in
// a plan or a read, because a plan has no change yet. Effects is that
// change's journal and is set whenever ChangeID is. Call is set at plan and
// at every later call of the change.
type Env struct {
	HostID    string
	ChangeID  string
	Principal Principal
	Call      CallContext
	Effects   EffectJournal
	DB        *store.DB
	Exec      Runner
	Files     FileWriter
	Sealer    Sealer
	Clock     Clock
	Log       *slog.Logger
	Agents    HostDirectory
}

// CallContext is why a change runs. It comes from the controller, never from
// input: the API and the agent socket refuse it, and handlers that depend on
// provenance read only Env.Call.
type CallContext struct {
	ParentChangeID string   // the composite running this as a step; "" for a top-level change
	ParentAction   string   // that composite's action name
	StepID         string   // the step id within the parent
	RepairOf       string   // the change this one repairs; derived by the engine from the stored plan, never sent
	Retired        []string // exposure ids the parent has retired
}

// PlanOptions are the core-level fields of a plan request. Call is set only
// by the controller (dispatcher, composite runner, repair relay); the API
// rejects it.
type PlanOptions struct {
	Call *CallContext // controller only; nil for a top-level change. RepairOf is ignored.
}

// definiteError marks a refusal that guarantees nothing committed.
type definiteError struct{ err error }

func (e *definiteError) Error() string { return e.err.Error() }

func (e *definiteError) Unwrap() error { return e.err }

// Definite marks err as a refusal that guarantees the call committed
// nothing (a provider's 4xx validation answer, a precondition the module
// checked before calling). nil stays nil, and errors.Is, errors.As and
// errcode.From still see err through the mark.
func Definite(err error) error {
	if err == nil {
		return nil
	}
	return &definiteError{err: err}
}

// IsDefinite reports whether err, or an error it wraps, was marked by
// Definite.
func IsDefinite(err error) bool {
	var d *definiteError
	return errors.As(err, &d)
}

// HostDirectory finds the agent of a host; dispatch implements it.
type HostDirectory interface {
	Agent(ctx context.Context, hostID string) (AgentClient, error)
}

// DiffLine is one line of a plan's human-readable diff.
type DiffLine struct {
	Op   string `json:"op"` // "+", "-" or " "
	Text string `json:"text"`
}

// Guard is one safety check a plan ran.
type Guard struct {
	ID      string `json:"id"`
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}

// PlanOutcome is what a handler's Plan returns.
type PlanOutcome struct {
	Diff         []DiffLine
	Guards       []Guard
	StateHash    string
	Confirmation string // irreversible actions only: the phrase apply must repeat
}

// ActionSpec is an action's declaration (ADR 0002).
type ActionSpec struct {
	Name, Summary string
	Version       int
	Class         Class
	Side          Side
	BreakGlass    bool
	LockKeys      []string
	CompensatedBy string        // compensable only, an action name
	ForwardOnly   bool          // compensable: never reversed; requires Converges
	Converges     bool          // apply run again with the same input, after a partial or complete apply, ends in the same state and plans an empty diff when the effect is present
	RepairedBy    string        // revertible only, required: an action of the same module
	ApplyTimeout  time.Duration // one budget for apply and verify; 0 means 30 s; above 45 s requires LongRunning; composites: computed at plan
	LongRunning   bool          // apply answers once the intent is persisted
	Task          bool          // the host sequences run in transient units; set by DefineTask
	Unwind        Unwind        // composite only
}

// Reader is the handler of a read action.
type Reader[In, Out any] interface {
	Read(ctx context.Context, env Env, in In) (Out, error)
}

// Mutator is the handler of a compensable or irreversible action.
type Mutator[In, Out any] interface {
	Plan(ctx context.Context, env Env, in In) (PlanOutcome, error)
	Apply(ctx context.Context, env Env, in In) (Out, error)
	// Verify checks that Apply took effect. It must only read the host. It
	// runs under the change's deadline, the action's ApplyTimeout, and is
	// never abandoned: a Verify that does not return keeps its change
	// applying and its locks held. Startup recovery also calls it, under a
	// 30 s bound, for a change cut off during Apply, with out the zero value
	// because no output was recorded, so Verify must check the host from in
	// alone.
	Verify(ctx context.Context, env Env, in In, out Out) error
}

// Reverter is the handler of a revertible action. Revert reports what it did
// to each effect; it never writes the journal itself.
type Reverter[In, Out any] interface {
	Mutator[In, Out]
	Snapshot(ctx context.Context, env Env, in In) ([]byte, error)
	Revert(ctx context.Context, env Env, snapshot []byte) (RevertOutcome, error)
}

// RevertOutcome is what a revert did. The engine, not the handler, writes
// the journal: it applies Effects in the transaction that records the
// revert.
type RevertOutcome struct {
	Effects  []EffectOutcome // one per effect the revert acted on or left
	Override bool            // the connectivity exception: the snapshot was restored over unresolved effects
	Detail   json.RawMessage // Override: what was overwritten, shown to the operator
}

// EffectOutcome is what a revert did to one effect.
type EffectOutcome struct {
	Seq     int
	State   EffectState // "undone" | "unresolved"
	Reason  string      // unresolved: "conflict" (the resource differed from Post) or "uncertain"
	Current *StateDoc   // unresolved: what the revert observed and left in place
}

// TaskMutator is the handler of a task action. Task runs inside the apply
// unit; Plan and Verify run in the agent; Observe and Inverse run wherever
// the settle or undo run that calls them executes (in the agent, or inside
// an undo unit); a module Recoverer runs inside a recover unit.
type TaskMutator[In, Out any] interface {
	Plan(ctx context.Context, env Env, in In) (PlanOutcome, error)
	Task(ctx context.Context, env Env, in In) (Out, error)
	Verify(ctx context.Context, env Env, in In, out Out) error
}

// Step is one step of a composite.
type Step struct {
	ID     string          `json:"id"`              // [a-z][a-z0-9]*, unique in the composite
	Host   string          `json:"host"`            // host id; "" for a controller-side action
	Action ActionRef       `json:"action"`          // "<name>/v<version>"
	Input  json.RawMessage `json:"input"`           // the child's input, with ${input...} and ${steps...} references
	After  []string        `json:"after,omitempty"` // steps this one follows
}

// Composer is the handler of a composite action. The engine, not the
// module, plans, applies and recovers a composite.
type Composer[In, Out any] interface {
	// Steps returns the steps for in. It reads only in and the controller
	// database, never a host.
	Steps(ctx context.Context, env Env, in In) ([]Step, error)
	// Output builds the composite's output from its steps' redacted
	// outputs, keyed by step id; read steps are not among them.
	Output(ctx context.Context, env Env, in In, steps map[string]json.RawMessage) (Out, error)
}

// EffectKind is what an effect does to its resource.
type EffectKind string // "create" | "modify" | "delete"

// The effect kinds.
const (
	EffectCreate EffectKind = "create"
	EffectModify EffectKind = "modify"
	EffectDelete EffectKind = "delete"
)

// EffectState is where an effect stands in the journal. Skipped, undone,
// accepted and abandoned are terminal.
type EffectState string

// The effect states.
const (
	EffectIntended   EffectState = "intended"
	EffectMade       EffectState = "made"
	EffectSkipped    EffectState = "skipped"
	EffectUnresolved EffectState = "unresolved"
	EffectUndoing    EffectState = "undoing"
	EffectUndone     EffectState = "undone"
	EffectAccepted   EffectState = "accepted"
	EffectAbandoned  EffectState = "abandoned"
)

// Reasons of an unresolved effect.
const (
	ReasonUncertain = "uncertain"
	ReasonAmbiguous = "ambiguous"
	ReasonConflict  = "conflict"
)

// StateDoc is the comparable form of a resource's state. State holds the
// fields an action sets and its inverse restores; Version holds the fields
// that change on every write or restart (a start time, an endpoint id, an
// ETag, modified_on) and every State field an inverse cannot restore. It
// never holds a secret. State "null" means the resource does not exist.
type StateDoc struct {
	State   json.RawMessage `json:"state"`
	Version json.RawMessage `json:"version,omitempty"`
}

// RecoveryID is how an observation finds exactly the object a creation made.
type RecoveryID struct {
	Kind  string `json:"kind"` // "name" | "marker" | "idempotency"
	Value string `json:"value"`
}

// Effect is one row of core_effects.
type Effect struct {
	ChangeID        string
	Seq             int // 1, 2, ... in the order intended
	Kind            EffectKind
	Resource        string        // "<module>.<noun>:<identity>"; a creation: "<module>.<noun>:?<recovery value>" until made
	Recovery        *RecoveryID   // creations only
	Local           bool          // the forward call ends with the process that makes it
	InverseLocal    bool          // the same for the inverse call
	CommitWait      time.Duration // a call that is not Local: how long the other side may still commit it
	CommitBy        time.Time     // no resolution before this time: the forward call's (or, while undoing, the inverse's) start plus CommitWait
	InverseRecovery *RecoveryID   // an inverse that creates (the undo of a deletion): how an observation finds the recreated object
	UndoResource    string        // the object the inverse left, as Inverse returned it
	Before          StateDoc      // the state the call expects to find
	After           *StateDoc     // the state the call leaves; nil when it cannot be known before the call
	Post            *StateDoc     // made: the state an undo must still find
	UndoTo          *StateDoc     // undoing: the state the inverse leaves
	Current         *StateDoc     // unresolved and accepted: what was observed
	State           EffectState
	Reason          string // unresolved: "uncertain" | "ambiguous" | "conflict"
	Resolution      string // the last resolution ("accept" | "reconcile"), with ResolvedBy and ResolvedAt
	ResolvedBy      string
	ResolvedAt      time.Time
}

// EffectJournal is the handler's view of its change's journal. Every
// method commits before it returns; an illegal transition is an error that
// writes nothing.
type EffectJournal interface {
	// Intend records e as intended and returns its seq. secret, if any, is
	// sealed with associated data "core_effects.secret_ct.<change id>.<seq>".
	Intend(ctx context.Context, e Effect, secret []byte) (seq int, err error)
	// Made records that the call succeeded; resource names the object
	// (a creation's server-assigned id) and post its state now.
	Made(ctx context.Context, seq int, resource string, post StateDoc) error
	// Skipped records that the call was not made, or was refused with an
	// answer that guarantees nothing committed (a Definite error).
	Skipped(ctx context.Context, seq int) error
	List(ctx context.Context) ([]Effect, error)
	Secret(ctx context.Context, seq int) ([]byte, error)
}

// Observation is what an Observer found for one effect.
type Observation struct {
	Current    StateDoc
	Resource   string      // a creation: the matched object's identity when Matches is 1
	Matches    int         // a creation: objects its RecoveryID finds; 1 for modify and delete
	Candidates []Candidate // a creation with Matches > 1: every match, for the operator
	Exclusive  bool        // the identity tells this object apart from every other
	Allowed    []string    // resolutions the module permits for this effect; nil means both
}

// Candidate is one object a creation's recovery identity matched.
type Candidate struct {
	Resource string   `json:"resource"`
	State    StateDoc `json:"state"`
}

// Observer reads the resource of one effect. It only reads. For an effect
// that is undoing or was undone, it looks for e.UndoResource or, when that
// is empty, the object e.InverseRecovery finds.
type Observer interface {
	Observe(ctx context.Context, env Env, e Effect) (Observation, error)
}

// Resolver is optional. Resolved runs inside the resolution transaction,
// after the effect's new state is written and before the commit, so a
// module can move its own bookkeeping row with the resolution. It writes
// only the module's own tables; an error rolls the resolution back.
type Resolver interface {
	Resolved(ctx context.Context, tx *sql.Tx, e Effect, resolution string) error
}

// Undoer is the undo handler of a compensable action. Inverse makes the
// inverse call of one made effect, leaving e.Resource in e.UndoTo, and
// touches no other resource. A Definite error means it changed nothing.
// An inverse that creates (the undo of a deletion) returns the new
// object's identity; the engine records it as e.UndoResource.
type Undoer interface {
	Observer
	Inverse(ctx context.Context, env Env, e Effect) (InverseResult, error)
}

// InverseResult is what an inverse call left.
type InverseResult struct {
	Resource string // an inverse that creates: the recreated object; "" otherwise
}

// RecoveryOutcome is a recovery handler's verdict on its change.
type RecoveryOutcome struct {
	State  string          // "completed" | "failed" | "rolled_back" | "needs_repair"
	Output json.RawMessage // completed: the action's output
	Reason string          // failed, needs_repair: shown to the operator
}

// Values of RecoveryOutcome.State.
const (
	RecoveryCompleted   = "completed"
	RecoveryFailed      = "failed"
	RecoveryRolledBack  = "rolled_back"
	RecoveryNeedsRepair = "needs_repair"
)

// Recoverer finishes or rolls back an action's own transaction after a
// crash. env.ChangeID is the change; env.Effects its journal.
type Recoverer interface {
	Recover(ctx context.Context, env Env) (RecoveryOutcome, error)
}

// ErasedHandler is a handler working on JSON. DefineRead, DefineMutating,
// DefineRevertible, DefineComposite and DefineTask build it; the fields a
// class does not use are nil.
type ErasedHandler struct {
	Read     func(ctx context.Context, env Env, in json.RawMessage) (json.RawMessage, error)
	Plan     func(ctx context.Context, env Env, in json.RawMessage) (PlanOutcome, error)
	Apply    func(ctx context.Context, env Env, in json.RawMessage) (json.RawMessage, error)
	Verify   func(ctx context.Context, env Env, in, out json.RawMessage) error
	Snapshot func(ctx context.Context, env Env, in json.RawMessage) ([]byte, error)
	Revert   func(ctx context.Context, env Env, snapshot []byte) (RevertOutcome, error)
	Observe  func(ctx context.Context, env Env, e Effect) (Observation, error)
	Resolved func(ctx context.Context, tx *sql.Tx, e Effect, resolution string) error
	Inverse  func(ctx context.Context, env Env, e Effect) (InverseResult, error)
	Recover  func(ctx context.Context, env Env) (RecoveryOutcome, error)
	Task     func(ctx context.Context, env Env, in json.RawMessage) (json.RawMessage, error)
	Steps    func(ctx context.Context, env Env, in json.RawMessage) ([]Step, error)
	Output   func(ctx context.Context, env Env, in json.RawMessage, steps map[string]json.RawMessage) (json.RawMessage, error)
}

// ActionDef is a type-erased action as the registry stores it.
type ActionDef struct {
	Spec       ActionSpec
	InputType  reflect.Type
	OutputType reflect.Type
	Handler    ErasedHandler
}

// Ref is the action's name and version.
func (a ActionDef) Ref() ActionRef { return ActionRef{a.Spec.Name, a.Spec.Version} }

// ResourceDef is a read endpoint without input, named "<module>.<resource>",
// whose schema version is fixed at 1.
type ResourceDef struct {
	Name       string
	OutputType reflect.Type
	Read       func(ctx context.Context, env Env) (json.RawMessage, error)
}

// PreflightCheck is a host check; the error text is the reason shown when it
// fails.
type PreflightCheck struct {
	ID  string
	Run func(ctx context.Context, env Env) error
}

// The kinds of an owned path. Only files and blocks are written through
// FileWriter; directories, users and units are the installer's, recorded so
// that uninstall removes them.
const (
	OwnedFile  = "file"
	OwnedBlock = "block"
	OwnedDir   = "dir"
	OwnedUser  = "user"
	OwnedUnit  = "unit"
)

// OwnedPath is something a module or the core creates on the host and
// uninstall removes (ADR 0009). For OwnedUser, Path is the user name.
type OwnedPath struct {
	Path          string
	Kind          string // one of the Owned kinds
	BlockIDs      []string
	CommentPrefix string // default "#"
}

// Manifest is everything a module declares (R5).
type Manifest struct {
	ID             string
	Version        int
	Actions        []ActionDef
	Resources      []ResourceDef
	Settings       reflect.Type
	Preflight      []PreflightCheck
	OwnedPaths     []OwnedPath
	Migrations     map[Side]fs.FS
	DefaultEnabled bool
}

// Module is a compiled-in module.
type Module interface {
	Manifest() Manifest
}

// Uninstaller is the optional agent-side uninstall step of a module, found
// with a type assertion on the Module. It runs before uninstall removes the
// module's marked blocks and files; with keepData it leaves the module's data
// in place for a reinstall. Uninstall runs again when an uninstall that
// stopped at a later step is run anew, so it must succeed on a host where it
// already ran.
type Uninstaller interface {
	Uninstall(ctx context.Context, env Env, keepData bool) error
}

// Sealer encrypts and decrypts secrets with associated data (ADR 0005).
type Sealer interface {
	Seal(ad string, plaintext []byte) ([]byte, error)
	Open(ad string, ciphertext []byte) ([]byte, error)
	Derive(label string) []byte
}

// Validator checks an action's input against its schema; it returns an
// *errcode.Error with field errors.
type Validator interface {
	ValidateInput(def ActionDef, raw json.RawMessage) error
}

// Clock is the time source; tests use coretest.FakeClock.
type Clock interface {
	Now() time.Time
	AfterFunc(d time.Duration, f func()) Timer
}

// Timer is a pending AfterFunc.
type Timer interface {
	Stop() bool
}

// SystemClock is the production Clock. time.AfterFunc measures on the
// monotonic clock, so a wall-clock step never fires or delays a revert timer.
type SystemClock struct{}

// Now returns the current time.
func (SystemClock) Now() time.Time { return time.Now() }

// AfterFunc calls f in its own goroutine after d.
func (SystemClock) AfterFunc(d time.Duration, f func()) Timer { return time.AfterFunc(d, f) }

// Cmd is a program to run with an argument vector (ADR 0009).
type Cmd struct {
	Path  string
	Args  []string
	Stdin []byte
}

// CmdResult is a finished program's output.
type CmdResult struct {
	Stdout, Stderr []byte
	ExitCode       int
}

// Runner runs allowlisted programs.
type Runner interface {
	Run(ctx context.Context, cmd Cmd) (CmdResult, error)
}

// FileWriter writes manifest-declared files and marked blocks.
type FileWriter interface {
	WriteFile(ctx context.Context, module, path string, data []byte, mode fs.FileMode) error
	ReplaceBlock(ctx context.Context, module, path, blockID string, content []byte) error
	RemoveBlock(ctx context.Context, module, path, blockID string) error
	Remove(ctx context.Context, module, path string) error
}

// OwnedEntry is one path or block Darkpanel wrote.
type OwnedEntry struct {
	Path, Module, Kind, BlockID string
	CreatedAt                   time.Time
}

// Ownership records what Darkpanel wrote, for uninstall.
type Ownership interface {
	Record(ctx context.Context, e OwnedEntry) error
	Forget(ctx context.Context, module, path, blockID string) error
	List(ctx context.Context) ([]OwnedEntry, error)
}

// JournalEntry is one lifecycle event of a change.
type JournalEntry struct {
	Seq       int64 // assigned by the agent journal; 0 on the controller side
	At        time.Time
	Actor     string
	Action    string
	Version   int
	Phase     string // "apply", "confirm", "revert", "recover", "interrupt"
	ChangeID  string
	Input     json.RawMessage // already redacted
	Result    string          // "ok", "error"
	ErrorCode string
	Source    string // the acting principal's Source; empty for system actors
}

// Journal appends lifecycle events inside the change's transaction.
type Journal interface {
	Append(ctx context.Context, tx *sql.Tx, e JournalEntry) error
}

// AuditEntry is one row of the audit log (spec section 11).
type AuditEntry struct {
	ID, Actor, HostID, Action, Phase, ChangeID, Result, ErrorCode, Source string
	At                                                                    time.Time
	Version                                                               int
	Seq                                                                   int64 // the agent journal sequence, 0 for controller-side rows
	Input                                                                 json.RawMessage
}

// AuditQuery filters core.audit.list.
type AuditQuery struct {
	Limit                         int
	Before, Actor, HostID, Action string
}

// Audit is the controller's audit log.
type Audit interface {
	Journal
	Record(ctx context.Context, e AuditEntry) error
	Ingest(ctx context.Context, hostID string, entries []JournalEntry) error
	List(ctx context.Context, q AuditQuery) (entries []AuditEntry, nextBefore string, err error)
}

// PlanView is a stored plan as callers see it.
type PlanView struct {
	PlanID, HostID, Action, InputHash, Confirmation string
	Version                                         int
	Diff                                            []DiffLine
	Guards                                          []Guard
	ExpiresAt                                       time.Time
}

// ChangeError is a failed change's error.
type ChangeError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Cause   string `json:"cause,omitempty"` // the code of the error that led to needs_repair
}

// ChangeView is a change as callers see it.
type ChangeView struct {
	ID, HostID, Action, Actor, CompensatedBy string
	Version                                  int
	Class                                    Class
	State                                    string
	CreatedAt                                time.Time
	UpdatedAt                                time.Time
	RevertDeadline                           *time.Time
	Output                                   json.RawMessage // secret fields present only in the apply response that created them
	Error                                    *ChangeError
	ParentChangeID                           string          // the composite this change is a step of
	Owner                                    string          // the mechanism acting on the change; "" when none
	RepairedBy                               string          // the repair change running under this change's locks
	RepairOutcome                            string          // a repaired change: "restored" or "kept"
	Override                                 json.RawMessage // what a revert's connectivity exception overwrote
}

// The change states (spec section 3).
const (
	StateApplying    = "applying"
	StatePending     = "pending"
	StateConfirmed   = "confirmed"
	StateReverted    = "reverted"
	StateCompleted   = "completed"
	StateFailed      = "failed"
	StateInterrupted = "interrupted"
)

// The change states of undo, repair, recovery and composites. Releasing is a
// composite's only.
const (
	StateAwaitingConfirmation = "awaiting_confirmation"
	StateUndoing              = "undoing"
	StateNeedsRepair          = "needs_repair"
	StateRecovering           = "recovering"
	StateRepaired             = "repaired"
	StateKept                 = "kept"
	StateAbandoned            = "abandoned"
	StateQuarantined          = "quarantined"
	StateReleasing            = "releasing"
)

// Owners of core_changes.recovery_owner; "" is unowned. One mechanism at a
// time acts on an unfinished change, and only the transaction recording its
// outcome ends its ownership.
const (
	OwnerEngine    = "engine"
	OwnerComposite = "composite"
	OwnerUndo      = "undo"
	OwnerModule    = "module"
	OwnerRepair    = "repair"
	OwnerResolve   = "resolve"
)

// Composite step states.
const (
	StepPending    = "pending"
	StepPlanning   = "planning"
	StepPlanned    = "planned"
	StepApplying   = "applying"
	StepApplied    = "applied"
	StepConfirming = "confirming"
	StepConfirmed  = "confirmed"
	StepRead       = "read"
	StepUndoing    = "undoing"
	StepUndone     = "undone"
	StepReleased   = "released"
)

// Values of PlanLookup.Status, RepairResult outcomes, resolutions and task
// unit operations.
const (
	LookupAbsent     = "absent"
	LookupRetired    = "retired"
	LookupRunning    = "running"
	LookupRecovering = "recovering"
	LookupTerminal   = "terminal"

	RepairRestored = "restored"
	RepairKept     = "kept"

	ResolutionAccept    = "accept"
	ResolutionReconcile = "reconcile"

	TaskOpApply   = "apply"
	TaskOpUndo    = "undo"
	TaskOpRecover = "recover"
)

// ApplyOptions carry apply's per-class options.
type ApplyOptions struct {
	ConfirmTimeout time.Duration
	Confirmation   string
	ConfirmNow     bool
	Call           *CallContext // set only by the controller (dispatcher, composite runner)
}

// PlanLookup is what became of a plan, for a composite following its child.
// Running: owned by engine, undo or repair. Recovering: owned by module.
// Terminal: no mechanism owns it, and State says which state.
type PlanLookup struct {
	Status        string          `json:"status"` // "absent" | "retired" | "running" | "recovering" | "terminal"
	ChangeID      string          `json:"changeId,omitempty"`
	State         string          `json:"state,omitempty"`
	Owner         string          `json:"owner,omitempty"`    // recovery_owner
	Deadline      *time.Time      `json:"deadline,omitempty"` // a pending revertible child's revert deadline
	Made          int             `json:"made"`               // effects made or undoing
	Unresolved    int             `json:"unresolved"`
	RepairOutcome string          `json:"repairOutcome,omitempty"`
	Output        json.RawMessage `json:"output,omitempty"`  // redacted
	Private       json.RawMessage `json:"private,omitempty"` // only with ?private=1 while the private output is kept
	Error         *ChangeError    `json:"error,omitempty"`
}

// RepairRequest asks for the repair of a change in needs_repair.
type RepairRequest struct {
	Input json.RawMessage `json:"input,omitempty"` // RepairedBy plans only; never carries repairs
	Step  string          `json:"step,omitempty"`  // composite step decision
	As    string          `json:"as,omitempty"`    // "reverted" | "applied"
}

// RepairResult is a repair's answer: the change an in-place repair works on,
// or the plan the client applies through the ordinary apply route.
type RepairResult struct {
	Change *ChangeView `json:"change,omitempty"`
	Plan   *PlanView   `json:"plan,omitempty"`
}

// ReleaseResult is a release's answer.
type ReleaseResult struct {
	Change    ChangeView   `json:"change"`
	Abandoned []EffectView `json:"abandoned"`
}

// ResolveRequest is an operator's resolution of an unresolved effect.
type ResolveRequest struct {
	Resolution string `json:"resolution"` // "accept" | "reconcile"
	Resource   string `json:"resource,omitempty"`
}

// ObserveResult is the read-only preview of a resolution.
type ObserveResult struct {
	Effect      EffectView      `json:"effect"`
	Observation ObservationView `json:"observation"`
	Allowed     []string        `json:"allowed"`
}

// EffectView is an effect as callers see it, never its secret.
type EffectView struct {
	ChangeID     string      `json:"changeId"`
	Seq          int         `json:"seq"`
	Kind         EffectKind  `json:"kind"`
	Resource     string      `json:"resource"`
	Local        bool        `json:"local"`
	InverseLocal bool        `json:"inverseLocal"`
	CommitBy     *time.Time  `json:"commitBy"`
	State        EffectState `json:"state"`
	Reason       string      `json:"reason"`
	Before       StateDoc    `json:"before"`
	After        *StateDoc   `json:"after"`
	Post         *StateDoc   `json:"post"`
	UndoTo       *StateDoc   `json:"undoTo"`
	UndoResource string      `json:"undoResource"`
	Current      *StateDoc   `json:"current"`
	Resolution   string      `json:"resolution"`
	ResolvedBy   string      `json:"resolvedBy"`
	ResolvedAt   *time.Time  `json:"resolvedAt"`
}

// ObservationView is an Observation as callers see it.
type ObservationView struct {
	Current    StateDoc    `json:"current"`
	Resource   string      `json:"resource"`
	Matches    int         `json:"matches"`
	Candidates []Candidate `json:"candidates"`
	Exclusive  bool        `json:"exclusive"`
}

// StepView is a composite step as callers see it, never its inputs or
// outputs.
type StepView struct {
	ID                    string       `json:"id"`
	Ordinal               int          `json:"ordinal"`
	Host                  string       `json:"host"`
	Action                ActionRef    `json:"action"`
	State                 string       `json:"state"`
	Attempt               int          `json:"attempt"`
	ChildChangeID         string       `json:"childChangeId"`
	EarlierChildChangeIDs []string     `json:"earlierChildChangeIds"`
	ChildDeadline         *time.Time   `json:"childDeadline"`
	Error                 *ChangeError `json:"error"`
}

// ChangeFilter filters change listings.
type ChangeFilter struct {
	State, HostID, Before string
	Limit                 int
}

// ModuleStatus is one module's availability on a host.
type ModuleStatus struct {
	ID        string
	Version   int
	Available bool
	Reason    string
}

// AgentHello is what an agent reports about itself (ADR 0003).
type AgentHello struct {
	ProtocolVersion int
	BinaryVersion   string
	OS              string
	Arch            string
	HostID          string
	Modules         []ModuleStatus
	// JournalEpoch is a ULID that changes only when the agent's journal is
	// recreated, so the controller can tell a reset sequence from an old one.
	JournalEpoch string
	// ControllerConnected is nil when the agent is not enrolled.
	ControllerConnected *bool
	// SchemaHashes are keyed "<action name>@<version>" for every agent-side
	// action version the agent serves.
	SchemaHashes map[string]SchemaHash
}

// SchemaHash identifies the input and output schemas of one action version:
// each is the lower-case hex SHA-256 of the schema re-encoded by
// encoding/json (ADR 0012).
type SchemaHash struct {
	Input, Output string
}

// AgentClient is the controller's view of one agent; in M1 the in-process
// client, from M2 the mTLS transport. The change operations of spec 1.3 are
// AgentChangeOps.
type AgentClient interface {
	Hello(ctx context.Context) (AgentHello, error)
	Read(ctx context.Context, p Principal, a ActionRef, input json.RawMessage) (json.RawMessage, error)
	Resource(ctx context.Context, p Principal, name string) (json.RawMessage, error)
	Plan(ctx context.Context, p Principal, a ActionRef, input json.RawMessage, opts PlanOptions) (PlanView, error)
	Apply(ctx context.Context, p Principal, planID string, opts ApplyOptions) (ChangeView, error)
	Confirm(ctx context.Context, p Principal, changeID string) (ChangeView, error)
	Revert(ctx context.Context, p Principal, changeID string) (ChangeView, error)
	Change(ctx context.Context, changeID string) (ChangeView, error)
	Changes(ctx context.Context, f ChangeFilter) ([]ChangeView, error)
	Preflight(ctx context.Context, moduleID string) (ModuleStatus, error)
	Journal(ctx context.Context, afterSeq int64, limit int) ([]JournalEntry, error)
	Ack(ctx context.Context, seq int64) error
	AgentChangeOps
}

// Dispatcher is what the HTTP API and the controller socket call. hostID is
// empty for controller-side actions.
type Dispatcher interface {
	Read(ctx context.Context, p Principal, hostID string, a ActionRef, input json.RawMessage) (json.RawMessage, error)
	Resource(ctx context.Context, p Principal, hostID, name string) (json.RawMessage, error)
	Plan(ctx context.Context, p Principal, hostID string, a ActionRef, input json.RawMessage, opts PlanOptions) (PlanView, error)
	Apply(ctx context.Context, p Principal, hostID string, a ActionRef, planID string, opts ApplyOptions) (ChangeView, error)
	Confirm(ctx context.Context, p Principal, changeID string) (ChangeView, error)
	Revert(ctx context.Context, p Principal, changeID string) (ChangeView, error)
	Change(ctx context.Context, p Principal, changeID string) (ChangeView, error)
	Changes(ctx context.Context, p Principal, f ChangeFilter) ([]ChangeView, error)
}

// AgentChangeOps are the agent half of the change operations: undo, repair,
// release, effects and their resolution, and the plan lookup and retire a
// composite uses to follow its children. A non-nil call names the composite
// whose unwind or release acts on one of its steps. AckPrivate with final
// set also says the change's composite is terminal, so the step can no
// longer be undone.
type AgentChangeOps interface {
	Undo(ctx context.Context, p Principal, changeID string, call *CallContext) (ChangeView, error)
	Repair(ctx context.Context, p Principal, changeID string, req RepairRequest) (RepairResult, error)
	Release(ctx context.Context, p Principal, changeID, reason string, call *CallContext) (ReleaseResult, error)
	Effects(ctx context.Context, p Principal, changeID string) ([]EffectView, error)
	Observe(ctx context.Context, p Principal, changeID string, seq int) (ObserveResult, error)
	Resolve(ctx context.Context, p Principal, changeID string, seq int, req ResolveRequest) (EffectView, error)
	Lookup(ctx context.Context, planID string, private bool) (PlanLookup, error)
	Retire(ctx context.Context, planID string) (PlanLookup, error)
	AckPrivate(ctx context.Context, changeID string, final bool) error
}

// DispatcherChangeOps are the controller half of the change operations.
type DispatcherChangeOps interface {
	Undo(ctx context.Context, p Principal, changeID string) (ChangeView, error)
	Repair(ctx context.Context, p Principal, changeID string, req RepairRequest) (RepairResult, error)
	Release(ctx context.Context, p Principal, changeID, reason string) (ReleaseResult, error)
	Effects(ctx context.Context, p Principal, changeID string) ([]EffectView, error)
	Observe(ctx context.Context, p Principal, changeID string, seq int) (ObserveResult, error)
	Resolve(ctx context.Context, p Principal, changeID string, seq int, req ResolveRequest) (EffectView, error)
	Steps(ctx context.Context, p Principal, changeID string) ([]StepView, error)
}
