package lifecycle

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
)

// StepsGrace is added to the sum of a composite's step deadlines to bound
// its steps phase (1.3 Deadlines).
const StepsGrace = 5 * time.Minute

// Children is how the composite runner reaches its steps. The dispatcher
// implements it for every host, so a child is planned, applied, indexed and
// ingested like an operator's change; hostID is empty for a controller-side
// child.
type Children interface {
	Plan(ctx context.Context, p core.Principal, hostID string, a core.ActionRef, input json.RawMessage, opts core.PlanOptions) (core.PlanView, error)
	Apply(ctx context.Context, p core.Principal, hostID string, a core.ActionRef, planID string, opts core.ApplyOptions) (core.ChangeView, error)
	Read(ctx context.Context, p core.Principal, hostID string, a core.ActionRef, input json.RawMessage) (json.RawMessage, error)
	Confirm(ctx context.Context, p core.Principal, changeID string) (core.ChangeView, error)
	Revert(ctx context.Context, p core.Principal, changeID string) (core.ChangeView, error)
	Undo(ctx context.Context, p core.Principal, hostID, changeID string, call *core.CallContext) (core.ChangeView, error)
	Release(ctx context.Context, p core.Principal, hostID, changeID, reason string, call *core.CallContext) (core.ReleaseResult, error)
	Lookup(ctx context.Context, hostID, planID string, private bool) (core.PlanLookup, error)
	Retire(ctx context.Context, hostID, planID string) (core.PlanLookup, error)
	AckPrivate(ctx context.Context, hostID, changeID string, final bool) error
}

// SetChildren gives the engine the children its composites run through.
// The controller calls it once, before RecoverComposites; an engine without
// children refuses composites.
func (e *Engine) SetChildren(c Children) {
	e.compMu.Lock()
	defer e.compMu.Unlock()
	e.children = c
}

func (e *Engine) kids() (Children, error) {
	e.compMu.Lock()
	defer e.compMu.Unlock()
	if e.children == nil {
		return nil, fmt.Errorf("lifecycle: composites need children; SetChildren was not called")
	}
	return e.children, nil
}

var stepIDPattern = regexp.MustCompile(`^[a-z][a-z0-9]*$`)

// stepRefPattern is a string that is exactly one ${steps...} reference.
var stepRefPattern = regexp.MustCompile(`^\$\{steps\.[a-z][a-z0-9]*\.output\.[^${}]+\}$`)

// redactedText is what Config.Redact writes in place of a secret.
const redactedText = "[redacted]"

// plannedStep is one step of a composite after its plan-time checks;
// Template is its input with ${input...} resolved.
type plannedStep struct {
	Step     core.Step
	Ordinal  int
	Def      core.ActionDef
	Template json.RawMessage
	Refs     []string
}

// compositePlan is a composite's plan (1.3 Preview). It checks the Steps
// result, resolves ${input...}, and dry-runs every step without an output
// reference as p. The dry-runs' guards join the composite's, prefixed with
// the step id, and their diff lines carry "[<step id>]". The state hash is
// SHA-256 over the input and each dry-run's input hash, diff and guards,
// which stand for its state hash because a plan view does not carry one.
// Dry-run plans are never applied; they expire unused.
func (e *Engine) compositePlan(ctx context.Context, p core.Principal, def core.ActionDef, input json.RawMessage) (core.PlanOutcome, []plannedStep, error) {
	kids, err := e.kids()
	if err != nil {
		return core.PlanOutcome{}, nil, err
	}
	steps, err := e.compositeSteps(ctx, p, def, input)
	if err != nil {
		return core.PlanOutcome{}, nil, err
	}
	ordinal := map[string]int{}
	for _, s := range steps {
		ordinal[s.Step.ID] = s.Ordinal
	}
	out := core.PlanOutcome{Diff: []core.DiffLine{}, Guards: []core.Guard{}}
	material := append([]byte(nil), input...)
	for _, s := range steps {
		name := s.Def.Ref().String()
		switch {
		case s.Def.Spec.Class == core.ClassRead:
			out.Diff = append(out.Diff, core.DiffLine{Op: " ", Text: fmt.Sprintf("read step %s: %s", s.Step.ID, name)})
		case len(s.Refs) > 0:
			after := s.Refs[0]
			for _, r := range s.Refs {
				if ordinal[r] > ordinal[after] {
					after = r
				}
			}
			out.Diff = append(out.Diff, core.DiffLine{Op: " ",
				Text: fmt.Sprintf("planned after step %s: %s %s", after, name, s.Template)})
		default:
			final, err := resolveStepRefs(s.Template, nil)
			if err != nil {
				return core.PlanOutcome{}, nil, err
			}
			v, err := kids.Plan(ctx, p, s.Step.Host, s.Def.Ref(), final, core.PlanOptions{
				Call: &core.CallContext{ParentAction: def.Spec.Name, StepID: s.Step.ID},
			})
			if err != nil {
				return core.PlanOutcome{}, nil, err
			}
			for _, d := range v.Diff {
				out.Diff = append(out.Diff, core.DiffLine{Op: d.Op, Text: "[" + s.Step.ID + "] " + d.Text})
			}
			for _, g := range v.Guards {
				g.ID = s.Step.ID + "/" + g.ID
				out.Guards = append(out.Guards, g)
			}
			fp, err := json.Marshal(struct {
				InputHash string
				Diff      []core.DiffLine
				Guards    []core.Guard
			}{v.InputHash, v.Diff, v.Guards})
			if err != nil {
				return core.PlanOutcome{}, nil, fmt.Errorf("lifecycle: encode the dry-run of step %s: %w", s.Step.ID, err)
			}
			material = append(append(material, 0), fp...)
		}
	}
	sum := sha256.Sum256(material)
	out.StateHash = hex.EncodeToString(sum[:])
	return out, steps, nil
}

// compositeSteps calls the composite's Steps and checks the result against
// 1.3's registry rules; a violation is a module bug, answered internal.
func (e *Engine) compositeSteps(ctx context.Context, p core.Principal, def core.ActionDef, input json.RawMessage) ([]plannedStep, error) {
	var raw []core.Step
	err := e.protect(def.Ref(), "Steps", func() error {
		var err error
		raw, err = def.Handler.Steps(ctx, e.env(p, def), input)
		return err
	})
	if err != nil {
		return nil, err
	}
	bug := func(format string, args ...any) error {
		return fmt.Errorf("lifecycle: composite %s: %s", def.Ref(), fmt.Sprintf(format, args...))
	}
	if len(raw) == 0 {
		return nil, bug("Steps returned no step")
	}
	ordinal := map[string]int{}
	follows := map[string]map[string]bool{}
	revertible := -1
	steps := make([]plannedStep, 0, len(raw))
	for i, st := range raw {
		if !stepIDPattern.MatchString(st.ID) {
			return nil, bug("step id %q does not match [a-z][a-z0-9]*", st.ID)
		}
		if _, dup := ordinal[st.ID]; dup {
			return nil, bug("step id %s is used twice", st.ID)
		}
		set := map[string]bool{}
		for _, a := range st.After {
			if _, ok := ordinal[a]; !ok {
				return nil, bug("step %s follows %q, which is not an earlier step", st.ID, a)
			}
			set[a] = true
			for f := range follows[a] {
				set[f] = true
			}
		}
		ordinal[st.ID], follows[st.ID] = i, set
		cdef, ok := e.cfg.Registry.Action(st.Action)
		if !ok {
			return nil, bug("step %s: action %s is not registered", st.ID, st.Action)
		}
		switch {
		case cdef.Spec.Side == core.SideController && st.Host != "":
			return nil, bug("step %s: %s is a controller-side action and takes no host", st.ID, st.Action)
		case cdef.Spec.Side == core.SideAgent && st.Host == "":
			return nil, bug("step %s: %s is an agent action and needs a host", st.ID, st.Action)
		}
		switch cdef.Spec.Class {
		case core.ClassComposite, core.ClassIrreversible:
			return nil, bug("step %s: a step is never %s", st.ID, cdef.Spec.Class)
		case core.ClassRevertible:
			if revertible >= 0 {
				return nil, bug("step %s: a composite has at most one revertible step", st.ID)
			}
			revertible = i
		case core.ClassCompensable:
			if def.Spec.Unwind == core.UnwindReverse && cdef.Handler.Inverse == nil {
				return nil, bug("step %s: %s has no undo handler, which a reverse composite's compensable steps need", st.ID, st.Action)
			}
		}
		if def.Spec.Unwind == core.UnwindForward && cdef.Spec.Class != core.ClassRead && !cdef.Spec.Converges {
			return nil, bug("step %s: %s does not converge, and a forward composite repeats its steps", st.ID, st.Action)
		}
		// A repaired child's secret output is never kept for its composite,
		// so a forward composite could not advance past a kept step.
		if def.Spec.Unwind == core.UnwindForward && compositeSecretType(cdef.OutputType) {
			return nil, bug("step %s: %s has a secret output, which a forward composite's steps never have", st.ID, st.Action)
		}
		tmpl, err := resolveInputRefs(st.Input, input)
		if err != nil {
			return nil, err
		}
		refs, err := stepRefs(tmpl)
		if err != nil {
			return nil, bug("step %s: %v", st.ID, err)
		}
		for _, r := range refs {
			if !set[r] {
				return nil, bug("step %s references step %s, which it does not follow", st.ID, r)
			}
		}
		if err := e.secretRefsOnly(cdef, tmpl); err != nil {
			return nil, bug("step %s: %v", st.ID, err)
		}
		if len(refs) == 0 {
			final, err := resolveStepRefs(tmpl, nil)
			if err != nil {
				return nil, err
			}
			if err := e.cfg.Validator.ValidateInput(cdef, final); err != nil {
				return nil, err
			}
		}
		steps = append(steps, plannedStep{Step: st, Ordinal: i, Def: cdef, Template: tmpl, Refs: refs})
	}
	if revertible >= 0 && revertible != len(raw)-1 {
		return nil, bug("step %s: the revertible step must be the last", raw[revertible].ID)
	}
	return steps, nil
}

// secretRefsOnly refuses a template holding a literal value in a field the
// child's input marks secret: redacting the template must replace only
// ${steps...} references, whose values go only into the sealed input.
func (e *Engine) secretRefsOnly(def core.ActionDef, tmpl json.RawMessage) error {
	var orig, red any
	if err := json.Unmarshal(tmpl, &orig); err != nil {
		return fmt.Errorf("the template is not JSON: %w", err)
	}
	if err := json.Unmarshal(e.cfg.Redact(def.InputType, tmpl), &red); err != nil {
		return fmt.Errorf("the redacted template is not JSON: %w", err)
	}
	return secretPositions("", orig, red)
}

func secretPositions(path string, orig, red any) error {
	if s, ok := red.(string); ok && s == redactedText {
		if o, ok := orig.(string); ok && stepRefPattern.MatchString(o) {
			return nil
		}
		if path == "" {
			path = "/"
		}
		return fmt.Errorf("the secret field %s holds a literal value; only a ${steps...} reference may fill it", path)
	}
	switch r := red.(type) {
	case map[string]any:
		o, _ := orig.(map[string]any)
		for k, v := range r {
			if err := secretPositions(path+"/"+k, o[k], v); err != nil {
				return err
			}
		}
	case []any:
		o, _ := orig.([]any)
		for i, v := range r {
			var ov any
			if i < len(o) {
				ov = o[i]
			}
			if err := secretPositions(fmt.Sprintf("%s/%d", path, i), ov, v); err != nil {
				return err
			}
		}
	}
	return nil
}

// compositeTimeout is the length of a composite's steps phase: the sum of
// its steps' deadlines plus StepsGrace (1.3 Deadlines).
func (e *Engine) compositeTimeout(steps []plannedStep) time.Duration {
	d := StepsGrace
	for _, s := range steps {
		d += e.applyTimeout(s.Def)
	}
	return d
}
