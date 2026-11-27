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
	"io"
	"io/fs"
	"log/slog"
	"net/url"
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
	// An unknown scope on either side denies: a mistyped required scope must fail closed.
	return s.rank() > 0 && need.rank() > 0 && s.rank() >= need.rank()
}

// rank orders the scopes; an empty or unknown scope ranks 0.
func (s Scope) rank() int {
	switch s {
	case ScopeRead:
		return 1
	case ScopeOperate:
		return 2
	case ScopeAdmin:
		return 3
	}
	return 0
}

// HostData is how a controller-side read relates to host data (ADR 0018).
type HostData string

// The four host data classes.
const (
	HostDataNone     HostData = "none"     // returns no host data; served to any principal its scope allows
	HostDataFleet    HostData = "fleet"    // returns data across hosts unfiltered; fleet principals only
	HostDataInput    HostData = "input"    // names its hosts in the input, at ActionSpec.HostPointers
	HostDataFiltered HostData = "filtered" // filters its rows to the hosts the caller may read
)

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
	Scope   Scope  // local break-glass and system actors carry ScopeAdmin; members and capped tokens carry ""
	Source  string // client address, or "socket"
	Access  Access // the hosts the principal may act on, resolved per request
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
	Units     Units     // the action's module's unit client; nil on the controller
	Listeners Listeners // controller-side actions only; nil on the agent
	// ControllerAddrs are the addresses the agent's configured controller
	// endpoint resolves to, resolved per call; set for module fail2ban.
	ControllerAddrs []string
	// ControllerAddrsErr is why the controller endpoint did not resolve;
	// ControllerAddrs then lacks its addresses.
	ControllerAddrsErr error
	// Home reads and writes the users' authorized keys; set for module users.
	Home HomeFiles
	// Shell starts root's login shell on a new terminal; set for module
	// console. It refuses while the module is off or denied by the policy file.
	Shell ShellStarter
	// Journal records a module's own audit entries in the agent journal,
	// which the controller ingests like change entries; set for module
	// console.
	Journal ModuleJournal
	// ConsoleFS is the file manager's root file access; set for module
	// console. It refuses while the module is off or denied by the policy
	// file.
	ConsoleFS ConsoleFiles
}

// ShellStarter starts root's login shell on a new terminal of cols by rows
// (ADR 0020): the one way to run a program outside the exec allowlist.
type ShellStarter interface {
	Start(ctx context.Context, cols, rows uint16) (ShellSession, error)
}

// ShellSession is a running shell's terminal. Read returns io.EOF once the
// terminal hung up. Close hangs the terminal up and returns once the shell
// exited, killing it when it outlives the hang-up. Wait returns the shell's
// exit code, 128 plus the signal for a shell a signal ended.
type ShellSession interface {
	Read(p []byte) (int, error)
	Write(p []byte) (int, error)
	Resize(cols, rows uint16) error
	Close() error
	Wait() (exitCode int, err error)
}

// ModuleJournal appends one entry, with its At, Actor, Action, Phase, Input
// and Source as given, to the agent journal in a transaction of its own.
type ModuleJournal interface {
	Record(ctx context.Context, e JournalEntry) error
}

// ModuleJournalPhases are the phases a ModuleJournal records: the console's
// terminal open and close and file intent and result, which the
// controller's audit admits (migration 0021).
var ModuleJournalPhases = []string{"open", "close", "intent", "result"}

// HomeFiles reads and writes ~user/.ssh/authorized_keys with sshd's
// StrictModes checks (ADR 0020). A missing .ssh or file reads as nil.
type HomeFiles interface {
	ReadAuthorizedKeys(ctx context.Context, user string) ([]byte, error)
	WriteAuthorizedKeys(ctx context.Context, user string, data []byte) error
}

// ConsoleFiles is the console's file manager over the whole host, as root
// (ADR 0020). Paths are absolute. A path with a symbolic link in any
// component is refused, and so are writes, renames and deletes of the
// protected paths. ReadText returns a file of at most 1 MiB of UTF-8 and its
// SHA-256 (hex); WriteText replaces a file only while its SHA-256 is still
// expectSHA ("" for a file that must not exist yet) and returns the new
// hash; Upload writes exactly size bytes from r and returns their hash.
// Delete removes a directory, recursively, only when confirm is its path.
// RemoveTemp removes a temporary file a write left behind, as
// RecordTemp named it; nothing else.
type ConsoleFiles interface {
	List(ctx context.Context, path, cursor string) (DirPage, error)
	Stat(ctx context.Context, path string) (FileInfo, error)
	ReadText(ctx context.Context, path string) ([]byte, string, error)
	Open(ctx context.Context, path string) (io.ReadCloser, int64, error)
	WriteText(ctx context.Context, path string, data []byte, expectSHA string) (string, error)
	Upload(ctx context.Context, path string, r io.Reader, size int64) (string, error)
	Mkdir(ctx context.Context, path string) error
	Rename(ctx context.Context, from, to string) error
	Delete(ctx context.Context, path, confirm string) error
	Chmod(ctx context.Context, path string, mode fs.FileMode) error
	Chown(ctx context.Context, path string, uid, gid int) error
	RemoveTemp(ctx context.Context, path string) error
}

// FileManager is a module serving the agent's file endpoints: Files wraps
// env.ConsoleFS with the module's records of every operation.
type FileManager interface {
	Files(env Env) ConsoleFiles
}

// DirPage is one page of a directory listing: directories first, then by
// name. Next is the cursor of the next page, "" on the last. Protected is
// why writes in the directory are refused, "" when they are not.
type DirPage struct {
	Path      string     `json:"path"`
	Entries   []FileInfo `json:"entries"`
	Next      string     `json:"next,omitempty"`
	Protected string     `json:"protected,omitempty"`
}

// The types of a FileInfo.
const (
	FileTypeFile    = "file"
	FileTypeDir     = "dir"
	FileTypeSymlink = "symlink"
	FileTypeOther   = "other" // a device, socket or FIFO
)

// FileInfo describes one directory entry, never following a symbolic link.
// A name that is not UTF-8 has its invalid bytes escaped as \xNN, Escaped
// set and its bytes in Raw (standard base64). Mode is the permission bits in
// octal, set-id and sticky bits included. Owner and Group fall back to the
// numeric ids. Protected is why writes to the entry are refused, "" when
// they are not.
type FileInfo struct {
	Name      string    `json:"name"`
	Escaped   bool      `json:"escaped,omitempty"`
	Raw       string    `json:"raw,omitempty"`
	Type      string    `json:"type"`
	Size      int64     `json:"size"`
	Mode      string    `json:"mode"`
	UID       int       `json:"uid"`
	GID       int       `json:"gid"`
	Owner     string    `json:"owner"`
	Group     string    `json:"group"`
	Modified  time.Time `json:"modified"`
	Target    string    `json:"target,omitempty"`
	Protected string    `json:"protected,omitempty"`
}

type tempRecordKey struct{}

// WithTempRecord returns ctx carrying record, which a ConsoleFiles write
// calls with the path of its temporary file once the file exists and before
// any byte is written to it, so a crash leaves the path on record. An error
// from record ends the write.
func WithTempRecord(ctx context.Context, record func(path string) error) context.Context {
	return context.WithValue(ctx, tempRecordKey{}, record)
}

// RecordTemp calls the record of ctx, if any, with path.
func RecordTemp(ctx context.Context, path string) error {
	if record, ok := ctx.Value(tempRecordKey{}).(func(string) error); ok {
		return record(path)
	}
	return nil
}

// CallContext is why a change runs. It comes from the controller, never from
// input: the API and the agent socket refuse it, and handlers that depend on
// provenance read only Env.Call.
type CallContext struct {
	ParentChangeID string        // the composite running this as a step; "" for a top-level change
	ParentAction   string        // that composite's action name
	StepID         string        // the step id within the parent
	RepairOf       string        // the change this one repairs; derived by the engine from the stored plan, never sent
	Retired        []string      // exposure ids the parent has retired
	GuardRefresh   *GuardContext // apply only: the controller's topology at apply time; never stored with the plan
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

// Guard is one safety check a plan ran. An overridden guard reports OK true.
type Guard struct {
	ID          string `json:"id"`
	OK          bool   `json:"ok"`
	Message     string `json:"message"`
	Overridable bool   `json:"overridable"` // the engine copies it from the action's GuardSpec
	Overridden  bool   `json:"overridden"`  // the plan's input listed this Overridable guard in overrides
}

// PlanOutcome is what a handler's Plan returns.
type PlanOutcome struct {
	Diff         []DiffLine
	Guards       []Guard
	StateHash    string
	Confirmation string // irreversible and TypedConfirmation actions only: the phrase apply must repeat
}

// ActionSpec is an action's declaration (ADR 0002).
type ActionSpec struct {
	Name, Summary string
	Version       int
	Class         Class
	Side          Side
	BreakGlass    bool
	LockKeys      []string
	// StepUp makes the controller refuse the action to a session that has not
	// given a fresh TOTP code in the last 15 minutes, and to every API token.
	StepUp        bool
	CompensatedBy string // compensable only, an action name
	ForwardOnly   bool   // compensable: never reversed; requires Converges
	// TypedConfirmation makes a compensable action's apply repeat the phrase
	// its plan names, as an irreversible action's always does. A plan of such
	// an action that names no phrase asks for none.
	TypedConfirmation bool
	Converges         bool          // apply run again with the same input, after a partial or complete apply, ends in the same state and plans an empty diff when the effect is present
	RepairedBy        string        // revertible only, required: an action of the same module
	ApplyTimeout      time.Duration // one budget for apply and verify; 0 means 30 s; above 45 s requires LongRunning; composites: computed at plan
	LongRunning       bool          // apply answers once the intent is persisted
	Task              bool          // the host sequences run in transient units; set by DefineTask
	Unwind            Unwind        // composite only
	Guards            []GuardSpec   // the guards the action's plan may report; the input type then embeds Guarded
	// Available is the action's own availability check; the error text is
	// the reason. nil means available whenever the module is.
	Available func(ctx context.Context, env Env) error
	MinScope  Scope // raises the scope the class and side require; "" keeps it
	Internal  bool  // dispatched only by the controller itself; read actions only
	Reveal    bool  // returns one stored secret once; read actions only, with MinScope admin
	// HostData classifies a controller-side read. A controller-side read
	// without it is served only to fleet principals; internal reads are
	// exempt.
	HostData HostData
	// HostPointers are the JSON pointers into the input that name the hosts
	// a HostDataInput read returns data for: a string or an array of
	// strings. A pointer the input leaves out is not checked, so a read
	// whose omitted field selects hosts filters them itself.
	HostPointers []string
	// Rechecks lists the modules whose availability a change of this action
	// can change; the controller rechecks them when the change ends.
	Rechecks []string
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
	ID      string          `json:"id"`                // [a-z][a-z0-9]*, unique in the composite
	Host    string          `json:"host"`              // host id; "" for a controller-side action
	Action  ActionRef       `json:"action"`            // "<name>/v<version>"
	Input   json.RawMessage `json:"input"`             // the child's input, with ${input...} and ${steps...} references
	After   []string        `json:"after,omitempty"`   // steps this one follows
	Retires string          `json:"retires,omitempty"` // exposure id this step retires once it has committed (1.5); forward composites only
	// DeferPlan plans the step only at its cursor during apply, like a step
	// with ${steps...} references: the composite's plan neither dry-runs it
	// nor hashes it, for a step whose plan depends on what earlier steps do.
	DeferPlan bool `json:"deferPlan,omitempty"`
}

// Composer is the handler of a composite action. The engine, not the
// module, plans, applies and recovers a composite.
type Composer[In, Out any] interface {
	// Steps returns the steps for in. It reads only in and the controller
	// database, never a host.
	Steps(ctx context.Context, env Env, in In) ([]Step, error)
	// Output builds the composite's output from its steps' redacted
	// outputs, read steps included, keyed by step id.
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
	// Reveal is set only by DefineReveal and DefineInternalRead: value is
	// the output encoded without redaction, identity names it for the audit.
	Reveal func(ctx context.Context, env Env, in json.RawMessage) (identity string, value json.RawMessage, err error)
}

// Revealed is a reveal handler's result; Out is the action's output type.
type Revealed[Out any] struct {
	Identity string // what is revealed, for the audit ("npm admin password, host h1")
	Value    Out    // the output, never redacted
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

// The kinds of an owned path. Directories and users are the installer's,
// recorded so that uninstall removes them, except that the files of a dir
// with a Pattern and of a unit-set are written through UnitSetWriter; the
// other kinds are written through FileWriter.
const (
	OwnedFile       = "file"
	OwnedBlock      = "block"
	OwnedDir        = "dir"
	OwnedUser       = "user"
	OwnedUnit       = "unit"
	OwnedRestore    = "restore"     // a regular file a snapshot may rewrite or remove
	OwnedManagedDir = "managed-dir" // a root-owned directory of flat module files
	OwnedSysctl     = "sysctl"      // a kernel parameter under /proc/sys
	OwnedServiceDir = "service-dir" // a directory owned by a module's service user
	OwnedUnitSet    = "unit-set"    // unit files in UnitSetDir whose names match a pattern, each recorded as a unit
)

// UnitSetDir is the directory of every unit set.
const UnitSetDir = "/etc/systemd/system"

// OwnedPath is something a module or the core creates on the host and
// uninstall removes (ADR 0009). For OwnedUser, Path is the user name; for
// OwnedSysctl, the key under /proc/sys ("net/ipv4/ip_forward"), which
// uninstall never touches.
type OwnedPath struct {
	Path          string
	Kind          string // one of the Owned kinds
	BlockIDs      []string
	CommentPrefix string // default "#"
	// Pattern is, for sysctl, the value pattern; for managed-dir and
	// service-dir, the file-name pattern; for unit-set, the unit-file names;
	// for dir, the names of the files it may hold, each recorded as it is
	// written (none without a Pattern).
	Pattern string
	User    string   // service-dir: the owning system user (an OwnedUser of the same module)
	Subdirs []string // service-dir: the only subdirectories, one level, created with it
}

// RestoreEntry is one file a snapshot restores. Present false removes it.
type RestoreEntry struct {
	Path     string
	Present  bool
	Mode     fs.FileMode
	UID, GID int
	Data     []byte
}

// LabelModule labels the containers, networks and volumes a module creates;
// its value is the module id.
const LabelModule = "darkpanel.module"

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
	ExternalUnits  []ExternalUnit // units the module drives but does not own
}

// ExternalUnit is a unit a module may drive without owning it.
type ExternalUnit struct {
	Name string   // "tailscaled.service"
	Ops  []string // a subset of UnitOpState, UnitOpStart, UnitOpStop, UnitOpRestart, UnitOpReload
}

// The operations of ExternalUnit.Ops.
const (
	UnitOpState   = "state"
	UnitOpStart   = "start"
	UnitOpStop    = "stop"
	UnitOpRestart = "restart"
	UnitOpReload  = "reload" // through UnitReloader
)

// Module is a compiled-in module.
type Module interface {
	Manifest() Manifest
}

// UninstallOption is one option a module's uninstall step accepts.
type UninstallOption struct {
	Key     string   // ^[a-z][a-z0-9_]{0,31}$, unique within the module
	Values  []string // the allowed values, at least one
	Default string   // one of Values
	Help    string   // one line, printed in the plan
}

// Uninstaller is the optional agent-side uninstall step of a module, found
// with a type assertion on the Module. It runs before uninstall removes the
// module's marked blocks and files; with keepData it leaves the module's data
// in place for a reinstall. opts holds every declared option with its value
// filled: the --module-option value or the default. Uninstall runs again when
// an uninstall that stopped at a later step is run anew, so it must succeed
// on a host where it already ran.
type Uninstaller interface {
	UninstallOptions() []UninstallOption
	Uninstall(ctx context.Context, env Env, keepData bool, opts map[string]string) error
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
	// Stream runs a program and hands each line of its stdout, at most
	// 64 KiB, to onLine until it exits, onLine fails or ctx ends.
	Stream(ctx context.Context, path string, args []string, onLine func(line []byte) error) error
}

// FileWriter writes manifest-declared files and marked blocks, and the other
// owned-path kinds of spec 1.8. Every method refuses a path, key or name the
// module does not declare with the right kind.
type FileWriter interface {
	WriteFile(ctx context.Context, module, path string, data []byte, mode fs.FileMode) error
	ReplaceBlock(ctx context.Context, module, path, blockID string, content []byte) error
	RemoveBlock(ctx context.Context, module, path, blockID string) error
	Remove(ctx context.Context, module, path string) error
	Restore(ctx context.Context, module string, e RestoreEntry) error
	ReadSysctl(ctx context.Context, module, key string) (string, error)
	WriteSysctl(ctx context.Context, module, key, value string) error
	WriteManaged(ctx context.Context, module, dir, name string, data []byte, mode fs.FileMode, uid, gid int) error
	RemoveManaged(ctx context.Context, module, dir, name string) error
	Rename(ctx context.Context, module, from, to string) error
	EnsureServiceDir(ctx context.Context, module, dir string) error
	RenameIn(ctx context.Context, module, dir, from, to string) error
	RemoveIn(ctx context.Context, module, dir, name string) error
	RemoveServiceDir(ctx context.Context, module, dir string) error
	// Owns reports whether the ownership store records path (any kind) as
	// module's, so a plan can refuse to take over a pre-existing file, unit
	// file or directory.
	Owns(ctx context.Context, module, path string) (bool, error)
}

// DirMaker is implemented by the agent's FileWriter. MakeDir creates a
// directory the module declares as kind dir, root-owned with mode 0755,
// when it is absent, and records nothing.
type DirMaker interface {
	MakeDir(ctx context.Context, module, dir string) error
}

// EnablementHook is implemented by a module value that reacts to its
// enabled flag on the agent: the runtime calls it after storing a new flag,
// and once at start with the stored one. An error is logged; the flag
// stands.
type EnablementHook interface {
	OnEnabled(ctx context.Context, env Env, enabled bool) error
}

// UnitSetWriter writes the files of a module's unit sets and of its dir
// declarations with a Pattern, recording each file in the ownership store
// as it is written and forgetting it as it is removed. The agent's
// FileWriter implements it.
type UnitSetWriter interface {
	WriteUnitInSet(ctx context.Context, module, name string, data []byte) error
	RemoveUnitInSet(ctx context.Context, module, name string) error
	WriteDirFile(ctx context.Context, module, dir, name string, data []byte, mode fs.FileMode, gid int) error
	RemoveDirFile(ctx context.Context, module, dir, name string) error
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
	// Owns reports whether module's entry for path exists, of any kind.
	Owns(ctx context.Context, module, path string) (bool, error)
}

// JournalEntry is one lifecycle event of a change.
type JournalEntry struct {
	Seq     int64 // assigned by the agent journal; 0 on the controller side
	At      time.Time
	Actor   string
	Action  string
	Version int
	// Phase is a change's "apply", "confirm", "revert", "recover" or
	// "interrupt", or a module journal's phase (ModuleJournalPhases).
	Phase     string
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
	// Rows, when not nil, keeps only entries on its hosts, before Limit;
	// host-less entries go to fleet principals alone.
	Rows *RowScope
}

// Audit is the controller's audit log.
type Audit interface {
	Journal
	Record(ctx context.Context, e AuditEntry) error
	Ingest(ctx context.Context, hostID string, entries []JournalEntry) error
	List(ctx context.Context, q AuditQuery) (entries []AuditEntry, nextBefore string, err error)
}

// The severities of a NotifyEvent, lowest first.
const (
	SeverityInfo     = "info"
	SeverityWarning  = "warning"
	SeverityCritical = "critical"
)

// The kinds of a NotifyEvent (M10 spec section 4).
const (
	NotifyAlertFiring        = "alert.firing"
	NotifyAlertResolved      = "alert.resolved"
	NotifyChangeFailed       = "change.failed"
	NotifyChangeAutoReverted = "change.auto_reverted"
	NotifyRolloutFailed      = "rollout.failed"
	NotifyUpdateRolledBack   = "update.rolled_back"
	NotifyTest               = "notify.test"
	NotifyDeliveryFailed     = "notify.delivery_failed"
)

// NotifyKinds lists every NotifyEvent kind.
func NotifyKinds() []string {
	return []string{NotifyAlertFiring, NotifyAlertResolved, NotifyChangeFailed, NotifyChangeAutoReverted,
		NotifyRolloutFailed, NotifyUpdateRolledBack, NotifyTest, NotifyDeliveryFailed}
}

// NotifyEvent is one notification. Title and Body leave for third parties,
// so they are fixed templates and never carry an error cause, an action
// input or an output. HostID is empty for a host-less event.
type NotifyEvent struct {
	Kind, Severity, HostID, Title, Body, DedupeKey string
	// Facts, when set, has the notifier write Title, Body and an empty
	// Severity from the kind's fixed template, so a caller outside the
	// notify module sends no text of its own.
	Facts *NotifyFacts
}

// NotifyFacts are what a kind's template names: the change, action, state
// and error code of a change event, the rollout and version of a rollout
// event. They are never stored; only the text written from them is.
type NotifyFacts struct {
	ChangeID, Action, State, ErrorCode string
	RolloutID, Version                 string
}

// Notifier records a notification in the transaction that writes its fact,
// so the event is stored exactly when the fact is. The notify module
// implements it and server wiring injects it; core and controller packages
// never import a module.
type Notifier interface {
	Emit(ctx context.Context, tx *sql.Tx, e NotifyEvent) error
}

// NopNotifier is the default Notifier: it records nothing.
type NopNotifier struct{}

// Emit records nothing.
func (NopNotifier) Emit(context.Context, *sql.Tx, NotifyEvent) error { return nil }

// NotifierOrNop is n, or NopNotifier when n is nil.
func NotifierOrNop(n Notifier) Notifier {
	if n == nil {
		return NopNotifier{}
	}
	return n
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
	// Rows, when not nil, keeps changes on its hosts, host-less changes by
	// its actors, and composites whose every step is on one of its hosts.
	// It is applied before Limit, so a page is never short of visible rows.
	Rows *RowScope
}

// ModuleStatus is one module's availability on a host.
type ModuleStatus struct {
	ID        string
	Version   int
	Available bool
	Reason    string
}

// ModuleAvailability is one module's availability on a host, per action.
type ModuleAvailability struct {
	Module    string
	Available bool
	Reason    string // "" when available
	Actions   []ActionAvailability
}

// ActionAvailability is one action's availability; Reason is "" when available.
type ActionAvailability struct {
	Name      string
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
	AgentEnablement
	AgentAvailability
	AgentReveal
	AgentStreams
}

// Dispatcher is what the HTTP API and the controller socket call. hostID is
// empty for controller-side actions. The change operations of spec 1.3 are
// DispatcherChangeOps.
type Dispatcher interface {
	Read(ctx context.Context, p Principal, hostID string, a ActionRef, input json.RawMessage) (json.RawMessage, error)
	Resource(ctx context.Context, p Principal, hostID, name string) (json.RawMessage, error)
	Plan(ctx context.Context, p Principal, hostID string, a ActionRef, input json.RawMessage, opts PlanOptions) (PlanView, error)
	Apply(ctx context.Context, p Principal, hostID string, a ActionRef, planID string, opts ApplyOptions) (ChangeView, error)
	Confirm(ctx context.Context, p Principal, changeID string) (ChangeView, error)
	Revert(ctx context.Context, p Principal, changeID string) (ChangeView, error)
	Change(ctx context.Context, p Principal, changeID string) (ChangeView, error)
	Changes(ctx context.Context, p Principal, f ChangeFilter) ([]ChangeView, error)
	DispatcherChangeOps
	DispatcherReveal
	DispatcherStreams
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

// AgentEnablement pushes the controller's module enablement to the agent.
type AgentEnablement interface {
	SetModuleEnabled(ctx context.Context, moduleID string, enabled bool) error
}

// AgentAvailability asks the agent which actions of a module are available.
type AgentAvailability interface {
	Availability(ctx context.Context, moduleID string) (ModuleAvailability, error)
}

// AgentReveal runs a reveal action, or with internal an Internal read.
type AgentReveal interface {
	Reveal(ctx context.Context, p Principal, a ActionRef, input json.RawMessage, internal bool) (json.RawMessage, error)
}

// AgentStreams lists and opens module streams.
type AgentStreams interface {
	Streams(ctx context.Context) ([]StreamInfo, error)
	OpenStream(ctx context.Context, p Principal, module, name string, params url.Values) (io.ReadCloser, error)
}

// DispatcherReveal is the reveal path the API calls; it refuses Internal actions.
type DispatcherReveal interface {
	Reveal(ctx context.Context, p Principal, hostID string, a ActionRef, input json.RawMessage) (json.RawMessage, error)
}

// DispatcherStreams opens a module stream for the relay.
type DispatcherStreams interface {
	OpenStream(ctx context.Context, p Principal, hostID, module, name string, params url.Values) (io.ReadCloser, error)
}

// DispatcherChannels opens a module channel on a host's agent for the
// controller's channel bridge (M11 spec 1.2).
type DispatcherChannels interface {
	OpenChannel(ctx context.Context, p Principal, hostID, module, name string, params url.Values) (io.ReadWriteCloser, error)
	// ModuleUsable is nil while module is enabled and available on hostID,
	// else the module.unavailable error an action would get.
	ModuleUsable(ctx context.Context, hostID, module string) error
}

// FileRequest is one request of the console's file endpoints (M11 spec
// section 5): Op is the endpoint (list, stat, read, download, write,
// upload, mkdir, rename, delete, chmod, chown) and Query its path, cursor
// and sha256. Body, when set, holds exactly Size bytes: the raw bytes of a
// write or an upload, or the JSON of a metadata operation when JSON is set.
type FileRequest struct {
	Op    string
	Query url.Values
	Body  io.Reader
	Size  int64
	JSON  bool
}

// FileAnswer is an agent's successful answer to a FileRequest. Body is nil
// for 204 and is the caller's to close otherwise; it is not limited to the
// agent API's body cap.
type FileAnswer struct {
	Status      int
	ContentType string
	Size        int64  // the answer's Content-Length, -1 when unknown
	SHA256      string // a read's content hash header
	Body        io.ReadCloser
}

// AgentFiles sends file requests to an agent on module-stream connections.
type AgentFiles interface {
	File(ctx context.Context, p Principal, req FileRequest) (FileAnswer, error)
}

// DispatcherFiles proxies the console's file endpoints to a host's agent
// for the controller's file routes (M11 spec section 5).
type DispatcherFiles interface {
	File(ctx context.Context, p Principal, hostID string, req FileRequest) (FileAnswer, error)
	// ModuleUsable is as DispatcherChannels has it: a transfer rechecks it.
	ModuleUsable(ctx context.Context, hostID, module string) error
}
