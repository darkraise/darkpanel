// Package wire holds the request and response bodies of the agent API
// (ADR 0012). Most core types carry no JSON tags, so the API never encodes
// them directly: their bodies are these types, with camelCase fields, times
// in UTC, durations in whole seconds and absent values as JSON null, and with
// conversions to and from their core counterparts. The lifecycle views and
// requests of spec 1.3 (core.EffectView, core.ObserveResult,
// core.PlanLookup, core.RepairRequest and core.ResolveRequest) carry their
// own tags and are sent as they are.
package wire

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"net/url"
	"slices"
	"strconv"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// The agent API's headers (ADR 0012).
const (
	// HeaderPrincipal carries the JSON of the calling Principal; the agent
	// accepts it only from the authenticated controller.
	HeaderPrincipal    = "Darkpanel-Principal"
	HeaderProtocol     = "Darkpanel-Protocol"
	HeaderVersion      = "Darkpanel-Version"
	HeaderControllerID = "Darkpanel-Controller-Id"
	// HeaderCall carries the CallContext the controller sends with a plan,
	// apply, undo or release: its JSON encoded with base64.RawURLEncoding
	// (spec 1.5). A plan's body is the action input itself, so the call
	// cannot travel in the body.
	HeaderCall = "Darkpanel-Call"
	// HeaderInternal marks a reveal the controller makes for one of its
	// own Internal reads (spec 1.7); "1" is its only value. The agent
	// serves an Internal action on the reveal route only with it.
	HeaderInternal = "Darkpanel-Internal"
)

// MaxBody is the largest request or response body the agent API accepts.
const MaxBody = 4 << 20

// Principal is core.Principal.
type Principal struct {
	Actor   string `json:"actor"`
	UserID  string `json:"userId"`
	TokenID string `json:"tokenId"`
	Scope   string `json:"scope"`
	Source  string `json:"source"`
}

// FromPrincipal converts p.
func FromPrincipal(p core.Principal) Principal {
	return Principal{Actor: p.Actor, UserID: p.UserID, TokenID: p.TokenID, Scope: string(p.Scope), Source: p.Source}
}

// Core converts p back.
func (p Principal) Core() core.Principal {
	return core.Principal{Actor: p.Actor, UserID: p.UserID, TokenID: p.TokenID, Scope: core.Scope(p.Scope), Source: p.Source}
}

// CallContext is core.CallContext without RepairOf, which the agent derives
// from a repair plan and never receives (spec 1.5). The agent decodes it
// with unknown members refused, so a repairOf member is
// request.invalid_input. GuardRefresh is sent with an apply only.
type CallContext struct {
	ParentChangeID string             `json:"parentChangeId"`
	ParentAction   string             `json:"parentAction"`
	StepID         string             `json:"stepId"`
	Retired        []string           `json:"retired"`
	GuardRefresh   *core.GuardContext `json:"guardRefresh,omitempty"`
}

// FromCall converts c, leaving out RepairOf.
func FromCall(c core.CallContext) CallContext {
	return CallContext{ParentChangeID: c.ParentChangeID, ParentAction: c.ParentAction, StepID: c.StepID,
		Retired: slices.Clone(c.Retired), GuardRefresh: c.GuardRefresh}
}

// Core converts c back; RepairOf stays empty.
func (c CallContext) Core() core.CallContext {
	return core.CallContext{ParentChangeID: c.ParentChangeID, ParentAction: c.ParentAction, StepID: c.StepID,
		Retired: slices.Clone(c.Retired), GuardRefresh: c.GuardRefresh}
}

// SchemaHash is core.SchemaHash.
type SchemaHash struct {
	Input  string `json:"input"`
	Output string `json:"output"`
}

// ModuleStatus is core.ModuleStatus.
type ModuleStatus struct {
	ID        string `json:"id"`
	Version   int    `json:"version"`
	Available bool   `json:"available"`
	Reason    string `json:"reason"`
}

// FromModuleStatus converts s.
func FromModuleStatus(s core.ModuleStatus) ModuleStatus {
	return ModuleStatus{ID: s.ID, Version: s.Version, Available: s.Available, Reason: s.Reason}
}

// Core converts s back.
func (s ModuleStatus) Core() core.ModuleStatus {
	return core.ModuleStatus{ID: s.ID, Version: s.Version, Available: s.Available, Reason: s.Reason}
}

// ModuleEnabled is the body of PUT modules/{id}/enabled: the controller's
// enabled flag of the module on this host.
type ModuleEnabled struct {
	Enabled bool `json:"enabled"`
}

// ModuleAvailability is core.ModuleAvailability, the body of GET
// modules/{id}/availability (spec 1.7); an empty reason is null.
type ModuleAvailability struct {
	Module    string               `json:"module"`
	Available bool                 `json:"available"`
	Reason    *string              `json:"reason"`
	Actions   []ActionAvailability `json:"actions"`
}

// ActionAvailability is core.ActionAvailability; an empty reason is null.
type ActionAvailability struct {
	Name      string  `json:"name"`
	Available bool    `json:"available"`
	Reason    *string `json:"reason"`
}

// FromModuleAvailability converts m; its actions are [] rather than null.
func FromModuleAvailability(m core.ModuleAvailability) ModuleAvailability {
	out := ModuleAvailability{Module: m.Module, Available: m.Available, Reason: nullReason(m.Reason),
		Actions: make([]ActionAvailability, len(m.Actions))}
	for i, a := range m.Actions {
		out.Actions[i] = ActionAvailability{Name: a.Name, Available: a.Available, Reason: nullReason(a.Reason)}
	}
	return out
}

// Core converts m back.
func (m ModuleAvailability) Core() core.ModuleAvailability {
	out := core.ModuleAvailability{Module: m.Module, Available: m.Available, Reason: reasonText(m.Reason),
		Actions: make([]core.ActionAvailability, len(m.Actions))}
	for i, a := range m.Actions {
		out.Actions[i] = core.ActionAvailability{Name: a.Name, Available: a.Available, Reason: reasonText(a.Reason)}
	}
	return out
}

// nullReason is a reason as the API sends it: null when there is none.
func nullReason(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// reasonText reads a reason the API sent; null is none.
func reasonText(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// Hello is core.AgentHello. ControllerConnected is null for an agent that
// is not enrolled.
type Hello struct {
	ProtocolVersion     int                   `json:"protocolVersion"`
	BinaryVersion       string                `json:"binaryVersion"`
	OS                  string                `json:"os"`
	Arch                string                `json:"arch"`
	HostID              string                `json:"hostId"`
	JournalEpoch        string                `json:"journalEpoch"`
	ControllerConnected *bool                 `json:"controllerConnected"`
	Modules             []ModuleStatus        `json:"modules"`
	SchemaHashes        map[string]SchemaHash `json:"schemaHashes"`
}

// FromHello converts h.
func FromHello(h core.AgentHello) Hello {
	out := Hello{
		ProtocolVersion: h.ProtocolVersion, BinaryVersion: h.BinaryVersion, OS: h.OS, Arch: h.Arch, HostID: h.HostID,
		JournalEpoch: h.JournalEpoch, ControllerConnected: clonePtr(h.ControllerConnected),
	}
	if h.Modules != nil {
		out.Modules = make([]ModuleStatus, len(h.Modules))
		for i, m := range h.Modules {
			out.Modules[i] = FromModuleStatus(m)
		}
	}
	if h.SchemaHashes != nil {
		out.SchemaHashes = make(map[string]SchemaHash, len(h.SchemaHashes))
		for k, v := range h.SchemaHashes {
			out.SchemaHashes[k] = SchemaHash{Input: v.Input, Output: v.Output}
		}
	}
	return out
}

// Core converts h back.
func (h Hello) Core() core.AgentHello {
	out := core.AgentHello{
		ProtocolVersion: h.ProtocolVersion, BinaryVersion: h.BinaryVersion, OS: h.OS, Arch: h.Arch, HostID: h.HostID,
		JournalEpoch: h.JournalEpoch, ControllerConnected: clonePtr(h.ControllerConnected),
	}
	if h.Modules != nil {
		out.Modules = make([]core.ModuleStatus, len(h.Modules))
		for i, m := range h.Modules {
			out.Modules[i] = m.Core()
		}
	}
	if h.SchemaHashes != nil {
		out.SchemaHashes = make(map[string]core.SchemaHash, len(h.SchemaHashes))
		for k, v := range h.SchemaHashes {
			out.SchemaHashes[k] = core.SchemaHash{Input: v.Input, Output: v.Output}
		}
	}
	return out
}

// PlanView is core.PlanView.
type PlanView struct {
	PlanID       string          `json:"planId"`
	HostID       string          `json:"hostId"`
	Action       string          `json:"action"`
	Version      int             `json:"version"`
	InputHash    string          `json:"inputHash"`
	Confirmation string          `json:"confirmation"`
	Diff         []core.DiffLine `json:"diff"`
	Guards       []core.Guard    `json:"guards"`
	ExpiresAt    time.Time       `json:"expiresAt"`
}

// FromPlanView converts v.
func FromPlanView(v core.PlanView) PlanView {
	return PlanView{
		PlanID: v.PlanID, HostID: v.HostID, Action: v.Action, Version: v.Version, InputHash: v.InputHash,
		Confirmation: v.Confirmation, Diff: slices.Clone(v.Diff), Guards: slices.Clone(v.Guards), ExpiresAt: v.ExpiresAt.UTC(),
	}
}

// Core converts v back.
func (v PlanView) Core() core.PlanView {
	return core.PlanView{
		PlanID: v.PlanID, HostID: v.HostID, Action: v.Action, Version: v.Version, InputHash: v.InputHash,
		Confirmation: v.Confirmation, Diff: slices.Clone(v.Diff), Guards: slices.Clone(v.Guards), ExpiresAt: v.ExpiresAt.UTC(),
	}
}

// ApplyOptions is core.ApplyOptions, with the confirm timeout in whole
// seconds.
type ApplyOptions struct {
	ConfirmTimeoutSeconds int    `json:"confirmTimeoutSeconds"`
	Confirmation          string `json:"confirmation"`
	ConfirmNow            bool   `json:"confirmNow"`
}

// FromApplyOptions converts o; a fraction of a second is dropped.
func FromApplyOptions(o core.ApplyOptions) ApplyOptions {
	return ApplyOptions{
		ConfirmTimeoutSeconds: int(o.ConfirmTimeout / time.Second), Confirmation: o.Confirmation, ConfirmNow: o.ConfirmNow,
	}
}

// Core converts o back.
func (o ApplyOptions) Core() core.ApplyOptions {
	return core.ApplyOptions{
		ConfirmTimeout: time.Duration(o.ConfirmTimeoutSeconds) * time.Second, Confirmation: o.Confirmation, ConfirmNow: o.ConfirmNow,
	}
}

// ChangeError is core.ChangeError.
type ChangeError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Cause   string `json:"cause"`
}

// ChangeView is core.ChangeView.
type ChangeView struct {
	ID             string          `json:"id"`
	HostID         string          `json:"hostId"`
	Action         string          `json:"action"`
	Version        int             `json:"version"`
	Actor          string          `json:"actor"`
	Class          string          `json:"class"`
	CompensatedBy  string          `json:"compensatedBy"`
	State          string          `json:"state"`
	CreatedAt      time.Time       `json:"createdAt"`
	UpdatedAt      time.Time       `json:"updatedAt"`
	RevertDeadline *time.Time      `json:"revertDeadline"`
	Output         json.RawMessage `json:"output"`
	Error          *ChangeError    `json:"error"`
	ParentChangeID string          `json:"parentChangeId"`
	Owner          string          `json:"owner"`
	RepairedBy     string          `json:"repairedBy"`
	RepairOutcome  string          `json:"repairOutcome"`
	Override       json.RawMessage `json:"override"`
}

// FromChangeView converts v.
func FromChangeView(v core.ChangeView) ChangeView {
	out := ChangeView{
		ID: v.ID, HostID: v.HostID, Action: v.Action, Version: v.Version, Actor: v.Actor, Class: string(v.Class),
		CompensatedBy: v.CompensatedBy, State: v.State, CreatedAt: v.CreatedAt.UTC(), UpdatedAt: v.UpdatedAt.UTC(),
		RevertDeadline: utcPtr(v.RevertDeadline), Output: raw(v.Output), ParentChangeID: v.ParentChangeID,
		Owner: v.Owner, RepairedBy: v.RepairedBy, RepairOutcome: v.RepairOutcome, Override: raw(v.Override),
	}
	if v.Error != nil {
		out.Error = &ChangeError{Code: v.Error.Code, Message: v.Error.Message, Cause: v.Error.Cause}
	}
	return out
}

// Core converts v back.
func (v ChangeView) Core() core.ChangeView {
	out := core.ChangeView{
		ID: v.ID, HostID: v.HostID, Action: v.Action, Version: v.Version, Actor: v.Actor, Class: core.Class(v.Class),
		CompensatedBy: v.CompensatedBy, State: v.State, CreatedAt: v.CreatedAt.UTC(), UpdatedAt: v.UpdatedAt.UTC(),
		RevertDeadline: utcPtr(v.RevertDeadline), Output: raw(v.Output), ParentChangeID: v.ParentChangeID,
		Owner: v.Owner, RepairedBy: v.RepairedBy, RepairOutcome: v.RepairOutcome, Override: raw(v.Override),
	}
	if v.Error != nil {
		out.Error = &core.ChangeError{Code: v.Error.Code, Message: v.Error.Message, Cause: v.Error.Cause}
	}
	return out
}

// RepairResult is core.RepairResult: the change a repair started, or the
// plan it made.
type RepairResult struct {
	Change *ChangeView `json:"change,omitempty"`
	Plan   *PlanView   `json:"plan,omitempty"`
}

// FromRepairResult converts r.
func FromRepairResult(r core.RepairResult) RepairResult {
	var out RepairResult
	if r.Change != nil {
		v := FromChangeView(*r.Change)
		out.Change = &v
	}
	if r.Plan != nil {
		p := FromPlanView(*r.Plan)
		out.Plan = &p
	}
	return out
}

// Core converts r back.
func (r RepairResult) Core() core.RepairResult {
	var out core.RepairResult
	if r.Change != nil {
		v := r.Change.Core()
		out.Change = &v
	}
	if r.Plan != nil {
		p := r.Plan.Core()
		out.Plan = &p
	}
	return out
}

// Release is the body of POST changes/{id}/release.
type Release struct {
	Reason string `json:"reason"`
}

// ReleaseResult is core.ReleaseResult; Abandoned is [] rather than null.
type ReleaseResult struct {
	Change    ChangeView        `json:"change"`
	Abandoned []core.EffectView `json:"abandoned"`
}

// FromReleaseResult converts r.
func FromReleaseResult(r core.ReleaseResult) ReleaseResult {
	abandoned := slices.Clone(r.Abandoned)
	if abandoned == nil {
		abandoned = []core.EffectView{}
	}
	return ReleaseResult{Change: FromChangeView(r.Change), Abandoned: abandoned}
}

// Core converts r back.
func (r ReleaseResult) Core() core.ReleaseResult {
	return core.ReleaseResult{Change: r.Change.Core(), Abandoned: slices.Clone(r.Abandoned)}
}

// Effects is the answer of GET changes/{id}/effects.
type Effects struct {
	Effects []core.EffectView `json:"effects"`
}

// AckPrivate is the optional body of POST changes/{id}/private-output/ack;
// an empty body is not final.
type AckPrivate struct {
	Final bool `json:"final"`
}

// JournalEntry is core.JournalEntry.
type JournalEntry struct {
	Seq       int64           `json:"seq"`
	At        time.Time       `json:"at"`
	Actor     string          `json:"actor"`
	Action    string          `json:"action"`
	Version   int             `json:"version"`
	Phase     string          `json:"phase"`
	ChangeID  string          `json:"changeId"`
	Input     json.RawMessage `json:"input"`
	Result    string          `json:"result"`
	ErrorCode string          `json:"errorCode"`
	Source    string          `json:"source"`
}

// FromJournalEntry converts e.
func FromJournalEntry(e core.JournalEntry) JournalEntry {
	return JournalEntry{
		Seq: e.Seq, At: e.At.UTC(), Actor: e.Actor, Action: e.Action, Version: e.Version, Phase: e.Phase,
		ChangeID: e.ChangeID, Input: raw(e.Input), Result: e.Result, ErrorCode: e.ErrorCode, Source: e.Source,
	}
}

// Core converts e back.
func (e JournalEntry) Core() core.JournalEntry {
	return core.JournalEntry{
		Seq: e.Seq, At: e.At.UTC(), Actor: e.Actor, Action: e.Action, Version: e.Version, Phase: e.Phase,
		ChangeID: e.ChangeID, Input: raw(e.Input), Result: e.Result, ErrorCode: e.ErrorCode, Source: e.Source,
	}
}

// Ack is the body of POST journal/ack: the highest sequence the controller
// stored.
type Ack struct {
	Seq int64 `json:"seq"`
}

// The query parameters of GET changes.
var changeFilterParams = []string{"before", "hostId", "limit", "state"}

// ChangeFilterQuery encodes f as the query of GET changes, leaving out the
// fields that are not set.
func ChangeFilterQuery(f core.ChangeFilter) url.Values {
	q := url.Values{}
	for k, v := range map[string]string{"state": f.State, "hostId": f.HostID, "before": f.Before} {
		if v != "" {
			q.Set(k, v)
		}
	}
	if f.Limit != 0 {
		q.Set("limit", strconv.Itoa(f.Limit))
	}
	return q
}

// ParseChangeFilter decodes the query of GET changes. An unknown or
// repeated parameter, or a limit that is not a positive integer, is
// request.invalid_input.
func ParseChangeFilter(q url.Values) (core.ChangeFilter, error) {
	for _, k := range slices.Sorted(maps.Keys(q)) {
		switch {
		case !slices.Contains(changeFilterParams, k):
			return core.ChangeFilter{}, invalidQuery(k, "unknown parameter")
		case len(q[k]) > 1:
			return core.ChangeFilter{}, invalidQuery(k, "given more than once")
		}
	}
	f := core.ChangeFilter{State: q.Get("state"), HostID: q.Get("hostId"), Before: q.Get("before")}
	if s := q.Get("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 {
			return core.ChangeFilter{}, invalidQuery("limit", "must be a positive integer")
		}
		f.Limit = n
	}
	return f, nil
}

func invalidQuery(param, msg string) error {
	return &errcode.Error{
		Code: errcode.InvalidInput, Detail: fmt.Sprintf("query parameter %s: %s", param, msg),
		Fields: []errcode.FieldError{{Path: "/" + param, Message: msg}},
	}
}

func clonePtr[T any](p *T) *T {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

func utcPtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}

// raw copies m, mapping JSON null to nil, so an absent value is nil on both
// sides of the API.
func raw(m json.RawMessage) json.RawMessage {
	if m == nil || bytes.Equal(m, []byte("null")) {
		return nil
	}
	return bytes.Clone(m)
}
