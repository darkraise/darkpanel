package firewall

import (
	"bytes"
	"context"
	"fmt"
	"net/netip"
	"slices"
	"strconv"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/firewall/dockerfw"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/modules/firewall/model"
)

// ContainerAllowInput is the input of firewall.container.allow (v1).
type ContainerAllowInput struct {
	core.Guarded
	Binding dockerfw.Binding `json:"binding" jsonschema_description:"The published container port to allow."`
	From    netip.Prefix     `json:"from,omitzero" jsonschema_description:"The source range allowed; omitted: every source."`
}

// ContainerDenyInput is the input of firewall.container.deny (v1).
type ContainerDenyInput struct {
	core.Guarded
	Binding dockerfw.Binding `json:"binding" jsonschema_description:"The allowed published container port the deny narrows."`
	From    netip.Prefix     `json:"from" jsonschema_description:"The source range denied."`
}

// ContainerRemoveInput is the input of firewall.container.remove (v1).
type ContainerRemoveInput struct {
	core.Guarded
	ID string `json:"id" jsonschema_description:"The container rule's id, as firewall.docker lists it."`
}

// ContainerOp is one operation of firewall.containers.apply: an allowance or
// deny to store, or a stored container rule to remove.
type ContainerOp struct {
	Op      string            `json:"op" jsonschema:"enum=allow,enum=deny,enum=remove" jsonschema_description:"What the operation does."`
	Binding *dockerfw.Binding `json:"binding,omitempty" jsonschema_description:"The published container port of an allow or deny."`
	From    netip.Prefix      `json:"from,omitzero" jsonschema_description:"The source range of an allow (omitted: every source) or a deny."`
	ID      string            `json:"id,omitempty" jsonschema_description:"The container rule a remove deletes."`
}

// ContainersApplyInput is the input of firewall.containers.apply (v1).
type ContainersApplyInput struct {
	core.Guarded
	Ops []ContainerOp `json:"ops" jsonschema_description:"The operations, applied in order; empty: re-realize the stored rows against the current containers."`
}

// containerAllow is firewall.container.allow: store an allowance of a
// published container port and realize it when the integration is on.
func (f fw) containerAllow() core.ActionDef {
	return ctrMutation(f, core.ActionSpec{Name: "firewall.container.allow", Summary: "Allow access to a published container port."},
		func(in ContainerAllowInput) core.Guarded { return in.Guarded },
		func(in ContainerAllowInput) []ContainerOp {
			return []ContainerOp{{Op: "allow", Binding: &in.Binding, From: in.From}}
		}, false)
}

// containerDeny is firewall.container.deny: store a deny of a source range on
// an allowed container port and realize it when the integration is on.
func (f fw) containerDeny() core.ActionDef {
	return ctrMutation(f, core.ActionSpec{Name: "firewall.container.deny", Summary: "Deny a source range on an allowed container port."},
		func(in ContainerDenyInput) core.Guarded { return in.Guarded },
		func(in ContainerDenyInput) []ContainerOp {
			return []ContainerOp{{Op: "deny", Binding: &in.Binding, From: in.From}}
		}, false)
}

// containerRemove is firewall.container.remove: delete a stored container
// rule and its realization.
func (f fw) containerRemove() core.ActionDef {
	return ctrMutation(f, core.ActionSpec{Name: "firewall.container.remove", Summary: "Remove a container allowance or deny."},
		func(in ContainerRemoveInput) core.Guarded { return in.Guarded },
		func(in ContainerRemoveInput) []ContainerOp { return []ContainerOp{{Op: "remove", ID: in.ID}} }, false)
}

// containersApply is firewall.containers.apply: apply a list of container
// rule operations in order and realize the resulting rows in one change.
func (f fw) containersApply() core.ActionDef {
	return ctrMutation(f, core.ActionSpec{Name: "firewall.containers.apply", Summary: "Change container rules in one batch and realize them."},
		func(in ContainersApplyInput) core.Guarded { return in.Guarded },
		func(in ContainersApplyInput) []ContainerOp { return in.Ops }, true)
}

// ctrMutation declares a container rule action whose input maps to the
// operation list ops; batch makes refusals name the operation's index.
func ctrMutation[In any](f fw, spec core.ActionSpec, guarded func(In) core.Guarded, ops func(In) []ContainerOp, batch bool) core.ActionDef {
	return defineMutation(spec, mutation[In]{
		f:       f,
		guarded: guarded,
		build: func(ctx context.Context, env core.Env, in In) buildFunc {
			return func(cfg fwstate.Config, cur fwstate.Loaded) (change, error) {
				st, err := f.dockerState(ctx, env, cur)
				if err != nil {
					return change{}, err
				}
				return containerChange(env, cfg, st, ops(in), batch)
			}
		},
		check: func(ctx context.Context, env core.Env, in In, _ fwstate.Config) error {
			return containerRowsCheck(ctx, env, ops(in))
		},
	})
}

// ctrInvalid is the invalid-input refusal of one field.
func ctrInvalid(path, msg string) error {
	return &errcode.Error{Code: errcode.InvalidInput, Detail: msg, Fields: []errcode.FieldError{{Path: path, Message: msg}}}
}

// ctrSame reports whether two container rules are the same rule: the same
// binding, kind and source, whatever their ids.
func ctrSame(a, b dockerfw.ContainerRule) bool {
	return a.Binding == b.Binding && a.Kind == b.Kind && a.From == b.From
}

// containerChange applies ops in order to the stored rows of st and returns
// the change that stores the result and, with the integration on, realizes
// it. batch makes the field paths of a refusal /ops/<i>/…; every refusal
// comes with the zero change.
func containerChange(env core.Env, cfg fwstate.Config, st dockerState, ops []ContainerOp, batch bool) (change, error) {
	if st.cfg.Enabled && !st.available {
		return change{}, errcode.New(errcode.FirewallUnsupportedIntegration,
			"container rules cannot change while the integration is on and Docker is unavailable: "+st.reason)
	}
	newID := newULID(env)
	rows := slices.Clone(st.rows)
	var add []dockerfw.ContainerRule
	var del []string
	for i, op := range ops {
		path := ""
		if batch {
			path = "/ops/" + strconv.Itoa(i)
		}
		switch op.Op {
		case "allow", "deny":
			if op.Binding == nil {
				return change{}, ctrInvalid(path+"/binding", "an allow or deny needs the binding")
			}
			kind := dockerfw.Allow
			if op.Op == "deny" {
				kind = dockerfw.Deny
			}
			r := dockerfw.ContainerRule{ID: newID(), Binding: *op.Binding, Kind: kind, From: op.From}
			if err := r.Binding.Validate(); err != nil {
				return change{}, fieldErrors(err, path+"/binding")
			}
			if err := r.Validate(st.cfg.Settings); err != nil {
				return change{}, fieldErrors(err, path)
			}
			if slices.ContainsFunc(rows, func(s dockerfw.ContainerRule) bool { return ctrSame(s, r) }) {
				continue
			}
			if kind == dockerfw.Deny && !slices.ContainsFunc(rows, func(s dockerfw.ContainerRule) bool {
				return s.Kind == dockerfw.Allow && s.Binding == r.Binding
			}) {
				return change{}, ctrInvalid(path+"/binding", "a deny narrows an allowance, and this binding has none")
			}
			rows = append(rows, r)
			add = append(add, r)
		case "remove":
			at := slices.IndexFunc(rows, func(s dockerfw.ContainerRule) bool { return s.ID == op.ID })
			if op.ID == "" || at < 0 {
				return change{}, ctrInvalid(path+"/id", "no container rule has this id")
			}
			rows = slices.Delete(rows, at, at+1)
			del = append(del, op.ID)
		default:
			return change{}, ctrInvalid(path+"/op", "must be allow, deny or remove")
		}
	}

	c := change{rowsAdd: add, rowsDel: del}
	var desired []model.Rule
	if st.cfg.Enabled {
		st.rows = rows
		st.realized = dockerfw.Realize(rows, st.facts, st.cfg.Settings)
		files, err := blockFiles(cfg, st, true)
		if err != nil {
			return change{}, err
		}
		for p, data := range files {
			if !bytes.Equal(data, cfg.Files[p].Data) {
				if c.files == nil {
					c.files = map[string][]byte{}
				}
				c.files[p] = data
			}
		}
		c.reload = len(c.files) > 0
		desired = st.realized.Rules
	}
	ruleOps, err := containerRuleOps(cfg, desired, newID)
	if err != nil {
		return change{}, err
	}
	c.ops = ruleOps
	return c, nil
}

// containerRowsCheck is the verify of the container rule actions: every row
// ops removed is gone, and every allowance or deny after the last removal of
// ops is stored. Operations before a later removal are not checked, because
// that removal may have taken the row they stored or found.
func containerRowsCheck(ctx context.Context, env core.Env, ops []ContainerOp) error {
	rows, err := listContainerRules(ctx, env.DB)
	if err != nil {
		return fmt.Errorf("firewall: verify: %w", err)
	}
	last := -1
	for i, op := range ops {
		if op.Op == "remove" {
			last = i
		}
	}
	for i, op := range ops {
		switch op.Op {
		case "remove":
			if slices.ContainsFunc(rows, func(r dockerfw.ContainerRule) bool { return r.ID == op.ID }) {
				return mismatch("container rule %s is still stored after the change", op.ID)
			}
		case "allow", "deny":
			if i < last {
				continue
			}
			want := dockerfw.ContainerRule{Binding: *op.Binding, Kind: dockerfw.Kind(op.Op), From: op.From}
			if !slices.ContainsFunc(rows, func(r dockerfw.ContainerRule) bool { return ctrSame(r, want) }) {
				return mismatch("no %s rule of %s:%d/%s is stored after the change", op.Op, op.Binding.Selector, op.Binding.HostPort, op.Binding.Proto)
			}
		}
	}
	return nil
}
