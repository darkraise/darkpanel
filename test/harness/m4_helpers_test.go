//go:build harness

package harness

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/test/harness/vm"
)

// m4SettleTimeout bounds how long a change may take to reach a terminal state.
const m4SettleTimeout = 20 * time.Minute

// m4MaxFrameLine bounds one line of a server-sent event stream.
const m4MaxFrameLine = 256 << 10

// m4PlanView is the part of a plan the scenarios read.
type m4PlanView struct {
	PlanID       string
	Confirmation string
	Diff         []core.DiffLine
	Guards       []core.Guard
}

// m4Container is one entry of the docker.containers resource.
type m4Container struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Image string `json:"image"`
	State string `json:"state"`
	Owner string `json:"owner"`
}

// m4Effect is one effect of a change as the effects route lists it.
type m4Effect struct {
	Seq      int    `json:"seq"`
	Resource string `json:"resource"`
	State    string `json:"state"`
	Reason   string `json:"reason"`
}

// m4UndoError is an undo that did not end reverted.
type m4UndoError struct{ Change Change }

func (e *m4UndoError) Error() string {
	return fmt.Sprintf("harness: undo of change %s ended %s, want reverted", e.Change.ID, e.Change.State)
}

// m4Plan plans version 1 of action on host with input. A refusal of the plan
// is returned wrapped, so errors.As still finds its *APIError.
func m4Plan(ctx context.Context, api *API, host, action string, input any) (m4PlanView, error) {
	path, err := actionPath(host, action+"/v1", "plan")
	if err != nil {
		return m4PlanView{}, err
	}
	var p struct {
		PlanID       string          `json:"planId"`
		Confirmation *string         `json:"confirmation"`
		Diff         []core.DiffLine `json:"diff"`
		Guards       []core.Guard    `json:"guards"`
	}
	if err := api.do(ctx, http.MethodPost, path, map[string]any{"input": input}, &p); err != nil {
		return m4PlanView{}, fmt.Errorf("plan %s: %w", action, err)
	}
	v := m4PlanView{PlanID: p.PlanID, Diff: p.Diff, Guards: p.Guards}
	if p.Confirmation != nil {
		v.Confirmation = *p.Confirmation
	}
	return v, nil
}

// m4xTerminal reports whether a change in state will not move on its own.
func m4xTerminal(state string) bool {
	switch state {
	case core.StateCompleted, core.StateFailed, core.StateReverted, core.StateNeedsRepair, core.StateInterrupted:
		return true
	}
	return false
}

// m4Settle polls change id until it reaches a terminal state and returns it.
func m4Settle(ctx context.Context, api *API, id string) (Change, error) {
	var ch Change
	err := eventually(ctx, m4SettleTimeout, func(ctx context.Context) error {
		c, err := api.m3Change(ctx, id)
		if err != nil {
			return err
		}
		if !m4xTerminal(c.State) {
			return fmt.Errorf("harness: change %s is %s", id, c.State)
		}
		ch = c
		return nil
	})
	return ch, err
}

// m4ApplyAsync plans version 1 of action on host, applies the plan with its
// confirmation, waits for the change to settle, and fails the test unless it
// completed. It serves long-running and synchronous actions alike.
func m4ApplyAsync(ctx context.Context, t *testing.T, api *API, host, action string, input any) Change {
	t.Helper()
	p, err := m4Plan(ctx, api, host, action, input)
	if err != nil {
		t.Fatal(err)
	}
	path, err := actionPath(host, action+"/v1", "apply")
	if err != nil {
		t.Fatal(err)
	}
	body := map[string]any{"planId": p.PlanID}
	if p.Confirmation != "" {
		body["confirmation"] = p.Confirmation
	}
	var applied Change
	if err := api.do(ctx, http.MethodPost, path, body, &applied); err != nil {
		t.Fatalf("apply %s: %v", action, err)
	}
	ch, err := m4Settle(ctx, api, applied.ID)
	if err != nil {
		t.Fatalf("change %s of %s: %v", applied.ID, action, err)
	}
	if ch.State != core.StateCompleted {
		detail := "no error recorded"
		if ch.Error != nil {
			detail = ch.Error.Code + ": " + ch.Error.Message
		}
		t.Fatalf("change %s of %s ended %s, want %s: %s", ch.ID, action, ch.State, core.StateCompleted, detail)
	}
	return ch
}

// m4xUndoSettled is nil for a reverted change and an *m4UndoError otherwise.
func m4xUndoSettled(ch Change) error {
	if ch.State == core.StateReverted {
		return nil
	}
	return &m4UndoError{Change: ch}
}

// m4Undo undoes change id, waits for the undo to settle, and returns an
// *m4UndoError unless the change ended reverted.
func m4Undo(ctx context.Context, api *API, changeID string) error {
	if err := api.do(ctx, http.MethodPost, "/api/v1/changes/"+url.PathEscape(changeID)+"/undo", nil, nil); err != nil {
		return fmt.Errorf("undo %s: %w", changeID, err)
	}
	ch, err := m4Settle(ctx, api, changeID)
	if err != nil {
		return fmt.Errorf("undo %s: %w", changeID, err)
	}
	return m4xUndoSettled(ch)
}

// m4Docker runs sudo docker with args on host and returns its output.
func m4Docker(ctx context.Context, t *testing.T, host *vm.VM, args ...string) string {
	t.Helper()
	return m3Run(ctx, t, host, "sudo docker "+m3dxShell(args...))
}

// m4Stream opens the stream module/name of host with params and returns the
// data of its first n events.
func m4Stream(ctx context.Context, api *API, host, module, name string, params url.Values, n int) ([]json.RawMessage, error) {
	path := "/api/v1/hosts/" + url.PathEscape(host) + "/streams/" + module + "/" + name
	if len(params) > 0 {
		path += "?" + params.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, api.base+path, nil)
	if err != nil {
		return nil, fmt.Errorf("harness: GET %s: %w", path, err)
	}
	req.Header.Set("Accept", "text/event-stream")
	resp, err := api.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("harness: GET %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, maxProblemBody))
		return nil, &APIError{Status: resp.StatusCode, Detail: string(b)}
	}
	return m4xFrames(resp.Body, n)
}

// m4xFrames reads server-sent events from r and returns the JSON data of the
// first n events named data. An end event before that, data that is not JSON
// and a stream that closes early are errors.
func m4xFrames(r io.Reader, n int) ([]json.RawMessage, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), m4MaxFrameLine)
	var (
		got   []json.RawMessage
		event string
		data  []string
	)
	for sc.Scan() {
		line := sc.Text()
		if line != "" {
			switch {
			case strings.HasPrefix(line, ":"), strings.HasPrefix(line, "id:"):
			case strings.HasPrefix(line, "event:"):
				event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			case strings.HasPrefix(line, "data:"):
				data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
			}
			continue
		}
		payload := strings.Join(data, "\n")
		name := event
		event, data = "", nil
		switch name {
		case "end":
			return got, fmt.Errorf("harness: the stream ended (%s) after %d of %d events", payload, len(got), n)
		case "data":
			if !json.Valid([]byte(payload)) {
				return got, fmt.Errorf("harness: stream event data %q is not JSON", payload)
			}
			got = append(got, json.RawMessage(payload))
			if len(got) >= n {
				return got, nil
			}
		}
	}
	if err := sc.Err(); err != nil {
		return got, fmt.Errorf("harness: reading the stream after %d of %d events: %w", len(got), n, err)
	}
	return got, fmt.Errorf("harness: the stream closed after %d of %d events", len(got), n)
}

// m4xAtLeast reports whether the major.minor version have is at least floor,
// comparing numerically; a string that is not two integers separated by a dot
// is below every floor.
func m4xAtLeast(have, floor string) bool {
	h, ok := m4xVersion(have)
	if !ok {
		return false
	}
	f, ok := m4xVersion(floor)
	if !ok {
		return false
	}
	return h[0] > f[0] || (h[0] == f[0] && h[1] >= f[1])
}

// m4xVersion parses a major.minor version string.
func m4xVersion(s string) ([2]uint64, bool) {
	major, minor, ok := strings.Cut(s, ".")
	if !ok {
		return [2]uint64{}, false
	}
	a, err := strconv.ParseUint(major, 10, 32)
	if err != nil {
		return [2]uint64{}, false
	}
	b, err := strconv.ParseUint(minor, 10, 32)
	if err != nil {
		return [2]uint64{}, false
	}
	return [2]uint64{a, b}, true
}

// m4Reveal runs the reveal of action, written without its version, on host
// with input and decodes the revealed value into out.
func m4Reveal(ctx context.Context, api *API, host, action string, input, out any) error {
	path, err := actionPath(host, action+"/v1", "reveal")
	if err != nil {
		return err
	}
	var r struct {
		Value json.RawMessage `json:"value"`
	}
	if err := api.do(ctx, http.MethodPost, path, map[string]any{"input": input}, &r); err != nil {
		return fmt.Errorf("reveal %s: %w", action, err)
	}
	if err := json.Unmarshal(r.Value, out); err != nil {
		return fmt.Errorf("harness: value of %s: %w", action, err)
	}
	return nil
}

// m4Release releases the needs_repair change id with reason.
func m4Release(ctx context.Context, api *API, changeID, reason string) error {
	path := "/api/v1/changes/" + url.PathEscape(changeID) + "/release"
	if err := api.do(ctx, http.MethodPost, path, map[string]any{"reason": reason}, nil); err != nil {
		return fmt.Errorf("release %s: %w", changeID, err)
	}
	return nil
}

// m4Effects lists the effects of change id.
func m4Effects(ctx context.Context, api *API, changeID string) ([]m4Effect, error) {
	var r struct {
		Effects []m4Effect `json:"effects"`
	}
	if err := api.do(ctx, http.MethodGet, "/api/v1/changes/"+url.PathEscape(changeID)+"/effects", nil, &r); err != nil {
		return nil, fmt.Errorf("effects of %s: %w", changeID, err)
	}
	return r.Effects, nil
}

// m4xContainers decodes the containers member of the docker.containers resource.
func m4xContainers(raw []byte) ([]m4Container, error) {
	var l struct {
		Containers []m4Container `json:"containers"`
	}
	if err := json.Unmarshal(raw, &l); err != nil {
		return nil, fmt.Errorf("harness: docker.containers: %w", err)
	}
	return l.Containers, nil
}

// m4xFindContainer returns the container named name.
func m4xFindContainer(cs []m4Container, name string) (m4Container, bool) {
	for _, c := range cs {
		if c.Name == name {
			return c, true
		}
	}
	return m4Container{}, false
}
