// Package core is the module contract every Darkpanel component builds on:
// action and resource definitions, the registry, and the interfaces between
// the controller, the agent, the lifecycle engine and the API (spec section
// 2a). Changing a declaration here changes a contract other parts depend on.
package core

import (
	"context"
	"database/sql"
	"encoding/json"
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

// The four action classes.
const (
	ClassRead         Class = "read"
	ClassRevertible   Class = "revertible"
	ClassCompensable  Class = "compensable"
	ClassIrreversible Class = "irreversible"
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
	return rank[s] > 0 && rank[s] >= rank[need]
}

// ActionRef names one version of an action.
type ActionRef struct {
	Name    string
	Version int
}

// String renders the reference as "<name>/v<version>".
func (r ActionRef) String() string { return fmt.Sprintf("%s/v%d", r.Name, r.Version) }

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
// Agents is nil on the agent side.
type Env struct {
	HostID    string
	Principal Principal
	DB        *store.DB
	Exec      Runner
	Files     FileWriter
	Sealer    Sealer
	Clock     Clock
	Log       *slog.Logger
	Agents    HostDirectory
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
	CompensatedBy string // compensable only, an action name
}

// Reader is the handler of a read action.
type Reader[In, Out any] interface {
	Read(ctx context.Context, env Env, in In) (Out, error)
}

// Mutator is the handler of a compensable or irreversible action.
type Mutator[In, Out any] interface {
	Plan(ctx context.Context, env Env, in In) (PlanOutcome, error)
	Apply(ctx context.Context, env Env, in In) (Out, error)
	Verify(ctx context.Context, env Env, in In, out Out) error
}

// Reverter is the handler of a revertible action.
type Reverter[In, Out any] interface {
	Mutator[In, Out]
	Snapshot(ctx context.Context, env Env, in In) ([]byte, error)
	Revert(ctx context.Context, env Env, snapshot []byte) error
}

// ErasedHandler is a handler working on JSON. DefineRead, DefineMutating and
// DefineRevertible build it; the fields a class does not use are nil.
type ErasedHandler struct {
	Read     func(ctx context.Context, env Env, in json.RawMessage) (json.RawMessage, error)
	Plan     func(ctx context.Context, env Env, in json.RawMessage) (PlanOutcome, error)
	Apply    func(ctx context.Context, env Env, in json.RawMessage) (json.RawMessage, error)
	Verify   func(ctx context.Context, env Env, in, out json.RawMessage) error
	Snapshot func(ctx context.Context, env Env, in json.RawMessage) ([]byte, error)
	Revert   func(ctx context.Context, env Env, snapshot []byte) error
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

// OwnedPath is a file or marked-block file a module may write (ADR 0009).
type OwnedPath struct {
	Path          string
	Kind          string // "file" or "block"
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
	Uninstall      func(ctx context.Context, env Env) error
	Migrations     map[Side]fs.FS
	DefaultEnabled bool
}

// Module is a compiled-in module.
type Module interface {
	Manifest() Manifest
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
	Code, Message string
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

// ApplyOptions carry apply's per-class options.
type ApplyOptions struct {
	ConfirmTimeout time.Duration
	Confirmation   string
	ConfirmNow     bool
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
	HostID          string
	Modules         []ModuleStatus
}

// AgentClient is the controller's view of one agent; in M1 the in-process
// client, from M2 the mTLS transport.
type AgentClient interface {
	Hello(ctx context.Context) (AgentHello, error)
	Read(ctx context.Context, p Principal, a ActionRef, input json.RawMessage) (json.RawMessage, error)
	Resource(ctx context.Context, p Principal, name string) (json.RawMessage, error)
	Plan(ctx context.Context, p Principal, a ActionRef, input json.RawMessage) (PlanView, error)
	Apply(ctx context.Context, p Principal, planID string, opts ApplyOptions) (ChangeView, error)
	Confirm(ctx context.Context, p Principal, changeID string) (ChangeView, error)
	Revert(ctx context.Context, p Principal, changeID string) (ChangeView, error)
	Change(ctx context.Context, changeID string) (ChangeView, error)
	Changes(ctx context.Context, f ChangeFilter) ([]ChangeView, error)
	Preflight(ctx context.Context, moduleID string) (ModuleStatus, error)
	Journal(ctx context.Context, afterSeq int64, limit int) ([]JournalEntry, error)
	Ack(ctx context.Context, seq int64) error
}

// Dispatcher is what the HTTP API and the controller socket call. hostID is
// empty for controller-side actions.
type Dispatcher interface {
	Read(ctx context.Context, p Principal, hostID string, a ActionRef, input json.RawMessage) (json.RawMessage, error)
	Resource(ctx context.Context, p Principal, hostID, name string) (json.RawMessage, error)
	Plan(ctx context.Context, p Principal, hostID string, a ActionRef, input json.RawMessage) (PlanView, error)
	Apply(ctx context.Context, p Principal, hostID string, a ActionRef, planID string, opts ApplyOptions) (ChangeView, error)
	Confirm(ctx context.Context, p Principal, changeID string) (ChangeView, error)
	Revert(ctx context.Context, p Principal, changeID string) (ChangeView, error)
	Change(ctx context.Context, p Principal, changeID string) (ChangeView, error)
	Changes(ctx context.Context, p Principal, f ChangeFilter) ([]ChangeView, error)
}
