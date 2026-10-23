package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// maxEventLine bounds one line of the relay's server-sent events.
const maxEventLine = 1 << 20

// streamCommand builds `<module> <name> --follow` for a module stream (spec
// 1.6): its parameters as flags, the connection flags and --host. A stream
// has no --local: the agent socket serves no streams.
func (a *app) streamCommand(e entry) (*cobra.Command, error) {
	ms := *e.Stream
	fields, err := inputFields(ms.Def.ParamsType)
	if err != nil {
		return nil, err
	}
	if i := slices.IndexFunc(fields, func(f inputField) bool { return f.Flag == "follow" }); i >= 0 {
		return nil, fmt.Errorf("stream %s: parameter %s would take the CLI's own flag --follow", e.Name, fields[i].Name)
	}
	cmd := &cobra.Command{
		Use:   e.Path[len(e.Path)-1],
		Short: "Follow the " + e.Name + " stream of a host",
		Args:  cobra.NoArgs,
	}
	addInputFlags(cmd, fields)
	addConnFlags(cmd, false, true)
	cmd.Flags().Bool("follow", false, "print each event as one line of JSON until the stream ends (required)")
	cmd.RunE = func(cmd *cobra.Command, _ []string) error { return a.runFollow(cmd, ms, fields) }
	return cmd, nil
}

// runFollow reads the stream's relay and prints each event's JSON on its
// own line as it arrives. It succeeds when the stream ends with done; any
// other end is an error naming the reason.
func (a *app) runFollow(cmd *cobra.Command, ms core.ModuleStream, fields []inputField) error {
	if follow, _ := cmd.Flags().GetBool("follow"); !follow {
		return usagef("%s is a stream; run it with --follow", cmd.CommandPath())
	}
	input, err := collectInput(cmd, fields, a.term)
	if err != nil {
		return err
	}
	params, err := queryOf(input)
	if err != nil {
		return err
	}
	c, err := a.connect(cmd)
	if err != nil {
		return err
	}
	host, err := hostFor(cmd, c, core.SideAgent)
	if err != nil {
		return err
	}
	path := "/api/v1/hosts/" + url.PathEscape(host) + "/streams/" + url.PathEscape(ms.Module) + "/" + url.PathEscape(ms.Def.Name)
	if len(params) > 0 {
		path += "?" + params.Encode()
	}
	return c.follow(cmd.Context(), path, cmd.OutOrStdout())
}

// queryOf turns a stream's parameter object into query parameters: a
// scalar member is one value, an array member one value per element.
func queryOf(input json.RawMessage) (url.Values, error) {
	dec := json.NewDecoder(bytes.NewReader(input))
	dec.UseNumber()
	var obj map[string]any
	if err := dec.Decode(&obj); err != nil {
		return nil, fmt.Errorf("decode the stream parameters: %w", err)
	}
	q := url.Values{}
	for key, v := range obj {
		items, isList := v.([]any)
		if !isList {
			items = []any{v}
		}
		for _, item := range items {
			var s string
			switch item := item.(type) {
			case string:
				s = item
			case json.Number:
				s = item.String()
			case bool:
				s = strconv.FormatBool(item)
			default:
				return nil, usagef("stream parameter %q must be a string, number, boolean or a list of them", key)
			}
			q.Add(key, s)
		}
	}
	return q, nil
}

// streamingHTTP is hc without its overall timeout, which would cut every
// stream after requestTimeout; the wait for the answer's headers stays
// bounded by it.
func streamingHTTP(hc *http.Client) (*http.Client, error) {
	base := hc.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	tr, ok := base.(*http.Transport)
	if !ok {
		return nil, errors.New("cli: the HTTP transport is not an *http.Transport")
	}
	tr = tr.Clone()
	tr.ResponseHeaderTimeout = requestTimeout
	return &http.Client{Transport: tr, CheckRedirect: hc.CheckRedirect}, nil
}

// follow reads the server-sent events at path with its own streaming
// client and writes each data event to w as one line.
func (c *client) follow(ctx context.Context, path string, w io.Writer) error {
	hc, err := streamingHTTP(c.http)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return fmt.Errorf("GET %s: %w", path, err)
	}
	req.Header.Set("Accept", "text/event-stream")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("GET %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
		if err != nil {
			return fmt.Errorf("GET %s: read the answer: %w", path, err)
		}
		return answerError(http.MethodGet, path, resp, data)
	}
	return printEvents(resp.Body, w)
}

// printEvents parses server-sent events from r: it prints the data of each
// data event as one line to w, skips comments, and returns at the end
// event, nil when its reason is done.
func printEvents(r io.Reader, w io.Writer) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 4096), maxEventLine)
	var event string
	var data []string
	for sc.Scan() {
		line := sc.Text()
		if line != "" {
			field, value, _ := strings.Cut(line, ":")
			value = strings.TrimPrefix(value, " ")
			switch field {
			case "event":
				event = value
			case "data":
				data = append(data, value)
			}
			continue
		}
		payload := strings.Join(data, "\n")
		switch event {
		case "data":
			if !json.Valid([]byte(payload)) {
				return errors.New("the stream sent an event that is not JSON")
			}
			if _, err := fmt.Fprintln(w, payload); err != nil {
				return err
			}
		case "end":
			return streamEnded(payload)
		}
		event, data = "", nil
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("read the stream: %w", err)
	}
	return errors.New("the stream closed without an end event")
}

// streamEnded is the result of an end event: nil for done, else an error
// naming the reason and, for an error, the problem.
func streamEnded(payload string) error {
	var end struct {
		Reason string `json:"reason"`
		Error  *struct {
			Code   errcode.Code `json:"code"`
			Detail string       `json:"detail"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(payload), &end); err != nil {
		return errors.New("the stream sent an end event that is not JSON")
	}
	switch {
	case end.Reason == "done":
		return nil
	case end.Error != nil:
		return fmt.Errorf("the stream ended: %s: %s (%s)", end.Reason, end.Error.Detail, end.Error.Code)
	}
	return fmt.Errorf("the stream ended: %s", end.Reason)
}
