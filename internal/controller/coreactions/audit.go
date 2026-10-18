package coreactions

import (
	"context"
	"encoding/json"
	"time"

	"github.com/invopop/jsonschema"

	"github.com/darkraise/darkpanel/internal/core"
)

// The page sizes of core.audit.list/v1 (spec section 10).
const (
	DefaultAuditLimit = 100
	MaxAuditLimit     = 500
)

// AuditListInput is the input of core.audit.list/v1.
type AuditListInput struct {
	Limit  int    `json:"limit,omitempty" jsonschema:"minimum=1,maximum=500" jsonschema_description:"Most entries to return, 1 to 500; 100 when omitted."`
	Before string `json:"before,omitempty" jsonschema_description:"The nextBefore of a previous page: only older entries are returned."`
	Actor  string `json:"actor,omitempty" jsonschema_description:"Only entries of this actor, such as user:<id> or local:root@<host id>."`
	HostID string `json:"hostId,omitempty" jsonschema_description:"Only entries of agent actions on this host."`
	Action string `json:"action,omitempty" jsonschema_description:"Only entries of this action, such as core.token.create."`
}

// AuditList is the output of core.audit.list/v1.
type AuditList struct {
	Entries    []AuditEntry `json:"entries" jsonschema_description:"Matching entries, newest first."`
	NextBefore *string      `json:"nextBefore" jsonschema:"nullable" jsonschema_description:"Pass as before to read the next page; null on the last page."`
}

// AuditEntry is one row of the audit log (spec section 12).
type AuditEntry struct {
	ID        string     `json:"id" jsonschema_description:"Entry id."`
	At        time.Time  `json:"at" jsonschema_description:"When the event happened."`
	Actor     string     `json:"actor" jsonschema_description:"Who acted, such as user:<id>, token:<id>, local:root@<host id>, anonymous or system:recovery."`
	HostID    *string    `json:"hostId" jsonschema:"nullable" jsonschema_description:"The host of an agent action; null for controller-side events."`
	Action    string     `json:"action" jsonschema_description:"The action, or core.auth.login and core.auth.logout."`
	Version   int        `json:"version" jsonschema_description:"The action version."`
	Phase     string     `json:"phase" jsonschema_description:"apply, confirm, revert, recover, interrupt, login or logout."`
	ChangeID  *string    `json:"changeId" jsonschema:"nullable" jsonschema_description:"The change; null for logins and logouts."`
	Input     AuditInput `json:"input" jsonschema_description:"The input, with secret fields redacted when it was recorded."`
	Result    string     `json:"result" jsonschema_description:"ok or error."`
	ErrorCode *string    `json:"errorCode" jsonschema:"nullable" jsonschema_description:"The error code of a failed event; null when it succeeded."`
	Source    *string    `json:"source" jsonschema:"nullable" jsonschema_description:"The client address, or socket; null when it was not recorded."`
}

// AuditInput is an audit entry's input as recorded, already redacted. Its
// schema describes every JSON value as not secret, so the engine's output
// redaction returns it unchanged instead of redacting every property no
// schema names.
type AuditInput json.RawMessage

// MarshalJSON returns the input as recorded, or null when there is none.
func (a AuditInput) MarshalJSON() ([]byte, error) {
	if len(a) == 0 {
		return []byte("null"), nil
	}
	return a, nil
}

// UnmarshalJSON keeps a copy of data.
func (a *AuditInput) UnmarshalJSON(data []byte) error {
	*a = append((*a)[:0], data...)
	return nil
}

// JSONSchema describes any JSON value, at any depth.
func (AuditInput) JSONSchema() *jsonschema.Schema {
	self := &jsonschema.Schema{Ref: "#/$defs/AuditInput"}
	return &jsonschema.Schema{AdditionalProperties: self, Items: self}
}

func (m module) auditActions() []core.ActionDef {
	return []core.ActionDef{
		core.DefineRead[AuditListInput, AuditList](core.ActionSpec{
			Name: "core.audit.list", Version: 1, Side: core.SideController,
			Summary: "List audit log entries, newest first, filtered by actor, host or action.",
		}, auditList{m}),
	}
}

type auditList struct{ m module }

func (h auditList) Read(ctx context.Context, _ core.Env, in AuditListInput) (AuditList, error) {
	if h.m.d.Audit == nil {
		return AuditList{}, errUnset("Deps.Audit")
	}
	limit := in.Limit
	if limit == 0 {
		limit = DefaultAuditLimit
	}
	entries, next, err := h.m.d.Audit.List(ctx, core.AuditQuery{
		Limit: min(limit, MaxAuditLimit), Before: in.Before, Actor: in.Actor, HostID: in.HostID, Action: in.Action,
	})
	if err != nil {
		return AuditList{}, err
	}
	out := AuditList{Entries: make([]AuditEntry, 0, len(entries))}
	for _, e := range entries {
		out.Entries = append(out.Entries, AuditEntry{
			ID: e.ID, At: e.At, Actor: e.Actor, HostID: nonEmpty(e.HostID), Action: e.Action, Version: e.Version,
			Phase: e.Phase, ChangeID: nonEmpty(e.ChangeID), Input: AuditInput(e.Input), Result: e.Result,
			ErrorCode: nonEmpty(e.ErrorCode), Source: nonEmpty(e.Source),
		})
	}
	out.NextBefore = nonEmpty(next)
	return out, nil
}

// nonEmpty is nil for "", so absent values encode as null.
func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
