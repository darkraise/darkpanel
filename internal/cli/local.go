package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/darkraise/darkpanel/internal/agent/api"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// annotationLocal marks the generated agent-side commands, the only ones
// --local serves.
const annotationLocal = "darkpanel.local"

// AnnotationRefusalStatus gives a hand-written command the exit status of
// its --local refusal. The refusal runs in the root's pre-run hook, outside
// the command's own argument and flag error handling, so a command whose exit
// statuses carry meaning of their own has to name the status here.
const AnnotationRefusalStatus = "darkpanel.refusal-status"

// guardLocal makes every command but the generated agent-side ones refuse
// --local. The check runs as root's persistent pre-run hook; cobra runs
// only the nearest such hook, so a hand-written command's own hook gets
// the check put before it.
func guardLocal(root *cobra.Command) {
	root.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error { return refuseLocal(cmd) }
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		for _, sub := range c.Commands() {
			if hook, plain := sub.PersistentPreRunE, sub.PersistentPreRun; hook != nil || plain != nil {
				sub.PersistentPreRun = nil
				sub.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
					if err := refuseLocal(cmd); err != nil {
						return err
					}
					if hook != nil {
						return hook(cmd, args)
					}
					plain(cmd, args)
					return nil
				}
			}
			walk(sub)
		}
	}
	walk(root)
}

func refuseLocal(cmd *cobra.Command) error {
	if local, _ := cmd.Flags().GetBool("local"); !local || cmd.Annotations[annotationLocal] != "" {
		return nil
	}
	err := usagef("--local reaches only this host's agent; %s is not an agent-side command", cmd.CommandPath())
	if code, convErr := strconv.Atoi(cmd.Annotations[AnnotationRefusalStatus]); convErr == nil {
		return ExitWith(code, err)
	}
	return err
}

// useLocal reports whether an agent-side command runs with --local, which
// leaves no host and no API to choose.
func useLocal(cmd *cobra.Command) (bool, error) {
	local, _ := cmd.Flags().GetBool("local")
	if !local {
		return false, nil
	}
	for _, flag := range []string{"host", "url"} {
		if cmd.Flags().Changed(flag) {
			return false, usagef("--%s does not apply with --local: the agent socket serves this host only", flag)
		}
	}
	return true, nil
}

// socketError is a failed dial of the agent socket.
type socketError struct {
	path string
	err  error
}

func (e *socketError) Error() string {
	return fmt.Sprintf("the agent socket %s does not answer: %v", e.path, e.err)
}

func (e *socketError) Unwrap() error { return e.err }

// absent reports whether no agent serves the socket: the path is missing,
// or nothing listens on it.
func (e *socketError) absent() bool {
	return errors.Is(e.err, syscall.ENOENT) || errors.Is(e.err, syscall.ECONNREFUSED)
}

func (a *app) dialAgent(ctx context.Context) (net.Conn, error) {
	var d net.Dialer
	c, err := d.DialContext(ctx, "unix", a.opts.AgentSocket)
	if err != nil {
		return nil, &socketError{path: a.opts.AgentSocket, err: err}
	}
	return c, nil
}

// withAgent runs run with a client of the agent socket. A dial first finds
// a socket no agent serves before anything is read or planned; then def's
// local fallback, if it has one, runs instead. def is nil for a resource,
// which has no fallback.
func (a *app) withAgent(cmd *cobra.Command, def *core.ActionDef, input json.RawMessage, secrets []string,
	run func(*api.Client) error) error {
	conn, err := a.dialAgent(cmd.Context())
	if err != nil {
		var down *socketError
		if errors.As(err, &down) && down.absent() && def != nil {
			if fallback, ok := a.opts.LocalFallbacks[def.Spec.Name]; ok {
				return a.runFallback(cmd, *def, fallback, input, secrets)
			}
		}
		return err
	}
	if err := conn.Close(); err != nil {
		return err
	}
	return run(api.NewClient(api.ClientConfig{Dial: a.dialAgent}))
}

// runFallback runs def in this process, after the same Apply? [y/N]
// question as the mutating flow; a read asks nothing.
func (a *app) runFallback(cmd *cobra.Command, def core.ActionDef, fallback LocalFallback, input json.RawMessage, secrets []string) error {
	_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "darkpanel: the agent socket %s does not answer; running %s in this process\n",
		sanitize(a.opts.AgentSocket), def.Spec.Name)
	if def.Spec.Class != core.ClassRead {
		if err := a.askApply(cmd, false); err != nil {
			return err
		}
	}
	out, err := fallback(cmd.Context(), input)
	if err != nil {
		return localError(err)
	}
	return a.show(cmd, nullOutput(out), secrets)
}

// The agent socket's caller is always the local root user, whatever
// principal the client sends, so the local calls send the zero principal.

func (a *app) localRead(cmd *cobra.Command, def core.ActionDef, input json.RawMessage, secrets []string) error {
	return a.withAgent(cmd, &def, input, secrets, func(c *api.Client) error {
		ctx, cancel := context.WithTimeout(cmd.Context(), requestTimeout)
		defer cancel()
		out, err := c.Read(ctx, core.Principal{}, core.ActionRef{Name: def.Spec.Name, Version: def.Spec.Version}, input)
		if err != nil {
			return localError(err)
		}
		return a.show(cmd, nullOutput(out), secrets)
	})
}

func (a *app) localResource(cmd *cobra.Command, name string, secrets []string) error {
	return a.withAgent(cmd, nil, nil, secrets, func(c *api.Client) error {
		ctx, cancel := context.WithTimeout(cmd.Context(), requestTimeout)
		defer cancel()
		out, err := c.Resource(ctx, core.Principal{}, name)
		if err != nil {
			return localError(err)
		}
		return a.show(cmd, nullOutput(out), secrets)
	})
}

func (a *app) localMutating(cmd *cobra.Command, def core.ActionDef, input json.RawMessage, secrets []string) error {
	return a.withAgent(cmd, &def, input, secrets, func(c *api.Client) error {
		return a.applyAndShow(cmd, localCalls(c, def), input, secrets)
	})
}

// localCalls are def's plan and apply on the agent socket. They convert the
// agent's plan and change to the API's shapes, so the plan, the change and
// --json print as they do through the controller API.
func localCalls(c *api.Client, def core.ActionDef) actionCalls {
	ref := core.ActionRef{Name: def.Spec.Name, Version: def.Spec.Version}
	return actionCalls{
		plan: func(ctx context.Context, input any) (planWire, error) {
			raw, err := json.Marshal(input)
			if err != nil {
				return planWire{}, fmt.Errorf("encode the input: %w", err)
			}
			ctx, cancel := context.WithTimeout(ctx, requestTimeout)
			defer cancel()
			v, err := c.Plan(ctx, core.Principal{}, ref, raw)
			if err != nil {
				return planWire{}, localError(err)
			}
			return planWire{PlanID: v.PlanID, HostID: optional(v.HostID), Action: v.Action, Version: v.Version,
				Diff: v.Diff, Guards: v.Guards, Confirmation: optional(v.Confirmation)}, nil
		},
		apply: func(ctx context.Context, planID, confirmation string, confirmNow bool) (changeWire, json.RawMessage, error) {
			ctx, cancel := context.WithTimeout(ctx, requestTimeout)
			defer cancel()
			v, err := c.Apply(ctx, core.Principal{}, planID, core.ApplyOptions{Confirmation: confirmation, ConfirmNow: confirmNow})
			if err != nil {
				return changeWire{}, nil, localError(err)
			}
			ch := changeWire{ID: v.ID, HostID: optional(v.HostID), Action: v.Action, Version: v.Version, Class: v.Class,
				State: v.State, Actor: v.Actor, CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt, RevertDeadline: v.RevertDeadline,
				Output: nullOutput(v.Output), CompensatedBy: optional(v.CompensatedBy)}
			if v.Error != nil {
				ch.Error = &changeErrorWire{Code: v.Error.Code, Message: v.Error.Message}
			}
			raw, err := json.Marshal(ch)
			if err != nil {
				return changeWire{}, nil, fmt.Errorf("encode the change: %w", err)
			}
			return ch, raw, nil
		},
	}
}

// localError makes a coded error of the agent socket or of a fallback read
// as the API's refusal would, so its code chooses the exit code.
func localError(err error) error {
	var coded *errcode.Error
	if !errors.As(err, &coded) {
		return err
	}
	detail := coded.Detail
	if coded.Err != nil {
		detail += ": " + coded.Err.Error()
	}
	return &problemError{Status: coded.Code.Status(), Code: coded.Code, Detail: detail, Fields: coded.Fields}
}

func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// nullOutput is the output the API shows for none: JSON null.
func nullOutput(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage("null")
	}
	return raw
}
