package dockerapi

import (
	"context"
	"net/http"
	"net/url"
)

// ExecCreate is the command an exec instance runs, and the user it runs as
// ("uid:gid", or empty for the container's user).
type ExecCreate struct {
	Cmd  []string
	User string
}

// ExecInspect is the state of an exec instance. ExitCode is nil until the
// command has exited: the daemon answers null while it runs.
type ExecInspect struct {
	ID       string
	Running  bool
	ExitCode *int
}

// TopResult is the process listing of a container: the ps column titles and,
// per process, one value for each title.
type TopResult struct {
	Titles    []string
	Processes [][]string
}

// ExecCreate sets up a detached command in a running container and returns
// the exec instance's ID; nothing runs until ExecStart.
func (c *Client) ExecCreate(ctx context.Context, container string, o ExecCreate) (string, error) {
	if err := requireID("container", container); err != nil {
		return "", err
	}
	body := struct {
		AttachStdin  bool
		AttachStdout bool
		AttachStderr bool
		Cmd          []string
		User         string `json:",omitempty"`
	}{Cmd: o.Cmd, User: o.User}
	var out struct {
		ID string `json:"Id"`
	}
	_, err := c.call(ctx, request{method: http.MethodPost, path: "/containers/" + url.PathEscape(container) + "/exec", body: body}, &out)
	return out.ID, err
}

// ExecStart starts an exec instance detached; it returns once the command
// has started, not when it ends.
func (c *Client) ExecStart(ctx context.Context, id string) error {
	if err := requireID("exec", id); err != nil {
		return err
	}
	body := struct {
		Detach bool
		Tty    bool
	}{Detach: true}
	_, err := c.call(ctx, request{method: http.MethodPost, path: "/exec/" + url.PathEscape(id) + "/start", body: body}, nil)
	return err
}

// ExecInspect reads the state of an exec instance.
func (c *Client) ExecInspect(ctx context.Context, id string) (ExecInspect, error) {
	if err := requireID("exec", id); err != nil {
		return ExecInspect{}, err
	}
	var out ExecInspect
	_, err := c.call(ctx, request{method: http.MethodGet, path: "/exec/" + url.PathEscape(id) + "/json"}, &out)
	return out, err
}

// ContainerTop lists a running container's processes by running ps with
// psArgs on the daemon's host; an empty psArgs is the daemon's default.
func (c *Client) ContainerTop(ctx context.Context, container, psArgs string) (TopResult, error) {
	if err := requireID("container", container); err != nil {
		return TopResult{}, err
	}
	var q url.Values
	if psArgs != "" {
		q = url.Values{"ps_args": {psArgs}}
	}
	var out TopResult
	_, err := c.call(ctx, request{method: http.MethodGet, path: "/containers/" + url.PathEscape(container) + "/top", query: q}, &out)
	return out, err
}
