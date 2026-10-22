package lifecycle

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

const phaseResolve = "resolve"

// effectOf reads one effect of a change; a missing one is not_found.
func effectOf(ctx context.Context, q queryer, changeID string, seq int) (core.Effect, error) {
	effs, err := loadEffects(ctx, q, changeID)
	if err != nil {
		return core.Effect{}, fmt.Errorf("lifecycle: read effects of %s: %w", changeID, err)
	}
	for _, eff := range effs {
		if eff.Seq == seq {
			return eff, nil
		}
	}
	return core.Effect{}, errcode.New(errcode.NotFound, fmt.Sprintf("change %s has no effect %d", changeID, seq))
}

// allowedResolutions is Observation.Allowed, or both resolutions when the
// module allows every one.
func allowedResolutions(obs core.Observation) []string {
	if obs.Allowed == nil {
		return []string{core.ResolutionAccept, core.ResolutionReconcile}
	}
	return slices.Clone(obs.Allowed)
}

// Observe previews an effect for its resolution (spec 1.2, Resolving an
// unresolved effect): it calls the action's Observe and answers the
// effect, the observation and the resolutions the module allows. It
// changes nothing.
func (e *Engine) Observe(ctx context.Context, p core.Principal, changeID string, seq int) (core.ObserveResult, error) {
	q := e.cfg.Base.DB.Read()
	f, err := loadFacts(ctx, q, changeID)
	if err != nil {
		return core.ObserveResult{}, err
	}
	def, err := e.action(f.ref())
	if err != nil {
		return core.ObserveResult{}, err
	}
	eff, err := effectOf(ctx, q, changeID, seq)
	if err != nil {
		return core.ObserveResult{}, err
	}
	call, err := changeCall(ctx, q, changeID)
	if err != nil {
		return core.ObserveResult{}, err
	}
	obs, err := e.observeCall(ctx, e.changeEnv(p, def, changeID, call), def, eff)
	if err != nil {
		return core.ObserveResult{}, err
	}
	return core.ObserveResult{
		Effect: effectView(eff),
		Observation: core.ObservationView{
			Current: obs.Current, Resource: obs.Resource, Matches: obs.Matches,
			Candidates: nonNil(obs.Candidates), Exclusive: obs.Exclusive,
		},
		Allowed: allowedResolutions(obs),
	}, nil
}

// Resolve records the operator's resolution of an unresolved effect. It
// claims owner resolve on the change that holds the locks (the change, or
// the target of a failed repair change), observes the effect, and records
// the outcome, the module's Resolved bookkeeping, the journal entry and the
// end of the claim in one transaction. A resolution never writes to the
// host.
func (e *Engine) Resolve(ctx context.Context, p core.Principal, changeID string, seq int, req core.ResolveRequest) (core.EffectView, error) {
	if req.Resolution != core.ResolutionAccept && req.Resolution != core.ResolutionReconcile {
		return core.EffectView{}, &errcode.Error{
			Code: errcode.InvalidInput, Detail: "a resolution is accept or reconcile",
			Fields: []errcode.FieldError{{Path: "/resolution", Message: "must be accept or reconcile"}},
		}
	}
	e.applyMu.Lock()
	holder, def, eff, err := e.claimResolve(ctx, changeID, seq)
	e.applyMu.Unlock()
	if err != nil {
		return core.EffectView{}, err
	}
	// The claim is committed: whatever happens to the request, the claim
	// must end in a recorded outcome or a released owner.
	ctx = context.WithoutCancel(ctx)
	v, err := e.recordResolution(ctx, p, holder, def, eff, req)
	if err != nil {
		e.endResolveClaim(holder)
		return core.EffectView{}, err
	}
	return v, nil
}

// claimResolve checks that the effect may be resolved now and claims owner
// resolve on the change that holds the locks, in one transaction.
func (e *Engine) claimResolve(ctx context.Context, changeID string, seq int) (changeFacts, core.ActionDef, core.Effect, error) {
	var holder changeFacts
	var def core.ActionDef
	var eff core.Effect
	err := e.cfg.Base.DB.Write(ctx, func(tx *sql.Tx) error {
		f, err := loadFacts(ctx, tx, changeID)
		if err != nil {
			return err
		}
		if def, err = e.action(f.ref()); err != nil {
			return err
		}
		if eff, err = effectOf(ctx, tx, changeID, seq); err != nil {
			return err
		}
		if eff.State != core.EffectUnresolved {
			return errcode.New(errcode.ChangeNotPending,
				fmt.Sprintf("effect %d of change %s is %s, not unresolved", seq, changeID, eff.State))
		}
		holder = f
		switch {
		case f.state == core.StateNeedsRepair:
		case f.state == core.StateFailed && f.repairs != "":
			if holder, err = loadFacts(ctx, tx, f.repairs); err != nil {
				return err
			}
			if holder.state != core.StateNeedsRepair {
				return errcode.New(errcode.ChangeNotPending, fmt.Sprintf(
					"change %s repairs change %s, which is %s, not needs_repair", changeID, holder.id, holder.state))
			}
		default:
			return errcode.New(errcode.ChangeNotPending, fmt.Sprintf(
				"change %s is %s; only the effects of a change in needs_repair are resolved", changeID, f.state))
		}
		now := e.cfg.Base.Clock.Now()
		if now.Before(eff.CommitBy) {
			return errcode.New(errcode.ChangeBusy, fmt.Sprintf(
				"the call may still commit until %s", eff.CommitBy.UTC().Format(time.RFC3339)))
		}
		return claim(ctx, tx, now, holder.id, "", core.OwnerResolve, holder.state, holder.state)
	})
	var coded *errcode.Error
	if err != nil && !errors.As(err, &coded) {
		err = fmt.Errorf("lifecycle: claim the resolution of %s: %w", changeID, err)
	}
	return holder, def, eff, err
}

// recordResolution observes the effect and records the resolution's
// outcome. Any error rolls the record back; the caller then ends the
// claim.
func (e *Engine) recordResolution(ctx context.Context, p core.Principal, holder changeFacts, def core.ActionDef,
	eff core.Effect, req core.ResolveRequest) (core.EffectView, error) {
	call, err := changeCall(ctx, e.cfg.Base.DB.Read(), eff.ChangeID)
	if err != nil {
		return core.EffectView{}, err
	}
	obs, err := e.observeCall(ctx, e.changeEnv(p, def, eff.ChangeID, call), def, eff)
	if err != nil {
		return core.EffectView{}, err
	}
	u, err := resolution(eff, obs, req)
	if err != nil {
		return core.EffectView{}, err
	}
	now := e.cfg.Base.Clock.Now()
	u.Resolution, u.ResolvedBy, u.ResolvedAt = req.Resolution, p.Actor, &now
	entry, err := json.Marshal(resolveEntry{Seq: eff.Seq, Resolution: req.Resolution, Resource: req.Resource})
	if err != nil {
		return core.EffectView{}, fmt.Errorf("lifecycle: encode the resolution: %w", err)
	}
	var v core.EffectView
	err = e.cfg.Base.DB.Write(ctx, func(tx *sql.Tx) error {
		if err := setEffect(ctx, tx, now, eff.ChangeID, eff.Seq, []core.EffectState{core.EffectUnresolved}, u); err != nil {
			return err
		}
		after, err := effectOf(ctx, tx, eff.ChangeID, eff.Seq)
		if err != nil {
			return err
		}
		if def.Handler.Resolved != nil {
			if err := e.protect(def.Ref(), "Resolved", func() error {
				return def.Handler.Resolved(ctx, tx, after, req.Resolution)
			}); err != nil {
				return err
			}
		}
		if err := e.journalChange(ctx, tx, now, eff.ChangeID, p, phaseResolve, resultOK, "", entry); err != nil {
			return err
		}
		if err := claim(ctx, tx, now, holder.id, core.OwnerResolve, "", holder.state, holder.state); err != nil {
			return err
		}
		v = effectView(after)
		return nil
	})
	return v, err
}

// resolveEntry is the journal input of a resolution.
type resolveEntry struct {
	Seq        int    `json:"seq"`
	Resolution string `json:"resolution"`
	Resource   string `json:"resource,omitempty"`
}

// resolution is the outcome table of spec 1.2: accept keeps what is there;
// reconcile skips an effect whose object is back in its undo state, and
// otherwise records what is there as made, after the operator picked one
// of several candidates.
func resolution(eff core.Effect, obs core.Observation, req core.ResolveRequest) (effectUpdate, error) {
	allowed := allowedResolutions(obs)
	if !slices.Contains(allowed, req.Resolution) {
		return effectUpdate{}, errcode.New(errcode.InvalidInput, fmt.Sprintf(
			"resolution %s is not allowed for effect %d; allowed: %s", req.Resolution, eff.Seq, strings.Join(allowed, ", ")))
	}
	cur := obs.Current
	if req.Resolution == core.ResolutionAccept {
		return effectUpdate{State: core.EffectAccepted, Current: &cur}, nil
	}
	if stateEqual(cur, undoTo(eff)) {
		return effectUpdate{State: core.EffectSkipped}, nil
	}
	resource, post := obs.Resource, obs.Current
	switch {
	case eff.Kind == core.EffectCreate && obs.Matches > 1:
		i := slices.IndexFunc(obs.Candidates, func(c core.Candidate) bool { return c.Resource == req.Resource })
		if req.Resource == "" || i < 0 {
			return effectUpdate{}, errcode.New(errcode.InvalidInput, fmt.Sprintf(
				"the identity matches %d objects; name one of the candidates as the resource", obs.Matches))
		}
		resource, post = obs.Candidates[i].Resource, obs.Candidates[i].State
	case req.Resource != "" && req.Resource != obs.Resource:
		return effectUpdate{}, errcode.New(errcode.InvalidInput,
			fmt.Sprintf("resource %s is not the observed object", req.Resource))
	}
	u := effectUpdate{State: core.EffectMade, Post: &post}
	if resource != "" {
		u.Resource = &resource
	}
	return u, nil
}

// endResolveClaim gives back a resolution's claim after a refusal or a
// failure, under the cleanup bound so a cancelled request still ends it.
func (e *Engine) endResolveClaim(holder changeFacts) {
	ctx, cancel := e.cleanup()
	defer cancel()
	err := e.cfg.Base.DB.Write(ctx, func(tx *sql.Tx) error {
		return claim(ctx, tx, e.cfg.Base.Clock.Now(), holder.id, core.OwnerResolve, "", holder.state, holder.state)
	})
	if err != nil {
		e.cfg.Base.Log.Error("lifecycle: end a resolution's claim", "change", holder.id, "error", err)
	}
}
