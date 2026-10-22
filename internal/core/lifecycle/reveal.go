package lifecycle

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// phaseReveal is the journal phase of a reveal and of an internal read.
const phaseReveal = "reveal"

// revealAudit is the journal input of a reveal: what was revealed, never the
// value.
type revealAudit struct {
	Identity string `json:"identity"`
}

// Reveal runs a reveal action (spec 1.14) and returns its value, which no
// Redact touches. The reveal is journaled before the value is returned, so a
// value is never returned unaudited. Any action but a reveal action is
// action.unknown.
func (e *Engine) Reveal(ctx context.Context, p core.Principal, ref core.ActionRef, input json.RawMessage) (json.RawMessage, error) {
	return e.reveal(ctx, p, ref, input, false)
}

// ReadInternal runs an Internal read (spec 1.7) as Reveal runs a reveal
// action. Any action but an Internal read is action.unknown.
func (e *Engine) ReadInternal(ctx context.Context, p core.Principal, ref core.ActionRef, input json.RawMessage) (json.RawMessage, error) {
	return e.reveal(ctx, p, ref, input, true)
}

func (e *Engine) reveal(ctx context.Context, p core.Principal, ref core.ActionRef, input json.RawMessage, internal bool) (json.RawMessage, error) {
	def, err := e.action(ref)
	if err != nil {
		return nil, err
	}
	served := def.Spec.Reveal
	if internal {
		served = def.Spec.Internal
	}
	if !served || def.Handler.Reveal == nil {
		return nil, errcode.New(errcode.ActionUnknown, fmt.Sprintf("unknown action %s", ref))
	}
	if err := e.cfg.Validator.ValidateInput(def, input); err != nil {
		return nil, err
	}
	var identity string
	var value json.RawMessage
	err = e.protect(ref, "Reveal", func() error {
		var err error
		identity, value, err = def.Handler.Reveal(ctx, e.env(p, def), input)
		return err
	})
	if err != nil {
		return nil, err
	}
	audit, err := json.Marshal(revealAudit{Identity: identity})
	if err != nil {
		return nil, fmt.Errorf("lifecycle: encode the audit of %s: %w", ref, err)
	}
	err = e.cfg.Base.DB.Write(ctx, func(tx *sql.Tx) error {
		return e.cfg.Journal.Append(ctx, tx, core.JournalEntry{
			At: e.cfg.Base.Clock.Now(), Actor: p.Actor, Action: ref.Name, Version: ref.Version, Phase: phaseReveal,
			Input: audit, Result: resultOK, Source: p.Source,
		})
	})
	if err != nil {
		return nil, fmt.Errorf("lifecycle: journal the reveal of %s: %w", ref, err)
	}
	return value, nil
}
